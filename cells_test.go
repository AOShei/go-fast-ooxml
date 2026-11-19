package gofastooxml_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// cellName names the cell at r, c of a table whose range starts at start
// ("B2:F40"): "C3" for 1, 1.
func cellName(start string, r, c int) string {
	ref := strings.SplitN(start, ":", 2)[0]
	col, row := 0, 0
	i := 0
	for i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z' {
		col = col*26 + int(ref[i]-'A'+1)
		i++
	}
	fmt.Sscan(ref[i:], &row)
	return colName(col+c-1) + fmt.Sprint(row+r)
}

// details returns, per sheet name, what Table.Cells says of each cell by
// its reference.
func details(doc *gofastcore.Document) map[string]map[string]gofastcore.CellDetail {
	out := map[string]map[string]gofastcore.CellDetail{}
	for _, p := range doc.Parts {
		if p.Kind != gofastcore.PartSheet {
			continue
		}
		m := map[string]gofastcore.CellDetail{}
		for _, b := range p.Blocks {
			if b.Table == nil {
				continue
			}
			for _, d := range b.Table.Cells {
				m[cellName(b.Loc.Range, d.Row, d.Col)] = d
			}
		}
		out[p.Name] = m
	}
	return out
}

// Formula text and cell hyperlinks agree with openpyxl's, over the
// workbooks of the corpus: shared formulas moved to each cell, array
// formulas in their first cell, links to addresses and to places in the
// workbook.
func TestExcelCellDetailsAsOpenpyxl(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the corpus")
	}
	if _, ok := ooxmltest.PythonBin(t); !ok {
		t.Skip("the Python comparison environment is not set up")
	}
	var contents [][]byte
	var names []string
	for _, f := range ooxmltest.LoadManifest(t, "corpus.json") {
		if f.Format != "xlsx" || f.Note != "" {
			continue
		}
		if data, err := os.ReadFile(f.AbsPath(t)); err == nil {
			contents, names = append(contents, data), append(names, f.Name)
		}
	}
	if len(contents) == 0 {
		t.Skip("no corpus files downloaded")
	}
	ooxmltest.WarmPython(t, "openpyxl-formulas", "xlsx", contents)
	formulas, links := [2]int{}, [2]int{} // agree, differ
	respaced := 0
	var differ []string
	for i, data := range contents {
		doc, err := extract(t, data, &gofastcore.Options{Cells: true, Spans: true})
		if err != nil {
			continue
		}
		ooxmltest.CheckDocument(t, doc)
		res := ooxmltest.Python(t, "openpyxl-formulas", data, "xlsx")
		if res.Error != "" {
			continue
		}
		var oracle []struct {
			Sheet    string            `json:"sheet"`
			Formulas map[string]string `json:"formulas"`
			Links    map[string]string `json:"links"`
		}
		if json.Unmarshal([]byte(res.Text), &oracle) != nil {
			continue
		}
		ours := details(doc)
		for _, sheet := range oracle {
			got, ok := ours[sheet.Sheet]
			if !ok {
				continue
			}
			// Only cells this library has: a formula whose result is
			// empty is not a cell of a table.
			for ref, d := range got {
				if want, ok := sheet.Formulas[ref]; ok || d.Formula != "" {
					switch {
					case d.Formula == want:
						formulas[0]++
					case squeezed(d.Formula) == squeezed(want):
						// openpyxl rebuilds a shared formula from its
						// tokens, and loses spaces and line breaks the
						// file has: kept here as the file has them.
						formulas[0]++
						respaced++
					default:
						formulas[1]++
						differ = append(differ, fmt.Sprintf("%s %s!%s formula %q, openpyxl %q", names[i], sheet.Sheet, ref, d.Formula, want))
					}
				}
				if want, ok := sheet.Links[ref]; ok || d.Href != "" {
					// An address is trimmed of the spaces around it.
					if d.Href == strings.TrimSpace(want) {
						links[0]++
					} else {
						links[1]++
						differ = append(differ, fmt.Sprintf("%s %s!%s link %q, openpyxl %q", names[i], sheet.Sheet, ref, d.Href, want))
					}
				}
			}
		}
	}
	sort.Strings(differ)
	t.Logf("formulas: %d agree (%d of them with spaces openpyxl leaves out), %d differ; links: %d agree, %d differ", formulas[0], respaced, formulas[1], links[0], links[1])
	for i, d := range differ {
		if i == 40 {
			t.Logf("... and %d more", len(differ)-i)
			break
		}
		t.Log(d)
	}
	if formulas[0] == 0 || links[0] == 0 || formulas[1] > 0 || links[1] > 0 {
		t.Errorf("formulas or links differ from openpyxl's")
	}
}

// squeezed is s without white space.
func squeezed(s string) string { return strings.Join(strings.Fields(s), "") }

// With Options.Cells a cell says what it holds besides its text: its
// formula, and the type of its value, with the value where the text does
// not show it in full. With Options.Spans, where it links to. Without
// them, nothing.
func TestExcelCellDetails(t *testing.T) {
	data := xlRow(1, xlStr("A1", "Item"), xlStr("B1", "Amount"), xlStr("C1", "Share"), xlStr("D1", "Due"), xlStr("E1", "Paid")) +
		xlRow(2, xlStr("A2", "Rent"), `<c r="B2"><f>B3*2</f><v>1200.5</v></c>`, `<c r="C2" s="1"><f t="shared" ref="C2:C3" si="0">B2/$B$4</f><v>0.33333333</v></c>`,
			`<c r="D2" s="2"><v>46085.5</v></c>`, `<c r="E2" t="b"><v>1</v></c>`) +
		xlRow(3, `<c r="A3" t="str"><f>UPPER("power")</f><v>POWER</v></c>`, xlNum("B3", "600.25"), `<c r="C3" s="1"><f t="shared" si="0"/><v>0.16666666</v></c>`,
			`<c r="D3" t="e"><f>1/0</f><v>#DIV/0!</v></c>`) +
		xlRow(4, xlStr("A4", "Total"), `<c r="B4"><f>SUM(B2:B3)</f><v>3601.5</v></c>`)
	pkg := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: data, Extra: `<hyperlinks><hyperlink ref="A2" r:id="rId9"/><hyperlink ref="A4" location="'Other sheet'!A1"/></hyperlinks>`}).
		WithSheetStyles(xlStyles(map[int]string{164: "0%"}, nil, nil, `numFmtId="0"`, `numFmtId="164"`, `numFmtId="14"`))
	for pkg.ExternalRel(firstSheet, ooxmltest.RelHyperlink, "https://example.org/rent") != "rId9" {
	}
	got := func(opts *gofastcore.Options) []string {
		doc := excel(t, pkg, opts)
		ooxmltest.CheckDocument(t, doc)
		var out []string
		for _, b := range doc.AllBlocks() {
			if b.Table == nil {
				continue
			}
			for _, d := range b.Table.Cells {
				out = append(out, fmt.Sprintf("%s href=%q formula=%q type=%s value=%q", cellName(b.Loc.Range, d.Row, d.Col), d.Href, d.Formula, d.Type, d.Value))
			}
		}
		return out
	}
	want := []string{
		`A2 href="https://example.org/rent" formula="" type= value=""`,
		`B2 href="" formula="B3*2" type=number value=""`,
		// 0.33333333 shown as 33%: the value says what the text does not.
		`C2 href="" formula="B2/$B$4" type=number value="0.33333333"`,
		`D2 href="" formula="" type=date value="46085.5"`,
		`E2 href="" formula="" type=bool value=""`,
		`A3 href="" formula="UPPER(\"power\")" type=text value=""`,
		`B3 href="" formula="" type=number value=""`,
		// The shared formula, moved to its cell.
		`C3 href="" formula="B3/$B$4" type=number value="0.16666666"`,
		`D3 href="" formula="1/0" type=error value=""`,
		`A4 href="#'Other sheet'!A1" formula="" type= value=""`,
		`B4 href="" formula="SUM(B2:B3)" type=number value=""`,
	}
	if g := got(&gofastcore.Options{Cells: true, Spans: true}); strings.Join(g, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(g, "\n"), strings.Join(want, "\n"))
	}
	if g := got(nil); len(g) != 0 {
		t.Errorf("details without asking: %q", g)
	}
	if g := got(&gofastcore.Options{Spans: true}); len(g) != 2 {
		t.Errorf("with Spans only: %q", g)
	}
	// The links are in Markdown; formulas are not.
	doc := excel(t, pkg, &gofastcore.Options{Cells: true, Spans: true})
	if md := doc.Markdown(); !strings.Contains(md, "[Rent](<https://example.org/rent>)") || strings.Contains(md, "B3*2") {
		t.Errorf("markdown:\n%s", md)
	}
}

// What a workbook stores and shows on no sheet, its defined names, is in
// a stored part after the sheets, flagged as hidden. It comes with the
// whole document only, and is not a sheet to read rows of.
func TestExcelStoredPart(t *testing.T) {
	pkg := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "A", Data: xlRow(1, xlStr("A1", "a"))}, ooxmltest.Sheet{Name: "B", Data: xlRow(1, xlStr("A1", "b"))})
	pkg.Add("xl/workbook.xml", ooxmltest.CTWorkbook, `<workbook xmlns="`+ooxmltest.NSSheet+`" xmlns:r="`+ooxmltest.NSRel+`"><sheets>`+
		`<sheet name="A" sheetId="1" r:id="rId1"/><sheet name="B" sheetId="2" r:id="rId2"/></sheets><definedNames>`+
		`<definedName name="Rate">0.2</definedName><definedName name="Note" hidden="1">"Ignore the figures on sheet B"</definedName>`+
		`<definedName name="_xlnm.Print_Area" localSheetId="1">B!$A$1:$C$9</definedName></definedNames></workbook>`)
	doc := excel(t, pkg, nil)
	expectSheets(t, doc,
		`sheet 1 "A"`, `  table A1:A1 inferred [["a"]]`,
		`sheet 2 "B"`, `  table A1:A1 inferred [["b"]]`,
		`stored 3 "" hidden=invisible`,
		`  paragraph  "Rate: 0.2" hidden=invisible name=Rate stored=defined_name`,
		"  paragraph  \"Note: \\\"Ignore the figures on sheet B\\\"\" hidden=invisible name=Note stored=defined_name",
		`  paragraph  "B!_xlnm.Print_Area: B!$A$1:$C$9" hidden=invisible name=B!_xlnm.Print_Area stored=defined_name`,
	)
	// A range of sheets does not bring it.
	if d := excel(t, pkg, &gofastcore.Options{Parts: gofastcore.PartRange{First: 1, Last: 3}}); len(d.Parts) != 2 {
		t.Errorf("a range gave %d parts", len(d.Parts))
	}
	f, err := gofastooxml.OpenBytes(pkg.Bytes(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.NumParts() != 3 || len(f.Sheets()) != 2 {
		t.Errorf("%d parts, %d sheets", f.NumParts(), len(f.Sheets()))
	}
	var kinds []string
	for p, err := range f.Parts(context.Background()) {
		if err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, p.Kind.String())
	}
	if strings.Join(kinds, " ") != "sheet sheet stored" {
		t.Errorf("parts one at a time: %v", kinds)
	}
	for _, err := range f.Rows(context.Background(), 3, 10) {
		if !errors.Is(err, gofastcore.ErrNoPartsSelected) {
			t.Errorf("rows of the stored part: %v", err)
		}
		break
	}
}
