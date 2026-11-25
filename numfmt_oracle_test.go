package gofastooxml_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-core/numfmt"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// The number formats and values of the comparison with LibreOffice. Every
// format is applied to every value.
var oracleFormats = []string{
	"General", "0", "0.0", "0.00", "0.000", "#", "#.#", "#.##", "#.0#", "0.0#", "#,##0", "#,##0.00", "#,###", "0,000",
	"#,##0,", "#,##0,,", "0.0,", "0%", "0.0%", "0.00%", "#%", "0.00E+00", "0E+0", "0.0E-0", "##0.0E+0", "#.##E+00", "0.000E+00",
	"#,##0.00;(#,##0.00)", "#,##0;-#,##0", "0.00;[Red]-0.00", "0;-0;\"zero\"", "0.0;(0.0);\"-\"", "[Blue]0.0;[Red](0.0);0", "\"$\"#,##0.00", "$#,##0", "#,##0.00\" EUR\"", "[$€-407]#,##0.00", "[$USD] #,##0.00", "#,##0.00 [$kr-41D]",
	"_(* #,##0.00_);_(* \\(#,##0.00\\);_(* \"-\"??_);_(@_)", "_(\"$\"* #,##0_);_(\"$\"* \\(#,##0\\);_(\"$\"* \"-\"_);_(@_)", "#,##0_);(#,##0)", "#,##0.00_);[Red](#,##0.00)",
	"000000", "00000-0000", "000-00-0000", "(000) 000-0000", "0\"m\"", "0.0\" kg\"", "\"No. \"0", "0\\x0", "+0;-0", "0.00_ ;\\-0.00\\ ",
	"# ?/?", "# ??/??", "# ?/2", "# ?/4", "# ??/16", "?/?", "# ?/10", "# ???/???",
	"[>=100]0;[<0]\"neg\";0.00", "[>1000]#,##0;0.0", "[<1]0.00%;0", "[=0]\"nil\";0.0", "[<=9999999]###-####;(###) ###-####",
	"@", "0;;", ";;;", "0.0;;;", "\"x\"", "0.0?", "0.??", "???.???", "?0.0", "#,##0.0#", "0.###############", "#,##0.00;[Red]-#,##0.00;0.00;\"t\"@",
	"[h]:mm:ss", "[h]:mm", "[mm]:ss", "[s]", "[h]", "[ss].00",
}

var oracleValues = []float64{
	0, 1, -1, 0.5, -0.5, 2.5, 3.5, 1.005, 0.125, 12, 1234.5, -1234.5, 1234567.891, 0.000123, 99.995, 0.05, 1e10, 123456789012, 1.5e15, 1e20, 1.23e-7, -0.0001, 5551234, 1234567890, 0.333333333333333, 2.0 / 3, 1e-10, 0.30000000000000004, 1.0000001, 25.75, 999.5,
	// Where notations change, where rounding carries, and the very large and very small.
	1e15, 999999999999999, 1e16, 9.99999999999999e15, 1e17, 1.5e19, 1e100, 1e-5, 1.5e-5, 1.23456789e-6, 1e-9, 1.5e-9, 1.23456789012345e-8, 1.5e-10, 0.1234567890123456, 123456789.123456789, 1e-15,
	// Negative values that round to zero, down to the residue of a sum.
	-1e-10, -1e-11, -1e-12, -4.5e-13, -1e-13, -1e-14, -1e-15, 4.5e-13,
	// The fifteenth digit rounded from the binary value and from the
	// shortest decimal differs for these.
	664.5600532184905, 315.2080853201245, 535314.2866377655,
	// Subnormal numbers, which have fewer than fifteen digits of their own,
	// and a normal neighbour.
	2.12199579096527e-314, 5.4323092248711e-312, 3.30035851421953e-321, 2.5e-308, -2.12199579096527e-314,
	0.999, 0.9999999, 59.9999, 1.0 / 86400, 1.5, 4.5, -2.5, -0.005, 0.045, 0.995, 9.5, 99.5, 0.0005, 1e-300, 1e300, -1e15, 7.0 / 16, 1.0 / 7, 100, 1000, 1e6, 0.1, 0.01, 0.001,
}

// oracleBook builds a workbook with one row per format and one column per
// value, and returns it with the style index of each format.
func oracleBook() *ooxmltest.Package {
	var fmts, xfs, rows strings.Builder
	for i, f := range oracleFormats {
		fmt.Fprintf(&fmts, `<numFmt numFmtId="%d" formatCode="%s"/>`, 200+i, ooxmltest.EscapeXML(f))
		fmt.Fprintf(&xfs, `<xf numFmtId="%d" fontId="0" fillId="0" borderId="0" applyNumberFormat="1"/>`, 200+i)
		fmt.Fprintf(&rows, `<row r="%d">`, i+1)
		for j, v := range oracleValues {
			fmt.Fprintf(&rows, `<c r="%s%d" s="%d"><v>%s</v></c>`, colName(j), i+1, i+1, strconv.FormatFloat(v, 'g', -1, 64))
		}
		rows.WriteString(`</row>`)
	}
	styles := fmt.Sprintf(`<numFmts count="%d">%s</numFmts><fonts count="1"><font><sz val="11"/><name val="Calibri"/></font></fonts>`+
		`<fills count="1"><fill><patternFill patternType="none"/></fill></fills><borders count="1"><border/></borders>`+
		`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>`+
		`<cellXfs count="%d"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/>%s</cellXfs>`,
		len(oracleFormats), fmts.String(), len(oracleFormats)+1, xfs.String())
	return ooxmltest.Xlsx(ooxmltest.Sheet{Name: "F", Data: rows.String()}).WithSheetStyles(styles)
}

func colName(i int) string {
	name := ""
	for i++; i > 0; i = (i - 1) / 26 {
		name = string(rune('A'+(i-1)%26)) + name
	}
	return name
}

// numfmtTable returns the table of what LibreOffice displays for every
// format and value, one line each: format, value, quoted text. The table
// is go-fast-core's, beside the engine held to it; it is read from the
// module this build uses.
func numfmtTable(t *testing.T) (path string, data []byte) {
	t.Helper()
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/AOShei/go-fast-core")
	cmd.Dir = ooxmltest.Root(t)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("finding go-fast-core's directory: %v", err)
	}
	path = filepath.Join(strings.TrimSpace(string(out)), "numfmt", "testdata", "libreoffice.tsv")
	if data, err = os.ReadFile(path); err != nil {
		t.Fatal(err)
	}
	return path, data
}

// TestNumberFormatTable checks that the table go-fast-core's number-format
// engine (package numfmt) is held to is what LibreOffice displays, for the
// formats and values listed here. With -update it writes the table to
// bench/out/libreoffice.tsv, which is not tracked: it is sent to
// go-fast-core as a pull request.
func TestNumberFormatTable(t *testing.T) {
	if testing.Short() {
		t.Skip("runs LibreOffice")
	}
	grid := ooxmltest.LibreOfficeCSV(t, oracleBook().Bytes())["F"]
	var sb strings.Builder
	for i, f := range oracleFormats {
		for j, v := range oracleValues {
			shown := ""
			if i < len(grid) && j < len(grid[i]) {
				shown = grid[i][j]
			}
			fmt.Fprintf(&sb, "%s\t%s\t%q\n", f, strconv.FormatFloat(v, 'g', -1, 64), shown)
		}
	}
	if ooxmltest.Updating() {
		path := filepath.Join(ooxmltest.Root(t), "bench", "out", "libreoffice.tsv")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s, for go-fast-core's numfmt/testdata/libreoffice.tsv", path)
		return
	}
	path, have := numfmtTable(t)
	if string(have) != sb.String() {
		t.Errorf("%s is not what LibreOffice displays for the formats and values listed here; run with -update and compare it with bench/out/libreoffice.tsv", path)
	}
}

// TestNumberFormatsThroughTheReader reads the workbook of every format and
// value, and requires each cell to be what go-fast-core's engine gives for
// its format and value: the styles of a workbook lead to the right format,
// and the cell keeps what the engine wrote, with its display reason.
func TestNumberFormatsThroughTheReader(t *testing.T) {
	book := oracleBook().Bytes()
	doc, err := gofastooxml.Extract(context.Background(), bytes.NewReader(book), int64(len(book)), nil)
	if err != nil {
		t.Fatal(err)
	}
	cells, unseen := sheetCells(t, &doc.Parts[0])
	for i, code := range oracleFormats {
		f := numfmt.Parse(code)
		for j, v := range oracleValues {
			out, result := f.Number(nil, v, false)
			at := cellAt{i, j}
			if want := string(out); cells[at] != want {
				t.Errorf("%q of %v: the cell is %q, the engine gives %q", code, v, cells[at], want)
			}
			if want := result == numfmt.Blank; unseen[at] != want {
				t.Errorf("%q of %v: not displayed %v, the engine says %v", code, v, unseen[at], want)
			}
		}
	}
}

// TestRepeatedNumbers reads sheets whose numbers come back, many more of
// them than a sheet's reader remembers (cachedNumbers in internal/sml):
// the same value through several formats, soon and late, in both date
// systems, the sheet whole and by bands of rows. Every cell is to be what
// go-fast-core's engine gives for its format and value, with its display
// reason and its type.
func TestRepeatedNumbers(t *testing.T) {
	codes := []string{"0.00", "#,##0", "0%", "yyyy\\-mm\\-dd", "0;0;;", ";;;", "General", "[>100]0.0;\"low\""}
	var fmts, xfs strings.Builder
	formats := make([]*numfmt.Format, len(codes))
	for i, code := range codes {
		formats[i] = numfmt.Parse(code)
		fmt.Fprintf(&fmts, `<numFmt numFmtId="%d" formatCode="%s"/>`, 200+i, ooxmltest.EscapeXML(code))
		fmt.Fprintf(&xfs, `<xf numFmtId="%d" applyNumberFormat="1"/>`, 200+i)
	}
	styles := fmt.Sprintf(`<numFmts count="%d">%s</numFmts><cellXfs count="%d"><xf numFmtId="0"/>%s</cellXfs>`,
		len(codes), fmts.String(), len(codes)+1, xfs.String())
	// Three times over the same 483 values, three of which (-1, 0 and 1)
	// are also on every fifth row: each in every format, 3,864 pairs.
	var values []float64
	for i := 0; i < 1800; i++ {
		switch i % 5 {
		case 0:
			values = append(values, float64(i%3)-1) // -1, 0 and 1
		default:
			values = append(values, float64(i%600)*1.25)
		}
	}
	var rows strings.Builder
	for r, v := range values {
		fmt.Fprintf(&rows, `<row r="%d">`, r+1)
		for c := range codes {
			fmt.Fprintf(&rows, `<c r="%s%d" s="%d"><v>%s</v></c>`, colName(c), r+1, c+1, strconv.FormatFloat(v, 'g', -1, 64))
		}
		rows.WriteString(`</row>`)
	}
	for _, date1904 := range []bool{false, true} {
		pkg := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "F", Data: rows.String()}).WithSheetStyles(styles)
		if date1904 {
			pkg.Add("xl/workbook.xml", ooxmltest.CTWorkbook, `<workbook xmlns="`+ooxmltest.NSSheet+`" xmlns:r="`+ooxmltest.NSRel+
				`"><workbookPr date1904="1"/><sheets><sheet name="F" sheetId="1" r:id="rId1"/></sheets></workbook>`)
		}
		f, err := gofastooxml.OpenBytes(pkg.Bytes(), &gofastcore.Options{Cells: true})
		if err != nil {
			t.Fatal(err)
		}
		read := map[string][]gofastcore.Part{}
		for p, err := range f.Parts(context.Background()) {
			if err != nil {
				t.Fatal(err)
			}
			read["whole"] = append(read["whole"], p)
		}
		for p, err := range f.Rows(context.Background(), 1, 100) {
			if err != nil {
				t.Fatal(err)
			}
			read["bands"] = append(read["bands"], p)
		}
		for how, parts := range read {
			cells, unseen, dates := map[cellAt]string{}, map[cellAt]bool{}, map[cellAt]bool{}
			for i := range parts {
				cs, us := sheetCells(t, &parts[i])
				for at, text := range cs {
					cells[at] = text
				}
				for at := range us {
					unseen[at] = true
				}
				for _, b := range parts[i].Blocks {
					if b.Table == nil {
						continue
					}
					origin := parseA1(t, b.Loc.Range)
					for _, d := range b.Table.Cells {
						if d.Type == gofastcore.CellDate {
							dates[cellAt{origin.row + d.Row, origin.col + d.Col}] = true
						}
					}
				}
			}
			wrong := 0
			for r, v := range values {
				for c, f := range formats {
					out, result := f.Number(nil, v, date1904)
					at := cellAt{r, c}
					if cells[at] != string(out) || unseen[at] != (result == numfmt.Blank) || dates[at] != (result == numfmt.Date) {
						if wrong++; wrong <= 10 {
							t.Errorf("%s, 1904 %v: %q of %v at %v: %q, unseen %v, date %v; the engine gives %q, %v",
								how, date1904, codes[c], v, at, cells[at], unseen[at], dates[at], out, result)
						}
					}
				}
			}
			if wrong > 10 {
				t.Errorf("%s, 1904 %v: %d cells differ", how, date1904, wrong)
			}
		}
	}
}
