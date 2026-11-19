package gofastooxml_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math"
	"os"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// TestExcelFixtures holds the Excel reader to the exact content of the
// generated workbooks: every sheet, every table with its range, merges and
// header rows, every cell as displayed, and what is hidden.
func TestExcelFixtures(t *testing.T) {
	for _, f := range ooxmltest.LoadManifest(t, "fixtures.json") {
		if f.Format != "xlsx" {
			continue
		}
		t.Run(f.Name, func(t *testing.T) {
			doc, err := extract(t, f.Read(t, false), nil)
			if err != nil {
				t.Fatal(err)
			}
			ooxmltest.CheckDocument(t, doc)
			ooxmltest.CompareBlocks(t, doc, expected(t, f), ooxmltest.CompareOptions{Hidden: true})
		})
	}
}

// cellAt is a cell's place on a sheet: row and column, from 0.
type cellAt struct{ row, col int }

// parseA1 reads the first cell of a range such as "B3:D9".
func parseA1(t testing.TB, ref string) cellAt {
	t.Helper()
	first, _, _ := strings.Cut(ref, ":")
	col, i := 0, 0
	for ; i < len(first) && first[i] >= 'A' && first[i] <= 'Z'; i++ {
		col = col*26 + int(first[i]-'A') + 1
	}
	row, err := strconv.Atoi(first[i:])
	if err != nil || col == 0 || row == 0 {
		t.Fatalf("bad range %q", ref)
	}
	return cellAt{row - 1, col - 1}
}

func (c cellAt) String() string { return colName(c.col) + strconv.Itoa(c.row+1) }

// sheetCells lays the tables of a sheet back out on the sheet: the text
// of every cell by its place, and the cells flagged as not displayed.
func sheetCells(t testing.TB, part *gofastcore.Part) (cells map[cellAt]string, unseen map[cellAt]bool) {
	t.Helper()
	cells, unseen = map[cellAt]string{}, map[cellAt]bool{}
	for i := range part.Blocks {
		b := &part.Blocks[i]
		if b.Table == nil || b.Attrs[gofastcore.AttrRole] == gofastcore.RoleChart {
			continue // a chart's data is not cells of the sheet
		}
		origin := parseA1(t, b.Loc.Range)
		for r, row := range b.Table.Rows {
			for c, text := range row {
				if text != "" {
					at := cellAt{origin.row + r, origin.col + c}
					if _, dup := cells[at]; dup {
						t.Errorf("sheet %q: cell %v is in two tables", part.Name, at)
					}
					cells[at] = text
					if b.HiddenWhole()&gofastcore.HiddenInvisible != 0 {
						unseen[at] = true
					}
				}
			}
		}
		for _, h := range b.Table.Hidden {
			if h.Hidden&gofastcore.HiddenInvisible == 0 {
				continue
			}
			for r := h.Row; r < h.Row+h.RowSpan; r++ {
				for c := h.Col; c < h.Col+h.ColSpan; c++ {
					unseen[cellAt{origin.row + r, origin.col + c}] = true
				}
			}
		}
	}
	return cells, unseen
}

// typedSheet is what openpyxl reports of a sheet beyond its text.
type typedSheet struct {
	Name     string            `json:"name"`
	Dates    map[string]string `json:"dates"`
	Formulas []string          `json:"formulas"`
	Pivots   []string          `json:"pivots"`
}

// lastDigit reports whether two cells show the same number to fourteen
// significant digits and differ in the fifteenth.
func lastDigit(a, b string) bool {
	x, err1 := strconv.ParseFloat(a, 64)
	y, err2 := strconv.ParseFloat(b, 64)
	if err1 != nil || err2 != nil || x == y || len(a) < 14 || len(b) < 14 {
		return false
	}
	return math.Abs(x-y) <= 1.1e-14*math.Max(math.Abs(x), math.Abs(y))
}

var (
	asciiDigits = regexp.MustCompile(`[0-9]`)
	plainNumber = regexp.MustCompile(`^-?[0-9][0-9,.]*$`)
)

// sameInstant reports whether a date or time as this library writes it
// and as openpyxl gives it are the same moment, to the millisecond.
func sameInstant(ours, theirs string) bool {
	parse := func(s string) (time.Time, bool) {
		for _, layout := range []string{"2006-01-02T15:04:05.999999", "2006-01-02", "15:04:05.999999"} {
			if v, err := time.Parse(layout, s); err == nil {
				return v, true
			}
		}
		return time.Time{}, false
	}
	a, ok1 := parse(ours)
	b, ok2 := parse(theirs)
	if !ok1 || !ok2 {
		return false
	}
	d := a.Sub(b)
	return d > -2*time.Millisecond && d < 2*time.Millisecond
}

// normalCell puts a cell's text into the form compared: the padding
// LibreOffice writes with figure spaces, and all runs of white space,
// become one space.
func normalCell(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\u2007", " ")), " ")
}

// excelFloor is set a little under what was measured: two cells in seven
// million differ without a named reason.
var excelFloor = 0.9999

// TestExcelCorpus compares every cell of every sound workbook in the
// corpus with what LibreOffice displays for it. Dates are compared with
// openpyxl instead, since this library writes them in ISO 8601 and not as
// displayed. Every way the two may differ without either being wrong is a
// named class, counted and reported; what is left must be under half a
// per cent of the cells.
//
// Set EXCEL_MISMATCHES to a file name to have every unexplained difference
// written there.
func TestExcelCorpus(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the whole corpus and runs LibreOffice")
	}
	if _, ok := ooxmltest.PythonBin(t); !ok {
		t.Skip("the Python comparison environment is not set up")
	}
	if _, err := exec.LookPath("soffice"); err != nil {
		t.Skip("LibreOffice is not installed")
	}
	var files []ooxmltest.File
	var contents [][]byte
	for _, f := range ooxmltest.LoadManifest(t, "corpus.json") {
		if f.Format != "xlsx" || f.Note != "" {
			continue
		}
		if data, err := os.ReadFile(f.AbsPath(t)); err == nil {
			files, contents = append(files, f), append(contents, data)
		}
	}
	if len(files) == 0 {
		t.Skip("no corpus files downloaded")
	}
	ooxmltest.WarmLibreOfficeCSV(t, contents)
	ooxmltest.WarmPython(t, "openpyxl-cells", "xlsx", contents)

	var mismatches strings.Builder
	classes := map[string]int{}
	agree, differ, compared, noOracle, sheetsUnmatched := 0, 0, 0, 0, 0
	for i, f := range files {
		data := contents[i]
		doc, err := extract(t, data, nil)
		if err != nil {
			t.Errorf("%s: %v", f.Name, err)
			continue
		}
		ooxmltest.CheckDocument(t, doc)
		if warned(doc, gofastcore.WarnInputAmbiguous) {
			continue
		}
		lo, ok := ooxmltest.LibreOfficeCSVOK(t, data)
		if !ok {
			noOracle++
			continue
		}
		typed := map[string]typedSheet{}
		typedOK := false
		if py := ooxmltest.Python(t, "openpyxl-cells", data, "xlsx"); py.Error == "" {
			var sheets []typedSheet
			if json.Unmarshal([]byte(py.Text), &sheets) == nil {
				typedOK = true
				for _, s := range sheets {
					typed[s.Name] = s
				}
			}
		}
		compared++
		fileDiffer := 0
		pivots := pivotSheets(data)
		for p := range doc.Parts {
			part := &doc.Parts[p]
			grid, found := lo[part.Name]
			if !found {
				sheetsUnmatched++
				continue
			}
			cells, unseen := sheetCells(t, part)
			formulas := map[string]bool{}
			for _, ref := range typed[part.Name].Formulas {
				formulas[ref] = true
			}
			dates := typed[part.Name].Dates
			// A sheet with a pivot table: LibreOffice builds the table
			// again from the data, and may make it larger or smaller.
			pivot := pivots[part.Name]
			// A sheet LibreOffice reads nothing from.
			blank := len(cells) > 0
			for _, row := range grid {
				for _, c := range row {
					blank = blank && c == ""
				}
			}
			check := func(at cellAt, ours, theirs string) {
				ours, theirs = normalCell(ours), normalCell(theirs)
				if ours == "" && theirs == "" {
					return
				}
				ref := at.String()
				date, isDate := dates[ref]
				switch {
				case ours == theirs:
					agree++
				case isDate && date != "duration" && sameInstant(ours, date):
					classes["dates and times, in ISO 8601 here and as displayed there; equal to openpyxl's value"]++
					agree++
				case !typedOK && isoLike.MatchString(ours):
					classes["dates not checked: openpyxl cannot read the file"]++
				case theirs == "" && unseen[at]:
					classes["displayed as nothing; the value is kept here and flagged as hidden"]++
					agree++
				case strings.ContainsAny(ours+theirs, "E") && sameNumber(ours, theirs):
					classes["the same number in another notation"]++
					agree++
				case theirs == "#FMT" || strings.HasPrefix(theirs, "Err:") || strings.HasPrefix(theirs, "#"):
					classes["LibreOffice shows an error"]++
				case formulas[ref]:
					classes["formula cells where LibreOffice calculated another result than the file stores"]++
				case pivot:
					classes["sheets with pivot tables, which LibreOffice lays out again with its own labels and formats"]++
				case blank:
					classes["sheets LibreOffice reads nothing from"]++
				case len(ours) > 32767:
					// More than Excel's limit of 32,767 characters to a
					// cell. LibreOffice shortens such text or drops it.
					classes["text longer than a cell can hold in Excel"]++
				case lastDigit(ours, theirs):
					// 2.3561944901923448 is 2.35619449019234 to fifteen
					// digits. LibreOffice rounds it to sixteen first and
					// then to fifteen, and shows 2.35619449019235.
					classes["the fifteenth digit, where LibreOffice rounds twice"]++
					agree++
				case (ours == "TRUE" || ours == "FALSE") && plainNumber.MatchString(strings.Trim(theirs, "$€£ %()")):
					// Excel shows a logical value as TRUE or FALSE whatever
					// the cell's number format.
					classes["logical values, which LibreOffice puts through the number format"]++
				case plainNumber.MatchString(ours) && theirs != "" && !asciiDigits.MatchString(theirs):
					classes["numbers a format writes in the digits of another script; written in ASCII digits here"]++
				default:
					differ++
					fileDiffer++
					if fileDiffer <= 25 {
						fmt.Fprintf(&mismatches, "%s\t%s\t%s\t%q\t%q\n", f.Name, part.Name, ref, ours, theirs)
					}
				}
			}
			for r, row := range grid {
				for c, theirs := range row {
					at := cellAt{r, c}
					check(at, cells[at], theirs)
					delete(cells, at)
				}
			}
			for at, ours := range cells {
				check(at, ours, "")
			}
		}
	}
	if path := os.Getenv("EXCEL_MISMATCHES"); path != "" {
		os.WriteFile(path, []byte(mismatches.String()), 0o644)
	}
	total := agree + differ
	if total == 0 {
		t.Skip("nothing could be compared")
	}
	score := float64(agree) / float64(total)
	t.Logf("%d workbooks compared (%d more LibreOffice could not convert; %d sheets it names otherwise)", compared, noOracle, sheetsUnmatched)
	t.Logf("%d of %d cells agree (%.4f); %d differ", agree, total, score, differ)
	names := make([]string, 0, len(classes))
	for name := range classes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Logf("  %7d  %s", classes[name], name)
	}
	if score < excelFloor {
		t.Errorf("cell agreement %.4f is under the floor of %.3f", score, excelFloor)
	}
}

var (
	sheetElement = regexp.MustCompile(`<sheet\b[^>]*>`)
	relElement   = regexp.MustCompile(`<Relationship\b[^>]*>`)
	attrName     = regexp.MustCompile(`\bname="([^"]*)"`)
	attrRID      = regexp.MustCompile(`\br:id="([^"]*)"`)
	attrID       = regexp.MustCompile(`\bId="([^"]*)"`)
	attrTarget   = regexp.MustCompile(`\bTarget="([^"]*)"`)
)

// pivotSheets returns the names of the sheets of a workbook that hold a
// pivot table. It reads the archive itself, with none of the library's
// code, so that the comparison does not lean on what it checks.
func pivotSheets(data []byte) map[string]bool {
	out := map[string]bool{}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return out
	}
	read := func(name string) string {
		for _, f := range zr.File {
			if f.Name == name {
				rc, err := f.Open()
				if err != nil {
					return ""
				}
				defer rc.Close()
				b, _ := io.ReadAll(io.LimitReader(rc, 16<<20))
				return string(b)
			}
		}
		return ""
	}
	targets := map[string]string{}
	for _, rel := range relElement.FindAllString(read("xl/_rels/workbook.xml.rels"), -1) {
		id, target := attrID.FindStringSubmatch(rel), attrTarget.FindStringSubmatch(rel)
		if id != nil && target != nil {
			targets[id[1]] = strings.TrimPrefix(strings.TrimPrefix(target[1], "/"), "xl/")
		}
	}
	for _, sheet := range sheetElement.FindAllString(read("xl/workbook.xml"), -1) {
		name, id := attrName.FindStringSubmatch(sheet), attrRID.FindStringSubmatch(sheet)
		if name == nil || id == nil {
			continue
		}
		target := targets[id[1]]
		dir, file := path.Split(target)
		if strings.Contains(read("xl/"+dir+"_rels/"+file+".rels"), `/pivotTable"`) {
			out[html.UnescapeString(name[1])] = true
		}
	}
	return out
}

var isoLike = regexp.MustCompile(`^(\d{4}-\d\d-\d\d)?T?(\d\d:\d\d:\d\d(\.\d+)?)?$`)

// sameNumber reports whether two cells show the same number written two
// ways, such as 1E+20 and 1E+020.
func sameNumber(a, b string) bool {
	x, err1 := strconv.ParseFloat(a, 64)
	y, err2 := strconv.ParseFloat(b, 64)
	return err1 == nil && err2 == nil && x == y
}

// TestExcelHiddenSurvey reads every workbook of the corpus and reports
// what is flagged as hidden. It asserts nothing about the counts: it is
// how false alarms on real workbooks are found. Set HIDDEN_SURVEY=1 to
// list the flagged cells, a few per sheet and kind.
func TestExcelHiddenSurvey(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the whole corpus")
	}
	detail := os.Getenv("HIDDEN_SURVEY") != ""
	files, flagged := 0, 0
	cells := map[string]int{}
	inFiles := map[string]int{}
	for _, f := range ooxmltest.LoadManifest(t, "corpus.json") {
		if f.Format != "xlsx" || f.Note != "" {
			continue
		}
		data, err := os.ReadFile(f.AbsPath(t))
		if err != nil {
			continue
		}
		doc, err := extract(t, data, nil)
		if err != nil {
			continue
		}
		files++
		seen := map[string]bool{}
		for p := range doc.Parts {
			part := &doc.Parts[p]
			shown := map[string]int{}
			note := func(what, where, text string, n int) {
				cells[what] += n
				seen[what] = true
				if shown[what]++; detail && shown[what] <= 3 {
					if len(text) > 50 {
						text = text[:50]
					}
					t.Logf("%-46s %-22s %-34s %-14s %q", f.Name, part.Name, what, where, text)
				}
			}
			if part.Hidden != 0 {
				n := 0
				for _, b := range part.Blocks {
					if b.Table != nil {
						for _, row := range b.Table.Rows {
							for _, c := range row {
								if c != "" {
									n++
								}
							}
						}
					}
				}
				note("hidden sheet", "", "", n)
				continue
			}
			for _, b := range part.Blocks {
				if b.Table == nil || b.Attrs[gofastcore.AttrRole] == gofastcore.RoleChart {
					if b.Table == nil && b.Hidden != 0 {
						note("shape text: "+b.Hidden.String(), b.Loc.Range, b.Text, 1)
					}
					continue
				}
				origin := parseA1(t, b.Loc.Range)
				rows, cols := len(b.Table.Rows), len(b.Table.Rows[0])
				for _, h := range b.Table.Hidden {
					what := "cell: " + h.Hidden.String()
					switch {
					case h.Hidden == gofastcore.HiddenChars:
						what = "invisible characters"
					case h.Hidden == gofastcore.HiddenInvisible && h.ColSpan == cols && h.Col == 0:
						what = "hidden row"
					case h.Hidden == gofastcore.HiddenInvisible && h.RowSpan == rows && h.Row == 0:
						what = "hidden column"
					case h.Hidden == gofastcore.HiddenInvisible:
						what = "value its format shows as nothing"
					}
					n, sample := 0, ""
					for r := h.Row; r < h.Row+h.RowSpan; r++ {
						for c := h.Col; c < h.Col+h.ColSpan; c++ {
							if text := b.Table.Rows[r][c]; text != "" {
								if n++; sample == "" {
									sample = text
								}
							}
						}
					}
					if n > 0 {
						note(what, cellAt{origin.row + h.Row, origin.col + h.Col}.String(), sample, n)
					}
				}
			}
		}
		if len(seen) > 0 {
			flagged++
		}
		for what := range seen {
			inFiles[what]++
		}
	}
	if files == 0 {
		t.Skip("no corpus files downloaded")
	}
	t.Logf("%d workbooks, %d with hidden text", files, flagged)
	names := make([]string, 0, len(cells))
	for name := range cells {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Logf("  %-44s %8d cells in %3d workbooks", name, cells[name], inFiles[name])
	}
}
