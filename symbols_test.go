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

	// A string of runs is in the fonts of its runs: a run that names a
	// font is in that font, and one that names none in the cell's. A
	// check mark in Wingdings before words in Arial is common in published
	// workbooks; the cell's font is then often the symbol font, and its
	// words must not be read as codes of it.
	t.Run("Excel runs", func(t *testing.T) {
		const arial = `<rPr><rFont val="Arial"/></rPr>`
		const wingdings = `<rPr><rFont val="Wingdings"/></rPr>`
		sst := `<sst xmlns="` + ooxmltest.NSSheet + `">` +
			`<si><r><t>ü</t></r><r>` + arial + `<t xml:space="preserve"> Done</t></r></si>` + // cell in Wingdings, words in Arial
			`<si><r>` + wingdings + `<t>ü</t></r><r><t xml:space="preserve"> Done</t></r></si>` + // cell in Arial, mark in Wingdings
			`<si><r>` + arial + `<t>Done</t></r></si>` + // cell in Wingdings, every run in Arial
			`<si><r><t>ü</t></r><r><t>û</t></r></si>` + // no run names a font: the cell's
			`</sst>`
		pkg := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: xlRow(1,
			`<c r="A1" s="1" t="s"><v>0</v></c>`, `<c r="B1" t="s"><v>1</v></c>`,
			`<c r="C1" s="1" t="s"><v>2</v></c>`, `<c r="D1" s="1" t="s"><v>3</v></c>`,
			`<c r="E1" t="s"><v>0</v></c>`)}).
			WithSheetStyles(xlStyles(nil, []string{`<sz val="11"/><name val="Wingdings"/>`}, nil, `numFmtId="0"`, `numFmtId="0" fontId="1"`))
		pkg.Rel("xl/workbook.xml", ooxmltest.RelSharedStrings, "sharedStrings.xml")
		pkg.Add("xl/sharedStrings.xml", ooxmltest.CTSharedStrings, sst)
		doc := excel(t, pkg, nil)
		// The last cell is in the default font: its first run, which
		// names none, is not translated.
		expectTexts(t, doc, "✓ Done", "✓ Done", "Done", "✓🗶", "ü Done")

		inline := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: xlRow(1,
			`<c r="A1" s="1" t="inlineStr"><is><r><t>ü</t></r><r>`+arial+`<t xml:space="preserve"> Done</t></r></is></c>`,
			`<c r="B1" t="inlineStr"><is><r>`+wingdings+`<t>ü</t></r><r><t xml:space="preserve"> Done</t></r></is></c>`)}).
			WithSheetStyles(xlStyles(nil, []string{`<sz val="11"/><name val="Wingdings"/>`}, nil, `numFmtId="0"`, `numFmtId="0" fontId="1"`))
		expectTexts(t, excel(t, inline, nil), "✓ Done", "✓ Done")
	})

	// The same in a binary workbook, whose runs name fonts by number.
	t.Run("Excel binary runs", func(t *testing.T) {
		rich := func(s string, runs ...uint16) []byte {
			f := ooxmltest.Join(ooxmltest.U8(1), ooxmltest.WStr(s), ooxmltest.U32(uint32(len(runs)/2)))
			for _, r := range runs {
				f = append(f, ooxmltest.U16(r)...)
			}
			return f
		}
		pkg := ooxmltest.Xlsb(&ooxmltest.XlsbStyles{Fonts: []string{"Arial", "Wingdings"}, XFs: [][2]uint16{{0, 0}, {0, 1}}}, nil, nil,
			ooxmltest.XlsbSheet{Name: "S", Data: ooxmltest.Join(
				ooxmltest.Row(0, 0, 300),
				ooxmltest.Cell(ooxmltest.XCellIsst, 0, 1, ooxmltest.U32(0)),           // Wingdings cell: mark, then Arial words
				ooxmltest.Cell(ooxmltest.XCellIsst, 1, 0, ooxmltest.U32(0)),           // Arial cell, the same string
				ooxmltest.Cell(ooxmltest.XCellIsst, 2, 1, ooxmltest.U32(1)),           // Wingdings cell, all of it in Arial
				ooxmltest.Cell(ooxmltest.XCellIsst, 3, 1, ooxmltest.U32(2)),           // no runs: the cell's font
				ooxmltest.Cell(ooxmltest.XCellRStr, 4, 1, rich("ü Done", 0, 1, 1, 0)), // a rich string in the cell
			)})
		sst := ooxmltest.Join(ooxmltest.Rec(159, ooxmltest.U32(3), ooxmltest.U32(3)),
			ooxmltest.Rec(19, rich("ü Done", 0, 1, 1, 0)), ooxmltest.Rec(19, rich("Done", 0, 0)),
			ooxmltest.Rec(19, ooxmltest.RStr("üû")), ooxmltest.Rec(160))
		pkg.Rel("xl/workbook.bin", ooxmltest.RelSharedStrings, "sharedStrings.bin")
		pkg.AddBytes("xl/sharedStrings.bin", ooxmltest.CTXlsbStrings, sst)
		expectTexts(t, excel(t, pkg, nil), "✓ Done", "✓ Done", "Done", "✓🗶", "✓ Done")
	})
}
