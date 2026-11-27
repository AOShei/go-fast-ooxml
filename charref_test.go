package gofastooxml_test

import (
	"context"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// charRefs returns the messages of the warnings of character references
// to no character in ws.
func charRefs(ws []gofastcore.Warning) []string {
	var out []string
	for _, w := range ws {
		if w.Code == gofastcore.WarnTextUnmappedGlyphs && strings.Contains(w.Message, "character reference") {
			out = append(out, w.Message)
		}
	}
	return out
}

const (
	oneRef  = "1 character reference names no character (&#0;, a surrogate or a number past U+10FFFF) and is read as U+FFFD"
	twoRefs = "2 character references name no character (&#0;, a surrogate or a number past U+10FFFF) and are read as U+FFFD"
)

// A character reference to no character XML allows is read as U+FFFD and
// counted, in every way a document is written.
func TestCharRefsToNoCharacter(t *testing.T) {
	refsPara := `<w:p><w:r><w:t>a&#0;b&#xD800;c</w:t></w:r></w:p>`
	docx := ooxmltest.Docx(refsPara + ooxmltest.Para("plain")).Bytes()
	flat, ok := ooxmltest.FlatOPC(docx)
	if !ok {
		t.Fatal("no Flat OPC")
	}
	xlsx := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: `<row r="1"><c r="A1" t="inlineStr"><is><t>x&#x110000;y</t></is></c></row>`}).Bytes()
	pptx := ooxmltest.Pptx(ooxmltest.Slide{Shapes: `<p:sp><p:nvSpPr><p:cNvPr id="2" name="T"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr/><p:txBody><a:bodyPr/><a:p><a:r><a:t>s&#0;t</a:t></a:r></a:p></p:txBody></p:sp>`}).Bytes()
	ss := []byte(`<?xml version="1.0"?><Workbook xmlns="urn:schemas-microsoft-com:office:spreadsheet" xmlns:ss="urn:schemas-microsoft-com:office:spreadsheet"><Worksheet ss:Name="S"><Table><Row><Cell><Data ss:Type="String">p&#0;q&#55296;r</Data></Cell></Row></Table></Worksheet></Workbook>`)
	word := wordDoc("", `<w:p><w:r><w:t>u&#0;v</w:t></w:r></w:p>`)
	for _, c := range []struct {
		name string
		data []byte
		want string
		text string
	}{
		{"docx", docx, twoRefs, "a\uFFFDb\uFFFDc"},
		{"flat opc", flat, twoRefs, "a\uFFFDb\uFFFDc"},
		{"xlsx", xlsx, oneRef, "x\uFFFDy"},
		{"pptx", pptx, oneRef, "s\uFFFDt"},
		{"spreadsheetml 2003", ss, twoRefs, "p\uFFFDq\uFFFDr"},
		{"wordml 2003", word, oneRef, "u\uFFFDv"},
	} {
		doc, err := extract(t, c.data, nil)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got := charRefs(doc.Warnings); len(got) != 1 || got[0] != c.want {
			t.Errorf("%s: warnings %q, want %q", c.name, got, c.want)
		}
		if !strings.Contains(doc.Text(), c.text) {
			t.Errorf("%s: text %q, want %q in it", c.name, doc.Text(), c.text)
		}
	}
	doc, err := extract(t, ooxmltest.Docx(ooxmltest.Para("a&b")+`<w:p><w:r><w:t>&#9;&#x1F600;</w:t></w:r></w:p>`).Bytes(), nil)
	if err != nil || len(charRefs(doc.Warnings)) > 0 {
		t.Errorf("references to characters: %v, %+v", err, doc.Warnings)
	}
}

// A part read again is counted once: the File's warnings, its document,
// and its document read a second time all give the count of the file.
func TestCharRefsCountedOnce(t *testing.T) {
	data := ooxmltest.Docx(`<w:p><w:r><w:t>a&#0;b&#0;c</w:t></w:r></w:p>`).Bytes()
	f, err := gofastooxml.OpenBytes(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if got := charRefs(f.Warnings()); len(got) != 0 {
		t.Errorf("before any part is read: %q", got)
	}
	for i := range 2 {
		doc, err := f.Document(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if got := charRefs(doc.Warnings); len(got) != 1 || got[0] != twoRefs {
			t.Errorf("reading %d: %q", i+1, got)
		}
	}
	if got := charRefs(f.Warnings()); len(got) != 1 || got[0] != twoRefs {
		t.Errorf("the File's warnings: %q", got)
	}
}
