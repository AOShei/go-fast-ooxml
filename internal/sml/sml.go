// Package sml reads Excel workbooks (SpreadsheetML) into the shared
// document model: one part per sheet, the cells as tables, with what each
// cell displays rather than what it stores.
package sml

import (
	"context"
	"errors"
	"fmt"
	"runtime"
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
	"github.com/AOShei/go-fast-ooxml/internal/theme"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// HiddenChecked is the set of hidden-text reasons this reader checks
// completely: hidden sheets, rows and columns and values a format blanks
// (invisible), fonts under two points (tiny), text in the colour of its
// cell (low_contrast), and invisible characters. Where conditional
// formatting applies, the colours a cell shows depend on its value and
// are not worked out: text there is not judged against its fill.
const HiddenChecked = gofastcore.HiddenInvisible | gofastcore.HiddenTiny | gofastcore.HiddenLowContrast | gofastcore.HiddenChars

// Config holds the limits that are this format's own.
type Config struct {
	// MaxCells bounds the cells kept from a workbook, all sheets
	// together.
	MaxCells int64
	// MaxTextBytes bounds the text of those cells. A cell's text is
	// counted wherever it is used, so a long string that a million cells
	// refer to counts a million times: it would be written a million
	// times by whatever uses the result.
	MaxTextBytes int64
}

var scanOptions = &xmlscan.Options{Spaces: ns.Spaces}

// workbook is what the sheets of a workbook share. Apart from the
// budgets and the record of pictures, nothing in it changes once the
// sheets are being read.
type workbook struct {
	ctx      context.Context
	pkg      *opc.Package
	opts     *gofastcore.Options
	strings  *stringTable
	styles   *styleSheet
	theme    *theme.Scheme
	date1904 bool
	persons  map[string]string // authors of threaded comments, by id

	cells atomic.Int64 // cells that may still be kept
	text  atomic.Int64 // bytes of cell text that may still be kept

	mu        sync.Mutex
	images    map[string]*gofastcore.Image
	delivered map[string]bool
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

// Extract reads the workbook in main into doc. The error is one of the
// caller's making: no sheet selected, or the context cancelled.
func Extract(ctx context.Context, pkg *opc.Package, main string, opts *gofastcore.Options, doc *gofastcore.Document, cfg Config) error {
	wb := &workbook{ctx: ctx, pkg: pkg, opts: opts, images: map[string]*gofastcore.Image{}, delivered: map[string]bool{}}
	wb.cells.Store(cfg.MaxCells)
	wb.text.Store(cfg.MaxTextBytes)
	doc.HiddenChecked = HiddenChecked

	var damaged []string
	budget := false
	note := func(name string, err error) {
		switch {
		case err == nil:
		case errors.Is(err, opc.ErrBudget):
			budget = true
		default:
			damaged = append(damaged, name)
		}
	}
	load := func(name string, read func(*xmlscan.Scanner)) {
		if budget || !pkg.Has(name) {
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
	var refs []sheetRef
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
			case s.Depth() == 3 && s.Is(ns.Sheet, "sheet") && len(refs) < maxSheets:
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
				refs = append(refs, ref)
			}
		}
	})

	// The sheets asked for, in workbook order. A sheet's number is its
	// place in the workbook, hidden sheets included.
	var selected []int
	for i := range refs {
		if opts.Parts.Contains(i + 1) {
			selected = append(selected, i)
		}
	}
	if len(refs) == 0 {
		// A workbook with no sheets, or whose list of them could not be
		// read. A document has at least one part, and this one says why
		// it is empty.
		part := gofastcore.Part{Kind: gofastcore.PartSheet, Number: 1}
		part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "the workbook lists no sheets")
		part.MarkHidden()
		doc.Parts = append(doc.Parts, part)
	}
	if len(refs) > 0 && len(selected) == 0 {
		return fmt.Errorf("gofastooxml: the workbook has %d sheets, numbered from 1: %w", len(refs), gofastcore.ErrNoPartsSelected)
	}
	if limit := opts.Limits.MaxParts; limit > 0 && len(selected) > limit {
		doc.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			fmt.Sprintf("%d sheets were asked for; the first %d are given", len(selected), limit))
		selected = selected[:limit]
	}

	// What the sheets share.
	external := 0
	for _, rel := range rels {
		switch {
		case ns.IsRel(rel.Type, ns.RelExternalLink) || rel.External && !ns.IsRel(rel.Type, ns.RelHyperlink):
			external++
		case rel.External || len(selected) == 0:
		case ns.IsRel(rel.Type, ns.RelTheme):
			load(rel.Target, func(s *xmlscan.Scanner) { wb.theme = theme.Read(s) })
		case ns.IsRel(rel.Type, ns.RelPerson):
			load(rel.Target, func(s *xmlscan.Scanner) { wb.persons = readPersons(s) })
		}
	}
	removed := 0
	for _, rel := range rels {
		if rel.External || len(selected) == 0 {
			continue
		}
		switch {
		case ns.IsRel(rel.Type, ns.RelStyles):
			load(rel.Target, func(s *xmlscan.Scanner) { wb.styles = readStyles(s, wb.theme) })
		case ns.IsRel(rel.Type, ns.RelSharedStrings):
			load(rel.Target, func(s *xmlscan.Scanner) {
				var full bool
				var n int
				wb.strings, n, full = readStrings(s)
				removed += n
				if full {
					doc.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the shared strings are cut off at 4 GiB; cells that use later ones are empty")
				}
			})
		}
	}

	// The sheets, each by one worker.
	sheets := make([]*sheet, len(selected))
	workers := opts.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	workers = min(workers, len(selected))
	if workers <= 1 {
		for k, i := range selected {
			sheets[k] = wb.sheet(i, refs[i], budget)
		}
	} else {
		var wg sync.WaitGroup
		var next atomic.Int64
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					k := int(next.Add(1)) - 1
					if k >= len(selected) {
						return
					}
					sheets[k] = wb.sheet(selected[k], refs[selected[k]], budget)
				}
			}()
		}
		wg.Wait()
	}

	// Put together in workbook order, so that the result does not depend
	// on how many workers there were.
	unread := map[string]int{}
	embedded := map[string]bool{}
	macros := false
	for _, sh := range sheets {
		doc.Parts = append(doc.Parts, sh.part)
		for kind, n := range sh.unread {
			unread[kind] += n
		}
		damaged = append(damaged, sh.damaged...)
		budget = budget || sh.budget
		macros = macros || sh.macros
		children, bad, err := media.Embedded(ctx, pkg, opts, sh.list, embedded, !budget)
		doc.Children = append(doc.Children, children...)
		damaged = append(damaged, bad...)
		note("", err)
	}
	if macros {
		doc.Warn(gofastcore.SeverityInfo, gofastcore.WarnInputActiveContent, "macro sheets (Excel 4.0 macros); what their cells show is read, the macros are not")
	}
	if external > 0 {
		doc.Warn(gofastcore.SeverityWarning, gofastcore.WarnInputExternalReference,
			fmt.Sprintf("%d links to other workbooks or files; nothing was fetched", external))
	}
	if len(unread) > 0 {
		kinds := make([]string, 0, len(unread))
		for kind, n := range unread {
			kinds = append(kinds, strconv.Itoa(n)+" "+kind)
		}
		sort.Strings(kinds)
		doc.Warn(gofastcore.SeverityWarning, warn.UnreadContent, strings.Join(kinds, ", "))
	}
	sort.Strings(damaged)
	for i, name := range damaged {
		if i == 0 || name != damaged[i-1] {
			doc.Warn(gofastcore.SeverityWarning, warn.PartDamaged, clean.Label(name))
		}
	}
	if removed > 0 {
		doc.Warn(gofastcore.SeverityInfo, warn.ControlCharsRemoved, strconv.Itoa(removed)+" in shared strings")
	}
	if budget && len(sheets) == 0 {
		doc.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the decompressed size limit was reached before any sheet was read")
	}
	return nil
}

// sheet reads one sheet into a part.
func (wb *workbook) sheet(index int, ref sheetRef, budget bool) *sheet {
	sh := &sheet{wb: wb, name: ref.target, budget: budget, unread: map[string]int{}}
	part := &sh.part
	part.Kind, part.Number, part.Name = gofastcore.PartSheet, index+1, ref.name
	if ref.hidden {
		part.Hidden = gofastcore.HiddenInvisible
	}
	sawImage := false
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
			sh.unread["chart"]++
		case ns.RelMacrosheet, ns.RelIntlMacrosheet:
			// A sheet of macros in the language Excel had before VBA. It
			// is a sheet of cells like any other: what the cells show is
			// read, and the macros in their formulas are not.
			sh.macros = true
			sawImage = sh.content()
		case ns.RelDialogsheet:
			sh.unread["dialog sheet"]++
		default:
			sawImage = sh.content()
		}
	}

	if sh.budget {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the decompressed size limit was reached; the sheet is cut off there")
	}
	if sh.cut != "" && sh.cut != "cancelled" {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, sh.cut+"; the sheet is cut off there")
	}
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
	part.MarkHidden()
	return sh
}

// content reads a worksheet and everything that hangs on it into the
// part's blocks. It reports whether the sheet has pictures that were not
// asked for.
func (sh *sheet) content() (sawImage bool) {
	c, err := sh.wb.pkg.Scan(sh.name, &sh.s, scanOptions)
	if err != nil {
		sh.failed(err)
		return false
	}
	sh.read()
	c.Close()
	sh.failed(sh.s.Err())
	sh.s = xmlscan.Scanner{} // its window is no longer needed

	sh.order()
	sh.unjudge()
	blocks := sh.regions(sh.declared())
	blocks = append(blocks, sh.comments()...)
	for _, target := range sh.drawings {
		var saw bool
		blocks, saw = sh.drawing(target, blocks)
		sawImage = sawImage || saw
	}
	// In the order of the cells they sit at; where two sit at one cell,
	// tables first, then notes, then what floats over the sheet.
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
	sh.part.Blocks = make([]gofastcore.Block, len(blocks))
	for i := range blocks {
		sh.part.Blocks[i] = blocks[i].block
	}
	// The tables hold the text now; the list of cells is not needed while
	// the other sheets are read.
	sh.cells, sh.raw, sh.out = nil, nil, nil
	return sawImage
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
// given the first time the workbook shows it.
func (wb *workbook) image(name, alt string) gofastcore.Block {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	img, ok := wb.images[name]
	if !ok {
		img, _ = media.Image(wb.pkg, name, wb.opts.Images)
		wb.images[name] = img
	}
	copy := *img
	if wb.delivered[name] {
		copy.Data = nil
	}
	wb.delivered[name] = true
	return gofastcore.Block{Kind: gofastcore.KindImage, Text: alt, Image: &copy}
}
