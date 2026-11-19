package pml

import (
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/dml"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/theme"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// The parts of a table a table style formats, from the one that wins to
// the one that gives way, and the table's own background last.
const (
	partFirstRow = iota
	partLastRow
	partFirstCol
	partLastCol
	partBand1H
	partBand2H
	partBand1V
	partBand2V
	partWhole
	partBack
	numParts
)

var partNames = [numParts]string{"firstRow", "lastRow", "firstCol", "lastCol", "band1H", "band2H", "band1V", "band2V", "wholeTbl", "tblBg"}

// tableStyle is a table style as far as hidden text needs it: the fill
// and the text colour it gives each part of a table.
type tableStyle struct {
	fill [numParts]dml.Fill
	ink  [numParts]dml.Color
}

// maxTableStyles bounds the table styles kept from a presentation.
const maxTableStyles = 1024

// tableStyle returns the table style with the given identifier: the
// presentation's own definition, or, for one of PowerPoint's built-in
// styles that the file names without defining, PowerPoint's (see
// presetstyles.go). It returns nil for a style neither knows.
func (p *presentation) tableStyle(id string) *tableStyle {
	p.tableOnce.Do(func() {
		tables := map[string]*tableStyle{}
		p.load(p.tablePart, func(s *xmlscan.Scanner) { readTableStyles(s, tables) })
		p.tables = tables
	})
	if st, own := p.tables[id]; own {
		return st
	}
	xml, ok := presetTableStyles[id]
	if !ok {
		return nil
	}
	// A built-in style is read when a table first names it.
	p.mu.Lock()
	defer p.mu.Unlock()
	if st, ok := p.presets[id]; ok {
		return st
	}
	if p.presets == nil {
		p.presets = map[string]*tableStyle{}
	}
	var s xmlscan.Scanner
	s.ResetBytes([]byte(`<a:tblStyleLst xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">`+xml+`</a:tblStyleLst>`), scanOptions)
	readTableStyles(&s, p.presets)
	return p.presets[id]
}

// readTableStyles reads the table styles of a tableStyles part (or of a
// list of them) into tables, by identifier.
func readTableStyles(s *xmlscan.Scanner, tables map[string]*tableStyle) {
	var cur *tableStyle
	part, level := -1, 0
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(ns.Draw, "tblStyle"):
			cur, level, part = nil, s.Depth(), -1
			if v, ok := s.Attr(xmlscan.NoSpace, "styleId"); ok && len(v) <= 64 && len(tables) < maxTableStyles {
				cur = &tableStyle{}
				tables[string(v)] = cur
			} else {
				s.Skip()
			}
		case cur == nil:
		case s.Depth() == level+1:
			part = -1
			for i, name := range partNames {
				if s.Space() == ns.Draw && string(s.Local()) == name {
					part = i
				}
			}
			if part < 0 {
				s.Skip()
			}
		case part < 0:
			s.Skip()
		case s.Is(ns.Draw, "tcTxStyle"):
			// The colour of the text is the colour element that is
			// its child; the font reference has one of its own.
			depth := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= depth {
				if s.Kind() != xmlscan.Start {
					continue
				}
				if s.Is(ns.Draw, "fontRef") || s.Is(ns.Draw, "font") {
					s.Skip()
					continue
				}
				if c := dml.ReadColorElement(s); !cur.ink[part].IsSet() {
					cur.ink[part] = c
				}
			}
		case s.Is(ns.Draw, "fill"):
			depth := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= depth {
				if s.Kind() != xmlscan.Start {
					continue
				}
				if f, ok := dml.ReadFill(s); ok {
					if cur.fill[part].Paint == dml.PaintUnset {
						cur.fill[part] = f
					}
					continue
				}
				s.Skip()
			}
		case s.Is(ns.Draw, "fillRef"):
			cur.fill[part] = dml.Fill{Paint: dml.PaintOther} // a fill style of the theme
			s.Skip()
		case s.Is(ns.Draw, "tcBdr"):
			s.Skip() // the borders have fills of their own
		}
	}
}

// tableLook is what a table says of itself: which special rows and
// columns it has, and the style that formats them.
type tableLook struct {
	firstRow, lastRow, firstCol, lastCol, bandRow, bandCol bool
	style                                                  string
}

// cell returns the fill and the text colour the style gives the cell at
// (r, c) of a table of the given size.
func (st *tableStyle) cell(look *tableLook, r, c, rows, cols int) (fill dml.Fill, ink dml.Color) {
	var parts [numParts]bool
	parts[partWhole], parts[partBack] = true, true
	parts[partFirstRow] = look.firstRow && r == 0
	parts[partLastRow] = look.lastRow && r == rows-1 && rows > 1
	parts[partFirstCol] = look.firstCol && c == 0
	parts[partLastCol] = look.lastCol && c == cols-1 && cols > 1
	if look.bandRow && !parts[partFirstRow] && !parts[partLastRow] {
		k := r
		if look.firstRow {
			k--
		}
		parts[partBand1H], parts[partBand2H] = k%2 == 0, k%2 != 0
	}
	if look.bandCol && !parts[partFirstCol] && !parts[partLastCol] {
		k := c
		if look.firstCol {
			k--
		}
		parts[partBand1V], parts[partBand2V] = k%2 == 0, k%2 != 0
	}
	for i := 0; i < numParts; i++ {
		if !parts[i] {
			continue
		}
		if fill.Paint == dml.PaintUnset {
			fill = st.fill[i]
		}
		if !ink.IsSet() {
			ink = st.ink[i]
		}
	}
	return fill, ink
}

// cellNote is what is kept of a table cell that has text, to judge the
// text once the table is read: a cell's fill follows its text in the
// file, and what the style gives it depends on the size of the table.
type cellNote struct {
	row, col int32
	inks     [4]theme.Color // the colours its text states
	n        uint8
	plain    bool     // some of its text states no colour
	fill     dml.Fill // the cell's own
	reasons  gofastcore.Hidden
}

type anchor struct{ row, col int32 }

// maxColumns bounds the columns of a table.
const maxColumns = 1 << 14

// table reads a table (the a:tbl whose Start token is current) into a
// table block.
func (pg *page) table(sh *props) {
	s, t := &pg.s, &pg.text
	// The text of a cell inherits nothing here: the colour the table
	// style gives it comes before the presentation's defaults, and is
	// applied when the table has been read.
	t.Palette, t.Chain, t.Base = &pg.pal, nil, dml.RunProps{}
	t.Lit, t.Later, t.Styles, t.Link, t.SlideNumber = false, true, false, nil, pg.number

	var look tableLook
	var rows [][]string
	var merges []gofastcore.Merge
	var notes []cellNote
	var anchors []anchor // per column: the cell that a covered cell there belongs to
	var busy []int32     // per column: the row a merge covers it up to
	cols := 0
	// The note of a cell, made if the cell has none. Looking one up is
	// only needed for cells that a merged cell covers, so the index is
	// built when the first of those is met.
	var index map[anchor]int
	note := func(r, c int) *cellNote {
		key := anchor{int32(r), int32(c)}
		if index == nil {
			index = make(map[anchor]int, len(notes))
			for i := range notes {
				index[anchor{notes[i].row, notes[i].col}] = i
			}
		}
		i, ok := index[key]
		if !ok {
			i = len(notes)
			notes = append(notes, cellNote{row: key.row, col: key.col})
			index[key] = i
		}
		return &notes[i]
	}

	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(ns.Draw, "tblPr"):
			flag := func(name string) bool {
				v, ok := s.Attr(xmlscan.NoSpace, name)
				return ok && isTrue(v)
			}
			look.firstRow, look.lastRow, look.firstCol = flag("firstRow"), flag("lastRow"), flag("firstCol")
			look.lastCol, look.bandRow, look.bandCol = flag("lastCol"), flag("bandRow"), flag("bandCol")
			inner := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= inner {
				if s.Kind() != xmlscan.Start {
					continue
				}
				if s.Is(ns.Draw, "tableStyleId") {
					if id := s.AppendText(nil); len(id) <= 64 {
						look.style = strings.TrimSpace(string(id))
					}
					continue
				}
				s.Skip()
			}
		case s.Is(ns.Draw, "tr"):
			if pg.cut != "" {
				s.Skip()
				continue
			}
			r := len(rows)
			var row []string
			inner := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= inner {
				if s.Kind() != xmlscan.Start {
					continue
				}
				if !s.Is(ns.Draw, "tc") || len(row) >= maxColumns {
					s.Skip()
					continue
				}
				if pg.p.cells.Add(-1) < 0 {
					pg.p.cells.Add(1)
					pg.cut = "the limit on table cells was reached"
					s.Skip()
					continue
				}
				pg.cells++
				c := len(row)
				span, down := int64(1), int64(1)
				if v, ok := intAttr(s, "gridSpan"); ok && v > 1 {
					span = min(v, maxColumns)
				}
				if v, ok := intAttr(s, "rowSpan"); ok && v > 1 {
					down = min(v, 1<<20)
				}
				covered := false
				if v, ok := s.Attr(xmlscan.NoSpace, "hMerge"); ok && isTrue(v) {
					covered = true
				}
				if v, ok := s.Attr(xmlscan.NoSpace, "vMerge"); ok && isTrue(v) {
					covered = true
				}
				text := ""
				var cn cellNote
				cell := s.Depth()
				for s.Next() != xmlscan.EOF && s.Depth() >= cell {
					if s.Kind() != xmlscan.Start {
						continue
					}
					switch {
					case s.Is(ns.Draw, "txBody"):
						t.Read(s)
						pg.programs += t.Programs
						t.Programs = 0
						switch len(t.Paras) {
						case 0:
						case 1:
							text = string(t.Buf[t.Paras[0].Start:t.Paras[0].End])
						default:
							var sb strings.Builder
							for i := range t.Paras {
								if i > 0 {
									sb.WriteByte('\n')
								}
								sb.Write(t.Buf[t.Paras[i].Start:t.Paras[i].End])
							}
							text = sb.String()
						}
						for i := range t.Paras {
							cn.reasons |= t.Paras[i].Hidden
						}
						cn.n = uint8(copy(cn.inks[:], t.Inks))
						cn.plain = t.Plain
					case s.Is(ns.Draw, "tcPr"):
						props := s.Depth()
						for s.Next() != xmlscan.EOF && s.Depth() >= props {
							if s.Kind() != xmlscan.Start {
								continue
							}
							if f, ok := dml.ReadFill(s); ok {
								if cn.fill.Paint == dml.PaintUnset {
									cn.fill = f
								}
								continue
							}
							s.Skip() // the borders have fills of their own
						}
					default:
						s.Skip()
					}
				}

				for len(anchors) <= c {
					anchors = append(anchors, anchor{row: -1})
					busy = append(busy, 0)
				}
				if covered && anchors[c].row >= 0 {
					// A cell that a merged cell covers. It is not shown; what
					// it says is kept with the cell that covers it, and marked.
					row = append(row, "")
					if text != "" {
						a := anchors[c]
						at := &row // the row being read, unless the cell is in one above
						if int(a.row) != r {
							at = &rows[a.row]
						}
						if (*at)[a.col] != "" {
							(*at)[a.col] += "\n"
						}
						(*at)[a.col] += text
						to := note(int(a.row), int(a.col))
						to.reasons |= cn.reasons | gofastcore.HiddenInvisible
					}
					continue
				}
				row = append(row, text)
				if text != "" {
					cn.row, cn.col = int32(r), int32(c)
					if index != nil {
						index[anchor{cn.row, cn.col}] = len(notes)
					}
					notes = append(notes, cn)
				}
				free := true
				for k := c; k < c+int(span) && k < maxColumns; k++ {
					for len(anchors) <= k {
						anchors = append(anchors, anchor{row: -1})
						busy = append(busy, 0)
					}
					free = free && busy[k] <= int32(r)
				}
				for k := c; k < c+int(span) && k < len(anchors); k++ {
					anchors[k] = anchor{row: int32(r), col: int32(c)}
					if free {
						busy[k] = int32(r) + int32(down)
					}
				}
				if free && (span > 1 || down > 1) {
					merges = append(merges, gofastcore.Merge{Row: r, Col: c, RowSpan: int(down), ColSpan: int(span)})
				}
			}
			cols = max(cols, len(row))
			rows = append(rows, row)
		default:
			s.Skip()
		}
	}
	if len(rows) == 0 || cols == 0 {
		return
	}
	for r := range rows {
		for len(rows[r]) < cols {
			rows[r] = append(rows[r], "")
		}
	}
	tb := &gofastcore.Table{Rows: rows}
	if look.firstRow {
		tb.HeaderRows = 1
	}
	for _, m := range merges {
		m.RowSpan, m.ColSpan = min(m.RowSpan, len(rows)-m.Row), min(m.ColSpan, cols-m.Col)
		if m.RowSpan > 1 || m.ColSpan > 1 {
			tb.Merges = append(tb.Merges, m)
		}
	}

	// Each cell's text against the cell's fill, now that both are known.
	var style *tableStyle
	styled := look.style != ""
	if styled {
		style = pg.p.tableStyle(look.style)
	}
	low := func(ink theme.Color, back theme.Color) bool {
		return gofastcore.LowContrast(ink.R, ink.G, ink.B, back.R, back.G, back.B)
	}
	all := gofastcore.Hidden(0)
	common := ^gofastcore.Hidden(0)
	for i := range notes {
		cn := &notes[i]
		if rows[cn.row][cn.col] == "" {
			continue
		}
		fill, ink := cn.fill, textInk
		known := !styled // what the style gives the cell is known
		if style != nil {
			known = true
			f, c := style.cell(&look, int(cn.row), int(cn.col), len(rows), cols)
			if fill.Paint == dml.PaintUnset {
				fill = f
			}
			if c.IsSet() {
				ink = c
			}
		}
		var back theme.Color
		lit := false
		switch fill.Paint {
		case dml.PaintSolid:
			back, lit = pg.pal.Opaque(fill, nil)
		case dml.PaintOther:
		default:
			if known {
				back, lit = sh.behind, sh.lit
			}
		}
		if lit {
			for _, c := range cn.inks[:cn.n] {
				if low(c, back) {
					cn.reasons |= gofastcore.HiddenLowContrast
				}
			}
			if cn.plain && known {
				if c, alpha, ok := pg.pal.Resolve(ink, nil); ok && alpha >= 1 && low(c, back) {
					cn.reasons |= gofastcore.HiddenLowContrast
				}
			}
		}
		cn.reasons |= sh.reasons
		all |= cn.reasons
		common &= cn.reasons
	}
	if all == 0 {
		common = 0
	}
	common &= all
	for i := range notes {
		cn := &notes[i]
		if part := cn.reasons &^ common; part != 0 && rows[cn.row][cn.col] != "" {
			tb.Hidden = append(tb.Hidden, gofastcore.HiddenCells{Row: int(cn.row), Col: int(cn.col), RowSpan: 1, ColSpan: 1, Hidden: part})
		}
	}
	pg.blocks = append(pg.blocks, gofastcore.Block{Kind: gofastcore.KindTable, Table: tb, Hidden: all | sh.reasons, Loc: gofastcore.Location{BBox: bbox(sh)}})
}
