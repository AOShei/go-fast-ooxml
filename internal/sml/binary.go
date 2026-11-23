package sml

import (
	"bytes"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/symbol"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
	"github.com/AOShei/go-fast-ooxml/internal/xlsb"
)

// An Excel binary workbook (.xlsb) is the package of an .xlsx file whose
// workbook, sheets, shared strings, styles, comments and tables are
// records ([MS-XLSB]) in place of XML; its theme, drawings and charts are
// XML as they are in .xlsx. The records are read into what the XML gives:
// the same cells, styles, merged cells, hidden rows and columns, links,
// comments, drop-down lists and tables, so that formats, dates, tables,
// display reasons and limits are worked out by the same code for both.
// Formulas are compiled in this format, and are turned back into the text
// Excel shows (xlsb.Formula); one that cannot be is left out and counted.

// unreadFormula is the kind under which formulas not read back are
// counted.
const unreadFormula = "binary formulas not read back"

// binary reports whether a part of a workbook is in records rather than
// XML: Excel's binary parts are declared with a content type of its own
// that does not end in +xml. A part with no content type is taken by its
// name.
func binary(pkg *opc.Package, name string) bool {
	ct := pkg.ContentType(name)
	if ct == "" {
		return strings.HasSuffix(strings.ToLower(name), ".bin")
	}
	return strings.HasPrefix(ct, "application/vnd.ms-excel.") && !strings.HasSuffix(ct, "+xml")
}

// records reads a binary part of the workbook a record at a time, the
// decompression budget charged as it is read.
func records(pkg *opc.Package, name string, read func(r *xlsb.Reader)) error {
	rc, err := pkg.Open(name)
	if err != nil {
		return err
	}
	defer rc.Close()
	r := xlsb.NewReader(rc)
	read(r)
	return r.Err()
}

// plainText is cellText for text read from records: there is nothing to
// unescape, and "_x000D_" in such text is those seven characters.
func plainText(dst, raw []byte) ([]byte, int) {
	start := len(dst)
	dst, removed := clean.Append(dst, raw, clean.Ligatures)
	trimmed := bytes.TrimSpace(dst[start:])
	n := copy(dst[start:], trimmed)
	return dst[:start+n], removed
}

// textOf is cellText or plainText, as the sheet is XML or records.
func (sh *sheet) textOf(dst, raw []byte) ([]byte, int) {
	if sh.bin {
		return plainText(dst, raw)
	}
	return cellText(dst, raw)
}

// binName is a defined name of a binary workbook, whose formula is read
// once all the names and sheets it may refer to are known.
type binName struct {
	name        string
	sheet       int32 // the sheet it is local to, or -1
	rgce, extra []byte
}

// readBinaryBook reads the workbook part of a binary workbook: the dates'
// epoch, the sheets, the defined names, and what formulas need to read
// their references to sheets.
func (b *Book) readBinaryBook(r *xlsb.Reader, byID map[string]opc.Rel) []binName {
	wb := b.wb
	fb := &xlsb.Book{}
	wb.formulas = fb
	var names []binName
	var buf []byte
	books := 0 // links to other files
	for r.Next() {
		f := xlsb.NewFields(r.Data())
		switch r.ID() {
		case xlsb.WbProp:
			wb.date1904 = f.U32()&1 != 0
		case xlsb.BundleSh:
			if len(b.refs) >= maxSheets {
				b.docLost.Add(warn.CapSheets, 1)
				continue
			}
			state := f.U32()
			f.U32() // the sheet's id
			buf = f.String(buf[:0])
			id := string(buf)
			buf = f.String(buf[:0])
			ref := sheetRef{hidden: state == 1 || state == 2}
			ref.name, _ = clean.String(buf, clean.Prose)
			if rel, found := byID[id]; found && !rel.External {
				ref.target = rel.Target
				if i := strings.LastIndexByte(rel.Type, '/'); i >= 0 {
					ref.kind = rel.Type[i+1:]
				}
			}
			b.refs = append(b.refs, ref)
			b.macroSheets = b.macroSheets || ref.kind == ns.RelMacrosheet || ref.kind == ns.RelIntlMacrosheet
		case xlsb.Name:
			if len(names) >= maxNames {
				continue
			}
			flags := f.U32()
			f.U8() // the keyboard shortcut of a macro
			sheet := f.I32()
			buf = f.String(buf[:0])
			name := b.lost.Name(string(buf))
			if flags&0x20 != 0 && name != "" {
				name = "_xlnm." + name // a name Excel gives a meaning to: Print_Area
			}
			rgce, extra := f.Formula()
			names = append(names, binName{name: name, sheet: sheet, rgce: bytes.Clone(rgce), extra: bytes.Clone(extra)})
			fb.Names = append(fb.Names, name)
		case xlsb.SupSelf, xlsb.SupSame, xlsb.SupAddin, xlsb.SupBookSrc:
			if len(fb.Links) >= maxLinksOut {
				b.docLost.Add(warn.CapExternalLinks, 1)
				continue
			}
			l := xlsb.Link{Kind: xlsb.LinkSelf}
			switch r.ID() {
			case xlsb.SupSame:
				l.Kind = xlsb.LinkSame
			case xlsb.SupAddin:
				l.Kind = xlsb.LinkAddin
			case xlsb.SupBookSrc:
				// Another file, read from its part once the workbook is.
				books++
				l = xlsb.Link{Kind: xlsb.LinkOther, Book: books}
				buf = f.String(buf[:0])
				if rel, found := byID[string(buf)]; found && !rel.External && ns.IsRel(rel.Type, ns.RelExternalLink) && binary(b.wb.pkg, rel.Target) {
					b.linkParts = append(b.linkParts, linkPart{index: len(fb.Links), target: rel.Target})
				}
			}
			fb.Links = append(fb.Links, l)
		case xlsb.PlaceholderName:
			// A name used through the link before it.
			if n := len(fb.Links); n > 0 && fb.Links[n-1].Kind != xlsb.LinkOther {
				if len(fb.Links[n-1].Names) >= maxExternNames {
					b.docLost.Add(warn.CapExternalNames, 1)
					continue
				}
				buf = f.String(buf[:0])
				fb.Links[n-1].Names = append(fb.Links[n-1].Names, xlsb.ExternName{Name: b.docLost.Name(string(buf))})
			}
		case xlsb.ExternSheet:
			n := f.U32()
			for i := uint32(0); i < n && f.Left() >= 12; i++ {
				if len(fb.XTI) >= maxSheetRefs {
					b.docLost.Add(warn.CapSheetRefs, int64(n-i))
					break
				}
				fb.XTI = append(fb.XTI, xlsb.XTI{Link: f.I32(), First: f.I32(), Last: f.I32()})
			}
		case xlsb.PivotCacheID:
			f.U32() // the number pivot tables know it by
			buf = f.String(buf[:0])
			if rel, found := byID[string(buf)]; found && !rel.External && ns.IsRel(rel.Type, relPivotCache) && len(b.caches) < maxCaches {
				b.caches = append(b.caches, pivotCache{target: rel.Target})
			}
		}
	}
	for _, ref := range b.refs {
		fb.Sheets = append(fb.Sheets, ref.name)
	}
	fb.Tables = sync.OnceValue(b.binaryTables)
	return names
}

// Bounds on what a binary workbook's links give formulas: the links, and
// the sheets and names of each.
const (
	maxLinksOut    = warn.MaxExternalLinks
	maxExternNames = warn.MaxExternalNames
	maxSheetRefs   = warn.MaxSheetRefs
)

// linkPart is a link to another file whose part says what it is.
type linkPart struct {
	index  int // in the formulas' links
	target string
}

// readLink reads the part of a link to another file (External Link) into
// what formulas take from it: whether it is a workbook, and the sheets and
// names of one.
func readLink(r *xlsb.Reader, l *xlsb.Link, lost *warn.Lost) {
	var buf []byte
	for r.Next() {
		f := xlsb.NewFields(r.Data())
		switch r.ID() {
		case xlsb.BeginSupBook:
			if f.U16() == 0 && !f.Short {
				l.Kind = xlsb.LinkBook // not a DDE or OLE link
			}
		case xlsb.SupTabs:
			n := f.U32()
			for i := uint32(0); i < n && !f.Short; i++ {
				if len(l.Sheets) >= maxSheets {
					lost.Add(warn.CapExternalSheets, int64(n-i))
					break
				}
				buf = f.String(buf[:0])
				name, _ := clean.String(buf, clean.Prose)
				l.Sheets = append(l.Sheets, name)
			}
		case xlsb.SupNameStart:
			if len(l.Names) < maxExternNames {
				buf = f.String(buf[:0])
				l.Names = append(l.Names, xlsb.ExternName{Name: lost.Name(string(buf))})
			} else {
				lost.Add(warn.CapExternalNames, 1)
			}
		case xlsb.SupNameBits:
			// The sheet the name before is local to (ExternalNameProperties).
			f.U16()
			sheet := f.I32()
			if n := len(l.Names); n > 0 && !f.Short && sheet >= 0 {
				l.Names[n-1].Sheet = sheet
			}
		}
	}
}

// maxBookTables bounds the tables a binary workbook's formulas may name.
const maxBookTables = warn.MaxBookTables

// binaryTables reads the tables the sheets of a binary workbook declare,
// for formulas that name their columns: their numbers, names and columns.
func (b *Book) binaryTables() map[uint32]xlsb.Table {
	out := map[uint32]xlsb.Table{}
	pkg := b.wb.pkg
	for _, ref := range b.refs {
		if ref.target == "" || !pkg.Has(ref.target) {
			continue
		}
		rels, _ := pkg.Rels(ref.target)
		for _, rel := range rels {
			if rel.External || !ns.IsRel(rel.Type, ns.RelTable) || !binary(pkg, rel.Target) || !pkg.Has(rel.Target) {
				continue
			}
			if len(out) >= maxBookTables {
				b.docLost.Add(warn.CapBookTables, 1)
				continue
			}
			// A part that cannot be read to its end gives what it has; its
			// sheet reports it.
			records(pkg, rel.Target, func(r *xlsb.Reader) {
				var t xlsb.Table
				id, in := uint32(0), false
				var buf []byte
				for r.Next() && len(out) < maxBookTables {
					f := xlsb.NewFields(r.Data())
					switch r.ID() {
					case xlsb.BeginList:
						f.Skip(16) // where it is
						f.U32()    // its kind
						id = f.U32()
						f.Skip(40) // its header and total rows, flags, formats, connection
						buf = f.String(buf[:0])
						t = xlsb.Table{Name: b.docLost.Name(string(buf))}
						buf = f.String(buf[:0])
						if display := b.docLost.Name(string(buf)); display != "" {
							t.Name = display
						}
						in = !f.Short
					case xlsb.BeginListCol:
						if in && len(t.Columns) >= maxCols {
							b.docLost.Add(warn.CapTableColumns, 1)
						}
						if in && len(t.Columns) < maxCols {
							f.Skip(24) // its number, total, formats and query field
							buf = f.String(buf[:0])
							name, _ := clean.String(buf, clean.Prose)
							t.Columns = append(t.Columns, name)
						}
					case xlsb.EndList:
						if in {
							if _, dup := out[id]; !dup {
								out[id] = t
							}
						}
						in = false
					}
				}
			})
		}
	}
	return out
}

// binaryNames reads the formulas of the defined names, as readNames does.
func (b *Book) binaryNames(names []binName) {
	for _, n := range names {
		if n.name == "" || len(n.rgce) == 0 {
			continue // a name with nothing behind it: a function newer than the format
		}
		text, ok := xlsb.Formula(n.rgce, n.extra, b.wb.formulas, xlsb.Place{})
		if !ok {
			b.unreadNames++
			continue
		}
		f := strings.TrimSpace(verbatim([]byte(text), &b.lost))
		if f == "" {
			continue // a formula of nothing: as readNames leaves it out
		}
		b.names = append(b.names, definedName{name: n.name, formula: f})
		if n.sheet >= 0 {
			b.localNames = append(b.localNames, localName{index: len(b.names) - 1, sheet: int(n.sheet)})
		}
	}
}

// readBinaryStyles reads a binary styles part: the number formats, the
// fonts as far as a symbol font goes, and the cell formats.
func readBinaryStyles(r *xlsb.Reader, lost *warn.Lost) *styleSheet {
	codes := map[int]string{}
	var fonts []symbol.Font
	var xfs []xf
	inFonts, inXFs := false, false
	var buf []byte
	for r.Next() {
		f := xlsb.NewFields(r.Data())
		switch r.ID() {
		case xlsb.Fmt:
			if len(codes) >= maxFormats {
				lost.Add(warn.CapNumberFormats, 1)
				continue
			}
			id := int(f.U16())
			buf = f.String(buf[:0])
			if !f.Short {
				codes[id], _ = clean.String(buf, clean.Ligatures)
			}
		case xlsb.BeginFonts:
			inFonts = true
		case xlsb.EndFonts:
			inFonts = false
		case xlsb.Font:
			if !inFonts {
				continue
			}
			if len(fonts) >= maxStyles {
				lost.Add(warn.CapCellStyles, 1)
				continue
			}
			f.Skip(21) // size, flags, weight, position, underline, family, character set, colour, scheme
			buf = f.String(buf[:0])
			fonts = append(fonts, symbol.ByName(string(buf)))
		case xlsb.BeginCellXFs:
			inXFs = true
		case xlsb.EndCellXFs:
			inXFs = false
		case xlsb.XF:
			if !inXFs {
				continue
			}
			if len(xfs) >= maxStyles {
				lost.Add(warn.CapCellStyles, 1)
				continue
			}
			f.U16() // the cell style it is based on
			format := int(f.U16())
			font := int(f.U16())
			xfs = append(xfs, xf{format, font})
		}
	}
	return buildStyles(codes, fonts, xfs)
}

// binaryRuns appends to runs the runs of a binary workbook's rich string,
// whose text is raw: where each begins in raw, counted in bytes, and its
// font from the workbook's fonts. Text before the first run is in the
// cell's font.
func binaryRuns(runs []fontRun, raw []byte, strRuns []xlsb.StrRun, st *styleSheet) []fontRun {
	if len(strRuns) == 0 {
		return runs
	}
	if strRuns[0].Ich > 0 {
		runs = append(runs, fontRun{})
	}
	unit, next := 0, 0
	for i := 0; i <= len(raw) && next < len(strRuns); {
		if unit >= int(strRuns[next].Ich) {
			runs = append(runs, fontRun{at: uint32(i), font: st.font(int(strRuns[next].Font)), named: true})
			next++
			continue
		}
		if i == len(raw) {
			break
		}
		_, size := utf8.DecodeRune(raw[i:])
		if size == 4 {
			unit++ // a character outside the first plane is two units
		}
		unit++
		i += size
	}
	return runs
}

// readBinaryStrings reads a binary shared-strings part, as readStrings
// does an XML one; st gives the fonts its runs name.
func readBinaryStrings(r *xlsb.Reader, st *styleSheet) (t *stringTable, removed int, full bool) {
	t = &stringTable{}
	var all strings.Builder
	var raw, cleaned []byte
	var strRuns []xlsb.StrRun
	var runs []fontRun
	symbolic := st.hasSymbolFont()
	for r.Next() {
		if r.ID() != xlsb.SSTItem {
			continue
		}
		f := xlsb.NewFields(r.Data())
		raw, strRuns = f.RichStringRuns(raw[:0], strRuns[:0])
		var n int
		runs = binaryRuns(runs[:0], raw, strRuns, st)
		if fontsMatter(runs, symbolic) {
			cleaned, n = cleanRuns(cleaned[:0], raw, runs, false)
			if t.rich == nil {
				t.rich = map[uint32][]fontRun{}
			}
			t.rich[uint32(len(t.ends))] = append([]fontRun(nil), runs...)
		} else {
			cleaned, n = plainText(cleaned[:0], raw)
		}
		removed += n
		if int64(all.Len())+int64(len(cleaned)) > maxStringBytes {
			t.text = all.String()
			return t, removed, true
		}
		all.Write(cleaned)
		t.ends = append(t.ends, uint32(all.Len()))
	}
	t.text = all.String()
	return t, removed, false
}

// relTarget returns the part a relationship id names, if it is of the
// given kind and stays inside the package.
func (sh *sheet) relTarget(id []byte, kind string) (string, bool) {
	rel, found := sh.rels[string(id)]
	if !found || rel.External || !ns.IsRel(rel.Type, kind) {
		return "", false
	}
	return rel.Target, true
}

// binRect reads a rectangle of cells, and reports whether it lies on the
// sheet.
func binRect(f *xlsb.Fields) (rect, bool) {
	r0, r1, c0, c1 := f.Range()
	if f.Short || r0 < 0 || r1 < 0 || c0 < 0 || c1 < 0 || r0 >= maxRows || r1 >= maxRows || c0 >= maxCols || c1 >= maxCols {
		return rect{}, false
	}
	return rect{min(r0, r1), min(c0, c1), max(r0, r1), max(c0, c1)}, true
}

// binFormula is a shared or array formula: written once, after the first
// cell of the range it is for, and referred to from that range's cells.
type binFormula struct {
	rgce, extra []byte
	array       bool // an array formula, whose text is given at its first cell only
}

// pendingFormula is a cell whose formula is the shared formula that the
// next record gives.
type pendingFormula struct {
	at   cell
	typ  byte
	raw  []byte
	date bool
	base pos
}

// readBin reads a binary worksheet part: readData's rows and cells, and
// what read takes from around them.
func (sh *sheet) readBin(r *xlsb.Reader) {
	inData := false
	row, col := int32(-1), int32(-1)
	rowHidden := false
	first := len(sh.cells)
	var textBytes int64
	ordered := true
	rows := 0
	// endRow settles the row read so far, as readData does after each
	// row: false if reading stops there.
	endRow := func() bool {
		if row < 0 {
			return true
		}
		if !ordered {
			sh.unsorted = true
		}
		n := len(sh.cells) - first
		if n == 0 {
			return true
		}
		if rowHidden {
			sh.hiddenRows = append(sh.hiddenRows, row)
		}
		if sh.band > 0 {
			if sh.inBand++; sh.inBand >= sh.band {
				sh.inBand = 0
				if !sh.emit() {
					sh.cut = "stopped"
					return false
				}
			}
			return true
		}
		if sh.wb.cells.Add(-int64(n)) < 0 {
			sh.cells = sh.cells[:first]
			sh.cut = "the limit on the number of cells was reached"
			return false
		}
		if sh.pending += textBytes; sh.pending > sh.room.Left() {
			sh.cells = sh.cells[:first]
			sh.cut = "the limit on the amount of text was reached"
			return false
		}
		return true
	}
	defer sh.settle(nil)
	var buf []byte
	for r.Next() {
		id := r.ID()
		f := xlsb.NewFields(r.Data())
		if sh.pend != nil && id != xlsb.ShrFmla && id != xlsb.ArrFmla {
			sh.settle(nil)
		}
		switch {
		case id == xlsb.BeginSheetData:
			inData = true
			continue
		case id == xlsb.EndSheetData:
			if inData && sh.pass != passAround && !endRow() {
				return
			}
			// The pass of the rows reads on past the end, as the sheet
			// read whole does: a damaged part may have its data in more
			// than one place, or an end before its beginning.
			inData, row = false, -1
			continue
		}
		if inData {
			if sh.pass == passAround {
				continue
			}
			switch id {
			case xlsb.RowHdr:
				if !endRow() {
					return
				}
				if rows++; rows&1023 == 0 && sh.ctx.Err() != nil {
					sh.cut = "cancelled"
					return
				}
				rw := f.I32()
				f.U32() // the row's style
				height := f.U16()
				flags := f.U16()
				row = -1
				if rw >= maxRows && !f.Short {
					sh.lost.Add(warn.CapSheetRows, 1)
				}
				if rw >= 0 && rw < maxRows && !f.Short {
					row = rw
					if row < sh.nextRow {
						sh.unsorted = true
					}
					sh.nextRow = row + 1
				}
				// A hidden row, or one of no height.
				rowHidden = flags&0x1000 != 0 || flags&0x2000 != 0 && height == 0
				first, textBytes, ordered, col = len(sh.cells), 0, true, -1
			case xlsb.ShrFmla, xlsb.ArrFmla:
				sh.binShared(id, &f)
			default:
				if row < 0 {
					continue
				}
				if n := len(sh.cells); sh.binCell(id, &f, row, &col) {
					textBytes += int64(len(sh.cells[n].text))
					if n > first && sh.cells[n].col <= sh.cells[n-1].col {
						ordered = false
					}
				}
			}
			continue
		}
		if sh.pass == passRows {
			continue
		}
		switch id {
		case xlsb.ColInfo:
			lo, hi := f.I32(), f.I32()
			width := f.I32()
			f.I32() // style
			flags := f.U16()
			hidden := flags&1 != 0 || width == 0
			if hidden && !f.Short && lo >= 0 && hi >= lo && lo < maxCols && len(sh.hiddenCols) < maxCols {
				sh.hiddenCols = append(sh.hiddenCols, span{lo, min(hi, maxCols-1)})
			}
		case xlsb.MergeCell:
			if r, ok := binRect(&f); ok && (r.r1 > r.r0 || r.c1 > r.c0) {
				if len(sh.merges) < maxMerges {
					sh.merges = append(sh.merges, r)
				} else {
					sh.lost.Add(warn.CapMerges, 1)
				}
			}
		case xlsb.HLink:
			if sh.wb.opts.Spans {
				sh.binLink(&f)
			}
		case xlsb.DVal:
			if sh.pass == passWhole {
				sh.binValidation(&f)
			}
		case xlsb.ListPart:
			buf = f.String(buf[:0])
			sh.addTable(sh.relTarget(buf, ns.RelTable))
		case xlsb.Drawing:
			buf = f.String(buf[:0])
			sh.addDrawing(sh.relTarget(buf, ns.RelDrawing))
		}
	}
	// A part that ends inside its data: the row read so far is kept, as
	// the XML reader keeps the rows before where a part breaks off.
	if inData && sh.pass != passAround {
		endRow()
	}
}

// binCell reads one cell record of a row, and reports whether the cell
// shows something; if it does, it is the last of sh.cells. col is the
// column of the cell before, which a short record follows.
func (sh *sheet) binCell(id int, f *xlsb.Fields, row int32, col *int32) bool {
	switch id {
	case xlsb.CellBlank, xlsb.CellRk, xlsb.CellError, xlsb.CellBool, xlsb.CellReal, xlsb.CellSt, xlsb.CellIsst,
		xlsb.FmlaString, xlsb.FmlaNum, xlsb.FmlaBool, xlsb.FmlaError, xlsb.CellRString:
		*col = f.I32()
	case xlsb.ShortBlank, xlsb.ShortRk, xlsb.ShortError, xlsb.ShortBool, xlsb.ShortReal, xlsb.ShortSt, xlsb.ShortIsst, xlsb.ShortRString:
		*col++
	default:
		return false // not a cell
	}
	style := sh.wb.styles.style(int(f.U32() & 0xFFFFFF))
	raw := sh.raw[:0]
	sh.runs = sh.runs[:0]
	typ := byte('n')
	switch id {
	case xlsb.CellBlank, xlsb.ShortBlank:
		return false
	case xlsb.CellRk, xlsb.ShortRk:
		raw = appendNumber(raw, xlsb.RK(f.U32()))
	case xlsb.CellReal, xlsb.ShortReal, xlsb.FmlaNum:
		raw = appendNumber(raw, f.F64())
	case xlsb.CellError, xlsb.ShortError, xlsb.FmlaError:
		raw = append(raw, xlsb.ErrorText(f.U8())...)
		typ = 'e'
	case xlsb.CellBool, xlsb.ShortBool, xlsb.FmlaBool:
		raw = append(raw, '0'+f.U8()&1)
		typ = 'b'
	case xlsb.CellSt, xlsb.ShortSt:
		raw = f.String(raw)
		typ = 'i'
	case xlsb.CellRString, xlsb.ShortRString:
		raw, sh.strRuns = f.RichStringRuns(raw, sh.strRuns[:0])
		sh.runs = binaryRuns(sh.runs[:0], raw, sh.strRuns, sh.wb.styles)
		typ = 'i'
	case xlsb.FmlaString:
		raw = f.String(raw)
		typ = 't'
	case xlsb.CellIsst, xlsb.ShortIsst:
		raw = strconv.AppendUint(raw, uint64(f.U32()), 10)
		typ = 's'
	}
	formula := id >= xlsb.FmlaString && id <= xlsb.FmlaError
	sh.raw = raw
	if !f.Short && *col >= maxCols && len(raw) > 0 {
		sh.lost.Add(warn.CapSheetCols, 1)
	}
	if f.Short || *col < 0 || *col >= maxCols || len(raw) == 0 {
		// A record cut short in a damaged file, whose value may be part
		// of one; a cell off the sheet; or a formula whose result is an
		// empty string.
		return false
	}
	sh.isDate = false
	text, hidden := sh.value(typ, style, raw)
	if text == "" {
		return false
	}
	c := cell{text: text, row: row, col: *col, hidden: hidden}
	sh.cells = append(sh.cells, c)
	if !sh.wb.opts.Cells {
		return true
	}
	ftext := ""
	if formula {
		f.U16() // flags
		rgce, extra := f.Formula()
		if exp, ok := xlsb.ExpOf(rgce, extra); ok {
			base := pos{exp.Row, exp.Col}
			if s, found := sh.formulasAt[base]; found {
				ftext = sh.sharedText(s, base, c)
			} else if base == (pos{row, *col}) {
				// The first cell of a shared formula, which the next
				// record gives.
				sh.pend = &pendingFormula{at: c, typ: typ, raw: bytes.Clone(raw), date: sh.isDate, base: base}
				return true
			} else {
				sh.unread[unreadFormula]++
			}
		} else {
			ftext = sh.formulaAt(rgce, extra, row, *col)
		}
	}
	sh.keepDetail(&c, typ, raw, ftext, sh.isDate)
	return true
}

// formulaAt reads a cell's formula, counting one that cannot be read
// back.
func (sh *sheet) formulaAt(rgce, extra []byte, row, col int32) string {
	text, ok := xlsb.Formula(rgce, extra, sh.wb.formulas, xlsb.Place{Row: row, Col: col})
	if !ok {
		sh.unread[unreadFormula]++
		return ""
	}
	return verbatim([]byte(text), &sh.lost)
}

// sharedText is the formula of cell c, which uses the shared formula s
// whose first cell is base. An array formula is given at that cell only.
func (sh *sheet) sharedText(s binFormula, base pos, c cell) string {
	if s.array && (c.row != base.row || c.col != base.col) {
		return ""
	}
	return sh.formulaAt(s.rgce, s.extra, c.row, c.col)
}

// binShared reads a shared or array formula, which follows the first
// cell of its range.
func (sh *sheet) binShared(id int, f *xlsb.Fields) {
	at, ok := binRect(f)
	if id == xlsb.ArrFmla {
		f.U8() // flags
	}
	rgce, extra := f.Formula()
	if !ok || f.Short || !sh.wb.opts.Cells {
		return
	}
	base := pos{at.r0, at.c0}
	s := binFormula{rgce: bytes.Clone(rgce), extra: bytes.Clone(extra), array: id == xlsb.ArrFmla}
	if sh.formulasAt == nil {
		sh.formulasAt = map[pos]binFormula{}
	}
	if len(sh.formulasAt) < maxShared {
		sh.formulasAt[base] = s
	} else {
		sh.lost.Add(warn.CapSharedFormulas, 1)
	}
	if p := sh.pend; p != nil && p.base == base {
		sh.settle(&s)
	}
}

// settle keeps the detail of the cell waiting for its shared formula,
// with s, the formula, or with none if the file did not give it.
func (sh *sheet) settle(s *binFormula) {
	p := sh.pend
	if p == nil {
		return
	}
	sh.pend = nil
	f := ""
	if s != nil {
		f = sh.sharedText(*s, p.base, p.at)
	} else {
		sh.unread[unreadFormula]++
	}
	sh.keepDetail(&p.at, p.typ, p.raw, f, p.date)
}

// appendNumber writes a number as an XML part holds it: the shortest
// text that reads back as the same number, written out in full unless it
// is very large or very small, and then with E, the exponent's sign and
// its digits with no zeros in front: 1E+20, 1.5E-7.
func appendNumber(dst []byte, v float64) []byte {
	if a := math.Abs(v); a == 0 || a >= 1e-5 && a < 1e15 {
		return strconv.AppendFloat(dst, v, 'f', -1, 64)
	}
	start := len(dst)
	dst = strconv.AppendFloat(dst, v, 'E', -1, 64)
	e := bytes.IndexByte(dst[start:], 'E') + start
	sign, digits := dst[e+1], bytes.TrimLeft(dst[e+2:], "0")
	dst = append(dst[:e+1], sign)
	return append(dst, digits...)
}

// binLink reads a hyperlink record, as readLinks does a hyperlink
// element.
func (sh *sheet) binLink(f *xlsb.Fields) {
	if len(sh.links)+len(sh.linkAt) >= maxLinks {
		sh.lost.Add(warn.CapLinks, 1)
		return
	}
	at, ok := binRect(f)
	if !ok {
		return
	}
	id := f.String(nil)
	location := f.String(nil)
	href := ""
	if rel, found := sh.rels[string(id)]; found && rel.External {
		href = rel.Target // cleaned when the relationships were read
	}
	if href == "" && len(location) > 0 {
		href = "#" + sh.lost.Address(string(location))
	}
	sh.addLink(at, href)
}

// binValidation reads a data validation record, as readValidation does a
// dataValidation element: only lists are kept.
func (sh *sheet) binValidation(f *xlsb.Fields) {
	flags := f.U32()
	if flags&0xF != 3 {
		return
	}
	if len(sh.lists) >= maxChoiceLists {
		sh.lost.Add(warn.CapChoiceLists, 1)
		return
	}
	v := choiceList{arrow: flags&0x200 == 0}
	n := f.U32()
	for i := uint32(0); i < n && !f.Short; i++ {
		r, ok := binRect(f)
		switch {
		case !ok:
		case sh.listRanges < maxChoiceRanges:
			v.ranges = append(v.ranges, r)
			sh.listRanges++
		default:
			sh.lost.Add(warn.CapChoiceRanges, 1)
		}
	}
	for range 4 { // the titles and messages of its prompt and its error
		f.String(nil)
	}
	rgce, extra := f.Formula()
	if f.Short || len(v.ranges) == 0 {
		return
	}
	// Its relative references count from the first cell of its ranges.
	text, ok := xlsb.Formula(rgce, extra, sh.wb.formulas, xlsb.Place{Row: v.ranges[0].r0, Col: v.ranges[0].c0})
	if !ok {
		sh.unread[unreadFormula]++
		return
	}
	v.source = text
	sh.lists = append(sh.lists, v)
}

// binDeclared reads a binary table part, as declared does an XML one.
func (sh *sheet) binDeclared(target string, out []declaredTable) []declaredTable {
	sh.loadBin(target, func(r *xlsb.Reader) {
		var t declaredTable
		found := false
		var buf []byte
		for r.Next() {
			f := xlsb.NewFields(r.Data())
			switch r.ID() {
			case xlsb.BeginList:
				at, ok := binRect(&f)
				if !ok {
					return
				}
				f.U32() // its kind
				f.U32() // its id
				header, totals := f.U32(), f.U32()
				f.Skip(32)
				buf = f.String(buf[:0])
				name := sh.lost.Name(string(buf))
				buf = f.String(buf[:0])
				if display := sh.lost.Name(string(buf)); display != "" {
					name = display
				}
				t = declaredTable{at: at, headerRows: int(min(header, 1)), totalRows: int(min(totals, 1)), name: name}
				found = true
			case xlsb.TableStyleClt:
				flags := f.U16()
				buf = f.String(buf[:0])
				t.style = string(buf)
				t.firstColumn, t.lastColumn = flags&1 != 0, flags&2 != 0
				t.rowStripes, t.columnStripes = flags&4 != 0, flags&8 != 0
			}
		}
		if found {
			out = append(out, t)
		}
	})
	return out
}

// binComments reads a binary comments part, as comments does an XML one.
func (sh *sheet) binComments(target string, add func(row, col int32, raw []byte, author, date string), threaded map[int64]bool) {
	sh.loadBin(target, func(r *xlsb.Reader) {
		var authors []string
		var row, col int32
		author := ""
		in := false
		for r.Next() {
			f := xlsb.NewFields(r.Data())
			switch r.ID() {
			case xlsb.CommentAuthor:
				if len(authors) < maxAuthors {
					authors = append(authors, sh.lost.Name(string(f.String(nil))))
				} else {
					sh.lost.Add(warn.CapCommentAuthorsSheet, 1)
				}
			case xlsb.BeginComment:
				i := f.I32()
				at, ok := binRect(&f)
				in = ok
				row, col = at.r0, at.c0
				author = ""
				if i >= 0 && int(i) < len(authors) {
					author = authors[i]
				}
			case xlsb.CommentText:
				if !in {
					continue
				}
				sh.raw = f.RichString(sh.raw[:0])
				if !threaded[int64(row)<<32|int64(col)] {
					add(row, col, sh.raw, author, "")
				}
			case xlsb.EndComment:
				in = false
			}
		}
	})
}

// loadBin reads a binary part that supports the sheet, as load reads an
// XML one.
func (sh *sheet) loadBin(name string, read func(*xlsb.Reader)) {
	if sh.budget || !sh.wb.pkg.Has(name) {
		return
	}
	sh.noteErr(name, records(sh.wb.pkg, name, read))
}

// readOwn reads the sheet's own part, XML or binary. chunks reads it in
// chunks whatever its size, as reading by bands does. opened is false if
// the part could not be opened at all.
func (sh *sheet) readOwn(chunks bool) (opened bool, err error) {
	pkg, ahead := sh.wb.pkg, opc.Spare(sh.wb.opts.Workers)
	var rc io.ReadCloser
	if chunks {
		rc, err = pkg.Chunks(sh.name, ahead)
	} else {
		rc, err = pkg.OpenPart(sh.name, ahead)
	}
	if err != nil {
		return false, err
	}
	defer rc.Close()
	if sh.bin {
		r := xlsb.NewReader(rc)
		sh.readBin(r)
		return true, r.Err()
	}
	if chunks {
		sh.s.Reset(rc, scanOptions)
	} else {
		sh.s.Reset(rc, pkg.Window(sh.name, scanOptions))
	}
	sh.read()
	return true, sh.s.Err()
}

// binMacroCode is macroCode for a binary macro sheet: its formulas, one
// cell to a line.
func (b *Book) binMacroCode(target string, room int64, lost *warn.Lost) (code string, damaged bool) {
	var sb strings.Builder
	line := func(row, col int32, f string) {
		if f == "" {
			b.unreadNames++ // counted with the names: shown nowhere else
			return
		}
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(cellName(row, col))
		sb.WriteString("\t=")
		sb.WriteString(strings.ReplaceAll(verbatim([]byte(f), lost), "\n", " "))
	}
	err := records(b.wb.pkg, target, func(r *xlsb.Reader) {
		row := int32(-1)
		shared := map[pos]binFormula{}
		var pend *pos // a cell waiting for its shared formula, which the next record gives
		for r.Next() && int64(sb.Len()) < room {
			id := r.ID()
			f := xlsb.NewFields(r.Data())
			if pend != nil && id != xlsb.ShrFmla && id != xlsb.ArrFmla {
				line(pend.row, pend.col, "")
				pend = nil
			}
			switch {
			case id == xlsb.RowHdr:
				row = f.I32()
			case id == xlsb.ShrFmla || id == xlsb.ArrFmla:
				at, ok := binRect(&f)
				if id == xlsb.ArrFmla {
					f.U8()
				}
				rgce, extra := f.Formula()
				if !ok || f.Short {
					continue
				}
				s := binFormula{rgce: bytes.Clone(rgce), extra: bytes.Clone(extra), array: id == xlsb.ArrFmla}
				if len(shared) < maxShared {
					shared[pos{at.r0, at.c0}] = s
				} else {
					lost.Add(warn.CapSharedFormulas, 1)
				}
				if pend != nil && *pend == (pos{at.r0, at.c0}) {
					text, _ := xlsb.Formula(s.rgce, s.extra, b.wb.formulas, xlsb.Place{Row: pend.row, Col: pend.col})
					line(pend.row, pend.col, text)
					pend = nil
				}
			case id >= xlsb.FmlaString && id <= xlsb.FmlaError:
				col := f.I32()
				f.U32() // style
				switch id {
				case xlsb.FmlaString:
					f.String(nil)
				case xlsb.FmlaNum:
					f.F64()
				default:
					f.U8()
				}
				f.U16() // flags
				rgce, extra := f.Formula()
				if f.Short || row < 0 || row >= maxRows || col < 0 || col >= maxCols {
					continue
				}
				if exp, ok := xlsb.ExpOf(rgce, extra); ok {
					base := pos{exp.Row, exp.Col}
					if s, found := shared[base]; found {
						if s.array && base != (pos{row, col}) {
							continue
						}
						text, _ := xlsb.Formula(s.rgce, s.extra, b.wb.formulas, xlsb.Place{Row: row, Col: col})
						line(row, col, text)
					} else if base == (pos{row, col}) {
						pend = &pos{row, col}
					} else {
						line(row, col, "")
					}
					continue
				}
				text, _ := xlsb.Formula(rgce, extra, b.wb.formulas, xlsb.Place{Row: row, Col: col})
				line(row, col, text)
			}
		}
		if pend != nil {
			line(pend.row, pend.col, "")
		}
	})
	return sb.String(), err != nil
}
