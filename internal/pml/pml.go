// Package pml reads PowerPoint presentations (PresentationML) into the
// shared document model: one part per slide, with what each shape says as
// headings, paragraphs, list items, tables and pictures, each with the
// place on the slide it was read from.
package pml

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
	"github.com/AOShei/go-fast-ooxml/internal/dml"
	"github.com/AOShei/go-fast-ooxml/internal/media"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/theme"
	"github.com/AOShei/go-fast-ooxml/internal/unit"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// HiddenChecked is the set of hidden-text reasons this reader checks
// completely: hidden slides and shapes and text with neither fill nor
// outline (invisible), text under two points after any shrinking to fit
// (tiny), text in the colour of what it is seen against (low_contrast),
// shapes that lie wholly outside the slide (off_page), text whose shape
// lies wholly under a later opaque rectangle or picture (occluded), text
// filled with zero opacity (transparent), and invisible characters. A
// slide with more overlapping shapes than the checks will work through
// says so with ooxml.hidden_unchecked. Text cut off by a box too small
// for it (clipped) is flagged where the file states it, and is not in
// this set.
const HiddenChecked = gofastcore.HiddenInvisible | gofastcore.HiddenTiny | gofastcore.HiddenLowContrast |
	gofastcore.HiddenOffPage | gofastcore.HiddenOccluded | gofastcore.HiddenTransparent | gofastcore.HiddenChars

// Config holds the limits that are this format's own.
type Config struct {
	// MaxCells bounds the table cells kept from a presentation, all
	// slides together.
	MaxCells int64
}

var scanOptions = &xmlscan.Options{Spaces: ns.Spaces}

// presentation is what the slides of a presentation share.
type presentation struct {
	pkg  *opc.Package
	opts *gofastcore.Options

	width, height float64        // the size of a slide in points; zero if the file does not say
	paperHeight   float64        // the height of a notes page or handout, in points; zero if not said
	masters       [2]string      // the notes master and the handout master, if there are
	first         int            // the number the first slide shows
	defaults      *dml.ListStyle // the text style under every other
	tablePart     string         // the part that defines table styles
	authorParts   []string       // the parts that name the authors of comments

	maxCells int64
	*shared
}

// shared is what is worked out as the slides need it and kept for the
// slides after: layouts and masters, themes, table styles, pictures. All
// of it is a function of the file alone, so which slide asks first makes
// no difference to the result.
type shared struct {
	cells    atomic.Int64 // table cells that may still be kept
	repeated atomic.Int64 // blocks of layouts and masters that may still be repeated on slides
	exceeded atomic.Bool  // the decompression budget ran out

	mu     sync.Mutex
	sheets map[string]*layout
	themes map[string]*themeEntry
	images map[string]*gofastcore.Image
	opaque map[string]bool // pictures: whether one hides what is behind it

	// What the notes master [0] and the handout master [1] put on every
	// page they make, read once, and the bytes of its text.
	paperOnce  [2]sync.Once
	paper      [2][]gofastcore.Block
	paperFrom  [2][]int8 // the placeholder of each block: header 0, footer 1, none -1
	paperBytes [2]int
	damaged    []string // parts that could not be read in full

	tableOnce  sync.Once
	tables     map[string]*tableStyle
	authorOnce sync.Once
	authors    map[string]string
}

func newShared(cells int64) *shared {
	sh := &shared{
		sheets: map[string]*layout{}, themes: map[string]*themeEntry{},
		images: map[string]*gofastcore.Image{}, opaque: map[string]bool{},
	}
	sh.cells.Store(cells)
	sh.repeated.Store(maxRepeated)
	return sh
}

type themeEntry struct {
	once   sync.Once
	scheme *theme.Scheme
}

// note records a failure to read a part that slides share.
func (p *presentation) note(name string, err error) {
	switch {
	case err == nil:
	case errors.Is(err, opc.ErrBudget):
		p.exceeded.Store(true)
	default:
		p.mu.Lock()
		p.damaged = append(p.damaged, name)
		p.mu.Unlock()
	}
}

// load reads a part that slides share.
func (p *presentation) load(name string, read func(*xmlscan.Scanner)) {
	if name == "" || !p.pkg.Has(name) || p.exceeded.Load() {
		return
	}
	var s xmlscan.Scanner
	c, err := p.pkg.Scan(name, &s, scanOptions)
	if err != nil {
		p.note(name, err)
		return
	}
	read(&s)
	c.Close()
	p.note(name, s.Err())
}

// theme returns the colours of the theme in the named part.
func (p *presentation) theme(name string) *theme.Scheme {
	if name == "" {
		return nil
	}
	p.mu.Lock()
	e := p.themes[name]
	if e == nil {
		e = &themeEntry{}
		p.themes[name] = e
	}
	p.mu.Unlock()
	e.once.Do(func() {
		p.load(name, func(s *xmlscan.Scanner) { e.scheme = theme.Read(s) })
	})
	return e.scheme
}

// image describes the picture in the named part. Its bytes, if asked
// for, are read once; which of the blocks that show it carries them is
// settled when the slides are put together.
//
// The error is opc.ErrBudget if the picture could not be read within the
// decompression budget.
func (p *presentation) image(name string) (*gofastcore.Image, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	img, ok := p.images[name]
	var err error
	if !ok {
		img, err = media.Image(p.pkg, name, p.opts.Images)
		if err == nil {
			p.images[name] = img // one that was cut short is read again
		}
	}
	return img, err
}

// maxRepeated bounds the blocks that slides take from their layouts and
// masters, all slides together. The text of a master is on every slide
// that uses it, so a small file with a long master and many slides asks
// for a large result; a presentation of any ordinary size is far below
// this.
const maxRepeated = 1 << 20

// slideRef is a slide as the presentation lists it.
type slideRef struct {
	target string // its part; empty if the presentation does not say
}

// maxSlides bounds the slides of a presentation that are listed at all.
const maxSlides = 1 << 16

// Deck is a presentation opened for reading a slide at a time. The list
// of slides and the presentation's own settings are read when it is
// opened; layouts, masters and whatever else slides share are read when
// a slide first needs them.
type Deck struct {
	p        *presentation
	refs     []slideRef
	after    int64 // the decompression budget once the presentation part was read
	external int   // links from the presentation to things outside the file
	pages    sync.Pool
}

// Open reads the presentation in main. It does not fail: what could not
// be read is reported by Summary, and a presentation with no slides has
// one part that says so.
func Open(pkg *opc.Package, main string, opts *gofastcore.Options, cfg Config) *Deck {
	p := &presentation{pkg: pkg, opts: opts, first: 1, maxCells: cfg.MaxCells, shared: newShared(cfg.MaxCells)}
	d := &Deck{p: p}
	d.pages.New = func() any { return &page{p: p} }

	rels, err := pkg.Rels(main)
	p.note(opc.RelsName(main), err)
	byID := make(map[string]opc.Rel, len(rels))
	for _, rel := range rels {
		byID[rel.ID] = rel
		switch {
		case rel.External:
			if !ns.IsRel(rel.Type, ns.RelHyperlink) {
				d.external++
			}
		case ns.IsRel(rel.Type, ns.RelTableStyles):
			p.tablePart = rel.Target
		case ns.IsRel(rel.Type, ns.RelCommentAuthors, ns.RelAuthors):
			p.authorParts = append(p.authorParts, rel.Target)
		case ns.IsRel(rel.Type, ns.RelNotesMaster) && p.masters[0] == "":
			p.masters[0] = rel.Target
		case ns.IsRel(rel.Type, ns.RelHandoutMaster) && p.masters[1] == "":
			p.masters[1] = rel.Target
		}
	}
	p.load(main, func(s *xmlscan.Scanner) {
		for s.Next() != xmlscan.EOF {
			if s.Kind() != xmlscan.Start {
				continue
			}
			switch {
			case s.Depth() == 1:
				if n, ok := intAttr(s, "firstSlideNum"); ok && n >= 0 && n < 1<<20 {
					p.first = int(n)
				}
			case s.Depth() != 2 && !(s.Depth() == 3 && s.Is(ns.Pres, "sldId")):
				s.Skip()
			case s.Is(ns.Pres, "sldId") && len(d.refs) < maxSlides:
				var ref slideRef
				if id, ok := s.Attr(ns.Rel, "id"); ok {
					if rel, found := byID[string(id)]; found && !rel.External {
						ref.target = rel.Target
					}
				}
				d.refs = append(d.refs, ref)
			case s.Is(ns.Pres, "sldSz"):
				cx, ok1 := intAttr(s, "cx")
				cy, ok2 := intAttr(s, "cy")
				if ok1 && ok2 && cx > 0 && cy > 0 && cx < 1<<40 && cy < 1<<40 {
					p.width, p.height = round(float64(cx)/dml.EMU), round(float64(cy)/dml.EMU)
				}
				s.Skip()
			case s.Is(ns.Pres, "notesSz"):
				if cy, ok := intAttr(s, "cy"); ok && cy > 0 && cy < 1<<40 {
					p.paperHeight = round(float64(cy) / dml.EMU)
				}
				s.Skip()
			case s.Is(ns.Pres, "defaultTextStyle"):
				p.defaults = dml.ReadListStyle(s)
			case s.Is(ns.Pres, "sldIdLst"):
				// Its slides are read as they come.
			default:
				s.Skip()
			}
		}
	})
	d.after = pkg.Budget()
	return d
}

// Len returns the number of parts: one for each slide, and one for a
// presentation that has none.
func (d *Deck) Len() int { return max(len(d.refs), 1) }

// Noun names the parts of a presentation.
func (d *Deck) Noun() string { return "slides" }

// HiddenChecked returns the reasons this reader checks completely.
func (d *Deck) HiddenChecked() gofastcore.Hidden { return HiddenChecked }

// Begin starts a pass over the presentation: the limits count from here.
func (d *Deck) Begin() {
	d.p.pkg.Restore(d.after)
	d.p.cells.Store(d.p.maxCells)
	d.p.repeated.Store(maxRepeated)
}

// Limited reports whether the decompression budget has run out.
func (d *Deck) Limited() bool { return d.p.exceeded.Load() }

// Read reads the slide with the given index into a part.
func (d *Deck) Read(ctx context.Context, index int) *unit.Result {
	if len(d.refs) == 0 {
		// A presentation with no slides, which a template may be. A
		// document has at least one part, and this one says it is empty.
		res := &unit.Result{Part: gofastcore.Part{Kind: gofastcore.PartSlide, Number: 1, Width: d.p.width, Height: d.p.height}}
		res.Part.Warn(gofastcore.SeverityInfo, gofastcore.WarnPartNoText, "the presentation has no slides")
		res.Part.MarkHidden()
		return res
	}
	pg := d.pages.Get().(*page)
	defer d.pages.Put(pg)
	return pg.slide(ctx, index, d.refs[index])
}

// Summary turns what was found while the slides were read into warnings
// about the presentation.
func (d *Deck) Summary(tallies []*unit.Tally) []gofastcore.Warning {
	var out []gofastcore.Warning
	add := func(severity gofastcore.Severity, code, message string) {
		out = append(out, gofastcore.Warning{Code: code, Severity: severity, Message: message})
	}
	unread := map[string]int{}
	d.p.mu.Lock()
	damaged := append([]string(nil), d.p.damaged...)
	d.p.mu.Unlock()
	external, programs := d.external, 0
	for _, t := range tallies {
		for kind, n := range t.Unread {
			unread[kind] += n
		}
		damaged = append(damaged, t.Damaged...)
		external += t.External
		programs += t.Programs
	}
	if programs > 0 {
		add(gofastcore.SeverityInfo, gofastcore.WarnInputActiveContent,
			strconv.Itoa(programs)+" actions that start a program or run a macro; none was run")
	}
	if external > 0 {
		add(gofastcore.SeverityWarning, gofastcore.WarnInputExternalReference,
			fmt.Sprintf("%d links to pictures, media or files outside the presentation; nothing was fetched", external))
	}
	if len(unread) > 0 {
		kinds := make([]string, 0, len(unread))
		for kind, n := range unread {
			kinds = append(kinds, strconv.Itoa(n)+" "+kind)
		}
		sort.Strings(kinds)
		add(gofastcore.SeverityWarning, warn.UnreadContent, strings.Join(kinds, ", "))
	}
	sort.Strings(damaged)
	for i, name := range damaged {
		if i == 0 || name != damaged[i-1] {
			add(gofastcore.SeverityWarning, warn.PartDamaged, clean.Label(d.p.pkg.Written(name)))
		}
	}
	if d.p.exceeded.Load() && len(tallies) == 0 {
		add(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the decompressed size limit was reached before any slide was read")
	}
	return out
}

// round rounds a length in points to the hundredth, which is finer than
// anything shown and keeps the numbers short.
func round(v float64) float64 {
	if v < 0 {
		return -round(-v)
	}
	return float64(int64(v*100+0.5)) / 100
}

func intAttr(s *xmlscan.Scanner, name string) (int64, bool) {
	v, ok := s.Attr(xmlscan.NoSpace, name)
	if !ok || len(v) == 0 || len(v) > 15 {
		return 0, false
	}
	neg := v[0] == '-'
	if neg {
		v = v[1:]
	}
	if len(v) == 0 {
		return 0, false
	}
	var n int64
	for _, c := range v {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	if neg {
		n = -n
	}
	return n, true
}

func isTrue(v []byte) bool { return string(v) == "1" || string(v) == "true" }
