package sml

import (
	"bytes"
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/dml"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/numfmt"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/symbol"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// The size of a sheet: 1,048,576 rows and 16,384 columns. A cell placed
// outside it is not a cell.
const (
	maxRows = 1 << 20
	maxCols = 1 << 14
)

// cell is one cell that shows something. Empty cells are not kept.
type cell struct {
	text   string
	row    int32 // from 0
	col    int32
	style  int32             // its style, for a table's style to be judged against
	hidden gofastcore.Hidden // why its text cannot be seen, where the cell itself is the reason
}

// rect is a rectangle of cells, both corners included.
type rect struct{ r0, c0, r1, c1 int32 }

// span is a run of rows or columns, both ends included.
type span struct{ lo, hi int32 }

// sheet reads one worksheet. Each sheet is read by one goroutine and
// shares nothing that changes with the others, apart from the workbook's
// budgets.
type sheet struct {
	wb   *workbook
	ctx  context.Context
	part gofastcore.Part
	name string // the worksheet's part
	rels map[string]opc.Rel
	list []opc.Rel

	s          xmlscan.Scanner
	cells      []cell
	unsorted   bool
	lastRow    int32
	hiddenRows []int32
	hiddenCols []span
	merges     []rect
	varying    []rect   // where conditional formatting may change how cells look
	varyAll    bool     // or everywhere: a range of it could not be read
	tables     []string // parts of the tables the sheet declares
	drawings   []string

	text   arena
	shapes dml.Text // reads the text of the shapes on the sheet
	// room is what the document may still hold of text: the cells' and
	// the shapes' (gofastcore.Limits.MaxTextBytes).
	room     *gofastcore.TextBudget
	raw, out []byte

	noValue  int   // formulas stored without a result
	removed  int   // control characters taken out
	unmapped int   // codes of symbol fonts with no character
	pending  int64 // text of the cells read and not yet in tables

	// What cells hold besides their text (details.go).
	details         []detail // in the order of the cells, once sortDetails has run
	detailsUnsorted bool
	shared          map[int]sharedFormula
	linkAt          map[pos]string // links of single cells and small ranges
	links           []link         // links of large ranges
	isDate          bool           // the value just read was shown as a date
	fbuf            []byte         // for reading a formula
	symbols         []byte         // for translating text in a symbol font
	split           int            // regions too sparse to be a grid
	unread          map[string]int // content of kinds that are not read
	damaged         []string       // parts that could not be read to their end
	cut             string         // why reading stopped early, if it did

	// Reading a band of rows at a time (see Rows).
	pass   int                // which of the two passes over the part this is
	band   int                // rows to a band; zero when the sheet is read whole
	inBand int                // rows with cells read since the last band
	emit   func() (more bool) // hands the band on
	budget bool               // the decompression budget ran out
	macros bool               // the sheet is a sheet of macros
}

// arena holds the text of a sheet's cells in large pieces, so that a cell
// costs no allocation of its own. A piece is never moved once text has
// been cut from it.
type arena struct {
	b strings.Builder
}

const arenaPiece = 256 << 10

func (a *arena) put(p []byte) string {
	if a.b.Cap()-a.b.Len() < len(p) {
		a.b = strings.Builder{}
		a.b.Grow(max(arenaPiece, len(p)))
	}
	start := a.b.Len()
	a.b.Write(p)
	return a.b.String()[start:]
}

// The passes over a worksheet's part when it is read a band at a time.
const (
	passWhole  = iota // the one pass of reading a sheet whole
	passAround        // everything but the rows: hidden columns, merged cells
	passRows          // the rows
)

// read reads the worksheet part.
func (sh *sheet) read() {
	s := &sh.s
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start || s.Depth() != 2 {
			continue
		}
		if s.Space() != ns.Sheet {
			s.Skip()
			continue
		}
		if sh.pass == passRows && string(s.Local()) != "sheetData" {
			s.Skip() // read by the pass before
			continue
		}
		switch string(s.Local()) {
		case "sheetData":
			if sh.pass == passAround {
				s.Skip()
				continue
			}
			sh.readData()
			if sh.cut != "" {
				return
			}
		case "cols":
			depth := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= depth {
				if s.Kind() != xmlscan.Start || !s.Is(ns.Sheet, "col") {
					continue
				}
				// A hidden column, or one of no width.
				hidden := false
				if v, ok := s.Attr(xmlscan.NoSpace, "hidden"); ok && isTrue(v) {
					hidden = true
				} else if v, ok := s.Attr(xmlscan.NoSpace, "width"); ok && isZero(v) {
					hidden = true
				}
				lo, ok1 := intAttr(s, "min")
				hi, ok2 := intAttr(s, "max")
				if hidden && ok1 && ok2 && lo >= 1 && hi >= lo && len(sh.hiddenCols) < maxCols {
					sh.hiddenCols = append(sh.hiddenCols, span{int32(min(lo, maxCols) - 1), int32(min(hi, maxCols) - 1)})
				}
			}
		case "mergeCells":
			depth := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= depth {
				if s.Kind() != xmlscan.Start || !s.Is(ns.Sheet, "mergeCell") || len(sh.merges) >= maxMerges {
					continue
				}
				if v, ok := s.Attr(xmlscan.NoSpace, "ref"); ok {
					if r, ok := parseRange(v); ok && (r.r1 > r.r0 || r.c1 > r.c0) {
						sh.merges = append(sh.merges, r)
					}
				}
			}
		case "conditionalFormatting":
			// Formatting that depends on values: a cell's colours there
			// are not what its style says.
			if v, ok := s.Attr(xmlscan.NoSpace, "sqref"); ok {
				sh.vary(v)
			}
			s.Skip()
		case "extLst":
			// Newer kinds of conditional formatting are kept here, with
			// their ranges as text.
			depth := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= depth {
				if s.Kind() == xmlscan.Start && string(s.Local()) == "sqref" {
					sh.raw = s.AppendText(sh.raw[:0])
					sh.vary(sh.raw)
				}
			}
		case "hyperlinks":
			if sh.wb.opts.Spans {
				sh.readLinks(s)
			} else {
				s.Skip()
			}
		case "tableParts":
			depth := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= depth {
				if s.Kind() == xmlscan.Start && s.Is(ns.Sheet, "tablePart") && len(sh.tables) < maxTables {
					if target, ok := sh.target(ns.RelTable); ok {
						sh.tables = append(sh.tables, target)
					}
				}
			}
		case "drawing":
			if target, ok := sh.target(ns.RelDrawing); ok && len(sh.drawings) < 16 {
				sh.drawings = append(sh.drawings, target)
			}
			s.Skip()
		default:
			s.Skip()
		}
	}
}

const (
	maxMerges  = 1 << 18
	maxTables  = 1 << 12
	maxVarying = 1 << 12
)

// vary records the ranges of a conditional format: a list such as
// "A1:B9 D4".
func (sh *sheet) vary(sqref []byte) {
	for _, ref := range bytes.Fields(sqref) {
		r, ok := parseRange(ref)
		if !ok || len(sh.varying) >= maxVarying {
			sh.varyAll = true
			return
		}
		sh.varying = append(sh.varying, r)
	}
}

// unjudge takes back the judgement that a cell's text is in the colour of
// its fill wherever conditional formatting applies: the colours shown
// there depend on the values, and are not worked out.
func (sh *sheet) unjudge() {
	if !sh.varyAll && len(sh.varying) == 0 {
		return
	}
	var judged []int
	for i := range sh.cells {
		if sh.cells[i].hidden&gofastcore.HiddenLowContrast != 0 {
			judged = append(judged, i)
		}
	}
	all := sh.varyAll || len(judged)*len(sh.varying) > 1<<22
	for _, i := range judged {
		c := &sh.cells[i]
		clear := all
		for k := 0; !clear && k < len(sh.varying); k++ {
			r := sh.varying[k]
			clear = c.row >= r.r0 && c.row <= r.r1 && c.col >= r.c0 && c.col <= r.c1
		}
		if clear {
			c.hidden &^= gofastcore.HiddenLowContrast
		}
	}
}

// target returns the part that the current element's r:id names, if the
// relationship is of the given kind and stays inside the package.
func (sh *sheet) target(kind string) (string, bool) {
	id, ok := sh.s.Attr(ns.Rel, "id")
	if !ok {
		return "", false
	}
	rel, found := sh.rels[string(id)]
	if !found || rel.External || !ns.IsRel(rel.Type, kind) {
		return "", false
	}
	return rel.Target, true
}

func isZero(v []byte) bool {
	f, err := strconv.ParseFloat(string(v), 64)
	return err == nil && f == 0
}

// readData reads sheetData: the rows and their cells.
func (sh *sheet) readData() {
	s := &sh.s
	depth := s.Depth()
	row := int32(-1)
	for n := 0; s.Next() != xmlscan.EOF && s.Depth() >= depth; n++ {
		if s.Kind() != xmlscan.Start {
			continue
		}
		if s.Depth() != depth+1 || !s.Is(ns.Sheet, "row") {
			s.Skip()
			continue
		}
		if n&1023 == 0 && sh.ctx.Err() != nil {
			sh.cut = "cancelled"
			return
		}
		// A row says its number; one that does not follows the row before.
		row++
		if v, ok := s.Attr(xmlscan.NoSpace, "r"); ok {
			if r, ok := atoi(v); ok && r >= 1 {
				row = int32(min(r, maxRows+1) - 1)
			}
		}
		if row >= maxRows {
			s.Skip()
			continue
		}
		if row < sh.lastRow {
			sh.unsorted = true
		}
		sh.lastRow = row
		hidden := false
		if v, ok := s.Attr(xmlscan.NoSpace, "hidden"); ok && isTrue(v) {
			hidden = true
		} else if v, ok := s.Attr(xmlscan.NoSpace, "ht"); ok && isZero(v) {
			hidden = true // a row of no height
		}
		first := len(sh.cells)
		bytesBefore := sh.readRow(row)
		if n := len(sh.cells) - first; n > 0 {
			if hidden {
				sh.hiddenRows = append(sh.hiddenRows, row)
			}
			if sh.band > 0 {
				// A band is handed on and forgotten: what is kept does
				// not grow with the sheet, and is not counted.
				if sh.inBand++; sh.inBand >= sh.band {
					sh.inBand = 0
					if !sh.emit() {
						sh.cut = "stopped"
						return
					}
				}
				continue
			}
			// What is kept is charged to the document's budgets, a row
			// at a time.
			if sh.wb.cells.Add(-int64(n)) < 0 {
				sh.cells = sh.cells[:first]
				sh.cut = "the limit on the number of cells was reached"
				return
			}
			// The text is taken from the budget when the tables are
			// made, with what their blocks and cells take; reading stops
			// here once there could be no room for it.
			if sh.pending += bytesBefore; sh.pending > sh.room.Left() {
				sh.cells = sh.cells[:first]
				sh.cut = "the limit on the amount of text was reached"
				return
			}
		}
	}
}

// readRow reads the cells of the row whose Start token is current, and
// returns the number of bytes of text they hold.
func (sh *sheet) readRow(row int32) (textBytes int64) {
	s := &sh.s
	depth := s.Depth()
	col := int32(-1)
	ordered := true
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		if s.Depth() != depth+1 || !s.Is(ns.Sheet, "c") {
			s.Skip()
			continue
		}
		// A cell says where it is; one that does not follows the cell
		// before. Its row is the row it is written in.
		prev := col
		col++
		if v, ok := s.Attr(xmlscan.NoSpace, "r"); ok {
			if c, _, ok := parseRef(v); ok {
				col = c
			}
		}
		typ := byte('n')
		if v, ok := s.Attr(xmlscan.NoSpace, "t"); ok && len(v) > 0 {
			switch string(v) {
			case "s":
				typ = 's'
			case "inlineStr":
				typ = 'i'
			case "str":
				typ = 't'
			case "b":
				typ = 'b'
			case "e":
				typ = 'e'
			case "d":
				typ = 'd'
			}
		}
		style, styleIndex := &plainStyle, int32(-1)
		if v, ok := s.Attr(xmlscan.NoSpace, "s"); ok {
			if i, ok := atoi(v); ok {
				style = sh.wb.styles.style(i)
				if style != &plainStyle {
					styleIndex = int32(i)
				}
			}
		}
		// The value: v, or an inline string; the formula with
		// Options.Cells.
		raw := sh.raw[:0]
		formula, value := false, false
		f := ""
		cellDepth := s.Depth()
		for s.Next() != xmlscan.EOF && s.Depth() >= cellDepth {
			if s.Kind() != xmlscan.Start {
				continue
			}
			switch {
			case s.Is(ns.Sheet, "v"):
				raw = s.AppendText(raw)
				value = true
			case s.Is(ns.Sheet, "is"):
				raw = readRichText(s, raw)
				value = true
			case s.Is(ns.Sheet, "f"):
				formula = true
				if sh.wb.opts.Cells {
					f = sh.formulaOf(s, row, col)
				} else {
					s.Skip()
				}
			default:
				s.Skip()
			}
		}
		sh.raw = raw
		if s.Err() != nil {
			// The file broke off inside this cell: what was read of its
			// value may be part of it, and part of a number is another
			// number.
			break
		}
		if col >= maxCols {
			continue
		}
		if !value || len(raw) == 0 {
			if formula {
				sh.noValue++
			}
			continue
		}
		sh.isDate = false
		text, hidden := sh.value(typ, style, raw)
		if text == "" {
			continue
		}
		if col <= prev {
			ordered = false
		}
		textBytes += int64(len(text))
		sh.cells = append(sh.cells, cell{text: text, row: row, col: col, style: styleIndex, hidden: hidden | style.hidden})
		if sh.wb.opts.Cells {
			sh.keepDetail(&sh.cells[len(sh.cells)-1], typ, raw, f, sh.isDate)
		}
	}
	if !ordered {
		sh.unsorted = true
	}
	return textBytes
}

var (
	textTrue  = "TRUE"
	textFalse = "FALSE"
)

// symbolText returns text in a cell whose font is a symbol font: each
// character is a code of the font ("ü" in Wingdings is a check mark).
func (sh *sheet) symbolText(f symbol.Font, text []byte) string {
	var missing int
	sh.symbols, missing = f.Translate(sh.symbols[:0], text)
	sh.unmapped += missing
	return string(sh.symbols)
}

// value returns what a cell shows, given what it holds.
func (sh *sheet) value(typ byte, style *cellStyle, raw []byte) (string, gofastcore.Hidden) {
	switch typ {
	case 's':
		i, ok := atoi(bytes.TrimSpace(raw))
		if !ok {
			return "", 0
		}
		text, _ := sh.wb.strings.at(i)
		if style.symbol != symbol.None && text != "" {
			text = sh.symbolText(style.symbol, []byte(text))
		}
		if text == "" || style.format.PlainText() {
			return text, 0
		}
		out, result := style.format.Text(sh.out[:0], text)
		sh.out = out
		return sh.text.put(bytes.TrimSpace(out)), blank(result)
	case 'i', 't', 'e', 'd':
		out, removed := cellText(sh.out[:0], raw)
		sh.removed += removed
		if style.symbol != symbol.None && (typ == 'i' || typ == 't') && len(out) > 0 {
			out = append(out[:0], sh.symbolText(style.symbol, out)...)
		}
		var hidden gofastcore.Hidden
		if (typ == 'i' || typ == 't') && len(out) > 0 && !style.format.PlainText() {
			var result numfmt.Result
			raw = append(raw[:0], out...)
			out, result = style.format.Text(out[:0], string(raw))
			out = bytes.TrimSpace(out)
			hidden = blank(result)
		}
		sh.out = out
		return sh.text.put(out), hidden
	case 'b':
		if v := bytes.TrimSpace(raw); len(v) == 1 && v[0] == '0' || string(v) == "false" {
			return textFalse, 0
		}
		return textTrue, 0
	}
	// A number.
	raw = bytes.TrimSpace(raw)
	if style.general && plainNumber(raw) {
		return sh.text.put(raw), 0
	}
	v, err := strconv.ParseFloat(string(raw), 64)
	if err != nil {
		// Not a number, whatever the cell says: its text.
		out, removed := cellText(sh.out[:0], raw)
		sh.removed += removed
		sh.out = out
		return sh.text.put(out), 0
	}
	out, result := style.format.Number(sh.out[:0], v, sh.wb.date1904)
	sh.isDate = result == numfmt.Date
	sh.out = out
	return sh.text.put(out), blank(result)
}

// blank is the reason a value that its format displays as nothing cannot
// be seen.
func blank(r numfmt.Result) gofastcore.Hidden {
	if r == numfmt.Blank {
		return gofastcore.HiddenInvisible
	}
	return 0
}

// plainNumber reports whether a number is written in the file exactly as
// the General format shows it, which most are: digits, with at most a
// minus sign in front and one point inside, no more than fifteen of them,
// and no zeros that the format would drop.
func plainNumber(b []byte) bool {
	if len(b) == 0 || len(b) > 17 {
		return false
	}
	i := 0
	if b[0] == '-' {
		i = 1
	}
	start, point, digits, zeros := i, -1, 0, 0
	nonZero := false
	for ; i < len(b); i++ {
		switch c := b[i]; {
		case c >= '0' && c <= '9':
			digits++
			if c != '0' {
				nonZero = true
			} else if point >= 0 && !nonZero {
				zeros++ // a zero between the point and the first digit that counts
			}
		case c == '.' && point < 0:
			point = i
		default:
			return false
		}
	}
	switch {
	case digits == 0 || digits > 15:
		return false
	case point < 0:
		// Whole: no leading zero, and not minus zero.
		return b[start] != '0' || len(b) == 1
	case point == start || point == len(b)-1 || b[len(b)-1] == '0':
		return false // ".5", "5." and "5.50" are shown otherwise
	case b[start] == '0' && point != start+1:
		return false // "007.5"
	case !nonZero || zeros >= 9:
		return false // zero, or small enough for scientific notation
	}
	return true
}

// parseRef reads a cell reference such as "B12" or "$B$12": column and
// row, from 0.
func parseRef(b []byte) (col, row int32, ok bool) {
	i := 0
	if i < len(b) && b[i] == '$' {
		i++
	}
	c := 0
	letters := 0
	for ; i < len(b) && letters < 4; i++ {
		ch := b[i] | 0x20
		if ch < 'a' || ch > 'z' {
			break
		}
		c = c*26 + int(ch-'a') + 1
		letters++
	}
	if letters == 0 || letters > 3 {
		return 0, 0, false
	}
	if i < len(b) && b[i] == '$' {
		i++
	}
	r := 0
	digits := 0
	for ; i < len(b); i++ {
		if b[i] < '0' || b[i] > '9' || digits >= 8 {
			return 0, 0, false
		}
		r = r*10 + int(b[i]-'0')
		digits++
	}
	if digits == 0 || r < 1 || r > maxRows || c > maxCols {
		return 0, 0, false
	}
	return int32(c - 1), int32(r - 1), true
}

// parseRange reads a range such as "A1:C4", or a single cell.
func parseRange(b []byte) (rect, bool) {
	first, second, two := bytes.Cut(b, []byte(":"))
	c0, r0, ok := parseRef(first)
	if !ok {
		return rect{}, false
	}
	c1, r1 := c0, r0
	if two {
		if c1, r1, ok = parseRef(second); !ok {
			return rect{}, false
		}
	}
	return rect{min(r0, r1), min(c0, c1), max(r0, r1), max(c0, c1)}, true
}

// refName writes a cell's place as a reference: column letters and row
// number.
func refName(dst []byte, row, col int32) []byte {
	var letters [3]byte
	n := 0
	for c := col + 1; c > 0; c = (c - 1) / 26 {
		letters[n] = byte('A' + (c-1)%26)
		n++
	}
	for n > 0 {
		n--
		dst = append(dst, letters[n])
	}
	return strconv.AppendInt(dst, int64(row)+1, 10)
}

func rangeName(r rect) string {
	var buf [24]byte
	b := refName(buf[:0], r.r0, r.c0)
	b = append(b, ':')
	b = refName(b, r.r1, r.c1)
	return string(b)
}

func cellName(row, col int32) string {
	var buf [12]byte
	return string(refName(buf[:0], row, col))
}

// order puts the cells in reading order, by row and then by column, if
// the file did not, and keeps the first of two cells written for one
// place. It also puts the hidden columns in order, as runs that do not
// touch.
func (sh *sheet) order() {
	if len(sh.hiddenCols) > 1 {
		sort.Slice(sh.hiddenCols, func(i, j int) bool { return sh.hiddenCols[i].lo < sh.hiddenCols[j].lo })
		kept := sh.hiddenCols[:1]
		for _, c := range sh.hiddenCols[1:] {
			if last := &kept[len(kept)-1]; c.lo <= last.hi+1 {
				last.hi = max(last.hi, c.hi)
			} else {
				kept = append(kept, c)
			}
		}
		sh.hiddenCols = kept
	}
	if !sh.unsorted {
		return
	}
	sort.SliceStable(sh.cells, func(i, j int) bool {
		a, b := &sh.cells[i], &sh.cells[j]
		return a.row < b.row || a.row == b.row && a.col < b.col
	})
	kept := sh.cells[:0]
	for i := range sh.cells {
		if n := len(kept); n > 0 && kept[n-1].row == sh.cells[i].row && kept[n-1].col == sh.cells[i].col {
			continue
		}
		kept = append(kept, sh.cells[i])
	}
	sh.cells = kept
	sort.Slice(sh.hiddenRows, func(i, j int) bool { return sh.hiddenRows[i] < sh.hiddenRows[j] })
	rows := sh.hiddenRows[:0]
	for i, r := range sh.hiddenRows {
		if i == 0 || r != sh.hiddenRows[i-1] {
			rows = append(rows, r)
		}
	}
	sh.hiddenRows = rows
}
