// Package sml reads Excel workbooks (SpreadsheetML) into the shared
// document model: one part per sheet, the cells as tables, with what each
// cell displays rather than what it stores.
package sml

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/media"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/stored"
	"github.com/AOShei/go-fast-ooxml/internal/theme"
	"github.com/AOShei/go-fast-ooxml/internal/unit"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// Config holds the limits that are this format's own.
type Config struct {
	// MaxCells bounds the cells kept from a workbook, all sheets
	// together.
	MaxCells int64
}

var scanOptions = &xmlscan.Options{Spaces: ns.Spaces}

// workbook is what the sheets of a workbook share. Apart from the
// budgets and the record of pictures, nothing in it changes once the
// sheets are being read.
type workbook struct {
	pkg      *opc.Package
	opts     *gofastcore.Options
	strings  *stringTable
	styles   *styleSheet
	theme    *theme.Scheme
	date1904 bool
	persons  map[string]string // authors of threaded comments, by id

	cells atomic.Int64 // cells that may still be kept

	mu     sync.Mutex
	images map[string]*gofastcore.Image
}

// sheetRef is a sheet as the workbook lists it.
type sheetRef struct {
	name   string
	hidden bool
	target string // its part; empty if the workbook does not say
	kind   string // the ending of its relationship type
}

// maxSheets bounds the sheets of a workbook that are listed at all.
const maxSheets = 1 << 16

// Book is a workbook opened for reading a sheet at a time. What the sheets
// share (the list of them, the styles, the shared strings, the theme) is
// read when it is opened.
type Book struct {
	wb   *workbook
	refs []sheetRef
	cfg  Config

	after       int64    // the decompression budget once the shared parts were read
	budget      bool     // it ran out while they were read
	damaged     []string // shared parts that could not be read to their end
	external    int      // links to other workbooks and files
	removed     int      // control characters removed from shared strings
	stringsFull bool     // the shared strings were cut off at their limit

	// What the workbook stores and shows on no sheet (stored.go).
	names       []definedName
	localNames  []localName
	macroSheets bool
	caches      []pivotCache
	calculated  []calcField
	extra       stored.Source // the VBA project and custom XML data
	raw         []byte
}

// SheetInfo is a sheet as the workbook lists it.
type SheetInfo struct {
	Name   string
	Hidden bool
}

// Open reads what the sheets of the workbook in main share. It does not
// fail: what could not be read is reported by Summary, and a workbook
// that lists no sheets has one part that says so.
func Open(pkg *opc.Package, main string, opts *gofastcore.Options, cfg Config) *Book {
	wb := &workbook{pkg: pkg, opts: opts, images: map[string]*gofastcore.Image{}}
	b := &Book{wb: wb, cfg: cfg}
	note := func(name string, err error) {
		switch {
		case err == nil:
		case errors.Is(err, opc.ErrBudget):
			b.budget = true
		default:
			b.damaged = append(b.damaged, name)
		}
	}
	load := func(name string, read func(*xmlscan.Scanner)) {
		if b.budget || !pkg.Has(name) {
			return
		}
		var s xmlscan.Scanner
		c, err := pkg.Scan(name, &s, scanOptions)
		if err != nil {
			note(name, err)
			return
		}
		read(&s)
		c.Close()
		note(name, s.Err())
	}

	rels, err := pkg.Rels(main)
	note(opc.RelsName(main), err)
	byID := make(map[string]opc.Rel, len(rels))
	for _, rel := range rels {
		byID[rel.ID] = rel
	}
	b.extra = stored.Find(pkg, rels)
	load(main, func(s *xmlscan.Scanner) {
		for s.Next() != xmlscan.EOF {
			if s.Kind() != xmlscan.Start || s.Space() != ns.Sheet {
				continue
			}
			switch {
			case s.Depth() == 2 && s.Is(ns.Sheet, "workbookPr"):
				if v, ok := s.Attr(xmlscan.NoSpace, "date1904"); ok {
					wb.date1904 = isTrue(v)
				}
			case s.Depth() == 3 && s.Is(ns.Sheet, "sheet") && len(b.refs) < maxSheets:
				var ref sheetRef
				if v, ok := s.Attr(xmlscan.NoSpace, "name"); ok {
					ref.name, _ = clean.String(v, clean.Prose)
				}
				if v, ok := s.Attr(xmlscan.NoSpace, "state"); ok {
					ref.hidden = string(v) == "hidden" || string(v) == "veryHidden"
				}
				if id, ok := s.Attr(ns.Rel, "id"); ok {
					if rel, found := byID[string(id)]; found && !rel.External {
						ref.target = rel.Target
						if i := strings.LastIndexByte(rel.Type, '/'); i >= 0 {
							ref.kind = rel.Type[i+1:]
						}
					}
				}
				b.refs = append(b.refs, ref)
				b.macroSheets = b.macroSheets || ref.kind == ns.RelMacrosheet || ref.kind == ns.RelIntlMacrosheet
			case s.Depth() == 2 && s.Is(ns.Sheet, "definedNames"):
				b.readNames(s)
			case s.Depth() == 2 && s.Is(ns.Sheet, "pivotCaches"):
				b.readCaches(s, byID)
			}
		}
	})
	// A name local to a sheet has the sheet's name in front.
	for _, l := range b.localNames {
		if l.sheet >= 0 && l.sheet < len(b.refs) {
			n := &b.names[l.index]
			n.name = b.refs[l.sheet].name + "!" + n.name
		}
	}

	// Pivot caches whose source is not in the workbook hold data that is
	// shown nowhere: the stored part reads it.
	if len(b.caches) > 0 && !b.budget {
		b.judgeCaches()
	}

	// What the sheets share. If no sheet is asked for there is nothing to
	// read it for.
	wanted := false
	for i := range b.refs {
		wanted = wanted || opts.Parts.Contains(i+1)
	}
	for _, rel := range rels {
		switch {
		case ns.IsRel(rel.Type, ns.RelExternalLink) || rel.External && !ns.IsRel(rel.Type, ns.RelHyperlink):
			b.external++
		case rel.External || !wanted:
		case ns.IsRel(rel.Type, ns.RelTheme):
			load(rel.Target, func(s *xmlscan.Scanner) { wb.theme = theme.Read(s) })
		case ns.IsRel(rel.Type, ns.RelPerson):
			load(rel.Target, func(s *xmlscan.Scanner) { wb.persons = readPersons(s) })
		}
	}
	for _, rel := range rels {
		if rel.External || !wanted {
			continue
		}
		switch {
		case ns.IsRel(rel.Type, ns.RelStyles):
			load(rel.Target, func(s *xmlscan.Scanner) { wb.styles = readStyles(s) })
		case ns.IsRel(rel.Type, ns.RelSharedStrings):
			load(rel.Target, func(s *xmlscan.Scanner) {
				var full bool
				var n int
				wb.strings, n, full = readStrings(s)
				b.removed += n
				b.stringsFull = b.stringsFull || full
			})
		}
	}
	b.after = pkg.Budget()
	b.Begin()
	return b
}

// Len returns the number of parts: one for each sheet, and one for a
// workbook that lists none.
func (b *Book) Len() int {
	if i := b.storedIndex(); i >= 0 {
		return i + 1
	}
	return max(len(b.refs), 1)
}

// Stored returns the index of the stored part, or -1 if there is none.
func (b *Book) Stored() int { return b.storedIndex() }

// Noun names the parts of a workbook.
func (b *Book) Noun() string { return "sheets" }

// Begin starts a pass over the workbook: the limits count from here.
func (b *Book) Begin() {
	b.wb.pkg.Restore(b.after)
	b.wb.cells.Store(b.cfg.MaxCells)
}

// Limited reports whether the decompression budget ran out while the
// shared parts were read.
func (b *Book) Limited() bool { return b.budget }

// Sheets lists the sheets in the order of the workbook.
func (b *Book) Sheets() []SheetInfo {
	out := make([]SheetInfo, len(b.refs))
	for i, ref := range b.refs {
		out[i] = SheetInfo{Name: ref.name, Hidden: ref.hidden}
	}
	return out
}

// Read reads the sheet with the given index into a part.
func (b *Book) Read(ctx context.Context, index int) *unit.Result {
	if index == b.storedIndex() {
		return b.readStored(ctx)
	}
	if len(b.refs) == 0 {
		// A workbook with no sheets, or whose list of them could not be
		// read. A document has at least one part, and this one says why
		// it is empty.
		res := &unit.Result{Part: gofastcore.Part{Kind: gofastcore.PartSheet, Number: 1}}
		res.Part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "the workbook lists no sheets")
		res.Part.Finish()
		return res
	}
	return b.wb.sheet(ctx, index, b.refs[index], b.budget).result()
}

// result is what reading the sheet gave.
func (sh *sheet) result() *unit.Result {
	return &unit.Result{Part: sh.part, Tally: unit.Tally{
		Unread: sh.unread, Damaged: sh.damaged, Macros: sh.macros, Rels: append(sh.list[:len(sh.list):len(sh.list)], sh.charts...),
		Limited: sh.budget || sh.cut != "" && sh.cut != "cancelled",
	}}
}

// Summary turns what was found while the sheets were read into warnings
// about the workbook.
func (b *Book) Summary(tallies []*unit.Tally) []gofastcore.Warning {
	var out []gofastcore.Warning
	add := func(severity gofastcore.Severity, code, message string) {
		out = append(out, gofastcore.Warning{Code: code, Severity: severity, Message: message})
	}
	if b.stringsFull {
		add(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the shared strings are cut off at 4 GiB; cells that use later ones are empty")
	}
	unread := map[string]int{}
	damaged := append([]string(nil), b.damaged...)
	macros := false
	for _, t := range tallies {
		for kind, n := range t.Unread {
			unread[kind] += n
		}
		damaged = append(damaged, t.Damaged...)
		macros = macros || t.Macros
	}
	if macros {
		add(gofastcore.SeverityInfo, gofastcore.WarnInputActiveContent, "macro sheets (Excel 4.0 macros); what their cells show is read, the macros are not")
	}
	if b.external > 0 {
		add(gofastcore.SeverityWarning, gofastcore.WarnInputExternalReference,
			fmt.Sprintf("%d links to other workbooks or files; nothing was fetched", b.external))
	}
	if len(unread) > 0 {
		kinds := make([]string, 0, len(unread))
		for kind, n := range unread {
			kinds = append(kinds, strconv.Itoa(n)+" "+kind)
		}
		sort.Strings(kinds)
		add(gofastcore.SeverityWarning, warn.UnreadContent, strings.Join(kinds, ", "))
	}
	sort.Strings(damaged)
	for i, name := range damaged {
		if i == 0 || name != damaged[i-1] {
			add(gofastcore.SeverityWarning, warn.PartDamaged, clean.Label(b.wb.pkg.Written(name)))
		}
	}
	if b.removed > 0 {
		add(gofastcore.SeverityInfo, warn.ControlCharsRemoved, strconv.Itoa(b.removed)+" in shared strings")
	}
	if b.budget && len(tallies) == 0 {
		add(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the decompressed size limit was reached before any sheet was read")
	}
	return out
}

// sheet reads one sheet into a part.
func (wb *workbook) sheet(ctx context.Context, index int, ref sheetRef, budget bool) *sheet {
	sh, readable := wb.start(ctx, index, ref, budget)
	sawImage := false
	switch {
	case readable:
		sawImage = sh.content()
	case sh.chartSheet:
		sawImage = sh.chartContent()
	}
	sh.limits()
	sh.finish(sawImage)
	return sh
}

// start sets up the reading of a sheet: its part, and its relationships.
// readable is false for a sheet with no cells to read: one that is
// missing, a chart, a dialog, or one the limit was reached before.
func (wb *workbook) start(ctx context.Context, index int, ref sheetRef, budget bool) (sh *sheet, readable bool) {
	sh = &sheet{wb: wb, ctx: ctx, name: ref.target, budget: budget, unread: map[string]int{}}
	sh.room = gofastcore.TextBudgetFrom(ctx)
	sh.shapes.Budget, sh.shapes.Cost = sh.room, gofastcore.BlockCost
	part := &sh.part
	part.Kind, part.Number, part.Name = gofastcore.PartSheet, index+1, ref.name
	if ref.hidden {
		part.Display = gofastcore.DisplayHidden
	}
	switch {
	case ref.target == "" || !wb.pkg.Has(ref.target):
		part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "the workbook lists the sheet but does not contain it")
	case sh.budget:
		part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "nothing was read before the limit")
	default:
		rels, err := wb.pkg.Rels(ref.target)
		sh.noteErr(opc.RelsName(ref.target), err)
		sh.list = rels
		sh.rels = make(map[string]opc.Rel, len(rels))
		for _, rel := range rels {
			sh.rels[rel.ID] = rel
		}
		switch ref.kind {
		case ns.RelChartsheet:
			// A sheet that is a chart: what is on it is read from its
			// drawing.
			sh.chartSheet = true
		case ns.RelMacrosheet, ns.RelIntlMacrosheet:
			// A sheet of macros in the language Excel had before VBA. It
			// is a sheet of cells like any other: what the cells show is
			// read, and the macros in their formulas are not.
			sh.macros = true
			return sh, true
		case ns.RelDialogsheet:
			sh.unread["dialog sheet"]++
		default:
			return sh, true
		}
	}
	return sh, false
}

// limits says on the part which limits cut the sheet short, if any did.
func (sh *sheet) limits() {
	if sh.budget {
		sh.part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the decompressed size limit was reached; the sheet is cut off there")
	}
	if sh.cut != "" && sh.cut != "cancelled" && sh.cut != "stopped" {
		sh.part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, sh.cut+"; the sheet is cut off there")
	}
}

// finish completes the part: what it lacks, what was found while its
// cells were read, and which of its text is hidden.
func (sh *sheet) finish(sawImage bool) {
	part := &sh.part
	hasText, hasImage := false, sawImage
	for i := range part.Blocks {
		if part.Blocks[i].Kind == gofastcore.KindImage {
			hasImage = true
		} else {
			hasText = true
		}
	}
	if !hasText && !part.Has(gofastcore.WarnPartFailed) {
		if hasImage {
			part.Warn(gofastcore.SeverityWarning, gofastcore.WarnPartImageOnly, "")
		} else {
			part.Warn(gofastcore.SeverityInfo, gofastcore.WarnPartNoText, "")
		}
	}
	if sh.noValue > 0 {
		part.Warn(gofastcore.SeverityInfo, warn.FormulaNoValue, strconv.Itoa(sh.noValue)+" formulas are stored without a result; their cells are empty")
	}
	if sh.split > 0 {
		part.Warn(gofastcore.SeverityInfo, warn.RegionSplit, strconv.Itoa(sh.split)+" areas too thinly filled to be tables were written a row at a time")
	}
	if sh.removed > 0 {
		part.Warn(gofastcore.SeverityInfo, warn.ControlCharsRemoved, strconv.Itoa(sh.removed))
	}
	if sh.unmapped > 0 {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnTextUnmappedGlyphs, warn.Unmapped(sh.unmapped))
	}
	part.Finish()
}

// content reads a worksheet and everything that hangs on it into the
// part's blocks. It reports whether the sheet has pictures that were not
// asked for.
func (sh *sheet) content() (sawImage bool) {
	c, err := sh.wb.pkg.ScanPart(sh.name, &sh.s, scanOptions, opc.Spare(sh.wb.opts.Workers))
	if err != nil {
		sh.failed(err)
		return false
	}
	sh.read()
	c.Close()
	sh.failed(sh.s.Err())
	sh.s = xmlscan.Scanner{} // its window is no longer needed

	declared := sh.declared()
	blocks := sh.tablesOf(declared)
	blocks = append(blocks, sh.comments()...)
	blocks = append(blocks, sh.choices()...)
	for _, target := range sh.drawings {
		var saw bool
		blocks, saw = sh.drawing(target, blocks)
		sawImage = sawImage || saw
	}
	sh.part.Blocks = sh.arrange(blocks)
	// The tables hold the text now; the list of cells is not needed while
	// the other sheets are read.
	sh.cells, sh.raw, sh.out = nil, nil, nil
	return sawImage
}

// chartContent reads a chart sheet: the drawing it holds, which has the
// chart, and anything drawn beside it.
func (sh *sheet) chartContent() (sawImage bool) {
	var blocks []placed
	for _, rel := range sh.list {
		if rel.External || !ns.IsRel(rel.Type, ns.RelDrawing) || len(blocks) > 1<<16 {
			continue
		}
		var saw bool
		blocks, saw = sh.drawing(rel.Target, blocks)
		sawImage = sawImage || saw
	}
	// A chart sheet has no cells: its blocks have no place on one.
	for i := range blocks {
		blocks[i].block.Loc.Range = ""
	}
	sh.part.Blocks = sh.arrange(blocks)
	return sawImage
}

// tablesOf turns the cells read so far into tables.
func (sh *sheet) tablesOf(declared []declaredTable) []placed {
	sh.order()
	sh.sortDetails()
	return sh.regions(declared)
}

// arrange puts blocks in the order of the cells they sit at; where two
// sit at one cell, tables first, then notes, then what floats over the
// sheet.
func (sh *sheet) arrange(blocks []placed) []gofastcore.Block {
	for i := range blocks {
		blocks[i].seq = i
	}
	sort.Slice(blocks, func(i, j int) bool {
		a, b := &blocks[i], &blocks[j]
		if a.row != b.row {
			return a.row < b.row
		}
		if a.col != b.col {
			return a.col < b.col
		}
		return a.seq < b.seq
	})
	out := make([]gofastcore.Block, len(blocks))
	for i := range blocks {
		out[i] = blocks[i].block
		// A later piece of a table split by rows continues the piece
		// before it, and says so if that is still the block before it:
		// another block may lie beside the table and come between.
		if blocks[i].piece && i > 0 && blocks[i-1].seq == blocks[i].seq-1 {
			out[i].Continued = true
		}
	}
	return out
}

// failed records why reading the sheet's own part stopped, if it did.
func (sh *sheet) failed(err error) {
	switch {
	case err == nil:
	case errors.Is(err, opc.ErrBudget):
		sh.budget = true
	case errors.Is(err, xmlscan.ErrDepth), errors.Is(err, xmlscan.ErrTokenTooLarge):
		sh.part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the sheet is cut off: "+clean.Label(err.Error()))
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
	default:
		sh.part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "the sheet is cut off: "+clean.Label(err.Error()))
	}
}

// noteErr records a failure to read a part that supports the sheet.
func (sh *sheet) noteErr(name string, err error) {
	switch {
	case err == nil:
	case errors.Is(err, opc.ErrBudget):
		sh.budget = true
	case errors.Is(err, warn.ErrText):
		// The text of a shape went over what the document may hold.
		if sh.cut == "" {
			sh.cut = "the limit on the amount of text was reached"
		}
	default:
		sh.damaged = append(sh.damaged, name)
	}
}

// load reads a part that supports the sheet.
func (sh *sheet) load(name string, read func(*xmlscan.Scanner)) {
	if sh.budget || !sh.wb.pkg.Has(name) {
		return
	}
	var s xmlscan.Scanner
	c, err := sh.wb.pkg.Scan(name, &s, scanOptions)
	if err != nil {
		sh.noteErr(name, err)
		return
	}
	read(&s)
	c.Close()
	sh.noteErr(name, s.Err())
}

// image returns the block of a picture. Its bytes, if asked for, are
// read once; which of the blocks that show it carries them is settled
// when the sheets are put together.
//
// The error is opc.ErrBudget if the picture could not be read within the
// decompression budget.
func (wb *workbook) image(name, alt string) (gofastcore.Block, error) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	img, ok := wb.images[name]
	var err error
	if !ok {
		img, err = media.Image(wb.pkg, name, wb.opts.Images)
		if err == nil {
			wb.images[name] = img // one that was cut short is read again
		}
	}
	copy := *img
	return gofastcore.Block{Kind: gofastcore.KindImage, Text: alt, Image: &copy}, err
}
