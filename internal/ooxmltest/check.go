package ooxmltest

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// CheckDocument applies the invariants every document extracted by this
// library must satisfy, whatever the input:
//
//   - it passes gofastcore's Validate;
//   - its JSON form decodes back to an equal document;
//   - its format is one this library produces;
//   - a part without text carries exactly one of part.failed, part.no_text
//     and part.image_only, and a part with text carries neither
//     part.no_text nor part.image_only.
//
// Embedded documents are checked for the first two only, since another
// extractor may have produced them.
func CheckDocument(t testing.TB, doc *gofastcore.Document) {
	t.Helper()
	if doc == nil {
		t.Fatal("nil document")
	}
	if err := doc.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("document does not marshal: %v", err)
	}
	back := new(gofastcore.Document)
	if err := json.Unmarshal(data, back); err != nil {
		t.Fatalf("JSON does not decode: %v", err)
	}
	if !reflect.DeepEqual(back, doc) {
		again, _ := json.Marshal(back)
		t.Errorf("JSON round trip changed the document\n first: %s\nsecond: %s", clip(data), clip(again))
	}

	switch doc.Source.Format {
	case "docx", "xlsx", "pptx":
	default:
		t.Errorf("source.format %q is not one of docx, xlsx, pptx", doc.Source.Format)
	}

	for i := range doc.Parts {
		p := &doc.Parts[i]
		hasText := false
		for j := range p.Blocks {
			// A reader's marks on blocks are its own and never reach
			// the output.
			for k := range p.Blocks[j].Attrs {
				if strings.ContainsRune(k, 0) {
					t.Errorf("part %d block %d: a mark of the reader's own is left in its attributes: %q", p.Number, j, k)
				}
			}
		}
		for j := range p.Blocks {
			b := &p.Blocks[j]
			if b.Kind != gofastcore.KindImage && (b.Text != "" || b.Table != nil) {
				hasText = true
				break
			}
		}
		n := 0
		for _, code := range []string{gofastcore.WarnPartFailed, gofastcore.WarnPartNoText, gofastcore.WarnPartImageOnly} {
			if p.Has(code) {
				n++
			}
		}
		switch {
		case hasText && (p.Has(gofastcore.WarnPartNoText) || p.Has(gofastcore.WarnPartImageOnly)):
			t.Errorf("part %d has text and a no-text warning", p.Number)
		case !hasText && n != 1:
			t.Errorf("part %d has no text and %d of part.failed, part.no_text, part.image_only; want exactly one", p.Number, n)
		}
	}
}
