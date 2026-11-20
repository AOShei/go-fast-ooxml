package gofastooxml_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"html"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// The generated presentations, one written by python-pptx and one saved
// again by LibreOffice, against their known content: the same slides,
// and on each the same blocks in the same order with the same hidden
// flags.
func TestPowerPointFixtures(t *testing.T) {
	for _, f := range ooxmltest.LoadManifest(t, "fixtures.json") {
		if f.Format != "pptx" {
			continue
		}
		t.Run(f.Name, func(t *testing.T) {
			doc, err := extract(t, f.Read(t, false), nil)
			if err != nil {
				t.Fatal(err)
			}
			ooxmltest.CheckDocument(t, doc)
			// LibreOffice does not write which row of a table is its
			// header, so the file it saved does not say.
			ooxmltest.CompareBlocks(t, doc, expected(t, f), ooxmltest.CompareOptions{Display: true, NoHeaderRows: f.Variant != ""})
		})
	}
}

// pySlide is one slide as python-pptx reads it (the python-pptx-slides
// oracle in testdata/gen/tools.py).
type pySlide struct {
	Body      []string `json:"body"`
	Furniture []string `json:"furniture"`
	Title     *string  `json:"title"`
	Notes     string   `json:"notes"`
	Hidden    bool     `json:"hidden"`
}

// powerPointFloors are the scores TestPowerPointCorpus holds the library
// to: a little under what was measured when they were set.
var powerPointFloors = struct{ f1, recall, titles float64 }{0.99, 0.999, 0.99}

// TestPowerPointCorpus compares every sound PowerPoint file in the corpus
// with python-pptx: the words of the slides and of the speaker's notes,
// the titles, and which slides are hidden.
//
// python-pptx reads the shapes of a slide and nothing of its layout or
// master. So the words are compared twice: everything python-pptx reads
// must be found (recall), and what this library reads apart from
// furniture must be what python-pptx reads apart from the date, footer
// and slide-number placeholders (F1).
func TestPowerPointCorpus(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the whole corpus")
	}
	if _, ok := ooxmltest.PythonBin(t); !ok {
		t.Skip("the Python comparison environment is not set up")
	}
	var files []ooxmltest.File
	var contents [][]byte
	for _, f := range ooxmltest.LoadManifest(t, "corpus.json") {
		if f.Format != "pptx" || f.Note != "" {
			continue
		}
		if data, err := os.ReadFile(f.AbsPath(t)); err == nil {
			files, contents = append(files, f), append(contents, data)
		}
	}
	if len(files) == 0 {
		t.Skip("no corpus files downloaded")
	}
	ooxmltest.WarmPython(t, "python-pptx-slides", "pptx", contents)
	scored, unread, slides := 0, 0, 0
	var f1, recall float64
	titles, titlesSame, hiddenSame := 0, 0, 0
	for i, f := range files {
		data := contents[i]
		doc, err := extract(t, data, nil)
		if err != nil {
			t.Errorf("%s: %v", f.Name, err)
			continue
		}
		ooxmltest.CheckDocument(t, doc)
		py := ooxmltest.Python(t, "python-pptx-slides", data, "pptx")
		var theirs []pySlide
		if py.Error != "" || json.Unmarshal([]byte(py.Text), &theirs) != nil {
			unread++ // python-pptx cannot read the file
			continue
		}
		if len(theirs) != pageCount(doc) && len(theirs) > 0 {
			t.Errorf("%s: %d slides, python-pptx reads %d", f.Name, pageCount(doc), len(theirs))
			continue
		}
		smartArt := diagramTexts(data)
		var body, all, pyBody, pyAll strings.Builder
		for k, sl := range theirs {
			part := &doc.Parts[k]
			slides++
			if sl.Hidden == (part.Display != 0) {
				hiddenSame++
			}
			if sl.Title != nil && len(ooxmltest.Words(*sl.Title)) > 0 {
				titles++
				if strings.Join(strings.Fields(*sl.Title), " ") == part.Name {
					titlesSame++
				} else {
					t.Logf("%s slide %d: title %q, python-pptx has %q", f.Name, part.Number, part.Name, *sl.Title)
				}
			}
			pyBody.WriteString(strings.Join(sl.Body, "\n") + "\n" + sl.Notes + "\n")
			pyAll.WriteString(strings.Join(sl.Body, "\n") + "\n" + strings.Join(sl.Furniture, "\n") + "\n" + sl.Notes + "\n")
			for _, b := range part.Blocks {
				text := b.Text
				switch {
				case b.Kind == gofastcore.KindImage:
					continue // python-pptx does not give alternative text
				case b.Kind == gofastcore.KindNote && b.Attrs[gofastcore.AttrNoteType] != gofastcore.NoteSpeaker:
					continue // nor comments
				case b.Attrs[gofastcore.AttrRole] == gofastcore.RoleChart, b.Kind == gofastcore.KindCaption:
					continue // nor charts, compared by TestChartsAsOracles
				case b.Kind == gofastcore.KindListItem && smartArt[b.Text]:
					continue // nor SmartArt, compared by TestSmartArtAsLibreOffice
				case b.Table != nil:
					var sb strings.Builder
					for _, row := range b.Table.Rows {
						sb.WriteString(strings.Join(row, "\t") + "\n")
					}
					text = sb.String()
				}
				all.WriteString(text + "\n")
				if !b.Kind.Furniture() {
					body.WriteString(text + "\n")
				}
			}
		}
		s, r := ooxmltest.Compare(body.String(), pyBody.String()), ooxmltest.Compare(all.String(), pyAll.String())
		scored++
		f1 += s.F1
		recall += r.Recall
		if s.F1 < 0.99 || r.Recall < 0.999 {
			t.Logf("%-50s body: precision %.3f recall %.3f F1 %.3f; everything: recall %.3f", f.Name, s.Precision, s.Recall, s.F1, r.Recall)
		}
	}
	n := float64(scored)
	t.Logf("%d files (%d more that python-pptx cannot read), %d slides: mean F1 of the body %.4f; mean recall of everything python-pptx reads %.4f; titles the same %d of %d; hidden slides the same %d of %d",
		scored, unread, slides, f1/n, recall/n, titlesSame, titles, hiddenSame, slides)
	for _, c := range []struct {
		what       string
		got, floor float64
	}{
		{"mean F1 of the body against python-pptx", f1 / n, powerPointFloors.f1},
		{"mean recall of everything python-pptx reads", recall / n, powerPointFloors.recall},
		{"titles the same as python-pptx", ratio(titlesSame, titles), powerPointFloors.titles},
	} {
		if c.got < c.floor {
			t.Errorf("%s is %.4f, below the floor of %.4f", c.what, c.got, c.floor)
		}
	}
	if hiddenSame != slides {
		t.Errorf("hidden slides differ from python-pptx on %d of %d slides", slides-hiddenSame, slides)
	}
}

// diagramTexts returns the paragraphs of the SmartArt data models of a
// package: text python-pptx does not read.
func diagramTexts(data []byte) map[string]bool {
	out := map[string]bool{}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return out
	}
	para := regexp.MustCompile(`(?s)<a:p>.*?</a:p>`)
	run := regexp.MustCompile(`(?s)<a:t>(.*?)</a:t>`)
	for _, f := range z.File {
		if !strings.Contains(f.Name, "diagrams/data") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		xmlData, _ := io.ReadAll(io.LimitReader(rc, 16<<20))
		rc.Close()
		for _, p := range para.FindAll(xmlData, -1) {
			var b strings.Builder
			for _, m := range run.FindAllSubmatch(p, -1) {
				b.WriteString(html.UnescapeString(string(m[1])))
			}
			out[strings.TrimSpace(b.String())] = true
		}
	}
	return out
}
