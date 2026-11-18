package ooxmltest

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// HiddenChecked is the set of hidden-text reasons each format checks
// completely, as documented in the plan. CheckDocument holds every
// extracted document to it.
var HiddenChecked = map[string]gofastcore.Hidden{
	"docx": gofastcore.HiddenInvisible | gofastcore.HiddenTiny | gofastcore.HiddenLowContrast | gofastcore.HiddenChars,
	"xlsx": gofastcore.HiddenInvisible | gofastcore.HiddenTiny | gofastcore.HiddenLowContrast | gofastcore.HiddenChars,
	"pptx": gofastcore.HiddenInvisible | gofastcore.HiddenTiny | gofastcore.HiddenLowContrast | gofastcore.HiddenOffPage |
		gofastcore.HiddenOccluded | gofastcore.HiddenTransparent | gofastcore.HiddenChars,
}

// CheckDocument applies the invariants every document extracted by this
// library must satisfy, whatever the input:
//
//   - it passes gofastcore's Validate;
//   - its JSON form decodes back to an equal document;
//   - its format is one this library produces, and HiddenChecked is the
//     documented set for that format;
//   - a part without text carries exactly one of part.failed, part.no_text
//     and part.image_only, and a part with text carries none of them.
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

	want, known := HiddenChecked[doc.Source.Format]
	if !known {
		t.Errorf("source.format %q is not one of docx, xlsx, pptx", doc.Source.Format)
	} else if doc.HiddenChecked != want {
		t.Errorf("hidden_checked is %q, want %q for %s", doc.HiddenChecked, want, doc.Source.Format)
	}

	for i := range doc.Parts {
		p := &doc.Parts[i]
		hasText := false
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
