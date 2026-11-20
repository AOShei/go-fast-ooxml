package gofastooxml_test

import (
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// hrLine is Word's Horizontal Line as Word writes it: a VML rectangle
// marked o:hr, in a run of its own.
func hrLine(style string) string {
	return run(`<w:pict><v:rect id="_x0000_i1025" style="width:0;height:1.5pt` + style + `" o:hralign="center" o:hrstd="t" o:hr="t" fillcolor="#a0a0a0" stroked="f"/></w:pict>`)
}

// A thematic break is what the file declares one: Word's Horizontal Line,
// a shape marked o:hr. It stands where the line is: alone, after the text
// before it in its paragraph, or before the text after it. A picture line
// is a break too, not a picture.
func TestWordHorizontalLine(t *testing.T) {
	picture := run(`<w:pict><v:shape id="_x0000_i1026" style="width:468pt;height:7.5pt" o:hrpct="0" o:hralign="center" o:hr="t"><v:imagedata r:id="rId7" o:title="line"/></v:shape></w:pict>`)
	body := para(run(text("Before."))) +
		para(hrLine("")) +
		para(run(text("Between."))) +
		para(run(text("Text first."))+hrLine("")) +
		para(hrLine("")+run(text("Line first."))) +
		para(picture) +
		para(run(text("After.")))
	pkg := ooxmltest.Docx(body)
	pkg.Rel("word/document.xml", ooxmltest.RelImage, "media/line.png")
	pkg.AddBytes("word/media/line.png", "image/png", []byte("\x89PNG\r\n\x1a\n"))
	doc := word(t, pkg, nil)
	expectBlocks(t, doc,
		`paragraph "Before."`,
		`thematic_break`,
		`paragraph "Between."`,
		`paragraph "Text first."`,
		`thematic_break`,
		`thematic_break`,
		`paragraph "Line first."`,
		`thematic_break`,
		`paragraph "After."`,
	)
	for _, b := range doc.Parts[0].Blocks {
		if b.Kind == gofastcore.KindThematicBreak && b.Origin != gofastcore.OriginNative {
			t.Errorf("a horizontal line has origin %v", b.Origin)
		}
	}
	if md := doc.Markdown(); !strings.Contains(md, "Before.\n\n---\n\nBetween.") {
		t.Errorf("Markdown:\n%s", md)
	}
	if err := doc.Validate(); err != nil {
		t.Error(err)
	}
}

// Nothing else is a break: a paragraph with only a bottom border (what
// Word makes of a typed ---), a bordered paragraph with text, section,
// page and column breaks, and a paragraph of asterisks, which stays text.
func TestWordNotABreak(t *testing.T) {
	bottom := `<w:pPr><w:pBdr><w:bottom w:val="single" w:sz="6" w:space="1" w:color="auto"/></w:pBdr></w:pPr>`
	body := para(run(text("One."))) +
		`<w:p>` + bottom + `</w:p>` +
		`<w:p>` + bottom + run(text("Underlined by a border.")) + `</w:p>` +
		para(run(`<w:br w:type="page"/>`)+run(text("Two."))) +
		para(run(`<w:br w:type="column"/>`)) +
		`<w:p><w:pPr><w:pageBreakBefore/><w:sectPr/></w:pPr>` + run(text("Three.")) + `</w:p>` +
		para(run(text("* * *"))) +
		para(run(text("Four.")))
	doc := word(t, ooxmltest.Docx(body), nil)
	expectBlocks(t, doc,
		`paragraph "One."`,
		`paragraph "Underlined by a border."`,
		`paragraph "Two."`,
		`paragraph "Three."`,
		`paragraph "* * *"`,
		`paragraph "Four."`,
	)
}

// A horizontal line in hidden text is a hidden break; one in a tracked
// deletion is not there; one in a tracked insertion is. In a table cell,
// a text box, a header or a footnote it is not a break.
func TestWordHorizontalLineInPlaces(t *testing.T) {
	body := para(run(text("Start."))) +
		para(styled(vanish, "")+run(`<w:rPr><w:vanish/></w:rPr><w:pict><v:rect style="width:0;height:1.5pt" o:hr="t" o:hrstd="t"/></w:pict>`)) +
		`<w:p><w:del w:id="1" w:author="A">` + run(`<w:pict><v:rect style="width:0;height:1.5pt" o:hr="t"/></w:pict>`) + `</w:del></w:p>` +
		`<w:p><w:ins w:id="2" w:author="A">` + hrLine("") + `</w:ins></w:p>` +
		`<w:tbl><w:tr><w:tc>` + para(run(text("Cell."))+hrLine("")) + `</w:tc></w:tr></w:tbl>` +
		para(run(`<w:pict><v:shape style="width:100pt;height:50pt"><v:textbox><w:txbxContent>`+para(run(text("In a box.")))+para(hrLine(""))+`</w:txbxContent></v:textbox></v:shape></w:pict>`)) +
		para(run(text("End.")))
	pkg := ooxmltest.DocxSection(body, `<w:headerReference w:type="default" r:id="rId1"/>`)
	pkg.WordPart("header1.xml", ooxmltest.CTHeader, ooxmltest.RelHeader, "hdr", para(run(text("Header.")))+para(hrLine("")))
	doc := word(t, pkg, nil)
	expectBlocks(t, doc,
		`header "Header."`,
		`paragraph "Start."`,
		`thematic_break display=hidden`,
		`thematic_break`,
		`table header=0 [["Cell."]]`,
		`paragraph "In a box."`,
		`paragraph "End."`,
	)
	if err := doc.Validate(); err != nil {
		t.Error(err)
	}
	ooxmltest.CheckDocument(t, doc)
}
