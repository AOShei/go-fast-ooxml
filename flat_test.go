package gofastooxml_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

var atByte = regexp.MustCompile(`at byte [0-9]+`)

// flatSame reads data as the package it is and as Flat OPC, and returns
// the two documents' JSON, the packaging note taken out of the second;
// skip is set for a file whose package reading is not comparable (one
// that is refused, or read by a recovery path).
func flatSame(t *testing.T, data []byte, opts *gofastcore.Options) (want, got []byte, skip string) {
	t.Helper()
	ctx := context.Background()
	zipped, err := gofastooxml.Extract(ctx, bytes.NewReader(data), int64(len(data)), opts)
	if err != nil {
		return nil, nil, "refused: " + err.Error()
	}
	want, _ = json.Marshal(zipped)
	for _, code := range []string{gofastcore.WarnInputRepaired, gofastcore.WarnInputAmbiguous, gofastcore.WarnInputTrailingData} {
		if bytes.Contains(want, []byte(`"`+code+`"`)) {
			return nil, nil, "read by a recovery path: " + code
		}
	}
	flat, ok := ooxmltest.FlatOPC(data)
	if !ok {
		return nil, nil, "cannot be written as Flat OPC"
	}
	doc, err := gofastooxml.Extract(ctx, bytes.NewReader(flat), int64(len(flat)), opts)
	if err != nil {
		t.Errorf("as Flat OPC: %v", err)
		return nil, nil, ""
	}
	ooxmltest.CheckDocument(t, doc)
	if doc.Metadata.Extra["packaging"] != "flat_opc" {
		t.Errorf("Flat OPC not noted: %v", doc.Metadata.Extra)
	}
	delete(doc.Metadata.Extra, "packaging")
	if len(doc.Metadata.Extra) == 0 {
		doc.Metadata.Extra = nil
	}
	doc.Source.Size = zipped.Source.Size
	// A picture whose part is missing has the type an archive's table
	// gives its extension; Flat OPC states types for the parts it has.
	if zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data))); err == nil {
		have := map[string]bool{}
		for _, f := range zr.File {
			have[strings.ToLower(strings.TrimLeft(f.Name, "/"))] = true
		}
		for i := range zipped.Parts {
			for j := range zipped.Parts[i].Blocks {
				if img := zipped.Parts[i].Blocks[j].Image; img != nil && !have[strings.ToLower(img.ID)] {
					img.MIME = ""
				}
			}
		}
		want, _ = json.Marshal(zipped)
	}
	got, _ = json.Marshal(doc)
	// Where a part's XML is malformed is said in bytes of the part, which
	// in Flat OPC has no declaration.
	want, got = atByte.ReplaceAll(want, []byte("at byte N")), atByte.ReplaceAll(got, []byte("at byte N"))
	return want, got, ""
}

// TestFlatOPCReadsAsItsPackage: a Word, Excel or PowerPoint file written
// as Flat OPC gives the document its package gives, for every file of the
// fixtures and of the corpus.
func TestFlatOPCReadsAsItsPackage(t *testing.T) {
	root := ooxmltest.Root(t)
	extensions := map[string]bool{}
	for _, e := range []string{"docx", "docm", "dotx", "dotm", "xlsx", "xlsm", "xltx", "xltm", "xlsb", "pptx", "pptm", "ppsx", "ppsm", "potx", "potm"} {
		extensions["."+e] = true
	}
	var paths []string
	for _, dir := range []string{"testdata/fixtures", "testdata/corpus"} {
		if testing.Short() && dir == "testdata/corpus" {
			continue
		}
		filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return nil
			case d.IsDir() && d.Name() == ".git":
				return filepath.SkipDir
			case !d.IsDir() && extensions[strings.ToLower(filepath.Ext(path))]:
				paths = append(paths, path)
			}
			return nil
		})
	}
	sort.Strings(paths)
	var (
		mu                     sync.Mutex
		same, skipped, differs int
		next                   = make(chan string)
		wg                     sync.WaitGroup
	)
	opts := &gofastcore.Options{Workers: 1}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range next {
				data, err := os.ReadFile(path)
				if err != nil || len(data) > 64<<20 {
					continue
				}
				name, _ := filepath.Rel(root, path)
				want, got, skip := flatSame(t, data, opts)
				mu.Lock()
				switch {
				case skip != "":
					skipped++
				case bytes.Equal(want, got):
					same++
				default:
					differs++
					if differs <= 20 {
						i := 0
						for i < len(want) && i < len(got) && want[i] == got[i] {
							i++
						}
						lo := max(i-150, 0)
						t.Errorf("%s: as Flat OPC differs at byte %d:\n package: %s\n flat:    %s", name, i, want[lo:min(i+150, len(want))], got[lo:min(i+150, len(got))])
					}
				}
				mu.Unlock()
			}
		}()
	}
	for _, p := range paths {
		next <- p
	}
	close(next)
	wg.Wait()
	t.Logf("%d files: %d the same as Flat OPC, %d differ, %d not comparable", len(paths), same, differs, skipped)
}

// TestXMLFormats: Flat OPC is read through the File API as its package is,
// and XML of any other kind is refused as unsupported, saying what it is.
func TestXMLFormats(t *testing.T) {
	data := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "Data", Data: `<row r="1">` + ooxmltest.InlineCell("A1", "flat") + `</row>`}).Bytes()
	flat, ok := ooxmltest.FlatOPC(data)
	if !ok {
		t.Fatal("not written as Flat OPC")
	}
	f, err := gofastooxml.OpenBytes(flat, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f.Format() != "xlsx" || len(f.Sheets()) != 1 || f.Sheets()[0].Name != "Data" || f.Metadata().Extra["packaging"] != "flat_opc" {
		t.Errorf("format %q, sheets %v, extra %v", f.Format(), f.Sheets(), f.Metadata().Extra)
	}
	for name, xml := range map[string]string{
		"other XML":     `<?xml version="1.0"?><root xmlns="urn:x"><a/></root>`,
		"a DTD":         `<?xml version="1.0"?><!DOCTYPE r [<!ENTITY x "y">]><r>&x;</r>`,
		"no root":       `<?xml version="1.0"?>`,
		"UTF-16 other":  "\xff\xfe<\x00a\x00/\x00>\x00",
		"Word 2003 ns":  `<w:wordDocument xmlns:w="urn:not-wordml"/>`,
		"package ns":    `<pkg:package xmlns:pkg="urn:not-it"/>`,
		"white space":   "  \r\n\t<x/>",
		"processing":    `<?mso-application progid="Word.Document"?><x/>`,
		"comment first": `<!-- c --><x/>`,
	} {
		_, err := gofastooxml.Extract(context.Background(), strings.NewReader(xml), int64(len(xml)), nil)
		if !errors.Is(err, gofastcore.ErrUnsupported) || !strings.Contains(err.Error(), "XML") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// FuzzFlatOPC feeds arbitrary Flat OPC packages to Extract, from seeds
// that are the builder's presets and the fixtures written as Flat OPC.
func FuzzFlatOPC(f *testing.F) {
	add := func(data []byte) {
		if flat, ok := ooxmltest.FlatOPC(data); ok {
			f.Add(flat)
		}
	}
	add(ooxmltest.Docx(ooxmltest.Para("seed") + ooxmltest.StyledPara("Heading1", "heading")).Bytes())
	add(ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: `<row r="1">` + ooxmltest.InlineCell("A1", "seed") + `</row>`}).Bytes())
	add(ooxmltest.Pptx(ooxmltest.Slide{Shapes: ooxmltest.TextShape(2, 0, 0, 100, 20, "seed")}).Bytes())
	for _, file := range ooxmltest.LoadManifest(f, "fixtures.json") {
		add(file.Read(f, false))
	}
	opts := &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 4 << 20}}
	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := gofastooxml.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts)
		checkOutcome(t, "fuzz input", doc, err)
	})
}

// TestFlatOPCWorstCase: what the index pass of a Flat OPC package costs
// grows with the file and no faster, for the inputs that could make it
// grow faster: many parts, many namespaces declared around many parts,
// deep nesting inside a part, and long base64.
func TestFlatOPCWorstCase(t *testing.T) {
	if testing.Short() {
		t.Skip("times large inputs")
	}
	flat, _ := ooxmltest.FlatOPC(ooxmltest.Docx(ooxmltest.Para("worst case")).Bytes())
	base := string(flat)
	with := func(open, extra string) []byte {
		s := strings.Replace(base, `<pkg:package xmlns:pkg="http://schemas.microsoft.com/office/2006/xmlPackage">`,
			`<pkg:package xmlns:pkg="http://schemas.microsoft.com/office/2006/xmlPackage"`+open+`>`, 1)
		return []byte(strings.Replace(s, "</pkg:package>", extra+"</pkg:package>", 1))
	}
	read := func(data []byte) func() {
		return func() {
			gofastooxml.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), nil)
		}
	}
	parts := func(n int, declare bool) string {
		var b strings.Builder
		for i := range n {
			decl := ""
			if declare {
				decl = ` xmlns:q` + strconv.Itoa(i) + `="urn:q"`
			}
			b.WriteString(`<pkg:part pkg:name="/extra/` + strconv.Itoa(i) + `.xml" pkg:contentType="application/xml"` + decl + `><pkg:xmlData><a/></pkg:xmlData></pkg:part>`)
		}
		return b.String()
	}
	declarations := func(n int) string {
		var b strings.Builder
		for i := range n {
			b.WriteString(` xmlns:p` + strconv.Itoa(i) + `="urn:p` + strconv.Itoa(i) + `"`)
		}
		return b.String()
	}
	costsAtMost(t, 8, "many parts", read(with("", parts(10_000, false))), read(with("", parts(40_000, false))))
	costsAtMost(t, 8, "many namespaces around many parts",
		read(with(declarations(600), parts(10_000, true))), read(with(declarations(2_400), parts(40_000, true))))
	deep := func(n int) string {
		return `<pkg:part pkg:name="/extra/deep.xml" pkg:contentType="application/xml"><pkg:xmlData>` +
			strings.Repeat("<a>", n) + strings.Repeat("</a>", n) + `</pkg:xmlData></pkg:part>`
	}
	costsAtMost(t, 8, "deep nesting in a part", read(with("", deep(250_000))), read(with("", deep(1_000_000))))
	b64 := func(n int) string {
		return `<pkg:part pkg:name="/extra/b.bin" pkg:contentType="application/octet-stream"><pkg:binaryData>` +
			strings.Repeat("QUJD \r\n", n) + `</pkg:binaryData></pkg:part>`
	}
	costsAtMost(t, 8, "long base64", read(with("", b64(1_000_000))), read(with("", b64(4_000_000))))
}
