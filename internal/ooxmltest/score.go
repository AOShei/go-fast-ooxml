package ooxmltest

import (
	"strings"
	"unicode"

	"github.com/AOShei/go-fast-core"
)

// Words splits text into the words that the scorers compare.
//
// Splitting is on Unicode whitespace. Presentation-form ligatures are
// expanded, so that "\uFB01le" and "file" are the same word whichever tool
// produced them. Case and the punctuation attached to a word are kept: they
// are content. A token with no letter or digit in it is dropped, because it
// is layout and not content: a list bullet, a Markdown table rule, a
// heading mark. Without that rule a tool would be marked down for how it
// draws a list, or up for drawing it the same way as the reference.
func Words(text string) []string {
	text = gofastcore.ExpandLigatures(text)
	fields := strings.FieldsFunc(text, unicode.IsSpace)
	words := fields[:0]
	for _, f := range fields {
		if strings.IndexFunc(f, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
			words = append(words, f)
		}
	}
	return words
}

// Score is the word-level agreement between extracted text and a reference.
type Score struct {
	Precision float64 `json:"precision"` // share of extracted words that are in the reference
	Recall    float64 `json:"recall"`    // share of reference words that were extracted
	F1        float64 `json:"f1"`
	// Order is the length of the longest common subsequence of the two
	// word lists divided by the longer list: 1 when the same words come in
	// the same order.
	Order float64 `json:"order"`
	Got   int     `json:"got_words"`
	Want  int     `json:"want_words"`
}

// Compare scores got against want. Words are counted as a multiset, so a
// word extracted twice when the reference has it once costs precision.
// Two empty texts agree perfectly.
func Compare(got, want string) Score {
	g, w := Words(got), Words(want)
	s := Score{Got: len(g), Want: len(w)}
	if len(g) == 0 && len(w) == 0 {
		s.Precision, s.Recall, s.F1, s.Order = 1, 1, 1, 1
		return s
	}
	counts := make(map[string]int, len(w))
	for _, x := range w {
		counts[x]++
	}
	common := 0
	for _, x := range g {
		if counts[x] > 0 {
			counts[x]--
			common++
		}
	}
	if len(g) > 0 {
		s.Precision = float64(common) / float64(len(g))
	}
	if len(w) > 0 {
		s.Recall = float64(common) / float64(len(w))
	}
	if s.Precision+s.Recall > 0 {
		s.F1 = 2 * s.Precision * s.Recall / (s.Precision + s.Recall)
	}
	s.Order = float64(lcs(g, w)) / float64(max(len(g), len(w)))
	return s
}

// lcsLimit bounds the quadratic longest-common-subsequence computation.
// Longer inputs are compared window by window, which can only lower the
// score, never raise it.
const lcsLimit = 4000

func lcs(a, b []string) int {
	total := 0
	for len(a) > lcsLimit && len(b) > lcsLimit {
		total += lcsExact(a[:lcsLimit], b[:lcsLimit])
		a, b = a[lcsLimit:], b[lcsLimit:]
	}
	if len(a) > lcsLimit {
		a = a[:lcsLimit]
	}
	if len(b) > lcsLimit {
		b = b[:lcsLimit]
	}
	return total + lcsExact(a, b)
}

func lcsExact(a, b []string) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			switch {
			case a[i-1] == b[j-1]:
				cur[j] = prev[j-1] + 1
			case prev[j] >= cur[j-1]:
				cur[j] = prev[j]
			default:
				cur[j] = cur[j-1]
			}
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// CellAgreement returns the share of cells on which two grids agree. The
// grids are compared over the union of their shapes, so a missing row or
// column counts as disagreement unless the other grid's cells there are
// empty. Cell text is compared after trimming surrounding whitespace. Two
// empty grids agree perfectly.
func CellAgreement(got, want [][]string) float64 {
	rows := max(len(got), len(want))
	cell := func(g [][]string, r, c int) string {
		if r < len(g) && c < len(g[r]) {
			return strings.TrimSpace(g[r][c])
		}
		return ""
	}
	total, same := 0, 0
	for r := range rows {
		cols := 0
		if r < len(got) {
			cols = len(got[r])
		}
		if r < len(want) {
			cols = max(cols, len(want[r]))
		}
		for c := range cols {
			g, w := cell(got, r, c), cell(want, r, c)
			if g == "" && w == "" {
				continue
			}
			total++
			if g == w {
				same++
			}
		}
	}
	if total == 0 {
		return 1
	}
	return float64(same) / float64(total)
}
