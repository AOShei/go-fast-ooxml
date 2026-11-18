package gofastooxml_test

import (
	"testing"

	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// FuzzOpen is the entry-point fuzz target. In Phase 0 the library has no
// reader, so it drives the harness's Sniff, which must never panic or run
// away on any input. Phase 1 points it at the package reader and adds the
// targets for the XML tokenizer, number formats and numbering.
//
// Seeds are the builder's presets and every committed fixture.
func FuzzOpen(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("PK\x03\x04"))
	f.Add(ooxmltest.Docx(ooxmltest.Para("seed")).Bytes())
	f.Add(ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: `<row r="1">` + ooxmltest.InlineCell("A1", "seed") + `</row>`}).Bytes())
	f.Add(ooxmltest.Pptx(ooxmltest.Slide{Shapes: ooxmltest.TextShape(2, 0, 0, 100, 20, "seed")}).Bytes())
	for _, file := range ooxmltest.LoadManifest(f, "fixtures.json") {
		f.Add(file.Read(f, false))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		format, err := ooxmltest.Sniff(data)
		if (err == nil) != (format != "") {
			t.Fatalf("Sniff returned format %q with error %v", format, err)
		}
	})
}
