package sml

import (
	"sort"

	"github.com/AOShei/go-fast-core"
)

// A sheet is a grid with no end, and what a person sees on it as "a
// table" is a block of filled cells with empty rows and columns around
// it. This file finds those blocks.
//
// A table the workbook declares is taken as declared. The cells left over
// are cut along empty rows, each piece along empty columns, and so on:
// a region is a rectangle with no empty row or column across it. The
// boundaries found this way are a reading of the layout, not something
// the file states, and such tables are marked as inferred.

// group is a set of cells in reading order: a run of the sheet's cells,
// or a list of their positions.
type group struct {
	lo, hi int
	list   []int32
}

func (g group) len() int {
	if g.list != nil {
		return len(g.list)
	}
	return g.hi - g.lo
}

func (g group) index(i int) int {
	if g.list != nil {
		return int(g.list[i])
	}
	return g.lo + i
}

func (g group) slice(from, to int) group {
	if g.list != nil {
		return group{list: g.list[from:to:to]}
	}
	return group{lo: g.lo + from, hi: g.lo + to}
}

// Limits on what a region may become.
const (
	// maxCuts is how many times a piece is cut again. Layouts people make
	// need two or three; the bound keeps a contrived one from costing
	// more than a fixed number of passes over the cells.
	maxCuts = 8
	// maxTableCells is the largest table written as one block. A larger
	// region is written as several, each a run of its rows.
	maxTableCells = 1 << 20
)

// placed is a block with the cell it sits at, by which the blocks of a
// sheet are put in order.
type placed struct {
	row, col int32
	seq      int
	block    gofastcore.Block
}

// builder turns the cells of a sheet into table blocks.
type builder struct {
	sh    *sheet
	out   []placed
	owner []int32 // for each cell, the table it went into plus one; kept only if the sheet has merged cells
	rects []rect  // the rectangle of each table
	occ   []bool  // scratch: which columns of a piece are occupied
	ids   []int32 // scratch: the column group of each column
}

// regions returns the sheet's cells as table blocks.
func (sh *sheet) regions(declared []declaredTable) []placed {
	b := &builder{sh: sh}
	n := len(sh.cells)
	if len(sh.merges) > 0 {
		b.owner = make([]int32, n)
	}
	rest := group{lo: 0, hi: n}
	if len(declared) > 0 {
		taken := make([]bool, n)
		any := false
		// Tables do not overlap, so finding their cells is one pass over
		// the sheet in all. A file that declares many tables over the
		// same rows would make it many passes; past a few, the rest of
		// its tables are not taken as declared.
		effort := 8*n + 1<<16
		for _, t := range declared {
			// The cells inside the table's rectangle that no earlier
			// table has.
			lo := sort.Search(n, func(i int) bool { return sh.cells[i].row >= t.at.r0 })
			var list []int32
			if effort < 0 {
				break
			}
			for i := lo; i < n && sh.cells[i].row <= t.at.r1; i++ {
				effort--
				if c := &sh.cells[i]; c.col >= t.at.c0 && c.col <= t.at.c1 && !taken[i] {
					list = append(list, int32(i))
				}
			}
			if len(list) == 0 {
				continue
			}
			at := t.at
			if b.table(group{list: list}, &at, t.headerRows) {
				for _, i := range list {
					taken[i] = true
				}
				any = true
			}
		}
		if any {
			var list []int32
			for i := range taken {
				if !taken[i] {
					list = append(list, int32(i))
				}
			}
			rest = group{list: list}
			if list == nil {
				rest = group{}
			}
		}
	}

	type item struct {
		g     group
		depth int
	}
	work := []item{{rest, 0}}
	var pieces []group
	for len(work) > 0 {
		it := work[len(work)-1]
		work = work[:len(work)-1]
		if it.g.len() == 0 {
			continue
		}
		var changed bool
		pieces, changed = b.cut(it.g, pieces[:0])
		for _, p := range pieces {
			if changed && it.depth+1 < maxCuts {
				work = append(work, item{p, it.depth + 1})
			} else if !b.table(p, nil, 0) {
				b.sparse(p)
			}
		}
	}
	b.merges()
	return b.out
}

// cut divides a group along its empty rows, and each piece along its
// empty columns. changed reports whether there was anything to divide.
func (b *builder) cut(g group, out []group) (pieces []group, changed bool) {
	cells := b.sh.cells
	n := g.len()
	start := 0
	for i := 1; i <= n; i++ {
		if i < n && cells[g.index(i)].row-cells[g.index(i-1)].row <= 1 {
			continue
		}
		// g[start:i] is a band of rows with no empty row across it.
		band := g.slice(start, i)
		if start > 0 || i < n {
			changed = true
		}
		start = i
		lo, hi := int32(maxCols), int32(-1)
		for j := 0; j < band.len(); j++ {
			c := cells[band.index(j)].col
			lo, hi = min(lo, c), max(hi, c)
		}
		width := int(hi - lo + 1)
		if cap(b.occ) < width {
			b.occ = make([]bool, maxCols)
			b.ids = make([]int32, maxCols)
		}
		occ, ids := b.occ[:width], b.ids[:width]
		clear(occ)
		for j := 0; j < band.len(); j++ {
			occ[cells[band.index(j)].col-lo] = true
		}
		groups := int32(0)
		for c := range occ {
			if occ[c] {
				if c == 0 || !occ[c-1] {
					groups++
				}
				ids[c] = groups - 1
			}
		}
		if groups == 1 {
			out = append(out, band)
			continue
		}
		// Side by side: the band's cells are dealt out to the column
		// groups, keeping their order.
		changed = true
		counts := make([]int, groups+1)
		for j := 0; j < band.len(); j++ {
			counts[ids[cells[band.index(j)].col-lo]+1]++
		}
		for k := 1; k <= int(groups); k++ {
			counts[k] += counts[k-1]
		}
		list := make([]int32, band.len())
		next := append([]int(nil), counts[:groups]...)
		for j := 0; j < band.len(); j++ {
			i := band.index(j)
			k := ids[cells[i].col-lo]
			list[next[k]] = int32(i)
			next[k]++
		}
		for k := 0; k < int(groups); k++ {
			out = append(out, group{list: list[counts[k]:counts[k+1]:counts[k+1]]})
		}
	}
	return out, changed
}

// table writes a group of cells as a table block, or as several if it is
// very large. at is the rectangle of a declared table; nil takes the
// rectangle the cells span, and marks the table as inferred. It reports
// false, having written nothing, if the rectangle is so much larger than
// the cells in it that filling it would cost memory out of proportion.
func (b *builder) table(g group, at *rect, headerRows int) bool {
	cells := b.sh.cells
	n := g.len()
	var r rect
	if at != nil {
		r = *at
	} else {
		r = rect{r0: cells[g.index(0)].row, r1: cells[g.index(n-1)].row, c0: maxCols, c1: -1}
		for i := 0; i < n; i++ {
			c := cells[g.index(i)].col
			r.c0, r.c1 = min(r.c0, c), max(r.c1, c)
		}
	}
	rows, cols := int64(r.r1-r.r0+1), int64(r.c1-r.c0+1)
	if rows*cols > 16*int64(n)+4096 {
		return false
	}
	perBlock := rows
	if rows*cols > maxTableCells {
		perBlock = max(maxTableCells/cols, 1)
	}
	next := 0 // the next cell of g to place
	for top := int64(0); top < rows; top += perBlock {
		height := min(perBlock, rows-top)
		part := rect{r0: r.r0 + int32(top), r1: r.r0 + int32(top+height) - 1, c0: r.c0, c1: r.c1}
		flat := make([]string, height*cols)
		t := &gofastcore.Table{Rows: make([][]string, height)}
		for i := range t.Rows {
			t.Rows[i] = flat[int64(i)*cols : int64(i+1)*cols : int64(i+1)*cols]
		}
		if top == 0 {
			t.HeaderRows = int(min(int64(headerRows), height))
		}
		id := int32(len(b.rects) + 1)
		b.rects = append(b.rects, part)
		var marks hiddenMarks
		for ; next < n; next++ {
			i := g.index(next)
			c := &cells[i]
			if c.row > part.r1 {
				break
			}
			if c.row < part.r0 || c.col < part.c0 || c.col > part.c1 {
				continue
			}
			t.Rows[c.row-part.r0][c.col-part.c0] = c.text
			if b.owner != nil {
				b.owner[i] = id
			}
			if c.hidden != 0 {
				marks.add(t, int(c.row-part.r0), int(c.col-part.c0), c.hidden)
			}
		}
		block := gofastcore.Block{Kind: gofastcore.KindTable, Table: t}
		block.Loc.Range = rangeName(part)
		if at == nil {
			block.Origin = gofastcore.OriginInferred
		}
		b.hide(&block, part)
		b.out = append(b.out, placed{row: part.r0, col: part.c0, seq: len(b.out), block: block})
	}
	return true
}

// sparse writes a group that is too thinly filled to be a grid: each run
// of cells side by side in a row becomes a table of one row.
func (b *builder) sparse(g group) {
	cells := b.sh.cells
	b.sh.split++
	start := 0
	for i := 1; i <= g.len(); i++ {
		if i < g.len() {
			a, c := &cells[g.index(i-1)], &cells[g.index(i)]
			if a.row == c.row && c.col == a.col+1 {
				continue
			}
		}
		b.table(g.slice(start, i), nil, 0)
		start = i
	}
}

// hiddenMarks collects the cells of a table that hide their own text
// (white on white, a tiny font, a format that shows nothing) as regions.
// Cells side by side with the same reasons make one region as the table
// is filled; settleMarks then joins identical regions in rows one under
// another, so that a hidden column of a million cells is one region.
type hiddenMarks struct {
	last    int // the region the last marked cell went into, or 0 if none
	row     int
	col     int
	reasons gofastcore.Hidden
}

func (m *hiddenMarks) add(t *gofastcore.Table, row, col int, reasons gofastcore.Hidden) {
	if m.last > 0 && m.row == row && m.col+1 == col && m.reasons == reasons {
		t.Hidden[m.last-1].ColSpan++
		m.col = col
		return
	}
	t.Hidden = append(t.Hidden, gofastcore.HiddenCells{Row: row, Col: col, RowSpan: 1, ColSpan: 1, Hidden: reasons})
	m.last, m.row, m.col, m.reasons = len(t.Hidden), row, col, reasons
}

// settleMarks joins each region to an identical one that ends on the row
// above. The regions arrive in row order and, within a row, in column
// order.
func settleMarks(t *gofastcore.Table) {
	if len(t.Hidden) < 2 {
		return
	}
	out := t.Hidden[:0]
	var open, next []int // regions of out that end on the row above, and on this row; by column
	for i := 0; i < len(t.Hidden); {
		row := t.Hidden[i].Row
		j := i
		for j < len(t.Hidden) && t.Hidden[j].Row == row {
			j++
		}
		k := 0
		next = next[:0]
		for _, h := range t.Hidden[i:j] {
			for k < len(open) && out[open[k]].Col < h.Col {
				k++
			}
			if k < len(open) {
				if o := &out[open[k]]; o.Col == h.Col && o.ColSpan == h.ColSpan && o.Hidden == h.Hidden && o.Row+o.RowSpan == row {
					o.RowSpan++
					next = append(next, open[k])
					continue
				}
			}
			out = append(out, h)
			next = append(next, len(out)-1)
		}
		open, next = next, open
		i = j
	}
	t.Hidden = out
}

// hide marks the parts of a table that the sheet hides: hidden rows and
// columns. Cells that hide their own text were marked as it was filled.
func (b *builder) hide(block *gofastcore.Block, at rect) {
	t := block.Table
	settleMarks(t)
	rows, cols := int(at.r1-at.r0+1), int(at.c1-at.c0+1)
	hr := b.sh.hiddenRows
	i := sort.Search(len(hr), func(i int) bool { return hr[i] >= at.r0 })
	for i < len(hr) && hr[i] <= at.r1 {
		j := i
		for j+1 < len(hr) && hr[j+1] == hr[j]+1 && hr[j+1] <= at.r1 {
			j++
		}
		t.Hidden = append(t.Hidden, gofastcore.HiddenCells{Row: int(hr[i] - at.r0), Col: 0, RowSpan: j - i + 1, ColSpan: cols, Hidden: gofastcore.HiddenInvisible})
		i = j + 1
	}
	hc := b.sh.hiddenCols
	for k := sort.Search(len(hc), func(k int) bool { return hc[k].hi >= at.c0 }); k < len(hc) && hc[k].lo <= at.c1; k++ {
		lo, hi := max(hc[k].lo, at.c0), min(hc[k].hi, at.c1)
		t.Hidden = append(t.Hidden, gofastcore.HiddenCells{Row: 0, Col: int(lo - at.c0), RowSpan: rows, ColSpan: int(hi - lo + 1), Hidden: gofastcore.HiddenInvisible})
	}
	for _, h := range t.Hidden {
		block.Hidden |= h.Hidden
	}
}

// merges gives each merged range to the table that holds its first cell.
// A merge with nothing in its first cell shows nothing and is left out.
func (b *builder) merges() {
	cells := b.sh.cells
	for _, m := range b.sh.merges {
		i := sort.Search(len(cells), func(i int) bool {
			c := &cells[i]
			return c.row > m.r0 || c.row == m.r0 && c.col >= m.c0
		})
		if i >= len(cells) || cells[i].row != m.r0 || cells[i].col != m.c0 || b.owner[i] == 0 {
			continue
		}
		at := b.rects[b.owner[i]-1]
		// The part of the merge inside the table.
		r1, c1 := min(m.r1, at.r1), min(m.c1, at.c1)
		if r1 == m.r0 && c1 == m.c0 {
			continue
		}
		t := b.out[b.owner[i]-1].block.Table
		t.Merges = append(t.Merges, gofastcore.Merge{Row: int(m.r0 - at.r0), Col: int(m.c0 - at.c0), RowSpan: int(r1 - m.r0 + 1), ColSpan: int(c1 - m.c0 + 1)})
	}
}
