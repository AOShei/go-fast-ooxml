package gofastooxml_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// FuzzOpen feeds arbitrary bytes to Extract. Whatever the input, the call
// must return a valid document or an error that wraps one of the shared
// errors; it must not panic, hang or exhaust memory.
//
// Seeds are the builder's presets and every committed fixture.
func FuzzOpen(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("PK\x03\x04"))
	f.Add(ooxmltest.Docx(ooxmltest.Para("seed")).WithCore(`<dc:title>t</dc:title>`).Bytes())
	f.Add(ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: `<row r="1">` + ooxmltest.InlineCell("A1", "seed") + `</row>`}).Bytes())
	f.Add(ooxmltest.Pptx(ooxmltest.Slide{Shapes: ooxmltest.TextShape(2, 0, 0, 100, 20, "seed")}).Bytes())
	for _, file := range ooxmltest.LoadManifest(f, "fixtures.json") {
		f.Add(file.Read(f, false))
	}
	// A small budget keeps a fuzzed decompression bomb cheap.
	opts := &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 4 << 20}}
	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := gofastooxml.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts)
		checkOutcome(t, "fuzz input", doc, err)
	})
}

// FuzzDocx feeds arbitrary XML to the Word reader as the three parts that
// decide a document's structure: the body, the styles and the numbering.
// The package around them is sound, so the fuzzer spends its effort on the
// reader and not on the zip format.
func FuzzDocx(f *testing.F) {
	wrap := func(body string) []byte {
		return []byte(`<w:document` + ooxmltest.WordNS + `><w:body>` + body + `</w:body></w:document>`)
	}
	styles := []byte(`<w:styles` + ooxmltest.WordNS + `>` + wordStyles + `</w:styles>`)
	numbering := []byte(`<w:numbering` + ooxmltest.WordNS + `>` + wordNumbering + `</w:numbering>`)
	for _, body := range []string{
		ooxmltest.Para("plain") + ooxmltest.StyledPara("Heading1", "heading") + ooxmltest.ListPara(1, 1, "item"),
		`<w:tbl><w:tr><w:trPr><w:tblHeader/></w:trPr><w:tc><w:tcPr><w:gridSpan w:val="2"/><w:vMerge w:val="restart"/></w:tcPr>` + ooxmltest.Para("cell") + `</w:tc></w:tr></w:tbl>`,
		`<w:p><w:r><w:t>a</w:t><w:tab/><w:br/><w:sym w:font="Symbol" w:char="F061"/><w:footnoteReference w:id="2"/><w:commentReference w:id="0"/></w:r>` +
			`<w:del w:author="x"><w:r><w:delText>gone</w:delText></w:r></w:del><w:ins><w:r><w:t>new</w:t></w:r></w:ins></w:p>`,
		`<w:p><w:r><mc:AlternateContent><mc:Choice Requires="wps"><w:drawing><wp:inline><wp:docPr descr="alt"/><a:graphic><a:graphicData><wps:wsp><wps:txbx><w:txbxContent>` +
			ooxmltest.Para("box") + `</w:txbxContent></wps:txbx></wps:wsp></a:graphicData></a:graphic></wp:inline></w:drawing></mc:Choice><mc:Fallback><w:pict/></mc:Fallback></mc:AlternateContent></w:r></w:p>`,
		`<w:p><w:pPr><w:sectPr><w:headerReference w:type="default" r:id="rId1"/></w:sectPr></w:pPr></w:p><w:sdt><w:sdtContent>` + ooxmltest.Para("x") + `</w:sdtContent></w:sdt>`,
	} {
		f.Add(wrap(body), styles, numbering)
	}
	opts := &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 4 << 20}, Images: gofastcore.ImagesMeta, Spans: true}
	f.Fuzz(func(t *testing.T, body, styles, numbering []byte) {
		pkg := ooxmltest.Docx("")
		pkg.AddBytes("word/document.xml", ooxmltest.CTDocument, body)
		pkg.Rel("word/document.xml", ooxmltest.RelStyles, "styles.xml")
		pkg.AddBytes("word/styles.xml", ooxmltest.CTStyles, styles)
		pkg.Rel("word/document.xml", ooxmltest.RelNumbering, "numbering.xml")
		pkg.AddBytes("word/numbering.xml", ooxmltest.CTNumbering, numbering)
		pkg.WordPart("footnotes.xml", ooxmltest.CTFootnotes, ooxmltest.RelFootnotes, "footnotes", `<w:footnote w:id="2">`+ooxmltest.Para("note")+`</w:footnote>`)
		pkg.WordPart("comments.xml", ooxmltest.CTComments, ooxmltest.RelComments, "comments", `<w:comment w:id="0" w:author="a">`+ooxmltest.Para("comment")+`</w:comment>`)
		data := pkg.Bytes()
		doc, err := gofastooxml.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts)
		checkOutcome(t, "fuzz input", doc, err)
	})
}
