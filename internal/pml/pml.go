// Package pml reads PowerPoint presentations (PresentationML) into the
// shared document model: one part per slide, with what each shape says as
// headings, paragraphs, list items, tables and pictures, each with the
// place on the slide it was read from.
package pml

import (
	"context"
	"errors"
	"fmt"
	"runtime"
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
	ctx  context.Context
	pkg  *opc.Package
	opts *gofastcore.Options

	width, height float64        // the size of a slide in points; zero if the file does not say
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

	mu      sync.Mutex
	sheets  map[string]*layout
	themes  map[string]*themeEntry
	images  map[string]*gofastcore.Image
	opaque  map[string]bool // pictures: whether one hides what is behind it
	damaged []string        // parts that could not be read in full

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

// result is what reading one slide gives.
type result struct {
	part     gofastcore.Part
	rels     []opc.Rel
	unread   map[string]int
	damaged  []string
	external int  // links to things outside the file that are not hyperlinks
	programs int  // actions that start a program or run a macro
	limited  bool // a limit cut the slide short
}

// Extract reads the presentation in main into doc. The error is one of
// the caller's making: no slide selected, or the context cancelled.
func Extract(ctx context.Context, pkg *opc.Package, main string, opts *gofastcore.Options, doc *gofastcore.Document, cfg Config) error {
	p := &presentation{ctx: ctx, pkg: pkg, opts: opts, first: 1, maxCells: cfg.MaxCells, shared: newShared(cfg.MaxCells)}
	doc.HiddenChecked = HiddenChecked

	rels, err := pkg.Rels(main)
	p.note(opc.RelsName(main), err)
	byID := make(map[string]opc.Rel, len(rels))
	external := 0
	for _, rel := range rels {
		byID[rel.ID] = rel
		switch {
		case rel.External:
			if !ns.IsRel(rel.Type, ns.RelHyperlink) {
				external++
			}
		case ns.IsRel(rel.Type, ns.RelTableStyles):
			p.tablePart = rel.Target
		case ns.IsRel(rel.Type, ns.RelCommentAuthors, ns.RelAuthors):
			p.authorParts = append(p.authorParts, rel.Target)
		}
	}
	var refs []slideRef
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
			case s.Is(ns.Pres, "sldId") && len(refs) < maxSlides:
				var ref slideRef
				if id, ok := s.Attr(ns.Rel, "id"); ok {
					if rel, found := byID[string(id)]; found && !rel.External {
						ref.target = rel.Target
					}
				}
				refs = append(refs, ref)
			case s.Is(ns.Pres, "sldSz"):
				cx, ok1 := intAttr(s, "cx")
				cy, ok2 := intAttr(s, "cy")
				if ok1 && ok2 && cx > 0 && cy > 0 && cx < 1<<40 && cy < 1<<40 {
					p.width, p.height = round(float64(cx)/dml.EMU), round(float64(cy)/dml.EMU)
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

	// The slides asked for, in presentation order. A slide's number is
	// its place in the presentation, hidden slides included.
	var selected []int
	for i := range refs {
		if opts.Parts.Contains(i + 1) {
			selected = append(selected, i)
		}
	}
	if len(refs) > 0 && len(selected) == 0 {
		return fmt.Errorf("gofastooxml: the presentation has %d slides, numbered from 1: %w", len(refs), gofastcore.ErrNoPartsSelected)
	}
	if limit := opts.Limits.MaxParts; limit > 0 && len(selected) > limit {
		doc.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			fmt.Sprintf("%d slides were asked for; the first %d are given", len(selected), limit))
		selected = selected[:limit]
	}

	// The slides, each by one worker.
	workers := opts.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	workers = min(workers, len(selected))
	results := make([]*result, len(selected))
	run := func(workers int) (limited bool) {
		if workers <= 1 {
			pg := &page{p: p}
			for k, i := range selected {
				results[k] = pg.slide(i, refs[i])
			}
		} else {
			var wg sync.WaitGroup
			var next atomic.Int64
			for w := 0; w < workers; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					pg := &page{p: p}
					for {
						k := int(next.Add(1)) - 1
						if k >= len(selected) {
							return
						}
						results[k] = pg.slide(selected[k], refs[selected[k]])
					}
				}()
			}
			wg.Wait()
		}
		for _, r := range results {
			limited = limited || r.limited
		}
		return limited || p.exceeded.Load()
	}
	budget, damaged := pkg.Budget(), len(p.damaged)
	if run(workers) && workers > 1 && ctx.Err() == nil {
		// A limit was reached, and which slides were read before it
		// depends on which worker got there first. What a limit leaves
		// out must not depend on the number of workers, so the slides are
		// read again, in order, against the same limits.
		pkg.Restore(budget)
		kept := p.damaged[:damaged:damaged]
		p.shared = newShared(p.maxCells)
		p.damaged = kept
		run(1)
	}

	// Put together in presentation order.
	unread := map[string]int{}
	embedded := map[string]bool{}
	delivered := map[string]bool{}
	programs := 0
	exceeded := p.exceeded.Load()
	all := p.damaged
	for _, r := range results {
		// A picture's bytes go with the first block that shows it.
		for i := range r.part.Blocks {
			img := r.part.Blocks[i].Image
			if img == nil || img.Data == nil {
				continue
			}
			if delivered[img.ID] {
				img.Data = nil
			}
			delivered[img.ID] = true
		}
		doc.Parts = append(doc.Parts, r.part)
		for kind, n := range r.unread {
			unread[kind] += n
		}
		all = append(all, r.damaged...)
		external += r.external
		programs += r.programs
		children, bad, err := media.Embedded(ctx, pkg, opts, r.rels, embedded, !exceeded)
		doc.Children = append(doc.Children, children...)
		all = append(all, bad...)
		if err != nil {
			exceeded = true
		}
	}
	if len(refs) == 0 {
		// A presentation with no slides, which a template may be. A
		// document has at least one part, and this one says it is empty.
		part := gofastcore.Part{Kind: gofastcore.PartSlide, Number: 1, Width: p.width, Height: p.height}
		part.Warn(gofastcore.SeverityInfo, gofastcore.WarnPartNoText, "the presentation has no slides")
		part.MarkHidden()
		doc.Parts = append(doc.Parts, part)
	}
	if programs > 0 {
		doc.Warn(gofastcore.SeverityInfo, gofastcore.WarnInputActiveContent,
			strconv.Itoa(programs)+" actions that start a program or run a macro; none was run")
	}
	if external > 0 {
		doc.Warn(gofastcore.SeverityWarning, gofastcore.WarnInputExternalReference,
			fmt.Sprintf("%d links to pictures, media or files outside the presentation; nothing was fetched", external))
	}
	if len(unread) > 0 {
		kinds := make([]string, 0, len(unread))
		for kind, n := range unread {
			kinds = append(kinds, strconv.Itoa(n)+" "+kind)
		}
		sort.Strings(kinds)
		doc.Warn(gofastcore.SeverityWarning, warn.UnreadContent, strings.Join(kinds, ", "))
	}
	sort.Strings(all)
	for i, name := range all {
		if i == 0 || name != all[i-1] {
			doc.Warn(gofastcore.SeverityWarning, warn.PartDamaged, clean.Label(pkg.Written(name)))
		}
	}
	if exceeded && len(results) == 0 {
		doc.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the decompressed size limit was reached before any slide was read")
	}
	return nil
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
