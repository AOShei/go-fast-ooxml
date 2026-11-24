package xmlscan

import "testing"

// Declarations given in Options.Bindings are in scope from the start,
// hidden by the input's own, and never closed by an end tag.
func TestBindings(t *testing.T) {
	spaces := map[string]Space{"urn:a": FirstSpace, "urn:b": FirstSpace + 1}
	s := NewBytes([]byte(`<a:x><y xmlns:a="urn:b"><a:z/></y><a:w/><v/></a:x>`), &Options{
		Spaces:   spaces,
		Bindings: []Binding{{Prefix: "a", URI: "urn:a"}, {Prefix: "", URI: "urn:b"}},
	})
	want := []struct {
		local string
		space Space
	}{{"x", FirstSpace}, {"y", FirstSpace + 1}, {"z", FirstSpace + 1}, {"w", FirstSpace}, {"v", FirstSpace + 1}}
	i := 0
	for s.Next() != EOF {
		if s.Kind() != Start {
			continue
		}
		if i >= len(want) || string(s.Local()) != want[i].local || s.Space() != want[i].space {
			t.Errorf("element %d: %s in %d", i, s.Local(), s.Space())
		}
		i++
	}
	if s.Err() != nil || i != len(want) {
		t.Errorf("%d elements, %v", i, s.Err())
	}
	// A scanner reset without them has none.
	s.ResetBytes([]byte(`<a:x/>`), &Options{Spaces: spaces})
	if s.Next(); s.Space() != SpaceUnknown {
		t.Errorf("after a reset: %d", s.Space())
	}
}
