package gofastooxml_test

import (
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// texts returns the text of every block, and of every cell of every
// table, in order.
func texts(doc *gofastcore.Document) []string {
	var out []string
	for _, b := range doc.AllBlocks() {
		if b.Table != nil {
			for _, row := range b.Table.Rows {
				out = append(out, row...)
			}
			continue
		}
		if b.Marker != "" {
			out = append(out, b.Marker+" "+b.Text)
			continue
		}
		out = append(out, b.Text)
	}
	return out
}

func expectTexts(t *testing.T, doc *gofastcore.Document, want ...string) {
	t.Helper()
	if got := texts(doc); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n  %q\nwant\n  %q", got, want)
	}
}

// Text typed in a symbol font is codes of the font, whichever way the font
// is given: "J" in Wingdings is a smiling face, "ü" a check mark, "a" in
// Symbol an alpha. A code the font has no character for in Unicode is
// U+FFFD and is reported.
func TestTextInSymbolFonts(t *testing.T) {
	t.Run("Word", func(t *testing.T) {
		wingdings := `<w:rPr><w:rFonts w:ascii="Wingdings" w:hAnsi="Wingdings"/></w:rPr>`
		body := para(run(text("Done "))+run(wingdings+text("üJ"))) +
			// By the run's character style, and by the paragraph's style.
			para(run(`<w:rPr><w:rStyle w:val="Checks"/></w:rPr>`+text("ü"))+run(text(" ok"))) +
			`<w:p><w:pPr><w:pStyle w:val="Greek"/></w:pPr>` + run(text("abg")) + `</w:p>` +
			// A run that names an ordinary font in a paragraph whose style
			// is a symbol font is ordinary.
			`<w:p><w:pPr><w:pStyle w:val="Greek"/></w:pPr>` + run(`<w:rPr><w:rFonts w:ascii="Arial" w:hAnsi="Arial"/></w:rPr>`+text("abg")) + `</w:p>` +
			// The private range, and a code with no character.
			para(run(wingdings+text("\uf04a\uf0ff")))
		styles := `<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style>` +
			`<w:style w:type="character" w:styleId="Checks"><w:name w:val="Checks"/><w:rPr><w:rFonts w:ascii="Wingdings" w:hAnsi="Wingdings"/></w:rPr></w:style>` +
			`<w:style w:type="paragraph" w:styleId="Greek"><w:name w:val="Greek"/><w:rPr><w:rFonts w:ascii="Symbol" w:hAnsi="Symbol"/></w:rPr></w:style>`
		doc := word(t, ooxmltest.Docx(body).WithStyles(styles), nil)
		expectTexts(t, doc, "Done ✓☺", "✓ ok", "αβγ", "abg", "☺\uFFFD")
		if !partWarned(doc, gofastcore.WarnTextUnmappedGlyphs) {
			t.Error("a code with no character was not reported")
		}
	})

	t.Run("PowerPoint", func(t *testing.T) {
		pkg := deck(ooxmltest.Slide{Shapes: box(2, 10, 10, 400, 100, "",
			dpara("", plain("Done "), drun("", `<a:latin typeface="Wingdings"/>`, "üJ")),
			// A symbol in the private range takes the symbol font; the
			// letters around it keep theirs.
			dpara("", drun("", `<a:latin typeface="Arial"/><a:sym typeface="Wingdings 2"/>`, "ok \uf050")),
			dpara("", drun("", `<a:latin typeface="Wingdings"/>`, "\uf0ff")),
		)})
		doc := powerpoint(t, pkg, nil)
		expectTexts(t, doc, "Done ✓☺\nok 🗸\n\uFFFD")
		if !doc.Parts[0].Has(gofastcore.WarnTextUnmappedGlyphs) {
			t.Error("a code with no character was not reported")
		}
	})

	t.Run("Excel", func(t *testing.T) {
		pkg := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: xlRow(1, xlStr("A1", "Task"), xlStr("B1", "Done")) +
			xlRow(2, xlStr("A2", "Write"), xlStyledStr("B2", 1, "ü")) +
			xlRow(3, xlStr("A3", "Check"), xlStyledStr("B3", 1, "û")) +
			xlRow(4, xlStr("A4", "Ship"), `<c r="B4" s="1" t="s"><v>0</v></c>`)}).
			WithSharedStrings("þ").
			WithSheetStyles(xlStyles(nil, []string{`<sz val="11"/><name val="Wingdings"/>`}, nil, `numFmtId="0"`, `numFmtId="0" fontId="1"`))
		doc := excel(t, pkg, nil)
		expectTexts(t, doc, "Task", "Done", "Write", "✓", "Check", "🗶", "Ship", "🗹")
	})
}
