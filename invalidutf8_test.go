package gofastooxml_test

import (
	"context"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// invalidUTF8 returns the messages of the warnings of bytes that are not
// UTF-8 in ws.
func invalidUTF8(ws []gofastcore.Warning) []string {
	var out []string
	for _, w := range ws {
		if w.Code == gofastcore.WarnTextUnmappedGlyphs && strings.Contains(w.Message, "not valid UTF-8") {
			out = append(out, w.Message)
		}
	}
	return out
}

const (
	oneBad   = "1 byte sequence is not valid UTF-8 and is read as U+FFFD"
	twoBad   = "2 byte sequences are not valid UTF-8 and are read as U+FFFD"
	threeBad = "3 byte sequences are not valid UTF-8 and are read as U+FFFD"
)

// A byte of XML in UTF-8 that is not part of a character is read as
// U+FFFD, one for each byte, and counted, in every way a document is
// written, in text and in attribute values.
func TestInvalidUTF8(t *testing.T) {
	// \xE2\x82 is a character cut short (two bytes), \xFF a byte no
	// character has: three bytes, in two runs.
	docx := ooxmltest.Docx(`<w:p><w:r><w:t>a` + "\xE2\x82" + `b` + "\xFF" + `c</w:t></w:r></w:p>` + ooxmltest.Para("plain")).Bytes()
	flat, ok := ooxmltest.FlatOPC(docx)
	if !ok {
		t.Fatal("no Flat OPC")
	}
	xlsx := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: `<row r="1"><c r="A1" t="inlineStr"><is><t>x` + "\xC0\xAF" + `y</t></is></c></row>`}).Bytes()
	pptx := ooxmltest.Pptx(ooxmltest.Slide{Shapes: `<p:sp><p:nvSpPr><p:cNvPr id="2" name="T"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr/><p:txBody><a:bodyPr/><a:p><a:r><a:t>s` + "\xFF" + `t</a:t></a:r></a:p></p:txBody></p:sp>`}).Bytes()
	ss := []byte(`<?xml version="1.0"?><Workbook xmlns="urn:schemas-microsoft-com:office:spreadsheet" xmlns:ss="urn:schemas-microsoft-com:office:spreadsheet"><Worksheet ss:Name="S"><Table><Row><Cell><Data ss:Type="String">p` + "\xFE\xFF" + `q</Data></Cell></Row></Table></Worksheet></Workbook>`)
	word := wordDoc("", `<w:p><w:r><w:t>u`+"\x80"+`v</w:t></w:r></w:p>`)
	// An attribute value: a list label's template.
	num := `<w:abstractNum w:abstractNumId="0"><w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:lvlText w:val="` +
		"\xFF" + `%1."/></w:lvl></w:abstractNum><w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>`
	label := ooxmltest.Docx(ooxmltest.ListPara(1, 0, "item")).WithNumbering(num).Bytes()
	for _, c := range []struct {
		name string
		data []byte
		want string
		text string
	}{
		{"docx", docx, threeBad, "a\uFFFD\uFFFDb\uFFFDc"},
		{"flat opc", flat, threeBad, "a\uFFFD\uFFFDb\uFFFDc"},
		{"xlsx", xlsx, twoBad, "x\uFFFD\uFFFDy"},
		{"pptx", pptx, oneBad, "s\uFFFDt"},
		{"spreadsheetml 2003", ss, twoBad, "p\uFFFD\uFFFDq"},
		{"wordml 2003", word, oneBad, "u\uFFFDv"},
		{"attribute", label, oneBad, "\uFFFD1."},
	} {
		doc, err := extract(t, c.data, nil)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got := invalidUTF8(doc.Warnings); len(got) != 1 || got[0] != c.want {
			t.Errorf("%s: warnings %q, want %q", c.name, got, c.want)
		}
		if !strings.Contains(doc.Text(), c.text) {
			t.Errorf("%s: text %q, want %q in it", c.name, doc.Text(), c.text)
		}
	}
	doc, err := extract(t, ooxmltest.Docx(ooxmltest.Para("caf\u00e9 \u4E2D\u6587 \U0001F600")).Bytes(), nil)
	if err != nil || len(invalidUTF8(doc.Warnings)) > 0 {
		t.Errorf("valid UTF-8: %v, %+v", err, doc.Warnings)
	}
}

// Both kinds of U+FFFD are said, each in its own warning.
func TestInvalidUTF8AndCharRefs(t *testing.T) {
	doc, err := extract(t, ooxmltest.Docx(`<w:p><w:r><w:t>a&#0;b`+"\xFF"+`c</w:t></w:r></w:p>`).Bytes(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := charRefs(doc.Warnings); len(got) != 1 || got[0] != oneRef {
		t.Errorf("references: %q", got)
	}
	if got := invalidUTF8(doc.Warnings); len(got) != 1 || got[0] != oneBad {
		t.Errorf("bytes: %q", got)
	}
}

// A part read again is counted once: the File's warnings, its document,
// and its document read a second time all give the count of the file.
func TestInvalidUTF8CountedOnce(t *testing.T) {
	data := ooxmltest.Docx(`<w:p><w:r><w:t>a` + "\xFF" + `b` + "\xFF" + `c</w:t></w:r></w:p>`).Bytes()
	f, err := gofastooxml.OpenBytes(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if got := invalidUTF8(f.Warnings()); len(got) != 0 {
		t.Errorf("before any part is read: %q", got)
	}
	for i := range 2 {
		doc, err := f.Document(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if got := invalidUTF8(doc.Warnings); len(got) != 1 || got[0] != twoBad {
			t.Errorf("reading %d: %q", i+1, got)
		}
	}
	if got := invalidUTF8(f.Warnings()); len(got) != 1 || got[0] != twoBad {
		t.Errorf("the File's warnings: %q", got)
	}
}
