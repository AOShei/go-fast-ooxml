package gofastooxml_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// asJSON is the JSON form of anything in the shared model, for comparing
// two of them in full.
func asJSON(t testing.TB, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// sample is a file to read both ways.
type sample struct {
	name string
	data []byte
}

// samples returns the fixtures and a file of each format made to have
// several parts, pictures shown more than once, embedded files, notes,
// hidden text and parts that cannot be read.
func samples(t testing.TB) []sample {
	t.Helper()
	var out []sample
	for _, f := range ooxmltest.LoadManifest(t, "fixtures.json") {
		out = append(out, sample{f.Name, f.Read(t, false)})
	}
	png := pngBytes(t, 4, 3)
	inner := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "Inner", Data: xlRow(1, xlStr("A1", "inside"))}).Bytes()

	var slides []ooxmltest.Slide
	for i := 1; i <= 23; i++ {
		slides = append(slides, ooxmltest.Slide{
			Shapes: ooxmltest.Shape(2, ph("title", -1), "", ooxmltest.TextBody(dpara("", plain(fmt.Sprintf("Slide %d", i))))) +
				ooxmltest.Shape(3, ph("", 1), "", ooxmltest.TextBody(dpara("", plain("a point")), dpara(`<a:pPr lvl="1"/>`, colored(white, "hidden in white")))) +
				picture(4, "rId5", "the same picture", 10, 300, 40, 30, "") +
				frame(5, 300, 300, 100, 50, "http://schemas.openxmlformats.org/drawingml/2006/chart", `<c:chart r:id="rId9"/>`),
			Notes:  noteShapes(dpara("", plain(fmt.Sprintf("notes for %d", i)))),
			Hidden: i%7 == 0,
		})
	}
	pptx := deck(slides...)
	pptx.AddBytes("ppt/media/image1.png", "image/png", png)
	for i := range slides {
		slide := fmt.Sprintf("ppt/slides/slide%d.xml", i+1)
		for pptx.Rel(slide, ooxmltest.RelImage, "../media/image1.png") != "rId5" {
		}
		if i%5 == 0 {
			pptx.Rel(slide, ooxmltest.RelPackage, "../embeddings/book.xlsx")
		}
	}
	pptx.AddBytes("ppt/embeddings/book.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", inner)
	pptx.Add("ppt/slides/slide9.xml", ooxmltest.CTSlide, `<p:sld`+ooxmltest.PresNS+`><p:cSld><p:spTree>`+box(2, 1, 1, 9, 9, "", dpara("", plain("cut o"))))
	pptx.ExternalRel("ppt/slides/slide2.xml", ooxmltest.RelImage, "https://example.com/x.png")
	out = append(out, sample{"many slides", pptx.Bytes()})

	var sheets []ooxmltest.Sheet
	for i := 1; i <= 17; i++ {
		var rows strings.Builder
		for r := 1; r <= 30+i; r++ {
			rows.WriteString(xlRow(r, xlNum("A"+strconv.Itoa(r), strconv.Itoa(r*i)), xlStr("B"+strconv.Itoa(r), fmt.Sprintf("sheet %d row %d", i, r))))
		}
		state := ""
		if i == 4 {
			state = "hidden"
		}
		sheets = append(sheets, ooxmltest.Sheet{Name: "S" + strconv.Itoa(i), Data: rows.String(), State: state, Extra: `<drawing r:id="rId1"/>`})
	}
	xlsx := ooxmltest.Xlsx(sheets...)
	drawing := drawingHeader + `<xdr:twoCellAnchor><xdr:from><xdr:col>3</xdr:col><xdr:row>1</xdr:row></xdr:from>` +
		`<xdr:pic><xdr:nvPicPr><xdr:cNvPr id="3" name="Picture"/><xdr:cNvPicPr/></xdr:nvPicPr><xdr:blipFill><a:blip r:embed="rId1"/></xdr:blipFill><xdr:spPr/></xdr:pic></xdr:twoCellAnchor></xdr:wsDr>`
	for i := 1; i <= len(sheets); i++ {
		sheet, name := fmt.Sprintf("xl/worksheets/sheet%d.xml", i), fmt.Sprintf("xl/drawings/drawing%d.xml", i)
		xlsx.Rel(sheet, relBase+"drawing", fmt.Sprintf("../drawings/drawing%d.xml", i))
		xlsx.Add(name, ctDrawing, drawing)
		xlsx.Rel(name, relBase+"image", "../media/image1.png")
	}
	xlsx.AddBytes("xl/media/image1.png", "image/png", png)
	xlsx.Rel("xl/worksheets/sheet3.xml", ooxmltest.RelPackage, "../embeddings/book.xlsx")
	xlsx.AddBytes("xl/embeddings/book.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", inner)
	xlsx.Rel("xl/workbook.xml", relBase+"externalLink", "externalLinks/externalLink1.xml")
	xlsx.Add("xl/worksheets/sheet6.xml", ooxmltest.CTWorksheet, `<worksheet xmlns="`+ooxmltest.NSSheet+`"><sheetData>`+xlRow(1, xlStr("A1", "before the damage"))+`<row r="2"><c r="A2"><v>1`)
	out = append(out, sample{"many sheets", xlsx.Bytes()})

	docx := ooxmltest.Docx(ooxmltest.StyledPara("Heading1", "Report") + para(run(text("Body text."))) + para(styled(vanish, "Hidden text."))).WithStyles(wordStyles)
	docx.Rel("word/document.xml", ooxmltest.RelPackage, "embeddings/book.xlsx")
	docx.AddBytes("word/embeddings/book.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", inner)
	out = append(out, sample{"word with an embedded workbook", docx.Bytes()})

	out = append(out, sample{"no slides", deck().Bytes()}, sample{"no sheets", ooxmltest.Xlsx().Bytes()})
	return out
}

func childExtractor(ctx context.Context, name, mime string, r io.ReaderAt, size int64, o *gofastcore.Options) (*gofastcore.Document, error) {
	return gofastooxml.Extract(ctx, r, size, o)
}

// Reading a file a part at a time gives what Extract gives: the same
// parts, the same warnings about the document, the same embedded files,
// for any number of workers. A document put together from the parts is a
// valid one.
func TestFileGivesWhatExtractGives(t *testing.T) {
	for _, s := range samples(t) {
		for _, workers := range []int{1, 3, 64} {
			opts := &gofastcore.Options{Workers: workers, Images: gofastcore.ImagesData, Spans: true, Child: childExtractor, Name: "sample"}
			want, err := extract(t, s.data, opts)
			if err != nil {
				t.Fatalf("%s: %v", s.name, err)
			}
			ooxmltest.CheckDocument(t, want)
			f, err := gofastooxml.OpenBytes(s.data, opts)
			if err != nil {
				t.Fatalf("%s: %v", s.name, err)
			}
			name := fmt.Sprintf("%s, %d workers", s.name, workers)

			if f.Format() != want.Source.Format || f.Source() != want.Source || f.NumParts() != len(want.Parts) || f.HiddenChecked() != want.HiddenChecked {
				t.Errorf("%s: format %q, %d parts, checked %s; the document has %q, %d, %s", name, f.Format(), f.NumParts(), f.HiddenChecked(), want.Source.Format, len(want.Parts), want.HiddenChecked)
			}
			if asJSON(t, f.Metadata()) != asJSON(t, want.Metadata) {
				t.Errorf("%s: metadata differs", name)
			}

			// The whole document through the handle.
			doc, err := f.Document(context.Background())
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if asJSON(t, doc) != asJSON(t, want) {
				t.Errorf("%s: Document differs from Extract", name)
			}

			// The parts one after another, on a handle of their own so
			// that what it knows comes from this reading alone.
			f, _ = gofastooxml.OpenBytes(s.data, opts)
			built := gofastcore.New(f.Format())
			built.Source, built.Metadata, built.HiddenChecked = f.Source(), f.Metadata(), f.HiddenChecked()
			for part, err := range f.Parts(context.Background()) {
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				if part.Blocks == nil {
					t.Errorf("%s: part %d has not had MarkHidden called", name, part.Number)
				}
				built.Parts = append(built.Parts, part)
			}
			built.Warnings = f.Warnings()
			if built.Children, err = f.Children(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := built.Validate(); err != nil {
				t.Errorf("%s: the document built from Parts: %v", name, err)
			}
			if got := asJSON(t, built); got != asJSON(t, want) {
				t.Errorf("%s: the document built from Parts, Warnings and Children differs from Extract", name)
			}

			// Each part by its number, in any order. The bytes of a
			// picture come with every part that is read by itself.
			f, _ = gofastooxml.OpenBytes(s.data, &gofastcore.Options{Workers: workers, Images: gofastcore.ImagesMeta, Spans: true})
			meta, err := extract(t, s.data, &gofastcore.Options{Workers: workers, Images: gofastcore.ImagesMeta, Spans: true})
			if err != nil {
				t.Fatal(err)
			}
			for n := len(meta.Parts); n >= 1; n-- {
				part, err := f.Part(context.Background(), n)
				if err != nil {
					t.Fatalf("%s: part %d: %v", name, n, err)
				}
				if asJSON(t, part) != asJSON(t, meta.Parts[n-1]) {
					t.Errorf("%s: Part(%d) differs from the document's", name, n)
				}
			}
			if asJSON(t, f.Warnings()) != asJSON(t, meta.Warnings) {
				t.Errorf("%s: warnings after reading every part by number:\n got  %v\n want %v", name, f.Warnings(), meta.Warnings)
			}
		}
	}
}

// What a limit leaves out is the same through the iterator as through
// Extract, for any number of workers, although the iterator has handed
// parts on before the limit is met.
func TestFileLimitsAreTheSameAsExtracts(t *testing.T) {
	var slides []ooxmltest.Slide
	for i := 1; i <= 60; i++ {
		slides = append(slides, ooxmltest.Slide{Shapes: box(2, 10, 10, 300, 40, "", dpara("", plain(strings.Repeat("word ", 20*i)))) +
			tableFrame(3, 10, 100, 600, 200, `<a:tblPr/>`, trow(tc(fmt.Sprintf("slide %d", i)), tc("x")))})
	}
	pptx := deck(slides...).Bytes()
	var sheets []ooxmltest.Sheet
	for i := 1; i <= 40; i++ {
		var rows strings.Builder
		for r := 1; r <= 20; r++ {
			rows.WriteString(xlRow(r, xlNum("A"+strconv.Itoa(r), strconv.Itoa(r*i)), xlStr("B"+strconv.Itoa(r), strings.Repeat("y", 5*i))))
		}
		sheets = append(sheets, ooxmltest.Sheet{Name: "S" + strconv.Itoa(i), Data: rows.String()})
	}
	xlsx := ooxmltest.Xlsx(sheets...).Bytes()
	for name, c := range map[string]struct {
		data      []byte
		extractor gofastooxml.Extractor
		limits    gofastcore.Limits
	}{
		"slides, decompressed size": {data: pptx, limits: gofastcore.Limits{MaxDecodedBytes: 200_000}},
		"slides, table cells":       {data: pptx, extractor: gofastooxml.Extractor{MaxCells: 75}},
		"slides, number of parts":   {data: pptx, limits: gofastcore.Limits{MaxParts: 11}},
		"sheets, decompressed size": {data: xlsx, limits: gofastcore.Limits{MaxDecodedBytes: 60_000}},
		"sheets, cells":             {data: xlsx, extractor: gofastooxml.Extractor{MaxCells: 901}},
	} {
		want, err := c.extractor.Extract(context.Background(), bytes.NewReader(c.data), int64(len(c.data)), &gofastcore.Options{Workers: 1, Limits: c.limits})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cut := 0
		for _, p := range want.Parts {
			if p.Has(gofastcore.WarnLimitExceeded) || p.Has(gofastcore.WarnPartFailed) {
				cut++
			}
		}
		if (cut == 0 || cut == len(want.Parts)) && c.limits.MaxParts == 0 {
			t.Errorf("%s: %d of %d parts cut short; the test wants some", name, cut, len(want.Parts))
		}
		for _, workers := range []int{1, 2, 5, 64} {
			f, err := c.extractor.OpenBytes(c.data, &gofastcore.Options{Workers: workers, Limits: c.limits})
			if err != nil {
				t.Fatal(err)
			}
			// Twice over: the second pass must not be worse off for what
			// the first used up or left behind.
			for pass := 1; pass <= 2; pass++ {
				built := gofastcore.New(f.Format())
				built.Source, built.Metadata, built.HiddenChecked = f.Source(), f.Metadata(), f.HiddenChecked()
				for part, err := range f.Parts(context.Background()) {
					if err != nil {
						t.Fatalf("%s: %v", name, err)
					}
					built.Parts = append(built.Parts, part)
				}
				built.Warnings = f.Warnings()
				if asJSON(t, built) != asJSON(t, want) {
					t.Errorf("%s: with %d workers, pass %d, the parts from the iterator differ from Extract's with one worker", name, workers, pass)
				}
			}
		}
	}
}

func TestFileErrors(t *testing.T) {
	data := deck(ooxmltest.Slide{}, ooxmltest.Slide{}).Bytes()
	ctx := context.Background()

	// A range that selects nothing: Document says so, and the iterator
	// yields the error once.
	f, err := gofastooxml.OpenBytes(data, &gofastcore.Options{Parts: gofastcore.PartRange{First: 5, Last: 9}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Document(ctx); !errors.Is(err, gofastcore.ErrNoPartsSelected) {
		t.Errorf("Document: %v", err)
	}
	n := 0
	for part, err := range f.Parts(ctx) {
		n++
		if !errors.Is(err, gofastcore.ErrNoPartsSelected) || part.Blocks != nil {
			t.Errorf("Parts: %v %v", part, err)
		}
	}
	if n != 1 {
		t.Errorf("Parts yielded %d times", n)
	}
	// A part by number does not go by the range, but must exist.
	if part, err := f.Part(ctx, 2); err != nil || part.Number != 2 {
		t.Errorf("Part(2): %v %v", part.Number, err)
	}
	for _, number := range []int{0, 3, -1} {
		if _, err := f.Part(ctx, number); !errors.Is(err, gofastcore.ErrNoPartsSelected) {
			t.Errorf("Part(%d): %v", number, err)
		}
	}
	// Rows are a workbook's.
	for _, err := range f.Rows(ctx, 1, 10) {
		if !errors.Is(err, gofastcore.ErrUnsupported) {
			t.Errorf("Rows of a presentation: %v", err)
		}
	}
	if len(f.Sheets()) != 0 {
		t.Errorf("a presentation has sheets: %v", f.Sheets())
	}

	// What cannot be opened: the errors are those of Extract.
	for name, c := range map[string]struct {
		data []byte
		want error
	}{
		"not a zip":          {[]byte("plain text, not an archive"), gofastcore.ErrMalformed},
		"too short":          {[]byte("PK"), gofastcore.ErrMalformed},
		"a legacy .doc":      {append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, make([]byte, 600)...), gofastcore.ErrUnsupported},
		"a zip of something": {ooxmltest.NewPackage().Add("readme.txt", "text/plain", "hello").Bytes(), gofastcore.ErrUnsupported},
	} {
		if _, err := gofastooxml.OpenBytes(c.data, nil); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	if _, err := gofastooxml.OpenBytes(data, &gofastcore.Options{Limits: gofastcore.Limits{MaxInputBytes: 100}}); !errors.Is(err, gofastcore.ErrLimitExceeded) {
		t.Errorf("larger than MaxInputBytes: %v", err)
	}
	if _, err := gofastooxml.Open(filepath.Join(t.TempDir(), "absent.docx"), nil); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a file that is not there: %v", err)
	}
}

func TestOpenAFileByName(t *testing.T) {
	path := filepath.Join(ooxmltest.Root(t), "testdata", "fixtures", "layout.xlsx")
	f, err := gofastooxml.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f.Source().Name != path || f.Format() != "xlsx" {
		t.Errorf("source %+v", f.Source())
	}
	sheets := f.Sheets()
	if len(sheets) != f.NumParts() || sheets[0].Number != 1 || sheets[0].Name == "" {
		t.Errorf("sheets %+v", sheets)
	}
	doc, err := f.Document(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	// ExtractFile is the same thing in one call, and takes the name it
	// is told.
	same, err := gofastooxml.ExtractFile(context.Background(), path, nil)
	if err != nil || asJSON(t, same) != asJSON(t, doc) {
		t.Errorf("ExtractFile differs from Open and Document (%v)", err)
	}
	named, err := gofastooxml.ExtractFile(context.Background(), path, &gofastcore.Options{Name: "layout"})
	if err != nil || named.Source.Name != "layout" {
		t.Errorf("name %q (%v)", named.Source.Name, err)
	}
	// A file opened from memory has nothing to close.
	g, _ := gofastooxml.OpenBytes(deck().Bytes(), nil)
	if err := g.Close(); err != nil {
		t.Error(err)
	}
}

// goroutines waits for the number of goroutines to come back down to
// what it was, and reports it.
func goroutines(base int) int {
	n := runtime.NumGoroutine()
	for i := 0; i < 200 && n > base; i++ {
		time.Sleep(5 * time.Millisecond)
		n = runtime.NumGoroutine()
	}
	return n
}

// Leaving the loop early, or cancelling, stops the reading: no goroutine
// is left behind, and a cancelled iteration ends with the context's
// error.
func TestFileStopsWhenToldTo(t *testing.T) {
	var slides []ooxmltest.Slide
	for i := 1; i <= 400; i++ {
		slides = append(slides, ooxmltest.Slide{Shapes: box(2, 10, 10, 300, 40, "", dpara("", plain(strings.Repeat("word ", 400))))})
	}
	data := deck(slides...).Bytes()
	base := runtime.NumGoroutine()

	f, err := gofastooxml.OpenBytes(data, &gofastcore.Options{Workers: 8})
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, err := range f.Parts(context.Background()) {
		if err != nil {
			t.Fatal(err)
		}
		if seen++; seen == 3 {
			break
		}
	}
	if n := goroutines(base); n > base {
		t.Errorf("after leaving the loop early: %d goroutines, %d before", n, base)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	seen = 0
	var last error
	start := time.Now()
	for part, err := range f.Parts(ctx) {
		last = err
		if err != nil {
			if part.Blocks != nil {
				t.Errorf("a part came with the error")
			}
			continue
		}
		if seen++; seen == 40 {
			cancel()
		}
	}
	if !errors.Is(last, context.Canceled) || seen >= len(slides) {
		t.Errorf("after cancelling: %d parts, then %v", seen, last)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("cancelling took %v", time.Since(start))
	}
	if n := goroutines(base); n > base {
		t.Errorf("after cancelling: %d goroutines, %d before", n, base)
	}

	// Cancelled before it starts: Document and Part return the error,
	// the iterators yield it.
	if _, err := f.Document(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Document: %v", err)
	}
	if _, err := f.Part(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Errorf("Part: %v", err)
	}
	if _, err := gofastooxml.Extract(ctx, bytes.NewReader(data), int64(len(data)), nil); !errors.Is(err, context.Canceled) {
		t.Errorf("Extract: %v", err)
	}
	// And the handle is still good for a reading that is not cancelled.
	if part, err := f.Part(context.Background(), 400); err != nil || len(part.Blocks) != 1 {
		t.Errorf("after the cancelled readings: %v, %d blocks", err, len(part.Blocks))
	}
}

// One File read from many goroutines at once.
func TestFileIsSafeForConcurrentUse(t *testing.T) {
	for _, s := range samples(t) {
		if s.name != "many slides" && s.name != "many sheets" {
			continue
		}
		opts := &gofastcore.Options{Workers: 4, Images: gofastcore.ImagesMeta}
		want, err := extract(t, s.data, opts)
		if err != nil {
			t.Fatal(err)
		}
		f, err := gofastooxml.OpenBytes(s.data, opts)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for g := 0; g < 12; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				switch g % 3 {
				case 0:
					for n := 1; n <= f.NumParts(); n++ {
						part, err := f.Part(context.Background(), n)
						if err != nil || asJSON(t, part) != asJSON(t, want.Parts[n-1]) {
							t.Errorf("%s: Part(%d) from goroutine %d differs (%v)", s.name, n, g, err)
							return
						}
					}
				case 1:
					n := 0
					for part, err := range f.Parts(context.Background()) {
						if err != nil || asJSON(t, part) != asJSON(t, want.Parts[n]) {
							t.Errorf("%s: part %d of Parts from goroutine %d differs (%v)", s.name, n+1, g, err)
							return
						}
						n++
						f.Warnings()
					}
				default:
					doc, err := f.Document(context.Background())
					if err != nil || asJSON(t, doc) != asJSON(t, want) {
						t.Errorf("%s: Document from goroutine %d differs (%v)", s.name, g, err)
					}
				}
			}()
		}
		wg.Wait()
	}
}

// What is held while the parts are read one after another does not grow
// with their number.
func TestPartsMemoryDoesNotGrowWithTheDocument(t *testing.T) {
	if testing.Short() {
		t.Skip("builds large presentations")
	}
	build := func(n int) []byte {
		slides := make([]ooxmltest.Slide, n)
		for i := range slides {
			var paras []string
			for k := 0; k < 40; k++ {
				paras = append(paras, dpara("", plain(fmt.Sprintf("slide %d, paragraph %d: %s", i+1, k, strings.Repeat("some words of text ", 12)))))
			}
			slides[i].Shapes = ooxmltest.Shape(2, ph("title", -1), "", ooxmltest.TextBody(dpara("", plain(fmt.Sprintf("Slide %d", i+1))))) + box(3, 10, 100, 600, 400, "", paras...)
		}
		return deck(slides...).Bytes()
	}
	// The files are read from disk: a file held in memory is itself what
	// the process holds, and how much garbage may pile up before it is
	// collected goes by that.
	dir := t.TempDir()
	read := func(data []byte, workers int) (peak uint64, parts, held int) {
		path := filepath.Join(dir, "deck.pptx")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		data = nil
		f, err := gofastooxml.Open(path, &gofastcore.Options{Workers: workers})
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		peak = peakHeap(func() {
			for part, err := range f.Parts(context.Background()) {
				if err != nil {
					t.Fatal(err)
				}
				parts++
				for _, b := range part.Blocks {
					held += len(b.Text)
				}
			}
		})
		return peak, parts, held
	}
	whole := func(data []byte) uint64 {
		path := filepath.Join(dir, "whole.pptx")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return peakHeap(func() {
			doc, err := gofastooxml.ExtractFile(context.Background(), path, &gofastcore.Options{Workers: 1})
			if err != nil || len(doc.Parts) != 3000 {
				t.Fatal(err)
			}
		})
	}
	large := build(3000)
	held := whole(large)
	for _, workers := range []int{1, 4} {
		peakSmall, _, _ := read(build(300), workers)
		peakLarge, parts, text := read(large, workers)
		t.Logf("%d workers: 300 slides peak heap %.1f MB; %d slides (%d MB of text) %.1f MB; the document whole %.1f MB",
			workers, float64(peakSmall)/(1<<20), parts, text>>20, float64(peakLarge)/(1<<20), float64(held)/(1<<20))
		if parts != 3000 {
			t.Errorf("%d workers: %d parts", workers, parts)
		}
		// A part at a time takes a fraction of what the document takes,
		// and less than the text of the document alone. (How much garbage
		// is about when the peak is taken differs from run to run and
		// from system to system, so the test is of proportions.)
		if peakLarge > held/3 || peakLarge > uint64(text) {
			t.Errorf("%d workers: peak heap %d bytes a part at a time; the document whole takes %d, and its text alone is %d", workers, peakLarge, held, text)
		}
	}
}
