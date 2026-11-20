package gofastooxml_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

var soakRecord = flag.Bool("soak-record", false, "write what TestSoak counts to bench/soak.json")

// soakReport is what the soak run counts, as recorded in bench/soak.json.
type soakReport struct {
	Files    int            `json:"files"`
	Bytes    int64          `json:"bytes"`
	Read     int            `json:"read"`
	Formats  map[string]int `json:"formats"`
	Refused  map[string]int `json:"refused"`
	Warnings map[string]int `json:"documents_with_warning"`
	Display  map[string]int `json:"documents_with_display"`
	Children int            `json:"embedded_documents"`
	// Unlocked counts the encrypted files that were then opened with the
	// password their project's tests give; they are counted as refused
	// too, which is what they are without it.
	Unlocked int `json:"opened_with_their_password"`
	// Truncated lists the files that are archives cut off before their
	// directory: what recovery of truncated archives would be for.
	Truncated []string `json:"truncated_archives"`
	Slowest   []string `json:"slowest"`
}

// soakPasswords are the passwords of encrypted files in the soak set, by
// file name, as the tests of the projects they come from give them. The
// files were written by Microsoft Office 2007, 2010 and 2013, by
// LibreOffice and by Excelize.
var soakPasswords = map[string]string{
	"Encrypted_LO_Standard_abc.docx":      "abc",
	"Encrypted_MSO2007_abc.docx":          "abc",
	"Encrypted_MSO2010_abc.docx":          "abc",
	"Encrypted_MSO2013_abc.docx":          "abc",
	"encryptAES.xlsx":                     "password",
	"encryptSHA1.xlsx":                    "password",
	"encryptSHA512.xlsx":                  "password",
	"protected_normal_case.docx":          "password",
	"protected_passtika.xlsb":             "tika",
	"protected_upper_case.docx":           "password",
	"testEXCEL_protected_passtika.xlsx":   "tika",
	"testEXCEL_protected_passtika_2.xlsx": "tika",
	"testPPT_protected_passtika.pptx":     "tika",
	"testWORD_protected_passtika.docx":    "tika",
}

// textSize returns the bytes of text the document holds, counting a
// string as often as it occurs. Cells that share one string cost the
// document little and whatever writes it out a great deal.
func textSize(doc *gofastcore.Document) (n int64) {
	doc.Walk(func(d *gofastcore.Document, _ int) bool {
		for _, b := range d.AllBlocks() {
			n += int64(len(b.Text))
			if b.Table != nil {
				for _, row := range b.Table.Rows {
					for _, cell := range row {
						n += int64(len(cell))
					}
				}
			}
		}
		return true
	})
	return n
}

// watchHeap stops the test program if its heap passes the limit, and
// returns a function that stops the watching and gives the largest size
// seen. Thousands of files from strangers are read here, some of them
// made to exhaust memory: if one succeeds, or the test itself holds too
// much, the run must end, not the machine's memory.
func watchHeap(limit uint64) (stop func() uint64) {
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	var peak uint64
	quit, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-quit:
				return
			case <-tick.C:
				metrics.Read(sample)
				peak = max(peak, sample[0].Value.Uint64())
				if peak > limit {
					fmt.Fprintf(os.Stderr, "soak: the heap reached %d MB, over the %d MB allowed; stopping\n", peak>>20, limit>>20)
					os.Exit(2)
				}
			}
		}
	}()
	return func() uint64 { close(quit); <-done; return peak }
}

// refusal names the kind of file an error stands for.
func refusal(data []byte, err error) string {
	compound := bytes.HasPrefix(data, []byte{0xD0, 0xCF, 0x11, 0xE0})
	zipped := bytes.HasPrefix(data, []byte("PK\x03\x04"))
	switch {
	case errors.Is(err, gofastcore.ErrPasswordRequired):
		return "needs a password"
	case errors.Is(err, gofastcore.ErrLimitExceeded):
		return "over a limit"
	case compound && errors.Is(err, gofastcore.ErrUnsupported) && strings.Contains(err.Error(), "legacy"):
		return "legacy binary format"
	case compound && errors.Is(err, gofastcore.ErrUnsupported):
		return "encrypted otherwise than with a password"
	case compound:
		return "damaged compound file"
	case zipped && strings.Contains(err.Error(), "directory cannot be read") && !bytes.Contains(data, []byte("PK\x05\x06")):
		return "archive cut off before its directory"
	case zipped && strings.Contains(err.Error(), "directory cannot be read"):
		return "archive with a damaged directory"
	case errors.Is(err, gofastcore.ErrUnsupported) && (zipped || strings.Contains(err.Error(), "zip archive")):
		// An archive need not begin with a member: it may have other
		// bytes in front.
		return "archive that is not a Word, Excel or PowerPoint package"
	case zipped:
		return "damaged package"
	case len(data) == 0:
		return "empty file"
	case errors.Is(err, gofastcore.ErrUnsupported):
		return "not an archive"
	default:
		return "not an archive, malformed"
	}
}

// TestSoak reads every file of the corpus and of the soak set
// (testdata/soak.py) several ways and holds each to what holds for any
// input: a valid document or a shared error, the same for any number of
// workers, the same a part at a time, within limits, in bounded time. It
// asserts nothing about what a file says; it counts what happened.
func TestSoak(t *testing.T) {
	if testing.Short() {
		t.Skip("reads thousands of files")
	}
	root := filepath.Join(ooxmltest.Root(t), "testdata", "corpus")
	extensions := map[string]bool{}
	for _, e := range []string{"docx", "docm", "dotx", "dotm", "xlsx", "xlsm", "xltx", "xltm", "xlsb", "pptx", "pptm", "ppsx", "ppsm", "potx", "potm"} {
		extensions["."+e] = true
	}
	var paths []string
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return nil
		case d.IsDir() && (d.Name() == ".git" || d.Name() == "generated"):
			return filepath.SkipDir
		case !d.IsDir() && extensions[strings.ToLower(filepath.Ext(path))]:
			paths = append(paths, path)
		}
		return nil
	})
	if len(paths) < 1000 {
		t.Skipf("%d files under testdata/corpus: fetch the soak set with python3 testdata/soak.py", len(paths))
	}
	sort.Strings(paths)

	// What the run may hold at once: several documents, each bounded by
	// the limits, and their JSON forms. The race detector multiplies what
	// the process takes for each byte of heap, so less is allowed there.
	ceiling := uint64(4 << 30)
	if underRace {
		ceiling = 2 << 30
	}
	stopWatching := watchHeap(ceiling)

	report := soakReport{Formats: map[string]int{}, Refused: map[string]int{}, Warnings: map[string]int{}, Display: map[string]int{}}
	type timed struct {
		name string
		took time.Duration
	}
	var (
		mu    sync.Mutex
		times []timed
		// refused lists every file that was not read, with the reason.
		refused []string
		// large lists the documents too long to be written out and
		// compared.
		large []string
		wg    sync.WaitGroup
		next  = make(chan string)
	)
	ctx := context.Background()
	one := func(path string) {
		name, _ := filepath.Rel(root, path)
		name = filepath.ToSlash(name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			return
		}
		read := func(e *gofastooxml.Extractor, opts *gofastcore.Options) (doc *gofastcore.Document, err error) {
			// A file that takes this long has found a way around the
			// limits; the slowest file of the set takes a few seconds,
			// and some tens under the race detector.
			limit := 2 * time.Minute
			if underRace {
				limit = 20 * time.Minute
			}
			within(t, limit, name, func() {
				doc, err = e.Extract(ctx, bytes.NewReader(data), int64(len(data)), opts)
			})
			checkOutcome(t, name, doc, err)
			return doc, err
		}
		plain := &gofastooxml.Extractor{}
		start := time.Now()
		doc, err := read(plain, &gofastcore.Options{Workers: 1, Child: childExtractor})
		took := time.Since(start)

		// The same for any number of workers.
		again, againErr := read(plain, &gofastcore.Options{Workers: 8, Child: childExtractor})
		if (err == nil) != (againErr == nil) {
			t.Errorf("%s: one worker: %v; eight: %v", name, err, againErr)
		}
		// Spans say how the text is set and change none of it. Pictures
		// add their blocks, so with them only the outcome is checked.
		spans, _ := read(plain, &gofastcore.Options{Workers: 4, Spans: true, Cells: true})
		read(plain, &gofastcore.Options{Workers: 4, Spans: true, Images: gofastcore.ImagesData})
		// Under limits a real file runs into.
		// (With a child extractor: a document cut off by a limit still
		// has its embedded files to account for.)
		read(&gofastooxml.Extractor{MaxCells: 40, MaxArchiveMembers: 12}, &gofastcore.Options{Workers: 3, Child: childExtractor})
		read(plain, &gofastcore.Options{Workers: 3, Child: childExtractor, Limits: gofastcore.Limits{MaxDecodedBytes: 50_000}})
		read(plain, &gofastcore.Options{Workers: 3, Child: childExtractor, Limits: gofastcore.Limits{MaxTextBytes: 2_000}})

		unlocked := false
		if password, known := soakPasswords[filepath.Base(path)]; known && strings.HasPrefix(name, "soak/") {
			opened, openErr := read(plain, &gofastcore.Options{Password: password})
			if openErr != nil || !warned(opened, gofastooxml.WarnDecrypted) || warned(opened, gofastooxml.WarnIntegrityMismatch) {
				t.Errorf("%s: with its password: %v", name, openErr)
			}
			unlocked = openErr == nil
		}

		mu.Lock()
		defer mu.Unlock()
		report.Files++
		if unlocked {
			report.Unlocked++
		}
		report.Bytes += int64(len(data))
		times = append(times, timed{name, took})
		if err != nil {
			kind := refusal(data, err)
			report.Refused[kind]++
			refused = append(refused, fmt.Sprintf("%-55s %s: %.160s", kind, name, err))
			if kind == "archive cut off before its directory" {
				report.Truncated = append(report.Truncated, name)
			}
			return
		}
		mu.Unlock()
		// A document with more text than this is checked as far as it has
		// been: writing it out several times over is what a hostile file
		// that multiplies one string is after.
		big := textSize(doc) > 64<<20
		if !big {
			whole := asJSON(t, doc)
			if again != nil && asJSON(t, again) != whole {
				t.Errorf("%s: the document differs between one worker and eight", name)
			}
			if spans != nil && spans.Text() != doc.Text() {
				t.Errorf("%s: the text differs when spans are asked for", name)
			}
			// Encoded and decoded, it is the same document.
			var back gofastcore.Document
			if err := json.Unmarshal([]byte(whole), &back); err != nil || asJSON(t, &back) != whole {
				t.Errorf("%s: the document does not survive its JSON form (%v)", name, err)
			}
			// Rendering reads every field.
			_ = doc.Markdown()
			// A part at a time gives the parts of the document.
			if f, err := gofastooxml.OpenBytes(data, &gofastcore.Options{Workers: 3, Child: childExtractor}); err != nil {
				t.Errorf("%s: read whole but not opened: %v", name, err)
			} else {
				n := 0
				for part, err := range f.Parts(ctx) {
					if err != nil {
						t.Errorf("%s: part %d: %v", name, n+1, err)
						break
					}
					if n >= len(doc.Parts) || asJSON(t, part) != asJSON(t, doc.Parts[n]) {
						t.Errorf("%s: part %d read alone differs from the document's", name, n+1)
						break
					}
					n++
				}
				if n != len(doc.Parts) {
					t.Errorf("%s: %d parts read one at a time, the document has %d", name, n, len(doc.Parts))
				}
				f.Close()
			}
		}
		mu.Lock()
		if big {
			large = append(large, name)
		}
		report.Read++
		report.Formats[doc.Source.Format]++
		report.Children += len(doc.Children)
		codes, reasons := map[string]bool{}, map[string]bool{}
		for _, w := range doc.AllWarnings() {
			codes[w.Code] = true
		}
		var hidden gofastcore.Display
		for _, b := range doc.AllBlocks() {
			hidden |= b.Display
			if b.Table != nil {
				for _, h := range b.Table.Display {
					hidden |= h.Display
				}
			}
		}
		if hidden != 0 {
			for _, r := range strings.Split(hidden.String(), "+") {
				reasons[r] = true
			}
		}
		for c := range codes {
			report.Warnings[c]++
		}
		for r := range reasons {
			report.Display[r]++
		}
	}
	for range max(2, runtime.GOMAXPROCS(0)/3) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range next {
				one(path)
			}
		}()
	}
	for _, p := range paths {
		next <- p
	}
	close(next)
	wg.Wait()

	sort.Slice(times, func(i, j int) bool { return times[i].took > times[j].took })
	for _, s := range times[:min(8, len(times))] {
		report.Slowest = append(report.Slowest, fmt.Sprintf("%s: %s", s.name, s.took.Round(time.Millisecond)))
	}
	peak := stopWatching()
	t.Logf("largest heap during the run: %d MB (allowed %d MB); documents of over 64 MB of text, read and not compared: %v", peak>>20, ceiling>>20, large)
	sort.Strings(report.Truncated)
	sort.Strings(refused)
	t.Logf("not read:\n%s", strings.Join(refused, "\n"))
	out, err := json.MarshalIndent(report, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s", out)
	if refused := report.Files - report.Read; report.Read == 0 || refused*5 > report.Files {
		t.Errorf("%d of %d files refused", refused, report.Files)
	}
	if *soakRecord {
		// Timings are of this run and this machine; the record is of
		// what the files are and what became of them.
		report.Slowest = nil
		out, _ := json.MarshalIndent(report, "", " ")
		if err := os.WriteFile(filepath.Join(ooxmltest.Root(t), "bench", "soak.json"), append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
