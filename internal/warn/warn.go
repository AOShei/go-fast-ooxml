// Package warn holds what the readers share in saying what a document
// lost: the library's fixed bounds and the limit.exceeded warnings that
// say what they left out, the gathering of warnings that would repeat
// (Names), the end of a reading at the text limit (ErrText), and the
// library's own code ooxml.table_flattened, documented where the public
// package declares it.
package warn

import (
	"errors"
	"strconv"

	"github.com/AOShei/go-fast-core"
)

const (
	TableFlattened = "ooxml.table_flattened"
)

// ErrText is what a reader ends a scan with when the document has taken
// all the text it may hold (gofastcore.Limits.MaxTextBytes).
var ErrText = errors.New("the limit on the amount of text was reached")

// TextCut is the message of the limit.exceeded warning on a part cut off
// for that reason; what stands for "part" is the kind of part.
func TextCut(noun string) string {
	return "the limit on the amount of text was reached; the " + noun + " is cut off there"
}

// Unmapped is the message of gofastcore.WarnTextUnmappedGlyphs: n codes of
// a symbol font that have no character in Unicode.
func Unmapped(n int) string {
	return strconv.Itoa(n) + " characters of a symbol font that have no character in Unicode appear as U+FFFD"
}

// CharRefs is the message of gofastcore.WarnTextUnmappedGlyphs for n
// character references to no character XML allows, each read as U+FFFD.
func CharRefs(n int) string {
	if n == 1 {
		return "1 character reference names no character (&#0;, a surrogate or a number past U+10FFFF) and is read as U+FFFD"
	}
	return strconv.Itoa(n) + " character references name no character (&#0;, a surrogate or a number past U+10FFFF) and are read as U+FFFD"
}

// FitRows returns how many of rows rows of cols cells a table block can
// have and stay within what budget has left, taking
// gofastcore.BlockCost for the block and gofastcore.CellCost for each
// cell (the cells' text was taken as it was read), and takes it. A result
// below rows means the table is cut there; 0 means not even its block
// fits, and nothing is taken.
func FitRows(budget *gofastcore.TextBudget, rows, cols int) int {
	if cols <= 0 || rows <= 0 {
		return rows
	}
	if budget == nil {
		return rows
	}
	left := budget.Left() - gofastcore.BlockCost
	if left < int64(gofastcore.CellCost*cols) {
		return 0
	}
	fit := min(int64(rows), left/int64(gofastcore.CellCost*cols))
	budget.Take(gofastcore.BlockCost + gofastcore.CellCost*int(fit)*cols)
	return int(fit)
}
