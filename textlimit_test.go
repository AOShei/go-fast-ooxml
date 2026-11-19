package gofastooxml_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"unsafe"

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

// documentCost returns what a document takes from the text budget by
// core's rule: its text and the text of its tables' cells, link targets,
// gofastcore.BlockCost for every block and gofastcore.CellCost for every
// cell, with the documents embedded in it.
func documentCost(doc *gofastcore.Document) (n int) {
	doc.Walk(func(d *gofastcore.Document, _ int) bool {
		for _, b := range d.AllBlocks() {
			n += gofastcore.BlockCost + len(b.Text) + len(b.Attrs["href"])
			for _, sp := range b.Spans {
				n += len(sp.Href)
			}
			if b.Table != nil {
				for _, row := range b.Table.Rows {
					for _, cell := range row {
						n += gofastcore.CellCost + len(cell)
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
		all := documentCost(whole)
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
				got := documentCost(doc)
				switch {
				case limit < 0 || limit >= all:
					if got != all || cutForText(doc) {
						t.Errorf("%s, limit %d: %d bytes of %d, cut %v", name, limit, got, all, cutForText(doc))
					}
				case got > limit || !cutForText(doc):
					t.Errorf("%s, limit %d: %d bytes taken, cut %v", name, limit, got, cutForText(doc))
				case limit >= 10_000 && got < limit/2:
					t.Errorf("%s, limit %d: only %d bytes taken", name, limit, got)
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
	if got := documentCost(doc); got > 100_000 || !cutForText(doc) {
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
		if got := documentCost(doc); got > 1<<20 || got < 1<<19 || !cutForText(doc) {
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
		if got := documentCost(doc); got > limit || got < limit*9/10 {
			t.Errorf("%d workers: %d bytes taken by the document and its embedded files, limit %d", workers, got, limit)
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
		if got := documentCost(doc); got > limit || got < limit*9/10 {
			t.Errorf("reading %d: %d bytes taken", reading, got)
		}
	}
}

// A document cut off by a limit may have files embedded in it, and a
// caller that extracts those. They are listed with the reason, and the
// child extractor is not called with nothing to read. (It was, and this
// library's own Extract, called so, did not survive it.)
func TestEmbeddedFilesOfADocumentCutOffByALimit(t *testing.T) {
	inner := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "Inner", Data: xlRow(1, xlStr("A1", "inside"))}).Bytes()
	long := strings.Repeat("text ", 100)
	slide := ooxmltest.Slide{Shapes: box(2, 10, 10, 600, 300, "", dpara("", plain(long)), dpara("", plain("more")))}
	for name, c := range map[string]struct {
		pkg  *ooxmltest.Package
		from string
	}{
		"Word":       {ooxmltest.Docx(ooxmltest.Para(long) + ooxmltest.Para("more")), "word/document.xml"},
		"Excel":      {ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: xlRow(1, xlStr("A1", long)) + xlRow(2, xlStr("A2", "more"))}), "xl/worksheets/sheet1.xml"},
		"PowerPoint": {deck(slide), "ppt/slides/slide1.xml"},
	} {
		dir, _, _ := strings.Cut(c.from, "/")
		c.pkg.Rel(c.from, ooxmltest.RelPackage, "/"+dir+"/embeddings/book.xlsx")
		c.pkg.AddBytes(dir+"/embeddings/book.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", inner)
		data := c.pkg.Bytes()
		called := 0
		child := func(ctx context.Context, name, mime string, r io.ReaderAt, size int64, o *gofastcore.Options) (*gofastcore.Document, error) {
			called++
			if r == nil {
				t.Errorf("%s: the child extractor was called with nothing to read", name)
				return nil, fmt.Errorf("nothing to read")
			}
			return gofastooxml.Extract(ctx, r, size, o)
		}
		whole, err := extract(t, data, &gofastcore.Options{Child: child})
		if err != nil || len(whole.Children) != 1 || !strings.Contains(whole.Children[0].Text(), "inside") || called != 1 {
			t.Fatalf("%s, within the limits: %v, %d children, %d calls", name, err, len(whole.Children), called)
		}
		for _, limits := range []gofastcore.Limits{{MaxTextBytes: 50}, {MaxDecodedBytes: 2500}} {
			called = 0
			doc, err := extract(t, data, &gofastcore.Options{Child: child, Limits: limits})
			if err != nil {
				// The limit may stop the file before there is a document.
				if !errors.Is(err, gofastcore.ErrLimitExceeded) {
					t.Errorf("%s, %+v: %v", name, limits, err)
				}
				continue
			}
			ooxmltest.CheckDocument(t, doc)
			for _, c := range doc.Children {
				listed := len(c.Parts) == 0 && len(c.Warnings) == 1 && c.Warnings[0].Code == gofastcore.WarnChildNotExtracted && strings.Contains(c.Warnings[0].Message, "limit")
				if !listed && called == 0 {
					t.Errorf("%s, %+v: the embedded file has %d parts and warnings %v", name, limits, len(c.Parts), c.Warnings)
				}
			}
			// (A decompression limit this small may stop the part before
			// it is known what it embeds.)
			if n := len(doc.Children); n > 1 || n == 0 && limits.MaxTextBytes != 0 {
				t.Errorf("%s, %+v: %d embedded files listed", name, limits, n)
			}
		}
	}
	// And asked directly.
	if doc, err := gofastooxml.Extract(context.Background(), nil, 100, nil); doc != nil || !errors.Is(err, gofastcore.ErrMalformed) {
		t.Errorf("nothing to read: %v", err)
	}
}

// The limit on decompressed bytes is a document's and its embedded
// files' together. Each of twenty workbooks here decompresses to half a
// megabyte and is well inside a limit of three; together they are not,
// and the ones the limit has no room for are not read.
func TestDecompressionLimitIsSharedWithEmbeddedFiles(t *testing.T) {
	var rows strings.Builder
	for r := 1; r <= 2500; r++ {
		rows.WriteString(xlRow(r, xlStr(fmt.Sprintf("A%d", r), strings.Repeat("a", 150))))
	}
	inner := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "A", Data: rows.String()}).Bytes()
	pkg := ooxmltest.Docx(ooxmltest.Para("Twenty attachments."))
	const files = 20
	for i := range files {
		name := fmt.Sprintf("embeddings/book%d.xlsx", i)
		pkg.Rel("word/document.xml", ooxmltest.RelPackage, name)
		pkg.AddBytes("word/"+name, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", inner)
	}
	data := pkg.Bytes()
	read := func(doc *gofastcore.Document) (n int) {
		for _, c := range doc.Children {
			switch {
			case strings.Contains(c.Text(), "aaaa") && len(c.Warnings) == 0 && !c.Parts[0].Has(gofastcore.WarnLimitExceeded):
				n++
			case len(c.Warnings) == 0 && !c.Parts[0].Has(gofastcore.WarnLimitExceeded) && !c.Parts[0].Has(gofastcore.WarnPartFailed):
				t.Errorf("an embedded file neither read whole nor saying why not: %v, %v", c.Warnings, c.Parts[0].Warnings)
			}
		}
		return n
	}
	whole, err := extract(t, data, &gofastcore.Options{Child: childExtractor})
	if err != nil || len(whole.Children) != files || read(whole) != files {
		t.Fatalf("with the default limit: %v, %d of %d read", err, read(whole), files)
	}
	var first string
	for _, workers := range []int{1, 8} {
		doc, err := extract(t, data, &gofastcore.Options{Child: childExtractor, Workers: workers, Limits: gofastcore.Limits{MaxDecodedBytes: 3 << 20}})
		if err != nil {
			t.Fatal(err)
		}
		ooxmltest.CheckDocument(t, doc)
		if n := read(doc); len(doc.Children) != files || n < 2 || n > 7 {
			t.Errorf("%d workers: %d of %d embedded files read within 3 MB, each decompressing to about half a megabyte", workers, n, len(doc.Children))
		}
		if json := asJSON(t, doc); first == "" {
			first = json
		} else if json != first {
			t.Errorf("the document differs with %d workers", workers)
		}
	}
}

// documentLinks returns the bytes of the link targets a document holds,
// each counted as often as it is written.
func documentLinks(doc *gofastcore.Document) (n int) {
	for _, b := range doc.AllBlocks() {
		n += len(b.Attrs["href"])
		for _, sp := range b.Spans {
			n += len(sp.Href)
		}
	}
	return n
}

// The address of a link is stored once, with the relationships of a
// part, and written with every link to it: a file of a few kilobytes
// with thousands of links to one long address would otherwise write
// megabytes the limit on text never saw. Link targets count as text
// does: on the links of a text, also where every slide repeats a link of
// its master, and on pictures that are linked and not in the file. The
// result is the same for any number of workers.
func TestTextLimitCountsLinkTargets(t *testing.T) {
	address := "https://example.com/" + strings.Repeat("a", 2000)
	const links = 2000
	wordBody := func(pkg *ooxmltest.Package, body string) *ooxmltest.Package {
		return pkg.Add("word/document.xml", ooxmltest.CTDocument, `<w:document`+ooxmltest.WordNS+`><w:body>`+body+`</w:body></w:document>`)
	}
	relID := func(pkg *ooxmltest.Package, source, typ, id string) {
		for pkg.ExternalRel(source, typ, address) != id {
		}
	}

	// Word: a hundred paragraphs of twenty links.
	docx := ooxmltest.Docx("")
	link := docx.ExternalRel("word/document.xml", ooxmltest.RelHyperlink, address)
	var body strings.Builder
	for range links / 20 {
		var p strings.Builder
		for range 20 {
			p.WriteString(`<w:hyperlink r:id="` + link + `">` + run(text("here")) + `</w:hyperlink>` + run(text(" or ")))
		}
		body.WriteString(para(p.String()))
	}
	wordBody(docx, body.String())

	// Word: pictures that are somewhere else.
	linkedPictures := ooxmltest.Docx("")
	at := linkedPictures.ExternalRel("word/document.xml", ooxmltest.RelImage, address)
	body.Reset()
	for range links {
		body.WriteString(para(run(`<w:pict><v:shape alt="A picture"><v:imagedata r:id="` + at + `"/></v:shape></w:pict>`)))
	}
	wordBody(linkedPictures, body.String())

	// PowerPoint: a hundred slides of twenty links.
	linked := func(n int) string {
		runs := make([]string, 0, 2*n)
		for range n {
			runs = append(runs, drun("", `<a:hlinkClick r:id="rId7"/>`, "here"), plain(" or "))
		}
		return dpara("", runs...)
	}
	slides := make([]ooxmltest.Slide, links/20)
	for i := range slides {
		slides[i].Shapes = box(2, 10, 10, 600, 300, "", linked(20))
	}
	pptx := deck(slides...)
	for i := range slides {
		relID(pptx, fmt.Sprintf("ppt/slides/slide%d.xml", i+1), ooxmltest.RelHyperlink, "rId7")
	}

	// PowerPoint: twenty links on the master, shown by every slide.
	for i := range slides {
		slides[i].Shapes = ooxmltest.Shape(2, ph("title", -1), "", ooxmltest.TextBody(dpara("", plain(fmt.Sprintf("Slide %d", i+1)))))
	}
	repeated := deck(slides...)
	repeated.Master("", testMaster()+box(40, 10, 500, 700, 30, "", linked(20)), "", testTxStyles)
	relID(repeated, "ppt/slideMasters/slideMaster1.xml", ooxmltest.RelHyperlink, "rId7")

	// PowerPoint: pictures that are somewhere else.
	for i := range slides {
		var shapes strings.Builder
		for n := range 20 {
			shapes.WriteString(picture(10+n, "rId7", "A picture", 10, float64(10+20*n), 100, 15, ""))
		}
		slides[i].Shapes = shapes.String()
	}
	slidePictures := deck(slides...)
	for i := range slides {
		relID(slidePictures, fmt.Sprintf("ppt/slides/slide%d.xml", i+1), ooxmltest.RelImage, "rId7")
	}

	// Excel: pictures that are somewhere else.
	var drawing strings.Builder
	drawing.WriteString(drawingHeader)
	for n := range links {
		fmt.Fprintf(&drawing, `<xdr:twoCellAnchor><xdr:from><xdr:col>0</xdr:col><xdr:colOff>0</xdr:colOff><xdr:row>%d</xdr:row><xdr:rowOff>0</xdr:rowOff></xdr:from>`+
			`<xdr:to><xdr:col>1</xdr:col><xdr:colOff>0</xdr:colOff><xdr:row>%d</xdr:row><xdr:rowOff>0</xdr:rowOff></xdr:to>`+
			`<xdr:pic><xdr:nvPicPr><xdr:cNvPr id="%d" name="Picture" descr="A picture"/><xdr:cNvPicPr/></xdr:nvPicPr><xdr:blipFill><a:blip r:link="rId1"/></xdr:blipFill><xdr:spPr/></xdr:pic>`+
			`<xdr:clientData/></xdr:twoCellAnchor>`, n, n+1, n+2)
	}
	drawing.WriteString(`</xdr:wsDr>`)
	xlsx := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: xlRow(1, xlStr("A1", "cell")), Extra: `<drawing r:id="rId1"/>`})
	xlsx.Rel(firstSheet, relBase+"drawing", "../drawings/drawing1.xml")
	xlsx.Add("xl/drawings/drawing1.xml", ctDrawing, drawing.String())
	relID(xlsx, "xl/drawings/drawing1.xml", ooxmltest.RelImage, "rId1")

	const limit = 1 << 20
	for _, c := range []struct {
		name  string
		data  []byte
		spans bool // the links are in spans, which are asked for
	}{
		{"Word", docx.Bytes(), true},
		{"Word, linked pictures", linkedPictures.Bytes(), false},
		{"PowerPoint", pptx.Bytes(), true},
		{"PowerPoint, on the master", repeated.Bytes(), true},
		{"PowerPoint, linked pictures", slidePictures.Bytes(), false},
		{"Excel, linked pictures", xlsx.Bytes(), false},
	} {
		whole, err := extract(t, c.data, &gofastcore.Options{Spans: c.spans})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if all := documentLinks(whole); all != links*len(address) {
			t.Fatalf("%s: %d bytes of link targets, of %d", c.name, all, links*len(address))
		}
		if c.spans {
			// Without spans there are no links in the document, and
			// nothing of them is counted.
			if doc, err := extract(t, c.data, &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: limit}}); err != nil || cutForText(doc) || documentLinks(doc) != 0 {
				t.Errorf("%s, without spans: cut %v, %d bytes of link targets, %v", c.name, cutForText(doc), documentLinks(doc), err)
			}
		}
		var first string
		for _, workers := range []int{1, 8} {
			doc, err := extract(t, c.data, &gofastcore.Options{Spans: c.spans, Workers: workers, Limits: gofastcore.Limits{MaxTextBytes: limit}})
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			ooxmltest.CheckDocument(t, doc)
			if got := documentCost(doc); got > limit || got < limit/2 || !cutForText(doc) {
				t.Errorf("%s, %d workers: %d bytes of text and link targets, cut %v", c.name, workers, got, cutForText(doc))
			}
			if json := asJSON(t, doc); first == "" {
				first = json
			} else if json != first {
				t.Errorf("%s: the document differs with %d workers", c.name, workers)
			}
		}
	}
}

// An address is kept whole up to 8 KiB, which is longer than addresses
// are, and every link to it holds the same string: what a thousand links
// cost in memory is a thousand spans, not a thousand addresses.
func TestLongLinkTargets(t *testing.T) {
	long := "https://example.com/redirect?to=" + strings.Repeat("é", 1500) + "&token=" + strings.Repeat("0123456789abcdef", 60) // 3 999 bytes
	endless := "https://example.com/" + strings.Repeat("b", 20_000)
	pkg := ooxmltest.Docx("")
	first := pkg.ExternalRel("word/document.xml", ooxmltest.RelHyperlink, long)
	second := pkg.ExternalRel("word/document.xml", ooxmltest.RelHyperlink, endless)
	hyperlink := func(id, words string) string {
		return `<w:hyperlink r:id="` + id + `">` + run(text(words)) + `</w:hyperlink>`
	}
	pkg.Add("word/document.xml", ooxmltest.CTDocument, `<w:document`+ooxmltest.WordNS+`><w:body>`+
		para(run(text("See "))+hyperlink(first, "this")+run(text(" and "))+hyperlink(first, "this again")+run(text(" and "))+hyperlink(second, "that")+run(text(".")))+
		para(run(text("Or "))+run(`<w:fldChar w:fldCharType="begin"/>`)+run(`<w:instrText xml:space="preserve"> HYPERLINK "`+ooxmltest.EscapeXML(long)+`" </w:instrText>`)+
			run(`<w:fldChar w:fldCharType="separate"/>`)+run(text("a field"))+run(`<w:fldChar w:fldCharType="end"/>`)+run(text(".")))+
		`</w:body></w:document>`)
	doc := word(t, pkg, &gofastcore.Options{Spans: true})
	var targets []string
	for _, b := range doc.AllBlocks() {
		for _, sp := range b.Spans {
			if sp.Href != "" {
				targets = append(targets, sp.Href)
			}
		}
	}
	if len(targets) != 4 {
		t.Fatalf("%d links, of 4", len(targets))
	}
	if targets[0] != long || targets[1] != long || targets[3] != long {
		t.Errorf("a long address is not kept whole: %d, %d and %d bytes of %d", len(targets[0]), len(targets[1]), len(targets[3]), len(long))
	}
	if unsafe.StringData(targets[0]) != unsafe.StringData(targets[1]) {
		t.Error("two links to one address hold two copies of it")
	}
	if got := targets[2]; len(got) != 8<<10 || !strings.HasPrefix(endless, got) {
		t.Errorf("an address of %d bytes is kept as %d bytes", len(endless), len(got))
	}
}
