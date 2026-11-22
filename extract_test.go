package gofastooxml_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

func extract(t testing.TB, data []byte, opts *gofastcore.Options) (*gofastcore.Document, error) {
	t.Helper()
	return gofastooxml.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts)
}

// within fails the test if fn takes longer than the limit: a hostile file
// must be refused quickly, not eventually.
func within(t testing.TB, limit time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(limit):
		t.Fatalf("%s did not finish within %v", what, limit)
	}
}

func warned(doc *gofastcore.Document, code string) bool {
	for _, w := range doc.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}

func TestExtractIdentifiesTheFormat(t *testing.T) {
	cases := map[string]struct {
		pkg    *ooxmltest.Package
		format string
		mime   string
		kind   gofastcore.PartKind
	}{
		"word":       {ooxmltest.Docx(ooxmltest.Para("x")), "docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", gofastcore.PartBody},
		"excel":      {ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S"}), "xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", gofastcore.PartSheet},
		"powerpoint": {ooxmltest.Pptx(ooxmltest.Slide{}), "pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation", gofastcore.PartSlide},
		"macro-enabled excel": {ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S"}).Add("xl/workbook.xml", "application/vnd.ms-excel.sheet.macroEnabled.main+xml", "<workbook/>"),
			"xlsx", "application/vnd.ms-excel.sheet.macroEnabled.12", gofastcore.PartSheet},
	}
	for name, c := range cases {
		data := c.pkg.Bytes()
		doc, err := extract(t, data, &gofastcore.Options{Name: "report.bin"})
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if doc.Source.Format != c.format || doc.Source.MIME != c.mime || doc.Source.Name != "report.bin" || doc.Source.Size != int64(len(data)) {
			t.Errorf("%s: source = %+v", name, doc.Source)
		}
		if len(doc.Parts) != 1 || doc.Parts[0].Kind != c.kind {
			t.Errorf("%s: parts = %+v", name, doc.Parts)
		}
		if err := doc.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		// The content of all three formats is read: an empty document is
		// an empty file, not one that was not read. (The macro-enabled
		// workbook here is a stub that lists no sheets, and says so.)
		if name != "macro-enabled excel" && (doc.Parts[0].Has(gofastcore.WarnPartFailed) || len(doc.Warnings) > 0) {
			t.Errorf("%s: part warnings %v, document warnings %v", name, doc.Parts[0].Warnings, doc.Warnings)
		}
	}
}

func TestExtractProperties(t *testing.T) {
	pkg := ooxmltest.Docx(ooxmltest.Para("x")).
		WithCore(`<dc:title>  Annual Report  </dc:title><dc:creator>Ada Lovelace; Charles Babbage</dc:creator>` +
			`<dc:subject>Engines</dc:subject><cp:keywords>analysis, difference, engine</cp:keywords><dc:language>en-GB</dc:language>` +
			`<dcterms:created xsi:type="dcterms:W3CDTF">2026-03-04T09:30:00Z</dcterms:created>` +
			`<dcterms:modified xsi:type="dcterms:W3CDTF">2026-03-05T16:45:10+01:00</dcterms:modified>` +
			`<cp:lastModifiedBy>Mary Somerville</cp:lastModifiedBy><cp:revision>7</cp:revision><dc:description>Line one&#13;&#10;line two</dc:description>` +
			`<cp:category></cp:category>`).
		WithApp(`<Application>Microsoft Office Word</Application><AppVersion>16.0000</AppVersion><Company>Analytical Ltd</Company><Pages>3</Pages>`).
		WithCustom(`<property fmtid="{D5CDD505-2E9C-101B-9397-08002B2CF9AE}" pid="2" name="Project Code"><vt:lpwstr>AE-1843</vt:lpwstr></property>` +
			`<property fmtid="{D5CDD505-2E9C-101B-9397-08002B2CF9AE}" pid="3" name="Reviewed"><vt:bool>true</vt:bool></property>`)
	doc, err := extract(t, pkg.Bytes(), nil)
	if err != nil {
		t.Fatal(err)
	}
	m := doc.Metadata
	if m.Title != "Annual Report" || m.Subject != "Engines" || m.Language != "en-GB" || m.Application != "Microsoft Office Word" {
		t.Errorf("metadata = %+v", m)
	}
	// One entry each, as written: the fields are free text.
	if len(m.Authors) != 1 || m.Authors[0] != "Ada Lovelace; Charles Babbage" || len(m.Keywords) != 1 || m.Keywords[0] != "analysis, difference, engine" {
		t.Errorf("authors %q, keywords %q", m.Authors, m.Keywords)
	}
	if !m.Created.Equal(time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC)) || !m.Modified.Equal(time.Date(2026, 3, 5, 15, 45, 10, 0, time.UTC)) {
		t.Errorf("created %v, modified %v", m.Created, m.Modified)
	}
	want := map[string]string{
		"last_modified_by": "Mary Somerville", "revision": "7", "description": "Line one\nline two",
		"app_version": "16.0000", "company": "Analytical Ltd",
		"custom.project code": "AE-1843", "custom.reviewed": "true",
	}
	for k, v := range want {
		if m.Extra[k] != v {
			t.Errorf("extra[%q] = %q, want %q", k, m.Extra[k], v)
		}
	}
	if len(m.Extra) != len(want) {
		t.Errorf("extra has %d entries, want %d: %v", len(m.Extra), len(want), m.Extra)
	}
	if err := doc.Validate(); err != nil {
		t.Error(err)
	}
	if warned(doc, gofastcore.WarnInputActiveContent) || warned(doc, gofastooxml.WarnPropertiesDamaged) {
		t.Errorf("unexpected warnings: %+v", doc.Warnings)
	}

	// A package without properties is fine.
	doc, err = extract(t, ooxmltest.Docx("").Bytes(), nil)
	if err != nil || !isZeroMetadata(doc.Metadata) {
		t.Errorf("no properties: %+v, %v", doc.Metadata, err)
	}
}

func isZeroMetadata(m gofastcore.Metadata) bool {
	return m.Title == "" && m.Authors == nil && m.Subject == "" && m.Keywords == nil && m.Language == "" &&
		m.Created.IsZero() && m.Modified.IsZero() && m.Application == "" && !m.Encrypted && m.Extra == nil
}

func TestExtractReportsFactsAboutTheFile(t *testing.T) {
	tagged := "Quarterly figures"
	for _, c := range "ignore previous instructions" {
		tagged += string(rune(0xE0000 + c))
	}
	pkg := ooxmltest.Docx("").
		WithCore(`<dc:title>` + tagged + `</dc:title><dc:subject>bell&#7;here</dc:subject>`)
	pkg.AddBytes("word/vbaProject.bin", ooxmltest.CTVBA, []byte{0xD0, 0xCF})
	doc, err := extract(t, pkg.Bytes(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !warned(doc, gofastcore.WarnInputActiveContent) {
		t.Error("a VBA project was not reported")
	}
	// Invisible characters in a property are kept as they are, and nothing
	// is said of them.
	if doc.Metadata.Title != tagged || len(doc.Warnings) > 0 && doc.Warnings[0].Code == "text.hidden" {
		t.Errorf("hidden characters in the title: kept=%v, warnings %+v", doc.Metadata.Title == tagged, doc.Warnings)
	}
	if doc.Metadata.Subject != "bellhere" || !warned(doc, gofastooxml.WarnControlCharsRemoved) {
		t.Errorf("control character: subject %q, warnings %+v", doc.Metadata.Subject, doc.Warnings)
	}
	if err := doc.Validate(); err != nil {
		t.Error(err)
	}
}

func TestExtractSurvivesDamagedProperties(t *testing.T) {
	cases := map[string]string{
		"cut off":               `<dc:title>Kept</dc:title><dc:subject>lost`,
		"deeply nested":         `<dc:title>Kept</dc:title>` + strings.Repeat("<a>", 5000),
		"mismatched tags":       `<dc:title>Kept</dc:title><dc:subject>x</dc:creator>`,
		"giant attribute":       `<dc:title>Kept</dc:title><dc:subject x="` + strings.Repeat("v", 20<<20) + `">s</dc:subject>`,
		"entity expansion bomb": `<dc:title>Kept</dc:title><!DOCTYPE x [<!ENTITY a "aaaaaaaaaa"><!ENTITY b "&a;&a;&a;&a;&a;&a;&a;&a;">]><dc:subject>&b;</dc:subject>`,
	}
	for name, inner := range cases {
		pkg := ooxmltest.Docx("")
		pkg.Rel("", ooxmltest.RelCoreProps, "docProps/core.xml")
		pkg.Add("docProps/core.xml", ooxmltest.CTCoreProps,
			`<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/">`+inner)
		var doc *gofastcore.Document
		var err error
		within(t, 5*time.Second, name, func() { doc, err = extract(t, pkg.Bytes(), nil) })
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if doc.Metadata.Title != "Kept" || !warned(doc, gofastooxml.WarnPropertiesDamaged) {
			t.Errorf("%s: title %q, warnings %+v", name, doc.Metadata.Title, doc.Warnings)
		}
		if doc.Metadata.Subject != "" && name == "entity expansion bomb" {
			t.Errorf("%s: an entity was expanded: %q", name, doc.Metadata.Subject)
		}
		if err := doc.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	// Values and counts are bounded.
	var many strings.Builder
	for i := range 300 {
		many.WriteString(`<property fmtid="x" pid="2" name="p` + string(rune('a'+i%26)) + strings.Repeat("x", i) + `"><vt:lpwstr>v</vt:lpwstr></property>`)
	}
	doc, err := extract(t, ooxmltest.Docx("").WithCore(`<dc:title>`+strings.Repeat("é", 100_000)+`</dc:title>`).WithCustom(many.String()).Bytes(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(doc.Metadata.Title); n > 64<<10 || n < 60<<10 || !warned(doc, gofastcore.WarnLimitExceeded) || warned(doc, gofastooxml.WarnPropertyTruncated) {
		t.Errorf("long title: %d bytes, warnings %+v", n, doc.Warnings)
	}
	if len(doc.Metadata.Extra) != 256 {
		t.Errorf("%d custom properties kept, want 256", len(doc.Metadata.Extra))
	}
	if err := doc.Validate(); err != nil {
		t.Error(err)
	}
}

func TestExtractErrors(t *testing.T) {
	zipOf := func(names ...string) []byte {
		var buf bytes.Buffer
		w := zip.NewWriter(&buf)
		for _, n := range names {
			f, _ := w.Create(n)
			if n == "mimetype" {
				io.WriteString(f, "application/vnd.oasis.opendocument.text")
				continue
			}
			io.WriteString(f, "x")
		}
		w.Close()
		return buf.Bytes()
	}
	visio := ooxmltest.NewPackage()
	visio.Rel("", ooxmltest.RelOfficeDoc, "visio/document.xml")
	visio.Add("visio/document.xml", "application/vnd.ms-visio.drawing.main+xml", "<x/>")

	cases := []struct {
		name string
		data []byte
		want error
		says string
	}{
		{"empty", nil, gofastcore.ErrMalformed, ""},
		{"three bytes", []byte("PK\x03"), gofastcore.ErrMalformed, ""},
		{"text file", []byte("just some text, not an archive at all"), gofastcore.ErrMalformed, "not a zip"},
		{"pdf", []byte("%PDF-1.7\n%\xE2\xE3\xCF\xD3\n1 0 obj\n<<>>\nendobj\n"), gofastcore.ErrMalformed, "not a zip"},
		{"plain zip", zipOf("readme.txt"), gofastcore.ErrUnsupported, "not an Office Open XML package"},
		{"opendocument", zipOf("mimetype", "content.xml", "META-INF/manifest.xml"), gofastcore.ErrUnsupported, "not an Office Open XML package: it is application/vnd.oasis.opendocument.text"},
		{"cut off before the directory", ooxmltest.Docx(ooxmltest.Para("x")).Bytes()[:400], gofastcore.ErrMalformed, "the file is cut short or damaged"},
		{"other office package", visio.Bytes(), gofastcore.ErrUnsupported, "no Word, Excel or PowerPoint"},
		{"legacy binary", ooxmltest.Compound(map[string][]byte{"WordDocument": make([]byte, 5000), "1Table": make([]byte, 100)}), gofastcore.ErrUnsupported, "legacy binary"},
		{"password protected", ooxmltest.Encrypt(ooxmltest.Docx("").Bytes(), "a password", ooxmltest.Encryption{}), gofastcore.ErrPasswordRequired, "encrypted"},
		{"rights managed, binary", ooxmltest.Compound(map[string][]byte{"\x06DataSpaces/DataSpaceMap": make([]byte, 100), "\x09DRMContent": make([]byte, 5000)}), gofastcore.ErrUnsupported, "rights management"},
		// As Office writes a rights-managed .docx: the package encrypted,
		// the licence in the data spaces, no encryption information.
		{"rights managed", ooxmltest.Compound(map[string][]byte{
			"\x06DataSpaces/DataSpaceMap":                                        make([]byte, 100),
			"\x06DataSpaces/DataSpaceInfo/\x09DRMEncryptedDataSpace":             make([]byte, 64),
			"\x06DataSpaces/TransformInfo/\x09DRMEncryptedTransform/\x06Primary": make([]byte, 2000),
			"EncryptedPackage": make([]byte, 5000),
		}), gofastcore.ErrUnsupported, "rights-management server issues to the people the document names, and this library contacts no server"},
		// Encrypted, and neither a password nor rights management is said.
		{"encrypted, how not said", ooxmltest.Compound(map[string][]byte{"EncryptedPackage": make([]byte, 5000)}), gofastcore.ErrUnsupported, "whose encryption is not stated"},
		{"compound header only", ooxmltest.Compound(map[string][]byte{"WordDocument": make([]byte, 5000)})[:40], gofastcore.ErrMalformed, "compound file"},
		{"compound file cut short", ooxmltest.Compound(map[string][]byte{"WordDocument": make([]byte, 5000)})[:700], gofastcore.ErrMalformed, "compound file"},
	}
	for _, c := range cases {
		doc, err := extract(t, c.data, nil)
		if doc != nil || !errors.Is(err, c.want) || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: doc %v, err %v; want %v mentioning %q", c.name, doc != nil, err, c.want, c.says)
		}
	}

	// The input size limit is checked before anything is read.
	data := ooxmltest.Docx("").Bytes()
	_, err := extract(t, data, &gofastcore.Options{Limits: gofastcore.Limits{MaxInputBytes: int64(len(data)) - 1}})
	if !errors.Is(err, gofastcore.ErrLimitExceeded) {
		t.Errorf("input over the limit: %v", err)
	}
	if _, err := extract(t, data, &gofastcore.Options{Limits: gofastcore.Limits{MaxInputBytes: int64(len(data))}}); err != nil {
		t.Errorf("input at the limit: %v", err)
	}
	if _, err := extract(t, data, &gofastcore.Options{Limits: gofastcore.Limits{MaxInputBytes: -1, MaxDecodedBytes: -1}}); err != nil {
		t.Errorf("no limits: %v", err)
	}

	// A cancelled context stops the call.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gofastooxml.Extract(ctx, bytes.NewReader(data), int64(len(data)), nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled context: %v", err)
	}
}

func TestExtractFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memo.docx")
	if err := os.WriteFile(path, ooxmltest.Docx("").WithCore(`<dc:title>Memo</dc:title>`).Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := gofastooxml.ExtractFile(context.Background(), path, nil)
	if err != nil || doc.Source.Name != path || doc.Metadata.Title != "Memo" {
		t.Errorf("doc %+v, err %v", doc, err)
	}
	doc, err = gofastooxml.ExtractFile(context.Background(), path, &gofastcore.Options{Name: "given"})
	if err != nil || doc.Source.Name != "given" {
		t.Errorf("explicit name: %+v, %v", doc, err)
	}
	if _, err := gofastooxml.ExtractFile(context.Background(), filepath.Join(t.TempDir(), "absent.docx"), nil); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file: %v", err)
	}
}

// A decompression bomb is a small file that expands enormously. Every byte
// that comes out of the archive is charged to MaxDecodedBytes, so the bomb
// is cut off at the limit however it is built.
func TestDecompressionBombs(t *testing.T) {
	const expanded = 64 << 20
	filler := bytes.Repeat([]byte(" "), expanded)
	limit := &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 8 << 20}}

	// In the properties: the document survives and reports the limit.
	bomb := ooxmltest.Docx("")
	bomb.Rel("", ooxmltest.RelCoreProps, "docProps/core.xml")
	open := []byte(`<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>T</dc:title><dc:subject>`)
	bomb.AddBytes("docProps/core.xml", ooxmltest.CTCoreProps, append(open, filler...))
	data := bomb.Bytes()
	if len(data) > 1<<20 {
		t.Fatalf("the bomb is %d bytes; it should be small", len(data))
	}
	var doc *gofastcore.Document
	var err error
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	within(t, 10*time.Second, "properties bomb", func() { doc, err = extract(t, data, limit) })
	runtime.ReadMemStats(&after)
	if err != nil || doc.Metadata.Title != "T" || !warned(doc, gofastcore.WarnLimitExceeded) {
		t.Errorf("properties bomb: doc %+v, err %v", doc, err)
	} else if err := doc.Validate(); err != nil {
		t.Error(err)
	}
	if grew := int64(after.TotalAlloc - before.TotalAlloc); grew > 64<<20 {
		t.Errorf("reading a bomb under an 8 MiB limit allocated %d MiB", grew>>20)
	}

	// In the content-types table: nothing can be identified, so it is an error.
	bomb = ooxmltest.Docx("")
	bomb.AddBytes("[Content_Types].xml", "", append([]byte(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`), filler...))
	data = bomb.Bytes()
	within(t, 10*time.Second, "content types bomb", func() { doc, err = extract(t, data, limit) })
	if doc != nil || !errors.Is(err, gofastcore.ErrLimitExceeded) {
		t.Errorf("content types bomb: doc %v, err %v", doc != nil, err)
	}

	// With the default limit the same file is simply read: 64 MiB is allowed.
	within(t, 60*time.Second, "bomb under the default limit", func() { doc, err = extract(t, bomb.Bytes(), nil) })
	if err == nil {
		t.Error("an unterminated content-types table was accepted")
	}

	// Too many members.
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for i := range 200 {
		w.Create("p" + string(rune('a'+i%26)) + strings.Repeat("x", i))
	}
	w.Close()
	_, err = (&gofastooxml.Extractor{MaxArchiveMembers: 100}).Extract(context.Background(), bytes.NewReader(buf.Bytes()), int64(buf.Len()), nil)
	if !errors.Is(err, gofastcore.ErrLimitExceeded) {
		t.Errorf("200 members with a limit of 100: %v", err)
	}
}

// Whatever is done to a file, Extract returns a valid document or an error
// that wraps one of the shared errors. It never panics and never hangs.
func checkOutcome(t testing.TB, what string, doc *gofastcore.Document, err error) {
	t.Helper()
	switch {
	case err != nil:
		if doc != nil {
			t.Errorf("%s: both a document and an error (%v)", what, err)
		}
		if !errors.Is(err, gofastcore.ErrMalformed) && !errors.Is(err, gofastcore.ErrUnsupported) && !errors.Is(err, gofastcore.ErrLimitExceeded) &&
			!errors.Is(err, gofastcore.ErrPasswordRequired) && !errors.Is(err, gofastcore.ErrWrongPassword) {
			t.Errorf("%s: error does not wrap a shared error: %v", what, err)
		}
	case doc == nil:
		t.Errorf("%s: neither a document nor an error", what)
	default:
		if verr := doc.Validate(); verr != nil {
			t.Errorf("%s: invalid document: %v", what, verr)
		}
	}
}

func TestTruncatedAndCorruptedArchives(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, f := range ooxmltest.LoadManifest(t, "fixtures.json") {
		data := f.Read(t, false)
		// Cut the file short at many points, including inside the archive's
		// directory at the end.
		cuts := []int{0, 1, 3, 4, 30, len(data) / 2, len(data) - 100, len(data) - 22, len(data) - 1}
		for range 20 {
			cuts = append(cuts, rng.Intn(len(data)))
		}
		for _, cut := range cuts {
			cut = max(cut, 0)
			var doc *gofastcore.Document
			var err error
			within(t, 5*time.Second, f.Name, func() { doc, err = extract(t, data[:cut], nil) })
			checkOutcome(t, f.Name+" cut", doc, err)
		}
		// Damage bytes in place.
		for range 60 {
			damaged := bytes.Clone(data)
			for range 1 + rng.Intn(8) {
				damaged[rng.Intn(len(damaged))] ^= byte(1 << rng.Intn(8))
			}
			var doc *gofastcore.Document
			var err error
			within(t, 5*time.Second, f.Name, func() { doc, err = extract(t, damaged, nil) })
			checkOutcome(t, f.Name+" damaged", doc, err)
		}
	}
}

func TestFixturesExtract(t *testing.T) {
	for _, f := range ooxmltest.LoadManifest(t, "fixtures.json") {
		doc, err := extract(t, f.Read(t, false), nil)
		if err != nil {
			t.Errorf("%s: %v", f.Name, err)
			continue
		}
		if doc.Source.Format != f.Format {
			t.Errorf("%s: format %q, want %q", f.Name, doc.Source.Format, f.Format)
		}
		if err := doc.Validate(); err != nil {
			t.Errorf("%s: %v", f.Name, err)
		}
		if doc.Metadata.Application == "" && doc.Metadata.Authors == nil {
			t.Errorf("%s: no application and no author read from the properties", f.Name)
		}
	}
}

// TestCorpus runs every downloaded corpus file through Extract. A file the
// manifest records as sound must be identified as the format it is; a file
// recorded as damaged, encrypted or hostile must be refused with a shared
// error or read into a valid document. Nothing may panic or take long.
func TestCorpus(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the whole corpus")
	}
	files := ooxmltest.LoadManifest(t, "corpus.json")
	type outcome struct {
		f   ooxmltest.File
		doc *gofastcore.Document
		err error
		dur time.Duration
	}
	var (
		wg      sync.WaitGroup
		work    = make(chan ooxmltest.File)
		results = make(chan outcome, len(files))
	)
	for range runtime.GOMAXPROCS(0) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range work {
				data, err := os.ReadFile(f.AbsPath(t))
				if err != nil {
					continue // not downloaded
				}
				start := time.Now()
				doc, err := extract(t, data, nil)
				results <- outcome{f, doc, err, time.Since(start)}
			}
		}()
	}
	for _, f := range files {
		work <- f
	}
	close(work)
	wg.Wait()
	close(results)

	counts := map[string]int{}
	var slowest outcome
	n := 0
	for r := range results {
		n++
		checkOutcome(t, r.f.Name, r.doc, r.err)
		if r.dur > slowest.dur {
			slowest = r
		}
		if r.dur > 10*time.Second {
			t.Errorf("%s took %v", r.f.Name, r.dur)
		}
		switch {
		case r.err == nil && r.f.Note == "":
			counts["sound, read"]++
			if r.doc.Source.Format != r.f.Format {
				t.Errorf("%s: read as %q, manifest says %q", r.f.Name, r.doc.Source.Format, r.f.Format)
			}
		case r.err == nil:
			counts["unusual, read"]++
		case r.f.Note == "":
			counts["sound, refused"]++
			t.Errorf("%s: a sound package was refused: %v", r.f.Name, r.err)
		case errors.Is(r.err, gofastcore.ErrUnsupported):
			counts["unusual, refused as unsupported"]++
		case errors.Is(r.err, gofastcore.ErrLimitExceeded):
			counts["unusual, refused at a limit"]++
		default:
			counts["unusual, refused as malformed"]++
		}
	}
	if n == 0 {
		t.Skip("no corpus files downloaded; run python3 testdata/fetch.py")
	}
	t.Logf("%d files: %v; slowest %s (%v)", n, counts, slowest.f.Name, slowest.dur.Round(time.Millisecond))
}

// pageCount returns the number of the document's parts apart from its
// stored part, which comes last: its pages, sheets or slides.
func pageCount(doc *gofastcore.Document) int {
	if n := len(doc.Parts); n > 0 && doc.Parts[n-1].Kind == gofastcore.PartStored {
		return n - 1
	}
	return len(doc.Parts)
}
