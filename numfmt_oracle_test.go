package gofastooxml_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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

// numfmtTable is the table of what LibreOffice displays for every format
// and value, one line each: format, value, quoted text.
const numfmtTable = "testdata/numfmt/libreoffice.tsv"

// TestNumberFormatTable checks that the table the number-format engine is
// held to (internal/numfmt) is what LibreOffice displays. Run with -update
// to write the table again after changing the formats or values.
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
	path := filepath.Join(ooxmltest.Root(t), numfmtTable)
	if ooxmltest.Updating() {
		if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	have, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(have) != sb.String() {
		t.Errorf("%s is not what LibreOffice displays for the formats and values listed here; run with -update, then go test ./internal/numfmt", numfmtTable)
	}
}
