package pml

import (
	"github.com/AOShei/go-fast-ooxml/internal/chart"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
)

// chart reads the chart a graphic frame shows (c:chart or cx:chart, whose
// Start token is current): a caption with its title and tables of its
// data, at the frame's place. A layout's or master's chart is not read.
func (pg *page) chart(sh *props, g *group) {
	s := &pg.s
	id, ok := s.Attr(ns.Rel, "id")
	s.Skip()
	if pg.role != roleSlide {
		return
	}
	rel, found := pg.rels[string(id)]
	if !ok || !found || rel.External || !ns.IsRel(rel.Type, ns.RelChart, ns.RelChartEx) {
		// The chart is not in the file: the reference to it is broken.
		pg.damaged = append(pg.damaged, "the chart "+clean.Label(string(id)))
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
