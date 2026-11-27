package gofastooxml_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// cellsShown lists what each sheet of a document shows in its tables, a
// cell at a time ("Sheet!R1C1=text"), with the sheet's display and its
// merged ranges: the reading of a workbook that does not depend on how
// its cells are cut into tables. A sheet of the reference with a pivot
// table is left out of both (its cells are the pivot table's block, not
// cells); one with a declared table has its merged ranges left out (a
// declared table bounds the inferred ones beside it differently).
func cellsShown(doc, reference *gofastcore.Document) []string {
	pivot, declared := map[string]bool{}, map[string]bool{}
	for _, p := range reference.Parts {
		for _, b := range p.Blocks {
			switch {
			case b.Kind != gofastcore.KindTable:
			case b.Attrs[gofastcore.AttrRole] == "pivot":
				pivot[p.Name] = true
			case b.Origin != gofastcore.OriginInferred && b.Attrs[gofastcore.AttrRole] == "":
				declared[p.Name] = true
			}
		}
	}
	var out []string
	for _, p := range doc.Parts {
		if p.Kind != gofastcore.PartSheet || pivot[p.Name] {
			continue
		}
		out = append(out, fmt.Sprintf("%s: %s", p.Name, p.Display))
		for _, b := range p.Blocks {
			if b.Kind != gofastcore.KindTable || b.Table == nil || b.Attrs[gofastcore.AttrRole] != "" {
				continue // not cells: the data of a chart, a pivot table
			}
			r0, c0 := rangeOrigin(b.Loc.Range)
			for i, row := range b.Table.Rows {
				for j, text := range row {
					if text != "" {
						out = append(out, fmt.Sprintf("%s!R%dC%d=%s", p.Name, r0+i+1, c0+j+1, text))
					}
				}
			}
			if declared[p.Name] {
				continue
			}
			for _, m := range b.Table.Merges {
				out = append(out, fmt.Sprintf("%s!merge R%dC%d %dx%d", p.Name, r0+m.Row+1, c0+m.Col+1, m.RowSpan, m.ColSpan))
			}
			for _, d := range b.Table.Display {
				out = append(out, fmt.Sprintf("%s!display R%dC%d %dx%d %s", p.Name, r0+d.Row+1, c0+d.Col+1, d.RowSpan, d.ColSpan, d.Display))
			}
		}
	}
	sort.Strings(out)
	return out
}

// rangeOrigin reads the first cell of a range such as "B3:D9": row and
// column, from 0.
func rangeOrigin(ref string) (row, col int) {
	ref, _, _ = strings.Cut(ref, ":")
	i := 0
	for i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z' {
		col = col*26 + int(ref[i]-'A') + 1
		i++
	}
	fmt.Sscan(ref[i:], &row)
	return row - 1, col - 1
}

// TestSpreadsheetMLReadsAsXlsx: a workbook written as SpreadsheetML 2003
// shows, sheet by sheet, the cells, merged ranges and hidden rows and
// columns the .xlsx it was written from shows, for every workbook of the
// fixtures and of the corpus that the harness can write
// (ooxmltest.SpreadsheetML2003).
func TestSpreadsheetMLReadsAsXlsx(t *testing.T) {
	root := ooxmltest.Root(t)
	var paths []string
	for _, dir := range []string{"testdata/fixtures", "testdata/corpus"} {
		if testing.Short() && dir == "testdata/corpus" {
			continue
		}
		filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return nil
			case d.IsDir() && d.Name() == ".git":
				return filepath.SkipDir
			case !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".xlsx"):
				paths = append(paths, path)
			}
			return nil
		})
	}
	sort.Strings(paths)
	var (
		mu                     sync.Mutex
		same, skipped, differs int
		next                   = make(chan string)
		wg                     sync.WaitGroup
	)
	ctx := context.Background()
	opts := &gofastcore.Options{Workers: 1}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range next {
				data, err := os.ReadFile(path)
				if err != nil || len(data) > 16<<20 {
					continue
				}
				name, _ := filepath.Rel(root, path)
				skip, diff := "", ""
				zipped, err := gofastooxml.Extract(ctx, bytes.NewReader(data), int64(len(data)), opts)
				var ss []byte
				switch {
				case err != nil:
					skip = "refused"
				case damagedReading(zipped):
					skip = "read with damage"
				default:
					var ok bool
					if ss, ok = ooxmltest.SpreadsheetML2003(data); !ok {
						skip = "not written"
					}
				}
				if skip == "" {
					doc, err := gofastooxml.Extract(ctx, bytes.NewReader(ss), int64(len(ss)), opts)
					if err != nil {
						diff = "refused: " + err.Error()
					} else {
						ooxmltest.CheckDocument(t, doc)
						diff = firstDifference(cellsShown(zipped, zipped), cellsShown(doc, zipped))
					}
				}
				mu.Lock()
				switch {
				case skip != "":
					skipped++
				case diff == "":
					same++
				default:
					differs++
					if differs <= 25 {
						t.Errorf("%s: as SpreadsheetML 2003: %s", name, diff)
					}
				}
				mu.Unlock()
			}
		}()
	}
	for _, p := range paths {
		next <- p
	}
	close(next)
	wg.Wait()
	t.Logf("%d workbooks: %d show the same as SpreadsheetML 2003, %d differ, %d not comparable", len(paths), same, differs, skipped)
}

// firstDifference describes where two sorted lists first differ, or is
// empty if they do not.
func firstDifference(want, got []string) string {
	i, j := 0, 0
	for i < len(want) && j < len(got) {
		switch {
		case want[i] == got[j]:
			i++
			j++
		case want[i] < got[j]:
			return fmt.Sprintf("missing %q (of %d; %d read)", want[i], len(want), len(got))
		default:
			return fmt.Sprintf("extra %q (of %d; %d read)", got[j], len(want), len(got))
		}
	}
	if i < len(want) {
		return fmt.Sprintf("missing %q (of %d; %d read)", want[i], len(want), len(got))
	}
	if j < len(got) {
		return fmt.Sprintf("extra %q (of %d; %d read)", got[j], len(want), len(got))
	}
	return ""
}

// damagedReading reports whether a document was read past damage or a
// limit, where two ways of reading it need not meet.
func damagedReading(doc *gofastcore.Document) bool {
	check := func(ws []gofastcore.Warning) bool {
		for _, w := range ws {
			switch w.Code {
			case gofastcore.WarnInputStreamDamaged, gofastcore.WarnInputRepaired, gofastcore.WarnPartFailed, gofastcore.WarnLimitExceeded, gofastcore.WarnInputAmbiguous:
				return true
			}
		}
		return false
	}
	if check(doc.Warnings) {
		return true
	}
	for _, p := range doc.Parts {
		if check(p.Warnings) {
			return true
		}
	}
	return false
}

// ssBook wraps worksheets, and anything else at the top of a workbook, in
// a SpreadsheetML 2003 workbook.
func ssBook(body string) []byte {
	return []byte(`<?xml version="1.0"?>
<?mso-application progid="Excel.Sheet"?>
<Workbook xmlns="urn:schemas-microsoft-com:office:spreadsheet" xmlns:o="urn:schemas-microsoft-com:office:office"
 xmlns:x="urn:schemas-microsoft-com:office:excel" xmlns:ss="urn:schemas-microsoft-com:office:spreadsheet"
 xmlns:html="http://www.w3.org/TR/REC-html40">` + body + `</Workbook>`)
}

func readSS(t *testing.T, data []byte, opts *gofastcore.Options) (*gofastcore.Document, string) {
	t.Helper()
	doc, err := gofastooxml.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts)
	if err != nil {
		t.Fatal(err)
	}
	ooxmltest.CheckDocument(t, doc)
	out, _ := json.Marshal(doc)
	return doc, string(out)
}

func TestSpreadsheetMLValues(t *testing.T) {
	data := ssBook(`
 <DocumentProperties xmlns="urn:schemas-microsoft-com:office:office">
  <Title>Prices</Title><Author>A. Writer</Author><LastAuthor>B. Editor</LastAuthor>
  <Created>2024-01-02T03:04:05Z</Created><Company>Firm</Company><Version>11.9999</Version>
 </DocumentProperties>
 <CustomDocumentProperties xmlns="urn:schemas-microsoft-com:office:office"><Project_x0020_Code>X-1</Project_x0020_Code></CustomDocumentProperties>
 <Styles>
  <Style ss:ID="Default" ss:Name="Normal"><Font ss:FontName="Arial"/></Style>
  <Style ss:ID="fixed"><NumberFormat ss:Format="Fixed"/></Style>
  <Style ss:ID="pct"><NumberFormat ss:Format="Percent"/></Style>
  <Style ss:ID="date"><NumberFormat ss:Format="Short Date"/></Style>
  <Style ss:ID="time"><NumberFormat ss:Format="Short Time"/></Style>
  <Style ss:ID="child" ss:Parent="fixed"><Font ss:Bold="1"/></Style>
  <Style ss:ID="code"><NumberFormat ss:Format="0.000"/></Style>
  <Style ss:ID="yes"><NumberFormat ss:Format="Yes/No"/></Style>
  <Style ss:ID="sym"><Font ss:FontName="Symbol"/></Style>
 </Styles>
 <Worksheet ss:Name="Values">
  <Table>
   <Row>
    <Cell><Data ss:Type="String">text</Data></Cell>
    <Cell ss:StyleID="fixed"><Data ss:Type="Number">1.5</Data></Cell>
    <Cell ss:StyleID="pct"><Data ss:Type="Number">0.25</Data></Cell>
    <Cell ss:StyleID="date"><Data ss:Type="DateTime">2026-01-31T00:00:00.000</Data></Cell>
    <Cell ss:StyleID="time"><Data ss:Type="DateTime">1899-12-31T13:30:00.000</Data></Cell>
    <Cell><Data ss:Type="DateTime">2276-11-19T17:46:40.000</Data></Cell>
   </Row>
   <Row>
    <Cell><Data ss:Type="Boolean">1</Data></Cell>
    <Cell><Data ss:Type="Error">#DIV/0!</Data></Cell>
    <Cell ss:StyleID="child"><Data ss:Type="Number">2</Data></Cell>
    <Cell ss:StyleID="code"><Data ss:Type="Number">3</Data></Cell>
    <Cell ss:StyleID="yes"><Data ss:Type="Number">0</Data></Cell>
    <Cell><ss:Data ss:Type="String" xmlns="http://www.w3.org/TR/REC-html40"><B>bold</B> and <Font html:Color="#FF0000">red</Font></ss:Data></Cell>
    <Cell ss:StyleID="sym"><Data ss:Type="String">abg</Data></Cell>
   </Row>
  </Table>
 </Worksheet>`)
	doc, out := readSS(t, data, nil)
	if doc.Source.Format != "spreadsheetml" || doc.Source.MIME != "application/vnd.ms-spreadsheetml" {
		t.Errorf("source: %+v", doc.Source)
	}
	for _, want := range []string{
		`["text","1.50","25.00%","2026-01-31","13:30:00","2276-11-19T17:46:40",""]`,
		`["TRUE","#DIV/0!","2.00","3.000","No","bold and red","αβγ"]`,
		`"title":"Prices"`, `"authors":["A. Writer"]`, `"created":"2024-01-02T03:04:05Z"`,
		`"last_modified_by":"B. Editor"`, `"company":"Firm"`, `"app_version":"11.9999"`, `"custom.project code":"X-1"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
	// In the 1904 date system.
	data = ssBook(`<ExcelWorkbook xmlns="urn:schemas-microsoft-com:office:excel"><Date1904/></ExcelWorkbook>
 <Styles><Style ss:ID="d"><NumberFormat ss:Format="General Date"/></Style></Styles>
 <Worksheet ss:Name="S"><Table><Row><Cell ss:StyleID="d"><Data ss:Type="DateTime">2026-01-31T12:00:00.000</Data></Cell></Row></Table></Worksheet>`)
	if _, out := readSS(t, data, &gofastcore.Options{Cells: true}); !strings.Contains(out, `"2026-01-31T12:00:00"`) || !strings.Contains(out, `"value":"44591.5"`) {
		t.Errorf("1904 date: %s", out)
	}
}

func TestSpreadsheetMLLayout(t *testing.T) {
	data := ssBook(`
 <Styles><Style ss:ID="Default"><NumberFormat ss:Format="0.0"/></Style><Style ss:ID="row"><NumberFormat ss:Format="0.00"/></Style><Style ss:ID="col"><NumberFormat ss:Format="0.000"/></Style></Styles>
 <Worksheet ss:Name="Grid">
  <Table>
   <Column ss:Index="4" ss:StyleID="col"/>
   <Column ss:Index="6" ss:Hidden="1"/>
   <Row>
    <Cell ss:MergeAcross="1"><Data ss:Type="String">wide</Data></Cell>
    <Cell><Data ss:Type="String">C1</Data></Cell>
    <Cell ss:MergeDown="1"><Data ss:Type="String">tall</Data></Cell>
   </Row>
   <Row ss:StyleID="row">
    <Cell ss:Index="2"><Data ss:Type="String">B2</Data></Cell>
    <Cell><Data ss:Type="Number">1</Data></Cell>
    <Cell><Data ss:Type="String">E2</Data></Cell>
   </Row>
   <Row ss:Index="5" ss:Hidden="1"><Cell><Data ss:Type="Number">2</Data></Cell><Cell ss:Index="4"><Data ss:Type="Number">3</Data></Cell><Cell ss:Index="6"><Data ss:Type="String">F5</Data></Cell></Row>
   <Row ss:Span="2"/>
   <Row><Cell><Data ss:Type="String">A8</Data></Cell></Row>
  </Table>
 </Worksheet>
 <Worksheet ss:Name="Hidden"><Table><Row><Cell><Data ss:Type="String">x</Data></Cell></Row></Table>
  <WorksheetOptions xmlns="urn:schemas-microsoft-com:office:excel"><Visible>SheetHidden</Visible></WorksheetOptions>
 </Worksheet>`)
	doc, _ := readSS(t, data, nil)
	got := strings.Join(cellsShown(doc, doc), "\n")
	for _, want := range []string{
		"Grid!R1C1=wide", "Grid!R1C3=C1", "Grid!R1C4=tall", "Grid!merge R1C1 1x2", "Grid!merge R1C4 2x1",
		"Grid!R2C3=1.00", "Grid!R2C5=E2", // E2: after C2, and past D2 under "tall"
		"Grid!R5C1=2.0", "Grid!R5C4=3.000", "Grid!R5C6=F5", "Grid!R9C1=A8", // ss:Span adds rows
		"Hidden: hidden",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if !strings.Contains(got, "Grid!display R5C1 1x") {
		t.Errorf("hidden row not declared:\n%s", got)
	}
	f, err := gofastooxml.OpenBytes(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s := f.Sheets(); len(s) != 2 || s[0].Name != "Grid" || s[0].Hidden || !s[1].Hidden {
		t.Errorf("sheets: %+v", s)
	}
	// A band at a time gives the cells the whole sheet gives.
	var banded []string
	for p, err := range f.Rows(context.Background(), 1, 2) {
		if err != nil {
			t.Fatal(err)
		}
		d := &gofastcore.Document{Parts: []gofastcore.Part{p}}
		for _, line := range cellsShown(d, d) {
			if strings.Contains(line, "=") {
				banded = append(banded, line)
			}
		}
	}
	var whole []string
	for _, line := range cellsShown(doc, doc) {
		if strings.HasPrefix(line, "Grid!") && strings.Contains(line, "=") {
			whole = append(whole, line)
		}
	}
	sort.Strings(banded)
	if strings.Join(banded, "\n") != strings.Join(whole, "\n") {
		t.Errorf("by bands:\n%s\nwhole:\n%s", strings.Join(banded, "\n"), strings.Join(whole, "\n"))
	}
}

func TestSpreadsheetMLExtras(t *testing.T) {
	data := ssBook(`
 <Names><NamedRange ss:Name="Total" ss:RefersTo="=Data!R1C1:R2C2"/></Names>
 <Worksheet ss:Name="Data">
  <Names><NamedRange ss:Name="Local" ss:RefersTo="=Data!R3C1"/></Names>
  <Table>
   <Row>
    <Cell ss:HRef="http://example.com/"><Data ss:Type="String">link</Data></Cell>
    <Cell ss:Formula="=RC[-1]&amp;&quot;!&quot;"><Data ss:Type="String">link!</Data></Cell>
    <Cell><Data ss:Type="Number">7</Data><Comment ss:Author="Ann"><ss:Data xmlns="http://www.w3.org/TR/REC-html40"><B>Ann:</B> check this</ss:Data></Comment></Cell>
    <Cell ss:Formula="=SUM(R[1]C:R[2]C)"/>
   </Row>
  </Table>
  <DataValidation xmlns="urn:schemas-microsoft-com:office:excel"><Range>R2C1:R5C1</Range><Type>List</Type><Value>&quot;Yes,No&quot;</Value></DataValidation>
  <DataValidation xmlns="urn:schemas-microsoft-com:office:excel"><Range>C2</Range><Type>List</Type><Value>R1C3:R1C4</Value></DataValidation>
 </Worksheet>`)
	_, out := readSS(t, data, &gofastcore.Options{Cells: true, Spans: true})
	for _, want := range []string{
		`"href":"http://example.com/"`,
		`"formula":"A1\u0026\"!\""`,
		`"text":"Ann: check this"`, `"author":"Ann"`,
		`"text":"Yes\nNo"`, `"text":"=$C$1:$D$1","loc":{"range":"B1:B1048576"}`, // C2 is the second column
		`"text":"Total: Data!$A$1:$B$2"`, `"text":"Data!Local: Data!$A$3"`,
		`"table.formula_no_value"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
}

func TestSpreadsheetMLDamage(t *testing.T) {
	whole := ssBook(`<Worksheet ss:Name="One"><Table><Row><Cell><Data ss:Type="String">first</Data></Cell></Row></Table></Worksheet>` +
		`<Worksheet ss:Name="Two"><Table><Row><Cell><Data ss:Type="String">second</Data></Cell></Row></Table></Worksheet>`)
	cut := whole[:bytes.Index(whole, []byte("second"))]
	doc, out := readSS(t, cut, nil)
	if len(doc.Parts) != 1 || !strings.Contains(out, `"first"`) || !strings.Contains(out, gofastcore.WarnInputRepaired) {
		t.Errorf("cut short: %s", out)
	}
	// A sheet whose XML is malformed fails alone.
	bad := ssBook(`<Worksheet ss:Name="Bad"><Table><Row><Cell><Data>x</Cell></Row></Table></Worksheet>` +
		`<Worksheet ss:Name="Good"><Table><Row><Cell><Data ss:Type="String">fine</Data></Cell></Row></Table></Worksheet>`)
	doc, out = readSS(t, bad, nil)
	if len(doc.Parts) != 2 || !doc.Parts[0].Has(gofastcore.WarnPartFailed) || !strings.Contains(out, `"fine"`) {
		t.Errorf("malformed sheet: %s", out)
	}
	// In UTF-16, and in a code page.
	for name, data := range map[string][]byte{
		"UTF-16":       utf16With(string(ssBook(`<Worksheet ss:Name="Ü"><Table><Row><Cell><Data ss:Type="String">grüße</Data></Cell></Row></Table></Worksheet>`))),
		"windows-1251": append([]byte(`<?xml version="1.0" encoding="windows-1251"?>`), ssBook(`<Worksheet ss:Name="` + "\xcb\xe8\xf1\xf2" + `"><Table><Row><Cell><Data ss:Type="String">` + "\xcf\xf0\xe8\xe2\xe5\xf2" + `</Data></Cell></Row></Table></Worksheet>`)[len(`<?xml version="1.0"?>`):]...),
	} {
		_, out := readSS(t, data, nil)
		if !strings.Contains(out, `"grüße"`) && !strings.Contains(out, `"Привет"`) {
			t.Errorf("%s: %s", name, out)
		}
	}
}

// utf16With writes s as UTF-16 with a byte order mark.
func utf16With(s string) []byte {
	b := []byte{0xFF, 0xFE}
	for _, r := range s {
		if r >= 0x10000 {
			r -= 0x10000
			hi, lo := 0xD800+(r>>10), 0xDC00+(r&0x3FF)
			b = append(b, byte(hi), byte(hi>>8), byte(lo), byte(lo>>8))
			continue
		}
		b = append(b, byte(r), byte(r>>8))
	}
	return b
}

// TestSpreadsheetMLWorstCase: what reading SpreadsheetML 2003 costs grows
// with the file and no faster, for the inputs that could make it grow
// faster: long chains of parent styles, many merged ranges reaching down
// given out of order, many sheets, many names.
func TestSpreadsheetMLWorstCase(t *testing.T) {
	if testing.Short() {
		t.Skip("times large inputs")
	}
	read := func(data []byte) func() {
		return func() {
			gofastooxml.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), nil)
		}
	}
	sheet := func(rows string) string {
		return `<Worksheet ss:Name="S"><Table>` + rows + `</Table></Worksheet>`
	}
	styles := func(n int) []byte {
		var b strings.Builder
		b.WriteString(`<Styles><Style ss:ID="s0"><NumberFormat ss:Format="0.00"/></Style>`)
		for i := 1; i < n; i++ {
			fmt.Fprintf(&b, `<Style ss:ID="s%d" ss:Parent="s%d"/>`, i, i-1)
		}
		b.WriteString(`</Styles>`)
		fmt.Fprintf(&b, sheet(`<Row><Cell ss:StyleID="s%d"><Data ss:Type="Number">1</Data></Cell></Row>`), n-1)
		return ssBook(b.String())
	}
	costsAtMost(t, 8, "a chain of parent styles", read(styles(4_000)), read(styles(16_000)))
	merges := func(n int) []byte {
		var b strings.Builder
		b.WriteString(`<Row>`)
		for i := n; i >= 1; i-- {
			fmt.Fprintf(&b, `<Cell ss:Index="%d" ss:MergeDown="1000"><Data ss:Type="Number">%d</Data></Cell>`, 2*i, i)
		}
		b.WriteString(`</Row>`)
		for range n / 4 {
			b.WriteString(`<Row><Cell><Data ss:Type="Number">1</Data></Cell><Cell><Data ss:Type="Number">2</Data></Cell></Row>`)
		}
		return ssBook(sheet(b.String()))
	}
	costsAtMost(t, 8, "merged ranges reaching down, out of order", read(merges(2_000)), read(merges(8_000)))
	sheets := func(n int) []byte {
		var b strings.Builder
		for i := range n {
			fmt.Fprintf(&b, `<Worksheet ss:Name="S%d"><Table><Row><Cell><Data ss:Type="Number">%d</Data></Cell></Row></Table>`+
				`<WorksheetOptions xmlns="urn:schemas-microsoft-com:office:excel"><Visible>SheetHidden</Visible></WorksheetOptions>`+
				`<Names><NamedRange ss:Name="n%d" ss:RefersTo="=S%d!R1C1"/></Names></Worksheet>`, i, i, i, i)
		}
		return ssBook(b.String())
	}
	costsAtMost(t, 8, "many sheets", read(sheets(500)), read(sheets(2_000)))
}

// FuzzSpreadsheetML feeds arbitrary SpreadsheetML 2003 workbooks to
// Extract, from seeds that are the fixtures' workbooks written as
// SpreadsheetML 2003 and small workbooks using what the format has.
func FuzzSpreadsheetML(f *testing.F) {
	for _, file := range ooxmltest.LoadManifest(f, "fixtures.json") {
		if ss, ok := ooxmltest.SpreadsheetML2003(file.Read(f, false)); ok {
			f.Add(ss)
		}
	}
	f.Add(ssBook(`<Styles><Style ss:ID="a" ss:Parent="b"><NumberFormat ss:Format="Short Date"/></Style><Style ss:ID="b" ss:Parent="a"/></Styles>
 <Names><NamedRange ss:Name="N" ss:RefersTo="=S!R1C1:R[1]C[1]"/></Names>
 <Worksheet ss:Name="S"><Table><Column ss:Span="2" ss:Hidden="1"/><Row ss:Index="2"><Cell ss:MergeAcross="1" ss:MergeDown="2" ss:StyleID="a"><Data ss:Type="DateTime">2001-02-03T04:05:06.000</Data></Cell>
 <Cell ss:Formula="=SUM(R1C1:RC[-1])" ss:HRef="#S!A1"><Data ss:Type="Number">1</Data><Comment ss:Author="x"><ss:Data><B>b</B></ss:Data></Comment></Cell></Row><Row ss:Span="3"/><Row><Cell><Data ss:Type="Boolean">1</Data></Cell></Row></Table>
 <DataValidation xmlns="urn:schemas-microsoft-com:office:excel"><Range>R1C1:R4C1,C3</Range><Type>List</Type><Value>R1C2:R3C2</Value></DataValidation>
 <WorksheetOptions xmlns="urn:schemas-microsoft-com:office:excel"><Visible>SheetHidden</Visible></WorksheetOptions></Worksheet>`))
	opts := &gofastcore.Options{Cells: true, Spans: true, Limits: gofastcore.Limits{MaxDecodedBytes: 4 << 20}}
	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := gofastooxml.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts)
		checkOutcome(t, "fuzz input", doc, err)
	})
}

// A document type declaration that declares nothing is passed over, as
// go-fast-core's xmlscan passes over it; one with an internal subset is
// refused.
func TestSpreadsheetMLDoctype(t *testing.T) {
	body := `<Workbook xmlns="urn:schemas-microsoft-com:office:spreadsheet" xmlns:ss="urn:schemas-microsoft-com:office:spreadsheet"><Worksheet ss:Name="S"><Table><Row><Cell><Data ss:Type="String">hello</Data></Cell></Row></Table></Worksheet></Workbook>`
	doc, err := extract(t, []byte("<?xml version=\"1.0\"?>\n<!DOCTYPE Workbook>\n"+body), nil)
	if err != nil || !strings.Contains(doc.Text(), "hello") {
		t.Errorf("a declaration of nothing: %v", err)
	}
	if _, err := extract(t, []byte(`<!DOCTYPE Workbook [<!ENTITY e "x">]>`+body), nil); !errors.Is(err, gofastcore.ErrUnsupported) {
		t.Errorf("an internal subset: %v", err)
	}
}
