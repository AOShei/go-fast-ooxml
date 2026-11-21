package gofastooxml_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	. "github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// FuzzXlsb gives the reader of binary workbooks arbitrary records for a
// sheet, its styles, its shared strings, its comments and the workbook's
// own, inside a sound package. Whatever they hold, the result is a valid
// document or one of the shared errors, and the sheet a band of rows at
// a time has the cells of the sheet read whole.
func FuzzXlsb(f *testing.F) {
	sheet := Join(Rec(129), Rec(145),
		Row(0, 0, 300), Cell(XCellRk, 0, 1, U32(42<<2|2)), Cell(XCellIsst, 1, 0, U32(0)), Cell(XCellSt, 2, 2, WStr("x")),
		Cell(XFmlaNum, 3, 0, F64(2), Fmla(Join(ptgInt(1), ptgInt(1), U8(ptgAdd)), nil)),
		Row(1, 0x1000, 300), Cell(XCellBool, 0, 0, U8(1)), Cell(XCellError, 1, 0, U8(7)), Rec(XShortReal, U32(3), F64(1.5)),
		Cell(XFmlaNum, 2, 0, F64(4), Fmla(Join(U8(0x01), I32(1)), I32(2))),
		Rec(XShrFmla, Rect(1, 2, 2, 2), U32(7), ptgRefN(0, -1), U32(0)),
		Rec(146),
		Rec(XColInfo, I32(1), I32(2), I32(0), I32(0), U16(1)),
		Rec(XMergeCell, Rect(0, 1, 0, 1)),
		Rec(XHLink, Rect(0, 0, 0, 0), WStr(""), WStr("S!A1"), WStr(""), WStr("")),
		Rec(XDVal, U32(3), U32(1), Rect(0, 1, 5, 5), WStr(""), WStr(""), WStr(""), WStr(""), U32(8), ptgStr("a,b"), U32(0), U32(0), U32(0)),
		Rec(XListPart, WStr("rIdT")),
		Rec(130))
	styles := Join(Rec(278), Rec(615), Rec(44, U16(164), WStr("0.0%")), Rec(616),
		Rec(611), Rec(43, make([]byte, 21), WStr("Calibri")), Rec(43, make([]byte, 21), WStr("Wingdings")), Rec(612),
		Rec(617), Rec(47, U16(0), U16(0), U16(0), make([]byte, 10)), Rec(47, U16(0), U16(14), U16(0), make([]byte, 10)), Rec(47, U16(0), U16(164), U16(1), make([]byte, 10)), Rec(618), Rec(279))
	strs := Join(Rec(159), Rec(19, RStr("one")), Rec(19, RStr("two")), Rec(160))
	comments := Join(Rec(628), Rec(630), Rec(632, WStr("Ann")), Rec(631), Rec(633),
		Rec(635, I32(0), Rect(0, 0, 0, 0), make([]byte, 16)), Rec(637, RStr("a note")), Rec(636), Rec(634), Rec(629))
	book := Join(Rec(XSupSelf), Rec(XExtSheet, U32(1), I32(0), I32(0), I32(0)),
		bname("Rate", -1, Join(U8(0x1F), F64(0.2))), bname("Here", 0, Join(U8(0x3A), U16(0), I32(0), U16(0))))
	f.Add(sheet, styles, strs, comments, book)
	f.Add(sheet[:len(sheet)/2], styles[:len(styles)/3], strs, comments[:9], book[:7])
	opts := &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 4 << 20}, Workers: 1, Cells: true, Spans: true}
	e := &gofastooxml.Extractor{MaxCells: 100_000}
	f.Fuzz(func(t *testing.T, sheet, styles, strs, comments, book []byte) {
		pkg := Xlsb(&XlsbStyles{}, []string{}, book, XlsbSheet{Name: "S"}, XlsbSheet{Name: "T", State: 1})
		pkg.AddBytes("xl/worksheets/sheet1.bin", CTXlsbSheet, sheet)
		pkg.AddBytes("xl/styles.bin", CTXlsbStyles, styles)
		pkg.AddBytes("xl/sharedStrings.bin", CTXlsbStrings, strs)
		pkg.Rel("xl/worksheets/sheet1.bin", RelComments, "../comments1.bin")
		pkg.AddBytes("xl/comments1.bin", CTXlsbComments, comments)
		pkg.RelID("xl/worksheets/sheet1.bin", "rIdT", RelTable, "../tables/table1.bin")
		pkg.AddBytes("xl/tables/table1.bin", CTXlsbTable, comments)
		data := pkg.Bytes()
		doc, err := e.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts)
		checkOutcome(t, "fuzz input", doc, err)
		file, err := e.OpenBytes(data, opts)
		if err != nil {
			return
		}
		var bands []gofastcore.Part
		limited := doc != nil && (doc.Parts[0].Has(gofastcore.WarnLimitExceeded) || doc.Parts[0].Has(gofastcore.WarnPartFailed))
		for part, err := range file.Rows(context.Background(), 1, 2) {
			if err != nil {
				t.Fatalf("rows: %v", err)
			}
			band := gofastcore.New("xlsx")
			band.Parts = []gofastcore.Part{part}
			if verr := band.Validate(); verr != nil {
				t.Errorf("a band of rows is not a valid part: %v", verr)
			}
			limited = limited || part.Has(gofastcore.WarnLimitExceeded) || part.Has(gofastcore.WarnPartFailed)
			bands = append(bands, part)
		}
		if doc != nil && !limited {
			if got, want := len(fuzzCells(bands...)), len(fuzzCells(doc.Parts[0])); got != want {
				t.Errorf("%d cells a band at a time, %d in the sheet read whole", got, want)
			}
		}
	})
}

// FuzzXlsbLinksTablesPivots gives arbitrary records for the parts that a
// binary workbook's formulas and pivot tables are read from: a link to
// another workbook, a table with its columns, a pivot table, and a pivot
// cache's definition and records, inside a sound package whose sheet has
// formulas that name them. Whatever they hold, the result is a valid
// document or one of the shared errors.
func FuzzXlsbLinksTablesPivots(f *testing.F) {
	link := Join(Rec(360, U16(0), WStr("rId1"), U32(0xFFFFFFFF)), Rec(359, U32(2), WStr("Rates"), WStr("Q 2")),
		Rec(577, WStr("Tax")), Rec(586, U16(0), U32(1), U8(0)))
	table := Join(Rec(XBeginList, Rect(0, 2, 0, 1), U32(0), U32(1), U32(1), U32(0), make([]byte, 32), WStr("Scores"), WStr("Scores"), WStr(""), WStr(""), WStr("")),
		Rec(347, U32(1), make([]byte, 20), WStr("Name")), Rec(348), Rec(347, U32(2), make([]byte, 20), WStr("Score")), Rec(348), Rec(344))
	pivot := Join(Rec(280, make([]byte, 28), U32(1), WStr("P")), Rec(314, Rect(4, 6, 3, 4), I32(5), I32(5), I32(4), I32(0), I32(0)))
	def := Join(Rec(179, U8(6), U8(3), U8(6), U8(1), U32(0), F64(0), U8(2), U32(2), WStr("rId1")),
		Rec(185, U32(0), U32(0)), Rec(187, U8(1), U8(0), U8(0), WStr("Gone")), Rec(188), Rec(186),
		Rec(181, U32(3)),
		Rec(183, U16(4), make([]byte, 18), WStr("Team")), Rec(189, U16(0x0B), U32(2)), Rec(191, U16(2), U32(2), WStr("Red"), WStr("Blue")), Rec(192), Rec(190), Rec(184),
		Rec(183, U16(4), make([]byte, 18), WStr("Points")), Rec(189, U16(0x42), U32(1)), Rec(21, F64(3)), Rec(25, U16(2026), U16(1), U8(2), U8(0), U8(0), U8(0)), Rec(190), Rec(184),
		Rec(183, U16(0x100), make([]byte, 18), WStr("Twice"), U32(11), U8(0x18), U8(0x1D), U32(0), ptgInt(2), U8(0x05), U32(0)),
		Rec(253, U32(1)), Rec(255, U32(1), U8(0), U8(0)), Rec(256), Rec(254), Rec(184),
		Rec(182), Rec(180))
	records := Join(Rec(193, U32(2)), Rec(33, U32(1), U32(0)), Rec(34), Rec(26, U32(0)), Rec(21, F64(7)), Rec(194))
	f.Add(link, table, pivot, def, records)
	f.Add(link[:9], table[:40], pivot[:20], def[:len(def)/2], records[:7])
	opts := &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 4 << 20}, Workers: 1, Cells: true}
	e := &gofastooxml.Extractor{MaxCells: 100_000}
	book := Join(Rec(353), Rec(XSupSelf), Rec(XSupBookSrc, WStr("rIdL")),
		Rec(XExtSheet, U32(3), I32(0), I32(0), I32(0), I32(1), I32(0), I32(1), I32(1), I32(-2), I32(-2)), Rec(354),
		Rec(386, U32(1), WStr("rIdC")))
	sheet := XlsbSheet{Name: "S",
		Data: Join(Row(0, 0, 300),
			Cell(XFmlaNum, 2, 0, F64(1), Fmla(Join(ptgList(1, 1, 1, 1), ptgFuncVar(1, 4)), nil)),
			Cell(XFmlaNum, 3, 0, F64(2), Fmla(ptgRef3d(1, 0, 0), nil)),
			Cell(XFmlaNum, 4, 0, F64(3), Fmla(Join(U8(0x39), U16(2), U32(1)), nil))),
		After: Join(Rec(660), Rec(XListPart, WStr("rIdT")), Rec(662))}
	f.Fuzz(func(t *testing.T, link, table, pivot, def, records []byte) {
		pkg := Xlsb(nil, nil, book, sheet)
		pkg.RelID("xl/workbook.bin", "rIdL", relBase+"externalLink", "externalLinks/externalLink1.bin")
		pkg.AddBytes("xl/externalLinks/externalLink1.bin", ctXlsbLink, link)
		pkg.RelID("xl/worksheets/sheet1.bin", "rIdT", RelTable, "../tables/table1.bin")
		pkg.AddBytes("xl/tables/table1.bin", CTXlsbTable, table)
		pkg.Rel("xl/worksheets/sheet1.bin", relBase+"pivotTable", "../pivotTables/pivotTable1.bin")
		pkg.AddBytes("xl/pivotTables/pivotTable1.bin", ctXlsbPivot, pivot)
		pkg.RelID("xl/workbook.bin", "rIdC", relBase+"pivotCacheDefinition", "pivotCache/pivotCacheDefinition1.bin")
		pkg.AddBytes("xl/pivotCache/pivotCacheDefinition1.bin", ctXlsbCacheDef, def)
		pkg.RelID("xl/pivotCache/pivotCacheDefinition1.bin", "rId1", relBase+"pivotCacheRecords", "pivotCacheRecords1.bin")
		pkg.AddBytes("xl/pivotCache/pivotCacheRecords1.bin", ctXlsbCacheRecs, records)
		data := pkg.Bytes()
		doc, err := e.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts)
		checkOutcome(t, "fuzz input", doc, err)
	})
}
