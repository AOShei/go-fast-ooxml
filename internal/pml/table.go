package pml

import (
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-core/xmlscan"
	"github.com/AOShei/go-fast-ooxml/internal/dml"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
)

// cellNote is what the file declares about the display of a table cell
// that has text.
type cellNote struct {
	row, col int32
	reasons  gofastcore.Display
}

type anchor struct{ row, col int32 }

// maxColumns bounds the columns of a table.
const maxColumns = warn.MaxTableColumns

// table reads a table (the a:tbl whose Start token is current) into a
// table block.
func (pg *page) table(sh *props) {
	s, t := &pg.s, &pg.text
	// The text of a cell inherits nothing here: what the table style
	// gives it is not read.
	t.Palette, t.Chain, t.Base = &pg.pal, nil, dml.RunProps{}
	t.Styles, t.Link, t.SlideNumber = false, nil, pg.number

	header := false // the table says its first row is a header
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
	var joins map[anchor]*strings.Builder // the text of a merged cell and the cells it covers
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
			v, ok := s.Attr(xmlscan.NoSpace, "firstRow")
			header = ok && isTrue(v)
			s.Skip()
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
				if !s.Is(ns.Draw, "tc") {
					s.Skip()
					continue
				}
				if len(row) >= maxColumns {
					pg.lost.Add(warn.CapSlideTableColumns, 1)
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
						t.Cost = 0 // a cell: charged with the table
						t.Read(s)
						t.Cost = gofastcore.BlockCost
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
							cn.reasons |= t.Paras[i].Display
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
					// A cell that a merged cell covers, which the file
					// declares is not shown: what it says is kept with the
					// cell that covers it, and marked hidden.
					row = append(row, "")
					if text != "" {
						a := anchors[c]
						at := &row // the row being read, unless the cell is in one above
						if int(a.row) != r {
							at = &rows[a.row]
						}
						// Appended in a builder of the merged cell's, so a
						// merge over many cells is not copied again for each.
						j := joins[a]
						if j == nil {
							if joins == nil {
								joins = map[anchor]*strings.Builder{}
							}
							j = &strings.Builder{}
							j.WriteString((*at)[a.col])
							joins[a] = j
						}
						if j.Len() > 0 {
							j.WriteByte('\n')
						}
						j.WriteString(text)
						(*at)[a.col] = j.String()
						to := note(int(a.row), int(a.col))
						to.reasons |= cn.reasons | gofastcore.DisplayHidden
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
	// The table is a block, and each of its cells takes room, filled or
	// not. A table the slide has no room for in full keeps the rows that
	// fit, and the reading of the slide ends there.
	fit := warn.FitRows(pg.text.Budget, len(rows), cols)
	if fit > 0 {
		pg.text.Taken += int64(gofastcore.BlockCost + gofastcore.CellCost*fit*cols)
	}
	if fit < len(rows) {
		pg.s.Abort(warn.ErrText)
		if fit == 0 {
			return
		}
		rows = rows[:fit]
		kept := merges[:0]
		for _, m := range merges {
			if m.Row < fit {
				kept = append(kept, m)
			}
		}
		merges = kept
		keptNotes := notes[:0]
		for _, cn := range notes {
			if int(cn.row) < fit {
				keptNotes = append(keptNotes, cn)
			}
		}
		notes = keptNotes
	}
	tb := &gofastcore.Table{Rows: rows}
	if header {
		tb.HeaderRows = 1
	}
	for _, m := range merges {
		m.RowSpan, m.ColSpan = min(m.RowSpan, len(rows)-m.Row), min(m.ColSpan, cols-m.Col)
		if m.RowSpan > 1 || m.ColSpan > 1 {
			tb.Merges = append(tb.Merges, m)
		}
	}

	all := gofastcore.Display(0)
	common := ^gofastcore.Display(0)
	for i := range notes {
		cn := &notes[i]
		if rows[cn.row][cn.col] == "" {
			continue
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
			tb.Display = append(tb.Display, gofastcore.DisplayCells{Row: int(cn.row), Col: int(cn.col), RowSpan: 1, ColSpan: 1, Display: part})
		}
	}
	pg.blocks = append(pg.blocks, gofastcore.Block{Kind: gofastcore.KindTable, Table: tb, Display: all | sh.reasons, Loc: gofastcore.Location{BBox: bbox(sh)}})
}
