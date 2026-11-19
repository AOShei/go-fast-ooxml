// Package warn holds the warning codes this library raises beyond the ones
// every go-fast library shares (those are in gofastcore). Each states a
// fact about the file. They are documented where the public package
// declares them.
package warn

import (
	"errors"
	"strconv"

	"github.com/AOShei/go-fast-core"
)

const (
	UnreadContent       = "ooxml.unread_content"
	ControlCharsRemoved = "ooxml.control_characters_removed"
	PartDamaged         = "ooxml.part_damaged"
	TableFlattened      = "ooxml.table_flattened"
	FormulaNoValue      = "ooxml.formula_no_value"
	RegionSplit         = "ooxml.region_split"
	HiddenUnchecked     = "ooxml.hidden_unchecked"
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
