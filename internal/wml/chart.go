package wml

import (
	"github.com/AOShei/go-fast-core/clean"
	"github.com/AOShei/go-fast-ooxml/internal/chart"
	"github.com/AOShei/go-fast-ooxml/internal/diagram"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
)

// chart reads the chart a drawing shows (c:chart or cx:chart, whose Start
// token is current): a caption with its title and tables of its data,
// after the paragraph the drawing is in. name is the name the drawing
// gives it.
func (p *parser) chart(name string, nameOver int64) {
	s := &p.s
	id, _ := s.Attr(ns.Rel, "id")
	rel, found := p.rels[string(id)]
	if !found || rel.External || !ns.IsRel(rel.Type, ns.RelChart, ns.RelChartEx) || !p.r.pkg.Has(rel.Target) {
		p.r.missing.Add("the chart " + clean.Label(string(id)))
		return
	}
	c, err := chart.Read(p.r.pkg, rel.Target)
	p.r.noteErr(rel.Target, err)
	if c == nil {
		return
	}
	p.r.removed += c.Removed
	p.r.lost.NameOver(nameOver) // the chart takes the drawing's name
	p.r.charts = append(p.r.charts, c.Rels...)
	p.r.lost.Merge(&c.Lost)
	blocks, _, cut := c.Blocks(p.r.text, name)
	if len(blocks) > 0 {
		p.produced++
	}
	p.after = append(p.after, blocks...)
	if cut {
		s.Abort(warn.ErrText)
	}
}

// diagram reads the SmartArt diagram a drawing shows (dgm:relIds, whose
// Start token is current): the text of its nodes as list items, after
// the paragraph the drawing is in.
func (p *parser) diagram() {
	s := &p.s
	id, _ := s.Attr(ns.Rel, "dm")
	rel, found := p.rels[string(id)]
	if !found || rel.External || !ns.IsRel(rel.Type, ns.RelDiagram) || !p.r.pkg.Has(rel.Target) {
		p.r.missing.Add("the diagram " + clean.Label(string(id)))
		return
	}
	d, err := diagram.Read(p.r.pkg, rel.Target, p.rels, &p.r.drawn)
	p.r.noteErr(rel.Target, err)
	if d == nil {
		return
	}
	p.r.removed += d.Removed
	p.r.lost.Merge(&d.Lost)
	blocks, _, cut := d.Blocks(p.r.text)
	if len(blocks) > 0 {
		p.produced++
	}
	p.after = append(p.after, blocks...)
	if cut {
		s.Abort(warn.ErrText)
	}
}
