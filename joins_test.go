package gofastooxml_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// Text that the readers join from many pieces (paragraphs of code into
// one block, the cells a merged cell covers into it) costs in proportion
// to its length: a file of many small pieces cannot make the reading
// quadratic. Joining by copying the text so far for each piece made 60,000
// code paragraphs of a 166 KB file take six seconds and allocate tens of
// gigabytes; the bound below is far under that and far over what a linear
// join takes.
func TestLongJoinsAreLinear(t *testing.T) {
	const n = 30_000
	line := func(i int) string { return "line " + strconv.Itoa(i) + " of the listing, some forty bytes" }
	var want strings.Builder
	for i := range n {
		if i > 0 {
			want.WriteByte('\n')
		}
		want.WriteString(line(i))
	}
	bound := uint64(200 << 20)

	t.Run("code paragraphs", func(t *testing.T) {
		var body strings.Builder
		for i := range n {
			body.WriteString(`<w:p><w:pPr><w:pStyle w:val="Code"/></w:pPr>` + run(text(line(i))) + `</w:p>`)
		}
		data := ooxmltest.Docx(body.String()).WithStyles(wordStyles).Bytes()
		var doc *gofastcore.Document
		var err error
		_, got := allocated(func() { doc, err = extract(t, data, nil) })
		if err != nil {
			t.Fatal(err)
		}
		blocks := doc.Parts[0].Blocks
		if len(blocks) != 1 || blocks[0].Kind != gofastcore.KindCode || blocks[0].Text != want.String() {
			t.Fatalf("%d blocks, the first %v of %d bytes", len(blocks), blocks[0].Kind, len(blocks[0].Text))
		}
		if got > bound {
			t.Errorf("%d code paragraphs allocated %d MB", n, got>>20)
		}
	})

	t.Run("word cell merged down", func(t *testing.T) {
		var body strings.Builder
		body.WriteString(`<w:tbl>`)
		for i := range n {
			merge := `<w:vMerge/>`
			if i == 0 {
				merge = `<w:vMerge w:val="restart"/>`
			}
			body.WriteString(`<w:tr><w:tc><w:tcPr>` + merge + `</w:tcPr>` + para(run(text(line(i)))) + `</w:tc>` +
				`<w:tc>` + para(run(text("b"))) + `</w:tc></w:tr>`)
		}
		body.WriteString(`</w:tbl>`)
		data := ooxmltest.Docx(body.String()).Bytes()
		var doc *gofastcore.Document
		var err error
		_, got := allocated(func() { doc, err = extract(t, data, nil) })
		if err != nil {
			t.Fatal(err)
		}
		blocks := doc.Parts[0].Blocks
		if len(blocks) != 1 || blocks[0].Table == nil || blocks[0].Table.Rows[0][0] != want.String() {
			t.Fatalf("%d blocks", len(blocks))
		}
		if got > bound {
			t.Errorf("a cell merged over %d rows allocated %d MB", n, got>>20)
		}
	})

	t.Run("slide cell merged down", func(t *testing.T) {
		rows := make([]string, n)
		for i := range n {
			cell := tcell(` vMerge="1"`, "", dpara("", plain(line(i))))
			if i == 0 {
				cell = tcell(` rowSpan="`+strconv.Itoa(n)+`"`, "", dpara("", plain(line(i))))
			}
			rows[i] = trow(cell, tc("b"))
		}
		data := deck(ooxmltest.Slide{Shapes: tableFrame(2, 50, 100, 600, 200, `<a:tblPr/>`, rows...)}).Bytes()
		var doc *gofastcore.Document
		var err error
		_, got := allocated(func() { doc, err = extract(t, data, nil) })
		if err != nil {
			t.Fatal(err)
		}
		var table *gofastcore.Table
		for _, b := range doc.Parts[0].Blocks {
			if b.Table != nil {
				table = b.Table
			}
		}
		if table == nil || table.Rows[0][0] != want.String() {
			t.Fatalf("the merged cell is not the text of the cells it covers")
		}
		if got > bound {
			t.Errorf("a slide cell merged over %d rows allocated %d MB", n, got>>20)
		}
	})
}
