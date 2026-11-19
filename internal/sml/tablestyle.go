package sml

import (
	"sort"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/dml"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/theme"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// A declared table may have a table style: a format for each part of the
// table (the whole, its header row, every other row...) that gives cells
// the colour of their text and the fill behind it where the cells do not
// give their own. A style is the workbook's own, defined in its styles
// part, or one Excel has built in, which the workbook names and does not
// define (tablestyles.go, from the standard). Whether text is seen is
// judged against what the style paints, so white text on a table's dark
// header is not taken for white on white.

// tableElement is a part of a table that a style formats, in the order in
// which they are applied: a later one is over an earlier one.
type tableElement uint8

const (
	elWholeTable tableElement = iota
	elFirstColumnStripe
	elSecondColumnStripe
	elFirstRowStripe
	elSecondRowStripe
	elLastColumn
	elFirstColumn
	elHeaderRow
	elTotalRow
	elFirstHeaderCell
	elLastHeaderCell
	elFirstTotalCell
	elLastTotalCell
	elementCount
)

var elementNames = map[string]tableElement{
	"wholeTable": elWholeTable, "firstColumnStripe": elFirstColumnStripe, "secondColumnStripe": elSecondColumnStripe,
	"firstRowStripe": elFirstRowStripe, "secondRowStripe": elSecondRowStripe, "lastColumn": elLastColumn,
	"firstColumn": elFirstColumn, "headerRow": elHeaderRow, "totalRow": elTotalRow,
	"firstHeaderCell": elFirstHeaderCell, "lastHeaderCell": elLastHeaderCell,
	"firstTotalCell": elFirstTotalCell, "lastTotalCell": elLastTotalCell,
}

// presetElement is one element of a built-in style, as the standard
// writes it.
type presetElement struct {
	element tableElement
	ink     colorRef
	fill    colorRef
	filled  bool
}

// look is the colour of text and the fill a format gives, as far as each
// is said.
type look struct {
	inkSet    bool
	ink       theme.Color
	fill      uint8 // 0 none said, 1 one colour, 2 something that is not one colour, or not known
	fillColor theme.Color
}

// tableStyle is a style resolved against the workbook's theme.
type tableStyle struct {
	elements [elementCount]look
	has      [elementCount]bool
	size     [elementCount]int // of stripes: how many rows or columns each is
}

// customStyle is a table style the workbook defines, by the formats
// (dxfs) its elements use.
type customStyle struct {
	dxf  [elementCount]int // index into the formats, -1 if the element is not formatted
	size [elementCount]int
}

// readTableStyles reads tableStyles in a styles part: the workbook's own
// table styles, as the formats their elements use.
func readTableStyles(s *xmlscan.Scanner) map[string]*customStyle {
	styles := map[string]*customStyle{}
	depth := s.Depth()
	var cur *customStyle
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Depth() == depth+1 && s.Is(ns.Sheet, "tableStyle"):
			cur = nil
			// A style is for tables unless it says it is not.
			if v, ok := s.Attr(xmlscan.NoSpace, "table"); !ok || isTrue(v) {
				name, _ := s.Attr(xmlscan.NoSpace, "name")
				if len(styles) < maxTableStyles && len(name) > 0 {
					cur = &customStyle{}
					for i := range cur.dxf {
						cur.dxf[i] = -1
					}
					styles[string(name)] = cur
				}
			}
		case s.Depth() == depth+2 && cur != nil && s.Is(ns.Sheet, "tableStyleElement"):
			typ, _ := s.Attr(xmlscan.NoSpace, "type")
			el, ok := elementNames[string(typ)]
			if !ok {
				continue
			}
			if id, ok := intAttr(s, "dxfId"); ok && id >= 0 {
				cur.dxf[el] = id
			}
			if n, ok := intAttr(s, "size"); ok && n >= 1 && n <= 9 {
				cur.size[el] = n
			}
		}
	}
	return styles
}

// maxTableStyles bounds the table styles kept from a workbook.
const maxTableStyles = 1 << 10

// readDxf reads one format (dxf) for what a table style needs of it.
func readDxf(s *xmlscan.Scanner) (ink, fill colorRef, fillKind uint8) {
	depth := s.Depth()
	inFont := false
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Depth() == depth+1:
			inFont = s.Is(ns.Sheet, "font")
		case inFont && s.Depth() == depth+2 && s.Is(ns.Sheet, "color"):
			ink = readColorRef(s)
		case s.Depth() == depth+2 && s.Is(ns.Sheet, "patternFill"):
			// In a format, a pattern fill with no type is a solid one,
			// and the colour of a solid fill is its background.
			switch v, _ := s.Attr(xmlscan.NoSpace, "patternType"); string(v) {
			case "", "solid":
				fillKind = 1
			case "none":
				fillKind = 0
			default:
				fillKind = 2
			}
		case s.Depth() == depth+2 && s.Is(ns.Sheet, "gradientFill"):
			fillKind = 2
		case s.Depth() == depth+3 && s.Is(ns.Sheet, "bgColor"):
			fill = readColorRef(s)
		case s.Depth() == depth+3 && s.Is(ns.Sheet, "fgColor") && fill.kind == 0:
			fill = readColorRef(s)
		}
	}
	return ink, fill, fillKind
}

// tableStyle returns the style a table names, resolved, or nil if the
// workbook neither defines it nor is it one Excel has built in.
func (st *styleSheet) tableStyle(name string) *tableStyle {
	if st == nil || name == "" {
		return nil
	}
	var ts tableStyle
	if c, ok := st.custom[name]; ok {
		for el, i := range c.dxf {
			if i >= 0 && i < len(st.dxfs) {
				ts.elements[el], ts.has[el] = st.dxfs[i], true
			}
			ts.size[el] = max(c.size[el], 1)
		}
		return &ts
	}
	preset, ok := presetTableStyles[name]
	if !ok {
		return nil
	}
	for i := range ts.size {
		ts.size[i] = 1
	}
	for _, e := range preset {
		var l look
		if c, ok := st.resolve(e.ink); ok {
			l.inkSet, l.ink = true, c
		}
		if e.filled {
			l.fill = 2
			if c, ok := st.resolve(e.fill); ok {
				l.fill, l.fillColor = 1, c
			}
		}
		ts.elements[e.element], ts.has[e.element] = l, true
	}
	return &ts
}

// at returns what the style gives the cell at row and col of table t:
// each element that applies there, in order, over the ones before it.
func (ts *tableStyle) at(t *declaredTable, row, col int32) look {
	var out look
	r := t.at
	header := row < r.r0+int32(t.headerRows)
	total := row > r.r1-int32(t.totalRows)
	first, last := col == r.c0, col == r.c1
	apply := func(el tableElement) {
		if !ts.has[el] {
			return
		}
		l := ts.elements[el]
		if l.inkSet {
			out.inkSet, out.ink = true, l.ink
		}
		if l.fill != 0 {
			out.fill, out.fillColor = l.fill, l.fillColor
		}
	}
	apply(elWholeTable)
	if t.columnStripes {
		k := int(col - r.c0)
		a, b := ts.size[elFirstColumnStripe], ts.size[elSecondColumnStripe]
		if k%(a+b) < a {
			apply(elFirstColumnStripe)
		} else {
			apply(elSecondColumnStripe)
		}
	}
	if t.rowStripes && !header && !total {
		k := int(row - r.r0 - int32(t.headerRows))
		a, b := ts.size[elFirstRowStripe], ts.size[elSecondRowStripe]
		if k%(a+b) < a {
			apply(elFirstRowStripe)
		} else {
			apply(elSecondRowStripe)
		}
	}
	if t.lastColumn && last {
		apply(elLastColumn)
	}
	if t.firstColumn && first {
		apply(elFirstColumn)
	}
	if header {
		apply(elHeaderRow)
	}
	if total {
		apply(elTotalRow)
	}
	switch {
	case header && first && t.firstColumn:
		apply(elFirstHeaderCell)
	case header && last && t.lastColumn:
		apply(elLastHeaderCell)
	case total && first && t.firstColumn:
		apply(elFirstTotalCell)
	case total && last && t.lastColumn:
		apply(elLastTotalCell)
	}
	return out
}

// applyTableStyles judges again whether the text of each cell of a styled
// table can be seen, against the fill and with the text colour its style
// gives where the cell gives none of its own.
func (sh *sheet) applyTableStyles(tables []declaredTable) {
	cells := sh.cells
	for i := range tables {
		t := &tables[i]
		ts := sh.wb.styles.tableStyle(t.style)
		if ts == nil {
			continue
		}
		lo := sort.Search(len(cells), func(i int) bool { return cells[i].row >= t.at.r0 })
		for j := lo; j < len(cells) && cells[j].row <= t.at.r1; j++ {
			c := &cells[j]
			if c.col < t.at.c0 || c.col > t.at.c1 {
				continue
			}
			own := sh.wb.styles.style(int(c.style))
			c.hidden &^= gofastcore.HiddenLowContrast
			if own.lowContrastOver(ts.at(t, c.row, c.col)) {
				c.hidden |= gofastcore.HiddenLowContrast
			}
		}
	}
}

// lowContrastOver reports whether text in this style is not seen in a
// table whose style gives the cell from.
func (cs *cellStyle) lowContrastOver(from look) bool {
	ink, inkSet := cs.ink, cs.inkSet
	if !inkSet && !cs.inkAuto {
		ink, inkSet = from.ink, from.inkSet
	}
	if !inkSet {
		return false // automatic: dark on light, light on dark
	}
	behind := theme.White
	switch {
	case cs.fill == 1:
		behind = cs.fillColor
	case cs.fill == 2:
		return dml.LowOnMost(ink, 1, cs.stops) // not one colour: judged against a gradient's stops only
	case from.fill == 1:
		behind = from.fillColor
	case from.fill == 2:
		return false
	}
	return gofastcore.LowContrast(ink.R, ink.G, ink.B, behind.R, behind.G, behind.B)
}
