package xmlscan

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// Inputs built to be expensive for a careless tokenizer. Each must be read
// in time proportional to its size, through the byte path and through a
// reader that delivers ordinary-sized chunks: at 4 MiB in at most 8 times
// the time it takes at 1 MiB, where linear work is 4 times and quadratic
// 16. The two are timed best of three, in turn, so a slow or busy machine
// slows both alike; a full-size read under 20 ms passes whatever its
// ratio, as times that short are mostly noise.
func TestWorstCaseInputsAreLinear(t *testing.T) {
	const size = 4 << 20
	full, quarter := worstCases(size), worstCases(size/4)
	read := func(name, in, way string) time.Duration {
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
		return time.Since(start)
	}
	for name := range full {
		for _, way := range []string{"bytes", "reader"} {
			var big, small time.Duration
			for i := range 3 {
				d := read(name, quarter[name], way)
				if i == 0 || d < small {
					small = d
				}
				if d = read(name, full[name], way); i == 0 || d < big {
					big = d
				}
			}
			ratio := float64(big) / float64(small)
			if big > 20*time.Millisecond && ratio > 8 {
				t.Errorf("%s (%s): %v for %d MiB, %.1f times its time for a quarter of it", name, way, big.Round(time.Millisecond), len(full[name])>>20, ratio)
			} else {
				t.Logf("%-34s %-6s %6.0f MB/s, %4.1f times a quarter", name, way, float64(len(full[name]))/1e6/big.Seconds(), ratio)
			}
		}
	}
}

// worstCases gives the inputs of TestWorstCaseInputsAreLinear, each of
// about size bytes.
func worstCases(size int) map[string]string {
	return map[string]string{
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
}
