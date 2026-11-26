package vsd

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/dml"
	"github.com/AOShei/go-fast-ooxml/internal/media"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/order"
	"github.com/AOShei/go-fast-ooxml/internal/unit"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// points is the number of points to an inch, the unit Visio stores
// lengths in.
const points = 72.0

// placed is a shape that gave blocks, for putting them in reading order.
type placed struct {
	box        dml.Rect // where it stands for reading order: its box, or the start of a line
	boxed      bool
	first, end int // its blocks
	unit       int // the top-level shape it belongs to
}

// page reads one page of a drawing. It is reused from page to page.
type page struct {
	d    *drawing
	s    xmlscan.Scanner
	ref  *pageRef
	rels map[string]opc.Rel

	toPage        dml.Matrix // from the page's inches, y up, to points from the top
	width, height float64    // of the page in points; zero if not known

	blocks []gofastcore.Block
	placed []placed
	unit   int

	room     *gofastcore.TextBudget
	removed  int
	full     bool // the text budget is spent
	repeat   bool // the bound on text taken from masters left some out
	sawImage bool
	failure  error
	damaged  []string
	lost     warn.Lost
	unread   map[string]int
}

func (pg *page) reset(ref *pageRef, room *gofastcore.TextBudget) {
	pg.ref, pg.room = ref, room
	pg.blocks, pg.placed, pg.unit = pg.blocks[:0], pg.placed[:0], 0
	pg.removed, pg.full, pg.repeat, pg.sawImage, pg.failure = 0, false, false, false, nil
	pg.damaged, pg.unread = nil, nil
	pg.lost.Reset()
	pg.width, pg.height = round(ref.width*points), round(ref.height*points)
	pg.toPage = dml.Matrix{A: points, D: -points, F: ref.height * points}
	if pg.rels == nil {
		pg.rels = map[string]opc.Rel{}
	} else {
		clear(pg.rels)
	}
}

// read reads a page into a part.
func (pg *page) read(ctx context.Context, index int, ref *pageRef) *unit.Result {
	d := pg.d
	res := &unit.Result{}
	part := &res.Part
	part.Kind, part.Number = gofastcore.PartPage, index+1
	room := gofastcore.TextBudgetFrom(ctx)
	pg.reset(ref, room)
	part.Width, part.Height = pg.width, pg.height
	part.Name = pg.lost.Name(ref.name)

	switch {
	case ref.target == "" || !d.pkg.Has(ref.target):
		part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "the drawing lists the page but does not contain it")
	case d.exceeded.Load():
		part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "nothing was read before the limit")
		res.Limited = true
	case ctx.Err() != nil:
		// Cancelled: the caller gets the context's error, not this part.
	default:
		rels := pg.content(ref)
		res.Rels = rels
		for _, rel := range rels {
			if rel.External && !ns.IsRel(rel.Type, ns.RelHyperlink) {
				res.External++
			}
		}
		blocks := pg.arrange()
		blocks = pg.background(blocks, ref, part, res)
		blocks = pg.comments(blocks, ref.id)
		part.Blocks = blocks
		pg.blocks = pg.blocks[:0]
		if err := pg.failure; err != nil {
			switch {
			case errors.Is(err, xmlscan.ErrDepth), errors.Is(err, xmlscan.ErrTokenTooLarge):
				part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the page is cut off: "+clean.Label(err.Error()))
			default:
				part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "the page is cut off: "+clean.Label(err.Error()))
			}
		}
	}

	res.Damaged, res.Unread = pg.damaged, pg.unread
	if pg.full {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, warn.TextCut("page"))
		res.Limited = true
	}
	if d.exceeded.Load() {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the decompressed size limit was reached; the page is cut off there")
		res.Limited = true
	}
	if pg.repeat {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "text that shapes of the page take from their masters, or from background pages, is left out: the pages of the drawing repeat more of it than is kept")
		res.Limited = true
	}
	if pg.lost.Say(part) {
		res.Limited = true
	}
	hasText, hasImage := false, pg.sawImage
	for i := range part.Blocks {
		if part.Blocks[i].Kind == gofastcore.KindImage {
			hasImage = true
		} else {
			hasText = true
		}
	}
	if !hasText && !part.Has(gofastcore.WarnPartFailed) {
		if hasImage {
			part.Warn(gofastcore.SeverityWarning, gofastcore.WarnPartImageOnly, "")
		} else {
			part.Warn(gofastcore.SeverityInfo, gofastcore.WarnPartNoText, "")
		}
	}
	if pg.removed > 0 {
		part.Warn(gofastcore.SeverityInfo, gofastcore.WarnTextControlCharactersRemoved, strconv.Itoa(pg.removed))
	}
	part.Finish()
	return res
}

// content reads the shapes of the page part into pg.blocks and
// pg.placed, and returns the part's relationships.
func (pg *page) content(ref *pageRef) []opc.Rel {
	d := pg.d
	rels, err := d.pkg.RelsWith(ref.target, &pg.s)
	pg.fail(opc.RelsName(ref.target), err)
	for _, rel := range rels {
		pg.rels[rel.ID] = rel
	}
	c, err := d.pkg.Scan(ref.target, &pg.s, scanOptions)
	if err != nil {
		pg.fail(ref.target, err)
		return rels
	}
	s := &pg.s
	r := reader{s: s, lost: &pg.lost}
	var top []*shape
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Depth() == 1:
		case s.Is(ns.Visio, "Shapes") && s.Depth() == 2:
			top = append(top, r.shapes(0)...)
		default:
			s.Skip()
		}
	}
	if err := s.Err(); err != nil {
		if errors.Is(err, opc.ErrBudget) {
			d.exceeded.Store(true)
		} else {
			pg.failure = err
			pg.damaged = append(pg.damaged, ref.target)
		}
	}
	c.Close()
	for _, sh := range top {
		pg.unit++
		pg.walk(sh, pg.toPage, nil, 0)
	}
	return rels
}

// fail records that a part the page needs could not be read.
func (pg *page) fail(name string, err error) {
	switch {
	case err == nil:
	case errors.Is(err, opc.ErrBudget):
		pg.d.exceeded.Store(true)
	default:
		pg.damaged = append(pg.damaged, name)
	}
}

// view is a shape as it is shown: its own sheet, and the master's shape
// it takes whatever it does not set from.
type view struct {
	d      *drawing
	s      *shape
	base   *shape     // the master's shape; nil if none
	master *masterRef // the master it belongs to
}

// cell returns the value of a single cell, the shape's own or else its
// master's.
func (v view) cell(id cellID) (string, bool) {
	if x, ok := v.s.cells.get(id); ok {
		return x, true
	}
	if v.base != nil {
		return v.base.cells.get(id)
	}
	return "", false
}

// num returns the number a cell holds.
func (v view) num(id cellID) (float64, bool) {
	x, ok := v.cell(id)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(x, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) || math.Abs(f) > 1e7 {
		return 0, false
	}
	return f, true
}

// on reports whether a cell holds the value true.
func (v view) on(id cellID) bool {
	x, _ := v.cell(id)
	return x == "1" || strings.EqualFold(x, "true")
}

// sheets returns the sheets a row of a section is looked for in, in
// turn: the shape's, its master's, then the style its text takes after
// and the styles that style takes after. Only the text's sections are
// taken from styles.
func (v view) sheets(sec secID, buf *[maxStyleHops + 2]*shape) []*shape {
	out := buf[:0]
	out = append(out, v.s)
	if v.base != nil {
		out = append(out, v.base)
	}
	if sec != sChar && sec != sPara {
		return out
	}
	id := v.s.textStyle
	if id < 0 && v.base != nil {
		id = v.base.textStyle
	}
	for hops := 0; id >= 0 && hops < maxStyleHops; hops++ {
		st := v.d.styles[id]
		if st == nil {
			break
		}
		seen := false
		for _, o := range out {
			seen = seen || o == st
		}
		if seen {
			break
		}
		out = append(out, st)
		id = st.textStyle
	}
	return out
}

// rowCell returns a cell of the row with the given index of a section
// indexed by number, from the first sheet that sets it.
func (v view) rowCell(sec secID, ix int32, name string) (string, bool) {
	var buf [maxStyleHops + 2]*shape
	for _, sh := range v.sheets(sec, &buf) {
		r := sh.sections[sec].byIX(ix)
		if r == nil {
			if sh.sections[sec] != nil && sh.sections[sec].del {
				return "", false
			}
			continue
		}
		if r.del {
			return "", false
		}
		if x, ok := r.cell(name); ok {
			return x, true
		}
	}
	return "", false
}

// namedRows returns the rows of a section indexed by name as the shape
// has them: the master's in its order, each with the shape's own cells
// over the master's, then the shape's own; rows the shape deletes are
// left out.
func (v view) namedRows(sec secID) []row {
	own := v.s.sections[sec]
	var base *section
	if v.base != nil && (own == nil || !own.del) {
		base = v.base.sections[sec]
	}
	var out []row
	if base != nil {
		for _, r := range base.rows {
			if r.del {
				continue
			}
			mine := own.byName(r.name)
			if mine == nil {
				out = append(out, r)
				continue
			}
			if mine.del {
				continue
			}
			merged := row{name: r.name, ix: r.ix, cells: make(map[string]string, len(r.cells)+len(mine.cells))}
			for k, x := range r.cells {
				merged.cells[k] = x
			}
			for k, x := range mine.cells {
				merged.cells[k] = x
			}
			out = append(out, merged)
		}
	}
	if own != nil {
		for _, r := range own.rows {
			if r.del || (base != nil && base.byName(r.name) != nil) {
				continue
			}
			out = append(out, r)
		}
	}
	return out
}

// text returns the shape's text: its own, or else its master's, and
// whether it is the master's.
func (v view) text() ([]tok, bool) {
	if v.s.hasText || v.base == nil {
		return v.s.text, false
	}
	return v.base.text, v.base.hasText && len(v.base.text) > 0
}

// local returns the map from the shape's own space (inches from its
// lower left corner, y up) to that of what holds it, and whether the
// shape says where it is.
func (v view) local() (dml.Matrix, bool) {
	pinX, ok1 := v.num(cPinX)
	pinY, ok2 := v.num(cPinY)
	if !ok1 || !ok2 {
		return dml.Identity, false
	}
	w, _ := v.num(cWidth)
	h, _ := v.num(cHeight)
	locX, ok := v.num(cLocPinX)
	if !ok {
		locX = w / 2
	}
	locY, ok := v.num(cLocPinY)
	if !ok {
		locY = h / 2
	}
	angle, _ := v.num(cAngle)
	fx, fy := 1.0, 1.0
	if v.on(cFlipX) {
		fx = -1
	}
	if v.on(cFlipY) {
		fy = -1
	}
	sin, cos := math.Sincos(angle)
	m := dml.Matrix{A: 1, D: 1, E: -locX, F: -locY}
	m = m.Then(dml.Matrix{A: fx, D: fy})
	m = m.Then(dml.Matrix{A: cos, B: sin, C: -sin, D: cos})
	return m.Then(dml.Matrix{A: 1, D: 1, E: pinX, F: pinY}), true
}

// hidden reports whether the file declares the shape's text not shown:
// the shape hides its text, or every layer it is on that the page lists
// is switched off.
func (pg *page) hidden(v view) bool {
	if v.on(cHideText) {
		return true
	}
	member, ok := v.cell(cLayerMember)
	if !ok || member == "" || pg.ref.layers == nil {
		return false
	}
	found, shown := false, false
	for _, f := range strings.Split(member, ";") {
		ix, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil {
			continue
		}
		r := pg.ref.layers.byIX(int32(ix))
		if r == nil || r.del {
			continue
		}
		found = true
		if vis, ok := r.cell("Visible"); !ok || vis != "0" {
			shown = true
		}
	}
	return found && !shown
}

// walk reads a shape and the shapes of a group, in the space that parent
// maps to the page. master is the master the shape's group is an
// instance of, if it is.
func (pg *page) walk(sh *shape, parent dml.Matrix, master *masterRef, depth int) {
	if sh.del || depth > maxDepth {
		return
	}
	d := pg.d
	if sh.master >= 0 {
		master = d.master(sh.master)
	}
	v := view{d: d, s: sh, master: master}
	if master != nil {
		switch {
		case sh.masterShape >= 0:
			v.base = master.byID[sh.masterShape]
		case sh.master >= 0 && len(master.top) == 1:
			v.base = master.top[0]
		}
	}
	m, placedOK := v.local()
	m = m.Then(parent)
	var box dml.Rect
	boxed := false
	if placedOK && pg.height > 0 {
		w, _ := v.num(cWidth)
		h, _ := v.num(cHeight)
		box, _, boxed = m.Bounds(w, h)
		box = roundRect(box)
	}
	at, atBoxed := box, boxed
	if bx, ok1 := v.num(cBeginX); ok1 && boxed {
		if by, ok2 := v.num(cBeginY); ok2 {
			if _, ok3 := v.num(cEndX); ok3 {
				// A line or connector stands at its start for reading order.
				x, y := parent.Apply(bx, by)
				at = roundRect(dml.Rect{X0: x, Y0: y, X1: x, Y1: y})
			}
		}
	}
	first := len(pg.blocks)
	if !pg.full {
		pg.picture(v, box, boxed)
		pg.shapeText(v, box, boxed, pg.hidden(v))
	}
	if len(pg.blocks) > first {
		pg.placed = append(pg.placed, placed{box: at, boxed: atBoxed, first: first, end: len(pg.blocks), unit: pg.unit})
	}
	for _, child := range sh.children {
		pg.walk(child, m, master, depth+1)
	}
}

// picture adds the picture a shape shows, if it is one.
func (pg *page) picture(v view, box dml.Rect, boxed bool) {
	id, rels := v.s.foreign, pg.rels
	if id == "" && v.base != nil && v.master != nil {
		id, rels = v.base.foreign, v.master.rels
	}
	if id == "" {
		return
	}
	rel, ok := rels[id]
	if !ok || rel.External || !pg.d.pkg.Has(rel.Target) {
		return
	}
	if !ns.IsRel(rel.Type, ns.RelImage) && !strings.HasPrefix(pg.d.pkg.ContentType(rel.Target), "image/") {
		return // an embedded object, which is handed on as a child
	}
	img, err := pg.d.image(rel.Target)
	if err != nil {
		pg.fail(rel.Target, err)
		return
	}
	if img == nil {
		return
	}
	pg.sawImage = true
	cp := *img
	b := gofastcore.Block{Kind: gofastcore.KindImage, Image: &cp}
	if boxed {
		b.Loc.BBox = gofastcore.Rect(box)
	}
	pg.blocks = append(pg.blocks, b)
}

// image describes the picture in the named part, read once.
func (d *drawing) image(name string) (*gofastcore.Image, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	img, ok := d.images[name]
	var err error
	if !ok {
		img, err = media.Image(d.pkg, name, d.opts.Images)
		if err == nil {
			d.images[name] = img
		}
	}
	return img, err
}

// arrange returns the page's blocks in reading order: by where their
// top-level shapes are on the page, a group read as one thing at the
// place of the group, and the shapes inside it in the same order.
func (pg *page) arrange() []gofastcore.Block {
	if len(pg.blocks) == 0 {
		return nil
	}
	var units, members []order.Item
	for i := 0; i < len(pg.placed); {
		j := i
		u := order.Item{First: len(members)}
		boxed := false
		for ; j < len(pg.placed) && pg.placed[j].unit == pg.placed[i].unit; j++ {
			sh := &pg.placed[j]
			members = append(members, order.Item{Box: sh.box, Rank: order.Rank(sh.box, sh.boxed, pg.width, pg.height), First: sh.first, End: sh.end})
			if sh.boxed {
				if !boxed {
					u.Box, boxed = sh.box, true
				} else {
					u.Box = dml.Rect{X0: min(u.Box.X0, sh.box.X0), Y0: min(u.Box.Y0, sh.box.Y0), X1: max(u.Box.X1, sh.box.X1), Y1: max(u.Box.Y1, sh.box.Y1)}
				}
			}
		}
		u.End, u.Rank = len(members), order.Rank(u.Box, boxed, pg.width, pg.height)
		units = append(units, u)
		i = j
	}
	order.Sort(units)
	out := make([]gofastcore.Block, 0, len(pg.blocks))
	for _, u := range units {
		inside := members[u.First:u.End]
		if len(inside) > 1 {
			order.Sort(inside)
		}
		for _, m := range inside {
			out = append(out, pg.blocks[m.First:m.End]...)
		}
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

func roundRect(r dml.Rect) dml.Rect {
	return dml.Rect{X0: round(r.X0), Y0: round(r.Y0), X1: round(r.X1), Y1: round(r.Y1)}
}
