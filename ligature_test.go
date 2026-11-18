package gofastooxml_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// The seven ligature characters U+FB00 to U+FB06 are written as their
// letters in all text, whatever way it reaches the output: the rule of
// the whole series, because the output is searched. Each file below puts
// a ligature on every path text takes in its format, the roundabout ones
// included: a character given by number, an escape in a shared string,
// text a number format writes, a list label, a bullet, a name.
func TestLigaturesAreWrittenAsLetters(t *testing.T) {
	const fi, ffl = "\ufb01", "\ufb04"
	check := func(name string, doc *gofastcore.Document, want ...string) {
		t.Helper()
		ooxmltest.CheckDocument(t, doc)
		data, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		// The JSON form holds every string of the document.
		decoded := string(data)
		for r := rune(0xFB00); r <= 0xFB06; r++ {
			if strings.ContainsRune(decoded, r) {
				t.Errorf("%s: U+%04X is in the output", name, r)
			}
		}
		for _, w := range want {
			if !strings.Contains(decoded, w) {
				t.Errorf("%s: %q is not in the output:\n%s", name, w, decoded)
			}
		}
	}

	// Word: body text, a character given by number, a list label, a
	// comment and its author, the properties.
	docx := ooxmltest.Docx(para(run(text("The o"+ffl+"ine "+fi+"le"))) +
		para(run(`<w:sym w:font="Arial" w:char="FB01"/>`+text("rst by number"))) +
		`<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>` + run(text("a labelled item")) + `</w:p>` +
		para(`<w:commentRangeStart w:id="0"/>`+run(text("commented"))+`<w:commentRangeEnd w:id="0"/>`+run(`<w:commentReference w:id="0"/>`))).
		WithNumbering(`<w:abstractNum w:abstractNumId="0"><w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:lvlText w:val="` + fi + `g. %1"/></w:lvl></w:abstractNum>` +
			`<w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>`).
		WithCore(`<dc:title>The ` + fi + `nal report</dc:title><dc:creator>Stau` + "\ufb00" + `er</dc:creator><cp:keywords>` + fi + `sh</cp:keywords>`)
	docx.WordPart("comments.xml", ooxmltest.CTComments, ooxmltest.RelComments, "comments",
		`<w:comment w:id="0" w:author="Gri`+"\ufb03"+`n">`+para(run(text("A "+fi+"ne point")))+`</w:comment>`)
	check("word", word(t, docx, nil), "The offline file", "first by number", "fig. 1", "A fine point", "Griffin", "The final report", "Stauffer", "fish")

	// Excel: a shared string, an escape in one, text a number format
	// writes, a sheet's name, a comment's author, text in a shape.
	xlsx := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "Pro" + fi + "t", Data: xlRow(1, `<c r="A1" t="s"><v>0</v></c>`, `<c r="B1" t="s"><v>1</v></c>`, xlStyled("C1", 1, "12"), xlStr("D1", "in"+fi+"nite"))}).
		WithSharedStrings("o"+"\ufb03"+"ce", "_xFB01_gure").
		WithSheetStyles(xlStyles(map[int]string{164: `0" bu` + "\ufb00" + `ers"`}, nil, nil, `numFmtId="0"`, `numFmtId="164"`))
	xlsx.Rel(firstSheet, relBase+"comments", "../comments1.xml")
	xlsx.Add("xl/comments1.xml", ctComments, `<comments xmlns="`+ooxmltest.NSSheet+`"><authors><author>Wol`+"\ufb00"+`</author></authors>`+
		`<commentList><comment ref="A1" authorId="0"><text><r><t>A `+fi+`rst note</t></r></text></comment></commentList></comments>`)
	check("excel", excel(t, xlsx, nil), "office", "figure", "12 buffers", "infinite", "Profit", "Wolff", "A first note")

	// PowerPoint: a title (and so the slide's name), body text, a bullet,
	// a table cell, a picture's description, the notes, a comment.
	pptx := deck(ooxmltest.Slide{
		Shapes: ooxmltest.Shape(2, ph("title", -1), "", ooxmltest.TextBody(dpara("", plain("The "+fi+"rst quarter")))) +
			ooxmltest.Shape(3, ph("", 1), "", ooxmltest.TextBody(dpara(`<a:pPr><a:buChar char="`+fi+`"/></a:pPr>`, plain("a"+ffl+"uent")))) +
			tableFrame(4, 10, 300, 300, 50, `<a:tblPr/>`, trow(tc("di"+"\ufb00"+"erence"))) +
			picture(5, "rId5", "A "+fi+"gure", 400, 300, 50, 50, ""),
		Notes: noteShapes(dpara("", plain("Con"+fi+"rm the numbers"))),
	})
	pptx.AddBytes("ppt/media/image1.png", "image/png", pngBytes(t, 4, 3))
	for pptx.Rel("ppt/slides/slide1.xml", ooxmltest.RelImage, "../media/image1.png") != "rId5" {
	}
	pptx.Rel("ppt/slides/slide1.xml", relBase+"comments", "../comments/comment1.xml")
	pptx.Add("ppt/comments/comment1.xml", ooxmltest.CTSlideComments, `<p:cmLst`+ooxmltest.PresNS+`><p:cm authorId="0" idx="1"><p:text>Is this `+fi+`nal?</p:text></p:cm></p:cmLst>`)
	pptx.Rel("ppt/presentation.xml", relBase+"commentAuthors", "commentAuthors.xml")
	pptx.Add("ppt/commentAuthors.xml", "application/xml", `<p:cmAuthorLst`+ooxmltest.PresNS+`><p:cmAuthor id="0" name="Schi`+"\ufb00"+`"/></p:cmAuthorLst>`)
	doc := powerpoint(t, pptx, &gofastcore.Options{Images: gofastcore.ImagesMeta})
	check("powerpoint", doc, "The first quarter", "affluent", "difference", "A figure", "Confirm the numbers", "Is this final?", "Schiff")
	if doc.Parts[0].Name != "The first quarter" {
		t.Errorf("the slide's name is %q", doc.Parts[0].Name)
	}
}
