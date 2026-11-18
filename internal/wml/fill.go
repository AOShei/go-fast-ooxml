package wml

import (
	"bytes"
	"strings"

	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// What lies behind text decides whether its colour can be seen: white
// text is hidden on a white page and plain to read in a dark blue box.
// This file reads the things that can lie behind text: shading, the fill
// of a shape, and the shading a table's style gives its cells.
//
// Where what is behind cannot be worked out (a gradient, a picture, a
// colour the theme adjusts), it is recorded as unknown, and text in front
// of it is not judged. A flag is a statement of fact; a guess is not one.

var (
	black   = color{set: true}
	unknown = color{set: true, unknown: true}
)

// known reports whether c is a definite colour.
func (c color) known() bool { return c.set && !c.auto && !c.unknown }

// readShade reads the colour of a shading element (w:shd). Shading is a
// pattern in one colour over a fill in another: "clear" is the fill
// alone, "solid" the pattern colour alone, "pct25" a quarter of one over
// the other.
func readShade(s *xmlscan.Scanner, theme map[string]color) color {
	fill := readColor(s, "fill", "themeFill", theme)
	val, _ := s.Attr(ns.Word, "val")
	pattern := string(val)
	if pattern == "" || pattern == "clear" || pattern == "nil" {
		return fill
	}
	ink := readColor(s, "color", "themeColor", theme)
	if !ink.known() {
		ink = black
	}
	if pattern == "solid" {
		return ink
	}
	if percent, ok := strings.CutPrefix(pattern, "pct"); ok {
		n, ok := atoi([]byte(percent))
		if !ok || n < 0 || n > 100 {
			return unknown
		}
		if !fill.known() {
			fill = white
		}
		mix := func(a, b uint8) uint8 { return uint8((int(a)*n + int(b)*(100-n)) / 100) }
		return color{set: true, r: mix(ink.r, fill.r), g: mix(ink.g, fill.g), b: mix(ink.b, fill.b)}
	}
	return unknown // stripes, checks and the like
}

// drawingNames maps the names of theme colours in a drawing to the names
// of the colours in the theme part.
var drawingNames = map[string]string{
	"dk1": "dk1", "lt1": "lt1", "dk2": "dk2", "lt2": "lt2",
	"tx1": "dk1", "bg1": "lt1", "tx2": "dk2", "bg2": "lt2",
	"accent1": "accent1", "accent2": "accent2", "accent3": "accent3",
	"accent4": "accent4", "accent5": "accent5", "accent6": "accent6",
	"hlink": "hlink", "folHlink": "folHlink",
}

// drawingColor reads the colour inside a drawing's fill element, such as
// a:solidFill or a:fillRef, and consumes the element. A colour that is
// adjusted (lightened, darkened, made partly transparent) is unknown: the
// adjustments are not computed.
func drawingColor(s *xmlscan.Scanner, theme map[string]color) color {
	c := unknown
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		if s.Depth() > depth+1 {
			c = unknown // an adjustment of the colour
			continue
		}
		if s.Space() != ns.Draw {
			continue
		}
		switch string(s.Local()) {
		case "srgbClr":
			v, _ := s.Attr(xmlscan.NoSpace, "val")
			if rgb, ok := hexColor(v); ok {
				c = rgb
			}
		case "sysClr":
			v, _ := s.Attr(xmlscan.NoSpace, "lastClr")
			if rgb, ok := hexColor(v); ok {
				c = rgb
			}
		case "schemeClr":
			v, _ := s.Attr(xmlscan.NoSpace, "val")
			if rgb, ok := theme[drawingNames[string(v)]]; ok {
				c = rgb
			}
		}
	}
	return c
}

// vmlColors are the colour names legacy drawings use.
var vmlColors = map[string]color{
	"black": {set: true}, "white": {set: true, r: 255, g: 255, b: 255},
	"red": {set: true, r: 255}, "lime": {set: true, g: 255}, "blue": {set: true, b: 255},
	"yellow": {set: true, r: 255, g: 255}, "aqua": {set: true, g: 255, b: 255}, "fuchsia": {set: true, r: 255, b: 255},
	"maroon": {set: true, r: 128}, "green": {set: true, g: 128}, "navy": {set: true, b: 128},
	"olive": {set: true, r: 128, g: 128}, "teal": {set: true, g: 128, b: 128}, "purple": {set: true, r: 128, b: 128},
	"silver": {set: true, r: 192, g: 192, b: 192}, "gray": {set: true, r: 128, g: 128, b: 128},
}

// vmlColor reads a colour as legacy drawings write it: "#1f3864", "#abc",
// a name, any of them followed by a note in brackets.
func vmlColor(v []byte) color {
	v = bytes.TrimSpace(v)
	if i := bytes.IndexByte(v, ' '); i >= 0 {
		v = v[:i]
	}
	if len(v) > 0 && v[0] == '#' {
		v = v[1:]
		if len(v) == 3 {
			v = []byte{v[0], v[0], v[1], v[1], v[2], v[2]}
		}
		if c, ok := hexColor(v); ok {
			return c
		}
		return unknown
	}
	if c, ok := vmlColors[strings.ToLower(string(v))]; ok {
		return c
	}
	return unknown
}

// vmlFill reads the fill of a legacy shape from its attributes. A legacy
// shape is filled white unless it says otherwise.
func vmlFill(s *xmlscan.Scanner) color {
	fill := white
	if v, ok := s.Attr(xmlscan.NoSpace, "fillcolor"); ok {
		fill = vmlColor(v)
	}
	if v, ok := s.Attr(xmlscan.NoSpace, "filled"); ok && isOff(v) {
		fill = color{}
	}
	return fill
}

// vmlFillElement applies a v:fill element to the fill of its shape.
func vmlFillElement(s *xmlscan.Scanner, fill color) color {
	if v, ok := s.Attr(xmlscan.NoSpace, "on"); ok && isOff(v) {
		return color{}
	}
	if v, ok := s.Attr(xmlscan.NoSpace, "color"); ok {
		fill = vmlColor(v)
	}
	if v, ok := s.Attr(xmlscan.NoSpace, "type"); ok && string(v) != "solid" {
		return unknown // a gradient, a pattern, a picture
	}
	if _, ok := s.Attr(xmlscan.NoSpace, "opacity"); ok {
		return unknown
	}
	return fill
}

func isOff(v []byte) bool {
	switch string(v) {
	case "f", "false", "0", "off":
		return true
	}
	return false
}

// Table styles. A table's style shades its cells by where they are: the
// first row, alternate bands of rows, the last column. The conditions are
// numbered from the weakest to the strongest, which is the order in which
// Word lets one override another.
const (
	condWhole = iota
	condBand1Vert
	condBand2Vert
	condBand1Horz
	condBand2Horz
	condFirstCol
	condLastCol
	condFirstRow
	condLastRow
	condNWCell
	condNECell
	condSWCell
	condSECell
	condCount
)

// condNames are the names of the conditions in a table style.
var condNames = map[string]int{
	"wholeTable": condWhole, "band1Vert": condBand1Vert, "band2Vert": condBand2Vert,
	"band1Horz": condBand1Horz, "band2Horz": condBand2Horz, "firstCol": condFirstCol,
	"lastCol": condLastCol, "firstRow": condFirstRow, "lastRow": condLastRow,
	"nwCell": condNWCell, "neCell": condNECell, "swCell": condSWCell, "seCell": condSECell,
}

// cnfOrder lists the conditions in the order of the digits of w:cnfStyle's
// value, with the name of the attribute that says the same.
var cnfOrder = [...]struct {
	cond int
	attr string
}{
	{condFirstRow, "firstRow"}, {condLastRow, "lastRow"}, {condFirstCol, "firstColumn"}, {condLastCol, "lastColumn"},
	{condBand1Vert, "oddVBand"}, {condBand2Vert, "evenVBand"}, {condBand1Horz, "oddHBand"}, {condBand2Horz, "evenHBand"},
	{condNWCell, "firstRowFirstColumn"}, {condNECell, "firstRowLastColumn"}, {condSWCell, "lastRowFirstColumn"}, {condSECell, "lastRowLastColumn"},
}

// tableStyle is the shading a table style gives cells.
type tableStyle struct {
	based            string
	shade            [condCount]color
	rowBand, colBand int // rows and columns to a band; 0 is not set
	done             bool
}

// Options of a table (w:tblLook) that switch parts of its style on.
const (
	lookFirstRow = 0x0020
	lookLastRow  = 0x0040
	lookFirstCol = 0x0080
	lookLastCol  = 0x0100
	lookNoHBand  = 0x0200
	lookNoVBand  = 0x0400
)

// tableState is what the cells of the table being read need to know.
type tableState struct {
	style  *tableStyle
	look   int
	shade  color // the table's own shading
	row    int   // index of the row being read
	col    int   // grid column of the cell being read
	rowCnf int   // conditions the row says apply to it, as a bit set; with bit condCount if it says
}

// readCnf reads w:cnfStyle: the conditions of the table style that apply
// to a row or a cell, as Word worked them out. The result has bit
// condCount set, to tell "none apply" from "not stated".
func readCnf(s *xmlscan.Scanner) int {
	cnf := 1 << condCount
	if v, ok := s.Attr(ns.Word, "val"); ok && len(v) == len(cnfOrder) {
		for i, c := range v {
			if c == '1' {
				cnf |= 1 << cnfOrder[i].cond
			}
		}
		return cnf
	}
	for _, c := range cnfOrder {
		if v, ok := s.Attr(ns.Word, c.attr); ok && isOn(v) {
			cnf |= 1 << c.cond
		}
	}
	return cnf
}

// readLook reads w:tblLook.
func readLook(s *xmlscan.Scanner) int {
	look := 0
	if v, ok := s.Attr(ns.Word, "val"); ok && len(v) <= 8 {
		for _, c := range v {
			d := 0
			switch {
			case c >= '0' && c <= '9':
				d = int(c - '0')
			case c >= 'a' && c <= 'f':
				d = int(c-'a') + 10
			case c >= 'A' && c <= 'F':
				d = int(c-'A') + 10
			}
			look = look<<4 | d
		}
	}
	for _, o := range [...]struct {
		attr string
		bit  int
	}{{"firstRow", lookFirstRow}, {"lastRow", lookLastRow}, {"firstColumn", lookFirstCol}, {"lastColumn", lookLastCol}, {"noHBand", lookNoHBand}, {"noVBand", lookNoVBand}} {
		if v, ok := s.Attr(ns.Word, o.attr); ok {
			if isOn(v) {
				look |= o.bit
			} else {
				look &^= o.bit
			}
		}
	}
	return look
}

// styleShade returns the shading the table's style gives the cell being
// read, if it gives any. cellCnf is what the cell says of itself.
func (t *tableState) styleShade(cellCnf int) (color, bool) {
	st := t.style
	if st == nil {
		return color{}, false
	}
	conds := 1 << condWhole
	if stated := t.rowCnf | cellCnf; stated != 0 {
		conds |= stated
	} else {
		// The file does not say, so go by position. Whether this is the
		// last row or column is not known while the table is being read;
		// those two conditions are left out.
		firstRow := t.look&lookFirstRow != 0 && t.row == 0
		firstCol := t.look&lookFirstCol != 0 && t.col == 0
		if firstRow {
			conds |= 1 << condFirstRow
		}
		if firstCol {
			conds |= 1 << condFirstCol
		}
		if t.look&lookNoHBand == 0 && !firstRow {
			row := t.row
			if t.look&lookFirstRow != 0 {
				row--
			}
			if row/max(st.rowBand, 1)%2 == 0 {
				conds |= 1 << condBand1Horz
			} else {
				conds |= 1 << condBand2Horz
			}
		}
		if t.look&lookNoVBand == 0 && !firstCol {
			col := t.col
			if t.look&lookFirstCol != 0 {
				col--
			}
			if col/max(st.colBand, 1)%2 == 0 {
				conds |= 1 << condBand1Vert
			} else {
				conds |= 1 << condBand2Vert
			}
		}
	}
	for cond := condCount - 1; cond >= 0; cond-- {
		if conds&(1<<cond) != 0 && st.shade[cond].set {
			return st.shade[cond], !st.shade[cond].auto
		}
	}
	return color{}, false
}
