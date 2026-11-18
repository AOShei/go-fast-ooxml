package gofastooxml_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// FuzzOpen feeds arbitrary bytes to Extract. Whatever the input, the call
// must return a valid document or an error that wraps one of the shared
// errors; it must not panic, hang or exhaust memory.
//
// Seeds are the builder's presets and every committed fixture.
func FuzzOpen(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("PK\x03\x04"))
	f.Add(ooxmltest.Docx(ooxmltest.Para("seed")).WithCore(`<dc:title>t</dc:title>`).Bytes())
	f.Add(ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: `<row r="1">` + ooxmltest.InlineCell("A1", "seed") + `</row>`}).Bytes())
	f.Add(ooxmltest.Pptx(ooxmltest.Slide{Shapes: ooxmltest.TextShape(2, 0, 0, 100, 20, "seed")}).Bytes())
	for _, file := range ooxmltest.LoadManifest(f, "fixtures.json") {
		f.Add(file.Read(f, false))
	}
	// A small budget keeps a fuzzed decompression bomb cheap.
	opts := &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 4 << 20}}
	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := gofastooxml.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts)
		checkOutcome(t, "fuzz input", doc, err)
	})
}
