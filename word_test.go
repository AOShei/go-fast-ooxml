package gofastooxml_test

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"

	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// The Word fixtures have exactly known content. Each must come out with
// the same blocks: kinds, heading levels, list levels, text and tables.
func TestWordFixtures(t *testing.T) {
	for _, f := range ooxmltest.LoadManifest(t, "fixtures.json") {
		if f.Format != "docx" {
			continue
		}
		t.Run(f.Name, func(t *testing.T) {
			doc, err := extract(t, f.Read(t, false), nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := doc.Validate(); err != nil {
				t.Error(err)
			}
			ooxmltest.CompareBlocks(t, doc, expected(t, f), ooxmltest.CompareOptions{})
		})
	}
}

// wordScore is how this library's reading of one corpus file compares
// with what other programs make of it.
type wordScore struct {
	name string
	// recall is the share of the words python-docx finds that this
	// library finds too. python-docx reads the body and its tables only,
	// so this library should find everything it does.
	recall float64
	// found counts the words of LibreOffice's text export that this
	// library has too, of words. LibreOffice writes a footnote's number
	// straight after the word it hangs on ("claim.3") and leaves the
	// footnote's text out, so a word is also counted as found without
	// such a number, and what this library has beyond LibreOffice
	// (footnote text, comments) is not held against it.
	found, words int
	// labels counts the numbered list items and headings whose label and
	// text are found together in LibreOffice's export, of how many.
	labelsFound, labels int
	// headings counts the headings pandoc reports that this library
	// reports too, with the same level and text, of how many pandoc
	// reports. pandoc knows a heading by its style name only; this library
	// also by a declared outline level, which is Word's own definition, so
	// it finds more, and those are counted apart.
	headingsBoth, headingsPandoc, headingsExtra int
}

var spaces = regexp.MustCompile(`\s+`)

// TestWordCorpus compares every sound Word file in the corpus with three
// other programs: python-docx for the words of the body, LibreOffice for
// all text and for computed list labels, pandoc for heading levels. The
// first run converts each file with LibreOffice, which takes minutes; the
// results are cached.
func TestWordCorpus(t *testing.T) {
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
		if f.Format != "docx" || f.Note != "" {
			continue
		}
		if data, err := os.ReadFile(f.AbsPath(t)); err == nil {
			files, contents = append(files, f), append(contents, data)
		}
	}
	if len(files) == 0 {
		t.Skip("no corpus files downloaded")
	}
	ooxmltest.WarmLibreOfficeText(t, contents)

	var total wordScore
	var recallSum float64
	scored, limited := 0, 0
	for i, f := range files {
		data := contents[i]
		doc, err := extract(t, data, nil)
		if err != nil {
			t.Errorf("%s: %v", f.Name, err)
			continue
		}
		if warned(doc, gofastcore.WarnInputAmbiguous) {
			continue // programs read different content from such a file by design
		}
		if doc.Parts[0].Has(gofastcore.WarnLimitExceeded) {
			limited++ // cut off on purpose, and says so; not a reading to score
			continue
		}
		ours := doc.Text()
		s := wordScore{name: f.Name, recall: 1}
		// Where python-docx finds no words there is nothing to recall.
		if py := ooxmltest.Python(t, "python-docx", data, "docx"); py.Error == "" && len(ooxmltest.Words(py.Text)) > 0 {
			s.recall = ooxmltest.Compare(ours, py.Text).Recall
		}
		if lo, ok := ooxmltest.LibreOfficeTextOK(t, data); ok {
			s.found, s.words = recallOf(ours, lo)
			flat := spaces.ReplaceAllString(lo, " ")
			for _, b := range doc.Parts[0].Blocks {
				if b.Marker == "" || !b.Ordered && b.Kind != gofastcore.KindHeading {
					continue
				}
				text := spaces.ReplaceAllString(b.Text, " ")
				if len(text) > 24 {
					text = text[:24]
					for len(text) > 0 && !utf8.ValidString(text) {
						text = text[:len(text)-1]
					}
				}
				s.labels++
				if strings.Contains(flat, b.Marker+" "+text) {
					s.labelsFound++
				}
			}
		}
		if md, ok := pandocHeadings(t, data); ok {
			mine := map[string]int{}
			for _, b := range doc.Parts[0].Blocks {
				if b.Kind == gofastcore.KindHeading {
					mine[fmt.Sprintf("%d %s", b.Level, spaces.ReplaceAllString(b.Text, " "))]++
				}
			}
			for k, n := range md {
				both := min(n, mine[k])
				s.headingsBoth += both
				s.headingsPandoc += n
				mine[k] -= both
			}
			for _, n := range mine {
				s.headingsExtra += n
			}
		}
		scored++
		recallSum += s.recall
		total.found += s.found
		total.words += s.words
		total.labels += s.labels
		total.labelsFound += s.labelsFound
		total.headingsBoth += s.headingsBoth
		total.headingsPandoc += s.headingsPandoc
		total.headingsExtra += s.headingsExtra
		if s.recall < 0.98 || s.words-s.found > 20 {
			t.Logf("%-58s recall of python-docx %.3f; %d of LibreOffice's %d words found", f.Name, s.recall, s.found, s.words)
		}
	}
	n := float64(scored)
	recall, everything := recallSum/n, ratio(total.found, total.words)
	labels, headings := ratio(total.labelsFound, total.labels), ratio(total.headingsBoth, total.headingsPandoc)
	t.Logf("%d files cut off at a limit and not scored", limited)
	t.Logf("%d files: mean recall of python-docx %.4f; LibreOffice's words found %d of %d (%.4f); labels agreeing with LibreOffice %d of %d (%.3f); pandoc's headings found with the same level %d of %d (%.3f), plus %d headings pandoc does not report",
		scored, recall, total.found, total.words, everything, total.labelsFound, total.labels, labels, total.headingsBoth, total.headingsPandoc, headings, total.headingsExtra)

	// The floors are a little under what was measured when they were set,
	// so that a regression fails and noise does not. See bench/README.md.
	for _, c := range []struct {
		what       string
		got, floor float64
	}{
		{"mean recall of python-docx", recall, wordFloors.recall},
		{"LibreOffice's words found", everything, wordFloors.everything},
		{"labels agreeing with LibreOffice", labels, wordFloors.labels},
		{"pandoc's headings found with the same level", headings, wordFloors.headings},
	} {
		if c.got < c.floor {
			t.Errorf("%s is %.4f, below the floor of %.4f", c.what, c.got, c.floor)
		}
	}
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 1
	}
	return float64(a) / float64(b)
}

// wordFloors are the scores TestWordCorpus holds the library to.
var wordFloors = struct{ recall, everything, labels, headings float64 }{0.99, 0.99, 0.98, 0.98}

var noteAnchor = regexp.MustCompile(`^(.*?\D)\(?\d{1,3}\)?(\PL*)$`)

// recallOf counts how many of the reference's words are in ours. A
// reference word that ends in a footnote number ("claim.3", "claim(3).")
// is also looked for without it.
func recallOf(ours, reference string) (found, total int) {
	have := map[string]int{}
	for _, w := range ooxmltest.Words(ours) {
		have[w]++
	}
	take := func(w string) bool {
		if have[w] > 0 {
			have[w]--
			return true
		}
		return false
	}
	for _, w := range ooxmltest.Words(reference) {
		total++
		if take(w) {
			found++
			continue
		}
		if m := noteAnchor.FindStringSubmatch(w); m != nil && (take(m[1]+m[2]) || take(m[1])) {
			found++
		}
	}
	return found, total
}

var headingLine = regexp.MustCompile(`(?m)^(#{1,6}) +(.+?)\s*$`)

// pandocHeadings returns the headings pandoc finds in a Word file, as
// "level text" with a count, or false if pandoc cannot read the file.
func pandocHeadings(t testing.TB, data []byte) (map[string]int, bool) {
	md, ok := ooxmltest.PandocOK(t, data, "markdown_strict-raw_html")
	if !ok {
		return nil, false
	}
	out := map[string]int{}
	for _, m := range headingLine.FindAllStringSubmatch(md, -1) {
		text := strings.NewReplacer(`\`, "", "*", "", "_", "").Replace(m[2])
		out[fmt.Sprintf("%d %s", len(m[1]), spaces.ReplaceAllString(strings.TrimSpace(text), " "))]++
	}
	return out, true
}
