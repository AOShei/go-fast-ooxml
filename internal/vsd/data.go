package vsd

import (
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-core/clean"
	"github.com/AOShei/go-fast-ooxml/internal/dml"
)

// Shape data is what Visio's Shape Data window shows of a shape: a label
// and a value for each row of the shape's Property section, the master's
// rows first. It is never drawn, so it is a stored table (shape_data)
// that follows the shape's own text, at the shape's place.

// shapeData adds the table of a shape's data, if it has any.
func (pg *page) shapeData(v view, box dml.Rect, boxed bool) {
	own := v.s.sections[sProperty]
	var base *section
	if v.base != nil && (own == nil || !own.del) {
		base = v.base.sections[sProperty]
	}
	if (own == nil || len(own.rows) == 0) && (base == nil || len(base.rows) == 0) {
		return
	}
	d := pg.d
	if base != nil && len(base.rows) > 0 {
		// The master's rows are on every instance of it: each copy counts.
		n := int64(len(base.rows))
		if d.repeated.Add(-n) < 0 {
			d.repeated.Add(n)
			pg.repeat = true
			return
		}
	}
	rows := v.namedRows(sProperty)
	if len(rows) == 0 {
		return
	}
	t := &gofastcore.Table{Rows: make([][]string, 0, len(rows))}
	cost := gofastcore.BlockCost
	hidden := false
	for _, r := range rows {
		label, _ := r.cell("Label")
		value, _ := r.cell("Value")
		l, removed := clean.String([]byte(label), clean.Prose)
		pg.removed += removed
		if l == "" {
			// A row with no label is known by its name.
			l, removed = clean.String([]byte(r.name), clean.Prose)
			pg.removed += removed
		}
		x, removed := clean.String([]byte(value), clean.Prose)
		pg.removed += removed
		if inv, _ := r.cell("Invisible"); inv == "1" || strings.EqualFold(inv, "true") {
			i := len(t.Rows)
			if n := len(t.Display); n > 0 && t.Display[n-1].Row+t.Display[n-1].RowSpan == i {
				t.Display[n-1].RowSpan++
			} else {
				t.Display = append(t.Display, gofastcore.DisplayCells{Row: i, Col: 0, RowSpan: 1, ColSpan: 2, Display: gofastcore.DisplayHidden})
			}
			hidden = true
		}
		t.Rows = append(t.Rows, []string{l, x})
		cost += 2*gofastcore.CellCost + len(l) + len(x)
	}
	if n := 2 * int64(len(t.Rows)); d.cells.Add(-n) < 0 {
		d.cells.Add(n)
		pg.cellsOut = true
		return
	}
	if !pg.room.Take(cost) {
		pg.full = true
		return
	}
	b := gofastcore.Block{Kind: gofastcore.KindTable, Table: t,
		Attrs: map[string]string{gofastcore.AttrStored: gofastcore.StoredShapeData}}
	if hidden {
		b.Display = gofastcore.DisplayHidden
	}
	if boxed {
		b.Loc.BBox = gofastcore.Rect(box)
	}
	name := v.s.name
	if name == "" && v.base != nil {
		name = v.base.name
	}
	if name = pg.lost.Name(name); name != "" {
		b.Attrs[gofastcore.AttrName] = name
	}
	pg.blocks = append(pg.blocks, b)
}
