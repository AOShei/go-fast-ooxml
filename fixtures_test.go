package gofastooxml_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// expected returns the known content of a generated fixture.
func expected(t testing.TB, f ooxmltest.File) *gofastcore.Document {
	t.Helper()
	if f.Expect == "" {
		t.Fatalf("%s has no expected content", f.Name)
	}
	data, err := os.ReadFile(filepath.Join(ooxmltest.Root(t), "testdata", filepath.FromSlash(f.Expect)))
	if err != nil {
		t.Fatal(err)
	}
	doc := new(gofastcore.Document)
	if err := json.Unmarshal(data, doc); err != nil {
		t.Fatalf("%s: %v", f.Expect, err)
	}
	return doc
}

// The fixtures are committed, so each must be present, match its hash, be a
// package of the stated format and have expected content that parses.
func TestFixtureManifest(t *testing.T) {
	files := ooxmltest.LoadManifest(t, "fixtures.json")
	if len(files) == 0 {
		t.Fatal("no fixtures")
	}
	names := map[string]bool{}
	for _, f := range files {
		names[f.Name] = true
	}
	for _, f := range files {
		t.Run(f.Name, func(t *testing.T) {
			data := f.Read(t, false)
			if int64(len(data)) != f.Size {
				t.Errorf("size is %d, manifest says %d", len(data), f.Size)
			}
			format, err := ooxmltest.Sniff(data)
			if err != nil || format != f.Format {
				t.Errorf("sniffed %q (%v), manifest says %q", format, err, f.Format)
			}
			if f.Variant != "" && !names[f.Variant] {
				t.Errorf("variant of unknown fixture %q", f.Variant)
			}
			doc := expected(t, f)
			if doc.Source.Format != f.Format || len(doc.Parts) == 0 {
				t.Errorf("expected content has format %q and %d parts", doc.Source.Format, len(doc.Parts))
			}
		})
	}
}

var isoDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}(T\d{2}:\d{2}:\d{2})?$`)

// The expected display text of every spreadsheet cell was written by hand.
// LibreOffice is asked for the same cells, so a mistake in the expectations
// is caught here and not blamed on the library later.
//
// Dates are the stated exception (plan decision D5): this library gives
// them in ISO 8601 whatever the cell's format, so there the two must differ.
func TestExpectedCellTextMatchesLibreOffice(t *testing.T) {
	for _, f := range ooxmltest.LoadManifest(t, "fixtures.json") {
		if f.Name != "values" && f.Name != "values.lo" {
			continue
		}
		t.Run(f.Name, func(t *testing.T) {
			want := expected(t, f).Parts[0].Blocks[0].Table.Rows
			got := ooxmltest.LibreOfficeCSV(t, f.Read(t, false))["Values"]
			if len(got) != len(want) {
				t.Fatalf("LibreOffice shows %d rows, expected content has %d", len(got), len(want))
			}
			for r := range want {
				label, shown := want[r][0], want[r][1]
				switch {
				case len(got[r]) < 2:
					t.Errorf("row %d (%s): LibreOffice shows %v", r+1, label, got[r])
				case isoDate.MatchString(shown):
					if got[r][1] == shown {
						t.Errorf("row %d (%s): LibreOffice also shows %q; the row no longer tests the ISO rule", r+1, label, shown)
					}
				case got[r][1] != shown:
					t.Errorf("row %d (%s): expected content says %q, LibreOffice shows %q", r+1, label, shown, got[r][1])
				}
			}
		})
	}
}

// accuracy is one line of bench/accuracy.json: how well a tool in use today
// recovers the known text of a fixture.
type accuracy struct {
	Fixture string `json:"fixture"`
	Tool    string `json:"tool"`
	Version string `json:"version"`
	ooxmltest.Score
	Error string `json:"error,omitempty"`
}

var toolsByFormat = map[string][]string{
	"docx": {"python-docx", "docx2txt", "markitdown"},
	"xlsx": {"openpyxl", "openpyxl-readonly", "markitdown"},
	"pptx": {"python-pptx", "markitdown"},
}

// TestBaselineAccuracy scores the comparison tools against the known content
// of every fixture and holds the result to bench/accuracy.json, the baseline
// this library has to beat. The tools are pinned, so a difference means the
// fixtures, the scorer or the pins changed; -update records the new numbers.
func TestBaselineAccuracy(t *testing.T) {
	if _, ok := ooxmltest.PythonBin(t); !ok {
		t.Skip("the Python comparison environment is not set up (see bench/README.md)")
	}
	var got []accuracy
	for _, f := range ooxmltest.LoadManifest(t, "fixtures.json") {
		want := expected(t, f).Text()
		data := f.Read(t, false)
		for _, tool := range toolsByFormat[f.Format] {
			res := ooxmltest.Python(t, tool, data, f.Format)
			a := accuracy{Fixture: f.Name, Tool: tool, Version: res.Version, Error: res.Error}
			if res.Error == "" {
				a.Score = ooxmltest.Compare(res.Text, want)
			}
			got = append(got, a)
		}
	}
	sort.Slice(got, func(i, j int) bool {
		if got[i].Fixture != got[j].Fixture {
			return got[i].Fixture < got[j].Fixture
		}
		return got[i].Tool < got[j].Tool
	})
	out, err := json.MarshalIndent(got, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	out = append(out, '\n')
	path := filepath.Join(ooxmltest.Root(t), "bench", "accuracy.json")
	if ooxmltest.Updating() {
		if err := os.WriteFile(path, out, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no recorded baseline (run with -update): %v", err)
	}
	if strings.TrimSpace(string(stored)) != strings.TrimSpace(string(out)) {
		t.Errorf("tool accuracy differs from bench/accuracy.json; if the fixtures or pins changed on purpose, run with -update and review the diff")
	}
}
