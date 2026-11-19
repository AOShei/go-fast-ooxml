package gofastooxml_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// Behaviour of the Excel reader, each test on the smallest workbook that
// shows it.

const (
	relBase       = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/"
	ctTable       = "application/vnd.openxmlformats-officedocument.spreadsheetml.table+xml"
	ctComments    = "application/vnd.openxmlformats-officedocument.spreadsheetml.comments+xml"
	ctDrawing     = "application/vnd.openxmlformats-officedocument.drawing+xml"
	ctThreaded    = "application/vnd.ms-excel.threadedcomments+xml"
	ctPersons     = "application/vnd.ms-excel.person+xml"
	relThreaded   = "http://schemas.microsoft.com/office/2017/10/relationships/threadedComment"
	relPerson     = "http://schemas.microsoft.com/office/2017/10/relationships/person"
	nsThreaded    = "http://schemas.microsoft.com/office/spreadsheetml/2018/threadedcomments"
	nsSheetDraw   = "http://schemas.openxmlformats.org/drawingml/2006/spreadsheetDrawing"
	firstSheet    = "xl/worksheets/sheet1.xml"
	drawingHeader = `<xdr:wsDr xmlns:xdr="` + nsSheetDraw + `" xmlns:a="` + ooxmltest.NSDraw + `" xmlns:r="` + ooxmltest.NSRel + `" xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart" xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006">`
)

// Cells and rows, as a worksheet writes them.
func xlRow(n int, cells ...string) string {
	return `<row r="` + strconv.Itoa(n) + `">` + strings.Join(cells, "") + `</row>`
}
func xlNum(ref, v string) string { return `<c r="` + ref + `"><v>` + v + `</v></c>` }
func xlStyled(ref string, style int, v string) string {
	return fmt.Sprintf(`<c r="%s" s="%d"><v>%s</v></c>`, ref, style, v)
}
func xlStr(ref, s string) string { return ooxmltest.InlineCell(ref, s) }
func xlStyledStr(ref string, style int, s string) string {
	return fmt.Sprintf(`<c r="%s" s="%d" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, ref, style, ooxmltest.EscapeXML(s))
}

// xlStyles builds a styles part. Each of xfs is the attributes of one cell
// style, such as `numFmtId="164" fontId="1" fillId="2"`; a cell's s is its
// place in the list.
func xlStyles(numFmts map[int]string, fonts, fills []string, xfs ...string) string {
	var sb strings.Builder
	if len(numFmts) > 0 {
		ids := make([]int, 0, len(numFmts))
		for id := range numFmts {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		sb.WriteString(`<numFmts>`)
		for _, id := range ids {
			fmt.Fprintf(&sb, `<numFmt numFmtId="%d" formatCode="%s"/>`, id, ooxmltest.EscapeXML(numFmts[id]))
		}
		sb.WriteString(`</numFmts>`)
	}
	sb.WriteString(`<fonts><font><sz val="11"/><name val="Calibri"/></font>`)
	for _, f := range fonts {
		sb.WriteString(`<font>` + f + `</font>`)
	}
	sb.WriteString(`</fonts><fills><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill>`)
	for _, f := range fills {
		sb.WriteString(`<fill>` + f + `</fill>`)
	}
	sb.WriteString(`</fills><cellXfs>`)
	for _, x := range xfs {
		sb.WriteString(`<xf ` + x + `/>`)
	}
	sb.WriteString(`</cellXfs>`)
	return sb.String()
}

func solidFill(rgb string) string {
	return `<patternFill patternType="solid"><fgColor rgb="FF` + rgb + `"/><bgColor indexed="64"/></patternFill>`
}

// renderSheets writes a workbook's parts and blocks one to a line.
func renderSheets(doc *gofastcore.Document) []string {
	var out []string
	for p := range doc.Parts {
		part := &doc.Parts[p]
		line := fmt.Sprintf("%s %d %q", part.Kind, part.Number, part.Name)
		if part.Hidden != 0 {
			line += " hidden=" + part.Hidden.String()
		}
		out = append(out, line)
		for i := range part.Blocks {
			b := &part.Blocks[i]
			var sb strings.Builder
			sb.WriteString("  " + b.Kind.String() + " " + b.Loc.Range)
			if b.Origin == gofastcore.OriginInferred {
				sb.WriteString(" inferred")
			}
			if b.Text != "" {
				fmt.Fprintf(&sb, " %q", b.Text)
			}
			if b.Hidden != 0 {
				fmt.Fprintf(&sb, " hidden=%s", b.Hidden)
			}
			for _, sp := range b.Spans {
				fmt.Fprintf(&sb, " [%q %s]", b.Text[sp.Start:sp.End], sp.Hidden)
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
				if t.HeaderRows > 0 {
					fmt.Fprintf(&sb, " header=%d", t.HeaderRows)
				}
				fmt.Fprintf(&sb, " %q", t.Rows)
				if len(t.Merges) > 0 {
					fmt.Fprintf(&sb, " merges=%v", t.Merges)
				}
				for _, h := range t.Hidden {
					fmt.Fprintf(&sb, " hidden(%d,%d %dx%d %s)", h.Row, h.Col, h.RowSpan, h.ColSpan, h.Hidden)
				}
			}
			if img := b.Image; img != nil {
				fmt.Fprintf(&sb, " image(%s %s %dx%d)", img.ID, img.MIME, img.Width, img.Height)
			}
			out = append(out, sb.String())
		}
	}
	return out
}

func excel(t *testing.T, pkg *ooxmltest.Package, opts *gofastcore.Options) *gofastcore.Document {
	t.Helper()
	doc, err := extract(t, pkg.Bytes(), opts)
	if err != nil {
		t.Fatal(err)
	}
	ooxmltest.CheckDocument(t, doc)
	return doc
}

func expectSheets(t *testing.T, doc *gofastcore.Document, want ...string) {
	t.Helper()
	got := renderSheets(doc)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("sheets:\n got:\n  %s\n want:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestExcelCellKinds(t *testing.T) {
	data := xlRow(1, `<c r="A1" t="s"><v>0</v></c>`, `<c r="B1" t="s"><v>1</v></c>`, `<c r="C1" t="s"><v>2</v></c>`, `<c r="D1" t="s"><v>99</v></c>`) +
		xlRow(2, xlStr("A2", "  inline  "), `<c r="B2" t="str"><f>A1&amp;"!"</f><v>formula text</v></c>`, `<c r="C2" t="b"><v>1</v></c>`, `<c r="D2" t="b"><v>0</v></c>`) +
		xlRow(3, `<c r="A3" t="e"><v>#DIV/0!</v></c>`, `<c r="B3" t="d"><v>2026-03-04T14:30:00</v></c>`, xlNum("C3", "42"), xlNum("D3", "1E3")) +
		// A formula with a stored result is its result; cells with nothing to show are not cells.
		xlRow(4, `<c r="A4"><f>SUM(C3:D3)</f><v>1042</v></c>`, `<c r="B4" s="0"/>`, `<c r="C4"><v></v></c>`, xlNum("D4", "0.30000000000000004")) +
		// Neither the row nor its cells say where they are: they follow on.
		`<row><c><v>1</v></c><c t="inlineStr"><is><r><t>rich </t></r><r><rPr><b/></rPr><t>text</t></r><rPh sb="0" eb="1"><t>reading</t></rPh></is></c></row>`
	pkg := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "Kinds", Data: data})
	pkg.Rel("xl/workbook.xml", ooxmltest.RelSharedStrings, "sharedStrings.xml")
	pkg.Add("xl/sharedStrings.xml", ooxmltest.CTSharedStrings, `<sst xmlns="`+ooxmltest.NSSheet+`">`+
		`<si><t>plain</t></si><si><r><t>two </t></r><r><rPr><i/></rPr><t xml:space="preserve">runs </t></r><rPh sb="0" eb="1"><t>aid</t></rPh></si>`+
		`<si><t>line one_x000D__x000A_line two, an under_x005F_score, and _x263A_</t></si></sst>`)
	expectSheets(t, excel(t, pkg, nil),
		`sheet 1 "Kinds"`,
		`  table A1:D5 inferred [["plain" "two runs" "line one\nline two, an under_score, and ☺" ""] ["inline" "formula text" "TRUE" "FALSE"] ["#DIV/0!" "2026-03-04T14:30:00" "42" "1000"] ["1042" "" "" "0.3"] ["1" "rich text" "" ""]]`,
	)
}

func TestExcelNumberFormatsThroughStyles(t *testing.T) {
	styles := xlStyles(map[int]string{164: `#,##0.00;(#,##0.00)`, 165: `yyyy"年"m"月"d"日"`, 166: `"Ref "@`, 167: `[h]:mm`},
		nil, nil,
		`numFmtId="0"`, `numFmtId="164"`, `numFmtId="14"`, `numFmtId="165"`, `numFmtId="9"`, `numFmtId="166"`, `numFmtId="167"`, `numFmtId="49"`, `numFmtId="22"`, `numFmtId="999"`)
	data := xlRow(1, xlStyled("A1", 1, "-1234.5"), xlStyled("B1", 2, "46085"), xlStyled("C1", 3, "46085"), xlStyled("D1", 4, "0.125")) +
		xlRow(2, xlStyledStr("A2", 5, "X-1"), xlStyled("B2", 5, "12"), xlStyled("C2", 6, "1.5"), xlStyledStr("D2", 7, "007")) +
		xlRow(3, xlStyled("A3", 8, "46085.604166666664"), xlStyled("B3", 9, "2.5"), xlStyled("C3", 77, "2.5"), xlStyled("D3", 2, "0.75"))
	pkg := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "F", Data: data}).WithSheetStyles(styles)
	expectSheets(t, excel(t, pkg, nil),
		`sheet 1 "F"`,
		`  table A1:D3 inferred [["(1,234.50)" "2026-03-04" "2026-03-04" "13%"] ["Ref X-1" "Ref 12" "36:00" "007"] ["2026-03-04T14:30:00" "2.5" "2.5" "18:00:00"]]`,
	)

	// A workbook that counts days from 1904.
	old := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "F", Data: xlRow(1, xlStyled("A1", 2, "44623"), xlStyled("B1", 2, "0"))}).WithSheetStyles(styles)
	old.Add("xl/workbook.xml", ooxmltest.CTWorkbook, `<workbook xmlns="`+ooxmltest.NSSheet+`" xmlns:r="`+ooxmltest.NSRel+`"><workbookPr date1904="1"/><sheets><sheet name="F" sheetId="1" r:id="rId1"/></sheets></workbook>`)
	expectSheets(t, excel(t, old, nil),
		`sheet 1 "F"`,
		`  table A1:B1 inferred [["2026-03-04" "1904-01-01"]]`,
	)
}

// Blocks of cells with empty rows and columns between them are separate
// tables, each with its place on the sheet.
func TestExcelRegions(t *testing.T) {
	data := xlRow(1, xlStr("A1", "Left"), xlStr("B1", "table"), xlStr("D1", "Right"), xlStr("E1", "table")) +
		xlRow(2, xlNum("A2", "1"), xlNum("B2", "2"), xlNum("D2", "3"), xlNum("E2", "4")) +
		xlRow(3, xlNum("D3", "5"), xlNum("E3", "6")) +
		xlRow(5, xlStr("A5", "Below")) +
		xlRow(6, xlNum("A6", "7"), xlNum("C6", "8")) + // one table: no column between them is empty in both rows... B is
		xlRow(9, xlStr("H9", "alone"))
	expectSheets(t, excel(t, ooxmltest.Xlsx(ooxmltest.Sheet{Name: "R", Data: data}), nil),
		`sheet 1 "R"`,
		`  table A1:B2 inferred [["Left" "table"] ["1" "2"]]`,
		`  table D1:E3 inferred [["Right" "table"] ["3" "4"] ["5" "6"]]`,
		`  table A5:A6 inferred [["Below"] ["7"]]`,
		`  table C6:C6 inferred [["8"]]`,
		`  table H9:H9 inferred [["alone"]]`,
	)

	// A table with a gap inside it is one table: the gap is not empty all
	// the way across.
	gaps := xlRow(1, xlStr("A1", "a"), xlStr("C1", "c")) + xlRow(2, xlStr("B2", "b")) + xlRow(3, xlStr("A3", "d"))
	expectSheets(t, excel(t, ooxmltest.Xlsx(ooxmltest.Sheet{Name: "G", Data: gaps}), nil),
		`sheet 1 "G"`,
		`  table A1:C3 inferred [["a" "" "c"] ["" "b" ""] ["d" "" ""]]`,
	)
}

// Cells scattered so thinly that the rectangle around them is nearly all
// empty are not written as one grid.
func TestExcelSparseRegions(t *testing.T) {
	var rows strings.Builder
	// A diagonal: every row and column has a cell, so nothing separates
	// them, and the rectangle around them is 300 by 300 with 300 cells.
	for i := 1; i <= 300; i++ {
		rows.WriteString(xlRow(i, xlNum(colName(i-1)+strconv.Itoa(i), strconv.Itoa(i))))
	}
	doc := excel(t, ooxmltest.Xlsx(ooxmltest.Sheet{Name: "D", Data: rows.String()}), nil)
	part := &doc.Parts[0]
	if len(part.Blocks) != 300 || !part.Has(gofastooxml.WarnRegionSplit) {
		t.Fatalf("%d blocks, warnings %+v", len(part.Blocks), part.Warnings)
	}
	if b := part.Blocks[299]; b.Loc.Range != "KN300:KN300" || b.Table.Rows[0][0] != "300" {
		t.Errorf("last block = %s %q", b.Loc.Range, b.Table.Rows)
	}

	// The same number of cells in a block is one table.
	rows.Reset()
	for i := 1; i <= 30; i++ {
		var cells []string
		for j := 0; j < 10; j++ {
			cells = append(cells, xlNum(colName(j)+strconv.Itoa(i), "1"))
		}
		rows.WriteString(xlRow(i, cells...))
	}
	doc = excel(t, ooxmltest.Xlsx(ooxmltest.Sheet{Name: "D", Data: rows.String()}), nil)
	if len(doc.Parts[0].Blocks) != 1 || doc.Parts[0].Has(gofastooxml.WarnRegionSplit) {
		t.Errorf("a dense block: %d blocks", len(doc.Parts[0].Blocks))
	}
}

func TestExcelDeclaredTablesAndMerges(t *testing.T) {
	data := xlRow(1, xlStr("A1", "Title over the table")) +
		xlRow(2, xlStr("A2", "Region"), xlStr("B2", "Q1"), xlStr("C2", "Q2")) +
		xlRow(3, xlStr("A3", "North"), xlNum("B3", "10"), xlNum("C3", "12")) +
		xlRow(4, xlStr("A4", "South"), xlNum("B4", "7")) +
		xlRow(5, xlStr("A5", "next to the table's last row")) +
		xlRow(8, xlStr("A8", "Merged across"), xlStr("C8", "c")) +
		xlRow(9, xlStr("A9", "tall"), xlStr("B9", "x"), xlStr("C9", "y")) +
		xlRow(10, xlStr("B10", "z"))
	pkg := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "T", Data: data,
		Extra: `<mergeCells><mergeCell ref="A8:B8"/><mergeCell ref="A9:A10"/><mergeCell ref="E20:F21"/><mergeCell ref="C9:C30"/></mergeCells><tableParts><tablePart r:id="rId1"/><tablePart r:id="rId2"/></tableParts>`})
	pkg.Rel(firstSheet, relBase+"table", "../tables/table1.xml")
	pkg.Add("xl/tables/table1.xml", ctTable, `<table xmlns="`+ooxmltest.NSSheet+`" id="1" name="Table1" displayName="Sales" ref="A2:C4"><tableColumns count="3"/></table>`)
	// A second table that declares no header row.
	pkg.Rel(firstSheet, relBase+"table", "../tables/table2.xml")
	pkg.Add("xl/tables/table2.xml", ctTable, `<table xmlns="`+ooxmltest.NSSheet+`" id="2" name="Lower" displayName="Lower" ref="A8:C10" headerRowCount="0"/>`)
	expectSheets(t, excel(t, pkg, nil),
		`sheet 1 "T"`,
		`  table A1:A1 inferred [["Title over the table"]]`,
		// Declared: its own rectangle, an empty cell included, and its header row.
		// The name the workbook shows, not the one it was made with.
		`  table A2:C4 name=Sales header=1 [["Region" "Q1" "Q2"] ["North" "10" "12"] ["South" "7" ""]]`,
		`  table A5:A5 inferred [["next to the table's last row"]]`,
		// Merges are cut to the table; one whose first cell is empty is left out.
		`  table A8:C10 name=Lower [["Merged across" "" "c"] ["tall" "x" "y"] ["" "z" ""]] merges=[{0 0 1 2} {1 0 2 1} {1 2 2 1}]`,
	)
}

func TestExcelHiddenSheetsRowsAndColumns(t *testing.T) {
	grid := func() string {
		var sb strings.Builder
		for r := 1; r <= 6; r++ {
			attrs := ""
			switch r {
			case 2, 3:
				attrs = ` hidden="1"`
			case 5:
				attrs = ` ht="0" customHeight="1"`
			}
			fmt.Fprintf(&sb, `<row r="%d"%s>`, r, attrs)
			for c := 0; c < 5; c++ {
				sb.WriteString(xlStr(colName(c)+strconv.Itoa(r), colName(c)+strconv.Itoa(r)))
			}
			sb.WriteString(`</row>`)
		}
		return sb.String()
	}
	pkg := ooxmltest.Xlsx(
		ooxmltest.Sheet{Name: "Shown", Data: grid(), Extra: `<cols><col min="2" max="3" hidden="1"/><col min="5" max="5" width="0" customWidth="1"/><col min="9" max="12" hidden="1"/></cols>`},
		ooxmltest.Sheet{Name: "Hidden", State: "hidden", Data: xlRow(1, xlStr("A1", "on a hidden sheet"))},
		ooxmltest.Sheet{Name: "VeryHidden", State: "veryHidden", Data: xlRow(1, xlStr("A1", "on a sheet only a macro can show"))},
	)
	doc := excel(t, pkg, nil)
	expectSheets(t, doc,
		`sheet 1 "Shown"`,
		`  table A1:E6 inferred hidden=invisible [["A1" "B1" "C1" "D1" "E1"] ["A2" "B2" "C2" "D2" "E2"] ["A3" "B3" "C3" "D3" "E3"] ["A4" "B4" "C4" "D4" "E4"] ["A5" "B5" "C5" "D5" "E5"] ["A6" "B6" "C6" "D6" "E6"]]`+
			` hidden(1,0 2x5 invisible) hidden(4,0 1x5 invisible) hidden(0,1 6x2 invisible) hidden(0,4 6x1 invisible)`,
		`sheet 2 "Hidden" hidden=invisible`,
		`  table A1:A1 inferred hidden=invisible [["on a hidden sheet"]]`,
		`sheet 3 "VeryHidden" hidden=invisible`,
		`  table A1:A1 inferred hidden=invisible [["on a sheet only a macro can show"]]`,
	)
	for i := range doc.Parts {
		if !doc.Parts[i].Has(gofastcore.WarnTextHidden) {
			t.Errorf("sheet %d has no text.hidden warning", i+1)
		}
	}
	// The hidden text is in the text by default, and can be left out.
	var visible strings.Builder
	doc.WriteText(&visible, &gofastcore.RenderOptions{ExcludeHidden: gofastcore.HiddenAny})
	for _, cell := range []string{"A2", "B1", "C4", "E6", "A5", "hidden sheet", "macro"} {
		if !strings.Contains(doc.Text(), cell) {
			t.Errorf("%q is not in the text", cell)
		}
		if strings.Contains(visible.String(), cell) {
			t.Errorf("%q is in the visible text", cell)
		}
	}
	for _, cell := range []string{"A1", "D4", "D6"} {
		if !strings.Contains(visible.String(), cell) {
			t.Errorf("%q is missing from the visible text", cell)
		}
	}
}

func TestExcelCellsThatHideTheirText(t *testing.T) {
	styles := xlStyles(map[int]string{164: ";;;", 165: "0;;", 166: `"shown"`},
		[]string{
			`<sz val="11"/><color rgb="FFFFFFFF"/>`,        // 1 white
			`<sz val="1"/>`,                                // 2 one point
			`<sz val="11"/><color theme="0"/>`,             // 3 the theme's background
			`<sz val="11"/><color theme="1" tint="0.99"/>`, // 4 the theme's text colour, lightened to near white
			`<sz val="11"/><color indexed="9"/>`,           // 5 white in the old palette
			`<sz val="11"/><color rgb="FF1F3864"/>`,        // 6 navy
			`<sz val="11"/><color auto="1"/>`,              // 7 automatic
			`<sz val="11"/><color rgb="FFDDDDDD"/>`,        // 8 light grey: readable
			`<sz val="6"/>`,                                // 9 six points: readable
			`<sz val="1"/><color rgb="FFFFFFFF"/>`,         // 10 one point and white
		},
		[]string{solidFill("1F3864"), solidFill("FFFFFF"), `<patternFill patternType="darkGrid"><fgColor rgb="FF000000"/></patternFill>`, `<gradientFill><stop position="0"><color rgb="FFFFFFFF"/></stop></gradientFill>`},
		`numFmtId="0" fontId="0" fillId="0"`,    // 0 plain
		`numFmtId="0" fontId="1" fillId="0"`,    // 1 white on no fill
		`numFmtId="0" fontId="1" fillId="2"`,    // 2 white on navy
		`numFmtId="0" fontId="1" fillId="3"`,    // 3 white on white fill
		`numFmtId="0" fontId="2" fillId="0"`,    // 4 tiny
		`numFmtId="0" fontId="3" fillId="0"`,    // 5 theme background colour
		`numFmtId="0" fontId="4" fillId="0"`,    // 6 tinted to near white
		`numFmtId="0" fontId="5" fillId="0"`,    // 7 indexed white
		`numFmtId="0" fontId="6" fillId="2"`,    // 8 navy on navy
		`numFmtId="0" fontId="7" fillId="0"`,    // 9 automatic
		`numFmtId="0" fontId="1" fillId="4"`,    // 10 white on a pattern
		`numFmtId="0" fontId="1" fillId="5"`,    // 11 white on a gradient
		`numFmtId="164" fontId="0" fillId="0"`,  // 12 ;;;
		`numFmtId="165" fontId="0" fillId="0"`,  // 13 0;;
		`numFmtId="166" fontId="0" fillId="0"`,  // 14 "shown"
		`numFmtId="0" fontId="8" fillId="0"`,    // 15 light grey
		`numFmtId="0" fontId="9" fillId="0"`,    // 16 six points
		`numFmtId="164" fontId="10" fillId="3"`, // 17 everything at once
	)
	cells := []struct {
		style int
		text  string
	}{
		{0, "plain"}, {1, "white on the sheet"}, {2, "white on navy"}, {3, "white on a white fill"}, {4, "one point"},
		{5, "theme background"}, {6, "tinted near white"}, {7, "palette white"}, {8, "navy on navy"}, {9, "automatic"},
		{10, "white on a pattern"}, {11, "white on a gradient"}, {15, "light grey"}, {16, "six points"},
	}
	var rows strings.Builder
	for i, c := range cells {
		rows.WriteString(xlRow(i+1, xlStyledStr("A"+strconv.Itoa(i+1), c.style, c.text)))
	}
	n := len(cells)
	rows.WriteString(xlRow(n+1, xlStyled(fmt.Sprintf("A%d", n+1), 12, "1234.5")))          // a number blanked
	rows.WriteString(xlRow(n+2, xlStyledStr(fmt.Sprintf("A%d", n+2), 12, "blanked text"))) // text blanked
	rows.WriteString(xlRow(n+3, xlStyled(fmt.Sprintf("A%d", n+3), 13, "-5")))              // a negative number with an empty section
	rows.WriteString(xlRow(n+4, xlStyled(fmt.Sprintf("A%d", n+4), 13, "5")))               // shown
	rows.WriteString(xlRow(n+5, xlStyled(fmt.Sprintf("A%d", n+5), 14, "77")))              // replaced by text that is shown
	rows.WriteString(xlRow(n+6, xlStyled(fmt.Sprintf("A%d", n+6), 12, "0")))               // zero shown as nothing: an empty cell
	rows.WriteString(xlRow(n+7, xlStyledStr(fmt.Sprintf("A%d", n+7), 17, "all three")))
	pkg := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "H", Data: rows.String()}).WithSheetStyles(styles)
	pkg.Rel("xl/workbook.xml", relBase+"theme", "theme/theme1.xml")
	pkg.Add("xl/theme/theme1.xml", ooxmltest.CTTheme, `<a:theme xmlns:a="`+ooxmltest.NSDraw+`"><a:themeElements><a:clrScheme name="x"><a:dk1><a:sysClr val="windowText" lastClr="000000"/></a:dk1>`+
		`<a:lt1><a:sysClr val="window" lastClr="FFFFFF"/></a:lt1></a:clrScheme></a:themeElements></a:theme>`)
	doc := excel(t, pkg, nil)
	// The zero shown as nothing is an empty cell, so the column is two
	// tables with a gap where it was.
	got := map[string]string{}
	for _, b := range doc.Parts[0].Blocks {
		for r, row := range b.Table.Rows {
			got[row[0]] = ""
			for _, h := range b.Table.Hidden {
				if r >= h.Row && r < h.Row+h.RowSpan {
					got[row[0]] = strings.TrimPrefix(got[row[0]]+"+"+h.Hidden.String(), "+")
				}
			}
		}
	}
	want := map[string]string{
		"plain": "", "white on the sheet": "low_contrast", "white on navy": "", "white on a white fill": "low_contrast", "one point": "tiny",
		"theme background": "low_contrast", "tinted near white": "low_contrast", "palette white": "low_contrast", "navy on navy": "low_contrast", "automatic": "",
		"white on a pattern": "", "white on a gradient": "", "light grey": "", "six points": "",
		"1234.5": "invisible", "blanked text": "invisible", "-5": "invisible", "5": "", "shown": "", "all three": "invisible+tiny+low_contrast",
	}
	for text, reasons := range want {
		if have, ok := got[text]; !ok || have != reasons {
			t.Errorf("%q: hidden = %q (present %v), want %q", text, have, ok, reasons)
		}
	}
	if len(got) != len(want) {
		t.Errorf("cells: %v", renderSheets(doc))
	}
}

// Conditional formatting changes colours by value, which this library
// does not work out. White text in such a range is not judged; white text
// outside it still is.
func TestExcelConditionalFormattingIsNotJudged(t *testing.T) {
	styles := xlStyles(nil, []string{`<sz val="11"/><color rgb="FFFFFFFF"/>`, `<sz val="1"/><color rgb="FFFFFFFF"/>`}, nil,
		`numFmtId="0" fontId="0" fillId="0"`, `numFmtId="0" fontId="1" fillId="0"`, `numFmtId="0" fontId="2" fillId="0"`)
	data := xlRow(1, xlStyledStr("A1", 1, "white, outside"), xlStyledStr("B1", 1, "white, in a range"), xlStyledStr("C1", 1, "white, in another"), xlStyledStr("D1", 2, "white and tiny, in a range"), xlStyledStr("E1", 1, "white, in a newer kind"))
	extra := `<conditionalFormatting sqref="B1:B9 C1"><cfRule type="cellIs" dxfId="0" priority="1" operator="greaterThan"><formula>5</formula></cfRule></conditionalFormatting>` +
		`<conditionalFormatting sqref="D1"><cfRule type="colorScale" priority="2"/></conditionalFormatting>` +
		`<extLst><ext uri="{78C0D931-6437-407d-A8EE-F0AAD7539E65}"><x14:conditionalFormattings xmlns:x14="http://schemas.microsoft.com/office/spreadsheetml/2009/9/main">` +
		`<x14:conditionalFormatting xmlns:xm="http://schemas.microsoft.com/office/excel/2006/main"><x14:cfRule type="dataBar" id="{1}"/><xm:sqref>E1:E5</xm:sqref></x14:conditionalFormatting></x14:conditionalFormattings></ext></extLst>`
	expectSheets(t, excel(t, ooxmltest.Xlsx(ooxmltest.Sheet{Name: "C", Data: data, Extra: extra}).WithSheetStyles(styles), nil),
		`sheet 1 "C"`,
		`  table A1:E1 inferred hidden=tiny+low_contrast [["white, outside" "white, in a range" "white, in another" "white and tiny, in a range" "white, in a newer kind"]] hidden(0,0 1x1 low_contrast) hidden(0,3 1x1 tiny)`,
	)
}

// A column of a thousand cells in white is one region, not a thousand.
func TestExcelHiddenRegionsAreMerged(t *testing.T) {
	styles := xlStyles(nil, []string{`<sz val="11"/><color rgb="FFFFFFFF"/>`}, nil, `numFmtId="0" fontId="0" fillId="0"`, `numFmtId="0" fontId="1" fillId="0"`)
	var rows strings.Builder
	for r := 1; r <= 1000; r++ {
		rows.WriteString(xlRow(r, xlNum("A"+strconv.Itoa(r), "1"), xlStyled("B"+strconv.Itoa(r), 1, "2"), xlStyled("C"+strconv.Itoa(r), 1, "3"), xlNum("D"+strconv.Itoa(r), "4")))
	}
	rows.WriteString(xlRow(1001, xlStyled("A1001", 1, "5"), xlNum("B1001", "6"), xlStyled("C1001", 1, "7"), xlStyled("D1001", 1, "8")))
	doc := excel(t, ooxmltest.Xlsx(ooxmltest.Sheet{Name: "M", Data: rows.String()}).WithSheetStyles(styles), nil)
	var regions []string
	for _, h := range doc.Parts[0].Blocks[0].Table.Hidden {
		regions = append(regions, fmt.Sprintf("%d,%d %dx%d", h.Row, h.Col, h.RowSpan, h.ColSpan))
	}
	if got, want := strings.Join(regions, "; "), "0,1 1000x2; 1000,0 1x1; 1000,2 1x2"; got != want {
		t.Errorf("regions = %s, want %s", got, want)
	}
}

func TestExcelFormulasWithoutResults(t *testing.T) {
	data := xlRow(1, xlNum("A1", "1"), `<c r="B1"><f>A1*2</f></c>`, `<c r="C1"><f>A1*3</f><v></v></c>`, `<c r="D1" t="str"><f>"a"</f><v>a</v></c>`)
	doc := excel(t, ooxmltest.Xlsx(ooxmltest.Sheet{Name: "F", Data: data}), nil)
	expectSheets(t, doc, `sheet 1 "F"`, `  table A1:A1 inferred [["1"]]`, `  table D1:D1 inferred [["a"]]`)
	if w := doc.Parts[0].Warnings; len(w) != 1 || w[0].Code != gofastooxml.WarnFormulaNoValue || !strings.HasPrefix(w[0].Message, "2 ") {
		t.Errorf("warnings = %+v", w)
	}
}

func TestExcelComments(t *testing.T) {
	data := xlRow(1, xlStr("A1", "a"), xlStr("B1", "b")) + xlRow(2, xlStr("A2", "c"), xlStr("B2", "d"))
	pkg := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "C", Data: data})
	pkg.Rel(firstSheet, relBase+"comments", "../comments1.xml")
	pkg.Add("xl/comments1.xml", ctComments, `<comments xmlns="`+ooxmltest.NSSheet+`"><authors><author>Ada</author><author>tc={1}</author></authors><commentList>`+
		`<comment ref="B2" authorId="0"><text><r><rPr><b/></rPr><t>Ada:</t></r><r><t xml:space="preserve">&#10;check this figure</t></r></text></comment>`+
		`<comment ref="A1" authorId="1"><text><t>[Threaded comment] Your version of Excel allows you to read this threaded comment; however, any edits to it will get removed.</t></text></comment>`+
		`<comment ref="A2" authorId="7"><text><t>no such author</t></text></comment></commentList></comments>`)
	pkg.Rel(firstSheet, relThreaded, "../threadedComments/threadedComment1.xml")
	pkg.Add("xl/threadedComments/threadedComment1.xml", ctThreaded, `<ThreadedComments xmlns="`+nsThreaded+`">`+
		`<threadedComment ref="A1" dT="2026-03-04T10:00:00.00" personId="{P1}" id="{C1}"><text>Is this right?</text></threadedComment>`+
		`<threadedComment ref="A1" dT="2026-03-04T11:00:00.00" personId="{P2}" id="{C2}" parentId="{C1}"><text>Yes.</text></threadedComment></ThreadedComments>`)
	pkg.Rel("xl/workbook.xml", relPerson, "persons/person.xml")
	pkg.Add("xl/persons/person.xml", ctPersons, `<personList xmlns="`+nsThreaded+`"><person displayName="Grace Hopper" id="{P1}" userId="x" providerId="None"/><person displayName="Ada Lovelace" id="{P2}"/></personList>`)
	expectSheets(t, excel(t, pkg, nil),
		`sheet 1 "C"`,
		`  table A1:B2 inferred [["a" "b"] ["c" "d"]]`,
		// The thread, in order, in place of the stand-in an older program would show.
		`  note A1 "Is this right?" author=Grace Hopper date=2026-03-04T10:00:00.00 type=comment`,
		`  note A1 "Yes." author=Ada Lovelace date=2026-03-04T11:00:00.00 type=comment`,
		`  note A2 "no such author" type=comment`,
		`  note B2 "Ada:\ncheck this figure" author=Ada type=comment`,
	)
}

func TestExcelShapesAndPictures(t *testing.T) {
	anchor := func(col, row int, inner string) string {
		return fmt.Sprintf(`<xdr:twoCellAnchor><xdr:from><xdr:col>%d</xdr:col><xdr:colOff>0</xdr:colOff><xdr:row>%d</xdr:row><xdr:rowOff>0</xdr:rowOff></xdr:from>`+
			`<xdr:to><xdr:col>%d</xdr:col><xdr:colOff>0</xdr:colOff><xdr:row>%d</xdr:row><xdr:rowOff>0</xdr:rowOff></xdr:to>%s<xdr:clientData/></xdr:twoCellAnchor>`, col, row, col+3, row+4, inner)
	}
	shape := func(nv, props, style string, paras ...string) string {
		return `<xdr:sp><xdr:nvSpPr><xdr:cNvPr id="2" name="Shape"` + nv + `/><xdr:cNvSpPr/></xdr:nvSpPr><xdr:spPr>` + props + `</xdr:spPr>` + style +
			`<xdr:txBody><a:bodyPr/><a:lstStyle/>` + strings.Join(paras, "") + `</xdr:txBody></xdr:sp>`
	}
	para := func(runs ...string) string {
		return `<a:p>` + strings.Join(runs, "") + `<a:endParaRPr lang="en-GB"/></a:p>`
	}
	run := func(props, s string) string {
		return `<a:r><a:rPr lang="en-GB"` + props + `</a:rPr><a:t>` + ooxmltest.EscapeXML(s) + `</a:t></a:r>`
	}
	const white = `><a:solidFill><a:srgbClr val="FFFFFF"/></a:solidFill>`
	const themed = `<xdr:style><a:lnRef idx="2"><a:schemeClr val="accent1"/></a:lnRef><a:fillRef idx="1"><a:schemeClr val="accent1"/></a:fillRef><a:effectRef idx="0"><a:schemeClr val="accent1"/></a:effectRef><a:fontRef idx="minor"><a:schemeClr val="lt1"/></a:fontRef></xdr:style>`
	drawing := drawingHeader +
		anchor(5, 1, shape("", `<a:prstGeom prst="rect"/>`, themed, para(run(">", "A note in a box,"), `<a:br/>`, run(">", "on two lines.")), para(), para(run(white, "White in a shape its style fills.")))) +
		anchor(5, 8, shape("", `<a:noFill/>`, themed, para(run(">", "Seen, "), run(white, "white on the sheet"), run(">", ".")), para(run(` sz="100">`, "One point.")))) +
		anchor(5, 14, shape(` hidden="1"`, `<a:solidFill><a:srgbClr val="FFFF00"/></a:solidFill>`, "", para(run(">", "In a hidden shape.")))) +
		anchor(0, 20, `<xdr:pic><xdr:nvPicPr><xdr:cNvPr id="3" name="Picture 1" descr="A chart of sales"/><xdr:cNvPicPr/></xdr:nvPicPr><xdr:blipFill><a:blip r:embed="rId1"/></xdr:blipFill><xdr:spPr/></xdr:pic>`) +
		anchor(0, 30, `<xdr:graphicFrame><xdr:nvGraphicFramePr><xdr:cNvPr id="4" name="Chart 1"/></xdr:nvGraphicFramePr><a:graphic><a:graphicData><c:chart r:id="rId2"/></a:graphicData></a:graphic></xdr:graphicFrame>`) +
		// Two forms of one thing: the first is read, the second is not.
		`<mc:AlternateContent><mc:Choice Requires="x">` + anchor(0, 40, shape("", "", "", para(run(">", "The modern form.")))) + `</mc:Choice>` +
		`<mc:Fallback>` + anchor(0, 40, shape("", "", "", para(run(">", "The fallback form.")))) + `</mc:Fallback></mc:AlternateContent>` +
		`</xdr:wsDr>`
	pkg := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: xlRow(1, xlStr("A1", "cell")), Extra: `<drawing r:id="rId1"/>`})
	pkg.Rel(firstSheet, relBase+"drawing", "../drawings/drawing1.xml")
	pkg.Add("xl/drawings/drawing1.xml", ctDrawing, drawing)
	pkg.Rel("xl/drawings/drawing1.xml", relBase+"image", "../media/image1.png")
	pkg.Rel("xl/drawings/drawing1.xml", relBase+"chart", "../charts/chart1.xml")
	pkg.Add("xl/charts/chart1.xml", ctChart, quarterly)
	pkg.AddBytes("xl/media/image1.png", "image/png", pngBytes(t, 3, 2))
	pkg.Rel("xl/workbook.xml", relBase+"theme", "theme/theme1.xml")
	pkg.Add("xl/theme/theme1.xml", ooxmltest.CTTheme, `<a:theme xmlns:a="`+ooxmltest.NSDraw+`"><a:themeElements><a:clrScheme name="x"><a:accent1><a:srgbClr val="156082"/></a:accent1></a:clrScheme></a:themeElements></a:theme>`)

	doc := excel(t, pkg, &gofastcore.Options{Images: gofastcore.ImagesMeta})
	expectSheets(t, doc,
		`sheet 1 "S"`,
		`  table A1:A1 inferred [["cell"]]`,
		`  paragraph F2 "A note in a box,\non two lines."`,
		`  paragraph F2 "White in a shape its style fills."`,
		`  paragraph F9 "Seen, white on the sheet." hidden=low_contrast ["white on the sheet" low_contrast]`,
		`  paragraph F9 "One point." hidden=tiny`,
		`  paragraph F15 "In a hidden shape." hidden=invisible`,
		`  image A21 "A chart of sales" image(xl/media/image1.png image/png 3x2)`,
		// A chart is a caption and a table of its data at the cell its
		// frame is anchored to: not cells of the sheet.
		`  caption A31 "Sales"`,
		`  table A31 chart=bar name=Chart 1 role=chart x_axis=Quarter header=1 [["" "North" "South"] ["Q1" "10" "7"] ["Q2" "12.5" "9"]]`,
		`  paragraph A41 "The modern form."`,
	)
	if warned(doc, gofastooxml.WarnUnreadContent) {
		t.Errorf("the chart is reported as not read: %+v", doc.Warnings)
	}
	// Pictures not asked for are still known of.
	plain := excel(t, ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Extra: `<drawing r:id="rId1"/>`}).
		Add("xl/drawings/drawing1.xml", ctDrawing, drawingHeader+anchor(0, 0, `<xdr:pic><xdr:blipFill><a:blip r:embed="rId1"/></xdr:blipFill></xdr:pic>`)+`</xdr:wsDr>`), nil)
	_ = plain
}

// Rows and cells out of order, and a cell written twice, as some programs
// leave them.
func TestExcelUnorderedCells(t *testing.T) {
	data := xlRow(3, xlStr("B3", "f"), xlStr("A3", "e")) + xlRow(1, xlStr("A1", "a"), xlStr("B1", "b")) +
		xlRow(2, xlStr("A2", "c"), xlStr("B2", "d"), xlStr("A2", "again")) + xlRow(1, xlStr("C1", "late"))
	expectSheets(t, excel(t, ooxmltest.Xlsx(ooxmltest.Sheet{Name: "U", Data: data}), nil),
		`sheet 1 "U"`,
		`  table A1:C3 inferred [["a" "b" "late"] ["c" "d" ""] ["e" "f" ""]]`,
	)
	// A cell that names a place a sheet does not have keeps its text and
	// takes the place after the cell before it. A row past the last row
	// has nowhere to go. Attributes that mean nothing are passed over.
	odd := xlRow(1, xlStr("A1", "kept"), xlStr("XFE1", "column too far"), xlStr("A0", "row zero"), xlStr("1A", "not a place")) +
		`<row r="2"><c r="A2" s="x" t="zz"><v>7</v></c></row>` +
		`<row r="1048577">` + xlStr("A1048577", "row too far") + `</row>`
	expectSheets(t, excel(t, ooxmltest.Xlsx(ooxmltest.Sheet{Name: "O", Data: odd}), nil),
		`sheet 1 "O"`,
		`  table A1:D2 inferred [["kept" "column too far" "row zero" "not a place"] ["7" "" "" ""]]`,
	)
}

func TestExcelSheetSelectionAndLimits(t *testing.T) {
	book := func() *ooxmltest.Package {
		var sheets []ooxmltest.Sheet
		for i := 1; i <= 4; i++ {
			sheets = append(sheets, ooxmltest.Sheet{Name: "S" + strconv.Itoa(i), Data: xlRow(1, xlNum("A1", strconv.Itoa(i)), xlNum("B1", "0")) + xlRow(2, xlNum("A2", "1"), xlNum("B2", "2"))})
		}
		return ooxmltest.Xlsx(sheets...)
	}
	numbers := func(doc *gofastcore.Document) string {
		var out []string
		for _, p := range doc.Parts {
			out = append(out, strconv.Itoa(p.Number))
		}
		return strings.Join(out, ",")
	}
	// A range of sheets; numbers are places in the workbook.
	doc := excel(t, book(), &gofastcore.Options{Parts: gofastcore.PartRange{First: 2, Last: 3}})
	if numbers(doc) != "2,3" || doc.Parts[0].Name != "S2" {
		t.Errorf("parts = %s", numbers(doc))
	}
	_, err := extract(t, book().Bytes(), &gofastcore.Options{Parts: gofastcore.PartRange{First: 9, Last: 12}})
	if !errors.Is(err, gofastcore.ErrNoPartsSelected) {
		t.Errorf("a range past the last sheet: %v", err)
	}
	doc = excel(t, book(), &gofastcore.Options{Limits: gofastcore.Limits{MaxParts: 2}})
	if numbers(doc) != "1,2" || !warned(doc, gofastcore.WarnLimitExceeded) {
		t.Errorf("MaxParts: parts = %s, warnings = %+v", numbers(doc), doc.Warnings)
	}

	// The limit on cells, counted over all sheets: reading stops where it
	// is reached, and says so.
	data := book().Bytes()
	e := &gofastooxml.Extractor{MaxCells: 10}
	doc, err = e.Extract(context.Background(), strings.NewReader(string(data)), int64(len(data)), &gofastcore.Options{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	ooxmltest.CheckDocument(t, doc)
	kept := 0
	for i := range doc.Parts {
		for _, b := range doc.Parts[i].Blocks {
			for _, row := range b.Table.Rows {
				kept += len(row)
			}
		}
	}
	if kept != 10 || doc.Parts[1].Has(gofastcore.WarnLimitExceeded) || !doc.Parts[2].Has(gofastcore.WarnLimitExceeded) || !doc.Parts[3].Has(gofastcore.WarnLimitExceeded) {
		t.Errorf("MaxCells 10: kept %d cells; %v", kept, renderSheets(doc))
	}
}

// A long string that many cells refer to is small in the file and large in
// the result. The limit on text counts it every time it is used.
func TestExcelSharedStringAmplification(t *testing.T) {
	var rows strings.Builder
	for r := 1; r <= 2000; r++ {
		rows.WriteString(xlRow(r, `<c r="A`+strconv.Itoa(r)+`" t="s"><v>0</v></c>`))
	}
	pkg := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "A", Data: rows.String()}).WithSharedStrings(strings.Repeat("x", 1000))
	data := pkg.Bytes()
	// Two megabytes of text from a file of a few kilobytes, with a limit
	// of one megabyte on the text a document may hold.
	doc, err := extract(t, data, &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	ooxmltest.CheckDocument(t, doc)
	total := 0
	for _, b := range doc.Parts[0].Blocks {
		for _, row := range b.Table.Rows {
			total += len(row[0])
		}
	}
	if total > 1<<20 || total < 500_000 || !doc.Parts[0].Has(gofastcore.WarnLimitExceeded) {
		t.Errorf("%d bytes of text kept; warnings %+v", total, doc.Parts[0].Warnings)
	}
}

// The result does not depend on how many workers read the sheets.
func TestExcelWorkersGiveTheSameResult(t *testing.T) {
	var sheets []ooxmltest.Sheet
	for i := 1; i <= 12; i++ {
		var rows strings.Builder
		for r := 1; r <= 50+i; r++ {
			rows.WriteString(xlRow(r, xlNum("A"+strconv.Itoa(r), strconv.Itoa(r*i)), xlStr("B"+strconv.Itoa(r), fmt.Sprintf("sheet %d row %d", i, r))))
		}
		sheets = append(sheets, ooxmltest.Sheet{Name: "S" + strconv.Itoa(i), Data: rows.String()})
	}
	data := ooxmltest.Xlsx(sheets...).Bytes()
	one, err := extract(t, data, &gofastcore.Options{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join(renderSheets(one), "\n")
	for _, workers := range []int{0, 2, 5, 64} {
		doc, err := extract(t, data, &gofastcore.Options{Workers: workers})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(renderSheets(doc), "\n"); got != want {
			t.Errorf("with %d workers the result differs", workers)
		}
	}
}

func TestExcelOtherKindsOfSheet(t *testing.T) {
	pkg := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "Data", Data: xlRow(1, xlNum("A1", "1"))}, ooxmltest.Sheet{Name: "Empty"})
	// A sheet of old-style macros: its cells are read like any sheet's,
	// and its formulas, which are its code, are stored and flagged.
	macro := pkg.Rel("xl/workbook.xml", "http://schemas.microsoft.com/office/2006/relationships/xlMacrosheet", "macrosheets/sheet1.xml")
	pkg.Add("xl/macrosheets/sheet1.xml", "application/vnd.ms-excel.macrosheet+xml",
		`<xm:macrosheet xmlns="`+ooxmltest.NSSheet+`" xmlns:xm="http://schemas.microsoft.com/office/excel/2006/main"><sheetData>`+
			xlRow(1, xlStr("A1", "Auto_Open"), `<c r="B1"><f>EXEC("calc.exe")</f><v>0</v></c>`)+`</sheetData></xm:macrosheet>`)
	// A chart sheet, a sheet whose part is missing, and a link to another workbook.
	chart := pkg.Rel("xl/workbook.xml", relBase+"chartsheet", "chartsheets/sheet1.xml")
	pkg.Add("xl/chartsheets/sheet1.xml", "application/vnd.openxmlformats-officedocument.spreadsheetml.chartsheet+xml", `<chartsheet xmlns="`+ooxmltest.NSSheet+`" xmlns:r="`+ooxmltest.NSRel+`"><drawing r:id="rId1"/></chartsheet>`)
	pkg.Rel("xl/chartsheets/sheet1.xml", relBase+"drawing", "../drawings/drawing1.xml")
	pkg.Add("xl/drawings/drawing1.xml", ctDrawing, drawingHeader+`<xdr:absoluteAnchor><xdr:pos x="0" y="0"/><xdr:ext cx="8000000" cy="5000000"/>`+
		`<xdr:graphicFrame><xdr:nvGraphicFramePr><xdr:cNvPr id="2" name="Chart 1"/></xdr:nvGraphicFramePr><a:graphic><a:graphicData><c:chart r:id="rId1"/></a:graphicData></a:graphic></xdr:graphicFrame><xdr:clientData/></xdr:absoluteAnchor></xdr:wsDr>`)
	pkg.Rel("xl/drawings/drawing1.xml", relBase+"chart", "../charts/chart1.xml")
	pkg.Add("xl/charts/chart1.xml", ctChart, quarterly)
	missing := pkg.Rel("xl/workbook.xml", ooxmltest.RelWorksheet, "worksheets/gone.xml")
	pkg.Rel("xl/workbook.xml", relBase+"externalLink", "externalLinks/externalLink1.xml")
	pkg.Add("xl/workbook.xml", ooxmltest.CTWorkbook, `<workbook xmlns="`+ooxmltest.NSSheet+`" xmlns:r="`+ooxmltest.NSRel+`"><sheets>`+
		`<sheet name="Data" sheetId="1" r:id="rId1"/><sheet name="Empty" sheetId="2" r:id="rId2"/><sheet name="Chart" sheetId="3" r:id="`+chart+`"/>`+
		`<sheet name="Gone" sheetId="4" r:id="`+missing+`"/><sheet name="No part named" sheetId="5" r:id="rId99"/><sheet name="Macros" sheetId="6" r:id="`+macro+`"/></sheets></workbook>`)
	doc := excel(t, pkg, nil)
	expectSheets(t, doc,
		`sheet 1 "Data"`, `  table A1:A1 inferred [["1"]]`,
		`sheet 2 "Empty"`,
		`sheet 3 "Chart"`,
		`  caption  "Sales"`,
		`  table  chart=bar name=Chart 1 role=chart x_axis=Quarter header=1 [["" "North" "South"] ["Q1" "10" "7"] ["Q2" "12.5" "9"]]`,
		`sheet 4 "Gone"`,
		`sheet 5 "No part named"`,
		`sheet 6 "Macros"`, `  table A1:B1 inferred [["Auto_Open" "0"]]`,
		// The macro sheet's code is in the stored part, flagged.
		`stored 7 "" hidden=invisible`,
		"  code  \"B1\\t=EXEC(\\\"calc.exe\\\")\" hidden=invisible language=excel4 name=Macros stored=macro",
	)
	if !warned(doc, gofastcore.WarnInputActiveContent) {
		t.Errorf("the macro sheet is not reported: %+v", doc.Warnings)
	}
	if !doc.Parts[1].Has(gofastcore.WarnPartNoText) || !doc.Parts[3].Has(gofastcore.WarnPartFailed) || !doc.Parts[4].Has(gofastcore.WarnPartFailed) {
		t.Errorf("part warnings: %+v %+v %+v", doc.Parts[1].Warnings, doc.Parts[3].Warnings, doc.Parts[4].Warnings)
	}
	if warned(doc, gofastooxml.WarnUnreadContent) || !warned(doc, gofastcore.WarnInputExternalReference) {
		t.Errorf("document warnings: %+v", doc.Warnings)
	}
}

// A sheet whose XML breaks off keeps the rows before the break.
func TestExcelDamagedSheet(t *testing.T) {
	pkg := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "D"}, ooxmltest.Sheet{Name: "Sound", Data: xlRow(1, xlNum("A1", "9"))})
	pkg.Add(firstSheet, ooxmltest.CTWorksheet, `<worksheet xmlns="`+ooxmltest.NSSheet+`"><sheetData>`+xlRow(1, xlNum("A1", "1"))+xlRow(2, xlNum("A2", "2"))+`<row r="3"><c r="A3"><v>3</`)
	doc := excel(t, pkg, nil)
	expectSheets(t, doc, `sheet 1 "D"`, `  table A1:A2 inferred [["1"] ["2"]]`, `sheet 2 "Sound"`, `  table A1:A1 inferred [["9"]]`)
	if !doc.Parts[0].Has(gofastcore.WarnPartFailed) || doc.Parts[1].Has(gofastcore.WarnPartFailed) {
		t.Errorf("warnings: %+v", doc.Parts[0].Warnings)
	}
}

// A region too large for one table is written as several, each a run of
// its rows; the later ones say that they continue the one before.
func TestExcelVeryLargeRegionIsSplit(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a sheet of 1.2 million cells")
	}
	var rows strings.Builder
	const n = 600_000
	for r := 1; r <= n; r++ {
		rows.WriteString(`<row><c><v>`)
		rows.WriteString(strconv.Itoa(r))
		rows.WriteString(`</v></c><c><v>2</v></c></row>`)
	}
	doc := excel(t, ooxmltest.Xlsx(ooxmltest.Sheet{Name: "L", Data: rows.String()}), nil)
	blocks := doc.Parts[0].Blocks
	if len(blocks) != 2 || blocks[0].Loc.Range != "A1:B524288" || blocks[1].Loc.Range != "A524289:B600000" {
		t.Fatalf("%d blocks: %s, ...", len(blocks), blocks[0].Loc.Range)
	}
	if got := blocks[1].Table.Rows[len(blocks[1].Table.Rows)-1][0]; got != strconv.Itoa(n) {
		t.Errorf("last cell = %q", got)
	}
	if blocks[0].Continued || !blocks[1].Continued {
		t.Errorf("continued: %v, %v; want only the second piece", blocks[0].Continued, blocks[1].Continued)
	}
}

// A band of rows ends where it ends, table or no table. A table that goes
// on in the next band is marked there as continuing: it begins right
// under where the last one ended, over the same columns. A table that
// begins after a gap, or beside, is a table of its own.
func TestRowsMarkATableCarriedOverABand(t *testing.T) {
	var rows strings.Builder
	for r := 1; r <= 10; r++ { // one table of ten rows
		n := strconv.Itoa(r)
		rows.WriteString(xlRow(r, xlNum("A"+n, n), xlStr("B"+n, "row "+n)))
	}
	rows.WriteString(xlRow(13, xlStr("A13", "after a gap"), xlStr("B13", "x"))) // row 12 is empty
	rows.WriteString(xlRow(14, xlStr("A14", "second table"), xlStr("B14", "y")))
	rows.WriteString(xlRow(15, xlStr("D15", "beside"), xlStr("E15", "z")))
	f, err := gofastooxml.OpenBytes(ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: rows.String()}).Bytes(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for band, err := range f.Rows(context.Background(), 1, 4) {
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range band.Blocks {
			s := b.Loc.Range
			if b.Continued {
				s += " continued"
			}
			got = append(got, s)
		}
		got = append(got, "|")
	}
	// Bands of four rows: 1-4, 5-8, then 9, 10, 13, 14, then 15.
	want := "A1:B4 | A5:B8 continued | A9:B10 continued A13:B14 | D15:E15 |"
	if strings.Join(got, " ") != want {
		t.Errorf("bands: %s; want: %s", strings.Join(got, " "), want)
	}
}

// A link on the text of a shape on a sheet is a span, as it is on a slide.
func TestExcelLinksInShapes(t *testing.T) {
	drawing := drawingHeader +
		`<xdr:twoCellAnchor><xdr:from><xdr:col>1</xdr:col><xdr:colOff>0</xdr:colOff><xdr:row>1</xdr:row><xdr:rowOff>0</xdr:rowOff></xdr:from>` +
		`<xdr:to><xdr:col>4</xdr:col><xdr:colOff>0</xdr:colOff><xdr:row>4</xdr:row><xdr:rowOff>0</xdr:rowOff></xdr:to>` +
		`<xdr:sp><xdr:nvSpPr><xdr:cNvPr id="2" name="Shape"/><xdr:cNvSpPr/></xdr:nvSpPr><xdr:spPr/><xdr:txBody><a:bodyPr/><a:lstStyle/>` +
		`<a:p><a:r><a:rPr lang="en-GB"/><a:t>See </a:t></a:r><a:r><a:rPr lang="en-GB"><a:hlinkClick r:id="rId1"/></a:rPr><a:t>the source</a:t></a:r></a:p>` +
		`</xdr:txBody></xdr:sp><xdr:clientData/></xdr:twoCellAnchor></xdr:wsDr>`
	pkg := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: xlRow(1, xlStr("A1", "cell")), Extra: `<drawing r:id="rId1"/>`})
	pkg.Rel(firstSheet, relBase+"drawing", "../drawings/drawing1.xml")
	pkg.Add("xl/drawings/drawing1.xml", ctDrawing, drawing)
	pkg.ExternalRel("xl/drawings/drawing1.xml", ooxmltest.RelHyperlink, "https://example.org/source")
	doc := excel(t, pkg, &gofastcore.Options{Spans: true})
	var found bool
	for _, b := range doc.AllBlocks() {
		for _, sp := range b.Spans {
			if sp.Href == "https://example.org/source" && b.Text[sp.Start:sp.End] == "the source" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("the link on the shape's text is not a span: %s", asJSON(t, doc.Parts))
	}
	ooxmltest.CheckDocument(t, doc)
}

// Text in a declared table is judged against the fill its table style
// gives the cell, built in (from the standard) or the workbook's own: white
// text on a table's dark header is seen; white text where the style leaves
// the cell white is not.
func TestExcelTableStyles(t *testing.T) {
	const themeXML = `<a:theme xmlns:a="` + ooxmltest.NSDraw + `" name="x"><a:themeElements><a:clrScheme name="x">` +
		`<a:dk1><a:srgbClr val="000000"/></a:dk1><a:lt1><a:srgbClr val="FFFFFF"/></a:lt1><a:dk2><a:srgbClr val="44546A"/></a:dk2><a:lt2><a:srgbClr val="E7E6E6"/></a:lt2>` +
		`<a:accent1><a:srgbClr val="1F3864"/></a:accent1></a:clrScheme></a:themeElements></a:theme>`
	white := `<sz val="11"/><color rgb="FFFFFFFF"/><name val="Calibri"/>`
	black := `<sz val="11"/><color rgb="FF000000"/><name val="Calibri"/>`
	rows := func(r0 int) string {
		var sb strings.Builder
		// A header row, then three rows; column B is in white.
		sb.WriteString(xlRow(r0, xlStr(fmt.Sprintf("A%d", r0), "Region"), xlStyledStr(fmt.Sprintf("B%d", r0), 1, "Sales")))
		for i, name := range []string{"North", "South", "East"} {
			r := r0 + 1 + i
			sb.WriteString(xlRow(r, xlStr(fmt.Sprintf("A%d", r), name), xlStyledStr(fmt.Sprintf("B%d", r), 1, "hidden?")))
		}
		return sb.String()
	}
	table := func(n int, ref, style string) string {
		return fmt.Sprintf(`<table xmlns="%s" id="%d" name="T%d" displayName="T%d" ref="%s"><tableColumns count="2"/>`+
			`<tableStyleInfo name="%s" showFirstColumn="0" showLastColumn="0" showRowStripes="1" showColumnStripes="0"/></table>`, ooxmltest.NSSheet, n, n, n, ref, style)
	}
	styles := xlStyles(nil, []string{white, black}, nil, `numFmtId="0"`, `numFmtId="0" fontId="1"`, `numFmtId="0" fontId="2"`) +
		// The workbook's own style: a black header, white rows.
		`<dxfs count="2"><dxf><fill><patternFill><bgColor rgb="FF000000"/></patternFill></fill></dxf><dxf><fill><patternFill><bgColor rgb="FFFFFFFF"/></patternFill></fill></dxf></dxfs>` +
		`<tableStyles count="1"><tableStyle name="Ours" pivot="0" count="2"><tableStyleElement type="headerRow" dxfId="0"/><tableStyleElement type="wholeTable" dxfId="1"/></tableStyle></tableStyles>`
	pkg := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: rows(1) + rows(11),
		Extra: `<tableParts count="2"><tablePart r:id="rId1"/><tablePart r:id="rId2"/></tableParts>`}).WithSheetStyles(styles)
	pkg.Rel(firstSheet, relBase+"table", "../tables/table1.xml")
	pkg.Add("xl/tables/table1.xml", ctTable, table(1, "A1:B4", "TableStyleMedium2"))
	pkg.Rel(firstSheet, relBase+"table", "../tables/table2.xml")
	pkg.Add("xl/tables/table2.xml", ctTable, table(2, "A11:B14", "Ours"))
	pkg.Rel("xl/workbook.xml", relBase+"theme", "theme/theme1.xml")
	pkg.Add("xl/theme/theme1.xml", ooxmltest.CTTheme, themeXML)
	doc := excel(t, pkg, nil)
	var got []string
	for _, b := range doc.AllBlocks() {
		if b.Table == nil {
			continue
		}
		cells := map[[2]int]gofastcore.Hidden{}
		for _, h := range b.Table.Hidden {
			for r := h.Row; r < h.Row+h.RowSpan; r++ {
				for c := h.Col; c < h.Col+h.ColSpan; c++ {
					cells[[2]int{r, c}] |= h.Hidden
				}
			}
		}
		for r, row := range b.Table.Rows {
			got = append(got, fmt.Sprintf("%s %s %v", b.Loc.Range, row[1], cells[[2]int{r, 1}]&gofastcore.HiddenLowContrast != 0))
		}
	}
	want := []string{
		// TableStyleMedium2: the header is filled with accent 1, dark;
		// the first stripe with accent 1 lightened 80%, a pale blue that
		// white text can still be told from; the second not at all.
		"A1:B4 Sales false", "A1:B4 hidden? false", "A1:B4 hidden? true", "A1:B4 hidden? false",
		// The workbook's own: a black header, white rows.
		"A11:B14 Sales false", "A11:B14 hidden? true", "A11:B14 hidden? true", "A11:B14 hidden? true",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got\n  %q\nwant\n  %q", got, want)
	}
	ooxmltest.CheckDocument(t, doc)
}

// A drop-down list is a note of type choices at the cells it is for: the
// values of a list written out, one per line, or the reference a list is
// taken from. A list whose arrow is hidden, or whose cells are all
// hidden, is flagged; validations that are not lists offer no choices.
func TestExcelDropDownLists(t *testing.T) {
	data := xlRow(1, xlStr("A1", "Answer"), xlStr("B1", "Size"), xlStr("D1", "S")) +
		xlRow(2, xlStr("D2", "M")) + xlRow(3, xlStr("D3", "L"))
	extra := `<cols><col min="6" max="7" width="0" customWidth="1"/></cols>` +
		`<dataValidations count="5">` +
		`<dataValidation type="list" allowBlank="1" sqref="A2:A10"><formula1>"Yes,No,""Maybe"", sure,,"</formula1></dataValidation>` +
		`<dataValidation type="list" sqref="C5 B2:B10"><formula1>$D$1:$D$3</formula1></dataValidation>` +
		`<dataValidation type="list" showDropDown="1" sqref="H1"><formula1>"secret,list"</formula1></dataValidation>` +
		`<dataValidation type="list" sqref="F1:G4"><formula1>"in,hidden,columns"</formula1></dataValidation>` +
		`<dataValidation type="whole" operator="between" sqref="I1"><formula1>1</formula1><formula2>9</formula2></dataValidation>` +
		`</dataValidations>` +
		`<extLst><ext uri="{CCE6A557-97BC-4b89-ADB6-D9C93CAAB3DF}" xmlns:x14="http://schemas.microsoft.com/office/spreadsheetml/2009/9/main">` +
		`<x14:dataValidations count="1" xmlns:xm="http://schemas.microsoft.com/office/excel/2006/main"><x14:dataValidation type="list" allowBlank="1">` +
		`<x14:formula1><xm:f>Lists!$A$1:$A$3</xm:f></x14:formula1><xm:sqref>E1:E3</xm:sqref></x14:dataValidation></x14:dataValidations></ext></extLst>`
	doc := excel(t, ooxmltest.Xlsx(ooxmltest.Sheet{Name: "Form", Data: data, Extra: extra}), nil)
	expectSheets(t, doc,
		`sheet 1 "Form"`,
		`  table A1:B1 inferred [["Answer" "Size"]]`,
		`  table D1:D3 inferred [["S"] ["M"] ["L"]]`,
		`  note E1:E3 "=Lists!$A$1:$A$3" type=choices`,
		`  note F1:G4 "in\nhidden\ncolumns" hidden=invisible type=choices`,
		`  note H1 "secret\nlist" hidden=invisible type=choices`,
		`  note A2:A10 "Yes\nNo\n\"Maybe\"\nsure" type=choices`,
		`  note C5 B2:B10 "=$D$1:$D$3" type=choices`,
	)
	if md := doc.Markdown(); !strings.Contains(md, "Yes\nNo") {
		t.Errorf("the values are not in the Markdown:\n%s", md)
	}
	// Reading by bands leaves them out, as it does comments.
	f, err := gofastooxml.OpenBytes(ooxmltest.Xlsx(ooxmltest.Sheet{Name: "Form", Data: data, Extra: extra}).Bytes(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for p, err := range f.Rows(context.Background(), 1, 100) {
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range p.Blocks {
			if b.Kind == gofastcore.KindNote {
				t.Errorf("Rows gave a note: %q", b.Text)
			}
		}
	}
}
