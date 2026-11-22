package gofastooxml_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// The fixed bounds that leave content out say so: limit.exceeded,
// severity error, once per bound on the part it concerns (or on the
// document, for what belongs to no part), with how much was left out.

// limits returns the messages of the limit.exceeded warnings of a part,
// or of the document itself with part -1.
func limits(doc *gofastcore.Document, part int) []string {
	ws := doc.Warnings
	if part >= 0 {
		ws = doc.Parts[part].Warnings
	}
	var out []string
	for _, w := range ws {
		if w.Code == gofastcore.WarnLimitExceeded {
			out = append(out, w.Message)
		}
	}
	return out
}

// saysOnce checks that exactly one limit.exceeded warning of the part
// (or of the document, with part -1) contains each of want, and that
// every such warning has severity error.
func saysOnce(t *testing.T, doc *gofastcore.Document, part int, want ...string) {
	t.Helper()
	n := 0
	all := limits(doc, part)
	for _, m := range all {
		ok := true
		for _, w := range want {
			ok = ok && strings.Contains(m, w)
		}
		if ok {
			n++
		}
	}
	if n != 1 {
		t.Errorf("want one limit.exceeded with %q on part %d, have %q", want, part, all)
	}
	for _, w := range doc.AllWarnings() {
		if w.Code == gofastcore.WarnLimitExceeded && w.Severity != gofastcore.SeverityError {
			t.Errorf("severity %v: %q", w.Severity, w.Message)
		}
	}
}

// noLimits checks that nothing was said to be left out.
func noLimits(t *testing.T, doc *gofastcore.Document) {
	t.Helper()
	for _, w := range doc.AllWarnings() {
		if w.Code == gofastcore.WarnLimitExceeded {
			t.Errorf("unexpected limit.exceeded: %q", w.Message)
		}
	}
}

func capsExtract(t *testing.T, data []byte) *gofastcore.Document {
	t.Helper()
	doc, err := extract(t, data, &gofastcore.Options{Spans: true})
	if err != nil {
		t.Fatal(err)
	}
	ooxmltest.CheckDocument(t, doc)
	return doc
}

// contains reports whether s is in any block's text or attributes.
func contains(doc *gofastcore.Document, s string) bool {
	for _, b := range doc.AllBlocks() {
		if strings.Contains(b.Text, s) {
			return true
		}
		for _, v := range b.Attrs {
			if strings.Contains(v, s) {
				return true
			}
		}
		if b.Table != nil {
			for _, row := range b.Table.Rows {
				for _, c := range row {
					if strings.Contains(c, s) {
						return true
					}
				}
			}
		}
	}
	return false
}

func field(code, shown string) string {
	return run(`<w:fldChar w:fldCharType="begin"/>`) + code + run(`<w:fldChar w:fldCharType="separate"/>`) + run(text(shown)) + run(`<w:fldChar w:fldCharType="end"/>`)
}

func instr(s string) string { return run(`<w:instrText xml:space="preserve">` + s + `</w:instrText>`) }

// A field code longer than 64 KiB is cut there and said, once for the
// document however many are cut; one under the bound is whole.
func TestFieldCodeCap(t *testing.T) {
	long := ` HYPERLINK "https://example.com/` + strings.Repeat("a", 70<<10) + `ENDMARK" `
	split := instr(` QUOTE "`+strings.Repeat("b", 40<<10)) + instr(strings.Repeat("c", 40<<10)+`TAILMARK" `)
	body := para(field(instr(long), "one")) + para(field(split, "two")) + para(field(instr(" SHORTCODE "), "three"))
	doc := capsExtract(t, ooxmltest.Docx(body).Bytes())
	saysOnce(t, doc, 0, "field codes longer than 65536 bytes were cut there", "(2 fields)")
	if contains(doc, "ENDMARK") || contains(doc, "TAILMARK") || !contains(doc, "SHORTCODE") {
		t.Error("the codes are not cut where they should be")
	}
	for _, b := range doc.AllBlocks() {
		if len(b.Text) > 1<<16 {
			t.Errorf("a code of %d bytes", len(b.Text))
		}
	}
	noLimits(t, capsExtract(t, ooxmltest.Docx(para(field(instr(" SHORTCODE "), "x"))).Bytes()))
}

// Fields nested deeper than 64 are read as the field around them, and
// the ones around them still end where they end: text after them is
// shown.
func TestFieldDepthCap(t *testing.T) {
	var b strings.Builder
	for i := range 66 {
		b.WriteString(run(`<w:fldChar w:fldCharType="begin"/>`) + instr(fmt.Sprintf(" F%d ", i)))
	}
	for range 66 {
		b.WriteString(run(`<w:fldChar w:fldCharType="separate"/>`) + run(text("r")) + run(`<w:fldChar w:fldCharType="end"/>`))
	}
	doc := capsExtract(t, ooxmltest.Docx(para(b.String())+para(run(text("AFTERMARK")))).Bytes())
	saysOnce(t, doc, 0, "fields nested more than 64 deep", "(2 fields)")
	if !contains(doc, "AFTERMARK") || !contains(doc, "F0") {
		t.Errorf("blocks %v", doc.Parts[0].Blocks)
	}
}

// Document variables past 16384 are said on the stored part.
func TestDocumentVariablesCap(t *testing.T) {
	var vars strings.Builder
	for i := range 1<<14 + 2 {
		fmt.Fprintf(&vars, `<w:docVar w:name="v%d" w:val="x"/>`, i)
	}
	vars.WriteString(`<w:docVar w:name="last" w:val="LASTVAR"/>`)
	pkg := ooxmltest.Docx(para(run(text("body"))))
	pkg.Rel("word/document.xml", ooxmltest.RelSettings, "settings.xml")
	pkg.Add("word/settings.xml", ooxmltest.CTSettings, `<w:settings`+ooxmltest.WordNS+`><w:docVars>`+vars.String()+`</w:docVars></w:settings>`)
	doc := capsExtract(t, pkg.Bytes())
	saysOnce(t, doc, 1, "document variables past 16384 were not read", "(3 variables)")
	if contains(doc, "LASTVAR") {
		t.Error("LASTVAR read")
	}
}

// A list label longer than 64 bytes is cut there, and said.
func TestListLabelCap(t *testing.T) {
	num := `<w:abstractNum w:abstractNumId="0"><w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:lvlText w:val="` +
		strings.Repeat("L", 70) + `%1."/></w:lvl></w:abstractNum><w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>`
	doc := capsExtract(t, ooxmltest.Docx(ooxmltest.ListPara(1, 0, "item")).WithNumbering(num).Bytes())
	saysOnce(t, doc, 0, "list labels longer than 64 bytes were cut there", "(1 label)")
}

// Chart and diagram references that lead nowhere are one warning, with a
// count and the first few, however many there are.
func TestMissingChartsSaidOnce(t *testing.T) {
	var body strings.Builder
	for i := range 5000 {
		fmt.Fprintf(&body, `<w:p><w:r><w:drawing><wp:inline><wp:extent cx="1" cy="1"/><wp:docPr id="%d" name="C"/>`+
			`<a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/chart">`+
			`<c:chart xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart" r:id="rIdMissing%d"/></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>`, i+1, i)
	}
	doc := capsExtract(t, ooxmltest.Docx(body.String()).Bytes())
	var said []string
	for _, w := range doc.AllWarnings() {
		if w.Code == gofastooxml.WarnPartDamaged {
			said = append(said, w.Message)
		}
	}
	if len(said) != 1 || !strings.HasPrefix(said[0], "5000 drawings name a chart or diagram the document does not have (the chart rIdMissing0, the chart rIdMissing1,") ||
		!strings.HasSuffix(said[0], ", and 4992 more)") {
		t.Errorf("%q", said)
	}
}

// A PowerPoint table row of more than 16384 cells: the cells past it are
// said on the slide.
func TestSlideTableColumnsCap(t *testing.T) {
	var cells strings.Builder
	for range 1<<14 + 1 {
		cells.WriteString(`<a:tc><a:txBody><a:bodyPr/><a:p><a:r><a:t>c</a:t></a:r></a:p></a:txBody><a:tcPr/></a:tc>`)
	}
	frame := `<p:graphicFrame><p:nvGraphicFramePr><p:cNvPr id="2" name="Table 2"/><p:cNvGraphicFramePr/><p:nvPr/></p:nvGraphicFramePr>` +
		`<p:xfrm><a:off x="0" y="0"/><a:ext cx="100000" cy="100000"/></p:xfrm>` +
		`<a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/table"><a:tbl><a:tblGrid/>` +
		`<a:tr h="100">` + cells.String() + `</a:tr></a:tbl></a:graphicData></a:graphic></p:graphicFrame>`
	doc := capsExtract(t, ooxmltest.Pptx(ooxmltest.Slide{Shapes: frame}).Bytes())
	saysOnce(t, doc, 0, "table cells past column 16384 were not read", "(1 cell)")
	// A slide whose colour map turns out not to be the one assumed is read
	// again; what is left out is counted once.
	inverted := `<p:clrMapOvr><a:overrideClrMapping bg1="dk1" tx1="lt1" bg2="dk2" tx2="lt2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/></p:clrMapOvr>`
	doc = capsExtract(t, ooxmltest.Pptx(ooxmltest.Slide{Shapes: frame, After: inverted}).Bytes())
	saysOnce(t, doc, 0, "table cells past column 16384 were not read", "(1 cell)")
}

// Cells and rows outside a sheet's grid are said on the sheet.
func TestSheetGridCap(t *testing.T) {
	data := `<row r="1"><c r="XFD1" t="inlineStr"><is><t>last</t></is></c><c t="inlineStr"><is><t>past</t></is></c><c/></row>` +
		`<row r="1048577"><c r="A1048577" t="inlineStr"><is><t>below</t></is></c></row>`
	doc := capsExtract(t, ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: data}).Bytes())
	saysOnce(t, doc, 0, "cells past column 16384 of the sheet were not read", "(1 cell)")
	saysOnce(t, doc, 0, "rows past row 1048576 of the sheet were not read", "(1 row)")
	if contains(doc, "past") || contains(doc, "below") || !contains(doc, "last") {
		t.Error("the grid is not where it should be")
	}
}

// Drawing parts of a sheet past 16 are said.
func TestSheetDrawingPartsCap(t *testing.T) {
	var extra strings.Builder
	pkg := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: xlRow(1, xlNum("A1", "1"))})
	for i := range 18 {
		target := fmt.Sprintf("../drawings/drawing%d.xml", i+1)
		id := pkg.Rel(firstSheet, "http://schemas.openxmlformats.org/officeDocument/2006/relationships/drawing", target)
		fmt.Fprintf(&extra, `<drawing r:id="%s"/>`, id)
		pkg.Add(fmt.Sprintf("xl/drawings/drawing%d.xml", i+1), ctDrawing, drawingHeader+`</xdr:wsDr>`)
	}
	pkg.Add("xl/worksheets/sheet1.xml", ooxmltest.CTWorksheet,
		`<worksheet xmlns="`+ooxmltest.NSSheet+`" xmlns:r="`+ooxmltest.NSRel+`"><sheetData>`+xlRow(1, xlNum("A1", "1"))+`</sheetData>`+extra.String()+`</worksheet>`)
	doc := capsExtract(t, pkg.Bytes())
	saysOnce(t, doc, 0, "drawing parts of a sheet past 16 were not read", "(2 parts)")
}

// Defined names past 16384 are said on the stored part; a name longer
// than 512 bytes is cut there, and said.
func TestDefinedNamesCap(t *testing.T) {
	var names strings.Builder
	fmt.Fprintf(&names, `<definedName name="%s">S!$A$1</definedName>`, strings.Repeat("N", 600))
	for i := range 1 << 14 {
		fmt.Fprintf(&names, `<definedName name="n%d">S!$A$1</definedName>`, i)
	}
	pkg := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: xlRow(1, xlNum("A1", "1"))})
	pkg.Add("xl/workbook.xml", ooxmltest.CTWorkbook, `<workbook xmlns="`+ooxmltest.NSSheet+`" xmlns:r="`+ooxmltest.NSRel+`"><sheets><sheet name="S" sheetId="1" r:id="rId1"/></sheets><definedNames>`+names.String()+`</definedNames></workbook>`)
	doc := capsExtract(t, pkg.Bytes())
	stored := len(doc.Parts) - 1
	saysOnce(t, doc, stored, "defined names past 16384 were not read", "(1 name)")
	saysOnce(t, doc, stored, "longer than 512 bytes were cut there", "(1 name)")
}

// A file with nothing past a bound says nothing of one.
func TestNoCapsOnOrdinaryFiles(t *testing.T) {
	noLimits(t, capsExtract(t, ooxmltest.Docx(para(run(text("plain")))).Bytes()))
	noLimits(t, capsExtract(t, ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: xlRow(1, xlNum("A1", "1"))}).Bytes()))
	noLimits(t, capsExtract(t, ooxmltest.Pptx(ooxmltest.Slide{Shapes: ooxmltest.TextShape(2, 0, 0, 100, 100, ooxmltest.DrawPara("plain"))}).Bytes()))
}
