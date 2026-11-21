package gofastooxml_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// xlsbFiles lists the Excel binary workbooks of the soak set.
func xlsbFiles(t *testing.T) []string {
	var files []string
	filepath.WalkDir(filepath.Join(ooxmltest.Root(t), "testdata", "corpus", "soak"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".xlsb") {
			files = append(files, p)
		}
		return nil
	})
	if len(files) == 0 {
		t.Skip("the soak set is not fetched (testdata/soak.py)")
	}
	return files
}

// xlsbCells maps each sheet's cells, by name and then by row and column from
// 0, to what the document says of it: its text, and the detail of the
// cell (Options.Cells).
type sheetCell struct {
	text   string
	detail gofastcore.CellDetail
}

func xlsbCells(doc *gofastcore.Document) map[string]map[[2]int]sheetCell {
	out := map[string]map[[2]int]sheetCell{}
	for _, p := range doc.Parts {
		if p.Kind != gofastcore.PartSheet {
			continue
		}
		m := map[[2]int]sheetCell{}
		out[p.Name] = m
		for _, b := range p.Blocks {
			if b.Table == nil {
				continue
			}
			first, _, _ := strings.Cut(b.Loc.Range, ":")
			col, row := 0, 0
			i := 0
			for i < len(first) && first[i] >= 'A' && first[i] <= 'Z' {
				col = col*26 + int(first[i]-'A') + 1
				i++
			}
			row, _ = strconv.Atoi(first[i:])
			details := map[[2]int]gofastcore.CellDetail{}
			for _, d := range b.Table.Cells {
				details[[2]int{d.Row, d.Col}] = d
			}
			for r, cells := range b.Table.Rows {
				for c, text := range cells {
					if text != "" {
						m[[2]int{row - 1 + r, col - 1 + c}] = sheetCell{text, details[[2]int{r, c}]}
					}
				}
			}
		}
	}
	return out
}

// The cells this library reads from each binary workbook of the soak set
// are those pyxlsb reads: the same text, the same truth values, and the
// same numbers as stored, whatever the format shows of them.
func TestXlsbAgainstPyxlsb(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the soak set's binary workbooks with pyxlsb")
	}
	checked, cells := 0, 0
	for _, path := range xlsbFiles(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := gofastooxml.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), &gofastcore.Options{Cells: true})
		if errors.Is(err, gofastcore.ErrPasswordRequired) {
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		ooxmltest.CheckDocument(t, doc)
		res := ooxmltest.Python(t, "pyxlsb-cells", data, "xlsb")
		if res.Error != "" {
			// pyxlsb cannot read it: the damaged sheet list of two of POI's
			// files. The library reads what it can of them (TestXlsbDamaged).
			t.Logf("%s: pyxlsb: %s", filepath.Base(path), res.Error)
			continue
		}
		var sheets []struct {
			Sheet string
			Cells [][3]any
		}
		if err := json.Unmarshal([]byte(res.Text), &sheets); err != nil {
			t.Fatal(err)
		}
		ours := xlsbCells(doc)
		for _, s := range sheets {
			m, ok := ours[s.Sheet]
			if !ok {
				t.Errorf("%s: no sheet %q", filepath.Base(path), s.Sheet)
				continue
			}
			for _, c := range s.Cells {
				at := [2]int{int(c[0].(float64)), int(c[1].(float64))}
				got, have := m[at]
				where := filepath.Base(path) + " " + s.Sheet + " " + strconv.Itoa(at[0]) + "," + strconv.Itoa(at[1])
				switch v := c[2].(type) {
				case string:
					want := strings.TrimSpace(v)
					if want == "" {
						continue
					}
					if !have || got.text != want && !strings.HasPrefix(got.text, "#") {
						t.Errorf("%s: %q, pyxlsb %q", where, got.text, want)
					}
				case bool:
					want := "FALSE"
					if v {
						want = "TRUE"
					}
					if !have || got.text != want {
						t.Errorf("%s: %q, pyxlsb %v", where, got.text, v)
					}
				case float64:
					stored := got.detail.Value
					if stored == "" {
						stored = got.text
					}
					f, err := strconv.ParseFloat(stored, 64)
					if !have || err != nil || math.Abs(f-v) > 1e-12*math.Max(1, math.Abs(v)) {
						// pyxlsb gives an error cell's code as a number.
						if have && strings.HasPrefix(got.text, "#") {
							continue
						}
						t.Errorf("%s: %q (stored %q), pyxlsb %v", where, got.text, got.detail.Value, v)
					}
				}
				cells++
			}
		}
		checked++
	}
	t.Logf("%d workbooks, %d cells", checked, cells)
}

// The formulas this library reads back from each binary workbook of the
// soak set are those LibreOffice writes when it saves the workbook as
// .xlsx, where both have one.
func TestXlsbFormulasAgainstLibreOffice(t *testing.T) {
	if testing.Short() {
		t.Skip("converts the soak set's binary workbooks with LibreOffice")
	}
	checked, formulas := 0, 0
	for _, path := range xlsbFiles(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		opts := &gofastcore.Options{Cells: true}
		doc, err := gofastooxml.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts)
		if err != nil {
			continue // a workbook that needs a password: LibreOffice cannot open it either
		}
		x := ooxmltest.LibreOfficeXlsx(t, data)
		lo, err := gofastooxml.Extract(context.Background(), bytes.NewReader(x), int64(len(x)), opts)
		if err != nil {
			t.Errorf("%s: LibreOffice's .xlsx: %v", path, err)
			continue
		}
		ours, theirs := xlsbCells(doc), xlsbCells(lo)
		for sheet, m := range ours {
			for at, c := range m {
				o, ok := theirs[sheet][at]
				if !ok || c.detail.Formula == "" || o.detail.Formula == "" {
					continue
				}
				if c.detail.Formula != o.detail.Formula {
					if strings.HasPrefix(strings.TrimPrefix(c.detail.Formula, "'"), "[") && o.detail.Formula == "#REF!" {
						// A reference to a workbook whose path the file
						// says is missing (bug66682.xlsb: [1]ab!C1), which
						// LibreOffice writes as #REF!.
						t.Logf("%s %s %v: %q, LibreOffice %q", filepath.Base(path), sheet, at, c.detail.Formula, o.detail.Formula)
						continue
					}
					t.Errorf("%s %s %v: %q, LibreOffice %q", filepath.Base(path), sheet, at, c.detail.Formula, o.detail.Formula)
				}
				formulas++
			}
		}
		checked++
	}
	t.Logf("%d workbooks, %d formulas", checked, formulas)
}
