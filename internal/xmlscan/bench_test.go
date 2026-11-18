package xmlscan

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// largeParts returns the XML of one of the large generated files, joined
// part after part, so a benchmark measures tokenizing and nothing else.
// The files are made by testdata/gen/gen.py --large and are not tracked.
func largeParts(b *testing.B, name string) [][]byte {
	b.Helper()
	zr, err := zip.OpenReader(filepath.Join(ooxmltest.Root(b), "testdata", "corpus", "generated", name))
	if err != nil {
		b.Skipf("%s is not generated (run testdata/gen/gen.py --large): %v", name, err)
	}
	defer zr.Close()
	var parts [][]byte
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".xml") && !strings.HasSuffix(f.Name, ".rels") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			b.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			b.Fatal(err)
		}
		parts = append(parts, data)
	}
	return parts
}

func size(parts [][]byte) int64 {
	var n int64
	for _, p := range parts {
		n += int64(len(p))
	}
	return n
}

var benchSpaces = map[string]Space{
	"http://schemas.openxmlformats.org/wordprocessingml/2006/main":           FirstSpace,
	"http://schemas.openxmlformats.org/spreadsheetml/2006/main":              FirstSpace + 1,
	"http://schemas.openxmlformats.org/presentationml/2006/main":             FirstSpace + 2,
	"http://schemas.openxmlformats.org/drawingml/2006/main":                  FirstSpace + 3,
	"http://schemas.openxmlformats.org/officeDocument/2006/relationships":    FirstSpace + 4,
	"http://schemas.openxmlformats.org/package/2006/relationships":           FirstSpace + 5,
	"http://schemas.openxmlformats.org/markup-compatibility/2006":            FirstSpace + 6,
	"http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing": FirstSpace + 7,
}

// BenchmarkScan reads every token and touches what an extractor touches:
// the element's identity on each tag, one attribute on each start tag, and
// the text.
func BenchmarkScan(b *testing.B) {
	for _, name := range []string{"large.docx", "large.xlsx", "large.pptx"} {
		b.Run(name, func(b *testing.B) {
			parts := largeParts(b, name)
			opts := &Options{Spaces: benchSpaces}
			s := NewBytes(nil, opts)
			b.SetBytes(size(parts))
			b.ReportAllocs()
			var sink int
			for b.Loop() {
				for _, p := range parts {
					s.ResetBytes(p, opts)
					for {
						k := s.Next()
						if k == EOF {
							break
						}
						switch k {
						case Start:
							sink += int(s.Space()) + len(s.Local())
							if v, ok := s.AttrAny("r"); ok {
								sink += len(v)
							}
						case Text:
							sink += len(s.Text())
						}
					}
					if s.Err() != nil {
						b.Fatal(s.Err())
					}
				}
			}
			_ = sink
		})
	}
}

// BenchmarkScanReader is BenchmarkScan through an io.Reader and the
// refilling window, the way parts are read from a zip archive.
func BenchmarkScanReader(b *testing.B) {
	for _, name := range []string{"large.docx", "large.xlsx", "large.pptx"} {
		b.Run(name, func(b *testing.B) {
			parts := largeParts(b, name)
			opts := &Options{Spaces: benchSpaces}
			s := NewBytes(nil, opts)
			r := bytes.NewReader(nil)
			b.SetBytes(size(parts))
			b.ReportAllocs()
			for b.Loop() {
				for _, p := range parts {
					r.Reset(p)
					s.Reset(r, opts)
					for s.Next() != EOF {
					}
					if s.Err() != nil {
						b.Fatal(s.Err())
					}
				}
			}
		})
	}
}

// BenchmarkEncodingXML is the standard library's fastest mode on the same
// input: RawToken, which neither resolves namespaces nor checks nesting.
func BenchmarkEncodingXML(b *testing.B) {
	for _, name := range []string{"large.docx", "large.xlsx", "large.pptx"} {
		b.Run(name, func(b *testing.B) {
			parts := largeParts(b, name)
			b.SetBytes(size(parts))
			b.ReportAllocs()
			for b.Loop() {
				for _, p := range parts {
					dec := xml.NewDecoder(bytes.NewReader(p))
					for {
						if _, err := dec.RawToken(); err != nil {
							break
						}
					}
				}
			}
		})
	}
}
