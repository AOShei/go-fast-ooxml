package ooxmltest

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"unicode/utf16"
)

// Excel binary workbooks (.xlsb) for tests, written record by record as
// [MS-XLSB] has them. Excel is the only program that writes the format,
// so the tests that need a feature of it build the file here.

// Content types of the parts of a binary workbook.
const (
	CTXlsbWorkbook = "application/vnd.ms-excel.sheet.binary.macroEnabled.main"
	CTXlsbSheet    = "application/vnd.ms-excel.worksheet"
	CTXlsbStyles   = "application/vnd.ms-excel.styles"
	CTXlsbStrings  = "application/vnd.ms-excel.sharedStrings"
	CTXlsbComments = "application/vnd.ms-excel.comments"
	CTXlsbTable    = "application/vnd.ms-excel.table"
	CTXlsbMacro    = "application/vnd.ms-excel.macrosheet"
	RelTable       = relBase + "table"
	RelMacrosheet  = "http://schemas.microsoft.com/office/2006/relationships/xlMacrosheet"
)

// Rec writes one record: its type and size, each in seven-bit groups, and
// its fields.
func Rec(id int, fields ...[]byte) []byte {
	var out []byte
	if id < 0x80 {
		out = append(out, byte(id))
	} else {
		out = append(out, byte(id&0x7F)|0x80, byte(id>>7))
	}
	size := 0
	for _, f := range fields {
		size += len(f)
	}
	for {
		b := byte(size & 0x7F)
		size >>= 7
		if size > 0 {
			out = append(out, b|0x80)
			continue
		}
		out = append(out, b)
		break
	}
	for _, f := range fields {
		out = append(out, f...)
	}
	return out
}

// U8, U16, U32, I32 and F64 write fields of a record.
func U8(v byte) []byte    { return []byte{v} }
func U16(v uint16) []byte { return binary.LittleEndian.AppendUint16(nil, v) }
func U32(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }
func I32(v int32) []byte  { return U32(uint32(v)) }
func F64(v float64) []byte {
	return binary.LittleEndian.AppendUint64(nil, math.Float64bits(v))
}

// WStr writes a string (XLWideString): a count of UTF-16 code units, then
// the units.
func WStr(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := U32(uint32(len(units)))
	for _, u := range units {
		out = binary.LittleEndian.AppendUint16(out, u)
	}
	return out
}

// RStr writes a string with its formatting (RichStr), with none.
func RStr(s string) []byte { return append([]byte{0}, WStr(s)...) }

// Join puts fields together.
func Join(fields ...[]byte) []byte {
	var out []byte
	for _, f := range fields {
		out = append(out, f...)
	}
	return out
}

// Rect writes a rectangle of cells: first and last row, first and last
// column, from 0.
func Rect(r0, r1, c0, c1 int32) []byte { return Join(I32(r0), I32(r1), I32(c0), I32(c1)) }

// Record types used by the tests (the specification's numbers).
const (
	XRowHdr     = 0
	XCellBlank  = 1
	XCellRk     = 2
	XCellError  = 3
	XCellBool   = 4
	XCellReal   = 5
	XCellSt     = 6
	XCellIsst   = 7
	XFmlaString = 8
	XFmlaNum    = 9
	XFmlaBool   = 10
	XFmlaError  = 11
	XShortReal  = 16
	XShortSt    = 17
	XCellRStr   = 62
	XArrFmla    = 426
	XShrFmla    = 427
	XColInfo    = 60
	XDVal       = 64
	XMergeCell  = 176
	XHLink      = 494
	XListPart   = 661
	XBeginList  = 343
	XTableStyle = 513
	XName       = 39
	XExtSheet   = 362
	XSupSelf    = 357
	XSupBookSrc = 355
)

// Row writes a row header: its row from 0, flags (0x1000 hidden, 0x2000
// a height of its own) and height in twentieths of a point.
func Row(row int32, flags, height uint16) []byte {
	return Rec(XRowHdr, I32(row), U32(0), U16(height), U16(flags), U8(0), U32(0))
}

// Cell writes a cell record of the given type at a column, in a style,
// with its value's fields.
func Cell(id int, col int32, style uint32, value ...[]byte) []byte {
	return Rec(id, append([][]byte{I32(col), U32(style)}, value...)...)
}

// Fmla writes the formula fields of a cell, after its value: flags, then
// the formula's tokens and extra bytes.
func Fmla(rgce, extra []byte) []byte {
	return Join(U16(0), U32(uint32(len(rgce))), rgce, U32(uint32(len(extra))), extra)
}

// XlsbSheet is a sheet of a test binary workbook.
type XlsbSheet struct {
	Name string
	// State is 0, 1 (hidden) or 2 (very hidden).
	State uint32
	// Data is the records of its rows and cells, between the beginning
	// and the end of its data.
	Data []byte
	// Before and After are records before and after its data: column
	// widths before, merged cells, links, lists and tables after.
	Before, After []byte
	// Macro makes it a sheet of Excel 4 macros.
	Macro bool
}

// XlsbStyles describes the styles of a test workbook: number formats by
// number, the font names, and cell formats as (number format, font).
type XlsbStyles struct {
	Formats map[uint16]string
	Fonts   []string
	XFs     [][2]uint16
}

// Xlsb returns a binary workbook with the given sheets, and the given
// workbook records (names, links) after its list of sheets.
func Xlsb(styles *XlsbStyles, strs []string, book []byte, sheets ...XlsbSheet) *Package {
	p := NewPackage()
	p.Rel("", RelOfficeDoc, "xl/workbook.bin")
	var wb []byte
	wb = append(wb, Join(Rec(131), Rec(153, U32(0), U32(0), WStr("")), Rec(143))...)
	for i, s := range sheets {
		name := fmt.Sprintf("worksheets/sheet%d.bin", i+1)
		ct, rel := CTXlsbSheet, RelWorksheet
		if s.Macro {
			name, ct, rel = fmt.Sprintf("macrosheets/sheet%d.bin", i+1), CTXlsbMacro, RelMacrosheet
		}
		id := p.Rel("xl/workbook.bin", rel, name)
		wb = append(wb, Rec(156, U32(s.State), U32(uint32(i+1)), WStr(id), WStr(s.Name))...)
		var part []byte
		part = append(part, Rec(129)...)
		part = append(part, s.Before...)
		part = append(part, Rec(145)...)
		part = append(part, s.Data...)
		part = append(part, Rec(146)...)
		part = append(part, s.After...)
		part = append(part, Rec(130)...)
		p.AddBytes("xl/"+name, ct, part)
	}
	wb = append(wb, Rec(144)...)
	wb = append(wb, book...)
	wb = append(wb, Rec(132)...)
	p.AddBytes("xl/workbook.bin", CTXlsbWorkbook, wb)
	if styles != nil {
		var st []byte
		st = append(st, Rec(278)...)
		st = append(st, Rec(615, U32(uint32(len(styles.Formats))))...)
		for id, code := range styles.Formats {
			st = append(st, Rec(44, U16(id), WStr(code))...)
		}
		st = append(st, Rec(616)...)
		st = append(st, Rec(611, U32(uint32(len(styles.Fonts))))...)
		for _, f := range styles.Fonts {
			st = append(st, Rec(43, make([]byte, 21), WStr(f))...)
		}
		st = append(st, Rec(612)...)
		st = append(st, Rec(617, U32(uint32(len(styles.XFs))))...)
		for _, x := range styles.XFs {
			st = append(st, Rec(47, U16(0), U16(x[0]), U16(x[1]), make([]byte, 10))...)
		}
		st = append(st, Join(Rec(618), Rec(279))...)
		p.Rel("xl/workbook.bin", RelStyles, "styles.bin")
		p.AddBytes("xl/styles.bin", CTXlsbStyles, st)
	}
	if strs != nil {
		sst := Rec(159, U32(uint32(len(strs))), U32(uint32(len(strs))))
		for _, s := range strs {
			sst = append(sst, Rec(19, RStr(s))...)
		}
		sst = append(sst, Rec(160)...)
		p.Rel("xl/workbook.bin", RelSharedStrings, "sharedStrings.bin")
		p.AddBytes("xl/sharedStrings.bin", CTXlsbStrings, sst)
	}
	return p
}

// XlsbComments adds a binary comments part to a sheet: each comment is
// (row, column, author, text).
func (p *Package) XlsbComments(sheet string, comments ...[4]string) *Package {
	authors := map[string]int{}
	var list []string
	for _, c := range comments {
		if _, ok := authors[c[2]]; !ok {
			authors[c[2]] = len(list)
			list = append(list, c[2])
		}
	}
	var b []byte
	b = append(b, Join(Rec(628), Rec(630))...)
	for _, a := range list {
		b = append(b, Rec(632, WStr(a))...)
	}
	b = append(b, Join(Rec(631), Rec(633))...)
	for _, c := range comments {
		var row, col int32
		fmt.Sscan(c[0], &row)
		fmt.Sscan(c[1], &col)
		b = append(b, Rec(635, I32(int32(authors[c[2]])), Rect(row, row, col, col), make([]byte, 16))...)
		b = append(b, Rec(637, RStr(c[3]))...)
		b = append(b, Rec(636)...)
	}
	b = append(b, Join(Rec(634), Rec(629))...)
	name := "xl/comments" + strings.TrimSuffix(strings.TrimPrefix(sheet, "xl/worksheets/sheet"), ".bin") + ".bin"
	p.Rel(sheet, RelComments, "../"+strings.TrimPrefix(name, "xl/"))
	return p.AddBytes(name, CTXlsbComments, b)
}
