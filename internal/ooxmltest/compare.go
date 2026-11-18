package ooxmltest

import (
	"fmt"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// CompareOptions selects what CompareBlocks holds a document to.
type CompareOptions struct {
	// Hidden compares the hidden-text flags of blocks, spans and parts.
	Hidden bool
	// Furniture compares running headers and footers; without it they are
	// left out on both sides.
	Furniture bool
}

// CompareBlocks checks an extracted document against the known content of
// a fixture: the same parts, and in each the same blocks in the same
// order, with the same kind, level, text and table. A list label is
// compared only where the expected content gives one.
func CompareBlocks(t testing.TB, got, want *gofastcore.Document, opts CompareOptions) {
	t.Helper()
	if len(got.Parts) != len(want.Parts) {
		t.Errorf("document has %d parts, want %d", len(got.Parts), len(want.Parts))
		return
	}
	for i := range want.Parts {
		g, w := &got.Parts[i], &want.Parts[i]
		if g.Kind != w.Kind || g.Number != w.Number || g.Name != w.Name {
			t.Errorf("part %d is %s %d %q, want %s %d %q", i, g.Kind, g.Number, g.Name, w.Kind, w.Number, w.Name)
		}
		if opts.Hidden && g.Hidden != w.Hidden {
			t.Errorf("part %d: hidden %q, want %q", w.Number, g.Hidden, w.Hidden)
		}
		gl, wl := describeAll(g.Blocks, w.Blocks, opts, false), describeAll(w.Blocks, w.Blocks, opts, true)
		for j := 0; j < len(gl) || j < len(wl); j++ {
			var a, b string
			if j < len(gl) {
				a = gl[j]
			}
			if j < len(wl) {
				b = wl[j]
			}
			if a != b {
				t.Errorf("part %d, block %d:\n got  %s\n want %s\n(got %d blocks, want %d)", w.Number, j, orNone(a), orNone(b), len(gl), len(wl))
				return
			}
		}
	}
}

func orNone(s string) string {
	if s == "" {
		return "(no block)"
	}
	return s
}

// describeAll writes each block on one line. reference is the expected
// content: where it leaves a label out, the label is left out of both.
func describeAll(blocks, reference []gofastcore.Block, opts CompareOptions, isReference bool) []string {
	var ref []gofastcore.Block
	for _, b := range reference {
		if opts.Furniture || !b.Kind.Furniture() {
			ref = append(ref, b)
		}
	}
	var out []string
	n := 0
	for i := range blocks {
		b := &blocks[i]
		if !opts.Furniture && b.Kind.Furniture() {
			continue
		}
		withMarker := n < len(ref) && ref[n].Marker != ""
		n++
		var sb strings.Builder
		sb.WriteString(b.Kind.String())
		if b.Level > 0 {
			fmt.Fprintf(&sb, "[%d]", b.Level)
		}
		if b.Ordered {
			sb.WriteString(" ordered")
		}
		if withMarker {
			fmt.Fprintf(&sb, " marker=%q", b.Marker)
		}
		if opts.Hidden && b.Hidden != 0 {
			fmt.Fprintf(&sb, " hidden=%s", b.Hidden)
			for _, s := range b.Spans {
				if s.Hidden != 0 {
					fmt.Fprintf(&sb, " [%d:%d %s]", s.Start, s.End, s.Hidden)
				}
			}
		}
		if b.Text != "" {
			fmt.Fprintf(&sb, " %q", b.Text)
		}
		if t := b.Table; t != nil {
			fmt.Fprintf(&sb, " header_rows=%d rows=%q", t.HeaderRows, t.Rows)
			if len(t.Merges) > 0 {
				fmt.Fprintf(&sb, " merges=%v", t.Merges)
			}
			if opts.Hidden && len(t.Hidden) > 0 {
				fmt.Fprintf(&sb, " hidden_cells=%v", t.Hidden)
			}
		}
		out = append(out, sb.String())
	}
	return out
}
