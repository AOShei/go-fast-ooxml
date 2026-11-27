package opc

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/AOShei/go-fast-core/xmlscan"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
)

const pkgNS = `xmlns:pkg="http://schemas.microsoft.com/office/2006/xmlPackage"`

func openFlat(t *testing.T, xml string, lim Limits) *Package {
	t.Helper()
	p, err := OpenFlat(strings.NewReader(xml), int64(len(xml)), lim)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func readPart(t *testing.T, p *Package, name string) string {
	t.Helper()
	rc, err := p.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return string(b)
}

const flatDoc = `<?xml version="1.0" standalone="yes"?>
<?mso-application progid="Word.Document"?>
<pkg:package ` + pkgNS + `>
 <pkg:part pkg:name="/_rels/.rels" pkg:contentType="application/vnd.openxmlformats-package.relationships+xml" pkg:padding="512">
  <pkg:xmlData><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships></pkg:xmlData>
 </pkg:part>
 <pkg:part pkg:name="/word/document.xml" pkg:contentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml">
  <pkg:xmlData><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>a &lt; b</w:t></w:r></w:p><!-- </pkg:xmlData> --><![CDATA[</pkg:xmlData>]]><![CDATA[]]]><?pi </pkg:xmlData> ?><w:p w:x="&gt;/>"/></w:body></w:document></pkg:xmlData>
 </pkg:part>
 <pkg:part pkg:name="/word/media/image1.png" pkg:contentType="image/png" pkg:compression="store">
  <pkg:binaryData>aGVsbG8g
   d29ybGQ=</pkg:binaryData>
 </pkg:part>
</pkg:package>`

func TestFlatParts(t *testing.T) {
	p := openFlat(t, flatDoc, noLimits)
	if !p.Flat() || p.FlatDamage() != nil {
		t.Fatalf("flat %v, damage %v", p.Flat(), p.FlatDamage())
	}
	if got := strings.Join(p.Names(), " "); got != "_rels/.rels word/document.xml word/media/image1.png" {
		t.Errorf("names: %s", got)
	}
	main, format, _, err := p.Main()
	if err != nil || main != "word/document.xml" || format != Docx {
		t.Errorf("main: %q %q %v", main, format, err)
	}
	doc := readPart(t, p, "word/document.xml")
	if !strings.HasPrefix(doc, "<w:document ") || !strings.HasSuffix(doc, "</w:document>") || !strings.Contains(doc, "<![CDATA[</pkg:xmlData>]]>") {
		t.Errorf("document part: %s", doc)
	}
	if got := readPart(t, p, "word/media/image1.png"); got != "hello world" {
		t.Errorf("binary part: %q", got)
	}
	if got := p.Size("word/media/image1.png"); got != int64(len("hello world")) {
		t.Errorf("binary part's size: %d", got)
	}
	if got := p.ContentType("word/media/image1.png"); got != "image/png" {
		t.Errorf("content type: %q", got)
	}
	rels, err := p.Rels("")
	if err != nil || len(rels) != 1 || rels[0].Target != "word/document.xml" {
		t.Errorf("rels: %v %v", rels, err)
	}
	// The budget is charged as for an archive's parts.
	small := openFlat(t, flatDoc, Limits{MaxDecodedBytes: 100})
	rc, _ := small.Open("word/document.xml")
	if _, err := io.ReadAll(rc); !errors.Is(err, ErrBudget) {
		t.Errorf("over the budget: %v", err)
	}
}

// A part may use a prefix declared around it, on the package, the part or
// its data element.
func TestFlatInheritedNamespaces(t *testing.T) {
	xml := `<pkg:package ` + pkgNS + ` xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<pkg:part pkg:name="/a.xml" pkg:contentType="x"><pkg:xmlData xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><w:document><Relationships/></w:document></pkg:xmlData></pkg:part></pkg:package>`
	p := openFlat(t, xml, noLimits)
	var s xmlscan.Scanner
	c, err := p.Scan("a.xml", &s, &xmlscan.Options{Spaces: ns.Spaces})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var got []string
	for s.Next() != xmlscan.EOF {
		if s.Kind() == xmlscan.Start {
			got = append(got, string(s.Local()))
			switch string(s.Local()) {
			case "document":
				if !s.Is(ns.Word, "document") {
					t.Errorf("w: not resolved from the package's declaration")
				}
			case "Relationships":
				if !s.Is(ns.PackageRels, "Relationships") {
					t.Errorf("default namespace not resolved from the data element's declaration")
				}
			}
		}
	}
	if s.Err() != nil || strings.Join(got, " ") != "document Relationships" {
		t.Errorf("%v %v", got, s.Err())
	}
}

func utf16le(s string) []byte {
	b := []byte{0xFF, 0xFE}
	for _, u := range utf16.Encode([]rune(s)) {
		b = append(b, byte(u), byte(u>>8))
	}
	return b
}

func TestFlatEncodings(t *testing.T) {
	body := `<pkg:package ` + pkgNS + `><pkg:part pkg:name="/a.xml" pkg:contentType="x"><pkg:xmlData><a>café</a></pkg:xmlData></pkg:part></pkg:package>`
	for name, data := range map[string][]byte{
		"UTF-16":  utf16le(`<?xml version="1.0" encoding="UTF-16"?>` + body),
		"Latin-1": []byte(strings.ReplaceAll(`<?xml version="1.0" encoding="ISO-8859-1"?>`+body, "é", "\xe9")),
		"BOM":     append([]byte{0xEF, 0xBB, 0xBF}, body...),
	} {
		p, err := OpenFlat(bytes.NewReader(data), int64(len(data)), noLimits)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		var s xmlscan.Scanner
		c, err := p.Scan("a.xml", &s, nil)
		if err != nil {
			t.Fatal(err)
		}
		text := ""
		for s.Next() != xmlscan.EOF {
			if s.Kind() == xmlscan.Text {
				text += string(s.Text())
			}
		}
		c.Close()
		if text != "café" || s.Err() != nil {
			t.Errorf("%s: %q %v", name, text, s.Err())
		}
	}
}

func TestFlatDamage(t *testing.T) {
	// Cut inside the third part: the two before it are read, and where
	// it breaks off is said.
	cut := flatDoc[:strings.Index(flatDoc, "d29ybGQ")]
	p := openFlat(t, cut, noLimits)
	d := p.FlatDamage()
	if d == nil || d.Parts != 2 || len(p.Names()) != 2 {
		t.Fatalf("damage %+v, parts %v", d, p.Names())
	}
	if !strings.Contains(d.Message(), "2 parts") {
		t.Errorf("message: %s", d.Message())
	}
	// A part whose XML is malformed, or nested deeper than the scanner
	// reads, is that part's damage, as in an archive: the package is read.
	deep := strings.Repeat("<a>", 600) + strings.Repeat("</a>", 600)
	bad := `<pkg:package ` + pkgNS + `>` +
		`<pkg:part pkg:name="/bad.xml" pkg:contentType="x"><pkg:xmlData><a><b></a></b></pkg:xmlData></pkg:part>` +
		`<pkg:part pkg:name="/deep.xml" pkg:contentType="x"><pkg:xmlData>` + deep + `</pkg:xmlData></pkg:part>` +
		`<pkg:part pkg:name="/dtd.xml" pkg:contentType="x"><pkg:xmlData><a><!DOCTYPE a></a></pkg:xmlData></pkg:part>` +
		`<pkg:part pkg:name="/good.xml" pkg:contentType="x"><pkg:xmlData><a/></pkg:xmlData></pkg:part></pkg:package>`
	p = openFlat(t, bad, noLimits)
	if p.FlatDamage() != nil || len(p.Names()) != 4 {
		t.Errorf("damage %v, parts %v", p.FlatDamage(), p.Names())
	}
	// Nothing read: an error.
	for name, xml := range map[string]string{
		"cut in the first part": flatDoc[:strings.Index(flatDoc, "</pkg:part>")],
		"empty":                 "",
		"no package":            `<?xml version="1.0"?>`,
		"a document type":       `<!DOCTYPE pkg:package [<!ENTITY a "b">]><pkg:package ` + pkgNS + `/>`,
	} {
		if _, err := OpenFlat(strings.NewReader(xml), int64(len(xml)), noLimits); !errors.Is(err, ErrDamaged) {
			t.Errorf("%s: %v", name, err)
		}
	}
	other := `<root xmlns="urn:x"/>`
	if _, err := OpenFlat(strings.NewReader(other), int64(len(other)), noLimits); !errors.Is(err, ErrNotPackage) {
		t.Errorf("other XML: %v", err)
	}
}

func TestFlatNamesAndLimits(t *testing.T) {
	part := func(name, text string) string {
		return `<pkg:part pkg:name="` + name + `" pkg:contentType="x"><pkg:xmlData><a>` + text + `</a></pkg:xmlData></pkg:part>`
	}
	xml := `<pkg:package ` + pkgNS + `>` + part("/Word/A%20B.xml", "1") + part("/word/a b.xml", "2") + part("/x&amp;y.xml", "3") +
		`<pkg:part pkg:name="/nodata.xml"/><other><pkg:part pkg:name="/inside.xml"><pkg:xmlData><a/></pkg:xmlData></pkg:part></other></pkg:package>`
	p := openFlat(t, xml, noLimits)
	if got := strings.Join(p.Names(), "|"); got != "word/a b.xml|x&y.xml" {
		t.Errorf("names: %s", got)
	}
	if p.Ambiguous() != 1 || readPart(t, p, "word/a b.xml") != "<a>1</a>" || p.Written("word/a b.xml") != "Word/A%20B.xml" {
		t.Errorf("ambiguous %d, first %q, written %q", p.Ambiguous(), readPart(t, p, "word/a b.xml"), p.Written("word/a b.xml"))
	}
	if _, err := OpenFlat(strings.NewReader(xml), int64(len(xml)), Limits{MaxDecodedBytes: 1 << 20, MaxMembers: 1}); !errors.Is(err, ErrTooManyParts) {
		t.Errorf("more parts than allowed: %v", err)
	}
}
