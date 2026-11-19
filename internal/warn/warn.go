// Package warn holds the warning codes this library raises beyond the ones
// every go-fast library shares (those are in gofastcore). Each states a
// fact about the file. They are documented where the public package
// declares them.
package warn

import (
	"errors"
	"strconv"
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
