package opc

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

var noLimits = Limits{MaxDecodedBytes: math.MaxInt64}

func open(t *testing.T, data []byte, lim Limits) *Package {
	t.Helper()
	p, err := Open(bytes.NewReader(data), int64(len(data)), lim)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCanonical(t *testing.T) {
	for in, want := range map[string]string{
		"word/document.xml":     "word/document.xml",
		"/word/document.xml":    "word/document.xml",
		"Word/Document.XML":     "word/document.xml",
		`word\media\image1.png`: "word/media/image1.png",
		"word/my%20file.xml":    "word/my file.xml",
		"word/100%.xml":         "word/100%.xml",
		"word/caf%C3%A9.xml":    "word/café.xml",
		"[Content_Types].xml":   "[content_types].xml",
		"":                      "",
		"//word/x.xml":          "word/x.xml",
		"word/été.xml":          "word/été.xml",
	} {
		if got := Canonical(in); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolve(t *testing.T) {
	for _, c := range []struct{ source, target, want string }{
		{"", "word/document.xml", "word/document.xml"},
		{"", "/word/document.xml", "word/document.xml"},
		{"word/document.xml", "styles.xml", "word/styles.xml"},
		{"word/document.xml", "media/image1.png", "word/media/image1.png"},
		{"word/document.xml", "../customXml/item1.xml", "customxml/item1.xml"},
		{"word/document.xml", "/word/numbering.xml", "word/numbering.xml"},
		{"ppt/slides/slide1.xml", "../slideLayouts/slideLayout1.xml", "ppt/slidelayouts/slidelayout1.xml"},
		{"ppt/slides/slide1.xml", "../media/image1.png#frag", "ppt/media/image1.png"},
		{"xl/workbook.xml", `worksheets\sheet1.xml`, "xl/worksheets/sheet1.xml"},
		{"xl/workbook.xml", "./worksheets/./sheet1.xml", "xl/worksheets/sheet1.xml"},
		// A target cannot leave the package.
		{"word/document.xml", "../../../etc/passwd", "etc/passwd"},
		{"", "../x.xml", "x.xml"},
		{"word/document.xml", "..", ""},
	} {
		if got := Resolve(c.source, c.target); got != c.want {
			t.Errorf("Resolve(%q, %q) = %q, want %q", c.source, c.target, got, c.want)
		}
	}
	if RelsName("") != "_rels/.rels" || RelsName("word/document.xml") != "word/_rels/document.xml.rels" {
		t.Error("RelsName is wrong")
	}
}

func TestContentTypesAndRels(t *testing.T) {
	b := ooxmltest.Docx(ooxmltest.Para("x")).WithStyles("")
	b.AddBytes("word/media/image1.png", "image/png", []byte("png"))
	link := b.ExternalRel("word/document.xml", ooxmltest.RelHyperlink, "https://example.org/a b?x=1&y=2")
	p := open(t, b.Bytes(), noLimits)

	if got := p.ContentType("word/document.xml"); got != ooxmltest.CTDocument {
		t.Errorf("override: %q", got)
	}
	if got := p.ContentType("/Word/Media/IMAGE1.PNG"); got != "image/png" {
		t.Errorf("default by extension, any case: %q", got)
	}
	if got := p.ContentType("word/unknown.bin"); got != "" {
		t.Errorf("unknown extension: %q", got)
	}
	if !p.Has("WORD/styles.xml") || p.Has("word/nope.xml") {
		t.Error("Has is wrong")
	}
	if !p.HasContentType(ooxmltest.CTStyles) || !p.HasContentType("image/png") || p.HasContentType(ooxmltest.CTWorkbook) {
		t.Error("HasContentType is wrong")
	}

	rels, err := p.Rels("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	want := []Rel{
		{ID: "rId1", Type: ooxmltest.RelStyles, Target: "word/styles.xml"},
		{ID: link, Type: ooxmltest.RelHyperlink, Target: "https://example.org/a b?x=1&y=2", External: true},
	}
	if !reflect.DeepEqual(rels, want) {
		t.Errorf("rels = %+v", rels)
	}
	if none, err := p.Rels("word/styles.xml"); none != nil || err != nil {
		t.Errorf("part without relationships: %v, %v", none, err)
	}
	if _, err := p.Open("word/nope.xml"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing part: %v", err)
	}
}

func TestMain(t *testing.T) {
	const word = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	cases := []struct {
		name   string
		build  func() *ooxmltest.Package
		part   string
		format Format
		mime   string
		err    error
	}{
		{"word", func() *ooxmltest.Package { return ooxmltest.Docx("") }, "word/document.xml", Docx, word, nil},
		{"excel", func() *ooxmltest.Package { return ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S"}) }, "xl/workbook.xml", Xlsx,
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", nil},
		{"powerpoint", func() *ooxmltest.Package { return ooxmltest.Pptx(ooxmltest.Slide{}) }, "ppt/presentation.xml", Pptx,
			"application/vnd.openxmlformats-officedocument.presentationml.presentation", nil},
		{"macro-enabled word", func() *ooxmltest.Package {
			return ooxmltest.Docx("").Add("word/document.xml", "application/vnd.ms-word.document.macroEnabled.main+xml", "<x/>")
		}, "word/document.xml", Docx, "application/vnd.ms-word.document.macroEnabled.12", nil},
		{"powerpoint show", func() *ooxmltest.Package {
			return ooxmltest.Pptx().Add("ppt/presentation.xml", "application/vnd.openxmlformats-officedocument.presentationml.slideshow.main+xml", "<x/>")
		}, "ppt/presentation.xml", Pptx, "application/vnd.openxmlformats-officedocument.presentationml.slideshow", nil},
		{"main part in an unusual place", func() *ooxmltest.Package {
			p := ooxmltest.NewPackage()
			p.Rel("", ooxmltest.RelOfficeDoc, "/content/main.xml")
			return p.Add("content/main.xml", ooxmltest.CTWorkbook, "<x/>")
		}, "content/main.xml", Xlsx, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", nil},
		{"strict relationship type", func() *ooxmltest.Package {
			p := ooxmltest.NewPackage()
			p.Rel("", "http://purl.oclc.org/ooxml/officeDocument/relationships/officeDocument", "xl/workbook.xml")
			return p.Add("xl/workbook.xml", ooxmltest.CTWorkbook, "<x/>")
		}, "xl/workbook.xml", Xlsx, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", nil},
		{"no package relationships", func() *ooxmltest.Package {
			return ooxmltest.NewPackage().Add("word/document.xml", ooxmltest.CTDocument, "<x/>")
		}, "word/document.xml", Docx, word, nil},
		{"relationship right, content type generic", func() *ooxmltest.Package {
			p := ooxmltest.NewPackage()
			p.Rel("", ooxmltest.RelOfficeDoc, "word/document.xml")
			return p.Add("word/document.xml", "application/xml", "<x/>")
		}, "word/document.xml", Docx, word, nil},
		{"relationship to a missing part", func() *ooxmltest.Package {
			p := ooxmltest.NewPackage()
			p.Rel("", ooxmltest.RelOfficeDoc, "nowhere.xml")
			return p.Add("ppt/presentation.xml", ooxmltest.CTPresentation, "<x/>")
		}, "ppt/presentation.xml", Pptx, "application/vnd.openxmlformats-officedocument.presentationml.presentation", nil},
		{"some other package", func() *ooxmltest.Package {
			p := ooxmltest.NewPackage()
			p.Rel("", ooxmltest.RelOfficeDoc, "visio/document.xml")
			return p.Add("visio/document.xml", "application/vnd.ms-visio.drawing.main+xml", "<x/>")
		}, "", "", "", ErrNoMainPart},
		{"theme only", func() *ooxmltest.Package {
			return ooxmltest.NewPackage().Add("theme/theme1.xml", ooxmltest.CTTheme, "<x/>")
		}, "", "", "", ErrNoMainPart},
	}
	for _, c := range cases {
		p := open(t, c.build().Bytes(), noLimits)
		part, format, mime, err := p.Main()
		if part != c.part || format != c.format || mime != c.mime || !errors.Is(err, c.err) {
			t.Errorf("%s: got %q %q %q %v", c.name, part, format, mime, err)
		}
	}
}

// rawZip builds an archive member by member, for cases the package builder
// does not produce.
func rawZip(t testing.TB, add func(w *zip.Writer)) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	add(w)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func member(t *testing.T, w *zip.Writer, name, content string) {
	t.Helper()
	f, err := w.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(f, content)
}

const types = `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="XML" ContentType="application/xml"/></Types>`

func TestOpenRejects(t *testing.T) {
	if _, err := Open(bytes.NewReader([]byte("not a zip")), 9, noLimits); !errors.Is(err, ErrNotZip) {
		t.Errorf("garbage: %v", err)
	}
	plain := rawZip(t, func(w *zip.Writer) { member(t, w, "mimetype", "application/vnd.oasis.opendocument.text") })
	if _, err := Open(bytes.NewReader(plain), int64(len(plain)), noLimits); !errors.Is(err, ErrNotPackage) {
		t.Errorf("zip without content types: %v", err)
	}
	broken := rawZip(t, func(w *zip.Writer) { member(t, w, "[Content_Types].xml", "<Types><Default") })
	if _, err := Open(bytes.NewReader(broken), int64(len(broken)), noLimits); !errors.Is(err, ErrDamaged) {
		t.Errorf("unreadable content types: %v", err)
	}
	many := rawZip(t, func(w *zip.Writer) {
		member(t, w, "[Content_Types].xml", types)
		for i := range 50 {
			member(t, w, strings.Repeat("d/", i)+"x.xml", "<x/>")
		}
	})
	if _, err := Open(bytes.NewReader(many), int64(len(many)), Limits{MaxDecodedBytes: math.MaxInt64, MaxMembers: 50}); !errors.Is(err, ErrTooManyParts) {
		t.Errorf("51 members with a limit of 50: %v", err)
	}
	if _, err := Open(bytes.NewReader(many), int64(len(many)), Limits{MaxDecodedBytes: math.MaxInt64, MaxMembers: 51}); err != nil {
		t.Errorf("51 members with a limit of 51: %v", err)
	}
}

func TestNamesAreMatchedLoosely(t *testing.T) {
	data := rawZip(t, func(w *zip.Writer) {
		member(t, w, "[Content_Types].xml", types)
		member(t, w, `Word\Document.xml`, "first")
		member(t, w, "word/document.xml", "second") // same part name: the first one counts
		member(t, w, "word/", "")                   // a directory entry is not a part
		member(t, w, "_rels/.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="r1" Type="x/officeDocument" Target="WORD/DOCUMENT.XML"/></Relationships>`)
	})
	p := open(t, data, noLimits)
	if got := p.Names(); !reflect.DeepEqual(got, []string{"[content_types].xml", "word/document.xml", "_rels/.rels"}) {
		t.Errorf("names = %v", got)
	}
	rc, err := p.Open("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	content, _ := io.ReadAll(rc)
	rc.Close()
	if string(content) != "first" {
		t.Errorf("duplicate name resolved to %q", content)
	}
	if got := p.ContentType("word/document.xml"); got != "application/xml" {
		t.Errorf("default with upper-case extension: %q", got)
	}
	rels, _ := p.Rels("")
	if len(rels) != 1 || rels[0].Target != "word/document.xml" {
		t.Errorf("rels = %+v", rels)
	}
}

func TestBudget(t *testing.T) {
	big := strings.Repeat("a", 1<<20)
	data := rawZip(t, func(w *zip.Writer) {
		member(t, w, "[Content_Types].xml", types)
		member(t, w, "a.xml", big)
		member(t, w, "b.xml", big)
	})
	if len(data) > 64<<10 {
		t.Fatalf("test archive is %d bytes; it should compress to almost nothing", len(data))
	}
	read := func(p *Package, name string) (int, error) {
		rc, err := p.Open(name)
		if err != nil {
			return 0, err
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		return len(b), err
	}
	// The budget is shared by all parts of the package.
	p := open(t, data, Limits{MaxDecodedBytes: int64(len(types)) + 1<<20 + 512<<10})
	if n, err := read(p, "a.xml"); n != 1<<20 || err != nil {
		t.Fatalf("first part: %d bytes, %v", n, err)
	}
	if n, err := read(p, "b.xml"); !errors.Is(err, ErrBudget) || n > 512<<10 {
		t.Errorf("second part: %d bytes, %v; want ErrBudget after at most the remaining budget", n, err)
	}
	if p.Remaining() != 0 {
		t.Errorf("Remaining = %d", p.Remaining())
	}
	// The content-types table is charged too.
	if _, err := Open(bytes.NewReader(data), int64(len(data)), Limits{MaxDecodedBytes: 10}); !errors.Is(err, ErrBudget) {
		t.Errorf("content types over budget: %v", err)
	}
	// The declared size is not trusted, only reported.
	if open(t, data, noLimits).Size("a.xml") != 1<<20 || open(t, data, noLimits).Size("zzz") != -1 {
		t.Error("Size is wrong")
	}
}

func TestEncryptedMemberIsRefused(t *testing.T) {
	data := rawZip(t, func(w *zip.Writer) {
		member(t, w, "[Content_Types].xml", types)
		f, err := w.CreateHeader(&zip.FileHeader{Name: "secret.xml", Flags: 1, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(f, "ciphertext")
	})
	p := open(t, data, noLimits)
	if _, err := p.Open("secret.xml"); !errors.Is(err, ErrEncryptedMember) {
		t.Errorf("encrypted member: %v", err)
	}
}
