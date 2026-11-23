package gofastooxml_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	. "github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// An Excel binary workbook is read into what the XML of an .xlsx file
// gives, so the same workbook written both ways gives the same document:
// each case here is one workbook written twice, once as XML, once as
// records, and the two documents are compared whole, with every option
// that adds to what is read.

// Formula tokens, as [MS-XLSB] writes them.
func ptgRef(row int32, col uint16, rel bool) []byte {
	if rel {
		col |= 0xC000
	}
	return Join(U8(0x24), I32(row), U16(col))
}

func ptgArea(r0, r1 int32, c0, c1 uint16, rel bool) []byte {
	if rel {
		c0, c1 = c0|0xC000, c1|0xC000
	}
	return Join(U8(0x25), I32(r0), I32(r1), U16(c0), U16(c1))
}

func ptgStr(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := Join(U8(0x17), U16(uint16(len(units))))
	for _, u := range units {
		out = append(out, U16(u)...)
	}
	return out
}

func ptgInt(v uint16) []byte              { return Join(U8(0x1E), U16(v)) }
func ptgFunc(id uint16) []byte            { return Join(U8(0x41), U16(id)) }
func ptgFuncVar(n byte, id uint16) []byte { return Join(U8(0x42), U8(n), U16(id)) }
func ptgName(i uint32) []byte             { return Join(U8(0x43), U32(i)) }
func ptgRef3d(ixti uint16, row int32, col uint16) []byte {
	return Join(U8(0x3A), U16(ixti), I32(row), U16(col|0xC000))
}

// ptgRefN is a reference of a shared formula: offsets from the cell it is
// used in.
func ptgRefN(dr, dc int32) []byte {
	return Join(U8(0x2C), I32(dr&0xFFFFF), U16(uint16(dc&0x3FFF)|0xC000))
}

const (
	ptgAdd    = 0x03
	ptgConcat = 0x08
)

// pair is one workbook written as XML and as records.
type pair struct {
	name      string
	xlsx      []byte
	xlsb      []byte
	opts      gofastcore.Options
	extractor gofastooxml.Extractor
}

func (c pair) docs(t *testing.T) (x, b string) {
	t.Helper()
	read := func(data []byte) string {
		opts := c.opts
		e := c.extractor
		doc, err := e.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), &opts)
		if err != nil {
			return "error: " + err.Error()
		}
		CheckDocument(t, doc)
		doc.Source = gofastcore.Source{}
		out, _ := json.MarshalIndent(doc, "", " ")
		return string(out)
	}
	return read(c.xlsx), read(c.xlsb)
}

func TestXlsbAsXlsx(t *testing.T) {
	all := gofastcore.Options{Cells: true, Spans: true}
	xs := func(sheets ...Sheet) *Package { return Xlsx(sheets...) }
	cases := []pair{
		{
			name: "values",
			xlsx: xs(Sheet{Name: "Values", Data: `<row r="1">` +
				`<c r="A1"><v>42</v></c><c r="B1"><v>1.5</v></c><c r="C1"><v>-3.25</v></c><c r="D1"><v>0.1</v></c>` +
				`<c r="E1" t="s"><v>0</v></c><c r="F1" t="inlineStr"><is><t>inline</t></is></c>` +
				`<c r="G1" t="b"><v>1</v></c><c r="H1" t="e"><v>#DIV/0!</v></c><c r="I1" t="inlineStr"><is><t>rich</t></is></c>` +
				`</row><row r="3"><c r="A3"><v>123456789</v></c><c r="B3" t="inlineStr"><is><t>short</t></is></c><c r="C3"><v>2.5</v></c></row>`}).
				WithSharedStrings("shared").Bytes(),
			xlsb: Xlsb(nil, []string{"shared"}, nil, XlsbSheet{Name: "Values", Data: Join(
				Row(0, 0, 300),
				Cell(XCellRk, 0, 0, U32(42<<2|2)),
				Cell(XCellRk, 1, 0, U32(150<<2|3)), // 150 / 100
				Cell(XCellReal, 2, 0, F64(-3.25)),
				Cell(XCellReal, 3, 0, F64(0.1)),
				Cell(XCellIsst, 4, 0, U32(0)),
				Cell(XCellSt, 5, 0, WStr("inline")),
				Cell(XCellBool, 6, 0, U8(1)),
				Cell(XCellError, 7, 0, U8(0x07)),
				Cell(XCellRStr, 8, 0, RStr("rich")),
				Row(2, 0, 300),
				Cell(XCellReal, 0, 0, F64(123456789)),
				Rec(XShortSt, U32(0), WStr("short")),
				Rec(XShortReal, U32(0), F64(2.5)),
			)}).Bytes(),
			opts: all,
		},
		{
			name: "formulas",
			xlsx: xs(Sheet{Name: "F", Data: `<row r="1"><c r="A1"><v>1</v></c><c r="B1"><v>2</v></c>` +
				`<c r="C1"><f>SUM(A1:B1)</f><v>3</v></c><c r="D1" t="str"><f>"n="&amp;A1</f><v>n=1</v></c>` +
				`<c r="E1" t="b"><f>A1&lt;&gt;B1</f><v>1</v></c><c r="F1" t="e"><f>1/0</f><v>#DIV/0!</v></c>` +
				`<c r="G1" t="str"><f>""</f><v></v></c><c r="H1"><f>$A$1+B1</f><v>3</v></c></row>` +
				`<row r="2"><c r="A2"><v>5</v></c><c r="B2"><f>A2*2</f><v>10</v></c><c r="C2"><f>B2*2</f><v>20</v></c></row>`}).Bytes(),
			xlsb: Xlsb(nil, nil, nil, XlsbSheet{Name: "F", Data: Join(
				Row(0, 0, 300),
				Cell(XCellRk, 0, 0, U32(1<<2|2)),
				Cell(XCellRk, 1, 0, U32(2<<2|2)),
				Cell(XFmlaNum, 2, 0, F64(3), Fmla(Join(ptgArea(0, 0, 0, 1, true), ptgFuncVar(1, 4)), nil)),
				Cell(XFmlaString, 3, 0, WStr("n=1"), Fmla(Join(ptgStr("n="), ptgRef(0, 0, true), U8(ptgConcat)), nil)),
				Cell(XFmlaBool, 4, 0, U8(1), Fmla(Join(ptgRef(0, 0, true), ptgRef(0, 1, true), U8(0x0E)), nil)),
				Cell(XFmlaError, 5, 0, U8(0x07), Fmla(Join(ptgInt(1), ptgInt(0), U8(0x06)), nil)),
				Cell(XFmlaString, 6, 0, WStr(""), Fmla(ptgStr(""), nil)),
				Cell(XFmlaNum, 7, 0, F64(3), Fmla(Join(ptgRef(0, 0, false), ptgRef(0, 1, true), U8(ptgAdd)), nil)),
				Row(1, 0, 300),
				Cell(XCellRk, 0, 0, U32(5<<2|2)),
				// B2 and C2 share a formula, written after its first cell.
				Cell(XFmlaNum, 1, 0, F64(10), Fmla(Join(U8(0x01), I32(1)), I32(1))),
				Rec(XShrFmla, Rect(1, 1, 1, 2), U32(uint32(len(Join(ptgRefN(0, -1), ptgInt(2), U8(0x05))))), Join(ptgRefN(0, -1), ptgInt(2), U8(0x05)), U32(0)),
				Cell(XFmlaNum, 2, 0, F64(20), Fmla(Join(U8(0x01), I32(1)), I32(1))),
			)}).Bytes(),
			opts: all,
		},
		{
			name: "styles",
			xlsx: xs(Sheet{Name: "S", Data: `<row r="1"><c r="A1" s="1"><v>45000</v></c><c r="B1" s="2"><v>0.25</v></c>` +
				`<c r="C1" s="3"><v>7</v></c><c r="D1" s="4" t="inlineStr"><is><t>` + "ü" + `</t></is></c></row>`}).
				WithSheetStyles(`<numFmts count="2"><numFmt numFmtId="164" formatCode="0.0%"/><numFmt numFmtId="165" formatCode=";;;"/></numFmts>` +
					`<fonts count="2"><font><name val="Calibri"/></font><font><name val="Wingdings"/></font></fonts>` +
					`<cellXfs count="5"><xf numFmtId="0" fontId="0"/><xf numFmtId="14" fontId="0"/><xf numFmtId="164" fontId="0"/><xf numFmtId="165" fontId="0"/><xf numFmtId="0" fontId="1"/></cellXfs>`).Bytes(),
			xlsb: Xlsb(&XlsbStyles{Formats: map[uint16]string{164: "0.0%", 165: ";;;"}, Fonts: []string{"Calibri", "Wingdings"},
				XFs: [][2]uint16{{0, 0}, {14, 0}, {164, 0}, {165, 0}, {0, 1}}}, nil, nil,
				XlsbSheet{Name: "S", Data: Join(
					Row(0, 0, 300),
					Cell(XCellReal, 0, 1, F64(45000)),
					Cell(XCellReal, 1, 2, F64(0.25)),
					Cell(XCellReal, 2, 3, F64(7)),
					Cell(XCellSt, 3, 4, WStr("ü")),
				)}).Bytes(),
			opts: all,
		},
		{
			name: "hidden",
			xlsx: xs(
				Sheet{Name: "Seen", Before: `<cols><col min="2" max="3" width="0" customWidth="1"/><col min="5" max="5" width="9" hidden="1"/></cols>`,
					Data: `<row r="1"><c r="A1"><v>1</v></c><c r="B1"><v>2</v></c><c r="D1"><v>4</v></c><c r="E1"><v>5</v></c></row>` +
						`<row r="2" hidden="1"><c r="A2"><v>6</v></c></row><row r="3" ht="0" customHeight="1"><c r="A3"><v>7</v></c></row>` +
						`<row r="4"><c r="A4"><v>8</v></c><c r="B4"><v>9</v></c></row>`,
					Extra: `<mergeCells count="1"><mergeCell ref="A4:B4"/></mergeCells>`},
				Sheet{Name: "Hidden", State: "hidden", Data: `<row r="1"><c r="A1"><v>1</v></c></row>`},
				Sheet{Name: "Very", State: "veryHidden", Data: `<row r="1"><c r="A1"><v>2</v></c></row>`},
			).Bytes(),
			xlsb: Xlsb(nil, nil, nil,
				XlsbSheet{Name: "Seen",
					Before: Join(Rec(390), Rec(XColInfo, I32(1), I32(2), I32(0), I32(0), U16(2), U8(0)), Rec(XColInfo, I32(4), I32(4), I32(9*256), I32(0), U16(1), U8(0)), Rec(391)),
					Data: Join(
						Row(0, 0, 300), Cell(XCellRk, 0, 0, U32(1<<2|2)), Cell(XCellRk, 1, 0, U32(2<<2|2)), Cell(XCellRk, 3, 0, U32(4<<2|2)), Cell(XCellRk, 4, 0, U32(5<<2|2)),
						Row(1, 0x1000, 300), Cell(XCellRk, 0, 0, U32(6<<2|2)),
						Row(2, 0x2000, 0), Cell(XCellRk, 0, 0, U32(7<<2|2)),
						Row(3, 0, 300), Cell(XCellRk, 0, 0, U32(8<<2|2)), Cell(XCellRk, 1, 0, U32(9<<2|2)),
					),
					After: Join(Rec(177, U32(1)), Rec(XMergeCell, Rect(3, 3, 0, 1)), Rec(178))},
				XlsbSheet{Name: "Hidden", State: 1, Data: Join(Row(0, 0, 300), Cell(XCellRk, 0, 0, U32(1<<2|2)))},
				XlsbSheet{Name: "Very", State: 2, Data: Join(Row(0, 0, 300), Cell(XCellRk, 0, 0, U32(2<<2|2)))},
			).Bytes(),
			opts: all,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			x, b := c.docs(t)
			if testing.Verbose() {
				t.Log(b)
			}
			if x != b {
				t.Errorf("the .xlsb differs from the .xlsx:\n%s", diffLines(x, b))
			}
		})
	}
}

// diffLines shows the lines where two texts differ.
func diffLines(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	var out strings.Builder
	for i := 0; i < max(len(al), len(bl)); i++ {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			out.WriteString("- " + x + "\n+ " + y + "\n")
		}
	}
	return out.String()
}

// bname writes a defined name: flags, its sheet (-1 for the workbook),
// its name and its formula.
func bname(name string, sheet int32, rgce []byte) []byte {
	return Rec(XName, U32(0), U8(0), I32(sheet), WStr(name), U32(uint32(len(rgce))), rgce, U32(0))
}

func TestXlsbAsXlsxMore(t *testing.T) {
	all := gofastcore.Options{Cells: true, Spans: true}
	var cases []pair

	// Names, references to other sheets, and an array formula.
	{
		x := Xlsx(Sheet{Name: "Data", Data: `<row r="1"><c r="A1"><v>1</v></c><c r="B1"><f>'My sheet'!A1*Rate</f><v>0.4</v></c>` +
			`<c r="C1"><f t="array" ref="C1:D1">{1,2}</f><v>1</v></c><c r="D1"><v>2</v></c></row>`},
			Sheet{Name: "My sheet", Data: `<row r="1"><c r="A1"><v>2</v></c></row>`})
		x.Add("xl/workbook.xml", CTWorkbook, `<workbook xmlns="`+NSSheet+`" xmlns:r="`+NSRel+`"><sheets>`+
			`<sheet name="Data" sheetId="1" r:id="rId1"/><sheet name="My sheet" sheetId="2" r:id="rId2"/></sheets><definedNames>`+
			`<definedName name="Rate">0.2</definedName><definedName name="Area">Data!$A$1:$B$2</definedName>`+
			`<definedName name="Loc" localSheetId="1">'My sheet'!$A$1</definedName></definedNames></workbook>`)
		array := Join(U8(0x60), make([]byte, 14))
		arrayExtra := Join(I32(1), I32(2), U8(0), F64(1), U8(0), F64(2))
		b := Xlsb(nil, nil, Join(
			Rec(XSupSelf), Rec(XExtSheet, U32(2), I32(0), I32(0), I32(0), I32(0), I32(1), I32(1)),
			bname("Rate", -1, Join(U8(0x1F), F64(0.2))),
			bname("Area", -1, Join(U8(0x3B), U16(0), I32(0), I32(1), U16(0), U16(1))),
			bname("Loc", 1, Join(U8(0x3A), U16(1), I32(0), U16(0))),
		),
			XlsbSheet{Name: "Data", Data: Join(Row(0, 0, 300),
				Cell(XCellRk, 0, 0, U32(1<<2|2)),
				Cell(XFmlaNum, 1, 0, F64(0.4), Fmla(Join(ptgRef3d(1, 0, 0), ptgName(1), U8(0x05)), nil)),
				Cell(XFmlaNum, 2, 0, F64(1), Fmla(Join(U8(0x01), I32(0)), I32(2))),
				Rec(XArrFmla, Rect(0, 0, 2, 3), U8(0), U32(uint32(len(array))), array, U32(uint32(len(arrayExtra))), arrayExtra),
				Cell(XFmlaNum, 3, 0, F64(2), Fmla(Join(U8(0x01), I32(0)), I32(2))),
			)},
			XlsbSheet{Name: "My sheet", Data: Join(Row(0, 0, 300), Cell(XCellRk, 0, 0, U32(2<<2|2)))})
		cases = append(cases, pair{name: "names", xlsx: x.Bytes(), xlsb: b.Bytes(), opts: all})
	}

	// Links, drop-down lists, a table and a comment.
	{
		sheetXML := `<row r="1"><c r="A1" t="inlineStr"><is><t>Name</t></is></c><c r="B1" t="inlineStr"><is><t>Score</t></is></c></row>` +
			`<row r="2"><c r="A2" t="inlineStr"><is><t>x</t></is></c><c r="B2"><v>1</v></c></row>`
		x := Xlsx(Sheet{Name: "S", Data: sheetXML,
			Extra: `<dataValidations count="2"><dataValidation type="list" sqref="C1:C2"><formula1>"a,b,c"</formula1></dataValidation>` +
				`<dataValidation type="list" showDropDown="1" sqref="D1"><formula1>$A$1:$A$2</formula1></dataValidation></dataValidations>` +
				`<hyperlinks><hyperlink ref="A1" r:id="rId1"/><hyperlink ref="B1" location="S!A2"/></hyperlinks>` +
				`<tableParts count="1"><tablePart r:id="rIdT"/></tableParts>`})
		x.ExternalRel("xl/worksheets/sheet1.xml", RelHyperlink, "https://example.org/")
		x.RelID("xl/worksheets/sheet1.xml", "rIdT", RelTable, "../tables/table1.xml")
		x.Add("xl/tables/table1.xml", ctTable, `<table xmlns="`+NSSheet+`" id="1" name="Scores" displayName="Scores" ref="A1:B2">`+
			`<tableColumns count="2"><tableColumn id="1" name="Name"/><tableColumn id="2" name="Score"/></tableColumns>`+
			`<tableStyleInfo name="TableStyleMedium2" showRowStripes="1"/></table>`)
		x.Rel("xl/worksheets/sheet1.xml", RelComments, "../comments1.xml")
		x.Add("xl/comments1.xml", ctComments, `<comments xmlns="`+NSSheet+`"><authors><author>Ann</author></authors>`+
			`<commentList><comment ref="A2" authorId="0"><text><t>a note</t></text></comment></commentList></comments>`)

		dval := func(flags uint32, r []byte, rgce []byte) []byte {
			return Rec(XDVal, U32(flags), U32(1), r, WStr(""), WStr(""), WStr(""), WStr(""), U32(uint32(len(rgce))), rgce, U32(0), U32(0), U32(0))
		}
		b := Xlsb(nil, nil, nil, XlsbSheet{Name: "S",
			Data: Join(Row(0, 0, 300), Cell(XCellSt, 0, 0, WStr("Name")), Cell(XCellSt, 1, 0, WStr("Score")),
				Row(1, 0, 300), Cell(XCellSt, 0, 0, WStr("x")), Cell(XCellRk, 1, 0, U32(1<<2|2))),
			After: Join(
				Rec(573), dval(3|0x80, Rect(0, 1, 2, 2), ptgStr("a,b,c")), dval(3|0x200, Rect(0, 0, 3, 3), ptgArea(0, 1, 0, 0, false)), Rec(574),
				Rec(XHLink, Rect(0, 0, 0, 0), WStr("rId1"), WStr(""), WStr(""), WStr("")),
				Rec(XHLink, Rect(0, 0, 1, 1), WStr(""), WStr("S!A2"), WStr(""), WStr("")),
				Rec(660), Rec(XListPart, WStr("rIdT")), Rec(662),
			)})
		b.ExternalRel("xl/worksheets/sheet1.bin", RelHyperlink, "https://example.org/")
		b.RelID("xl/worksheets/sheet1.bin", "rIdT", RelTable, "../tables/table1.bin")
		b.AddBytes("xl/tables/table1.bin", CTXlsbTable, Join(
			Rec(XBeginList, Rect(0, 1, 0, 1), U32(0), U32(1), U32(1), U32(0), make([]byte, 32), WStr("Scores"), WStr("Scores"), WStr(""), WStr(""), WStr("")),
			Rec(XTableStyle, U16(4), WStr("TableStyleMedium2")),
			Rec(344)))
		b.XlsbComments("xl/worksheets/sheet1.bin", [4]string{"1", "0", "Ann", "a note"})
		cases = append(cases, pair{name: "links, lists, tables and comments", xlsx: x.Bytes(), xlsb: b.Bytes(), opts: all})
	}

	// Dates counted from 1904.
	{
		x := Xlsx(Sheet{Name: "D", Data: `<row r="1"><c r="A1" s="1"><v>1</v></c></row>`}).
			WithSheetStyles(`<cellXfs count="2"><xf numFmtId="0"/><xf numFmtId="14"/></cellXfs>`)
		x.Add("xl/workbook.xml", CTWorkbook, `<workbook xmlns="`+NSSheet+`" xmlns:r="`+NSRel+`"><workbookPr date1904="1"/><sheets>`+
			`<sheet name="D" sheetId="1" r:id="rId1"/></sheets></workbook>`)
		b := Xlsb(&XlsbStyles{XFs: [][2]uint16{{0, 0}, {14, 0}}}, nil, Rec(153, U32(1), U32(0), WStr("")),
			XlsbSheet{Name: "D", Data: Join(Row(0, 0, 300), Cell(XCellRk, 0, 1, U32(1<<2|2)))})
		cases = append(cases, pair{name: "1904", xlsx: x.Bytes(), xlsb: b.Bytes(), opts: all})
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			x, b := c.docs(t)
			if testing.Verbose() {
				t.Log(b)
			}
			if x != b {
				t.Errorf("the .xlsb differs from the .xlsx:\n%s", diffLines(x, b))
			}
		})
	}
}

// The formulas of an Excel 4 macro sheet in a binary workbook are in the
// stored part, as those of an XML one are; the cells show their results.
func TestXlsbMacroSheet(t *testing.T) {
	b := Xlsb(nil, nil, nil,
		XlsbSheet{Name: "Data", Data: Join(Row(0, 0, 300), Cell(XCellSt, 0, 0, WStr("figures")))},
		XlsbSheet{Name: "Macro1", Macro: true, Data: Join(Row(0, 0, 300),
			Cell(XFmlaNum, 0, 0, F64(3), Fmla(Join(ptgInt(1), ptgInt(2), ptgFuncVar(2, 4)), nil)),
			Row(2, 0, 300),
			Cell(XFmlaString, 1, 0, WStr("ab"), Fmla(Join(ptgStr("a"), ptgStr("b"), U8(ptgConcat)), nil)))})
	doc := excel(t, b, nil)
	expectSheets(t, doc,
		`sheet 1 "Data"`, `  table A1:A1 inferred [["figures"]]`,
		`sheet 2 "Macro1"`, `  table A1:A1 inferred [["3"]]`, `  table B3:B3 inferred [["ab"]]`,
		`stored 3 ""`,
		"  code  \"A1\\t=SUM(1,2)\\nB3\\t=\\\"a\\\"&\\\"b\\\"\" language=excel4 name=Macro1 stored=macro",
	)
}

// A formula this library cannot read back (here a function it does not
// know) is left out of the cell's detail and counted; the cell shows its
// result as any other.
func TestXlsbFormulaNotReadBack(t *testing.T) {
	b := Xlsb(nil, nil, nil, XlsbSheet{Name: "S", Data: Join(Row(0, 0, 300),
		Cell(XFmlaNum, 0, 0, F64(7), Fmla(Join(ptgInt(1), ptgFuncVar(1, 0x7000)), nil)),
		Cell(XFmlaNum, 1, 0, F64(8), Fmla(Join(ptgInt(8), ptgFunc(0x7001)), nil)))})
	doc := excel(t, b, &gofastcore.Options{Cells: true})
	expectSheets(t, doc, `sheet 1 "S"`, `  table A1:B1 inferred [["7" "8"]]`)
	if f := doc.Parts[0].Blocks[0].Table.Cells; len(f) != 2 || f[0].Formula != "" || f[1].Formula != "" {
		t.Errorf("cells %+v", f)
	}
	found := false
	for _, w := range doc.Warnings {
		found = found || w.Code == "input.unread_content" && strings.Contains(w.Message, "2 binary formulas not read back")
	}
	if !found {
		t.Errorf("warnings %+v", doc.Warnings)
	}
	// Without Options.Cells formulas are not read, and nothing is said.
	if doc := excel(t, b, nil); len(doc.Warnings) != 0 {
		t.Errorf("warnings %+v", doc.Warnings)
	}
}

// A binary part that breaks off, or whose records cannot be read, gives
// what came before the break, and says where the sheet is cut off.
func TestXlsbDamaged(t *testing.T) {
	rows := Join(Row(0, 0, 300), Cell(XCellSt, 0, 0, WStr("before")), Row(1, 0, 300), Cell(XCellSt, 0, 0, WStr("also before")))
	whole := func(data []byte) []byte {
		return Join(Rec(129), Rec(145), data)
	}
	for name, part := range map[string][]byte{
		"cut inside a record":      append(whole(rows), Cell(XCellSt, 1, 0, WStr("lost"))[:6]...),
		"a record of 200 MB":       append(whole(rows), 0x06, 0xFF, 0xFF, 0xFF, 0x7F),
		"a type of three bytes":    append(whole(rows), 0x86, 0x86, 0x01, 0x00),
		"a size of five bytes":     append(whole(rows), 0x06, 0x80, 0x80, 0x80, 0x80, 0x01),
		"cut inside its header":    append(whole(rows), 0x86),
		"no end to its sheet data": whole(rows),
	} {
		t.Run(name, func(t *testing.T) {
			b := Xlsb(nil, nil, nil, XlsbSheet{Name: "S"})
			b.AddBytes("xl/worksheets/sheet1.bin", CTXlsbSheet, part)
			doc := excel(t, b, nil)
			got := renderSheets(doc)
			if len(got) < 2 || got[1] != `  table A1:A2 inferred [["before"] ["also before"]]` {
				t.Errorf("sheets %q", got)
			}
			cut := doc.Parts[0].Has(gofastcore.WarnPartFailed)
			if name == "no end to its sheet data" {
				cut = !cut // a part that just ends is read to its end
			}
			if !cut {
				t.Errorf("warnings %+v", doc.Parts[0].Warnings)
			}
		})
	}
}

// The limits on cells and on text cut a binary workbook where they cut the
// same workbook as XML; reading it a band of rows at a time gives the
// same bands.
func TestXlsbLimitsAndBands(t *testing.T) {
	var xml strings.Builder
	var bin [][]byte
	for r := 0; r < 300; r++ {
		xml.WriteString(xlRow(r+1, xlStr("A"+strconv.Itoa(r+1), "row "+strconv.Itoa(r)), xlNum("B"+strconv.Itoa(r+1), strconv.Itoa(r*3))))
		bin = append(bin, Row(int32(r), 0, 300), Cell(XCellSt, 0, 0, WStr("row "+strconv.Itoa(r))), Cell(XCellRk, 1, 0, U32(uint32(r*3)<<2|2)))
	}
	x := Xlsx(Sheet{Name: "S", Data: xml.String()}).Bytes()
	b := Xlsb(nil, nil, nil, XlsbSheet{Name: "S", Data: Join(bin...)}).Bytes()
	for _, c := range []pair{
		{name: "cells", extractor: gofastooxml.Extractor{MaxCells: 101}},
		{name: "text", opts: gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 2000}}},
		{name: "none"},
	} {
		c.xlsx, c.xlsb = x, b
		t.Run(c.name, func(t *testing.T) {
			if x, b := c.docs(t); x != b {
				t.Errorf("the .xlsb differs from the .xlsx:\n%s", diffLines(x, b))
			}
		})
	}
	bands := func(data []byte) string {
		f, err := gofastooxml.OpenBytes(data, &gofastcore.Options{Workers: 2})
		if err != nil {
			t.Fatal(err)
		}
		var out strings.Builder
		for part, err := range f.Rows(context.Background(), 1, 70) {
			if err != nil {
				t.Fatal(err)
			}
			j, _ := json.Marshal(part)
			out.Write(j)
			out.WriteByte('\n')
		}
		return out.String()
	}
	if x, b := bands(x), bands(b); x != b {
		t.Errorf("the bands of the .xlsb differ from those of the .xlsx:\n%s", diffLines(x, b))
	}
}
