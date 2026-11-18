package ooxmltest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// The oracles are other tools' readings of a file, used to judge this
// library's output. Each skips the calling test when its tool is absent, so
// the suite runs anywhere and uses whatever is installed.
//
// Results are cached under the user's cache directory, keyed by the hash of
// the input and the conversion, because starting LibreOffice takes seconds.

func oracleCache(t testing.TB, data []byte, kind string) string {
	t.Helper()
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	sum := sha256.Sum256(append([]byte(kind+"\x00"), data...))
	dir := filepath.Join(base, "gofastooxml-oracle", hex.EncodeToString(sum[:16]))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func run(t testing.TB, timeout time.Duration, name string, args ...string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("%s: %w: %s", name, err, clip(stderr.Bytes()))
	}
	return out, nil
}

// soffice converts data with LibreOffice and returns the directory that
// holds the output files.
func soffice(t testing.TB, data []byte, ext, filter string) string {
	t.Helper()
	bin, err := exec.LookPath("soffice")
	if err != nil {
		t.Skip("LibreOffice (soffice) is not installed")
	}
	dir := oracleCache(t, data, "soffice:"+filter)
	if _, err := os.Stat(filepath.Join(dir, "done")); err == nil {
		return dir
	}
	in := filepath.Join(dir, "input."+ext)
	if err := os.WriteFile(in, data, 0o644); err != nil {
		t.Fatal(err)
	}
	// A private profile lets conversions run while the user has LibreOffice
	// open, and lets several run at once.
	profile := "-env:UserInstallation=file://" + filepath.Join(dir, "profile")
	if _, err := run(t, 3*time.Minute, bin, profile, "--headless", "--norestore", "--convert-to", filter, "--outdir", dir, in); err != nil {
		t.Fatalf("LibreOffice conversion failed: %v", err)
	}
	os.RemoveAll(filepath.Join(dir, "profile"))
	os.WriteFile(filepath.Join(dir, "done"), nil, 0o644)
	return dir
}

// LibreOfficeText returns the text of a Word file as LibreOffice exports
// it. The export writes computed list numbering, which makes it the oracle
// for list markers.
func LibreOfficeText(t testing.TB, docx []byte) string {
	t.Helper()
	dir := soffice(t, docx, "docx", "txt:Text (encoded):UTF8")
	out, err := os.ReadFile(filepath.Join(dir, "input.txt"))
	if err != nil {
		t.Fatalf("LibreOffice produced no text: %v", err)
	}
	return strings.TrimPrefix(string(out), "\uFEFF")
}

// LibreOfficeCSV returns the cells of every sheet of a workbook as
// LibreOffice displays them, keyed by sheet name. Values are formatted, so
// this is the oracle for number formats.
func LibreOfficeCSV(t testing.TB, xlsx []byte) map[string][][]string {
	t.Helper()
	// Filter tokens, by position: 1 separator ",", 2 quote ", 3 UTF-8,
	// 4 from row 1, 5 no column formats, 6 default language, 7 quote only
	// where needed, 8 (import only), 9 cell content as shown, 10 values not
	// formulas, 11 keep spaces, 12 sheet -1: every sheet to its own file.
	// Token 9 is the one that matters: without it LibreOffice writes raw
	// numbers and the oracle says nothing about number formats.
	dir := soffice(t, xlsx, "xlsx", "csv:Text - txt - csv (StarCalc):44,34,76,1,,0,false,true,true,false,false,-1")
	matches, _ := filepath.Glob(filepath.Join(dir, "input-*.csv"))
	sort.Strings(matches)
	sheets := map[string][][]string{}
	for _, m := range matches {
		f, err := os.Open(m)
		if err != nil {
			t.Fatal(err)
		}
		r := csv.NewReader(f)
		r.FieldsPerRecord = -1
		r.LazyQuotes = true
		rows, err := r.ReadAll()
		f.Close()
		if err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), "input-"), ".csv")
		sheets[name] = rows
	}
	if len(sheets) == 0 {
		t.Fatal("LibreOffice produced no CSV")
	}
	return sheets
}

// Pandoc converts a Word file with pandoc to the given output format
// ("markdown", "json", "plain"). Pandoc reads heading levels from styles,
// which makes it an oracle for document structure.
func Pandoc(t testing.TB, docx []byte, to string) string {
	t.Helper()
	bin, err := exec.LookPath("pandoc")
	if err != nil {
		t.Skip("pandoc is not installed")
	}
	dir := oracleCache(t, docx, "pandoc:"+to)
	cached := filepath.Join(dir, "out")
	if out, err := os.ReadFile(cached); err == nil {
		return string(out)
	}
	in := filepath.Join(dir, "input.docx")
	if err := os.WriteFile(in, docx, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, time.Minute, bin, "--from", "docx", "--to", to, "--wrap=none", in)
	if err != nil {
		t.Fatalf("pandoc failed: %v", err)
	}
	os.WriteFile(cached, out, 0o644)
	return string(out)
}

// PythonResult is what testdata/gen/oracle.py prints for one tool and file.
type PythonResult struct {
	Tool    string `json:"tool"`
	Version string `json:"version"`
	// Text is everything the tool extracted, as plain text.
	Text string `json:"text"`
	// Error is set, and Text empty, when the tool failed on the file.
	Error string `json:"error,omitempty"`
}

// PythonBin returns the interpreter of the comparison environment: the
// GOFASTOOXML_PYTHON variable if set, else bench-env/.venv-ooxml beside the
// repository. The second result is false when there is none.
func PythonBin(t testing.TB) (string, bool) {
	t.Helper()
	bin := os.Getenv("GOFASTOOXML_PYTHON")
	if bin == "" {
		bin = filepath.Join(Root(t), "..", "bench-env", ".venv-ooxml", "bin", "python")
	}
	if _, err := os.Stat(bin); err != nil {
		return "", false
	}
	return bin, true
}

// Python runs one of the comparison tools (python-docx, openpyxl,
// python-pptx, docx2txt, markitdown) on a file through
// testdata/gen/oracle.py.
func Python(t testing.TB, tool string, data []byte, ext string) PythonResult {
	t.Helper()
	bin, ok := PythonBin(t)
	if !ok {
		t.Skip("the Python comparison environment is not set up (see bench/README.md)")
	}
	dir := oracleCache(t, data, "python:"+tool)
	cached := filepath.Join(dir, "out.json")
	out, err := os.ReadFile(cached)
	if err != nil {
		in := filepath.Join(dir, "input."+ext)
		if err := os.WriteFile(in, data, 0o644); err != nil {
			t.Fatal(err)
		}
		out, err = run(t, 2*time.Minute, bin, filepath.Join(Root(t), "testdata", "gen", "oracle.py"), tool, in)
		if err != nil {
			t.Fatalf("oracle.py failed: %v", err)
		}
		os.WriteFile(cached, out, 0o644)
	}
	var res PythonResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("oracle.py output: %v", err)
	}
	return res
}
