package gofastooxml_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// Each test below builds the smallest Word document that shows one
// behaviour and checks the blocks that come out.

// render writes a document's blocks one per line: kind, level, label,
// text, attributes, table.
func render(doc *gofastcore.Document) []string {
	var out []string
	for i := range doc.Parts[0].Blocks {
		b := &doc.Parts[0].Blocks[i]
		var sb strings.Builder
		sb.WriteString(b.Kind.String())
		if b.Level > 0 {
			fmt.Fprintf(&sb, "[%d]", b.Level)
		}
		if b.Ordered {
			sb.WriteString(" ordered")
		}
		if b.Marker != "" {
			fmt.Fprintf(&sb, " {%s}", b.Marker)
		}
		if b.Text != "" {
			fmt.Fprintf(&sb, " %q", b.Text)
		}
		if b.Hidden != 0 {
			fmt.Fprintf(&sb, " hidden=%s", b.Hidden)
		}
		for _, sp := range b.Spans {
			fmt.Fprintf(&sb, " [%q", b.Text[sp.Start:sp.End])
			if sp.Hidden != 0 {
				fmt.Fprintf(&sb, " %s", sp.Hidden)
			}
			if sp.Style != 0 {
				fmt.Fprintf(&sb, " %s", sp.Style)
			}
			if sp.Href != "" {
				fmt.Fprintf(&sb, " ->%s", sp.Href)
			}
			sb.WriteString("]")
		}
		if b.Loc.Anchor != "" {
			fmt.Fprintf(&sb, " anchor=%s", b.Loc.Anchor)
		}
		keys := make([]string, 0, len(b.Attrs))
		for k := range b.Attrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&sb, " %s=%s", k, b.Attrs[k])
		}
		if t := b.Table; t != nil {
			fmt.Fprintf(&sb, " header=%d %q", t.HeaderRows, t.Rows)
			if len(t.Merges) > 0 {
				fmt.Fprintf(&sb, " merges=%v", t.Merges)
			}
			for _, h := range t.Hidden {
				fmt.Fprintf(&sb, " hidden_cell(%d,%d %s)", h.Row, h.Col, h.Hidden)
			}
		}
		if img := b.Image; img != nil {
			fmt.Fprintf(&sb, " image(%s %s %dx%d %d bytes)", img.ID, img.MIME, img.Width, img.Height, len(img.Data))
		}
		out = append(out, sb.String())
	}
	return out
}

// word extracts a built document and checks it is valid.
func word(t *testing.T, pkg *ooxmltest.Package, opts *gofastcore.Options) *gofastcore.Document {
	t.Helper()
	doc, err := extract(t, pkg.Bytes(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(); err != nil {
		t.Errorf("invalid document: %v", err)
	}
	return doc
}

func expectBlocks(t *testing.T, doc *gofastcore.Document, want ...string) {
	t.Helper()
	got := render(doc)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("blocks:\n got:\n  %s\n want:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func partWarned(doc *gofastcore.Document, code string) bool { return doc.Parts[0].Has(code) }

func run(inner string) string { return `<w:r>` + inner + `</w:r>` }
func text(s string) string {
	return `<w:t xml:space="preserve">` + ooxmltest.EscapeXML(s) + `</w:t>`
}
func para(inner string) string { return `<w:p>` + inner + `</w:p>` }

const wordStyles = `<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:pPr><w:outlineLvl w:val="0"/></w:pPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Titre2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Sub"><w:name w:val="My Subheading"/><w:basedOn w:val="Titre2"/></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Deep"><w:name w:val="heading 8"/><w:pPr><w:outlineLvl w:val="7"/></w:pPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Subtitle"><w:name w:val="Subtitle"/></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Quote"><w:name w:val="Quote"/></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Code"><w:name w:val="HTML Preformatted"/></w:style>` +
	`<w:style w:type="paragraph" w:styleId="LoopA"><w:name w:val="Loop A"/><w:basedOn w:val="LoopB"/></w:style>` +
	`<w:style w:type="paragraph" w:styleId="LoopB"><w:name w:val="Loop B"/><w:basedOn w:val="LoopA"/></w:style>` +
	`<w:style w:type="character" w:styleId="Heading1Char"><w:name w:val="Heading 1 Char"/></w:style>`

func TestWordHeadingsAndStyles(t *testing.T) {
	body := ooxmltest.StyledPara("Title", "The Title") +
		ooxmltest.StyledPara("Subtitle", "A subtitle") +
		ooxmltest.StyledPara("Heading1", "By outline level") +
		ooxmltest.StyledPara("Titre2", "By the style's name, whatever its id") +
		ooxmltest.StyledPara("Sub", "Inherited from the style it is based on") +
		ooxmltest.StyledPara("Deep", "Eighth level") +
		`<w:p><w:pPr><w:outlineLvl w:val="2"/></w:pPr>` + run(text("Level set on the paragraph")) + `</w:p>` +
		`<w:p><w:pPr><w:pStyle w:val="Heading1"/><w:outlineLvl w:val="9"/></w:pPr>` + run(text("Body text despite the style")) + `</w:p>` +
		ooxmltest.StyledPara("Quote", "To be, or not to be.") +
		ooxmltest.StyledPara("Code", "  if x {") + ooxmltest.StyledPara("Code", "\treturn") + ooxmltest.StyledPara("Code", "  }") +
		ooxmltest.StyledPara("LoopA", "A style that inherits from itself") +
		ooxmltest.StyledPara("Missing", "An unknown style") +
		ooxmltest.Para("Plain") + `<w:p/>` + para(run(text("   ")))
	doc := word(t, ooxmltest.Docx(body).WithStyles(wordStyles), nil)
	if !partWarned(doc, gofastcore.WarnHeadingLevelClamped) {
		t.Error("a heading deeper than six levels was not reported")
	}
	expectBlocks(t, doc,
		`heading[1] "The Title" style=title`,
		`heading[2] "A subtitle" style=subtitle`,
		`heading[1] "By outline level"`,
		`heading[2] "By the style's name, whatever its id"`,
		`heading[2] "Inherited from the style it is based on"`,
		`heading[6] "Eighth level" level=8`,
		`heading[3] "Level set on the paragraph"`,
		`paragraph "Body text despite the style"`,
		`quote[1] "To be, or not to be."`,
		"code \"  if x {\\n\\treturn\\n  }\"",
		`paragraph "A style that inherits from itself"`,
		`paragraph "An unknown style"`,
		`paragraph "Plain"`,
	)
}

const wordNumbering = `<w:abstractNum w:abstractNumId="0">` +
	`<w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:lvlText w:val="%1."/></w:lvl>` +
	`<w:lvl w:ilvl="1"><w:start w:val="1"/><w:numFmt w:val="lowerLetter"/><w:lvlText w:val="(%2)"/></w:lvl>` +
	`<w:lvl w:ilvl="2"><w:start w:val="1"/><w:numFmt w:val="lowerRoman"/><w:lvlText w:val="%1.%2.%3"/></w:lvl></w:abstractNum>` +
	`<w:abstractNum w:abstractNumId="1"><w:lvl w:ilvl="0"><w:numFmt w:val="bullet"/><w:lvlText w:val="&#xF0B7;"/><w:rPr><w:rFonts w:ascii="Symbol" w:hAnsi="Symbol"/></w:rPr></w:lvl>` +
	`<w:lvl w:ilvl="1"><w:numFmt w:val="bullet"/><w:lvlText w:val="o"/><w:rPr><w:rFonts w:ascii="Courier New"/></w:rPr></w:lvl></w:abstractNum>` +
	`<w:abstractNum w:abstractNumId="2">` +
	`<w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:pStyle w:val="Heading1"/><w:lvlText w:val="%1"/></w:lvl>` +
	`<w:lvl w:ilvl="1"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:pStyle w:val="Heading2"/><w:lvlText w:val="%1.%2"/></w:lvl></w:abstractNum>` +
	`<w:abstractNum w:abstractNumId="3">` +
	`<w:lvl w:ilvl="0"><w:start w:val="4"/><w:numFmt w:val="upperRoman"/><w:lvlText w:val="Article %1"/></w:lvl>` +
	`<w:lvl w:ilvl="1"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:isLgl/><w:lvlText w:val="%1.%2"/></w:lvl>` +
	`<w:lvl w:ilvl="2"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:lvlRestart w:val="0"/><w:lvlText w:val="[%3]"/></w:lvl></w:abstractNum>` +
	`<w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>` +
	`<w:num w:numId="2"><w:abstractNumId w:val="1"/></w:num>` +
	`<w:num w:numId="3"><w:abstractNumId w:val="0"/></w:num>` +
	`<w:num w:numId="4"><w:abstractNumId w:val="0"/><w:lvlOverride w:ilvl="0"><w:startOverride w:val="1"/></w:lvlOverride></w:num>` +
	`<w:num w:numId="5"><w:abstractNumId w:val="2"/></w:num>` +
	`<w:num w:numId="6"><w:abstractNumId w:val="3"/></w:num>`

func TestWordListLabels(t *testing.T) {
	L := ooxmltest.ListPara
	body := L(1, 0, "one") + L(1, 0, "two") + L(1, 1, "two a") + L(1, 1, "two b") + L(1, 2, "deep") +
		L(1, 0, "three") + L(1, 1, "three a") +
		ooxmltest.Para("Between the lists.") +
		// Another instance of the same definition carries on counting,
		// as Word does; one that overrides the start begins again.
		L(3, 0, "four") + L(4, 0, "restarted") + L(4, 0, "restarted again") +
		L(2, 0, "bullet") + L(2, 1, "inner bullet") +
		`<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="0"/></w:numPr></w:pPr>` + run(text("Numbering switched off")) + `</w:p>` +
		L(9, 0, "A list that is not defined")
	doc := word(t, ooxmltest.Docx(body).WithNumbering(wordNumbering), nil)
	expectBlocks(t, doc,
		`list_item[1] ordered {1.} "one"`,
		`list_item[1] ordered {2.} "two"`,
		`list_item[2] ordered {(a)} "two a"`,
		`list_item[2] ordered {(b)} "two b"`,
		`list_item[3] ordered {2.b.i} "deep"`,
		`list_item[1] ordered {3.} "three"`,
		`list_item[2] ordered {(a)} "three a"`,
		`paragraph "Between the lists."`,
		`list_item[1] ordered {4.} "four"`,
		`list_item[1] ordered {1.} "restarted"`,
		`list_item[1] ordered {2.} "restarted again"`,
		`list_item[1] {•} "bullet"`,
		`list_item[2] {o} "inner bullet"`,
		`paragraph "Numbering switched off"`,
		`paragraph "A list that is not defined"`,
	)
}

func TestWordNumberedHeadingsAndFormats(t *testing.T) {
	styles := `<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:pPr><w:numPr><w:numId w:val="5"/></w:numPr><w:outlineLvl w:val="0"/></w:pPr></w:style>` +
		`<w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:pPr><w:numPr><w:numId w:val="5"/></w:numPr><w:outlineLvl w:val="1"/></w:pPr></w:style>`
	H := ooxmltest.StyledPara
	L := ooxmltest.ListPara
	body := H("Heading1", "Scope") + H("Heading2", "Purpose") + H("Heading2", "Audience") + H("Heading1", "Terms") + H("Heading2", "Definitions") +
		L(6, 0, "First article") + L(6, 1, "clause") + L(6, 2, "point") + L(6, 2, "point") +
		L(6, 0, "Second article") + L(6, 1, "clause") + L(6, 2, "numbering not restarted") +
		// An empty numbered paragraph is not a block, but takes a number.
		`<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="6"/></w:numPr></w:pPr></w:p>` +
		L(6, 0, "Fourth article")
	doc := word(t, ooxmltest.Docx(body).WithStyles(styles).WithNumbering(wordNumbering), nil)
	expectBlocks(t, doc,
		`heading[1] {1} "Scope"`,
		`heading[2] {1.1} "Purpose"`,
		`heading[2] {1.2} "Audience"`,
		`heading[1] {2} "Terms"`,
		`heading[2] {2.1} "Definitions"`,
		`list_item[1] ordered {Article IV} "First article"`,
		`list_item[2] ordered {4.1} "clause"`, // a "legal" level shows the others in digits
		`list_item[3] ordered {[1]} "point"`,
		`list_item[3] ordered {[2]} "point"`,
		`list_item[1] ordered {Article V} "Second article"`,
		`list_item[2] ordered {5.1} "clause"`,
		`list_item[3] ordered {[3]} "numbering not restarted"`,
		`list_item[1] ordered {Article VII} "Fourth article"`,
	)
}

func TestWordRunContent(t *testing.T) {
	// White space at the ends of a text element counts only where the
	// element asks for it to be kept, as in Word.
	body := para(run(`<w:t>  trimmed  </w:t>`)+run(text(" kept "))+run("<w:t>\n\t  end\n  </w:t>")) +
		para(run(text("a")+`<w:tab/>`+text("b")+`<w:br/>`+text("c")+`<w:cr/>`+text("d"))) +
		para(run(text("non")+`<w:noBreakHyphen/>`+text("breaking soft")+`<w:softHyphen/>`+text("hyphen"))) +
		para(run(`<w:sym w:font="Symbol" w:char="F061"/><w:sym w:font="Symbol" w:char="F0B7"/><w:sym w:font="Wingdings" w:char="F04A"/>`)) +
		// A field: the code is skipped, the result is text.
		para(run(text("Page "))+run(`<w:fldChar w:fldCharType="begin"/>`)+run(`<w:instrText xml:space="preserve"> PAGE \* MERGEFORMAT </w:instrText>`)+
			run(`<w:fldChar w:fldCharType="separate"/>`)+run(text("7"))+run(`<w:fldChar w:fldCharType="end"/>`)+
			`<w:fldSimple w:instr=" NUMPAGES ">`+run(text(" of 9"))+`</w:fldSimple>`) +
		para(run(text("See "))+`<w:hyperlink r:id="rId9">`+run(text("the site"))+`</w:hyperlink>`+run(text("."))) +
		// Wrappers that hold ordinary content.
		para(`<w:smartTag><w:r>`+text("smart ")+`</w:r></w:smartTag><w:sdt><w:sdtPr><w:alias w:val="x"/></w:sdtPr><w:sdtContent>`+run(text("control"))+`</w:sdtContent></w:sdt>`) +
		`<w:sdt><w:sdtContent>` + ooxmltest.Para("A paragraph inside a content control") + `</w:sdtContent></w:sdt>` +
		para(run(text("Energy: "))+`<m:oMath><m:r><m:t>E=mc</m:t></m:r><m:sSup><m:e><m:r><m:t>2</m:t></m:r></m:e></m:sSup></m:oMath>`) +
		`<w:p w14:paraId="1A2B3C4D">` + run(text("Anchored")) + `</w:p>`
	doc := word(t, ooxmltest.Docx(body), nil)
	expectBlocks(t, doc,
		`paragraph "trimmed kept end"`,
		"paragraph \"a\\tb\\nc\\nd\"",
		`paragraph "non‑breaking soft\u00adhyphen"`,
		"paragraph \"α•\uFFFD\"",
		`paragraph "Page 7 of 9"`,
		`paragraph "See the site."`,
		`paragraph "smart control"`,
		`paragraph "A paragraph inside a content control"`,
		`paragraph "Energy: E=mc2"`,
		`paragraph "Anchored" anchor=1A2B3C4D`,
	)
	if !partWarned(doc, gofastcore.WarnTextUnmappedGlyphs) {
		t.Error("a symbol that could not be translated was not reported")
	}
}

func TestWordTables(t *testing.T) {
	tc := func(props, content string) string {
		return `<w:tc><w:tcPr>` + props + `</w:tcPr>` + content + `</w:tc>`
	}
	cell := func(s string) string { return `<w:tc>` + ooxmltest.Para(s) + `</w:tc>` }
	row := func(cells ...string) string { return `<w:tr>` + strings.Join(cells, "") + `</w:tr>` }
	header := func(cells ...string) string {
		return `<w:tr><w:trPr><w:tblHeader/></w:trPr>` + strings.Join(cells, "") + `</w:tr>`
	}
	inner := `<w:tbl>` + row(cell("i1"), cell("i2")) + row(cell("i3"), cell("i4")) + `</w:tbl>`
	body := `<w:tbl><w:tblPr><w:tblW w:w="0"/></w:tblPr><w:tblGrid><w:gridCol/><w:gridCol/><w:gridCol/></w:tblGrid>` +
		header(cell("Region"), cell("Q1"), cell("Q2")) +
		row(tc(`<w:vMerge w:val="restart"/>`, ooxmltest.Para("North")), cell("10"), cell("12")) +
		row(tc(`<w:vMerge/>`, `<w:p/>`), cell("11"), cell("13")) +
		row(cell("Total"), tc(`<w:gridSpan w:val="2"/>`, ooxmltest.Para("46"))) +
		row(cell("Notes"), tc(`<w:gridSpan w:val="2"/>`, ooxmltest.Para("first")+ooxmltest.ListPara(1, 0, "listed")+inner)) +
		`<w:tr><w:trPr><w:gridBefore w:val="1"/></w:trPr>` + cell("offset") + `</w:tr>` +
		`<w:sdt><w:sdtContent>` + row(cell("in a control"), cell(""), cell("")) + `</w:sdtContent></w:sdt>` +
		`</w:tbl>` +
		`<w:tbl></w:tbl>` + // an empty table is nothing
		ooxmltest.Para("After")
	doc := word(t, ooxmltest.Docx(body).WithNumbering(wordNumbering), nil)
	expectBlocks(t, doc,
		`table header=1 [["Region" "Q1" "Q2"] ["North" "10" "12"] ["" "11" "13"] ["Total" "46" ""] `+
			`["Notes" "first\n1. listed\ni1\ti2\ni3\ti4" ""] ["" "offset" ""] ["in a control" "" ""]] `+
			`merges=[{1 0 2 1} {3 1 1 2} {4 1 1 2}]`,
		`paragraph "After"`,
	)
}

func TestWordNotesAndComments(t *testing.T) {
	ref := func(kind string, id int) string {
		return run(fmt.Sprintf(`<w:%sReference w:id="%d"/>`, kind, id))
	}
	cellWithNote := `<w:tbl><w:tr><w:tc>` + para(run(text("cell"))+ref("footnote", 3)) + `</w:tc></w:tr></w:tbl>`
	body := para(run(text("First claim."))+ref("footnote", 2)+run(text(" Second claim."))+ref("endnote", 1)) +
		para(run(text("Commented text."))+run(`<w:commentReference w:id="0"/>`)) +
		cellWithNote +
		para(run(text("Custom mark"))+run(`<w:footnoteReference w:id="4" w:customMarkFollows="1"/>`+text("*"))) +
		para(run(text("Missing note."))+ref("footnote", 99)) +
		para(run(text("Last."))+ref("endnote", 2))
	pkg := ooxmltest.Docx(body)
	pkg.WordPart("footnotes.xml", ooxmltest.CTFootnotes, ooxmltest.RelFootnotes, "footnotes",
		`<w:footnote w:type="separator" w:id="0"><w:p><w:r><w:separator/></w:r></w:p></w:footnote>`+
			`<w:footnote w:id="2">`+para(run(`<w:footnoteRef/>`)+run(text(" The source of the first claim.")))+ooxmltest.Para("Its second paragraph.")+`</w:footnote>`+
			`<w:footnote w:id="3">`+ooxmltest.Para("About the cell.")+`</w:footnote>`+
			`<w:footnote w:id="4">`+ooxmltest.Para("Marked by hand.")+`</w:footnote>`)
	pkg.WordPart("endnotes.xml", ooxmltest.CTEndnotes, ooxmltest.RelEndnotes, "endnotes",
		`<w:endnote w:id="1">`+ooxmltest.Para("An endnote.")+`</w:endnote><w:endnote w:id="2">`+ooxmltest.Para("Another.")+`</w:endnote>`)
	pkg.WordPart("comments.xml", ooxmltest.CTComments, ooxmltest.RelComments, "comments",
		`<w:comment w:id="0" w:author="Ada Lovelace" w:date="2026-03-04T09:30:00Z" w:initials="AL">`+ooxmltest.Para("Is this right?")+`</w:comment>`)
	doc := word(t, pkg, nil)
	expectBlocks(t, doc,
		`paragraph "First claim. Second claim."`,
		`footnote {1} "The source of the first claim.\nIts second paragraph."`,
		`footnote {i} "An endnote."`,
		`paragraph "Commented text."`,
		`note "Is this right?" author=Ada Lovelace date=2026-03-04T09:30:00Z type=comment`,
		`table header=0 [["cell"]]`,
		`footnote {2} "About the cell."`,
		`paragraph "Custom mark*"`,
		`footnote "Marked by hand."`,
		`paragraph "Missing note."`,
		`paragraph "Last."`,
		`footnote {ii} "Another."`,
	)
}

func TestWordTrackedChanges(t *testing.T) {
	del := func(s string) string {
		return `<w:del w:id="1" w:author="Ada" w:date="2026-03-04T10:00:00Z"><w:r><w:delText xml:space="preserve">` + s + `</w:delText></w:r></w:del>`
	}
	ins := func(s string) string { return `<w:ins w:id="2" w:author="Ada">` + run(text(s)) + `</w:ins>` }
	body := para(run(text("The price is "))+del("500")+ins("600")+run(text(" dollars."))) +
		// A change of formatting records the old properties; they must not
		// be taken for the paragraph's own.
		`<w:p><w:pPr><w:pStyle w:val="Heading1"/><w:pPrChange w:id="3" w:author="Ada"><w:pPr><w:pStyle w:val="Quote"/></w:pPr></w:pPrChange></w:pPr>` + run(text("Now a heading")) + `</w:p>` +
		para(`<w:moveFrom w:id="4" w:author="Ada">`+run(text("moved text"))+`</w:moveFrom>`+run(text("Stays."))) +
		para(`<w:moveTo w:id="5" w:author="Ada">`+run(text("moved text"))+`</w:moveTo>`) +
		`<w:tbl><w:tr>` + `<w:tc>` + ooxmltest.Para("kept row") + `</w:tc></w:tr>` +
		`<w:tr><w:trPr><w:del w:id="6" w:author="Ada"/></w:trPr><w:tc>` + ooxmltest.Para("deleted row") + `</w:tc></w:tr></w:tbl>`
	doc := word(t, ooxmltest.Docx(body).WithStyles(wordStyles), nil)
	expectBlocks(t, doc,
		`paragraph "The price is 600 dollars."`,
		`note "500" author=Ada date=2026-03-04T10:00:00Z type=deletion`,
		`heading[1] "Now a heading"`,
		`paragraph "Stays."`,
		`paragraph "moved text"`,
		`table header=0 [["kept row"]]`,
	)
	if !partWarned(doc, gofastcore.WarnTextTrackedChanges) {
		t.Errorf("tracked changes were not reported: %+v", doc.Parts[0].Warnings)
	}
	if clean := word(t, ooxmltest.Docx(ooxmltest.Para("x")), nil); partWarned(clean, gofastcore.WarnTextTrackedChanges) {
		t.Error("tracked changes reported for a document without any")
	}
}

func TestWordHeadersAndFooters(t *testing.T) {
	pkg := ooxmltest.DocxSection(
		`<w:p><w:pPr><w:sectPr><w:headerReference w:type="default" r:id="rId1"/><w:footerReference w:type="default" r:id="rId2"/></w:sectPr></w:pPr>`+run(text("End of section one."))+`</w:p>`+
			ooxmltest.Para("Section two."),
		`<w:headerReference w:type="default" r:id="rId1"/><w:headerReference w:type="first" r:id="rId3"/><w:footerReference w:type="default" r:id="rId2"/><w:titlePg/>`)
	pkg.WordPart("header1.xml", ooxmltest.CTHeader, ooxmltest.RelHeader, "hdr", ooxmltest.Para("ACME Confidential")+`<w:p/>`)
	pkg.WordPart("footer1.xml", ooxmltest.CTFooter, ooxmltest.RelFooter, "ftr",
		para(run(text("Page "))+`<w:fldSimple w:instr=" PAGE ">`+run(text("3"))+`</w:fldSimple>`))
	pkg.WordPart("header2.xml", ooxmltest.CTHeader, ooxmltest.RelHeader, "hdr",
		`<w:tbl><w:tr><w:tc>`+ooxmltest.Para("First page")+`</w:tc><w:tc>`+ooxmltest.Para("2026")+`</w:tc></w:tr></w:tbl>`)
	doc := word(t, pkg, nil)
	expectBlocks(t, doc,
		`header "ACME Confidential"`,
		`header "First page\t2026"`,
		`paragraph "End of section one."`,
		`paragraph "Section two."`,
		`footer "Page 3"`,
	)
	// Rendered text leaves the furniture out unless asked.
	if got := doc.Text(); got != "End of section one.\n\nSection two.\n" {
		t.Errorf("Text() = %q", got)
	}
}

func TestWordTextBoxesAndAlternateContent(t *testing.T) {
	box := func(s string) string { return `<w:txbxContent>` + ooxmltest.Para(s) + `</w:txbxContent>` }
	modern := `<w:drawing><wp:anchor><wp:docPr id="1" name="Text Box 1"/><a:graphic><a:graphicData><wps:wsp><wps:txbx>` + box("In the box") + `</wps:txbx></wps:wsp></a:graphicData></a:graphic></wp:anchor></w:drawing>`
	legacy := func(s string) string {
		return `<w:pict><v:shape><v:textbox>` + box(s) + `</v:textbox></v:shape></w:pict>`
	}
	body := para(run(text("Before."))+run(`<mc:AlternateContent><mc:Choice Requires="wps">`+modern+`</mc:Choice><mc:Fallback>`+legacy("In the box")+`</mc:Fallback></mc:AlternateContent>`)+run(text(" After."))) +
		// A first branch with nothing readable in it: the fallback is read.
		para(run(`<mc:AlternateContent><mc:Choice Requires="w99"><w:unknown/></mc:Choice><mc:Fallback>`+legacy("Only in the fallback")+`</mc:Fallback></mc:AlternateContent>`)) +
		para(run(legacy("A legacy box on its own"))) +
		// Alternate content around whole paragraphs.
		`<mc:AlternateContent><mc:Choice Requires="x">` + ooxmltest.Para("New form") + `</mc:Choice><mc:Fallback>` + ooxmltest.Para("Old form") + `</mc:Fallback></mc:AlternateContent>`
	doc := word(t, ooxmltest.Docx(body), nil)
	expectBlocks(t, doc,
		`paragraph "Before. After."`,
		`paragraph "In the box"`,
		`paragraph "Only in the fallback"`,
		`paragraph "A legacy box on its own"`,
		`paragraph "New form"`,
	)
}

func pngBytes(t testing.TB, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestWordImages(t *testing.T) {
	picture := func(id, alt string) string {
		return run(`<w:drawing><wp:inline><wp:extent cx="914400" cy="457200"/><wp:docPr id="1" name="Picture 1" descr="` + alt + `"/>` +
			`<a:graphic><a:graphicData><pic:pic><pic:blipFill><a:blip r:embed="` + id + `"/></pic:blipFill></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing>`)
	}
	build := func(body string) *ooxmltest.Package {
		pkg := ooxmltest.Docx(body)
		pkg.AddBytes("word/media/image1.png", "image/png", pngBytes(t, 40, 20))
		pkg.Rel("word/document.xml", ooxmltest.RelImage, "media/image1.png")                     // rId1
		pkg.ExternalRel("word/document.xml", ooxmltest.RelImage, "https://example.org/logo.png") // rId2
		return pkg
	}
	body := para(run(text("A chart follows."))+picture("rId1", "Revenue by region")) +
		para(picture("rId1", "The same picture again")) +
		para(run(`<w:pict><v:shape alt="Legacy picture"><v:imagedata r:id="rId1" o:title="scan"/></v:shape></w:pict>`)) +
		para(picture("rId2", "A picture that is not in the file"))

	doc := word(t, build(body), &gofastcore.Options{Images: gofastcore.ImagesData})
	size := len(pngBytes(t, 40, 20))
	expectBlocks(t, doc,
		`paragraph "A chart follows."`,
		fmt.Sprintf(`image "Revenue by region" image(word/media/image1.png image/png 40x20 %d bytes)`, size),
		`image "The same picture again" image(word/media/image1.png image/png 40x20 0 bytes)`, // bytes go with the first use
		`image "Legacy picture" image(word/media/image1.png image/png 40x20 0 bytes)`,
		`image "A picture that is not in the file" href=https://example.org/logo.png image(  0x0 0 bytes)`,
	)
	if !warned(doc, gofastcore.WarnInputExternalReference) {
		t.Error("a linked picture was not reported as an external reference")
	}

	// Position and size without the bytes.
	meta := word(t, build(body), &gofastcore.Options{Images: gofastcore.ImagesMeta})
	if got := render(meta)[1]; got != `image "Revenue by region" image(word/media/image1.png image/png 40x20 0 bytes)` {
		t.Errorf("ImagesMeta: %s", got)
	}

	// That is the default: a picture is a block, with its alternative
	// text, unless the caller asks for none.
	if got := render(word(t, build(body), nil))[1]; got != `image "Revenue by region" image(word/media/image1.png image/png 40x20 0 bytes)` {
		t.Errorf("by default: %s", got)
	}
	// Asked for none, a document that is only pictures still says so.
	noImages := &gofastcore.Options{Images: gofastcore.ImagesNone}
	none := word(t, build(body), noImages)
	expectBlocks(t, none, `paragraph "A chart follows."`)
	only := word(t, build(para(picture("rId1", "Scanned page"))), noImages)
	if len(only.Parts[0].Blocks) != 0 || !partWarned(only, gofastcore.WarnPartImageOnly) || partWarned(only, gofastcore.WarnPartNoText) {
		t.Errorf("picture-only document: blocks %v, warnings %+v", render(only), only.Parts[0].Warnings)
	}
	empty := word(t, ooxmltest.Docx(""), nil)
	if !partWarned(empty, gofastcore.WarnPartNoText) || partWarned(empty, gofastcore.WarnPartImageOnly) {
		t.Errorf("empty document: warnings %+v", empty.Parts[0].Warnings)
	}
}

func TestWordEmbeddedFilesAndReferences(t *testing.T) {
	build := func() *ooxmltest.Package {
		pkg := ooxmltest.Docx(para(run(text("See the attached sheet."))+run(`<w:object><o:OLEObject Type="Embed" ProgID="Excel.Sheet.12" r:id="rId1"/></w:object>`)) +
			para(run(`<w:drawing><wp:inline><a:graphic><a:graphicData><c:chart r:id="rId3"/></a:graphicData></a:graphic></wp:inline></w:drawing>`)))
		inner := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S"}).Bytes()
		pkg.AddBytes("word/embeddings/Sheet1.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", inner)
		pkg.Rel("word/document.xml", ooxmltest.RelPackage, "embeddings/Sheet1.xlsx") // rId1
		pkg.Rel("word/document.xml", ooxmltest.RelSettings, "settings.xml")          // rId2
		pkg.Add("word/settings.xml", ooxmltest.CTSettings, `<w:settings`+ooxmltest.WordNS+`/>`)
		pkg.ExternalRel("word/settings.xml", ooxmltest.RelTemplate, "https://example.org/evil.dotm")
		return pkg
	}
	// Without a child extractor the embedded file is listed, not read.
	doc := word(t, build(), nil)
	if len(doc.Children) != 1 || doc.Children[0].Source.Name != "Sheet1.xlsx" || len(doc.Children[0].Parts) != 0 ||
		doc.Children[0].Warnings[0].Code != gofastcore.WarnChildNotExtracted || doc.Children[0].Source.Size == 0 {
		t.Errorf("children = %+v", doc.Children)
	}
	if !warned(doc, gofastcore.WarnInputExternalReference) {
		t.Error("an attached remote template was not reported")
	}
	if !warned(doc, gofastooxml.WarnUnreadContent) {
		t.Error("a chart was not reported as unread content")
	}

	// With one, it is handed over as bytes.
	var gotName, gotMIME string
	var gotSize int64
	opts := &gofastcore.Options{Child: func(ctx context.Context, name, mime string, r io.ReaderAt, size int64, o *gofastcore.Options) (*gofastcore.Document, error) {
		gotName, gotMIME, gotSize = name, mime, size
		return gofastooxml.Extract(ctx, r, size, o)
	}}
	doc = word(t, build(), opts)
	if gotName != "Sheet1.xlsx" || !strings.Contains(gotMIME, "spreadsheetml") || gotSize == 0 {
		t.Errorf("child extractor called with %q %q %d", gotName, gotMIME, gotSize)
	}
	if len(doc.Children) != 1 || doc.Children[0].Source.Format != "xlsx" {
		t.Errorf("children = %+v", doc.Children)
	}
}

func TestWordDamageAndLimits(t *testing.T) {
	// The body is cut off: what came before is kept and the part says so.
	pkg := ooxmltest.Docx("")
	pkg.Add("word/document.xml", ooxmltest.CTDocument, `<w:document`+ooxmltest.WordNS+`><w:body>`+ooxmltest.Para("Kept one.")+ooxmltest.Para("Kept two.")+`<w:p><w:r><w:t>lost`)
	doc := word(t, pkg, nil)
	// The paragraph the cut falls in is incomplete, but its text was read
	// and is kept.
	expectBlocks(t, doc, `paragraph "Kept one."`, `paragraph "Kept two."`, `paragraph "lost"`)
	if !partWarned(doc, gofastcore.WarnPartFailed) {
		t.Errorf("a cut-off body was not reported: %+v", doc.Parts[0].Warnings)
	}

	// Nesting beyond the limit.
	pkg = ooxmltest.Docx(ooxmltest.Para("Kept.") + strings.Repeat(`<w:tbl><w:tr><w:tc>`, 400) + ooxmltest.Para("deep") + strings.Repeat(`</w:tc></w:tr></w:tbl>`, 400))
	doc = word(t, pkg, nil)
	if got := render(doc); len(got) == 0 || got[0] != `paragraph "Kept."` || !partWarned(doc, gofastcore.WarnLimitExceeded) {
		t.Errorf("deep nesting: blocks %v, warnings %+v", got, doc.Parts[0].Warnings)
	}

	// The decompression budget runs out in the body.
	var many strings.Builder
	for i := range 20000 {
		many.WriteString(ooxmltest.Para(fmt.Sprintf("Paragraph %d of a long document.", i)))
	}
	doc = word(t, ooxmltest.Docx(many.String()), &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 256 << 10}})
	if n := len(doc.Parts[0].Blocks); n == 0 || n >= 20000 || !partWarned(doc, gofastcore.WarnLimitExceeded) {
		t.Errorf("budget: %d blocks, warnings %+v", n, doc.Parts[0].Warnings)
	}

	// Damaged supporting parts cost their content, not the document.
	pkg = ooxmltest.Docx(ooxmltest.StyledPara("Heading1", "Still read"))
	pkg.Rel("word/document.xml", ooxmltest.RelStyles, "styles.xml")
	pkg.Add("word/styles.xml", ooxmltest.CTStyles, `<w:styles`+ooxmltest.WordNS+`><w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/></w:style><w:style`)
	doc = word(t, pkg, nil)
	expectBlocks(t, doc, `heading[1] "Still read"`)
	if !warned(doc, gofastooxml.WarnPartDamaged) {
		t.Errorf("a damaged styles part was not reported: %+v", doc.Warnings)
	}

	// A part range that does not include the one part there is.
	data := ooxmltest.Docx(ooxmltest.Para("x")).Bytes()
	if _, err := extract(t, data, &gofastcore.Options{Parts: gofastcore.PartRange{First: 2, Last: 3}}); !errors.Is(err, gofastcore.ErrNoPartsSelected) {
		t.Errorf("part range 2-3 on a Word document: %v", err)
	}
	if _, err := extract(t, data, &gofastcore.Options{Parts: gofastcore.PartRange{First: 1, Last: 1}}); err != nil {
		t.Errorf("part range 1-1: %v", err)
	}

	// A cancelled context stops a long body.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	long := ooxmltest.Docx(many.String()).Bytes()
	if _, err := gofastooxml.Extract(ctx, bytes.NewReader(long), int64(len(long)), nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
}

func TestWordRuby(t *testing.T) {
	ruby := `<w:ruby><w:rubyPr/><w:rt>` + run(text("\u3068\u3046\u304d\u3087\u3046")) + `</w:rt><w:rubyBase>` + run(text("\u6771\u4eac")) + `</w:rubyBase></w:ruby>`
	doc := word(t, ooxmltest.Docx(para(run(text("In ")+ruby+text(" today.")))), nil)
	// The base text, then its pronunciation guide in full-width
	// parentheses: both are kept.
	expectBlocks(t, doc, "paragraph \"In \u6771\u4eac\uff08\u3068\u3046\u304d\u3087\u3046\uff09 today.\"")
}

func TestWordNotesAreGivenOnce(t *testing.T) {
	ref := run(`<w:footnoteReference w:id="2"/>`)
	var body strings.Builder
	for range 500 {
		body.WriteString(para(run(text("Claim.")) + ref))
	}
	pkg := ooxmltest.Docx(body.String())
	pkg.WordPart("footnotes.xml", ooxmltest.CTFootnotes, ooxmltest.RelFootnotes, "footnotes",
		`<w:footnote w:id="2">`+ooxmltest.Para(strings.Repeat("A long note. ", 2000))+`</w:footnote>`)
	doc := word(t, pkg, nil)
	notes := 0
	for _, b := range doc.Parts[0].Blocks {
		if b.Kind == gofastcore.KindFootnote {
			notes++
		}
	}
	if notes != 1 || len(doc.Parts[0].Blocks) != 501 {
		t.Errorf("%d footnote blocks among %d, want 1 among 501", notes, len(doc.Parts[0].Blocks))
	}
}

// A file can declare a table grid far larger than the cells it holds. The
// reader must not fill that grid: it writes the rows as text and says so.
func TestWordDegenerateTable(t *testing.T) {
	var rows strings.Builder
	for i := range 3000 {
		fmt.Fprintf(&rows, `<w:tr><w:tc><w:tcPr><w:gridSpan w:val="1000"/></w:tcPr>%s</w:tc><w:tc>%s</w:tc></w:tr>`,
			ooxmltest.Para(fmt.Sprintf("row %d", i)), ooxmltest.Para("b"))
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	doc := word(t, ooxmltest.Docx(`<w:tbl>`+rows.String()+`</w:tbl>`), nil)
	runtime.ReadMemStats(&after)
	blocks := doc.Parts[0].Blocks
	if len(blocks) != 3000 || blocks[0].Kind != gofastcore.KindParagraph || blocks[0].Text != "row 0\tb" || blocks[2999].Text != "row 2999\tb" {
		t.Errorf("%d blocks; first %+v", len(blocks), blocks[0])
	}
	if !partWarned(doc, gofastooxml.WarnTableFlattened) {
		t.Error("the flattened table was not reported")
	}
	if grew := (after.TotalAlloc - before.TotalAlloc) >> 20; grew > 64 {
		t.Errorf("a table declared 3,000 by 1,001 with 6,000 cells cost %d MiB", grew)
	}
	// An ordinary wide cell is still a table.
	ok := word(t, ooxmltest.Docx(`<w:tbl><w:tr><w:tc><w:tcPr><w:gridSpan w:val="6"/></w:tcPr>`+ooxmltest.Para("title")+`</w:tc></w:tr>`+
		`<w:tr>`+strings.Repeat(`<w:tc>`+ooxmltest.Para("x")+`</w:tc>`, 6)+`</w:tr></w:tbl>`), nil)
	expectBlocks(t, ok, `table header=0 [["title" "" "" "" "" ""] ["x" "x" "x" "x" "x" "x"]] merges=[{0 0 1 6}]`)
}

// Names, addresses and error text come from the file as much as its
// content does. Whatever they hold, the document stays valid.
func TestWordHostileNames(t *testing.T) {
	pkg := ooxmltest.Docx(para(run(text("See the picture."))+
		run(`<w:drawing><wp:inline><wp:docPr descr="alt&#1;text"/><a:graphic><a:graphicData><pic:pic><pic:blipFill><a:blip r:embed="rId1"/></pic:blipFill></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing>`)+
		run(`<w:drawing><wp:inline><a:graphic><a:graphicData><pic:pic><pic:blipFill><a:blip r:link="rId2"/></pic:blipFill></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing>`)) +
		`<w:p w14:paraId="A&#2;B"><w:r><w:t>x</w:t></w:r></w:p></w:body></w:document><w:oops`)
	pkg.AddBytes("word/media/we\x01ird\tname.png", "image/p\x02ng", pngBytes(t, 2, 2))
	pkg.Rel("word/document.xml", ooxmltest.RelImage, "media/we%01ird%09name.png")
	pkg.ExternalRel("word/document.xml", ooxmltest.RelImage, "https://example.org/a&#3;b")
	doc := word(t, pkg, &gofastcore.Options{Images: gofastcore.ImagesMeta})
	for _, b := range doc.Parts[0].Blocks {
		if b.Image != nil && strings.ContainsAny(b.Image.ID+b.Image.MIME+b.Attrs["href"]+b.Text, "\x01\x02\x03\t") {
			t.Errorf("control characters reached the output: %+v %+v", b, b.Image)
		}
	}
}

func TestAmbiguousArchiveIsReported(t *testing.T) {
	// Two members with one name: programs differ in which they read.
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	add := func(name, content string) {
		f, _ := w.Create(name)
		io.WriteString(f, content)
	}
	add("[Content_Types].xml", `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="`+ooxmltest.CTDocument+`"/></Types>`)
	add("word/document.xml", `<w:document`+ooxmltest.WordNS+`><w:body>`+ooxmltest.Para("first")+`</w:body></w:document>`)
	add("word/document.xml", `<w:document`+ooxmltest.WordNS+`><w:body>`+ooxmltest.Para("second")+`</w:body></w:document>`)
	w.Close()
	doc, err := extract(t, buf.Bytes(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Text(); got != "first\n" || !warned(doc, gofastcore.WarnInputAmbiguous) {
		t.Errorf("text %q, warnings %+v", got, doc.Warnings)
	}
	if clean := word(t, ooxmltest.Docx(ooxmltest.Para("x")), nil); warned(clean, gofastcore.WarnInputAmbiguous) {
		t.Error("an ordinary archive was reported as ambiguous")
	}
}

// A paragraph in a code style is code, kept as it is written. One that is
// also a numbered item or a heading is that, and its text is text like
// any other: trimmed, with ligatures written as letters. (Found in a
// document embedded in a presentation of the corpus, where a numbered
// paragraph in a plain-text style came out with a space in front.)
func TestWordCodeStyleOnListItemsAndHeadings(t *testing.T) {
	body := `<w:p><w:pPr><w:pStyle w:val="Code"/><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>` + run(text("  a numbered item with a \ufb01 ligature ")) + `</w:p>` +
		`<w:p><w:pPr><w:pStyle w:val="Code"/><w:outlineLvl w:val="1"/></w:pPr>` + run(text("\t a heading in a code style")) + `</w:p>` +
		ooxmltest.StyledPara("Code", "  kept as written: \ufb01")
	doc := word(t, ooxmltest.Docx(body).WithStyles(wordStyles).WithNumbering(wordNumbering), nil)
	blocks := doc.Parts[0].Blocks
	if len(blocks) != 3 {
		t.Fatalf("%d blocks: %+v", len(blocks), blocks)
	}
	for i, want := range []struct {
		kind gofastcore.Kind
		text string
	}{
		{gofastcore.KindListItem, "a numbered item with a fi ligature"},
		{gofastcore.KindHeading, "a heading in a code style"},
		{gofastcore.KindCode, "  kept as written: \ufb01"},
	} {
		if blocks[i].Kind != want.kind || blocks[i].Text != want.text {
			t.Errorf("block %d is %s %q, want %s %q", i, blocks[i].Kind, blocks[i].Text, want.kind, want.text)
		}
	}
}
