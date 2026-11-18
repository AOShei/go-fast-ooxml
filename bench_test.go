package gofastooxml_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// BenchmarkExtract times Extract on the large generated files, the ones the
// speed targets are set on. They are made by testdata/gen/gen.py --large
// and are not tracked; the benchmark skips a file that is absent.
//
// MB/s is of the file as stored. Excel and PowerPoint content is not
// extracted yet, so their numbers are for identifying the file and reading
// its properties only.
func BenchmarkExtract(b *testing.B) {
	for _, name := range []string{"large.docx", "large.xlsx", "large.pptx"} {
		b.Run(name, func(b *testing.B) {
			data, err := os.ReadFile(filepath.Join(ooxmltest.Root(b), "testdata", "corpus", "generated", name))
			if err != nil {
				b.Skipf("%s is not generated: %v", name, err)
			}
			opts := &gofastcore.Options{Workers: 1}
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			for b.Loop() {
				doc, err := gofastooxml.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts)
				if err != nil || len(doc.Parts) == 0 {
					b.Fatal(err)
				}
			}
		})
	}
}
