package gofastooxml_test

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
)

// The whole of a file as one document.
func ExampleExtractFile() {
	doc, err := gofastooxml.ExtractFile(context.Background(), "testdata/fixtures/basic-pptx.pptx", nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(doc.Source.Format, "with", len(doc.Parts), "slides")
	fmt.Println(doc.Parts[1].Markdown())
	// Output:
	// pptx with 5 slides
	// # Agenda
	//
	// - Status
	//     - Milestones reached
	// - Risks
	//
	// Remember to mention the budget.
}

// A file that is already in memory, or anything else that can be read at
// an offset.
func ExampleExtract() {
	data, err := os.ReadFile("testdata/fixtures/tables.docx")
	if err != nil {
		log.Fatal(err)
	}
	doc, err := gofastooxml.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), nil)
	if err != nil {
		log.Fatal(err)
	}
	for _, b := range doc.Parts[0].Blocks {
		if b.Table != nil {
			fmt.Printf("a table of %d rows and %d columns, %d merged cells\n", len(b.Table.Rows), len(b.Table.Rows[0]), len(b.Table.Merges))
		}
	}
	// Output:
	// a table of 4 rows and 3 columns, 2 merged cells
}

// Text that a reader of the document cannot see is extracted and flagged.
// It is in the rendered text unless it is asked to be left out.
func ExampleExtract_hiddenText() {
	doc, err := gofastooxml.ExtractFile(context.Background(), "testdata/fixtures/hidden.docx", nil)
	if err != nil {
		log.Fatal(err)
	}
	for _, b := range doc.Parts[0].Blocks {
		if b.Hidden != 0 {
			fmt.Println("hidden:", b.Hidden)
		}
	}
	fmt.Println(strings.Contains(doc.Text(), "Hidden by formatting."))

	var visible strings.Builder
	doc.WriteText(&visible, &gofastcore.RenderOptions{ExcludeHidden: gofastcore.HiddenAny})
	fmt.Println(visible.String())
	// Output:
	// hidden: invisible
	// hidden: low_contrast
	// hidden: tiny
	// hidden: invisible_chars
	// true
	// Visible sentence one. Visible sentence two.
	//
	// Looks clean.
	//
	// Light grey text is still visible.
	//
	// Six point text is small but legible.
	//
	// An ordinary closing paragraph.
}

// A file opened to be read a part at a time. Each part is complete when
// it is yielded, and nothing holds on to it afterwards.
func ExampleOpen() {
	f, err := gofastooxml.Open("testdata/fixtures/basic-pptx.pptx", nil)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	fmt.Println(f.Format(), f.NumParts())
	for part, err := range f.Parts(context.Background()) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s %d %q: %d blocks", part.Kind, part.Number, part.Name, len(part.Blocks))
		if part.Hidden != 0 {
			fmt.Printf(", %s", part.Hidden)
		}
		fmt.Println()
	}
	// Output:
	// pptx 5
	// slide 1 "Project Update": 2 blocks
	// slide 2 "Agenda": 5 blocks
	// slide 3 "Figures": 3 blocks
	// slide 4 "": 1 blocks, invisible
	// slide 5 "": 2 blocks
}

// One part by its number.
func ExampleFile_Part() {
	f, err := gofastooxml.Open("testdata/fixtures/layout.xlsx", nil)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	for _, sheet := range f.Sheets() {
		fmt.Println(sheet.Number, sheet.Name, sheet.Hidden)
	}
	part, err := f.Part(context.Background(), 1)
	if err != nil {
		log.Fatal(err)
	}
	for _, b := range part.Blocks {
		fmt.Println(b.Kind, b.Loc.Range, b.Origin)
	}
	// Output:
	// 1 Data false
	// 2 Secret true
	// 3 Empty false
	// table A1:C4 native
	// table A7:B8 inferred
}

// A sheet too long to hold, a band of rows at a time.
func ExampleFile_Rows() {
	f, err := gofastooxml.Open("testdata/fixtures/values.xlsx", nil)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	rows := 0
	for band, err := range f.Rows(context.Background(), 1, 8) {
		if err != nil {
			log.Fatal(err)
		}
		for _, b := range band.Blocks {
			rows += len(b.Table.Rows)
			fmt.Println(b.Loc.Range, b.Table.Rows[0])
		}
	}
	fmt.Println(rows, "rows")
	// Output:
	// A1:C8 [Kind Value Format]
	// A9:C16 [percent 12.5% 0.0%]
	// A17:C18 [boolean TRUE General]
	// 18 rows
}

// Settings that only this library has are fields of an Extractor.
func ExampleExtractor() {
	e := &gofastooxml.Extractor{MaxCells: 4}
	doc, err := e.ExtractFile(context.Background(), "testdata/fixtures/values.xlsx", nil)
	if err != nil {
		log.Fatal(err)
	}
	part := doc.Parts[0]
	fmt.Println("rows kept:", len(part.Blocks[0].Table.Rows))
	for _, w := range part.Warnings {
		fmt.Println(w.Code+":", w.Message)
	}
	// Output:
	// rows kept: 1
	// limit.exceeded: the limit on the number of cells was reached; the sheet is cut off there
}
