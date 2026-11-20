package ooxmltest

import (
	"archive/zip"
	"bytes"
	"io"
	"math"
	"runtime"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

func members(t *testing.T, pkg []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(pkg), int64(len(pkg)))
	if err != nil {
		t.Fatalf("builder output is not a zip archive: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = string(data)
	}
	return out
}

func TestBuilderLayout(t *testing.T) {
	cases := map[string]struct {
		pkg   *Package
		parts []string
		main  string
	}{
		"docx": {Docx(Para("Hello")).WithStyles(""), []string{"word/document.xml", "word/styles.xml", "word/_rels/document.xml.rels"}, CTDocument},
		"xlsx": {Xlsx(Sheet{Name: "A"}, Sheet{Name: "B", State: "hidden"}).WithSharedStrings("x"),
			[]string{"xl/workbook.xml", "xl/worksheets/sheet1.xml", "xl/worksheets/sheet2.xml", "xl/sharedStrings.xml", "xl/_rels/workbook.xml.rels"}, CTWorkbook},
		"pptx": {Pptx(Slide{Shapes: TextShape(2, 10, 10, 100, 20, "Hi")}, Slide{Hidden: true}),
			[]string{"ppt/presentation.xml", "ppt/slides/slide1.xml", "ppt/slides/slide2.xml", "ppt/slides/_rels/slide1.xml.rels",
				"ppt/slideMasters/slideMaster1.xml", "ppt/slideLayouts/slideLayout1.xml", "ppt/theme/theme1.xml"}, CTPresentation},
	}
	for name, c := range cases {
		m := members(t, c.pkg.Bytes())
		for _, part := range append(c.parts, "[Content_Types].xml", "_rels/.rels") {
			if _, ok := m[part]; !ok {
				t.Errorf("%s: package lacks %s", name, part)
			}
		}
		if !strings.Contains(m["[Content_Types].xml"], c.main) {
			t.Errorf("%s: content types do not declare the main part", name)
		}
		for part, data := range m {
			if strings.HasSuffix(part, ".xml") || strings.HasSuffix(part, ".rels") {
				if !strings.HasPrefix(data, "<?xml") || strings.Count(data, "<?xml") != 1 {
					t.Errorf("%s: %s does not start with exactly one XML declaration", name, part)
				}
			}
		}
	}
}

func TestBuilderIsDeterministicAndReplacesParts(t *testing.T) {
	build := func() []byte {
		return Docx(Para("one")).Add("word/document.xml", CTDocument, "<x/>").Bytes()
	}
	a, b := build(), build()
	if !bytes.Equal(a, b) {
		t.Error("the same calls gave different bytes")
	}
	m := members(t, a)
	if !strings.HasSuffix(m["word/document.xml"], "<x/>") {
		t.Errorf("Add did not replace the part: %q", m["word/document.xml"])
	}
	if got := EscapeXML(`a<b>&"c"`); got != "a&lt;b&gt;&amp;&quot;c&quot;" {
		t.Errorf("EscapeXML = %q", got)
	}
}

func TestBuilderRelationships(t *testing.T) {
	p := Docx(Para("x"))
	id := p.ExternalRel("word/document.xml", RelHyperlink, "https://example.org/?a=1&b=2")
	rels := members(t, p.Bytes())["word/_rels/document.xml.rels"]
	if id != "rId1" || !strings.Contains(rels, `Target="https://example.org/?a=1&amp;b=2" TargetMode="External"`) {
		t.Errorf("id %s, rels %s", id, rels)
	}
}

// The builder's packages must be real files: other programs open them and
// read the text back. This is the builder's own acceptance test.
func TestBuilderOutputOpensElsewhere(t *testing.T) {
	const marker = "Sphinx of black quartz"
	docx := Docx(Para(marker+" one") + Para(marker+" two")).Bytes()
	xlsx := Xlsx(Sheet{Name: "First", Data: `<row r="1">` + InlineCell("A1", marker) + `<c r="B1"><v>42</v></c></row>`}).Bytes()
	pptx := Pptx(Slide{Shapes: TextShape(2, 72, 72, 300, 40, marker)}).Bytes()

	t.Run("LibreOffice reads docx", func(t *testing.T) {
		if text := LibreOfficeText(t, docx); !strings.Contains(text, marker+" one") || !strings.Contains(text, marker+" two") {
			t.Errorf("text = %q", text)
		}
	})
	t.Run("LibreOffice reads xlsx", func(t *testing.T) {
		rows := LibreOfficeCSV(t, xlsx)["First"]
		if len(rows) != 1 || len(rows[0]) != 2 || rows[0][0] != marker || rows[0][1] != "42" {
			t.Errorf("sheet = %v", rows)
		}
	})
	t.Run("pandoc reads docx", func(t *testing.T) {
		if text := Pandoc(t, docx, "plain"); !strings.Contains(text, marker) {
			t.Errorf("text = %q", text)
		}
	})
	for tool, in := range map[string]struct {
		data []byte
		ext  string
	}{
		"python-docx": {docx, "docx"}, "docx2txt": {docx, "docx"},
		"openpyxl": {xlsx, "xlsx"}, "openpyxl-readonly": {xlsx, "xlsx"},
		"python-pptx": {pptx, "pptx"},
	} {
		t.Run(tool+" reads "+in.ext, func(t *testing.T) {
			res := Python(t, tool, in.data, in.ext)
			if res.Error != "" || !strings.Contains(res.Text, marker) {
				t.Errorf("%s %s: error %q, text %q", res.Tool, res.Version, res.Error, res.Text)
			}
		})
	}
}

func TestCompare(t *testing.T) {
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

	if s := Compare("the quick brown fox", "the quick brown fox"); s.F1 != 1 || s.Order != 1 || s.Precision != 1 || s.Recall != 1 {
		t.Errorf("identical text: %+v", s)
	}
	if s := Compare("", ""); s.F1 != 1 || s.Order != 1 {
		t.Errorf("two empty texts: %+v", s)
	}
	if s := Compare("", "something"); s.F1 != 0 || s.Recall != 0 {
		t.Errorf("nothing extracted: %+v", s)
	}
	// 3 of 4 extracted words are right, 3 of 5 reference words were found.
	s := Compare("a b c x", "a b c d e")
	if !near(s.Precision, 0.75) || !near(s.Recall, 0.6) || !near(s.F1, 2*0.75*0.6/1.35) || !near(s.Order, 0.6) {
		t.Errorf("hand-made pair: %+v", s)
	}
	// Same words, reversed: full marks for content, poor marks for order.
	if s := Compare("d c b a", "a b c d"); s.F1 != 1 || !near(s.Order, 0.25) {
		t.Errorf("reversed: %+v", s)
	}
	// A word extracted twice costs precision.
	if s := Compare("a a b", "a b"); !near(s.Precision, 2.0/3) || s.Recall != 1 {
		t.Errorf("duplicate: %+v", s)
	}
	// Whitespace of any kind separates words; ligatures are expanded; case counts.
	if s := Compare("of\uFB01ce\tfile\u00A0name\n", "office file name"); s.F1 != 1 {
		t.Errorf("normalisation: %+v", s)
	}
	if s := Compare("Word", "word"); s.F1 != 0 {
		t.Errorf("case must count: %+v", s)
	}
	// Layout marks are not words; punctuation attached to a word is.
	if s := Compare("# Title\n\n- one\n- two\n\n| a | b |\n| --- | --- |", "Title one two a b"); s.F1 != 1 || s.Got != 5 {
		t.Errorf("layout marks: %+v", s)
	}
	if s := Compare("end.", "end"); s.F1 != 0 {
		t.Errorf("attached punctuation must count: %+v", s)
	}
}

func TestOrderScoreOnLongText(t *testing.T) {
	words := make([]string, 3*lcsLimit)
	for i := range words {
		words[i] = string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)%26))
	}
	text := strings.Join(words, " ")
	if s := Compare(text, text); s.Order != 1 {
		t.Errorf("long identical text has order %v", s.Order)
	}
}

func TestCellAgreement(t *testing.T) {
	a := [][]string{{"x", "y"}, {"1", "2"}}
	if got := CellAgreement(a, a); got != 1 {
		t.Errorf("identical grids: %v", got)
	}
	if got := CellAgreement(nil, nil); got != 1 {
		t.Errorf("empty grids: %v", got)
	}
	if got := CellAgreement(a, [][]string{{"x", "y"}, {"1", "3"}}); got != 0.75 {
		t.Errorf("one of four differs: %v", got)
	}
	// Trailing empty cells and rows do not count; a missing value does.
	if got := CellAgreement(a, [][]string{{"x", "y", ""}, {" 1 ", "2"}, {""}}); got != 1 {
		t.Errorf("padding should not matter: %v", got)
	}
	if got := CellAgreement(a, [][]string{{"x", "y"}}); got != 0.5 {
		t.Errorf("missing row: %v", got)
	}
}

// recorder stands in for *testing.T so that a check can be seen to fail.
type recorder struct {
	testing.TB
	failed bool
}

func (r *recorder) Helper()               {}
func (r *recorder) Errorf(string, ...any) { r.failed = true }
func (r *recorder) Fatalf(string, ...any) { r.failed = true; runtime.Goexit() }
func (r *recorder) Fatal(...any)          { r.failed = true; runtime.Goexit() }
func (r *recorder) Skipf(string, ...any)  { runtime.Goexit() }
func (r *recorder) Skip(...any)           { runtime.Goexit() }
func rejects(check func(testing.TB)) (failed bool) {
	r := &recorder{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		check(r)
	}()
	<-done
	return r.failed
}

func goodDocument() *gofastcore.Document {
	d := gofastcore.New("docx")
	p := gofastcore.Part{Kind: gofastcore.PartBody, Number: 1, Blocks: []gofastcore.Block{
		{Kind: gofastcore.KindHeading, Level: 1, Text: "Title"},
		{Kind: gofastcore.KindParagraph, Text: "Body text."},
	}}
	p.Finish()
	d.Parts = append(d.Parts, p)
	return d
}

func TestCheckDocument(t *testing.T) {
	CheckDocument(t, goodDocument())

	empty := goodDocument()
	empty.Parts[0].Blocks = []gofastcore.Block{}
	empty.Parts[0].Warn(gofastcore.SeverityInfo, gofastcore.WarnPartNoText, "")
	CheckDocument(t, empty)

	bad := map[string]func(*gofastcore.Document){
		"invalid document":           func(d *gofastcore.Document) { d.Parts[0].Blocks[0].Level = 0 },
		"wrong format":               func(d *gofastcore.Document) { d.Source.Format = "pdf" },
		"textless part, no warning":  func(d *gofastcore.Document) { d.Parts[0].Blocks = []gofastcore.Block{} },
		"text and a no-text warning": func(d *gofastcore.Document) { d.Parts[0].Warn(gofastcore.SeverityInfo, gofastcore.WarnPartNoText, "") },
		"two textless warnings": func(d *gofastcore.Document) {
			d.Parts[0].Blocks = []gofastcore.Block{}
			d.Parts[0].Warn(gofastcore.SeverityInfo, gofastcore.WarnPartNoText, "")
			d.Parts[0].Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "")
		},
	}
	for name, mutate := range bad {
		d := goodDocument()
		mutate(d)
		if !rejects(func(tb testing.TB) { CheckDocument(tb, d) }) {
			t.Errorf("%s: CheckDocument accepted it", name)
		}
	}
	if !rejects(func(tb testing.TB) { CheckDocument(tb, nil) }) {
		t.Error("nil document accepted")
	}
}
