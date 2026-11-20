package gofastooxml_test

import (
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// Hidden text in Word documents: each test builds the smallest document
// that hides text one way, or that looks as if it does and does not.

// styled returns a run of text with the given run properties.
func styled(props, s string) string { return run(`<w:rPr>` + props + `</w:rPr>` + text(s)) }

const (
	vanish    = `<w:vanish/>`
	whiteText = `<w:color w:val="FFFFFF"/>`
)

// hiddenStyles has styles that hide text, at each layer where formatting
// can be set.
const hiddenStyles = `<w:docDefaults><w:rPrDefault><w:rPr><w:sz w:val="22"/></w:rPr></w:rPrDefault></w:docDefaults>` +
	`<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Secret"><w:name w:val="Secret"/><w:basedOn w:val="Normal"/><w:rPr><w:vanish/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="SecretChild"><w:name w:val="Secret Child"/><w:basedOn w:val="Secret"/></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Fine"><w:name w:val="Fine Print"/><w:rPr><w:sz w:val="2"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Banner"><w:name w:val="Banner"/><w:pPr><w:shd w:val="clear" w:color="auto" w:fill="1F3864"/></w:pPr><w:rPr><w:color w:val="FFFFFF"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Strong"><w:name w:val="Strong Para"/><w:rPr><w:b/></w:rPr></w:style>` +
	`<w:style w:type="character" w:styleId="Ghost"><w:name w:val="Ghost"/><w:rPr><w:vanish/></w:rPr></w:style>` +
	`<w:style w:type="character" w:styleId="GhostChild"><w:name w:val="Ghost Child"/><w:basedOn w:val="Ghost"/></w:style>` +
	`<w:style w:type="character" w:styleId="Emph"><w:name w:val="Emphasis"/><w:rPr><w:i/></w:rPr></w:style>`

func TestWordHiddenByFormatting(t *testing.T) {
	body := para(styled(vanish, "Hidden directly.")) +
		ooxmltest.StyledPara("Secret", "Hidden by its paragraph's style.") +
		ooxmltest.StyledPara("SecretChild", "Hidden by a style that style is based on.") +
		para(styled(`<w:rStyle w:val="Ghost"/>`, "Hidden by a character style.")) +
		para(styled(`<w:rStyle w:val="GhostChild"/>`, "Hidden by an inherited character style.")) +
		// The nearest setting wins: a run can switch off what its style sets.
		`<w:p><w:pPr><w:pStyle w:val="Secret"/></w:pPr>` + styled(`<w:vanish w:val="0"/>`, "Shown despite its style.") + `</w:p>` +
		para(styled(`<w:vanish w:val="false"/>`, "Plainly shown.")) +
		// Hidden by the character style and the paragraph's style both: the two cancel, as bold in a bold style does.
		`<w:p><w:pPr><w:pStyle w:val="Secret"/></w:pPr>` + styled(`<w:rStyle w:val="Ghost"/>`, "Hidden twice over, so shown.") + run(text(" Hidden once.")) + `</w:p>` +
		// Shown in print layout; hidden only in web layout, which is not what a reader of the file sees.
		para(styled(`<w:webHidden/>`, "Web-hidden only."))
	doc := word(t, ooxmltest.Docx(body).WithStyles(hiddenStyles), nil)
	expectBlocks(t, doc,
		`paragraph "Hidden directly." display=hidden`,
		`paragraph "Hidden by its paragraph's style." display=hidden`,
		`paragraph "Hidden by a style that style is based on." display=hidden`,
		`paragraph "Hidden by a character style." display=hidden`,
		`paragraph "Hidden by an inherited character style." display=hidden`,
		`paragraph "Shown despite its style."`,
		`paragraph "Plainly shown."`,
		`paragraph "Hidden twice over, so shown. Hidden once." display=hidden [" Hidden once." hidden]`,
		`paragraph "Web-hidden only."`,
	)
	ooxmltest.CheckDocument(t, doc)
	if w := doc.Parts[0].Warnings; len(w) != 0 {
		t.Errorf("warnings = %+v", w)
	}
}

func TestWordPartlyHiddenParagraphs(t *testing.T) {
	body := para(run(text("Visible one. "))+styled(vanish, "Hidden sentence. ")+run(text("Visible two."))) +
		// Two reasons, on different parts.
		para(run(text("See "))+styled(whiteText, "white")+run(text(" and "))+styled(`<w:sz w:val="2"/>`, "tiny")+run(text(" text."))) +
		// A hidden passage split over several runs is one range.
		para(run(text("A "))+styled(vanish, "hid")+styled(vanish+`<w:b/>`, "den")+styled(vanish, " word")+run(text(" here."))) +
		// Everything hidden, in pieces, with only spaces between: the whole block.
		para(styled(vanish, "All")+run(text(" "))+styled(vanish, "of it.")) +
		// Hidden for one reason throughout and for another in part.
		para(styled(vanish, "Hidden, and ")+styled(vanish+`<w:sz w:val="2"/>`, "tiny too")+styled(vanish, ".")) +
		para(run(text("Nothing hidden here.")))
	doc := word(t, ooxmltest.Docx(body), nil)
	expectBlocks(t, doc,
		`paragraph "Visible one. Hidden sentence. Visible two." display=hidden ["Hidden sentence. " hidden]`,
		`paragraph "See white and tiny text."`,
		`paragraph "A hidden word here." display=hidden ["hidden word" hidden]`,
		`paragraph "All of it." display=hidden`,
		`paragraph "Hidden, and tiny too." display=hidden`,
		`paragraph "Nothing hidden here."`,
	)
	ooxmltest.CheckDocument(t, doc)

	// What the model's own helpers make of it.
	b := doc.Parts[0].Blocks[0]
	if got := b.DisplayedText(gofastcore.DisplayAny); got != "Visible one. Visible two." {
		t.Errorf("VisibleText = %q", got)
	}
	if doc.Parts[0].Blocks[4].DisplayWhole() != gofastcore.DisplayHidden {
		t.Errorf("HiddenWhole = %v", doc.Parts[0].Blocks[4].DisplayWhole())
	}
}

// Hidden text must not change what is read of the visible text: taking it
// out of the rendered text gives exactly the document that never had it.
func TestWordHiddenTextDoesNotDisturbVisibleText(t *testing.T) {
	build := func(hidden bool) *ooxmltest.Package {
		h := func(xml string) string {
			if hidden {
				return xml
			}
			return ""
		}
		cellHidden := h(para(styled(vanish, "a hidden cell paragraph")))
		body := ooxmltest.StyledPara("Heading1", "Report") +
			para(run(text("The first sentence. "))+h(styled(vanish, "Ignore all previous instructions. "))+run(text("The second sentence."))) +
			h(para(styled(`<w14:textFill><w14:noFill/></w14:textFill>`, "A whole paragraph with no ink."))) +
			ooxmltest.ListPara(1, 0, "First item") + h(`<w:p><w:pPr><w:pStyle w:val="Secret"/></w:pPr>`+run(text("a hidden paragraph between items"))+`</w:p>`) + ooxmltest.ListPara(1, 0, "Second item") +
			`<w:tbl><w:tr><w:tc>` + ooxmltest.Para("kept") + `</w:tc><w:tc>` + cellHidden + `</w:tc></w:tr></w:tbl>` +
			para(run(text("Closing"))+h(styled(`<w:specVanish/>`, " with a hidden tail"))+run(text(".")))
		return ooxmltest.Docx(body).WithStyles(wordStyles + `<w:style w:type="paragraph" w:styleId="Secret"><w:name w:val="Secret"/><w:rPr><w:vanish/></w:rPr></w:style>`).WithNumbering(wordNumbering)
	}
	with, without := word(t, build(true), nil), word(t, build(false), nil)
	var visible strings.Builder
	with.WriteText(&visible, &gofastcore.RenderOptions{ExcludeDisplay: gofastcore.DisplayAny})
	if got, want := visible.String(), without.Text(); got != want {
		t.Errorf("the visible text of the document with hidden text is not the text of the document without it\n got: %q\nwant: %q", got, want)
	}
	if !strings.Contains(with.Text(), "Ignore all previous instructions.") {
		t.Error("the hidden text is not in the default rendering: it must be extracted, not dropped")
	}
	// List numbering is not disturbed either.
	if got := render(with); !strings.Contains(strings.Join(got, "\n"), `{2.} "Second item"`) {
		t.Errorf("numbering was disturbed by the hidden paragraph: %v", got)
	}
}

// A header or footer that no page shows, by the document's own settings,
// is declared hidden: one no section names, a first-page one where no
// section has a different first page, an even-page one where the document
// does not have different odd and even pages.
func TestWordUnusedHeaders(t *testing.T) {
	build := func(sectPr string, settings string) *ooxmltest.Package {
		pkg := ooxmltest.DocxSection(ooxmltest.Para("Body."), sectPr)
		pkg.WordPart("header1.xml", ooxmltest.CTHeader, ooxmltest.RelHeader, "hdr", ooxmltest.Para("Ordinary header"))           // rId1
		pkg.WordPart("header2.xml", ooxmltest.CTHeader, ooxmltest.RelHeader, "hdr", ooxmltest.Para("First-page header"))         // rId2
		pkg.WordPart("header3.xml", ooxmltest.CTHeader, ooxmltest.RelHeader, "hdr", ooxmltest.Para("Even-page header"))          // rId3
		pkg.WordPart("footer1.xml", ooxmltest.CTFooter, ooxmltest.RelFooter, "ftr", ooxmltest.Para("A footer no section names")) // rId4
		pkg.Rel("word/document.xml", ooxmltest.RelSettings, "settings.xml")
		pkg.Add("word/settings.xml", ooxmltest.CTSettings, `<w:settings`+ooxmltest.WordNS+`>`+settings+`</w:settings>`)
		return pkg
	}
	refs := `<w:headerReference w:type="default" r:id="rId1"/><w:headerReference w:type="first" r:id="rId2"/><w:headerReference w:type="even" r:id="rId3"/>`

	doc := word(t, build(refs, ""), nil)
	expectBlocks(t, doc,
		`header "Ordinary header"`,
		`header "First-page header" display=hidden`,
		`header "Even-page header" display=hidden`,
		`paragraph "Body."`,
		`footer "A footer no section names" display=hidden`,
	)
	ooxmltest.CheckDocument(t, doc)
	// With both switched on, all three headers are shown.
	doc = word(t, build(refs+`<w:titlePg/>`, `<w:evenAndOddHeaders/>`), nil)
	expectBlocks(t, doc,
		`header "Ordinary header"`,
		`header "First-page header"`,
		`header "Even-page header"`,
		`paragraph "Body."`,
		`footer "A footer no section names" display=hidden`,
	)
}

// A section that names no first-page header shows the one it inherits.
func TestWordFirstPageHeaderInherited(t *testing.T) {
	pkg := ooxmltest.DocxSection(
		`<w:p><w:pPr><w:sectPr><w:headerReference w:type="default" r:id="rId1"/><w:headerReference w:type="first" r:id="rId2"/></w:sectPr></w:pPr>`+run(text("Section one."))+`</w:p>`+
			ooxmltest.Para("Section two."),
		`<w:titlePg/>`)
	pkg.WordPart("header1.xml", ooxmltest.CTHeader, ooxmltest.RelHeader, "hdr", ooxmltest.Para("Ordinary header"))
	pkg.WordPart("header2.xml", ooxmltest.CTHeader, ooxmltest.RelHeader, "hdr", ooxmltest.Para("First-page header"))
	expectBlocks(t, word(t, pkg, nil),
		`header "Ordinary header"`,
		`header "First-page header"`,
		`paragraph "Section one."`,
		`paragraph "Section two."`,
	)
}

// Hidden text in the cells of a vertical merge is reported once, on the
// cell the merged text is in.
func TestWordHiddenTextInMergedCells(t *testing.T) {
	cell := func(merge, content string) string {
		return `<w:tc><w:tcPr><w:vMerge` + merge + `/></w:tcPr>` + content + `</w:tc>`
	}
	body := `<w:tbl>` +
		`<w:tr>` + cell(` w:val="restart"`, para(styled(vanish, "top"))) + `<w:tc>` + ooxmltest.Para("a") + `</w:tc></w:tr>` +
		`<w:tr>` + cell(``, para(styled(whiteText, "middle"))) + `<w:tc>` + ooxmltest.Para("b") + `</w:tc></w:tr>` +
		`<w:tr>` + cell(``, para(styled(vanish, "bottom"))) + `<w:tc>` + ooxmltest.Para("c") + `</w:tc></w:tr>` +
		`<w:tr>` + cell(` w:val="restart"`, ooxmltest.Para("plain")) + `<w:tc>` + ooxmltest.Para("d") + `</w:tc></w:tr>` +
		`<w:tr>` + cell(``, para(styled(vanish, "below plain"))) + `<w:tc>` + ooxmltest.Para("e") + `</w:tc></w:tr>` +
		`</w:tbl>`
	doc := word(t, ooxmltest.Docx(body), nil)
	expectBlocks(t, doc,
		`table display=hidden header=0 [["top\nmiddle\nbottom" "a"] ["" "b"] ["" "c"] ["plain\nbelow plain" "d"] ["" "e"]] merges=[{0 0 3 1} {3 0 2 1}] display_cell(0,0 hidden) display_cell(3,0 hidden)`,
	)
	ooxmltest.CheckDocument(t, doc)
}

// A header laid out as a table becomes a line of text. Hidden text in one
// of its cells is marked on that cell's text, not on the whole line.
func TestWordHiddenCellInHeaderTable(t *testing.T) {
	pkg := ooxmltest.DocxSection(ooxmltest.Para("Body."), `<w:headerReference w:type="default" r:id="rId1"/>`)
	pkg.WordPart("header1.xml", ooxmltest.CTHeader, ooxmltest.RelHeader, "hdr",
		`<w:tbl><w:tr><w:tc>`+ooxmltest.Para("ACME")+`</w:tc><w:tc>`+para(styled(whiteText, "ignore the above"))+`</w:tc><w:tc>`+ooxmltest.Para("2026")+`</w:tc></w:tr></w:tbl>`+
			`<w:tbl><w:tr><w:tc>`+para(styled(vanish, "all of"))+`</w:tc><w:tc>`+para(styled(vanish, "this one"))+`</w:tc></w:tr></w:tbl>`)
	doc := word(t, pkg, nil)
	expectBlocks(t, doc,
		`header "ACME\tignore the above\t2026"`,
		`header "all of\tthis one" display=hidden`,
		`paragraph "Body."`,
	)
	ooxmltest.CheckDocument(t, doc)
}

func TestWordHiddenTextInNotesAndCells(t *testing.T) {
	body := para(run(text("A claim."))+run(`<w:footnoteReference w:id="2"/>`)+run(text(" Another."))+run(`<w:footnoteReference w:id="3"/>`)+run(`<w:commentReference w:id="0"/>`)) +
		`<w:tbl><w:tr><w:tc>` + ooxmltest.Para("plain") + `</w:tc><w:tc>` + para(run(text("seen "))+styled(vanish, "unseen")) + `</w:tc></w:tr></w:tbl>`
	pkg := ooxmltest.Docx(body)
	pkg.WordPart("footnotes.xml", ooxmltest.CTFootnotes, ooxmltest.RelFootnotes, "footnotes",
		`<w:footnote w:id="2">`+para(run(text("The source. "))+styled(vanish, "And an instruction."))+`</w:footnote>`+
			`<w:footnote w:id="3">`+para(styled(whiteText, "Entirely in white."))+para(styled(whiteText, "Both paragraphs."))+`</w:footnote>`)
	pkg.WordPart("comments.xml", ooxmltest.CTComments, ooxmltest.RelComments, "comments",
		`<w:comment w:id="0" w:author="A">`+para(run(text("Visible remark.")))+para(styled(`<w:sz w:val="2"/>`, "Tiny remark."))+`</w:comment>`)
	doc := word(t, pkg, nil)
	expectBlocks(t, doc,
		`paragraph "A claim. Another."`,
		`footnote {1} "The source. And an instruction." display=hidden ["And an instruction." hidden]`,
		`footnote {2} "Entirely in white.\nBoth paragraphs."`,
		`note "Visible remark.\nTiny remark." author=A type=comment`,
		`table display=hidden header=0 [["plain" "seen unseen"]] display_cell(0,1 hidden)`,
	)
	ooxmltest.CheckDocument(t, doc)
}

func TestWordInlineFormattingAndLinks(t *testing.T) {
	pkg := ooxmltest.Docx("").WithStyles(hiddenStyles)
	link := pkg.ExternalRel("word/document.xml", ooxmltest.RelHyperlink, "https://example.org/page")
	body := para(run(text("Plain, "))+styled(`<w:b/>`, "bold")+run(text(", "))+styled(`<w:i/><w:u w:val="single"/>`, "italic underlined")+run(text(", "))+
		styled(`<w:strike/>`, "struck")+run(text(", x"))+styled(`<w:vertAlign w:val="superscript"/>`, "2")+run(text(" and H"))+styled(`<w:vertAlign w:val="subscript"/>`, "2")+run(text("O."))) +
		para(styled(`<w:rStyle w:val="Emph"/>`, "By a character style")+run(text(" and "))+styled(`<w:b w:val="0"/><w:u w:val="none"/>`, "switched off")+run(text("."))) +
		// Bold from the paragraph's style is not inline formatting.
		ooxmltest.StyledPara("Strong", "A paragraph bold by its style.") +
		para(run(text("See "))+`<w:hyperlink r:id="`+link+`">`+run(text("the "))+styled(`<w:b/>`, "site")+`</w:hyperlink>`+run(text(" or "))+
			`<w:hyperlink w:anchor="_Toc123">`+run(text("section 2"))+`</w:hyperlink>`+run(text("."))) +
		para(run(text("Or "))+run(`<w:fldChar w:fldCharType="begin"/>`)+run(`<w:instrText xml:space="preserve"> HYPERLINK "https://example.org/field" \o "tip" </w:instrText>`)+
			run(`<w:fldChar w:fldCharType="separate"/>`)+run(text("a field link"))+run(`<w:fldChar w:fldCharType="end"/>`)+run(text(" then plain."))) +
		para(run(text("Or "))+`<w:fldSimple w:instr=' HYPERLINK \l "Annex" '>`+run(text("a simple field"))+`</w:fldSimple>`+run(text("."))) +
		para(styled(`<w:b/>`+vanish, "Bold and hidden."))
	pkg.Add("word/document.xml", ooxmltest.CTDocument, `<w:document`+ooxmltest.WordNS+`><w:body>`+body+`</w:body></w:document>`)

	doc := word(t, pkg, &gofastcore.Options{Spans: true})
	expectBlocks(t, doc,
		`paragraph "Plain, bold, italic underlined, struck, x2 and H2O." ["bold" bold] ["italic underlined" italic+underline] ["struck" strike] ["2" super] ["2" sub]`,
		`paragraph "By a character style and switched off." ["By a character style" italic]`,
		`paragraph "A paragraph bold by its style."`,
		`paragraph "See the site or section 2." ["the " ->https://example.org/page] ["site" bold ->https://example.org/page] ["section 2" ->#_Toc123]`,
		`paragraph "Or a field link then plain." ["a field link" ->https://example.org/field]`,
		`paragraph "Or a simple field." ["a simple field" ->#Annex]`,
		`paragraph "Bold and hidden." display=hidden ["Bold and hidden." bold]`,
	)
	ooxmltest.CheckDocument(t, doc)

	// Without the option there is no formatting and there are no links,
	// but what is hidden is still marked.
	plain := word(t, pkg, nil)
	expectBlocks(t, plain,
		`paragraph "Plain, bold, italic underlined, struck, x2 and H2O."`,
		`paragraph "By a character style and switched off."`,
		`paragraph "A paragraph bold by its style."`,
		`paragraph "See the site or section 2."`,
		`paragraph "Or a field link then plain."`,
		`paragraph "Or a simple field."`,
		`paragraph "Bold and hidden." display=hidden`,
	)
}

// What hangs on hidden text is hidden with it.
func TestWordNotesAndBoxesOnHiddenText(t *testing.T) {
	box := run(`<w:rPr><w:vanish/></w:rPr><w:pict><v:shape style="width:100pt;height:40pt"><v:textbox><w:txbxContent>` + ooxmltest.Para("In a box anchored in hidden text.") + `</w:txbxContent></v:textbox></v:shape></w:pict>`)
	body := para(run(text("Seen."))+run(`<w:rPr><w:vanish/></w:rPr><w:footnoteReference w:id="2"/>`)+run(`<w:footnoteReference w:id="3"/>`)+
		run(`<w:rPr><w:vanish/></w:rPr><w:commentReference w:id="0"/>`)) +
		para(run(text("Also seen."))+box)
	pkg := ooxmltest.Docx(body)
	pkg.WordPart("footnotes.xml", ooxmltest.CTFootnotes, ooxmltest.RelFootnotes, "footnotes",
		`<w:footnote w:id="2">`+ooxmltest.Para("A note whose reference is hidden.")+`</w:footnote>`+
			`<w:footnote w:id="3">`+ooxmltest.Para("A note whose reference is shown.")+`</w:footnote>`)
	pkg.WordPart("comments.xml", ooxmltest.CTComments, ooxmltest.RelComments, "comments",
		`<w:comment w:id="0" w:author="A">`+ooxmltest.Para("A comment on hidden text.")+`</w:comment>`)
	doc := word(t, pkg, nil)
	expectBlocks(t, doc,
		`paragraph "Seen."`,
		`footnote {1} "A note whose reference is hidden." display=hidden`,
		`footnote {2} "A note whose reference is shown."`,
		`note "A comment on hidden text." author=A type=comment`,
		`paragraph "Also seen."`,
		`paragraph "In a box anchored in hidden text." display=hidden`,
	)
	ooxmltest.CheckDocument(t, doc)
}

// Text drawn with no ink (a Word 2010 text fill of nothing, or fully
// transparent) and drawings the file marks hidden. A box too small for
// its text, a row too short, and a colour are not judged.
func TestWordNoInkAndHiddenDrawings(t *testing.T) {
	box := func(extent, docPr, s string) string {
		return run(`<w:drawing><wp:anchor>` + extent + `<wp:docPr id="1" name="Box"` + docPr + `/><a:graphic><a:graphicData><wps:wsp><wps:txbx><w:txbxContent>` +
			ooxmltest.Para(s) + `</w:txbxContent></wps:txbx><wps:bodyPr/></wps:wsp></a:graphicData></a:graphic></wp:anchor></w:drawing>`)
	}
	legacy := func(style, s string) string {
		return run(`<w:pict><v:shape style="` + style + `"><v:textbox><w:txbxContent>` + ooxmltest.Para(s) + `</w:txbxContent></v:textbox></v:shape></w:pict>`)
	}
	row := func(height, s string) string {
		return `<w:tr><w:trPr>` + height + `</w:trPr><w:tc>` + ooxmltest.Para(s) + `</w:tc></w:tr>`
	}
	body := para(styled(`<w14:textFill><w14:solidFill><w14:srgbClr w14:val="000000"><w14:alpha w14:val="100000"/></w14:srgbClr></w14:solidFill></w14:textFill>`, "Fully transparent.")) +
		para(styled(`<w14:textFill><w14:noFill/></w14:textFill>`, "No fill at all.")) +
		para(styled(`<w14:textFill><w14:solidFill><w14:srgbClr w14:val="000000"><w14:alpha w14:val="40000"/></w14:srgbClr></w14:solidFill></w14:textFill>`, "Partly transparent.")) +
		para(styled(`<w:color w:val="FFFFFF"/>`, "White.")) +
		para(run(text("Anchor one."))+box(`<wp:extent cx="2540000" cy="914400"/>`, ` hidden="1"`, "In a drawing marked hidden.")) +
		para(run(text("Anchor two."))+box(`<wp:extent cx="12700" cy="12700"/>`, "", "A page of text in a box one point square.")) +
		para(run(text("Anchor three."))+legacy("position:absolute;width:200pt;height:50pt;visibility:hidden", "A legacy shape marked hidden.")) +
		para(run(text("Anchor four."))+legacy("width:1pt;height:1pt", "A legacy box one point square.")) +
		`<w:tbl>` + row(`<w:trHeight w:val="20" w:hRule="exact"/>`, "In a row one point high.") + `</w:tbl>`
	doc := word(t, ooxmltest.Docx(body), nil)
	expectBlocks(t, doc,
		`paragraph "Fully transparent." display=no_ink`,
		`paragraph "No fill at all." display=no_ink`,
		`paragraph "Partly transparent."`,
		`paragraph "White."`,
		`paragraph "Anchor one."`,
		`paragraph "In a drawing marked hidden." display=hidden`,
		`paragraph "Anchor two."`,
		`paragraph "A page of text in a box one point square."`,
		`paragraph "Anchor three."`,
		`paragraph "A legacy shape marked hidden." display=hidden`,
		`paragraph "Anchor four."`,
		`paragraph "A legacy box one point square."`,
		`table header=0 [["In a row one point high."]]`,
	)
	ooxmltest.CheckDocument(t, doc)
}
