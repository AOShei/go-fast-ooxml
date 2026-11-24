package gofastooxml_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// wordDoc wraps the parts of a WordprocessingML 2003 document as Word
// 2003 writes them.
func wordDoc(head, body string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<?mso-application progid="Word.Document"?>
<w:wordDocument xmlns:w="http://schemas.microsoft.com/office/word/2003/wordml" xmlns:v="urn:schemas-microsoft-com:vml"
 xmlns:w10="urn:schemas-microsoft-com:office:word" xmlns:sl="http://schemas.microsoft.com/schemaLibrary/2003/core"
 xmlns:aml="http://schemas.microsoft.com/aml/2001/core" xmlns:wx="http://schemas.microsoft.com/office/word/2003/auxHint"
 xmlns:o="urn:schemas-microsoft-com:office:office" xmlns:st1="urn:schemas-microsoft-com:office:smarttags"
 xmlns:wsp="http://schemas.microsoft.com/office/word/2003/wordml/sp2" xml:space="preserve">` + head + `<w:body><wx:sect>` + body + `</wx:sect></w:body></w:wordDocument>`)
}

const wordStyles2003 = `<w:styles>
 <w:style w:type="paragraph" w:default="on" w:styleId="Normal"><w:name w:val="Normal"/><wx:uiName wx:val="Normal"/></w:style>
 <w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><wx:uiName wx:val="Heading 1"/><w:basedOn w:val="Normal"/><w:pPr><w:outlineLvl w:val="0"/></w:pPr><w:rPr><w:b/><w:b-cs/><w:sz w:val="32"/><w:sz-cs w:val="32"/></w:rPr></w:style>
 <w:style w:type="paragraph" w:styleId="MyHeading"><w:name w:val="My Heading"/><w:basedOn w:val="Heading1"/></w:style>
 <w:style w:type="character" w:styleId="Secret"><w:name w:val="Secret"/><w:rPr><w:vanish/></w:rPr></w:style>
</w:styles>`

const wordLists2003 = `<w:lists>
 <w:listDef w:listDefId="0"><w:plt w:val="HybridMultilevel"/>
  <w:lvl w:ilvl="0"><w:start w:val="1"/><w:lvlText w:val="%1."/><w:lvlJc w:val="left"/></w:lvl>
  <w:lvl w:ilvl="1"><w:start w:val="1"/><w:nfc w:val="4"/><w:lvlText w:val="%2)"/><w:lvlJc w:val="left"/></w:lvl>
 </w:listDef>
 <w:listDef w:listDefId="1"><w:lvl w:ilvl="0"><w:start w:val="1"/><w:nfc w:val="23"/><w:lvlText w:val="•"/><w:lvlJc w:val="left"/></w:lvl></w:listDef>
 <w:list w:ilfo="1"><w:ilst w:val="0"/></w:list>
 <w:list w:ilfo="2"><w:ilst w:val="1"/></w:list>
</w:lists>`

func wpara(text string) string { return `<w:p><w:r><w:t>` + text + `</w:t></w:r></w:p>` }

func readWord(t *testing.T, data []byte, opts *gofastcore.Options) (*gofastcore.Document, string) {
	t.Helper()
	doc, err := gofastooxml.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts)
	if err != nil {
		t.Fatal(err)
	}
	ooxmltest.CheckDocument(t, doc)
	out, _ := json.Marshal(doc)
	return doc, string(out)
}

// blocks lists a document's blocks as "kind level text".
func blockList(doc *gofastcore.Document) string {
	var b strings.Builder
	for _, p := range doc.Parts {
		for _, bl := range p.Blocks {
			fmt.Fprintf(&b, "%s %d %q", bl.Kind, bl.Level, bl.Text)
			if bl.Display != 0 {
				fmt.Fprintf(&b, " %s", bl.Display)
			}
			if bl.Table != nil {
				rows, _ := json.Marshal(bl.Table.Rows)
				fmt.Fprintf(&b, " %s", rows)
			}
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func TestWordMLStructure(t *testing.T) {
	data := wordDoc(`<o:DocumentProperties><o:Title>Report</o:Title><o:Author>A. Writer</o:Author><o:Created>2005-03-04T05:06:00Z</o:Created></o:DocumentProperties>`+
		wordStyles2003+wordLists2003+`<w:docPr><w:evenAndOddHeaders/><w:footnotePr><w:numFmt w:val="upper-letter"/></w:footnotePr></w:docPr>`,
		`<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Introduction</w:t></w:r></w:p>`+
			`<w:p><w:pPr><w:pStyle w:val="MyHeading"/></w:pPr><w:r><w:t>Inherited heading</w:t></w:r></w:p>`+
			`<w:p><w:r><w:t>The </w:t></w:r><st1:City><st1:place><w:r><w:t>Paris</w:t></w:r></st1:place></st1:City><w:r><w:t> office</w:t></w:r><w:r><w:tab/><w:t>tabbed</w:t></w:r></w:p>`+
			`<w:p><w:pPr><w:listPr><w:ilvl w:val="0"/><w:ilfo w:val="1"/><wx:t wx:val="1."/></w:listPr></w:pPr><w:r><w:t>first</w:t></w:r></w:p>`+
			`<w:p><w:pPr><w:listPr><w:ilvl w:val="1"/><w:ilfo w:val="1"/></w:listPr></w:pPr><w:r><w:t>nested</w:t></w:r></w:p>`+
			`<w:p><w:pPr><w:listPr><w:ilvl w:val="0"/><w:ilfo w:val="1"/></w:listPr></w:pPr><w:r><w:t>second</w:t></w:r></w:p>`+
			`<w:p><w:pPr><w:listPr><w:ilvl w:val="0"/><w:ilfo w:val="2"/></w:listPr></w:pPr><w:r><w:t>bullet</w:t></w:r></w:p>`+
			`<w:p><w:r><w:t>Shown </w:t></w:r><w:r><w:rPr><w:vanish/></w:rPr><w:t>hidden</w:t></w:r><w:r><w:rPr><w:rStyle w:val="Secret"/></w:rPr><w:t> styled</w:t></w:r></w:p>`+
			`<w:tbl><w:tblGrid><w:gridCol/><w:gridCol/></w:tblGrid>`+
			`<w:tr><w:tc><w:tcPr><w:gridSpan w:val="2"/></w:tcPr>`+wpara("wide")+`</w:tc></w:tr>`+
			`<w:tr><w:tc><w:tcPr><w:vmerge w:val="restart"/></w:tcPr>`+wpara("tall")+`</w:tc><w:tc>`+wpara("b2")+`</w:tc></w:tr>`+
			`<w:tr><w:tc><w:tcPr><w:vmerge/></w:tcPr><w:p/></w:tc><w:tc>`+wpara("b3")+`</w:tc></w:tr></w:tbl>`+
			`<w:p><w:r><w:t>See note</w:t></w:r><w:r><w:rPr><w:rStyle w:val="FootnoteReference"/></w:rPr><w:footnote><w:p><w:r><w:footnoteRef/></w:r><w:r><w:t>The footnote.</w:t></w:r></w:p></w:footnote></w:r>`+
			`<w:r><w:endnote><w:p><w:r><w:t>The endnote.</w:t></w:r></w:p></w:endnote></w:r></w:p>`+
			`<w:sectPr><w:hdr w:type="odd">`+wpara("Odd header")+`</w:hdr><w:hdr w:type="even">`+wpara("Even header")+`</w:hdr><w:ftr w:type="odd">`+wpara("Footer")+`</w:ftr><w:pgSz w:w="12240" w:h="15840"/></w:sectPr>`)
	doc, out := readWord(t, data, nil)
	if doc.Source.Format != "wordml" || doc.Source.MIME != "application/vnd.ms-wordml" || doc.Metadata.Title != "Report" {
		t.Errorf("source %+v, metadata %+v", doc.Source, doc.Metadata)
	}
	got := blockList(doc)
	for _, want := range []string{
		`header 0 "Odd header"`, `header 0 "Even header"`,
		`heading 1 "Introduction"`, `heading 1 "Inherited heading"`,
		`paragraph 0 "The Paris office\ttabbed"`,
		`list_item 1 "first"`, `list_item 2 "nested"`, `list_item 1 "second"`, `list_item 1 "bullet"`,
		`footer 0 "Footer"`,
		`[["wide",""],["tall","b2"],["","b3"]]`,
		`footnote 0 "The footnote."`, `footnote 0 "The endnote."`, // endnotes are notes of the same kind
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in\n%s", want, got)
		}
	}
	for _, want := range []string{`"marker":"1."`, `"marker":"a)"`, `"marker":"2."`, `"marker":"•"`, `"text":"The footnote.","marker":"A"`, `"text":"The endnote.","marker":"i"`, `"display":"hidden"`, `"row_span":2`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
}

func TestWordMLAnnotationsLinksPictures(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nnot really a picture")
	b64 := base64.StdEncoding.EncodeToString(png)
	data := wordDoc(wordStyles2003, `<w:p><w:r><w:t>Kept </w:t></w:r>`+
		`<aml:annotation aml:id="0" w:type="Word.Insertion" aml:author="Ann" aml:createdate="2006-01-02T03:04:00Z"><aml:content><w:r><w:t>inserted </w:t></w:r></aml:content></aml:annotation>`+
		`<aml:annotation aml:id="1" w:type="Word.Deletion" aml:author="Bob" aml:createdate="2006-01-02T03:05:00Z"><aml:content><w:r><w:delText>deleted </w:delText></w:r></aml:content></aml:annotation>`+
		`<aml:annotation aml:id="2" w:type="Word.Comment.Start"/><w:r><w:t>commented</w:t></w:r><aml:annotation aml:id="2" w:type="Word.Comment.End"/>`+
		`<w:r><aml:annotation aml:id="2" aml:author="Cy" aml:createdate="2006-01-02T03:06:00Z" w:type="Word.Comment" w:initials="C"><aml:content><w:p><w:r><w:t>A remark.</w:t></w:r></w:p></aml:content></aml:annotation></w:r>`+
		`<aml:annotation aml:id="3" w:type="Word.Bookmark.Start" w:name="here"/><aml:annotation aml:id="3" w:type="Word.Bookmark.End"/>`+
		`<aml:annotation aml:id="4" w:type="Word.Formatting" aml:author="Dee"/>`+
		`</w:p>`+
		`<w:p><w:hlink w:dest="http://example.com/a?b=1&amp;c=2"><w:r><w:t>out</w:t></w:r></w:hlink><w:r><w:t> and </w:t></w:r><w:hlink w:bookmark="here"><w:r><w:t>in</w:t></w:r></w:hlink></w:p>`+
		`<w:p><w:r><w:pict><v:shape style="width:10pt;height:10pt"><v:imagedata src="wordml://01000001.png" o:title="A picture"/></v:shape><w:binData w:name="wordml://01000001.png">`+b64[:10]+"\n  "+b64[10:]+`</w:binData></w:pict></w:r></w:p>`+
		`<w:p><w:fldSimple w:instr=" DATE \@ &quot;yyyy&quot; "><w:r><w:t>2006</w:t></w:r></w:fldSimple></w:p>`)
	doc, out := readWord(t, data, &gofastcore.Options{Spans: true, Images: gofastcore.ImagesData})
	got := blockList(doc)
	for _, want := range []string{`paragraph 0 "Kept inserted commented"`, `note 0 "deleted"`, `note 0 "A remark."`, `paragraph 0 "out and in"`, `image 0 "A picture"`, `paragraph 0 "2006"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in\n%s", want, got)
		}
	}
	for _, want := range []string{`"author":"Bob"`, `"type":"deletion"`, `"author":"Cy"`, `"href":"http://example.com/a?b=1` + `\` + `u0026c=2"`, `"href":"#here"`,
		`"mime":"image/png"`, `"data":"` + b64 + `"`, `"text.tracked_changes"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
}

func TestWordMLDamageAndRefusals(t *testing.T) {
	whole := wordDoc("", wpara("first")+wpara("second")+wpara("third"))
	cut := whole[:bytes.Index(whole, []byte("third"))]
	doc, out := readWord(t, cut, nil)
	if got := blockList(doc); !strings.Contains(got, `"first"`) || !strings.Contains(got, `"second"`) || !strings.Contains(out, gofastcore.WarnInputRepaired) {
		t.Errorf("cut short: %s", out)
	}
	// Malformed XML in the body: what is before it is read.
	bad := wordDoc("", wpara("before")+`<w:p><w:r><w:t>x</w:r></w:p>`+wpara("after"))
	doc, out = readWord(t, bad, nil)
	if got := blockList(doc); !strings.Contains(got, `"before"`) || !strings.Contains(out, gofastcore.WarnInputRepaired) {
		t.Errorf("malformed: %s", out)
	}
	// A picture over the decompressed size limit.
	big := wordDoc("", `<w:p><w:r><w:pict><w:binData w:name="wordml://1.png">`+strings.Repeat("QUJD", 10000)+`</w:binData></w:pict></w:r></w:p>`)
	if _, err := gofastooxml.Extract(context.Background(), bytes.NewReader(big), int64(len(big)), &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 1000}}); err == nil {
		t.Error("a picture over the limit was read")
	}
	// UTF-16.
	u := utf16With(string(wordDoc("", wpara("grüße"))))
	if _, out := readWord(t, u, nil); !strings.Contains(out, `"grüße"`) {
		t.Errorf("UTF-16: %s", out)
	}
}

// TestWordMLWorstCase: what translating WordprocessingML 2003 costs grows
// with the file and no faster: many notes, many sections with headers,
// many comments, many lists.
func TestWordMLWorstCase(t *testing.T) {
	if testing.Short() {
		t.Skip("times large inputs")
	}
	read := func(data []byte) func() {
		return func() {
			gofastooxml.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), nil)
		}
	}
	repeat := func(n int, f func(i int) string) string {
		var b strings.Builder
		for i := range n {
			b.WriteString(f(i))
		}
		return b.String()
	}
	notes := func(n int) []byte {
		return wordDoc("", repeat(n, func(i int) string {
			return `<w:p><w:r><w:t>x</w:t></w:r><w:r><w:footnote>` + wpara("n") + `</w:footnote></w:r></w:p>`
		}))
	}
	costsAtMost(t, 8, "many notes", read(notes(2_000)), read(notes(8_000)))
	sections := func(n int) []byte {
		return wordDoc("", repeat(n, func(i int) string {
			return `<w:p><w:pPr><w:sectPr><w:hdr w:type="odd">` + wpara(fmt.Sprint("h", i)) + `</w:hdr></w:sectPr></w:pPr></w:p>`
		}))
	}
	costsAtMost(t, 8, "many sections with headers", read(sections(500)), read(sections(2_000)))
	comments := func(n int) []byte {
		return wordDoc("", repeat(n, func(i int) string {
			id := fmt.Sprint(i)
			return `<w:p><aml:annotation aml:id="` + id + `" w:type="Word.Comment.Start"/><w:r><w:t>c</w:t></w:r><aml:annotation aml:id="` + id + `" w:type="Word.Comment.End"/>` +
				`<w:r><aml:annotation aml:id="` + id + `" w:type="Word.Comment"><aml:content>` + wpara("remark") + `</aml:content></aml:annotation></w:r></w:p>`
		}))
	}
	costsAtMost(t, 8, "many comments", read(comments(2_000)), read(comments(8_000)))
	lists := func(n int) []byte {
		defs := `<w:lists>` + repeat(n, func(i int) string {
			return fmt.Sprintf(`<w:listDef w:listDefId="%d"><w:lvl w:ilvl="0"><w:start w:val="1"/><w:lvlText w:val="%%1."/></w:lvl></w:listDef>`, i)
		}) + repeat(n, func(i int) string { return fmt.Sprintf(`<w:list w:ilfo="%d"><w:ilst w:val="%d"/></w:list>`, i+1, i) }) + `</w:lists>`
		return wordDoc(defs, repeat(n, func(i int) string {
			return fmt.Sprintf(`<w:p><w:pPr><w:listPr><w:ilvl w:val="0"/><w:ilfo w:val="%d"/></w:listPr></w:pPr><w:r><w:t>i</w:t></w:r></w:p>`, i+1)
		}))
	}
	costsAtMost(t, 8, "many lists", read(lists(1_000)), read(lists(4_000)))
}

// FuzzWordML feeds arbitrary WordprocessingML 2003 documents to Extract,
// from seeds that use what the format has.
func FuzzWordML(f *testing.F) {
	f.Add(wordDoc(wordStyles2003+wordLists2003+`<w:docPr><w:evenAndOddHeaders/></w:docPr>`,
		`<w:p><w:pPr><w:pStyle w:val="Heading1"/><w:listPr><w:ilvl w:val="1"/><w:ilfo w:val="1"/></w:listPr></w:pPr><w:r><w:t>h</w:t></w:r></w:p>`+
			`<w:p><aml:annotation aml:id="0" w:type="Word.Insertion"><aml:content><w:r><w:t>i</w:t></w:r></aml:content></aml:annotation>`+
			`<w:r><aml:annotation aml:id="1" w:type="Word.Comment"><aml:content>`+wpara("c")+`</aml:content></aml:annotation><w:footnote>`+wpara("f")+`</w:footnote></w:r>`+
			`<w:hlink w:dest="http://x/" w:bookmark="b"><w:r><w:t>l</w:t></w:r></w:hlink></w:p>`+
			`<w:tbl><w:tr><w:tc><w:tcPr><w:vmerge w:val="restart"/><w:gridSpan w:val="2"/></w:tcPr>`+wpara("t")+`</w:tc></w:tr></w:tbl>`+
			`<w:p><w:r><w:pict><v:shape><v:imagedata src="wordml://1.png"/></v:shape><w:binData w:name="wordml://1.png">iVBORw0KGgo=</w:binData></w:pict></w:r></w:p>`+
			`<w:sectPr><w:hdr w:type="first">`+wpara("hd")+`</w:hdr><w:ftr>`+wpara("ft")+`</w:ftr><w:titlePg/></w:sectPr>`))
	opts := &gofastcore.Options{Spans: true, Images: gofastcore.ImagesData, Limits: gofastcore.Limits{MaxDecodedBytes: 4 << 20}}
	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := gofastooxml.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts)
		checkOutcome(t, "fuzz input", doc, err)
	})
}
