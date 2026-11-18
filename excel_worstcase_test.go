package gofastooxml_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// Workbooks made to cost as much as they can. Each is small and must be
// read in the time a workbook of its size takes, with a valid result.

// A staircase: cut along rows it leaves one step and the rest, cut along
// columns the same again, as many times as there are steps. Cutting stops
// after a fixed number of rounds.
func TestExcelWorstCaseStaircase(t *testing.T) {
	var rows strings.Builder
	const steps = 4000
	for i := 0; i < steps; i++ {
		// A step: one cell, two rows and two columns on from the last.
		rows.WriteString(xlRow(2*i+1, xlNum(colName(2*i)+strconv.Itoa(2*i+1), "1")))
	}
	// And under the stairs, a block that every round would carry along.
	for r := 2*steps + 1; r < 2*steps+401; r++ {
		cells := make([]string, 0, 500)
		for c := 2 * steps; c < 2*steps+500; c++ {
			cells = append(cells, xlNum(colName(c)+strconv.Itoa(r), "2"))
		}
		rows.WriteString(xlRow(r, cells...))
	}
	data := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: rows.String()}).Bytes()
	within(t, 20*time.Second, "a staircase of cells", func() {
		doc, err := extract(t, data, nil)
		if err != nil {
			t.Error(err)
			return
		}
		ooxmltest.CheckDocument(t, doc)
		cells := 0
		for _, b := range doc.Parts[0].Blocks {
			for _, row := range b.Table.Rows {
				for _, c := range row {
					if c != "" {
						cells++
					}
				}
			}
		}
		if cells != steps+400*500 {
			t.Errorf("%d cells kept, want %d", cells, steps+400*500)
		}
	})
}

// Many small tables, and as many hidden columns as a sheet can have: each
// table looks up the hidden columns that cross it, not all of them.
func TestExcelWorstCaseHiddenColumns(t *testing.T) {
	var cols, rows strings.Builder
	cols.WriteString(`<cols>`)
	for c := 1; c <= 16384; c += 2 {
		fmt.Fprintf(&cols, `<col min="%d" max="%d" hidden="1"/>`, c, c)
	}
	cols.WriteString(`</cols>`)
	// Sixty thousand cells, none next to another: sixty thousand tables.
	for r := 1; r <= 300; r++ {
		cells := make([]string, 0, 200)
		for c := 0; c < 400; c += 2 {
			cells = append(cells, xlNum(colName(c+(r%2)*40)+strconv.Itoa(2*r), "1"))
		}
		rows.WriteString(xlRow(2*r, cells...))
	}
	data := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: rows.String(), Extra: cols.String()}).Bytes()
	within(t, 20*time.Second, "many tables under many hidden columns", func() {
		doc, err := extract(t, data, nil)
		if err != nil {
			t.Error(err)
			return
		}
		ooxmltest.CheckDocument(t, doc)
		if n := len(doc.Parts[0].Blocks); n != 60000 {
			t.Errorf("%d blocks", n)
		}
	})
}

// Thousands of tables declared over the same cells. Real tables do not
// overlap; after a few passes over the sheet the rest are not looked for.
func TestExcelWorstCaseOverlappingTables(t *testing.T) {
	var rows, parts strings.Builder
	for r := 1; r <= 2000; r++ {
		cells := make([]string, 0, 100)
		for c := 0; c < 100; c++ {
			cells = append(cells, xlNum(colName(c)+strconv.Itoa(r), "1"))
		}
		rows.WriteString(xlRow(r, cells...))
	}
	parts.WriteString(`<tableParts>`)
	pkg := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S"})
	for i := 1; i <= 4000; i++ {
		id := pkg.Rel(firstSheet, relBase+"table", fmt.Sprintf("../tables/table%d.xml", i))
		// Each claims a column no table before it has, and every row.
		pkg.Add(fmt.Sprintf("xl/tables/table%d.xml", i), ctTable, fmt.Sprintf(`<table xmlns="%s" ref="%s1:%s2000"/>`, ooxmltest.NSSheet, colName(i%100), colName(i%100)))
		fmt.Fprintf(&parts, `<tablePart r:id="%s"/>`, id)
	}
	parts.WriteString(`</tableParts>`)
	pkg.Add(firstSheet, ooxmltest.CTWorksheet, `<worksheet xmlns="`+ooxmltest.NSSheet+`" xmlns:r="`+ooxmltest.NSRel+`"><sheetData>`+rows.String()+`</sheetData>`+parts.String()+`</worksheet>`)
	data := pkg.Bytes()
	within(t, 20*time.Second, "thousands of overlapping tables", func() {
		doc, err := extract(t, data, nil)
		if err != nil {
			t.Error(err)
			return
		}
		ooxmltest.CheckDocument(t, doc)
	})
}

// One cell in the first place of a sheet and one in the last: two tables
// of one cell, not a grid of seventeen thousand million.
func TestExcelFirstAndLastCell(t *testing.T) {
	data := xlRow(1, xlStr("A1", "first")) + xlRow(1048576, xlStr("XFD1048576", "last"))
	expectSheets(t, excel(t, ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: data}), nil),
		`sheet 1 "S"`,
		`  table A1:A1 inferred [["first"]]`,
		`  table XFD1048576:XFD1048576 inferred [["last"]]`,
	)
}

// A file embedded in a sheet is handed to the caller's extractor.
func TestExcelEmbeddedFiles(t *testing.T) {
	inner := ooxmltest.Docx(ooxmltest.Para("inside the embedded document")).Bytes()
	build := func() *ooxmltest.Package {
		pkg := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: xlRow(1, xlStr("A1", "host"))}, ooxmltest.Sheet{Name: "T", Data: xlRow(1, xlStr("A1", "second"))})
		pkg.Rel(firstSheet, relBase+"package", "../embeddings/Document1.docx")
		pkg.Rel("xl/worksheets/sheet2.xml", relBase+"package", "../embeddings/Document1.docx") // the same file again
		pkg.AddBytes("xl/embeddings/Document1.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", inner)
		return pkg
	}
	// With no extractor for it, it is listed.
	doc := excel(t, build(), nil)
	if len(doc.Children) != 1 || doc.Children[0].Source.Name != "Document1.docx" || len(doc.Children[0].Parts) != 0 {
		t.Fatalf("children = %+v", doc.Children)
	}
	// With one, it is read, once.
	calls := 0
	doc = excel(t, build(), &gofastcore.Options{Child: func(ctx context.Context, name, mime string, r io.ReaderAt, size int64, o *gofastcore.Options) (*gofastcore.Document, error) {
		calls++
		return gofastooxml.Extract(ctx, r, size, o)
	}})
	if calls != 1 || len(doc.Children) != 1 || !strings.Contains(doc.Children[0].Text(), "inside the embedded document") {
		t.Errorf("%d calls; children = %+v", calls, doc.Children)
	}
}

// What a limit leaves out of a workbook, and which sheet carries the
// bytes of a picture that several show, do not depend on how many
// workers read the sheets.
func TestExcelLimitsAndPicturesAreTheSameForAnyWorkers(t *testing.T) {
	var sheets []ooxmltest.Sheet
	for i := 1; i <= 24; i++ {
		var rows strings.Builder
		for r := 1; r <= 40; r++ {
			rows.WriteString(xlRow(r, xlNum("A"+strconv.Itoa(r), strconv.Itoa(r*i)), xlStr("B"+strconv.Itoa(r), strings.Repeat("x", 10*i))))
		}
		sheets = append(sheets, ooxmltest.Sheet{Name: "S" + strconv.Itoa(i), Data: rows.String(), Extra: `<drawing r:id="rId1"/>`})
	}
	pkg := ooxmltest.Xlsx(sheets...)
	drawing := drawingHeader + `<xdr:twoCellAnchor><xdr:from><xdr:col>3</xdr:col><xdr:row>1</xdr:row></xdr:from>` +
		`<xdr:pic><xdr:nvPicPr><xdr:cNvPr id="3" name="Picture"/><xdr:cNvPicPr/></xdr:nvPicPr><xdr:blipFill><a:blip r:embed="rId1"/></xdr:blipFill><xdr:spPr/></xdr:pic></xdr:twoCellAnchor></xdr:wsDr>`
	for i := 1; i <= len(sheets); i++ {
		sheet, name := fmt.Sprintf("xl/worksheets/sheet%d.xml", i), fmt.Sprintf("xl/drawings/drawing%d.xml", i)
		pkg.Rel(sheet, relBase+"drawing", fmt.Sprintf("../drawings/drawing%d.xml", i))
		pkg.Add(name, ctDrawing, drawing)
		pkg.Rel(name, relBase+"image", "../media/image1.png")
	}
	pkg.AddBytes("xl/media/image1.png", "image/png", pngBytes(t, 3, 2))
	data := pkg.Bytes()

	for name, c := range map[string]struct {
		extractor gofastooxml.Extractor
		limits    gofastcore.Limits
	}{
		"no limit":          {},
		"decompressed size": {limits: gofastcore.Limits{MaxDecodedBytes: 60_000}},
		"cells":             {extractor: gofastooxml.Extractor{MaxCells: 777}},
	} {
		var want string
		for _, workers := range []int{1, 2, 7, 64} {
			doc, err := c.extractor.Extract(t.Context(), bytes.NewReader(data), int64(len(data)), &gofastcore.Options{Workers: workers, Limits: c.limits, Images: gofastcore.ImagesData})
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			ooxmltest.CheckDocument(t, doc)
			var sb strings.Builder
			limited, withBytes := 0, 0
			for _, p := range doc.Parts {
				fmt.Fprintf(&sb, "%d %v\n", p.Number, p.Warnings)
				if p.Has(gofastcore.WarnLimitExceeded) || p.Has(gofastcore.WarnPartFailed) {
					limited++
				}
				for _, b := range p.Blocks {
					if b.Image != nil && len(b.Image.Data) > 0 {
						withBytes++
						fmt.Fprintf(&sb, "picture bytes on sheet %d\n", p.Number)
					}
				}
			}
			got := sb.String() + strings.Join(renderSheets(doc), "\n")
			if (name == "no limit") != (limited == 0) || limited == len(doc.Parts) {
				t.Errorf("%s: %d of %d sheets cut short", name, limited, len(doc.Parts))
			}
			if name == "no limit" && (withBytes != 1 || !strings.Contains(got, "picture bytes on sheet 1\n")) {
				t.Errorf("%d workers: the picture's bytes are on %d blocks, and not on the first sheet's alone", workers, withBytes)
			}
			if workers == 1 {
				want = got
			} else if got != want {
				t.Errorf("%s: with %d workers the result differs from that of one", name, workers)
			}
		}
	}
}
