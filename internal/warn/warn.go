// Package warn holds the warning codes this library raises beyond the ones
// every go-fast library shares (those are in gofastcore). Each states a
// fact about the file. They are documented where the public package
// declares them.
package warn

const (
	UnreadContent       = "ooxml.unread_content"
	ControlCharsRemoved = "ooxml.control_characters_removed"
	PartDamaged         = "ooxml.part_damaged"
	TableFlattened      = "ooxml.table_flattened"
	FormulaNoValue      = "ooxml.formula_no_value"
	RegionSplit         = "ooxml.region_split"
)
