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
		f.Add(wrap(body), styles, numbering, []byte(ooxmltest.Para("note")))
	}
	// Character formatting, what lies behind text, links: the paths that
	// decide what is flagged as hidden.
	formatted := []byte(`<w:styles` + ooxmltest.WordNS + `>` + hiddenStyles +
		`<w:style w:type="table" w:styleId="Grid"><w:basedOn w:val="Grid"/><w:tblPr><w:tblStyleRowBandSize w:val="2"/></w:tblPr><w:tcPr><w:shd w:val="pct25" w:color="000000" w:fill="FFFFFF"/></w:tcPr>` +
		`<w:tblStylePr w:type="firstRow"><w:tcPr><w:shd w:val="clear" w:fill="1F3864"/></w:tcPr></w:tblStylePr></w:style></w:styles>`)
	for _, body := range []string{
		`<w:p><w:pPr><w:pStyle w:val="Secret"/><w:shd w:val="solid" w:color="auto" w:fill="000000"/></w:pPr><w:r><w:rPr><w:rStyle w:val="GhostChild"/><w:vanish w:val="0"/><w:b/><w:i/><w:u w:val="single"/><w:strike/>` +
			`<w:color w:val="FFFFFF" w:themeColor="background1"/><w:w w:val="1"/><w:fitText w:val="20"/><w:sz w:val="2"/><w:highlight w:val="yellow"/><w:shd w:val="clear" w:fill="auto"/><w:vertAlign w:val="superscript"/>` +
			`<w14:textFill><w14:solidFill><w14:schemeClr w14:val="bg1"><w14:alpha w14:val="100000"/></w14:schemeClr></w14:solidFill></w14:textFill></w:rPr><w:t xml:space="preserve"> a </w:t></w:r>` +
			`<w:hyperlink r:id="rId1" w:anchor="x"><w:r><w:t>b</w:t></w:r></w:hyperlink><w:fldSimple w:instr=' HYPERLINK \l "y" '><w:r><w:t>c</w:t></w:r></w:fldSimple>` +
			`<w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:instrText> HYPERLINK "z" </w:instrText></w:r><w:r><w:fldChar w:fldCharType="separate"/></w:r><w:r><w:rPr><w:vanish/></w:rPr><w:t>d</w:t><w:footnoteReference w:id="2"/></w:r><w:r><w:fldChar w:fldCharType="end"/></w:r></w:p>`,
		`<w:tbl><w:tblPr><w:tblStyle w:val="Grid"/><w:tblLook w:val="04A0" w:firstRow="1" w:noHBand="0"/><w:shd w:val="clear" w:fill="EEEEEE"/></w:tblPr>` +
			`<w:tr><w:trPr><w:cnfStyle w:val="100000000000"/><w:trHeight w:val="20" w:hRule="exact"/><w:gridBefore w:val="1"/></w:trPr><w:tc><w:tcPr><w:cnfStyle w:firstRow="1"/><w:shd w:val="diagStripe" w:fill="FFFFFF"/><w:vMerge w:val="restart"/></w:tcPr>` +
			`<w:p><w:r><w:rPr><w:color w:val="FFFFFF"/></w:rPr><w:t>a</w:t></w:r></w:p></w:tc></w:tr><w:tr><w:tc><w:tcPr><w:vMerge/></w:tcPr><w:p><w:r><w:rPr><w:vanish/></w:rPr><w:t>b</w:t></w:r><w:r><w:t> c</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`,
		`<w:p><w:r><w:rPr><w:vanish/></w:rPr><w:drawing><wp:anchor><wp:extent cx="12700" cy="12700"/><a:graphic><a:graphicData><wps:wsp><wps:spPr><a:solidFill><a:schemeClr val="accent1"><a:lumMod val="50000"/></a:schemeClr></a:solidFill><a:ln><a:noFill/></a:ln></wps:spPr>` +
			`<wps:style><a:fillRef idx="1"><a:srgbClr val="000000"/></a:fillRef></wps:style><wps:txbx><w:txbxContent><w:tbl><w:tr><w:tc><w:p><w:r><w:rPr><w:color w:val="FFFFFF"/></w:rPr><w:t>in</w:t></w:r><w:r><w:t> box</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:txbxContent></wps:txbx>` +
			`<wps:bodyPr><a:spAutoFit/></wps:bodyPr></wps:wsp></a:graphicData></a:graphic></wp:anchor></w:drawing></w:r>` +
			`<w:r><w:pict><v:rect style="width:1pt;height:1pt" fillcolor="#abc [1]" filled="t"><v:fill type="gradient" opacity=".5" color="red"/><v:textbox><w:txbxContent><w:p><w:r><w:t>v</w:t></w:r></w:p></w:txbxContent></v:textbox></v:rect></w:pict></w:r></w:p>`,
	} {
		f.Add(wrap(body), formatted, numbering, []byte(ooxmltest.Para("note")))
	}
	// The content of a footnote, a comment and a header, which are joined
	// into one block each with their hidden ranges carried along.
	for _, note := range []string{
		`<w:p><w:r><w:t xml:space="preserve">seen </w:t></w:r><w:r><w:rPr><w:vanish/></w:rPr><w:t>unseen</w:t></w:r></w:p>` + ooxmltest.ListPara(1, 0, "item") +
			`<w:p><w:r><w:rPr><w:color w:val="FFFFFF"/></w:rPr><w:t>white</w:t></w:r></w:p>`,
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t> a</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:rPr><w:sz w:val="2"/></w:rPr><w:t>b</w:t></w:r></w:p></w:tc></w:tr>` +
			`<w:tr><w:trPr><w:trHeight w:val="1" w:hRule="exact"/></w:trPr><w:tc><w:tcPr><w:gridSpan w:val="2"/></w:tcPr><w:p><w:r><w:t>c</w:t></w:r></w:p></w:tc></w:tr></w:tbl>` + ooxmltest.Para("after"),
	} {
		f.Add(wrap(ooxmltest.Para("x")+`<w:p><w:r><w:footnoteReference w:id="2"/><w:commentReference w:id="0"/></w:r></w:p>`), formatted, numbering, []byte(note))
	}
	f.Add([]byte(`<w:document`+ooxmltest.WordNS+`><w:background w:color="000000"><v:background><v:fill type="gradient"/></v:background></w:background><w:body>`+
		ooxmltest.Para("x")+`<w:sectPr><w:headerReference w:type="first" r:id="rId5"/><w:titlePg/></w:sectPr></w:body></w:document>`), formatted, numbering, []byte(ooxmltest.Para("note")))
	opts := &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 4 << 20}, Images: gofastcore.ImagesMeta, Spans: true}
	f.Fuzz(func(t *testing.T, body, styles, numbering, note []byte) {
		pkg := ooxmltest.Docx("")
		pkg.AddBytes("word/document.xml", ooxmltest.CTDocument, body)
		pkg.Rel("word/document.xml", ooxmltest.RelStyles, "styles.xml")
		pkg.AddBytes("word/styles.xml", ooxmltest.CTStyles, styles)
		pkg.Rel("word/document.xml", ooxmltest.RelNumbering, "numbering.xml")
		pkg.AddBytes("word/numbering.xml", ooxmltest.CTNumbering, numbering)
		pkg.WordPart("footnotes.xml", ooxmltest.CTFootnotes, ooxmltest.RelFootnotes, "footnotes", `<w:footnote w:id="2">`+string(note)+`</w:footnote>`)
		pkg.WordPart("comments.xml", ooxmltest.CTComments, ooxmltest.RelComments, "comments", `<w:comment w:id="0" w:author="a">`+string(note)+`</w:comment>`)
		pkg.WordPart("header1.xml", ooxmltest.CTHeader, ooxmltest.RelHeader, "hdr", string(note))
		data := pkg.Bytes()
		doc, err := gofastooxml.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts)
		checkOutcome(t, "fuzz input", doc, err)
	})
}
