package gofastooxml_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// documentText returns the bytes of text a document holds, in its blocks
// and the cells of its tables, each counted as often as it occurs, with
// the documents embedded in it.
func documentText(doc *gofastcore.Document) (n int) {
	doc.Walk(func(d *gofastcore.Document, _ int) bool {
		for _, b := range d.AllBlocks() {
			n += len(b.Text)
			if b.Table != nil {
				for _, row := range b.Table.Rows {
					for _, cell := range row {
						n += len(cell)
					}
				}
			}
		}
		return true
	})
	return n
}

// cutForText reports whether a part of the document says it was cut off
// at the limit on text.
func cutForText(doc *gofastcore.Document) bool {
	for _, p := range doc.Parts {
		for _, w := range p.Warnings {
			if w.Code == gofastcore.WarnLimitExceeded && strings.Contains(w.Message, "amount of text") {
				return true
			}
		}
	}
	return false
}

// A document holds no more text than Limits.MaxTextBytes, whatever the
// format and wherever the text is, and says where it was cut off. Below
// the limit nothing changes. The result is the same for any number of
// workers.
func TestTextLimit(t *testing.T) {
	sentence := strings.Repeat("The quarterly figures were revised. ", 5) // 180 bytes
	var body, cells strings.Builder
	for i := range 400 {
		body.WriteString(ooxmltest.Para(fmt.Sprintf("%d. %s", i, sentence)))
	}
	body.WriteString(`<w:tbl>`)
	for i := range 200 {
		body.WriteString(`<w:tr><w:tc>` + ooxmltest.Para(fmt.Sprintf("%d", i)) + `</w:tc><w:tc>` + ooxmltest.Para(sentence) + `</w:tc></w:tr>`)
	}
	body.WriteString(`</w:tbl>`)
	for r := 1; r <= 400; r++ {
		cells.WriteString(xlRow(r, xlStr(fmt.Sprintf("A%d", r), sentence), xlNum(fmt.Sprintf("B%d", r), "42")))
	}
	slides := make([]ooxmltest.Slide, 60)
	for i := range slides {
		slides[i].Shapes = ooxmltest.Shape(2, ph("title", -1), "", ooxmltest.TextBody(dpara("", plain(fmt.Sprintf("Slide %d", i+1))))) +
			box(3, 10, 100, 600, 300, "", dpara("", plain(sentence)), dpara("", plain(sentence)), dpara("", plain(sentence))) +
			tableFrame(4, 10, 420, 600, 60, "", trow(tc(sentence), tc(sentence)))
		slides[i].Notes = noteShapes(dpara("", plain(sentence)))
	}
	for name, data := range map[string][]byte{
		"Word":       ooxmltest.Docx(body.String()).Bytes(),
		"Excel":      ooxmltest.Xlsx(ooxmltest.Sheet{Name: "A", Data: cells.String()}, ooxmltest.Sheet{Name: "B", Data: cells.String()}, ooxmltest.Sheet{Name: "C", Data: cells.String()}).Bytes(),
		"PowerPoint": deck(slides...).Bytes(),
	} {
		whole, err := extract(t, data, &gofastcore.Options{Workers: 1})
		if err != nil {
			t.Fatal(err)
		}
		all := documentText(whole)
		if cutForText(whole) || all < 50_000 {
			t.Fatalf("%s: %d bytes of text with the default limit, cut %v", name, all, cutForText(whole))
		}
		for _, limit := range []int{1, 500, 10_000, all / 2, all - 1, all, -1} {
			var first string
			for _, workers := range []int{1, 2, 8} {
				doc, err := extract(t, data, &gofastcore.Options{Workers: workers, Limits: gofastcore.Limits{MaxTextBytes: int64(limit)}})
				if err != nil {
					t.Fatalf("%s, limit %d: %v", name, limit, err)
				}
				ooxmltest.CheckDocument(t, doc)
				got := documentText(doc)
				switch {
				case limit < 0 || limit >= all:
					if got != all || cutForText(doc) {
						t.Errorf("%s, limit %d: %d bytes of %d, cut %v", name, limit, got, all, cutForText(doc))
					}
				case got > limit || !cutForText(doc):
					t.Errorf("%s, limit %d: %d bytes of text, cut %v", name, limit, got, cutForText(doc))
				case limit >= 10_000 && got < limit/2:
					t.Errorf("%s, limit %d: only %d bytes of text kept", name, limit, got)
				}
				if json := asJSON(t, doc); first == "" {
					first = json
				} else if json != first {
					t.Errorf("%s, limit %d: the document differs with %d workers", name, limit, workers)
				}
			}
		}
	}
}

// One paragraph can be the whole of a document. It is not read to its
// end before it is counted.
func TestTextLimitWithinOneParagraph(t *testing.T) {
	var runs strings.Builder
	for range 4000 {
		runs.WriteString(run(text(strings.Repeat("word ", 100))))
	}
	data := ooxmltest.Docx(para(runs.String()) + ooxmltest.Para("after")).Bytes()
	doc, err := extract(t, data, &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 100_000}})
	if err != nil {
		t.Fatal(err)
	}
	ooxmltest.CheckDocument(t, doc)
	if got := documentText(doc); got > 100_000 || !cutForText(doc) {
		t.Errorf("%d bytes of text, cut %v", got, cutForText(doc))
	}
}

// What a layout and a master put on every slide is stored once and
// written with every slide. It counts each time.
func TestTextLimitCountsWhatEverySlideRepeats(t *testing.T) {
	long := strings.Repeat("Confidential: not for distribution outside the company. ", 200) // 11 KB
	slides := make([]ooxmltest.Slide, 300)
	for i := range slides {
		slides[i].Shapes = ooxmltest.Shape(2, ph("title", -1), "", ooxmltest.TextBody(dpara("", plain(fmt.Sprintf("Slide %d", i+1)))))
	}
	pkg := deck(slides...)
	pkg.Master("", testMaster()+box(40, 10, 500, 700, 30, "", dpara("", plain(long))), "", testTxStyles)
	data := pkg.Bytes()
	whole, err := extract(t, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if all := documentText(whole); all < 3_000_000 {
		t.Fatalf("the file of %d bytes holds %d bytes of text", len(data), all)
	}
	var first string
	for _, workers := range []int{1, 8} {
		doc, err := extract(t, data, &gofastcore.Options{Workers: workers, Limits: gofastcore.Limits{MaxTextBytes: 1 << 20}})
		if err != nil {
			t.Fatal(err)
		}
		ooxmltest.CheckDocument(t, doc)
		if got := documentText(doc); got > 1<<20 || got < 1<<19 || !cutForText(doc) {
			t.Errorf("%d workers: %d bytes of text, cut %v", workers, got, cutForText(doc))
		}
		if json := asJSON(t, doc); first == "" {
			first = json
		} else if json != first {
			t.Errorf("the document differs with %d workers", workers)
		}
	}
}

// The limit is on a document and the documents embedded in it together:
// each embedded file has what the document has left, and what it uses is
// gone for the next. Twenty workbooks that each expand to 200 KB cannot
// together take more than the document may.
func TestTextLimitIsSharedWithEmbeddedFiles(t *testing.T) {
	var rows strings.Builder
	for r := 1; r <= 200; r++ {
		rows.WriteString(xlRow(r, fmt.Sprintf(`<c r="A%d" t="s"><v>0</v></c>`, r)))
	}
	inner := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "A", Data: rows.String()}).WithSharedStrings(strings.Repeat("x", 1000)).Bytes()
	pkg := ooxmltest.Docx(ooxmltest.Para(strings.Repeat("The report. ", 100)))
	const files = 20
	for i := range files {
		name := fmt.Sprintf("embeddings/book%d.xlsx", i)
		pkg.Rel("word/document.xml", ooxmltest.RelPackage, name)
		// The same workbook under another name: each is a file of its own.
		pkg.AddBytes("word/"+name, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", append(bytes.Clone(inner), make([]byte, i)...)[:len(inner)])
	}
	data := pkg.Bytes()
	child := func(ctx context.Context, name, mime string, r io.ReaderAt, size int64, o *gofastcore.Options) (*gofastcore.Document, error) {
		return gofastooxml.Extract(ctx, r, size, o)
	}
	whole, err := extract(t, data, &gofastcore.Options{Child: child})
	if err != nil {
		t.Fatal(err)
	}
	if all := documentText(whole); all != 1199+files*200_000 { // the paragraph, trimmed, and twenty workbooks
		t.Fatalf("with the default limit: %d bytes of text", all)
	}
	const limit = 1_000_000
	for _, workers := range []int{1, 8} {
		doc, err := extract(t, data, &gofastcore.Options{Child: child, Workers: workers, Limits: gofastcore.Limits{MaxTextBytes: limit}})
		if err != nil {
			t.Fatal(err)
		}
		ooxmltest.CheckDocument(t, doc)
		if got := documentText(doc); got > limit || got < limit*9/10 {
			t.Errorf("%d workers: %d bytes of text in the document and its embedded files, limit %d", workers, got, limit)
		}
		// The first four are whole, the fifth is cut off where the text
		// ran out, and the rest are listed or empty, each saying why.
		for i, c := range doc.Children {
			got := documentText(c)
			switch {
			case i < 4 && (got != 200_000 || cutForText(c)):
				t.Errorf("embedded file %d: %d bytes, cut %v", i+1, got, cutForText(c))
			case i == 4 && (got == 0 || got >= 200_000 || !cutForText(c)):
				t.Errorf("embedded file 5: %d bytes, cut %v", got, cutForText(c))
			case i > 4 && got != 0:
				t.Errorf("embedded file %d: %d bytes after the text ran out", i+1, got)
			}
		}
		if len(doc.Children) != files {
			t.Errorf("%d embedded files listed", len(doc.Children))
		}
	}
	// A file handle counts afresh for each reading.
	f, err := gofastooxml.OpenBytes(data, &gofastcore.Options{Child: child, Limits: gofastcore.Limits{MaxTextBytes: limit}})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for reading := 1; reading <= 2; reading++ {
		doc, err := f.Document(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if got := documentText(doc); got > limit || got < limit*9/10 {
			t.Errorf("reading %d: %d bytes of text", reading, got)
		}
	}
}
