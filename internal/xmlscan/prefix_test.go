package xmlscan

import (
	"fmt"
	"strings"
	"testing"
)

// The namespace of the last prefix resolved is kept only while the
// bindings in scope are unchanged: a prefix bound again in an element, and
// back to its outer binding when the element ends, resolves to each in
// turn, for elements and attributes, with few bindings in scope and with
// more than are searched in order.
func TestPrefixBoundAgain(t *testing.T) {
	for _, extra := range []int{0, 12} {
		var decl strings.Builder
		for i := range extra {
			fmt.Fprintf(&decl, ` xmlns:p%d="urn:extra%d"`, i, i)
		}
		doc := `<a:r xmlns:a="urn:one"` + decl.String() + ` a:v="1">` +
			`<a:x a:v="2" xmlns:a="urn:two"><a:y a:v="3"/></a:x>` +
			`<a:z a:v="4"/><b:w xmlns:b="urn:one" b:v="5"/><a:q a:v="6"/></a:r>`
		spaces := map[string]Space{"urn:one": 1, "urn:two": 2}
		want := map[string]Space{"r": 1, "x": 2, "y": 2, "z": 1, "w": 1, "q": 1}
		vals := map[string]string{"r": "1", "x": "2", "y": "3", "z": "4", "w": "5", "q": "6"}
		s := NewBytes([]byte(doc), &Options{Spaces: spaces})
		seen := 0
		for k := s.Next(); k != EOF; k = s.Next() {
			if k != Start {
				continue
			}
			l := string(s.Local())
			seen++
			if s.Space() != want[l] {
				t.Errorf("%d bindings more: element %s in namespace %d, want %d", extra, l, s.Space(), want[l])
			}
			if v, ok := s.Attr(want[l], "v"); !ok || string(v) != vals[l] {
				t.Errorf("%d bindings more: attribute v of %s in namespace %d: %q, %v", extra, l, want[l], v, ok)
			}
			other := 3 - want[l]
			if _, ok := s.Attr(other, "v"); ok {
				t.Errorf("%d bindings more: attribute v of %s found in namespace %d", extra, l, other)
			}
		}
		if s.Err() != nil || seen != len(want) {
			t.Fatalf("%d bindings more: %d elements, %v", extra, seen, s.Err())
		}
	}
}
