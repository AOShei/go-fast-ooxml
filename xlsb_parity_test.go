package gofastooxml_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	. "github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// What an .xlsx file holds as text and an .xlsb file compiles: references
// to a table's columns by name, to other workbooks, and calls of the
// functions of macro sheets; and pivot tables, their caches, and the
// formulas of calculated fields. Each workbook is written both ways, and
// the two documents are compared whole.

const (
	ctXlsbLink      = "application/vnd.ms-excel.externalLink"
	ctXlsbPivot     = "application/vnd.ms-excel.pivotTable"
	ctXlsbCacheDef  = "application/vnd.ms-excel.pivotCacheDefinition"
	ctXlsbCacheRecs = "application/vnd.ms-excel.pivotCacheRecords"
)

// ptgList is a reference to a table's rows and columns (PtgList).
func ptgList(flags uint16, table uint32, c0, c1 uint16) []byte {
	return Join(U8(0x18), U8(0x19), U16(0), U16(flags), U32(table), U16(c0), U16(c1))
}

// formulaBook is a workbook with a table, Scores, on A1:B3, written as
// XML and as records, and the formulas beside it.
type parityFormula struct {
	ref, xml string
	rgce     []byte
}

func formulaBook() (x, b *Package, formulas []parityFormula) {
	// A table, Scores, on A1:B3, with formulas beside it that name its
	// columns, cells and names of another workbook, and a function of
	// macro sheets.
	formulas = []parityFormula{
		{"C2", "SUM(Scores[Score])", Join(ptgList(1, 1, 1, 1), ptgFuncVar(1, 4))},
		{"D2", "Scores[[#This Row],[Score]]*2", Join(ptgList(1|0x10<<2, 1, 1, 1), ptgInt(2), U8(0x05))},
		{"E2", "COUNTA(Scores[#All])", Join(ptgList(1<<2, 1, 0, 0), ptgFuncVar(1, 169))},
		{"F2", "ROWS(Scores[[#Headers],[Name]:[Score]])", Join(ptgList(2|2<<2, 1, 0, 1), ptgFunc(76))},
		{"G2", "[1]Rates!A1", ptgRef3d(1, 0, 0)},
		{"H2", "SUM('[1]Q 2'!A1)", Join(ptgRef3d(2, 0, 0), ptgFuncVar(1, 4))},
		{"I2", "[1]!Tax*2", Join(U8(0x39), U16(3), U32(1), ptgInt(2), U8(0x05))},
		{"J2", "S!A1&Kind", Join(ptgRef3d(0, 0, 0), ptgName(1), U8(ptgConcat))},
	}
	var xmlCells strings.Builder
	var bin []byte
	for i, f := range formulas {
		fmt.Fprintf(&xmlCells, `<c r="%s"><f>%s</f><v>%d</v></c>`, f.ref, strings.ReplaceAll(f.xml, "&", "&amp;"), i)
		bin = append(bin, Cell(XFmlaNum, int32(2+i), 0, F64(float64(i)), Fmla(f.rgce, nil))...)
	}
	sheetXML := xlRow(1, xlStr("A1", "Name"), xlStr("B1", "Score")) +
		`<row r="2">` + xlStr("A2", "x") + xlNum("B2", "1") + xmlCells.String() + `</row>` +
		xlRow(3, xlStr("A3", "y"), xlNum("B3", "2"))

	x = Xlsx(Sheet{Name: "S", Data: sheetXML, Extra: `<tableParts count="1"><tablePart r:id="rIdT"/></tableParts>`})
	x.RelID("xl/worksheets/sheet1.xml", "rIdT", RelTable, "../tables/table1.xml")
	x.Add("xl/tables/table1.xml", ctTable, `<table xmlns="`+NSSheet+`" id="1" name="Scores" displayName="Scores" ref="A1:B3">`+
		`<tableColumns count="2"><tableColumn id="1" name="Name"/><tableColumn id="2" name="Score"/></tableColumns></table>`)
	linkID := x.Rel("xl/workbook.xml", relBase+"externalLink", "externalLinks/externalLink1.xml")
	x.Add("xl/externalLinks/externalLink1.xml", "application/vnd.openxmlformats-officedocument.spreadsheetml.externalLink+xml",
		`<externalLink xmlns="`+NSSheet+`" xmlns:r="`+NSRel+`"><externalBook r:id="rId1"><sheetNames><sheetName val="Rates"/><sheetName val="Q 2"/></sheetNames>`+
			`<definedNames><definedName name="Tax"/></definedNames></externalBook></externalLink>`)
	x.Add("xl/workbook.xml", CTWorkbook, `<workbook xmlns="`+NSSheet+`" xmlns:r="`+NSRel+`"><sheets><sheet name="S" sheetId="1" r:id="rId1"/></sheets>`+
		`<externalReferences><externalReference r:id="`+linkID+`"/></externalReferences>`+
		`<definedNames><definedName name="Kind">GET.CELL(5,S!$A$1)</definedName></definedNames></workbook>`)

	b = Xlsb(nil, nil, Join(
		Rec(353), Rec(XSupSelf), Rec(XSupBookSrc, WStr("rIdL")),
		Rec(XExtSheet, U32(4), I32(0), I32(0), I32(0), I32(1), I32(0), I32(0), I32(1), I32(1), I32(1), I32(1), I32(-2), I32(-2)), Rec(354),
		bname("Kind", -1, Join(ptgInt(5), Join(U8(0x3A), U16(0), I32(0), U16(0)), ptgFuncVar(2, 185))),
	), XlsbSheet{Name: "S",
		Data: Join(Row(0, 0, 300), Cell(XCellSt, 0, 0, WStr("Name")), Cell(XCellSt, 1, 0, WStr("Score")),
			Row(1, 0, 300), Cell(XCellSt, 0, 0, WStr("x")), Cell(XCellRk, 1, 0, U32(1<<2|2)), bin,
			Row(2, 0, 300), Cell(XCellSt, 0, 0, WStr("y")), Cell(XCellRk, 1, 0, U32(2<<2|2))),
		After: Join(Rec(660), Rec(XListPart, WStr("rIdT")), Rec(662)),
	})
	b.RelID("xl/worksheets/sheet1.bin", "rIdT", RelTable, "../tables/table1.bin")
	b.AddBytes("xl/tables/table1.bin", CTXlsbTable, Join(
		Rec(XBeginList, Rect(0, 2, 0, 1), U32(0), U32(1), U32(1), U32(0), make([]byte, 32), WStr("Scores"), WStr("Scores"), WStr(""), WStr(""), WStr("")),
		Rec(345, U32(2)),
		Rec(347, U32(1), make([]byte, 20), WStr("Name"), WStr(""), WStr(""), WStr(""), WStr(""), WStr("")), Rec(348),
		Rec(347, U32(2), make([]byte, 20), WStr("Score"), WStr(""), WStr(""), WStr(""), WStr(""), WStr("")), Rec(348),
		Rec(346), Rec(344)))
	b.RelID("xl/workbook.bin", "rIdL", relBase+"externalLink", "externalLinks/externalLink1.bin")
	b.ExternalRel("xl/externalLinks/externalLink1.bin", relBase+"externalLinkPath", "Rates.xlsx")
	b.AddBytes("xl/externalLinks/externalLink1.bin", ctXlsbLink, Join(
		Rec(360, U16(0), WStr("rId1"), U32(0xFFFFFFFF)),
		Rec(359, U32(2), WStr("Rates"), WStr("Q 2")),
		Rec(577, WStr("Tax")), Rec(586, U16(0), U32(0), U8(0)),
	))
	return x, b, formulas
}

func TestXlsbFormulasAsXlsx(t *testing.T) {
	x, b, formulas := formulaBook()
	c := pair{name: "formulas", xlsx: x.Bytes(), xlsb: b.Bytes(), opts: gofastcore.Options{Cells: true}}
	xd, bd := c.docs(t)
	if xd != bd {
		t.Errorf("the .xlsb differs from the .xlsx:\n%s", diffLines(xd, bd))
	}
	for _, f := range formulas {
		if !strings.Contains(bd, strings.ReplaceAll(fmt.Sprintf("%q", f.xml), "&", `\u0026`)) {
			t.Errorf("no formula %s", f.xml)
		}
	}
	if !strings.Contains(bd, `"Kind"`) || !strings.Contains(bd, "GET.CELL(5,S!$A$1)") {
		t.Errorf("the name's formula is not read back:\n%s", bd)
	}
	if strings.Contains(bd, "not read back") {
		t.Errorf("formulas not read back:\n%s", bd)
	}
}

// The workbook of TestPivotTablesAndCaches, with a fourth cache whose
// records are packed, written as XML and as records.
func TestXlsbPivotsAsXlsx(t *testing.T) {
	data := xlRow(1, xlStr("A1", "Region"), xlStr("B1", "Sales")) + xlRow(2, xlStr("A2", "North"), xlNum("B2", "10")) +
		xlRow(5, xlStr("D5", "Row Labels"), xlStr("E5", "Sum of Sales")) + xlRow(6, xlStr("D6", "North"), xlNum("E6", "10")) +
		xlRow(7, xlStr("D7", "Grand Total"), xlNum("E7", "10"))
	x := Xlsx(Sheet{Name: "Data", Data: data})
	x.Rel(firstSheet, relBase+"pivotTable", "../pivotTables/pivotTable1.xml")
	x.Add("xl/pivotTables/pivotTable1.xml", "application/vnd.openxmlformats-officedocument.spreadsheetml.pivotTable+xml",
		`<pivotTableDefinition xmlns="`+NSSheet+`" name="SalesPivot" cacheId="1"><location ref="D5:E7" firstHeaderRow="1" firstDataRow="1" firstDataCol="1"/></pivotTableDefinition>`)
	cacheDef := func(source, fields string, records bool) string {
		rid := ""
		if records {
			rid = ` r:id="rId1"`
		}
		return `<pivotCacheDefinition xmlns="` + NSSheet + `" xmlns:r="` + NSRel + `"` + rid + `>` + source + `<cacheFields>` + fields + `</cacheFields></pivotCacheDefinition>`
	}
	ct := "application/vnd.openxmlformats-officedocument.spreadsheetml.pivotCacheDefinition+xml"
	ctRecs := "application/vnd.openxmlformats-officedocument.spreadsheetml.pivotCacheRecords+xml"
	x.Add("xl/pivotCache/pivotCacheDefinition1.xml", ct, cacheDef(`<cacheSource type="worksheet"><worksheetSource ref="A1:B2" sheet="Data"/></cacheSource>`,
		`<cacheField name="Region"><sharedItems/></cacheField><cacheField name="Sales"><sharedItems/></cacheField>`+
			`<cacheField name="Unit margin" formula="Sales*0.1" databaseField="0"><sharedItems/></cacheField>`, false))
	x.Add("xl/pivotCache/pivotCacheDefinition2.xml", ct, cacheDef(`<cacheSource type="external" connectionId="1"/>`,
		`<cacheField name="Customer"><sharedItems><s v="Ada"/><s v="Grace"/></sharedItems></cacheField><cacheField name="Spent"><sharedItems containsNumber="1"/></cacheField>`+
			`<cacheField name="When"><sharedItems/></cacheField><cacheField name="Paid"><sharedItems/></cacheField>`, true))
	x.Rel("xl/pivotCache/pivotCacheDefinition2.xml", relBase+"pivotCacheRecords", "pivotCacheRecords2.xml")
	x.Add("xl/pivotCache/pivotCacheRecords2.xml", ctRecs,
		`<pivotCacheRecords xmlns="`+NSSheet+`"><r><x v="1"/><n v="1250.5"/><d v="2026-03-04T00:00:00"/><b v="1"/></r><r><x v="0"/><n v="7"/><d v="2026-03-05T14:30:00"/><m/></r></pivotCacheRecords>`)
	x.Add("xl/pivotCache/pivotCacheDefinition3.xml", ct, cacheDef(`<cacheSource type="worksheet"><worksheetSource ref="A1:A3" sheet="Archive"/></cacheSource>`,
		`<cacheField name="Code"><sharedItems><s v="X1"/><s v="Y2"/></sharedItems></cacheField><cacheField name="Kind"><sharedItems><s v="old"/></sharedItems></cacheField>`, false))
	x.Add("xl/pivotCache/pivotCacheDefinition4.xml", ct, cacheDef(`<cacheSource type="worksheet"><worksheetSource name="Gone"/></cacheSource>`,
		`<cacheField name="Team"><sharedItems><s v="Red"/><s v="Blue"/></sharedItems></cacheField><cacheField name="Points"><sharedItems containsNumber="1"/></cacheField>`+
			`<cacheField name="Day"><sharedItems containsDate="1"/></cacheField><cacheField name="Note"><sharedItems/></cacheField>`, true))
	x.Rel("xl/pivotCache/pivotCacheDefinition4.xml", relBase+"pivotCacheRecords", "pivotCacheRecords4.xml")
	x.Add("xl/pivotCache/pivotCacheRecords4.xml", ctRecs,
		`<pivotCacheRecords xmlns="`+NSSheet+`"><r><x v="1"/><n v="3"/><d v="2026-01-02T00:00:00"/><s v="first"/></r><r><x v="0"/><n v="-0.25"/><d v="2026-01-03T08:00:00"/><s v="second"/></r></pivotCacheRecords>`)
	var caches strings.Builder
	for i := 1; i <= 4; i++ {
		id := x.Rel("xl/workbook.xml", relBase+"pivotCacheDefinition", fmt.Sprintf("pivotCache/pivotCacheDefinition%d.xml", i))
		fmt.Fprintf(&caches, `<pivotCache cacheId="%d" r:id="%s"/>`, i, id)
	}
	x.Add("xl/workbook.xml", CTWorkbook, `<workbook xmlns="`+NSSheet+`" xmlns:r="`+NSRel+`"><sheets><sheet name="Data" sheetId="1" r:id="rId1"/></sheets><pivotCaches>`+caches.String()+`</pivotCaches></workbook>`)

	// The same as records.
	var book []byte
	for i := 1; i <= 4; i++ {
		book = append(book, Rec(386, U32(uint32(i)), WStr(fmt.Sprintf("rIdC%d", i)))...)
	}
	b := Xlsb(nil, nil, book, XlsbSheet{Name: "Data", Data: Join(
		Row(0, 0, 300), Cell(XCellSt, 0, 0, WStr("Region")), Cell(XCellSt, 1, 0, WStr("Sales")),
		Row(1, 0, 300), Cell(XCellSt, 0, 0, WStr("North")), Cell(XCellRk, 1, 0, U32(10<<2|2)),
		Row(4, 0, 300), Cell(XCellSt, 3, 0, WStr("Row Labels")), Cell(XCellSt, 4, 0, WStr("Sum of Sales")),
		Row(5, 0, 300), Cell(XCellSt, 3, 0, WStr("North")), Cell(XCellRk, 4, 0, U32(10<<2|2)),
		Row(6, 0, 300), Cell(XCellSt, 3, 0, WStr("Grand Total")), Cell(XCellRk, 4, 0, U32(10<<2|2)),
	)})
	b.Rel("xl/worksheets/sheet1.bin", relBase+"pivotTable", "../pivotTables/pivotTable1.bin")
	b.AddBytes("xl/pivotTables/pivotTable1.bin", ctXlsbPivot, Join(
		Rec(280, make([]byte, 28), U32(1), WStr("SalesPivot"), WStr("Values")),
		Rec(314, Rect(4, 6, 3, 4), I32(5), I32(5), I32(4), I32(0), I32(0)), Rec(315),
		Rec(281)))
	def := func(source []byte, records string, fields ...[]byte) []byte {
		flags := byte(0)
		var rid []byte
		if records != "" {
			flags = 2
			rid = WStr(records)
		}
		out := Join(Rec(179, U8(6), U8(3), U8(6), U8(1), U32(0), F64(0), U8(flags), U32(2), rid), source, Rec(181, U32(uint32(len(fields)))))
		for _, f := range fields {
			out = append(out, f...)
		}
		return append(out, Join(Rec(182), Rec(180))...)
	}
	field := func(name string, flags uint16, more ...[]byte) []byte {
		return Join(Rec(183, U16(flags), make([]byte, 18), WStr(name)), Join(more...), Rec(184))
	}
	formula := func(name string, rgce []byte, fields ...uint32) []byte {
		var names []byte
		for _, f := range fields {
			names = append(names, Rec(255, U32(f), U8(0), U8(0))...)
			names = append(names, Rec(256)...)
		}
		return Join(Rec(183, U16(0x100), make([]byte, 18), WStr(name), U32(uint32(len(rgce))), rgce, U32(0)),
			Rec(253, U32(uint32(len(fields)))), names, Rec(254), Rec(184))
	}
	atbl := func(flags uint16, items ...[]byte) []byte {
		return Join(Rec(189, U16(flags), U32(uint32(len(items)))), Join(items...), Rec(190))
	}
	date := func(y, mo uint16, d, h, mi byte) []byte { return Join(U16(y), U16(mo), U8(d), U8(h), U8(mi), U8(0)) }
	sheetSource := func(sheet string, r []byte) []byte {
		return Join(Rec(185, U32(0), U32(0)), Rec(187, U8(0), U8(0), U8(2), WStr(sheet), r), Rec(188), Rec(186))
	}
	const src = 0x04 // a field of the source's data
	caches4 := [][]byte{
		def(sheetSource("Data", Rect(0, 1, 0, 1)), "",
			field("Region", src), field("Sales", src),
			formula("Unit margin", Join(U8(0x18), U8(0x1D), U32(0), U8(0x1F), F64(0.1), U8(0x05)), 1)),
		def(Join(Rec(185, U32(1), U32(1)), Rec(186)), "rId1",
			field("Customer", src, atbl(0x0B, Rec(24, WStr("Ada")), Rec(24, WStr("Grace")))),
			field("Spent", src, atbl(0x42)), field("When", src, atbl(0x04)), field("Paid", src, atbl(0x01))),
		def(sheetSource("Archive", Rect(0, 2, 0, 0)), "",
			field("Code", src, atbl(0x0B, Rec(24, WStr("X1")), Rec(24, WStr("Y2")))),
			field("Kind", src, atbl(0x0B, Rec(24, WStr("old"))))),
		// Packed records: a field with values listed by number, and fields
		// with none, which pack their own as numbers, dates and text.
		def(Join(Rec(185, U32(0), U32(0)), Rec(187, U8(1), U8(0), U8(0), WStr("Gone")), Rec(188), Rec(186)), "rId1",
			field("Team", src, Rec(189, U16(0x0B), U32(2)), Rec(191, U16(2), U32(2), WStr("Red"), WStr("Blue")), Rec(192), Rec(190)),
			field("Points", src, atbl(0x42)), field("Day", src, atbl(0x04)), field("Note", src, atbl(0x0B))),
	}
	for i, c := range caches4 {
		name := fmt.Sprintf("pivotCache/pivotCacheDefinition%d.bin", i+1)
		b.RelID("xl/workbook.bin", fmt.Sprintf("rIdC%d", i+1), relBase+"pivotCacheDefinition", name)
		b.AddBytes("xl/"+name, ctXlsbCacheDef, c)
	}
	b.RelID("xl/pivotCache/pivotCacheDefinition2.bin", "rId1", relBase+"pivotCacheRecords", "pivotCacheRecords2.bin")
	b.AddBytes("xl/pivotCache/pivotCacheRecords2.bin", ctXlsbCacheRecs, Join(Rec(193, U32(2)),
		Rec(34), Rec(26, U32(1)), Rec(21, F64(1250.5)), Rec(25, date(2026, 3, 4, 0, 0)), Rec(22, U8(1)),
		Rec(34), Rec(26, U32(0)), Rec(21, F64(7)), Rec(25, date(2026, 3, 5, 14, 30)), Rec(20),
		Rec(194)))
	b.RelID("xl/pivotCache/pivotCacheDefinition4.bin", "rId1", relBase+"pivotCacheRecords", "pivotCacheRecords4.bin")
	b.AddBytes("xl/pivotCache/pivotCacheRecords4.bin", ctXlsbCacheRecs, Join(Rec(193, U32(2)),
		Rec(33, U32(1), F64(3), date(2026, 1, 2, 0, 0), WStr("first")),
		Rec(33, U32(0), F64(-0.25), date(2026, 1, 3, 8, 0), WStr("second")),
		Rec(194)))

	c := pair{name: "pivots", xlsx: x.Bytes(), xlsb: b.Bytes()}
	xd, bd := c.docs(t)
	if xd != bd {
		t.Errorf("the .xlsb differs from the .xlsx:\n%s", diffLines(xd, bd))
	}
	doc := excel(t, b, nil)
	expectSheets(t, doc,
		`sheet 1 "Data"`,
		`  table A1:B2 inferred [["Region" "Sales"] ["North" "10"]]`,
		`  table D5:E7 name=SalesPivot role=pivot header=1 [["Row Labels" "Sum of Sales"] ["North" "10"] ["Grand Total" "10"]]`,
		`stored 2 ""`,
		`  table  stored=pivot_cache header=1 [["Customer" "Spent" "When" "Paid"] ["Grace" "1250.5" "2026-03-04" "TRUE"] ["Ada" "7" "2026-03-05T14:30:00" ""]]`,
		`  table  stored=pivot_cache header=1 [["Code" "Kind"] ["X1" "old"] ["Y2" ""]]`,
		`  table  stored=pivot_cache header=1 [["Team" "Points" "Day" "Note"] ["Blue" "3" "2026-01-02" "first"] ["Red" "-0.25" "2026-01-03T08:00:00" "second"]]`,
		`  paragraph  "Unit margin: =Sales*0.1" name=Unit margin stored=pivot_cache`,
	)
}

// LibreOffice reads the same binary workbook: its .xlsx has the same text
// for references to sheets and other workbooks, and resolves each
// reference to the table's columns to the table, writing it as a range of
// its own (OFFSET of the table). It reads neither another workbook's
// names nor the functions of macro sheets, which are checked against the
// specification only.
func TestXlsbFormulasAgainstLibreOfficeToo(t *testing.T) {
	if testing.Short() {
		t.Skip("converts a binary workbook with LibreOffice")
	}
	_, b, formulas := formulaBook()
	data := b.Bytes()
	lo := LibreOfficeXlsx(t, data)
	read := func(d []byte) map[string]map[[2]int]sheetCell {
		doc, err := extract(t, d, &gofastcore.Options{Cells: true})
		if err != nil {
			t.Fatal(err)
		}
		return xlsbCells(doc)
	}
	ours, theirs := read(data), read(lo)
	for i, f := range formulas {
		at := [2]int{1, 2 + i}
		o, l := ours["S"][at].detail.Formula, theirs["S"][at].detail.Formula
		switch {
		case o != f.xml:
			t.Errorf("%s: %q", f.ref, o)
		case strings.Contains(f.xml, "Scores["):
			if !strings.Contains(l, "Scores") || strings.Contains(l, "#") {
				t.Errorf("%s: LibreOffice %q", f.ref, l)
			}
		case strings.Contains(f.xml, "[1]!"):
		case l != o:
			t.Errorf("%s: %q, LibreOffice %q", f.ref, o, l)
		}
	}
}

// The workbook's tables are read once, when a formula first names one,
// whichever of the sheets read at once asks first.
func TestXlsbTablesForSheetsAtOnce(t *testing.T) {
	var sheets []XlsbSheet
	for i := range 8 {
		sheets = append(sheets, XlsbSheet{Name: fmt.Sprintf("S%d", i), Data: Join(Row(0, 0, 300),
			Cell(XFmlaNum, 0, 0, F64(3), Fmla(Join(ptgList(1, 1, 1, 1), ptgFuncVar(1, 4)), nil)))})
	}
	sheets[0].After = Join(Rec(660), Rec(XListPart, WStr("rIdT")), Rec(662))
	b := Xlsb(nil, nil, nil, sheets...)
	b.RelID("xl/worksheets/sheet1.bin", "rIdT", RelTable, "../tables/table1.bin")
	b.AddBytes("xl/tables/table1.bin", CTXlsbTable, Join(
		Rec(XBeginList, Rect(2, 4, 0, 1), U32(0), U32(1), U32(1), U32(0), make([]byte, 32), WStr("Scores"), WStr("Scores"), WStr(""), WStr(""), WStr("")),
		Rec(347, U32(1), make([]byte, 20), WStr("Name")), Rec(348), Rec(347, U32(2), make([]byte, 20), WStr("Score")), Rec(348), Rec(344)))
	for range 20 {
		doc, err := extract(t, b.Bytes(), &gofastcore.Options{Cells: true, Workers: 8})
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range doc.Parts {
			if len(p.Blocks) == 0 || p.Blocks[0].Table == nil || len(p.Blocks[0].Table.Cells) == 0 || p.Blocks[0].Table.Cells[0].Formula != "SUM(Scores[Score])" {
				t.Fatalf("%s: %+v", p.Name, p.Blocks)
			}
		}
	}
}
