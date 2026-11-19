package sml

import (
	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/chart"
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
			b.Hidden |= gofastcore.HiddenInvisible
		}
		if row >= 0 {
			b.Loc.Range = cellName(row, col)
		}
		out = append(out, placed{row: max(row, 0), col: max(col, 0), block: b})
	}
	return out, cut
}
