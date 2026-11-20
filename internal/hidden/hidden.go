// Package hidden has what the readers of the three formats share in
// placing what the file declares about the display of text (hidden, no
// ink) on blocks and spans.
package hidden

import (
	"bytes"

	"github.com/AOShei/go-fast-core"
)

// Settle turns the spans collected while a paragraph was read into the
// spans and display reasons of its block. The spans are moved to the
// trimmed text, raw[from:to]. A reason that covers all of the text becomes
// a reason of the whole block, which is how the model says it; one that
// covers part stays on its spans.
func Settle(collected []gofastcore.Span, from, to int, raw []byte) ([]gofastcore.Span, gofastcore.Display) {
	if len(collected) == 0 {
		return nil, 0
	}
	var all gofastcore.Display
	spans := make([]gofastcore.Span, 0, len(collected))
	for _, sp := range collected {
		sp.Start, sp.End = max(sp.Start, from)-from, min(sp.End, to)-from
		if sp.Start < sp.End {
			spans = append(spans, sp)
			all |= sp.Display
		}
	}
	text := raw[from:to]
	whole := all
	for bit := gofastcore.Display(1); bit != 0 && bit <= all; bit <<= 1 {
		if all&bit == 0 {
			continue
		}
		// The reason covers the text if nothing but white space lies
		// outside the spans that carry it.
		at := 0
		for _, sp := range spans {
			if sp.Display&bit == 0 {
				continue
			}
			if len(bytes.TrimSpace(text[at:sp.Start])) > 0 {
				whole &^= bit
				break
			}
			at = sp.End
		}
		if whole&bit != 0 && len(bytes.TrimSpace(text[at:])) > 0 {
			whole &^= bit
		}
	}
	kept := spans[:0]
	for _, sp := range spans {
		sp.Display &^= whole
		if sp.Display != 0 || sp.Style != 0 || sp.Href != "" {
			kept = append(kept, sp)
		}
	}
	if len(kept) == 0 {
		kept = nil
	}
	return kept, all
}
