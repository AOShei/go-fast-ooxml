package sml

import (
	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/chart"
	"github.com/AOShei/go-fast-ooxml/internal/diagram"
	"github.com/AOShei/go-fast-ooxml/internal/dml"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
)

// chart reads a chart drawn on the sheet: a caption with its title and
// tables of its data, at the cell its frame is anchored to. cut reports
// that the text budget ran out.
func (sh *sheet) chart(name string, out []placed, row, col int32, frame string, unseen bool) ([]placed, bool) {
	c, err := chart.Read(sh.wb.pkg, name)
	sh.noteErr(name, err)
	if c == nil {
		return out, false
	}
	sh.removed += c.Removed
	sh.charts = append(sh.charts, c.Rels...)
	if c.Cut {
		sh.part.Warn(gofastcore.SeverityWarning, gofastcore.WarnLimitExceeded, "a chart holds more points than are read; its data is cut off")
	}
	blocks, _, cut := c.Blocks(sh.room, frame)
	for _, b := range blocks {
		if unseen {
			b.Display |= gofastcore.DisplayHidden
		}
		if row >= 0 {
			b.Loc.Range = cellName(row, col)
		}
		out = append(out, placed{row: max(row, 0), col: max(col, 0), block: b})
	}
	return out, cut
}

// diagram reads a SmartArt diagram drawn on the sheet: the text of its
// nodes as list items, at the cell its frame is anchored to. cut reports
// that the text budget ran out.
func (sh *sheet) diagram(name string, rels map[string]opc.Rel, pal *dml.Palette, out []placed, row, col int32, unseen bool) ([]placed, bool) {
	d, err := diagram.Read(sh.wb.pkg, name, rels, pal)
	sh.noteErr(name, err)
	if d == nil {
		return out, false
	}
	sh.removed += d.Removed
	blocks, _, cut := d.Blocks(sh.room)
	for _, b := range blocks {
		if unseen {
			b.Display |= gofastcore.DisplayHidden
		}
		b.Loc.Range = cellName(row, col)
		out = append(out, placed{row: row, col: col, block: b})
	}
	return out, cut
}
