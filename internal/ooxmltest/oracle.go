package ooxmltest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
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
	out, stderr, err := runContained(timeout, name, args...)
	if err != nil {
		return out, fmt.Errorf("%s: %w: %s", name, err, clip(stderr))
	}
	return out, nil
}

// runContained runs an oracle program with a time limit and a limit on the
// size of the files it writes, and makes sure nothing it started is left
// running. See contain.
func runContained(timeout time.Duration, name string, args ...string) (stdout, stderr []byte, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	cmd.WaitDelay = 5 * time.Second
	cleanup := contain(cmd)
	defer cleanup()
	err = cmd.Run()
	return out.Bytes(), errOut.Bytes(), err
}

// tidy removes what LibreOffice leaves in a conversion directory besides
// its output: the private profile, lock files and temporary files.
func tidy(dir string) {
	os.RemoveAll(filepath.Join(dir, "profile"))
	for _, pattern := range []string{"lu*.tmp", ".~lock.*"} {
		leftovers, _ := filepath.Glob(filepath.Join(dir, pattern))
		for _, f := range leftovers {
			os.RemoveAll(f)
		}
	}
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
	_, err = run(t, 3*time.Minute, bin, profile, "--headless", "--norestore", "--convert-to", filter, "--outdir", dir, in)
	tidy(dir)
	if err != nil {
		t.Fatalf("LibreOffice conversion failed: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "done"), nil, 0o644)
	return dir
}

// LibreOfficeXlsx returns an Excel binary workbook (.xlsb) as LibreOffice
// saves it as .xlsx: the same workbook read by another program, which
// this library's reading of the .xlsx is then set against.
func LibreOfficeXlsx(t testing.TB, xlsb []byte) []byte {
	t.Helper()
	dir := soffice(t, xlsb, "xlsb", "xlsx:Calc MS Excel 2007 XML")
	data, err := os.ReadFile(filepath.Join(dir, "input.xlsx"))
	if err != nil {
		t.Fatalf("LibreOffice wrote no .xlsx: %v", err)
	}
	return data
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

// convertText converts a Word file to text with LibreOffice into the
// oracle cache and returns the text. It does not need a test, so several
// can run at once. A file LibreOffice cannot open, or takes too long over,
// gives ok false; that outcome is cached too, so it is not tried again.
func convertText(bin string, docx []byte, timeout time.Duration) (text string, ok bool) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	sum := sha256.Sum256(append([]byte("soffice:txt:Text (encoded):UTF8\x00"), docx...))
	dir := filepath.Join(base, "gofastooxml-oracle", hex.EncodeToString(sum[:16]))
	if os.MkdirAll(dir, 0o755) != nil {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(dir, "done")); err != nil {
		in := filepath.Join(dir, "input.docx")
		if os.WriteFile(in, docx, 0o644) != nil {
			return "", false
		}
		runContained(timeout, bin, "-env:UserInstallation=file://"+filepath.Join(dir, "profile"),
			"--headless", "--norestore", "--convert-to", "txt:Text (encoded):UTF8", "--outdir", dir, in)
		tidy(dir)
		os.WriteFile(filepath.Join(dir, "done"), nil, 0o644)
	}
	out, err := os.ReadFile(filepath.Join(dir, "input.txt"))
	if err != nil {
		return "", false
	}
	return strings.TrimPrefix(string(out), "\uFEFF"), true
}

// LibreOfficeTextOK is LibreOfficeText for files LibreOffice may not be
// able to open, such as the damaged ones in the corpus: it reports false
// instead of failing the test.
func LibreOfficeTextOK(t testing.TB, docx []byte) (string, bool) {
	t.Helper()
	bin, err := exec.LookPath("soffice")
	if err != nil {
		t.Skip("LibreOffice (soffice) is not installed")
	}
	return convertText(bin, docx, 40*time.Second)
}

// WarmLibreOfficeText converts many Word files at once, so that a test
// which then asks for them one by one finds them in the cache. Starting
// LibreOffice costs seconds a file; this spreads that over the processors.
func WarmLibreOfficeText(t testing.TB, files [][]byte) {
	t.Helper()
	bin, err := exec.LookPath("soffice")
	if err != nil {
		t.Skip("LibreOffice (soffice) is not installed")
	}
	Warm(files, func(data []byte) { convertText(bin, data, 40*time.Second) })
}

// csvFilter makes LibreOffice write every sheet of a workbook to a CSV
// file of its own. The tokens, by position: 1 separator ",", 2 quote ",
// 3 UTF-8, 4 from row 1, 5 no column formats, 6 default language, 7 quote
// only where needed, 8 (import only), 9 cell content as shown, 10 values
// not formulas, 11 keep spaces, 12 sheet -1: every sheet to its own file.
// Token 9 is the one that matters: without it LibreOffice writes raw
// numbers and the oracle says nothing about number formats.
const csvFilter = "csv:Text - txt - csv (StarCalc):44,34,76,1,,0,false,true,true,false,false,-1"

// LibreOfficeCSV returns the cells of every sheet of a workbook as
// LibreOffice displays them, keyed by sheet name. Values are formatted, so
// this is the oracle for number formats.
func LibreOfficeCSV(t testing.TB, xlsx []byte) map[string][][]string {
	t.Helper()
	sheets, err := readCSVs(soffice(t, xlsx, "xlsx", csvFilter))
	if err != nil {
		t.Fatal(err)
	}
	if len(sheets) == 0 {
		t.Fatal("LibreOffice produced no CSV")
	}
	return sheets
}

// readCSVs reads the CSV files LibreOffice wrote into a directory, one a
// sheet, named input-<sheet>.csv.
func readCSVs(dir string) (map[string][][]string, error) {
	matches, _ := filepath.Glob(filepath.Join(dir, "input-*.csv"))
	sort.Strings(matches)
	sheets := map[string][][]string{}
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			return nil, err
		}
		rows := parseCSV(data)
		name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), "input-"), ".csv")
		sheets[name] = rows
	}
	return sheets, nil
}

// parseCSV reads CSV as LibreOffice writes it: fields separated by
// commas, rows by line feeds, a field with either or a quote inside put in
// quotes with its quotes doubled. Unlike encoding/csv it keeps empty
// lines, which are the empty rows of a sheet one column wide: dropping
// them would move every row below.
func parseCSV(data []byte) [][]string {
	var rows [][]string
	var row []string
	var field []byte
	quoted := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		switch {
		case quoted && c == '"' && i+1 < len(data) && data[i+1] == '"':
			field = append(field, '"')
			i++
		case c == '"':
			quoted = !quoted
		case quoted:
			field = append(field, c)
		case c == ',':
			row = append(row, string(field))
			field = field[:0]
		case c == '\n':
			row = append(row, strings.TrimSuffix(string(field), "\r"))
			rows = append(rows, row)
			row, field = nil, field[:0]
		default:
			field = append(field, c)
		}
	}
	if len(field) > 0 || len(row) > 0 {
		rows = append(rows, append(row, string(field)))
	}
	return rows
}

// convertCSV converts a workbook to CSV files with LibreOffice into the
// oracle cache and returns their directory. Like convertText it needs no
// test and caches a failure.
func convertCSV(bin string, xlsx []byte, timeout time.Duration) (dir string, ok bool) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	sum := sha256.Sum256(append([]byte("soffice:"+csvFilter+"\x00"), xlsx...))
	dir = filepath.Join(base, "gofastooxml-oracle", hex.EncodeToString(sum[:16]))
	if os.MkdirAll(dir, 0o755) != nil {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(dir, "done")); err != nil {
		in := filepath.Join(dir, "input.xlsx")
		if os.WriteFile(in, xlsx, 0o644) != nil {
			return "", false
		}
		runContained(timeout, bin, "-env:UserInstallation=file://"+filepath.Join(dir, "profile"),
			"--headless", "--norestore", "--convert-to", csvFilter, "--outdir", dir, in)
		tidy(dir)
		os.WriteFile(filepath.Join(dir, "done"), nil, 0o644)
	}
	return dir, true
}

// LibreOfficeCSVOK is LibreOfficeCSV for workbooks LibreOffice may not be
// able to open: it reports false instead of failing the test.
func LibreOfficeCSVOK(t testing.TB, xlsx []byte) (map[string][][]string, bool) {
	t.Helper()
	bin, err := exec.LookPath("soffice")
	if err != nil {
		t.Skip("LibreOffice (soffice) is not installed")
	}
	dir, ok := convertCSV(bin, xlsx, 90*time.Second)
	if !ok {
		return nil, false
	}
	sheets, err := readCSVs(dir)
	return sheets, err == nil && len(sheets) > 0
}

// Warm runs fn on every file, several at a time, so that oracles which
// cache their results have them ready when a test asks one by one.
func Warm(files [][]byte, fn func(data []byte)) {
	work := make(chan []byte)
	var wg sync.WaitGroup
	for range max(runtime.GOMAXPROCS(0)/3, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for data := range work {
				fn(data)
			}
		}()
	}
	for _, data := range files {
		work <- data
	}
	close(work)
	wg.Wait()
}

// WarmLibreOfficeCSV converts many workbooks at once into the cache.
func WarmLibreOfficeCSV(t testing.TB, files [][]byte) {
	t.Helper()
	bin, err := exec.LookPath("soffice")
	if err != nil {
		t.Skip("LibreOffice (soffice) is not installed")
	}
	Warm(files, func(data []byte) { convertCSV(bin, data, 90*time.Second) })
}

// WarmPython runs a Python tool over many files at once into the cache.
func WarmPython(t testing.TB, tool, ext string, files [][]byte) {
	t.Helper()
	bin, ok := PythonBin(t)
	if !ok {
		t.Skip("the Python comparison environment is not set up (see bench/README.md)")
	}
	root := Root(t)
	Warm(files, func(data []byte) { python(bin, root, tool, data, ext) })
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

// PandocOK is Pandoc for files pandoc may not be able to read: it reports
// false instead of failing the test.
func PandocOK(t testing.TB, docx []byte, to string) (string, bool) {
	t.Helper()
	bin, err := exec.LookPath("pandoc")
	if err != nil {
		t.Skip("pandoc is not installed")
	}
	dir := oracleCache(t, docx, "pandoc-atx:"+to)
	cached := filepath.Join(dir, "out")
	if out, err := os.ReadFile(cached); err == nil {
		return string(out), true
	}
	if _, err := os.Stat(filepath.Join(dir, "failed")); err == nil {
		return "", false
	}
	in := filepath.Join(dir, "input.docx")
	if err := os.WriteFile(in, docx, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, time.Minute, bin, "--from", "docx", "--to", to, "--wrap=none", "--markdown-headings=atx", in)
	if err != nil {
		os.WriteFile(filepath.Join(dir, "failed"), nil, 0o644)
		return "", false
	}
	os.WriteFile(cached, out, 0o644)
	return string(out), true
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
	res, err := python(bin, Root(t), tool, data, ext)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

var (
	adaptersOnce sync.Once
	adaptersSum  string
)

// adapters is a hash of the Python code that reads files for the oracles.
func adapters(root string) string {
	adaptersOnce.Do(func() {
		h := sha256.New()
		for _, name := range []string{"oracle.py", "tools.py"} {
			data, _ := os.ReadFile(filepath.Join(root, "testdata", "gen", name))
			h.Write(data)
		}
		adaptersSum = hex.EncodeToString(h.Sum(nil)[:8])
	})
	return adaptersSum
}

// python runs a tool on a file through testdata/gen/oracle.py, or returns
// the cached result of an earlier run.
func python(bin, root, tool string, data []byte, ext string) (PythonResult, error) {
	var res PythonResult
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	// The key holds the adapters' code, so that changing an adapter
	// does not leave its old answers in place.
	sum := sha256.Sum256(append([]byte("python:"+tool+"\x00"+adapters(root)+"\x00"), data...))
	dir := filepath.Join(base, "gofastooxml-oracle", hex.EncodeToString(sum[:16]))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return res, err
	}
	cached := filepath.Join(dir, "out.json")
	out, err := os.ReadFile(cached)
	if err != nil {
		in := filepath.Join(dir, "input."+ext)
		if err := os.WriteFile(in, data, 0o644); err != nil {
			return res, err
		}
		var stderr []byte
		out, stderr, err = runContained(2*time.Minute, bin, filepath.Join(root, "testdata", "gen", "oracle.py"), tool, in)
		if err != nil {
			return res, fmt.Errorf("oracle.py failed: %v: %s", err, clip(stderr))
		}
		os.WriteFile(cached, out, 0o644)
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return res, fmt.Errorf("oracle.py output: %v", err)
	}
	return res, nil
}
