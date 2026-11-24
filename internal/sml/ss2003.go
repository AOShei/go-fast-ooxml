package sml

import (
	"bytes"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/formula"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/numfmt"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/symbol"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
	"github.com/AOShei/go-fast-ooxml/internal/xmlindex"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// SpreadsheetML 2003 is the XML spreadsheet of Excel 2002 and 2003 ("XML
// Spreadsheet 2003"): one XML file whose root is Workbook, in the
// namespace urn:schemas-microsoft-com:office:spreadsheet. It is a schema
// of its own, older than .xlsx, but what it says of cells is what .xlsx
// says, and it is read into the same sheets: number formats, cell types,
// merged cells, hidden rows and columns, comments, links and drop-down
// lists go through the code .xlsx and .xlsb go through.
//
// The file is indexed once (xmlindex): its worksheets, styles, names and
// properties are elements, each read as a part of a package made of them
// (opc.NewSections), a sheet at a time as the sheets of .xlsx are.
//
// What differs from .xlsx:
//
//   - Rows and cells follow one another; ss:Index (from 1) jumps ahead. A
//     cell with no index comes after the cell before it, and after any
//     cells merged with it or with a cell above.
//   - A cell's style is named (ss:StyleID), and inherits through
//     ss:Parent; a cell with none has its row's, then its column's, then
//     the style named Default.
//   - Number formats are format codes, or names ("Short Date", "Fixed",
//     "Currency"): see namedFormat.
//   - Dates are written as dates (ss:Type DateTime, ISO 8601), and read
//     into the serial numbers .xlsx holds.
//   - Merged cells are the extra cells a cell covers: ss:MergeAcross and
//     ss:MergeDown.
//   - Formulas are in R1C1 notation, read into A1 notation
//     (formula.FromR1C1); so are defined names and the cells a drop-down
//     list is for.
//   - Comments are inside their cells, links are a cell's ss:HRef.

// Parts of the package made of a SpreadsheetML 2003 file.
const (
	ssWorkbook = "excelworkbook"
	ssStyles   = "styles"
	ssNames    = "names"
	// SSProps and SSCustom are the document's properties.
	SSProps  = "documentproperties"
	SSCustom = "customdocumentproperties"
)

func ssSheet(i int) string        { return "worksheet" + strconv.Itoa(i+1) }
func ssSheetOptions(i int) string { return ssSheet(i) + ".options" }
func ssSheetNames(i int) string   { return ssSheet(i) + ".names" }

// Namespaces of SpreadsheetML 2003, as URIs.
const (
	ssNS     = "urn:schemas-microsoft-com:office:spreadsheet"
	excelNS  = "urn:schemas-microsoft-com:office:excel"
	officeNS = "urn:schemas-microsoft-com:office:office"
)

// SSLayout is what the index of a SpreadsheetML 2003 file says: the
// stretches of the file to read as parts, and the sheets' names.
type SSLayout struct {
	Sections []opc.Section
	sheets   []string
	past     int64 // sheets past maxSheets
}

// SSDescend says which elements of a SpreadsheetML 2003 file are gone
// into when it is indexed: the root, and each worksheet, to find its
// options and names.
func SSDescend(e *xmlindex.Element, depth int) bool {
	return depth == 2 && e.Is(ssNS, "Worksheet")
}

// SSIndex lays out the parts of a SpreadsheetML 2003 file from its index.
func SSIndex(x *xmlindex.Index) *SSLayout {
	l := &SSLayout{}
	add := func(name string, e *xmlindex.Element) {
		l.Sections = append(l.Sections, opc.Section{Name: name, Off: e.Start, N: e.End - e.Start, Bindings: e.Outer})
	}
	for _, e := range x.Root.Children {
		if e.End == 0 {
			continue // the file breaks off inside it
		}
		switch {
		case e.Is(ssNS, "Worksheet"):
			if len(l.sheets) >= maxSheets {
				l.past++
				continue
			}
			i := len(l.sheets)
			name, _ := e.Attr(ssNS, "Name")
			name, _ = clean.String([]byte(name), clean.Prose)
			l.sheets = append(l.sheets, name)
			add(ssSheet(i), e)
			for _, c := range e.Children {
				switch {
				case c.End == 0:
				case c.Is(excelNS, "WorksheetOptions"):
					add(ssSheetOptions(i), c)
				case c.Is(ssNS, "Names"):
					add(ssSheetNames(i), c)
				}
			}
		case e.Is(excelNS, "ExcelWorkbook"):
			add(ssWorkbook, e)
		case e.Is(ssNS, "Styles"):
			add(ssStyles, e)
		case e.Is(ssNS, "Names"):
			add(ssNames, e)
		case e.Is(officeNS, "DocumentProperties"):
			add(SSProps, e)
		case e.Is(officeNS, "CustomDocumentProperties"):
			add(SSCustom, e)
		}
	}
	return l
}

// OpenSS opens a SpreadsheetML 2003 workbook, whose parts pkg holds as
// layout says, as Open does an .xlsx one.
func OpenSS(pkg *opc.Package, layout *SSLayout, opts *gofastcore.Options, cfg Config) *Book {
	wb := &workbook{pkg: pkg, opts: opts, ss: true, images: map[string]*gofastcore.Image{}}
	b := &Book{wb: wb, cfg: cfg}
	b.docLost.Add(warn.CapSheets, layout.past)
	load := func(name string, read func(*xmlscan.Scanner)) {
		if b.budget || !pkg.Has(name) {
			return
		}
		var s xmlscan.Scanner
		c, err := pkg.Scan(name, &s, scanOptions)
		if err != nil {
			b.damaged = append(b.damaged, name)
			return
		}
		read(&s)
		c.Close()
		switch err := s.Err(); {
		case err == nil:
		case errors.Is(err, opc.ErrBudget):
			b.budget = true
		default:
			b.damaged = append(b.damaged, name)
		}
	}
	load(ssWorkbook, func(s *xmlscan.Scanner) {
		for s.Next() != xmlscan.EOF {
			if s.Kind() == xmlscan.Start && s.Depth() == 2 && s.Is(ns.Excel2003, "Date1904") {
				v := bytes.TrimSpace(s.AppendText(nil))
				wb.date1904 = len(v) == 0 || isTrue(v) || string(v) == "True"
			}
		}
	})
	for i, name := range layout.sheets {
		ref := sheetRef{name: name, target: ssSheet(i)}
		load(ssSheetOptions(i), func(s *xmlscan.Scanner) {
			for s.Next() != xmlscan.EOF {
				if s.Kind() == xmlscan.Start && s.Depth() == 2 && s.Is(ns.Excel2003, "Visible") {
					v := string(bytes.TrimSpace(s.AppendText(nil)))
					ref.hidden = v == "SheetHidden" || v == "SheetVeryHidden"
				}
			}
		})
		b.refs = append(b.refs, ref)
	}
	// Names, which the stored part gives: the workbook's, then each
	// sheet's, with the sheet's name in front.
	load(ssNames, func(s *xmlscan.Scanner) { b.readSSNames(s, "") })
	for i, ref := range b.refs {
		load(ssSheetNames(i), func(s *xmlscan.Scanner) { b.readSSNames(s, ref.name) })
	}
	wanted := false
	for i := range b.refs {
		wanted = wanted || opts.Parts.Contains(i+1)
	}
	if wanted {
		load(ssStyles, func(s *xmlscan.Scanner) { wb.styles, wb.ssStyles = readSSStyles(s, &b.docLost) })
	}
	b.after = pkg.Budget()
	b.Begin()
	return b
}

// readSSNames reads NamedRange elements: a name, and what it refers to
// in R1C1 notation, absolute, read into A1.
func (b *Book) readSSNames(s *xmlscan.Scanner, sheet string) {
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start || !s.Is(ns.SS2003, "NamedRange") {
			continue
		}
		if len(b.names) >= maxNames {
			b.lost.Add(warn.CapNames, 1)
			continue
		}
		v, _ := s.Attr(ns.SS2003, "Name")
		name := b.lost.Name(string(v))
		refers, _ := s.Attr(ns.SS2003, "RefersTo")
		b.raw = append(b.raw[:0], bytes.TrimPrefix(bytes.TrimSpace(refers), []byte("="))...)
		f := verbatim(b.raw, &b.lost)
		if name == "" || f == "" {
			continue
		}
		if sheet != "" {
			name = sheet + "!" + name
		}
		b.names = append(b.names, definedName{name: name, formula: formula.FromR1C1(f, 0, 0)})
	}
}

// ssStyle is a style as the file writes it, before inheritance.
type ssStyle struct {
	parent       string
	format, font string
	hasFormat    bool
	hasFont      bool
}

// readSSStyles reads the Styles element: each style's number format and
// font, inherited through ss:Parent. It returns the styles and their
// numbers by ID.
func readSSStyles(s *xmlscan.Scanner, lost *warn.Lost) (*styleSheet, map[string]int) {
	written := map[string]*ssStyle{}
	var order []string
	var cur *ssStyle
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Depth() == 2 && s.Is(ns.SS2003, "Style"):
			cur = nil
			id, _ := s.Attr(ns.SS2003, "ID")
			if len(id) == 0 || written[string(id)] != nil {
				continue
			}
			if len(order) >= maxStyles {
				lost.Add(warn.CapCellStyles, 1)
				continue
			}
			cur = &ssStyle{}
			if v, ok := s.Attr(ns.SS2003, "Parent"); ok {
				cur.parent = string(v)
			}
			written[string(id)] = cur
			order = append(order, string(id))
		case cur != nil && s.Depth() == 3 && s.Is(ns.SS2003, "NumberFormat"):
			v, ok := s.Attr(ns.SS2003, "Format")
			// Text a format writes into a cell is text like any other.
			cur.format, _ = clean.String(v, clean.Ligatures)
			cur.hasFormat = ok
		case cur != nil && s.Depth() == 3 && s.Is(ns.SS2003, "Font"):
			if v, ok := s.Attr(ns.SS2003, "FontName"); ok {
				cur.font, cur.hasFont = string(v), true
			}
		}
	}
	st := &styleSheet{styles: make([]cellStyle, len(order))}
	byID := make(map[string]int, len(order))
	formats := map[string]*numfmt.Format{}
	// What a style does not say, its parents do. Each style is resolved
	// once, its parent first; a loop of parents ends where it closes.
	type resolved struct {
		format, font       string
		hasFormat, hasFont bool
	}
	done := make(map[string]*resolved, len(order))
	var resolve func(id string, depth int) *resolved
	resolve = func(id string, depth int) *resolved {
		if r, ok := done[id]; ok {
			if r == nil {
				return &resolved{} // a loop
			}
			return r
		}
		w := written[id]
		if w == nil || depth > len(order) {
			return &resolved{}
		}
		done[id] = nil
		r := &resolved{format: w.format, font: w.font, hasFormat: w.hasFormat, hasFont: w.hasFont}
		if w.parent != "" && (!r.hasFormat || !r.hasFont) {
			p := resolve(w.parent, depth+1)
			if !r.hasFormat {
				r.format, r.hasFormat = p.format, p.hasFormat
			}
			if !r.hasFont {
				r.font, r.hasFont = p.font, p.hasFont
			}
		}
		done[id] = r
		return r
	}
	for i, id := range order {
		r := resolve(id, 0)
		format, font, hasFont := r.format, r.font, r.hasFont
		code := namedFormat(format)
		f, ok := formats[code]
		if !ok {
			f = numfmt.Parse(code)
			formats[code] = f
		}
		cs := cellStyle{format: f, general: f == numfmt.General()}
		if hasFont {
			cs.symbol = symbol.ByName(font)
			st.symbolic = st.symbolic || cs.symbol != symbol.None
		}
		st.styles[i] = cs
		byID[id] = i
	}
	return st, byID
}

// namedFormat returns the format code for a format as SpreadsheetML 2003
// writes it: a code, or one of the names Excel gives its built-in formats
// there, which are those of Visual Basic's Format function. Dates are
// written as ISO 8601 whatever their format, so what a date format's name
// decides is whether a date, a time or both are shown.
func namedFormat(name string) string {
	switch name {
	case "", "General", "General Number":
		return "General"
	case "General Date":
		return numfmt.Builtin(22) // m/d/yyyy h:mm
	case "Long Date":
		return "dddd, mmmm d, yyyy"
	case "Medium Date":
		return numfmt.Builtin(15) // d-mmm-yy
	case "Short Date":
		return numfmt.Builtin(14) // m/d/yyyy
	case "Long Time":
		return numfmt.Builtin(19) // h:mm:ss AM/PM
	case "Medium Time":
		return numfmt.Builtin(18) // h:mm AM/PM
	case "Short Time":
		return numfmt.Builtin(20) // h:mm
	case "Currency":
		return `"$"#,##0.00_);\("$"#,##0.00\)`
	case "Euro Currency":
		return `[$€-2]\ #,##0.00`
	case "Fixed":
		return "0.00"
	case "Standard":
		return "#,##0.00"
	case "Percent":
		return "0.00%"
	case "Scientific":
		return "0.00E+00"
	case "Yes/No":
		return `"Yes";"Yes";"No"`
	case "True/False":
		return `"True";"True";"False"`
	case "On/Off":
		return `"On";"On";"Off"`
	}
	return name
}

// ssMerge is a merged range that covers rows below the row of its cell,
// by which a cell with no index in those rows is placed.
type ssMerge struct {
	r1, c0, c1 int32
}

// ssBelow holds the merged ranges that reach below their rows, in order
// of first column, at most maxBelow of them: a sheet has a few at a time.
// Past that, a range is still merged, and a cell with no index under it
// is placed as if it were not there.
type ssBelow struct {
	m     []ssMerge
	minR1 int32 // the last row of the one that ends first
}

const maxBelow = 1 << 12

// add keeps m, done with the ranges that end above row first if there is
// no room.
func (b *ssBelow) add(m ssMerge, row int32) {
	if len(b.m) >= maxBelow && row > b.minR1 {
		kept := b.m[:0]
		b.minR1 = maxRows
		for _, x := range b.m {
			if x.r1 >= row {
				kept = append(kept, x)
				b.minR1 = min(b.minR1, x.r1)
			}
		}
		b.m = kept
	}
	if len(b.m) >= maxBelow {
		return
	}
	if len(b.m) == 0 || m.r1 < b.minR1 {
		b.minR1 = m.r1
	}
	b.m = ssCover(b.m, m)
}

// ssNote is a comment, kept until the sheet's notes are made.
type ssNote struct {
	row, col int32
	text     string
	author   string
}

// ssDate is the style a date with the General format is shown with.
var ssDate = cellStyle{format: numfmt.Parse(numfmt.Builtin(22))}

// readSS reads a worksheet of SpreadsheetML 2003: the element Worksheet,
// whose Table holds the columns and rows, and its drop-down lists.
func (sh *sheet) readSS() {
	s := &sh.s
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start || s.Depth() != 2 {
			continue
		}
		switch {
		case s.Is(ns.SS2003, "Table"):
			sh.readSSTable()
			if sh.cut != "" {
				return
			}
		case s.Is(ns.Excel2003, "DataValidation") && sh.pass == passWhole:
			sh.readSSValidation()
		default:
			s.Skip()
		}
	}
}

// ssStyleAt returns the style named by the attribute ss:StyleID of the
// current element, or nil.
func (sh *sheet) ssStyleAt() *cellStyle {
	v, ok := sh.s.Attr(ns.SS2003, "StyleID")
	if !ok {
		return nil
	}
	if i, ok := sh.wb.ssStyles[string(v)]; ok {
		return sh.wb.styles.style(i)
	}
	return nil
}

// ssInt reads an integer attribute of the current element.
func (sh *sheet) ssInt(local string) (int, bool) {
	v, ok := sh.s.Attr(ns.SS2003, local)
	if !ok {
		return 0, false
	}
	return atoi(bytes.TrimSpace(v))
}

// columnStyle is a style given to a run of columns.
type columnStyle struct {
	span
	style *cellStyle
}

// readSSTable reads a Table: its columns, then its rows.
func (sh *sheet) readSSTable() {
	s := &sh.s
	depth := s.Depth()
	def := sh.ssStyleAt()
	if def == nil {
		if i, ok := sh.wb.ssStyles["Default"]; ok {
			def = sh.wb.styles.style(i)
		} else {
			def = &plainStyle
		}
	}
	var (
		colStyles []columnStyle
		col       = int32(-1) // the last column given
		row       = int32(-1) // the last row read
		below     ssBelow     // merged ranges that reach below their row
	)
	styleOf := func(c int32) *cellStyle {
		i := sort.Search(len(colStyles), func(i int) bool { return colStyles[i].hi >= c })
		if i < len(colStyles) && colStyles[i].lo <= c {
			return colStyles[i].style
		}
		return def
	}
	for n := 0; s.Next() != xmlscan.EOF && s.Depth() >= depth; n++ {
		if s.Kind() != xmlscan.Start {
			continue
		}
		if s.Depth() != depth+1 {
			s.Skip()
			continue
		}
		switch {
		case s.Is(ns.SS2003, "Column"):
			col++
			if i, ok := sh.ssInt("Index"); ok && i >= 1 {
				col = int32(min(i, maxCols+1) - 1)
			}
			lo := col
			if extra, ok := sh.ssInt("Span"); ok && extra > 0 {
				col = int32(min(int(col)+extra, maxCols))
			}
			if lo >= maxCols {
				continue
			}
			hi := min(col, maxCols-1)
			hidden := false
			if v, ok := s.Attr(ns.SS2003, "Hidden"); ok && isTrue(v) {
				hidden = true
			} else if v, ok := s.Attr(ns.SS2003, "Width"); ok && isZero(v) {
				hidden = true
			}
			// The hidden columns are kept by the pass before the rows';
			// the styles, by each pass that reads cells.
			if hidden && sh.pass != passRows && len(sh.hiddenCols) < maxCols {
				sh.hiddenCols = append(sh.hiddenCols, span{lo, hi})
			}
			if st := sh.ssStyleAt(); st != nil && len(colStyles) < maxVarying && (len(colStyles) == 0 || colStyles[len(colStyles)-1].hi < lo) {
				colStyles = append(colStyles, columnStyle{span{lo, hi}, st})
			}
		case s.Is(ns.SS2003, "Row"):
			if n&1023 == 0 && sh.ctx.Err() != nil {
				sh.cut = "cancelled"
				return
			}
			row++
			if i, ok := sh.ssInt("Index"); ok && i >= 1 {
				row = int32(min(i, maxRows+1) - 1)
			}
			this := row
			if extra, ok := sh.ssInt("Span"); ok && extra > 0 {
				row = int32(min(int(row)+extra, maxRows)) // a run of rows alike, which hold no cells
			}
			if this >= maxRows {
				sh.lost.Add(warn.CapSheetRows, 1)
				s.Skip()
				continue
			}
			if this < sh.nextRow {
				sh.unsorted = true
			}
			sh.nextRow = this + 1
			hidden := false
			if v, ok := s.Attr(ns.SS2003, "Hidden"); ok && isTrue(v) {
				hidden = true
			} else if v, ok := s.Attr(ns.SS2003, "Height"); ok && isZero(v) {
				hidden = true
			}
			rowStyle := sh.ssStyleAt()
			first := len(sh.cells)
			textBytes := sh.readSSRow(this, rowStyle, styleOf, &below)
			if sh.pass == passAround {
				continue
			}
			if !sh.rowDone(first, this, hidden, textBytes) {
				return
			}
		default:
			s.Skip()
		}
	}
}

// readSSRow reads the cells of the row whose Start token is current, and
// returns the bytes of text they hold. The merged ranges that start in
// the row and reach below it are added to below.
func (sh *sheet) readSSRow(row int32, rowStyle *cellStyle, styleOf func(int32) *cellStyle, below *ssBelow) int64 {
	s := &sh.s
	depth := s.Depth()
	col := int32(-1) // the last column taken
	kept := int32(-1)
	ordered := true
	var textBytes int64
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		if s.Depth() != depth+1 || !s.Is(ns.SS2003, "Cell") {
			s.Skip()
			continue
		}
		// A cell with an index is there; one without comes after the
		// cell before, and after cells merged from above.
		if i, ok := sh.ssInt("Index"); ok && i >= 1 {
			col = int32(min(i, maxCols+1) - 1)
		} else {
			col = ssPlace(below.m, row, col+1)
		}
		at := rect{row, col, row, col}
		across, _ := sh.ssInt("MergeAcross")
		down, _ := sh.ssInt("MergeDown")
		if across > 0 || down > 0 {
			at.c1 = int32(min(int(col)+max(across, 0), maxCols-1))
			at.r1 = int32(min(int(row)+max(down, 0), maxRows-1))
			if sh.pass != passRows && col < maxCols && (at.r1 > at.r0 || at.c1 > at.c0) {
				if len(sh.merges) >= maxMerges {
					sh.lost.Add(warn.CapMerges, 1)
				} else {
					sh.merges = append(sh.merges, at)
				}
			}
			if at.r1 > row {
				below.add(ssMerge{at.r1, at.c0, at.c1}, row)
			}
		}
		this := col
		col = max(col, at.c1) // the next cell comes after the merged ones
		if sh.pass == passAround {
			s.Skip()
			continue
		}
		style := sh.ssStyleAt()
		if style == nil {
			style = rowStyle
		}
		if style == nil {
			style = styleOf(this)
		}
		href, hasLink := s.Attr(ns.SS2003, "HRef")
		if hasLink && sh.wb.opts.Spans && this < maxCols {
			sh.addLink(at, sh.lost.Address(string(href)))
		}
		f := ""
		formula := false
		if v, ok := s.Attr(ns.SS2003, "Formula"); ok && len(v) > 0 {
			formula = true
			if sh.wb.opts.Cells {
				sh.fbuf = append(sh.fbuf[:0], bytes.TrimPrefix(bytes.TrimSpace(v), []byte("="))...)
				f = verbatim(sh.fbuf, &sh.lost)
				if f != "" {
					f = formulaText(formula2003(f, this, row))
				}
			}
		}
		// The value, and the comment.
		typ := byte(0)
		raw := sh.raw[:0]
		cellDepth := s.Depth()
		for s.Next() != xmlscan.EOF && s.Depth() >= cellDepth {
			if s.Kind() != xmlscan.Start || s.Depth() != cellDepth+1 {
				continue
			}
			switch {
			case s.Is(ns.SS2003, "Data") && typ == 0:
				t, _ := s.Attr(ns.SS2003, "Type")
				switch string(t) {
				case "Number":
					typ = 'n'
				case "DateTime":
					typ = 'd'
				case "Boolean":
					typ = 'b'
				case "Error":
					typ = 'e'
				default:
					typ = 't'
				}
				raw = s.AppendText(raw)
			case s.Is(ns.SS2003, "Comment"):
				sh.ssComment(row, this)
			default:
				s.Skip()
			}
		}
		sh.raw = raw
		if s.Err() != nil {
			break
		}
		if this >= maxCols {
			if typ != 0 && len(raw) > 0 {
				sh.lost.Add(warn.CapSheetCols, 1)
			}
			continue
		}
		if typ == 0 || len(raw) == 0 && typ != 't' {
			if formula && typ == 0 {
				sh.noValue++
			}
			continue
		}
		sh.isDate = false
		var text string
		var hidden gofastcore.Display
		if typ == 'd' {
			serial, ok := ssSerial(raw, sh.wb.date1904)
			if !ok {
				typ = 't' // not a date: its text
			} else {
				if style.general {
					style = &ssDate
				}
				raw = appendNumber(raw[:0], serial)
				sh.raw = raw
				typ = 'n'
			}
		}
		text, hidden = sh.value(typ, style, raw)
		if text == "" {
			continue
		}
		if this <= kept {
			ordered = false
		}
		kept = this
		textBytes += int64(len(text))
		sh.cells = append(sh.cells, cell{text: text, row: row, col: this, hidden: hidden})
		if sh.wb.opts.Cells {
			if typ == 't' {
				typ = 'i' // a string, as the detail is concerned
			}
			sh.keepDetail(&sh.cells[len(sh.cells)-1], typ, raw, f, sh.isDate)
		}
	}
	if !ordered {
		sh.unsorted = true
	}
	return textBytes
}

// formula2003 reads a cell's formula from R1C1 notation into A1.
func formula2003(f string, col, row int32) string {
	return formula.FromR1C1(f, int(row), int(col))
}

// formulaText is a formula as a cell's detail gives it: as .xlsx stores
// it, without "=".
func formulaText(f string) string { return strings.TrimPrefix(f, "=") }

// ssPlace returns the column of a cell with no index that would come at
// col: after any range merged from a row above that covers it. below is
// in order of first column.
func ssPlace(below []ssMerge, row, col int32) int32 {
	for {
		i := sort.Search(len(below), func(i int) bool { return below[i].c0 > col }) - 1
		if i < 0 {
			return col
		}
		m := below[i]
		if m.r1 < row || m.c1 < col {
			return col
		}
		col = m.c1 + 1
	}
}

// ssCover adds a merged range that reaches below its row, in order of
// first column; one it replaces, at the same column, is done with.
func ssCover(below []ssMerge, m ssMerge) []ssMerge {
	i := sort.Search(len(below), func(i int) bool { return below[i].c0 >= m.c0 })
	if i < len(below) && below[i].c0 == m.c0 {
		below[i] = m
		return below
	}
	below = append(below, ssMerge{})
	copy(below[i+1:], below[i:])
	below[i] = m
	return below
}

// ssComment reads a cell's Comment: its author and its text, which is in
// ss:Data, with HTML elements for its formatting.
func (sh *sheet) ssComment(row, col int32) {
	s := &sh.s
	if sh.pass != passWhole {
		s.Skip()
		return
	}
	author := ""
	if v, ok := s.Attr(ns.SS2003, "Author"); ok {
		author = sh.lost.Name(string(v))
	}
	raw := sh.out[:0]
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() == xmlscan.Start && s.Depth() == depth+1 && s.Is(ns.SS2003, "Data") {
			raw = s.AppendText(raw)
		}
	}
	sh.out = raw
	if len(sh.ssNotes) >= maxComments {
		sh.lost.Add(warn.CapComments, 1)
		return
	}
	sh.ssNotes = append(sh.ssNotes, ssNote{row: row, col: col, text: string(raw), author: author})
}

// ssSerial reads a date as SpreadsheetML 2003 writes it
// (2026-01-31T13:30:00.000) into the serial number .xlsx holds: days
// since the workbook's epoch, with the time as a fraction. Excel counts
// 29 February 1900, which was not a day, in the 1900 system.
func ssSerial(raw []byte, date1904 bool) (float64, bool) {
	v := string(bytes.TrimSpace(raw))
	var t time.Time
	var err error
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err = time.Parse(layout, v); err == nil {
			break
		}
	}
	if err != nil {
		return 0, false
	}
	// Counted in seconds: a time.Duration spans no more than 292 years,
	// and Excel's dates span 8,000.
	since := func(base time.Time) float64 {
		return (float64(t.Unix()-base.Unix()) + float64(t.Nanosecond())/1e9) / 86400
	}
	if date1904 {
		days := since(time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC))
		return days, days >= 0
	}
	days := since(time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC))
	if days >= 60 {
		days++ // from 1 March 1900, past the day that was not
	}
	return days, days >= 0
}

// readSSValidation reads an x:DataValidation: the cells it is for and,
// for a list, its values or the formula that gives them.
func (sh *sheet) readSSValidation() {
	s := &sh.s
	depth := s.Depth()
	var typ, ranges, value string
	arrow := true
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start || s.Depth() != depth+1 || s.Space() != ns.Excel2003 {
			continue
		}
		switch string(s.Local()) {
		case "Type":
			typ = string(bytes.TrimSpace(s.AppendText(sh.raw[:0])))
		case "Range":
			ranges = string(bytes.TrimSpace(s.AppendText(sh.raw[:0])))
		case "Value":
			value = string(bytes.TrimSpace(s.AppendText(sh.raw[:0])))
		case "ComboHide":
			arrow = false
			s.Skip()
		default:
			s.Skip()
		}
	}
	if typ != "List" || value == "" || ranges == "" {
		return
	}
	if len(sh.lists) >= maxChoiceLists {
		sh.lost.Add(warn.CapChoiceLists, 1)
		return
	}
	v := choiceList{arrow: arrow}
	for _, r := range strings.Split(ranges, ",") {
		if sh.listRanges >= maxChoiceRanges {
			sh.lost.Add(warn.CapChoiceRanges, 1)
			break
		}
		if r0, c0, r1, c1, ok := formula.R1C1Range(r, 0, 0); ok {
			v.ranges = append(v.ranges, rect{int32(r0), int32(c0), int32(r1), int32(c1)})
			sh.listRanges++
		}
	}
	if len(v.ranges) == 0 {
		return
	}
	// A list of values is a string; anything else is a formula, in R1C1
	// notation from the first cell the list is for.
	v.source = strings.TrimPrefix(value, "=")
	if !strings.HasPrefix(v.source, `"`) {
		v.source = formulaText(formula.FromR1C1(v.source, int(v.ranges[0].r0), int(v.ranges[0].c0)))
	}
	sh.lists = append(sh.lists, v)
}
