package wml

import (
	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/chart"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/diagram"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
)

// chart reads the chart a drawing shows (c:chart or cx:chart, whose Start
// token is current): a caption with its title and tables of its data,
// after the paragraph the drawing is in. name is the name the drawing
// gives it.
func (p *parser) chart(name string) {
	s := &p.s
	id, _ := s.Attr(ns.Rel, "id")
	rel, found := p.rels[string(id)]
	if !found || rel.External || !ns.IsRel(rel.Type, ns.RelChart, ns.RelChartEx) || !p.r.pkg.Has(rel.Target) {
		p.r.doc.Warn(gofastcore.SeverityWarning, WarnPartDamaged, "the chart "+clean.Label(string(id)))
		return
	}
	c, err := chart.Read(p.r.pkg, rel.Target)
	p.r.noteErr(rel.Target, err)
	if c == nil {
		return
	}
	p.r.removed += c.Removed
	p.r.charts = append(p.r.charts, c.Rels...)
	if c.Cut {
		p.r.doc.Warn(gofastcore.SeverityWarning, gofastcore.WarnLimitExceeded, "a chart holds more points than are read; its data is cut off")
	}
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
		p.r.doc.Warn(gofastcore.SeverityWarning, WarnPartDamaged, "the diagram "+clean.Label(string(id)))
		return
	}
	d, err := diagram.Read(p.r.pkg, rel.Target, p.rels, &p.r.drawn)
	p.r.noteErr(rel.Target, err)
	if d == nil {
		return
	}
	p.r.removed += d.Removed
	blocks, _, cut := d.Blocks(p.r.text)
	if len(blocks) > 0 {
		p.produced++
	}
	p.after = append(p.after, blocks...)
	if cut {
		s.Abort(warn.ErrText)
	}
}
