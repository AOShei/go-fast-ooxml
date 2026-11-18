package xmlscan

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// Inputs built to be expensive for a careless tokenizer. Each megabyte
// must be read in time proportional to its size, through the byte path and
// through a reader that delivers ordinary-sized chunks.
func TestWorstCaseInputsAreLinear(t *testing.T) {
	const size = 4 << 20
	cases := map[string]string{
		"one long attribute":         `<a x="` + strings.Repeat("v", size) + `"/>`,
		"many attributes on one tag": `<a ` + strings.Repeat(`x="1" `, size/6) + `/>`,
		"many small elements":        `<r>` + strings.Repeat(`<c/>`, size/4) + `</r>`,
		"deep then shallow":          strings.Repeat(`<a>`, 500) + strings.Repeat(`x`, size) + strings.Repeat(`</a>`, 500),
		"all ampersands":             `<a>` + strings.Repeat(`&`, size) + `</a>`,
		"all references":             `<a>` + strings.Repeat(`&amp;`, size/5) + `</a>`,
		"all carriage returns":       `<a>` + strings.Repeat("\r", size) + `</a>`,
		"long comment":               `<a><!--` + strings.Repeat("-", size) + `--></a>`,
		"long cdata of brackets":     `<a><![CDATA[` + strings.Repeat("]", size) + `]]></a>`,
		"unterminated tag":           `<a ` + strings.Repeat(`x="1" `, size/6),
		"unterminated comment":       `<a><!--` + strings.Repeat("-", size),
		"many namespace prefixes": `<r ` + func() string {
			var sb strings.Builder
			for i := range 2000 {
				sb.WriteString(`xmlns:p` + strings.Repeat("q", i%50) + string(rune('a'+i%26)) + `="urn:x" `)
			}
			return sb.String()
		}() + `>` + strings.Repeat(`<pz:c pz:a="1"/>`, size/16) + `</r>`,
		"mismatched end tags with recovery": strings.Repeat(`<a>`, 400) + strings.Repeat(`<b/></c>`, size/8),
		"shadowed prefixes":                 strings.Repeat(`<p:a xmlns:p="urn:x" xmlns:q="urn:y">`, 400) + strings.Repeat(`<q:b p:x="1"/>`, size/14) + strings.Repeat(`</p:a>`, 400),
	}
	for name, in := range cases {
		for _, way := range []string{"bytes", "reader"} {
			opts := &Options{Recover: strings.Contains(name, "recovery"), MaxTokenBytes: 2 * size}
			s := NewBytes([]byte(in), opts)
			if way == "reader" {
				s = New(bytes.NewReader([]byte(in)), opts)
			}
			start := time.Now()
			for s.Next() != EOF {
				if s.Kind() == Start {
					s.Attr(NoSpace, "x")
				}
			}
			if d := time.Since(start); d > 2*time.Second {
				t.Errorf("%s (%s): %v for %d MiB", name, way, d.Round(time.Millisecond), len(in)>>20)
			} else {
				t.Logf("%-34s %-6s %6.0f MB/s", name, way, float64(len(in))/1e6/d.Seconds())
			}
		}
	}
}
