package warn

import (
	"strconv"
	"strings"
	"sync"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
)

// Cap is a fixed bound of this library that can leave content out. Each
// is said once per part it left something out of, as limit.exceeded with
// severity error, with how much it left out; a bound that only bounds
// work loses nothing and is not here.
type Cap uint8

const (
	// Word.
	CapFieldCode Cap = iota
	CapFieldDepth
	CapListLabel
	CapNoteSections
	CapVariables
	// PowerPoint.
	CapSlides
	CapSlideTableColumns
	CapCommentDepth
	CapCommentAuthorID
	CapCommentDate
	CapCommentAuthors
	CapPlaceholders
	// Visio.
	CapPages
	CapShapes
	CapShapeDepth
	CapShapeRows
	CapShapeText
	CapBackPages
	CapRecordsets
	CapRecordColumns
	// Excel.
	CapSheets
	CapSheetRows
	CapSheetCols
	CapComments
	CapCommentAuthorsSheet
	CapLinks
	CapChoiceLists
	CapChoiceRanges
	CapDrawingParts
	CapDrawings
	CapMerges
	CapTableParts
	CapFormula
	CapNames
	CapPivotCaches
	CapPivotFields
	CapPivotItems
	CapCalculated
	CapExternalLinks
	CapSheetRefs
	CapSharedFormulas
	CapCellStyles
	CapNumberFormats
	CapPivotTables
	CapExternalNames
	CapExternalSheets
	CapBookTables
	CapTableColumns
	// Shared.
	CapChartSeries
	CapChartPoints
	CapChartLevels
	CapChartTitle
	CapChartValues
	CapChartTotal
	CapChartAxes
	CapDiagramNodes
	CapDiagramLinks
	CapDiagramDepth
	CapCustomXML
	CapProjects
	CapProjectSize
	CapTrailing
	CapModules
	CapPropertyName
	CapAddress
	CapName
	CapParaID
	CapPropertyValue
	CapCustomProps
	numCaps
)

type capInfo struct {
	what      string // what was left out and how, a clause
	one, many string // the unit counted
}

func itoa(n int) string { return strconv.Itoa(n) }

var caps = [numCaps]capInfo{
	CapFieldCode:    {"field codes longer than " + itoa(MaxFieldCode) + " bytes were cut there", "field", "fields"},
	CapFieldDepth:   {"fields nested more than " + itoa(MaxFieldDepth) + " deep were read as text of the field around them; their codes were not kept", "field", "fields"},
	CapListLabel:    {"list labels longer than " + itoa(MaxListLabel) + " bytes were cut there", "label", "labels"},
	CapNoteSections: {"sections past " + itoa(MaxNoteSections) + " number their notes as the document does, not as they say", "section", "sections"},
	CapVariables:    {"document variables past " + itoa(MaxVariables) + " were not read", "variable", "variables"},

	CapSlides:            {"slides past " + itoa(MaxSlides) + " were not read", "slide", "slides"},
	CapSlideTableColumns: {"table cells past column " + itoa(MaxTableColumns) + " were not read", "cell", "cells"},
	CapCommentDepth:      {"replies nested more than " + itoa(MaxCommentDepth) + " deep were read as part of the comment around them", "reply", "replies"},
	CapCommentAuthorID:   {"comments whose author is named by more than 64 bytes were read with no author", "comment", "comments"},
	CapCommentDate:       {"comment dates longer than 64 bytes were not read", "date", "dates"},
	CapCommentAuthors:    {"comment authors past " + itoa(MaxCommentAuthors) + " were not read; their comments have no author", "author", "authors"},
	CapPlaceholders:      {"placeholders of a layout or master past " + itoa(MaxPlaceholders) + " were not read; slide placeholders that would inherit from them do not", "placeholder", "placeholders"},

	CapPages:         {"pages past " + itoa(MaxSlides) + " were not read", "page", "pages"},
	CapShapes:        {"shapes of a page or master past " + itoa(MaxShapes) + " were not read", "shape", "shapes"},
	CapShapeDepth:    {"shapes in groups nested more than " + itoa(MaxShapeDepth) + " deep were not read", "shape", "shapes"},
	CapShapeRows:     {"rows of a section of a shape past " + itoa(MaxShapeRows) + " were not read (formats of text, fields, shape data, links)", "row", "rows"},
	CapShapeText:     {"text of a shape past " + itoa(MaxShapeText) + " runs, fields and paragraph marks was not read", "shape", "shapes"},
	CapBackPages:     {"background pages behind a page past " + itoa(MaxBackPages) + " in turn, or that lead back to a page already shown, were not read", "page", "pages"},
	CapRecordsets:    {"data recordsets past " + itoa(MaxRecordsets) + " were not read", "recordset", "recordsets"},
	CapRecordColumns: {"columns of a data recordset past " + itoa(MaxRecordColumns) + " were not read", "column", "columns"},

	CapSheets:              {"sheets past " + itoa(MaxSheets) + " were not read", "sheet", "sheets"},
	CapSheetRows:           {"rows past row " + itoa(MaxRows) + " of the sheet were not read", "row", "rows"},
	CapSheetCols:           {"cells past column " + itoa(MaxCols) + " of the sheet were not read", "cell", "cells"},
	CapComments:            {"comments of a sheet past " + itoa(MaxComments) + " were not read", "comment", "comments"},
	CapCommentAuthorsSheet: {"comment authors past " + itoa(MaxSheetCommentAuthors) + " were not read; their comments have no author", "author", "authors"},
	CapLinks:               {"hyperlinks of a sheet past " + itoa(MaxLinks) + " were not read", "hyperlink", "hyperlinks"},
	CapChoiceLists:         {"drop-down lists of a sheet past " + itoa(MaxChoiceLists) + " were not read", "list", "lists"},
	CapChoiceRanges:        {"ranges of a sheet's drop-down lists past " + itoa(MaxChoiceRanges) + " were not read", "range", "ranges"},
	CapDrawingParts:        {"drawing parts of a sheet past " + itoa(MaxDrawingParts) + " were not read (their charts, pictures and text boxes)", "part", "parts"},
	CapDrawings:            {"drawing parts of a chart sheet past " + itoa(MaxDrawings) + " charts, pictures and text boxes were not read", "part", "parts"},
	CapMerges:              {"merged ranges of a sheet past " + itoa(MaxMerges) + " were not read; their cells are given unmerged", "range", "ranges"},
	CapTableParts:          {"declared tables of a sheet past " + itoa(MaxTables) + " were not read; their cells are given as cells of the sheet", "table", "tables"},
	CapFormula:             {"formulas longer than " + itoa(MaxFormula) + " bytes were cut there", "formula", "formulas"},
	CapNames:               {"defined names past " + itoa(MaxNames) + " were not read", "name", "names"},
	CapPivotCaches:         {"pivot caches past " + itoa(MaxPivotCaches) + " were not read", "cache", "caches"},
	CapPivotFields:         {"fields of a pivot cache past " + itoa(MaxPivotFields) + " were not read", "field", "fields"},
	CapPivotItems:          {"values of a pivot cache field past " + itoa(MaxPivotItems) + " were not read", "value", "values"},
	CapCalculated:          {"calculated fields of a pivot cache past " + itoa(MaxCalculated) + " were not read", "formula", "formulas"},
	CapExternalLinks:       {"links to other workbooks past " + itoa(MaxExternalLinks) + " were not read; formulas that use them are not read back", "link", "links"},
	CapSheetRefs:           {"references of the workbook's formulas to other sheets past " + itoa(MaxSheetRefs) + " were not read; formulas that use them are not read back", "reference", "references"},
	CapSharedFormulas:      {"shared formulas of a sheet past " + itoa(MaxSharedFormulas) + " were not kept; cells that use them give no formula", "formula", "formulas"},
	CapCellStyles:          {"cell formats and fonts of the workbook past " + itoa(MaxCellStyles) + " were not read; cells that use them show their values unformatted", "format", "formats"},
	CapNumberFormats:       {"number formats of the workbook past " + itoa(MaxNumberFormats) + " were not read; cells that use them show their values unformatted", "format", "formats"},
	CapPivotTables:         {"pivot tables of a sheet past " + itoa(MaxPivotTables) + " were not read as tables; their cells are given as cells of the sheet", "table", "tables"},
	CapExternalNames:       {"names of a linked workbook past " + itoa(MaxExternalNames) + " were not read; formulas that use them are not read back", "name", "names"},
	CapExternalSheets:      {"sheets of a linked workbook past " + itoa(MaxExternalNames) + " were not read; formulas that use them are not read back", "sheet", "sheets"},
	CapBookTables:          {"declared tables of the workbook past " + itoa(MaxBookTables) + " were not read; formulas that use them are not read back", "table", "tables"},
	CapTableColumns:        {"columns of a declared table past " + itoa(MaxCols) + " were not read; formulas that use them are not read back", "column", "columns"},

	CapChartSeries:   {"chart series past " + itoa(MaxChartSeries) + " were not read", "series", "series"},
	CapChartPoints:   {"chart points at an index past " + itoa(MaxChartPoints) + " were not read", "point", "points"},
	CapChartLevels:   {"levels of chart categories past " + itoa(MaxChartLevels) + " were not read", "level", "levels"},
	CapChartTitle:    {"chart and series titles longer than " + itoa(MaxChartTitle) + " bytes were cut there", "title", "titles"},
	CapChartValues:   {"sets of values of a chart series past " + itoa(MaxChartValues) + " were not read", "set", "sets"},
	CapChartTotal:    {"charts holding more than " + itoa(MaxChartTotal) + " points were cut there", "chart", "charts"},
	CapChartAxes:     {"chart axes or plot types past " + itoa(MaxChartAxes) + " were not read", "axis", "axes"},
	CapDiagramNodes:  {"SmartArt nodes past " + itoa(MaxDiagramNodes) + " were not read", "node", "nodes"},
	CapDiagramLinks:  {"SmartArt connections past " + itoa(MaxDiagramLinks) + " were not read; nodes that only they place are not read", "connection", "connections"},
	CapDiagramDepth:  {"SmartArt nodes deeper than " + itoa(MaxDiagramDepth) + " levels were not read", "node", "nodes"},
	CapCustomXML:     {"custom XML parts past " + itoa(MaxCustomXML) + " were not read", "part", "parts"},
	CapProjects:      {"VBA projects past " + itoa(MaxProjects) + " were not read", "project", "projects"},
	CapProjectSize:   {"VBA projects larger than " + itoa(MaxProjectSize) + " bytes, or with more than 65536 entries in their compound file, were not read", "project", "projects"},
	CapTrailing:      {"data after the end of the archive past its first " + itoa(MaxTrailing) + " bytes was not read", "byte", "bytes"},
	CapModules:       {"VBA modules past " + itoa(MaxModules) + " were not read", "module", "modules"},
	CapPropertyName:  {"custom property names longer than " + itoa(MaxPropertyName) + " bytes were cut there", "name", "names"},
	CapAddress:       {"link addresses longer than " + itoa(MaxAddress) + " bytes were cut there", "address", "addresses"},
	CapName:          {"names and labels longer than " + itoa(MaxName) + " bytes (authors, names of sheets, shapes, tables, files and defined names, cell values) were cut there", "name", "names"},
	CapParaID:        {"paragraph identifiers longer than 32 bytes were not kept", "identifier", "identifiers"},
	CapPropertyValue: {"document property values longer than " + itoa(MaxPropertyValue) + " bytes were cut there", "value", "values"},
	CapCustomProps:   {"custom document properties past " + itoa(MaxCustomProps) + " were not read", "property", "properties"},
}

// The bounds, where the code that keeps to them and the messages above
// both need them.
const (
	MaxFieldCode           = 1 << 16
	MaxFieldDepth          = 64
	MaxListLabel           = 64
	MaxNoteSections        = 1 << 16
	MaxVariables           = 1 << 14
	MaxSlides              = 1 << 16
	MaxTableColumns        = 1 << 14
	MaxCommentDepth        = 64
	MaxCommentAuthors      = 1 << 16
	MaxPlaceholders        = 256
	MaxShapes              = 1 << 18
	MaxShapeDepth          = 64
	MaxShapeRows           = 1 << 14
	MaxShapeText           = 1 << 18
	MaxBackPages           = 16
	MaxRecordsets          = 1 << 10
	MaxRecordColumns       = 1 << 14
	MaxSheets              = 1 << 16
	MaxRows                = 1 << 20
	MaxCols                = 1 << 14
	MaxComments            = 1 << 18
	MaxSheetCommentAuthors = 1 << 12
	MaxLinks               = 1 << 18
	MaxChoiceLists         = 1 << 14
	MaxChoiceRanges        = 1 << 18
	MaxDrawingParts        = 16
	MaxDrawings            = 1 << 16
	MaxMerges              = 1 << 18
	MaxTables              = 1 << 12
	MaxFormula             = 32 << 10
	MaxNames               = 1 << 14
	MaxPivotCaches         = 1 << 10
	MaxPivotFields         = 1 << 14
	MaxPivotItems          = 1 << 20
	MaxCalculated          = 1 << 12
	MaxExternalLinks       = 1 << 16
	MaxExternalNames       = 1 << 16
	MaxBookTables          = 1 << 16
	MaxSheetRefs           = 1 << 16
	MaxSharedFormulas      = 1 << 20
	MaxCellStyles          = 1 << 17
	MaxNumberFormats       = 1 << 13
	MaxPivotTables         = 1 << 10
	MaxChartSeries         = 1 << 10
	MaxChartPoints         = 1 << 20
	MaxChartLevels         = 16
	MaxChartTitle          = 4096
	MaxChartAxes           = 16
	MaxChartValues         = 8
	MaxChartTotal          = 1 << 21
	MaxDiagramNodes        = 1 << 16
	MaxDiagramLinks        = 1 << 17
	MaxDiagramDepth        = 64
	MaxCustomXML           = 1 << 10
	MaxProjects            = 16
	MaxProjectSize         = 1 << 28
	MaxTrailing            = 1 << 20
	MaxModules             = 1 << 12
	MaxPropertyName        = clean.MaxLabel
	MaxPropertyValue       = 64 << 10
	MaxCustomProps         = 256
	MaxAddress             = clean.MaxAddress
	MaxName                = clean.MaxLabel
)

// Cuts is what a package counts of the names and addresses it cut.
type Cuts interface {
	CutTargets() int64
	CutNames() (n, bytes int64)
}

// Package is the warnings about what fixed bounds cut of what the package
// itself holds, for the document: the addresses its relationships give
// links, and the names of its parts put in the output.
func Package(p Cuts) []gofastcore.Warning {
	var l Lost
	l.Add(CapAddress, p.CutTargets())
	n, bytes := p.CutNames()
	l.AddCut(CapName, n, bytes)
	return l.Warnings()
}

// Message is the text of the warning for n items a bound left out.
func (c Cap) Message(n int64) string {
	info := caps[c]
	unit := info.many
	if n == 1 {
		unit = info.one
	}
	return info.what + " (" + strconv.FormatInt(n, 10) + " " + unit + ")"
}

// Lost counts what the fixed bounds left out of one part, or of the
// document, bound by bound in the order met, so that the warnings come
// in an order that does not vary. The zero value is ready to use, and a
// nil *Lost counts nothing. Several goroutines may use it at once: what parts
// share (a chart, a master) may be read by any of them.
type Lost struct {
	mu    sync.Mutex
	n     [numCaps]int64
	bytes [numCaps]int64 // of a bound that cuts text: the bytes cut off
	order []Cap
}

// Add records that the bound c left n more items out.
func (l *Lost) Add(c Cap, n int64) {
	if l == nil || n <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n[c] == 0 {
		l.order = append(l.order, c)
	}
	l.n[c] = satAdd(l.n[c], n)
}

// AddCut records that the bound c cut n more texts short, leaving bytes
// bytes out.
func (l *Lost) AddCut(c Cap, n, bytes int64) {
	if l == nil || n <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n[c] == 0 {
		l.order = append(l.order, c)
	}
	l.n[c] = satAdd(l.n[c], n)
	l.bytes[c] = satAdd(l.bytes[c], max(bytes, 0))
}

// Merge adds what o counted to l, and leaves o as it is.
func (l *Lost) Merge(o *Lost) {
	if l == nil || o == nil || l == o {
		return
	}
	o.mu.Lock()
	order := append([]Cap(nil), o.order...)
	n, bytes := o.n, o.bytes
	o.mu.Unlock()
	for _, c := range order {
		l.AddCut(c, n[c], bytes[c])
	}
}

// Name is clean.Name, counting a name it cuts.
func (l *Lost) Name(s string) string {
	l.cut(s, MaxName, CapName)
	return clean.Name(s)
}

// Over returns how many bytes a name is longer than Name keeps; 0 if it
// is not.
func Over(s string) int64 {
	return max(int64(len(strings.TrimSpace(s))-MaxName), 0)
}

// NameOver counts a name over the bound by over bytes (Over), where it is
// put in the output.
func (l *Lost) NameOver(over int64) {
	if over > 0 {
		l.AddCut(CapName, 1, over)
	}
}

// Label is clean.Label, counting a label it cuts.
func (l *Lost) Label(s string) string {
	l.cut(s, MaxName, CapName)
	return clean.Label(s)
}

// Address is clean.Address, counting an address it cuts.
func (l *Lost) Address(s string) string {
	l.cut(s, MaxAddress, CapAddress)
	return clean.Address(s)
}

func (l *Lost) cut(s string, limit int, c Cap) {
	if n := len(strings.TrimSpace(s)); n > limit {
		l.AddCut(c, 1, int64(n-limit))
	}
}

// Any reports whether anything was counted.
func (l *Lost) Any() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.order) > 0
}

// Count returns what the bound c left out.
func (l *Lost) Count(c Cap) int64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n[c]
}

// Warnings returns the warnings, one per bound, in the order met.
func (l *Lost) Warnings() []gofastcore.Warning {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []gofastcore.Warning
	for _, c := range l.order {
		msg := c.Message(l.n[c])
		if b := l.bytes[c]; b > 0 {
			msg = strings.TrimSuffix(msg, ")") + ", " + strconv.FormatInt(b, 10) + " bytes left out)"
		}
		out = append(out, gofastcore.Warning{Code: gofastcore.WarnLimitExceeded, Severity: gofastcore.SeverityError, Message: msg})
	}
	return out
}

// Take returns what was counted, and forgets it.
func (l *Lost) Take() *Lost {
	out := &Lost{}
	if l == nil {
		return out
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out.n, out.bytes, out.order = l.n, l.bytes, l.order
	l.n, l.bytes, l.order = [numCaps]int64{}, [numCaps]int64{}, nil
	return out
}

// Reset forgets what was counted.
func (l *Lost) Reset() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.n, l.bytes, l.order = [numCaps]int64{}, [numCaps]int64{}, nil
}

// Say gives the warnings to p, one per bound, forgets what was counted,
// and reports whether there was any.
func (l *Lost) Say(p *gofastcore.Part) bool {
	if !l.Any() {
		return false // the usual case, which need not copy anything
	}
	ws := l.Take().Warnings()
	for _, w := range ws {
		p.Warn(w.Severity, w.Code, w.Message)
	}
	return len(ws) > 0
}

func satAdd(a, b int64) int64 {
	if a > 1<<62 || b > 1<<62 {
		return 1 << 62
	}
	return min(a+b, 1<<62)
}

// The nouns of the warning that parts could not be read to their end.
const (
	DamagedOne  = "part could not be read to its end"
	DamagedMany = "parts could not be read to their end"
)

// Names gathers what a warning that could repeat is about (parts that
// could not be read, references that lead nowhere), so that it is said
// once, with how many there are and the first few. The zero value is
// ready to use. Several goroutines may use it at once.
type Names struct {
	mu    sync.Mutex
	n     int64
	first []string
	seen  map[string]bool
}

// shown is how many names a warning lists.
const shown = 8

// Add counts one more, named name.
func (l *Names) Add(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.n = satAdd(l.n, 1)
	l.keep(name)
}

// AddOnce counts name if it has not been counted before.
func (l *Names) AddOnce(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[string]bool{}
	}
	if l.seen[name] {
		return
	}
	l.seen[name] = true
	l.n = satAdd(l.n, 1)
	l.keep(name)
}

func (l *Names) keep(name string) {
	if len(l.first) >= shown {
		return
	}
	for _, f := range l.first {
		if f == name {
			return
		}
	}
	l.first = append(l.first, name)
}

// Len returns how many were counted.
func (l *Names) Len() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}

// Message returns "N <noun> (a, b, ..., and K more)", with one or many as
// the noun, and forgets what was counted; "" if nothing was.
func (l *Names) Message(one, many string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n == 0 {
		return ""
	}
	noun := many
	if l.n == 1 {
		noun = one
	}
	msg := strconv.FormatInt(l.n, 10) + " " + noun + " (" + strings.Join(l.first, ", ")
	if more := l.n - int64(len(l.first)); more > 0 {
		if len(l.first) > 0 {
			msg += ", "
		}
		msg += "and " + strconv.FormatInt(more, 10) + " more"
	}
	l.n, l.first, l.seen = 0, nil, nil
	return msg + ")"
}
