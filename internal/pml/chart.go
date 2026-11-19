package pml

import (
	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/chart"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/diagram"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
)

// chart reads the chart a graphic frame shows (c:chart or cx:chart, whose
// Start token is current): a caption with its title and tables of its
// data, at the frame's place. A layout's or master's chart is not read.
func (pg *page) chart(sh *props, g *group) {
	s := &pg.s
	v, ok := s.Attr(ns.Rel, "id")
	id := string(v) // copied before Skip reuses the scanner's buffer
	s.Skip()
	if pg.role != roleSlide {
		return
	}
	rel, found := pg.rels[id]
	if !ok || !found || rel.External || !ns.IsRel(rel.Type, ns.RelChart, ns.RelChartEx) {
		// The chart is not in the file: the reference to it is broken.
		pg.damaged = append(pg.damaged, "the chart "+clean.Label(id))
		return
	}
	if !pg.p.pkg.Has(rel.Target) {
		pg.damaged = append(pg.damaged, rel.Target)
		return
	}
	c, err := chart.Read(pg.p.pkg, rel.Target)
	pg.fail(rel.Target, err, false)
	if c == nil {
		return
	}
	pg.text.Removed += c.Removed
	pg.charts = append(pg.charts, c.Rels...)
	pg.place(sh, g)
	at := len(pg.blocks)
	blocks, taken, cut := c.Blocks(pg.text.Budget, sh.name)
	pg.text.Taken += taken
	for i := range blocks {
		blocks[i].Hidden |= sh.reasons
		blocks[i].Loc.BBox = bbox(sh)
	}
	pg.blocks = append(pg.blocks, blocks...)
	if c.Cut && pg.cut == "" {
		pg.cut = "a chart holds more points than are read"
	}
	if cut {
		s.Abort(warn.ErrText)
	}
	if len(pg.blocks) > at {
		pg.add(sh, g, at, false, false, "")
	}
}

// diagram reads the SmartArt diagram a graphic frame shows (dgm:relIds,
// whose Start token is current): the text of its nodes as list items,
// each at the shape the drawing shows it in, or at the frame. A layout's
// or master's diagram is not read.
func (pg *page) diagram(sh *props, g *group) {
	s := &pg.s
	v, ok := s.Attr(ns.Rel, "dm")
	id := string(v) // copied before Skip reuses the scanner's buffer
	s.Skip()
	if pg.role != roleSlide {
		return
	}
	rel, found := pg.rels[id]
	if !ok || !found || rel.External || !ns.IsRel(rel.Type, ns.RelDiagram) {
		pg.damaged = append(pg.damaged, "the diagram "+clean.Label(id))
		return
	}
	if !pg.p.pkg.Has(rel.Target) {
		pg.damaged = append(pg.damaged, rel.Target)
		return
	}
	d, err := diagram.Read(pg.p.pkg, rel.Target, pg.rels, &pg.pal)
	pg.fail(rel.Target, err, false)
	if d == nil {
		return
	}
	pg.text.Removed += d.Removed
	pg.place(sh, g)
	frame := sh.xfrm.Local().Then(g.m)
	at := len(pg.blocks)
	blocks, taken, cut := d.Blocks(pg.text.Budget)
	pg.text.Taken += taken
	for i := range blocks {
		blocks[i].Hidden |= sh.reasons
		blocks[i].Loc.BBox = bbox(sh)
		if box := d.Items[i].Box; box.Set && sh.xfrm.Set {
			if r, _, ok := box.Local().Then(frame).Bounds(box.W, box.H); ok {
				blocks[i].Loc.BBox = gofastcore.Rect{X0: round(r.X0), Y0: round(r.Y0), X1: round(r.X1), Y1: round(r.Y1)}
			}
		}
	}
	pg.blocks = append(pg.blocks, blocks...)
	if cut {
		s.Abort(warn.ErrText)
	}
	if len(pg.blocks) > at {
		pg.add(sh, g, at, false, false, "")
	}
}
