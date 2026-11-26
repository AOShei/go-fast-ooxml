// Package vsd reads Visio drawings (Visio 2013 and later: .vsdx, .vsdm,
// templates and stencils) into the shared document model: one part per
// page, and per master of a stencil, with what each shape says as
// paragraphs, list items, pictures and a table of its data, each with the
// place on the page it was read from; the drawing's data recordsets are
// tables of the stored part.
package vsd

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/stored"
	"github.com/AOShei/go-fast-ooxml/internal/unit"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// Config holds the limits that are this format's own.
type Config struct {
	// MaxCells bounds the table cells kept from a drawing, all pages
	// together.
	MaxCells int64
	// Stencil says the file is a stencil: its masters are parts, after
	// its pages.
	Stencil bool
}

var scanOptions = &xmlscan.Options{Spaces: ns.Spaces}

// The relationships of a Visio package, by the last segment of their type.
const (
	relPages      = "pages"
	relPage       = "page"
	relMasters    = "masters"
	relMaster     = "master"
	relComments   = "comments"
	relRecordsets = "recordsets"
	relRecordset  = "recordset"
)

// pageRef is a page as the drawing lists it.
type pageRef struct {
	id            int64
	name          string
	target        string // its part; empty if the list does not say
	background    bool
	backPage      int64   // the background page behind it; -1 if none
	width, height float64 // in inches; zero if not said
	layers        *section

	// A master of a stencil, read as a page.
	master bool
	prompt string // what the stencil says of it
	hidden bool   // the stencil does not show it
}

// masterRef is a master as the drawing lists it, and what was read of
// it when a page first needed it.
type masterRef struct {
	target string
	ref    pageRef // as a page, for a stencil

	once sync.Once
	top  []*shape           // its shapes
	byID map[int64]*shape   // every shape of it, groups and all, by ID
	rels map[string]opc.Rel // its relationships: the pictures its shapes show
	lost warn.Lost          // what the bounds left out of it
}

// drawing is what the pages of a drawing share.
type drawing struct {
	pkg  *opc.Package
	opts *gofastcore.Options

	pages   []pageRef
	byID    map[int64]int // page ID to its index in pages
	masters map[int64]*masterRef
	order   []*masterRef // as the drawing lists them
	sets    []recordset  // the data recordsets
	styles  map[int64]*shape
	comment string // the comments part

	maxCells int64
	cells    atomic.Int64 // table cells that may still be kept
	repeated atomic.Int64 // blocks that shapes may still take from masters and background pages
	exceeded atomic.Bool  // the decompression budget ran out

	mu      sync.Mutex
	damaged []string // parts that could not be read in full
	lost    warn.Lost

	backs map[int64]*backEntry

	images map[string]*gofastcore.Image // pictures read, by part

	commentOnce sync.Once
	comments    map[int64][]comment // by page ID
}

// maxRepeated bounds the blocks that shapes take from their masters, and
// pages from their background pages, all pages together. A master's text
// is on every shape that is an instance of it, so a small file with a
// long master and many instances asks for a large result; a drawing of
// any ordinary size is far below this.
const maxRepeated = 1 << 20

// note records a failure to read a part that pages share.
func (d *drawing) note(name string, err error) {
	switch {
	case err == nil:
	case errors.Is(err, opc.ErrBudget):
		d.exceeded.Store(true)
	default:
		d.mu.Lock()
		d.damaged = append(d.damaged, name)
		d.mu.Unlock()
	}
}

// load reads a part that pages share.
func (d *drawing) load(name string, read func(*xmlscan.Scanner)) {
	if name == "" || !d.pkg.Has(name) || d.exceeded.Load() {
		return
	}
	var s xmlscan.Scanner
	c, err := d.pkg.Scan(name, &s, scanOptions)
	if err != nil {
		d.note(name, err)
		return
	}
	read(&s)
	c.Close()
	d.note(name, s.Err())
}

// master returns the master with the given ID, read; nil if the drawing
// has no such master.
func (d *drawing) master(id int64) *masterRef {
	m := d.masters[id]
	if m == nil {
		return nil
	}
	m.once.Do(func() {
		m.byID = map[int64]*shape{}
		m.rels = map[string]opc.Rel{}
		if m.target != "" && !d.exceeded.Load() {
			rels, err := d.pkg.Rels(m.target)
			d.note(opc.RelsName(m.target), err)
			for _, rel := range rels {
				m.rels[rel.ID] = rel
			}
		}
		d.load(m.target, func(s *xmlscan.Scanner) {
			r := reader{s: s, lost: &m.lost}
			for s.Next() != xmlscan.EOF {
				if s.Kind() == xmlscan.Start && s.Is(ns.Visio, "Shapes") && s.Depth() == 2 {
					m.top = append(m.top, r.shapes(0)...)
				}
			}
		})
		var index func([]*shape)
		index = func(list []*shape) {
			for _, sh := range list {
				if sh.id >= 0 {
					if _, dup := m.byID[sh.id]; !dup {
						m.byID[sh.id] = sh
					}
				}
				index(sh.children)
			}
		}
		index(m.top)
		d.lost.Merge(&m.lost)
	})
	return m
}

// Drawing is a Visio drawing opened for reading a page at a time. The
// list of pages and masters, and the styles, are read when it is opened;
// masters are read when a page first needs them.
type Drawing struct {
	d        *drawing
	parts    []int // the pages that are parts, by index in d.pages
	after    int64 // the decompression budget once the lists were read
	external int   // links from the drawing to things outside the file
	pool     sync.Pool
	extra    stored.Source // the VBA project and custom XML data
}

// Open reads the drawing in main. It does not fail: what could not be
// read is reported by Summary, and a drawing with no pages has one part
// that says so.
func Open(pkg *opc.Package, main string, opts *gofastcore.Options, cfg Config) *Drawing {
	d := &drawing{
		pkg: pkg, opts: opts, maxCells: cfg.MaxCells,
		byID: map[int64]int{}, masters: map[int64]*masterRef{}, styles: map[int64]*shape{},
		backs: map[int64]*backEntry{}, images: map[string]*gofastcore.Image{},
	}
	d.cells.Store(cfg.MaxCells)
	d.repeated.Store(maxRepeated)
	dr := &Drawing{d: d}
	dr.pool.New = func() any { return &page{d: d} }

	rels, err := pkg.Rels(main)
	d.note(opc.RelsName(main), err)
	dr.extra = stored.Find(pkg, rels)
	var pagesPart, mastersPart, setsPart string
	for _, rel := range rels {
		switch {
		case rel.External:
			if !ns.IsRel(rel.Type, ns.RelHyperlink) {
				dr.external++
			}
		case ns.IsRel(rel.Type, relPages) && pagesPart == "":
			pagesPart = rel.Target
		case ns.IsRel(rel.Type, relMasters) && mastersPart == "":
			mastersPart = rel.Target
		case ns.IsRel(rel.Type, relComments) && d.comment == "":
			d.comment = rel.Target
		case ns.IsRel(rel.Type, relRecordsets) && setsPart == "":
			setsPart = rel.Target
		}
	}
	d.load(main, d.readStyles)
	if mastersPart != "" {
		d.readMasters(mastersPart)
	}
	if pagesPart != "" {
		d.readPages(pagesPart)
	}

	if setsPart != "" {
		d.readRecordsets(setsPart)
	}

	dr.parts = d.parts()
	if cfg.Stencil {
		// A stencil's masters are what it is for: each is a part, after
		// the pages.
		for _, m := range d.order {
			dr.parts = append(dr.parts, len(d.pages))
			d.pages = append(d.pages, m.ref)
		}
	}
	dr.after = pkg.Budget()
	return dr
}

// parts returns the pages that are parts, by index: the foreground pages
// in order, then the background pages that no page has behind it, which
// a reader can still open, in the order the drawing lists them. A
// background page behind one of those is shown on it, and is not a part
// of its own; of background pages that are only behind each other, in a
// loop, the first listed is.
func (d *drawing) parts() []int {
	n := len(d.pages)
	behind := func(i int) int { // the index of the page behind page i, or -1
		if b := d.pages[i].backPage; b >= 0 && b != d.pages[i].id {
			if j, ok := d.byID[b]; ok {
				return j
			}
		}
		return -1
	}
	// left: background pages not yet shown on a page or made a part.
	// before: of those, how many have each page behind them.
	left := make([]bool, n)
	before := make([]int, n)
	var queue []int
	take := func(i int) {
		left[i] = false
		if j := behind(i); j >= 0 && left[j] {
			if before[j]--; before[j] == 0 {
				queue = append(queue, j)
			}
		}
	}
	show := func(i int) { // page i is read: the pages behind it are shown on it
		for j, hops := behind(i), 0; j >= 0 && left[j] && hops < maxBackPages; hops++ {
			take(j)
			j = behind(j)
		}
	}
	var parts, extra []int
	for i := range d.pages {
		left[i] = d.pages[i].background
	}
	for i := range d.pages {
		if !d.pages[i].background {
			parts = append(parts, i)
			show(i)
		}
	}
	for i := range d.pages {
		if j := behind(i); left[i] && j >= 0 && left[j] {
			before[j]++
		}
	}
	for i := range d.pages {
		if left[i] && before[i] == 0 {
			queue = append(queue, i)
		}
	}
	next := 0 // where to look for the first page of a loop
	for {
		for len(queue) > 0 {
			i := queue[0]
			queue = queue[1:]
			if !left[i] {
				continue
			}
			extra = append(extra, i)
			take(i)
			show(i)
		}
		for next < n && !left[next] {
			next++
		}
		if next == n {
			break
		}
		queue = append(queue, next) // the first page of a loop
	}
	sort.Ints(extra)
	return append(parts, extra...)
}

// readStyles reads the style sheets of the document part: what the text
// of a shape takes after when neither it nor its master says.
func (d *drawing) readStyles(s *xmlscan.Scanner) {
	r := reader{s: s, lost: &d.lost}
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Depth() == 1:
		case s.Is(ns.Visio, "StyleSheets") && s.Depth() == 2:
		case s.Is(ns.Visio, "StyleSheet") && s.Depth() == 3:
			st := newShape()
			st.id = intAttr(s, "ID", -1)
			st.textStyle = intAttr(s, "TextStyle", -1)
			r.body(st, 0)
			if st.id >= 0 && len(d.styles) < maxShapes {
				d.styles[st.id] = st
			}
		default:
			s.Skip()
		}
	}
}

// readMasters reads the list of masters.
func (d *drawing) readMasters(name string) {
	rels, err := d.pkg.Rels(name)
	d.note(opc.RelsName(name), err)
	byRel := make(map[string]string, len(rels))
	for _, rel := range rels {
		if !rel.External && ns.IsRel(rel.Type, relMaster) {
			byRel[rel.ID] = rel.Target
		}
	}
	d.load(name, func(s *xmlscan.Scanner) {
		r := reader{s: s, lost: &d.lost}
		for s.Next() != xmlscan.EOF {
			if s.Kind() != xmlscan.Start {
				continue
			}
			switch {
			case s.Depth() == 1:
			case s.Is(ns.Visio, "Master") && s.Depth() == 2:
				id := intAttr(s, "ID", -1)
				m := &masterRef{ref: pageRef{id: -1, backPage: -1, master: true}}
				if v, ok := s.Attr(xmlscan.NoSpace, "Name"); ok && len(v) > 0 {
					m.ref.name = string(v)
				} else if v, ok := s.Attr(xmlscan.NoSpace, "NameU"); ok {
					m.ref.name = string(v)
				}
				if v, ok := s.Attr(xmlscan.NoSpace, "Prompt"); ok {
					m.ref.prompt = string(v)
				}
				if v, ok := s.Attr(xmlscan.NoSpace, "Hidden"); ok {
					m.ref.hidden = isTrue(v)
				}
				d2 := s.Depth()
				for s.Next() != xmlscan.EOF && s.Depth() >= d2 {
					if s.Kind() != xmlscan.Start || s.Depth() != d2+1 {
						continue
					}
					switch {
					case s.Is(ns.Visio, "PageSheet"):
						sheet := newShape()
						r.body(sheet, 0)
						m.ref.width = inches(sheet.cells.get(cPageWidth))
						m.ref.height = inches(sheet.cells.get(cPageHeight))
						m.ref.layers = sheet.sections[sLayer]
					case s.Is(ns.Visio, "Rel"):
						if rid, ok := s.Attr(ns.Rel, "id"); ok {
							m.target = byRel[string(rid)]
						}
						s.Skip()
					default:
						s.Skip()
					}
				}
				m.ref.target = m.target
				if id >= 0 && m.target != "" && len(d.masters) < maxShapes {
					if _, dup := d.masters[id]; !dup {
						d.masters[id] = m
						d.order = append(d.order, m)
					}
				}
			default:
				s.Skip()
			}
		}
	})
}

// readPages reads the list of pages.
func (d *drawing) readPages(name string) {
	rels, err := d.pkg.Rels(name)
	d.note(opc.RelsName(name), err)
	byRel := make(map[string]string, len(rels))
	for _, rel := range rels {
		if !rel.External && ns.IsRel(rel.Type, relPage) {
			byRel[rel.ID] = rel.Target
		}
	}
	d.load(name, func(s *xmlscan.Scanner) {
		r := reader{s: s, lost: &d.lost}
		for s.Next() != xmlscan.EOF {
			if s.Kind() != xmlscan.Start {
				continue
			}
			switch {
			case s.Depth() == 1:
			case s.Is(ns.Visio, "Page") && s.Depth() == 2:
				if len(d.pages) >= warn.MaxSlides {
					d.lost.Add(warn.CapPages, 1)
					s.Skip()
					continue
				}
				pg := pageRef{id: intAttr(s, "ID", -1), backPage: intAttr(s, "BackPage", -1)}
				if v, ok := s.Attr(xmlscan.NoSpace, "Name"); ok && len(v) > 0 {
					pg.name = string(v)
				} else if v, ok := s.Attr(xmlscan.NoSpace, "NameU"); ok {
					pg.name = string(v)
				}
				if v, ok := s.Attr(xmlscan.NoSpace, "Background"); ok {
					pg.background = isTrue(v)
				}
				d2 := s.Depth()
				for s.Next() != xmlscan.EOF && s.Depth() >= d2 {
					if s.Kind() != xmlscan.Start || s.Depth() != d2+1 {
						continue
					}
					switch {
					case s.Is(ns.Visio, "PageSheet"):
						sheet := newShape()
						r.body(sheet, 0)
						pg.width = inches(sheet.cells.get(cPageWidth))
						pg.height = inches(sheet.cells.get(cPageHeight))
						pg.layers = sheet.sections[sLayer]
					case s.Is(ns.Visio, "Rel"):
						if rid, ok := s.Attr(ns.Rel, "id"); ok {
							pg.target = byRel[string(rid)]
						}
						s.Skip()
					default:
						s.Skip()
					}
				}
				if _, dup := d.byID[pg.id]; !dup && pg.id >= 0 {
					d.byID[pg.id] = len(d.pages)
				}
				d.pages = append(d.pages, pg)
			default:
				s.Skip()
			}
		}
	})
}

// inches returns the length a cell holds, in inches, if it holds a
// sensible one; zero otherwise.
func inches(v string, ok bool) float64 {
	if !ok {
		return 0
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || !(f > 0 && f < 1e5) {
		return 0
	}
	return f
}

// Len returns the number of parts: one for each page shown, and one for a
// drawing that has none; then the stored part, if there is one.
func (dr *Drawing) Len() int {
	if i := dr.Stored(); i >= 0 {
		return i + 1
	}
	return max(len(dr.parts), 1)
}

// Stored returns the index of the stored part, or -1 if the drawing has
// nothing for one: what it stores and shows on no page is its data
// recordsets, the code of its VBA project and custom XML data.
func (dr *Drawing) Stored() int {
	if dr.extra.Empty() && len(dr.d.sets) == 0 {
		return -1
	}
	return max(len(dr.parts), 1)
}

// Noun names the parts of a drawing.
func (dr *Drawing) Noun() string { return "pages" }

// Begin starts a pass over the drawing: the limits count from here.
func (dr *Drawing) Begin() {
	dr.d.pkg.Restore(dr.after)
	dr.d.cells.Store(dr.d.maxCells)
	dr.d.repeated.Store(maxRepeated)
}

// Limited reports whether the decompression budget has run out.
func (dr *Drawing) Limited() bool { return dr.d.exceeded.Load() }

// Read reads the page with the given index into a part.
func (dr *Drawing) Read(ctx context.Context, index int) *unit.Result {
	d := dr.d
	if index == dr.Stored() {
		p := stored.NewPart(ctx, index+1)
		d.recordsets(ctx, p)
		dr.extra.Macros(ctx, d.pkg, p)
		dr.extra.XML(ctx, d.pkg, p)
		dr.extra.AfterEnd(d.pkg, p)
		res := &unit.Result{}
		res.Limited = p.Finish()
		res.Part, res.Damaged = p.Part, p.Damaged
		return res
	}
	if len(dr.parts) == 0 {
		res := &unit.Result{Part: gofastcore.Part{Kind: gofastcore.PartPage, Number: 1}}
		res.Part.Warn(gofastcore.SeverityInfo, gofastcore.WarnPartNoText, "the drawing has no pages")
		res.Part.Finish()
		return res
	}
	pg := dr.pool.Get().(*page)
	defer dr.pool.Put(pg)
	return pg.read(ctx, index, &d.pages[dr.parts[index]])
}

// Summary turns what was found while the pages were read into warnings
// about the drawing.
func (dr *Drawing) Summary(tallies []*unit.Tally) []gofastcore.Warning {
	d := dr.d
	var out []gofastcore.Warning
	add := func(severity gofastcore.Severity, code, message string) {
		out = append(out, gofastcore.Warning{Code: code, Severity: severity, Message: message})
	}
	unread := map[string]int{}
	d.mu.Lock()
	damaged := append([]string(nil), d.damaged...)
	d.mu.Unlock()
	external := dr.external
	for _, t := range tallies {
		for kind, n := range t.Unread {
			unread[kind] += n
		}
		damaged = append(damaged, t.Damaged...)
		external += t.External
	}
	out = append(out, d.lost.Warnings()...)
	out = append(out, warn.Package(d.pkg)...)
	if external > 0 {
		add(gofastcore.SeverityWarning, gofastcore.WarnInputExternalReference,
			fmt.Sprintf("%d links to pictures, data or files outside the drawing; nothing was fetched", external))
	}
	if len(unread) > 0 {
		kinds := make([]string, 0, len(unread))
		for kind, n := range unread {
			kinds = append(kinds, strconv.Itoa(n)+" "+kind)
		}
		sort.Strings(kinds)
		add(gofastcore.SeverityWarning, gofastcore.WarnInputUnreadContent, strings.Join(kinds, ", "))
	}
	sort.Strings(damaged)
	var names warn.Names
	for _, name := range damaged {
		names.AddOnce(clean.Label(d.pkg.Written(name)))
	}
	if msg := names.Message(warn.DamagedOne, warn.DamagedMany); msg != "" {
		add(gofastcore.SeverityWarning, gofastcore.WarnInputStreamDamaged, msg)
	}
	if d.exceeded.Load() && len(tallies) == 0 {
		add(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the decompressed size limit was reached before any page was read")
	}
	return out
}
