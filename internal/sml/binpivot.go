package sml

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
	"github.com/AOShei/go-fast-ooxml/internal/xlsb"
)

// The pivot tables and caches of a binary workbook are records in place of
// XML ([MS-XLSB] 2.1.7.38-40), read into what the XML gives: a pivot
// table's name and place, a cache's source, its fields and their values,
// its records, and the formulas of its calculated fields. Which caches are
// shown, stored or judged gone is then the same code for both.

// binarySource reads the source of a cache whose definition is records,
// and the formulas of its calculated fields.
func (b *Book) binarySource(target string) (src cacheSource, ok bool) {
	var calc []binCalc
	var names []string
	err := records(b.wb.pkg, target, func(r *xlsb.Reader) {
		var buf []byte
		var cur *binCalc // the calculated field being read, whose names follow it
		depth := 0       // inside a field's values or groups, whose names are not the field's
		for r.Next() {
			f := xlsb.NewFields(r.Data())
			switch r.ID() {
			case xlsb.BeginPCDSource:
				switch f.U32() {
				case 1:
					src.typ = "external"
				case 2:
					src.typ = "consolidation"
				case 3:
					src.typ = "scenario"
				}
			case xlsb.BeginPCDSRange:
				isName, builtIn, flags := f.U8(), f.U8(), f.U8()
				if flags&2 != 0 {
					buf = f.String(buf[:0])
					src.sheet = string(buf)
				}
				if flags&1 != 0 {
					f.String(nil)
					src.external = true
				}
				if isName&1 != 0 {
					buf = f.String(buf[:0])
					src.name = string(buf)
					if builtIn&1 != 0 {
						src.name = "_xlnm." + src.name
					}
				}
			case xlsb.BeginPCDSCSet:
				f.Skip(16) // the values of its page fields
				f.U8()     // a name, not a range
				f.U8()     // a built-in name
				flags := f.U8()
				sheet := ""
				if flags&2 != 0 {
					sheet = string(f.String(nil))
				}
				if flags&1 != 0 {
					src.external = true
				}
				if len(src.consolidation) < 256 {
					src.consolidation = append(src.consolidation, sheet)
				}
			case xlsb.BeginPCDField:
				flags := f.U16()
				f.Skip(18) // number format, SQL type, hierarchy, level, member properties
				buf = f.String(buf[:0])
				name, _ := clean.String(buf, clean.Prose)
				if len(names) < maxCacheFields {
					names = append(names, name)
				}
				if flags&0x08 != 0 {
					f.String(nil) // its caption
				}
				cur = nil
				if flags&0x100 != 0 && len(calc) >= maxCalculated {
					b.lost.Add(warn.CapCalculated, 1)
				}
				if flags&0x100 != 0 && len(calc) < maxCalculated {
					rgce, extra := f.Formula()
					if !f.Short {
						calc = append(calc, binCalc{name: clean.Name(name), rgce: bytes.Clone(rgce), extra: bytes.Clone(extra)})
						cur = &calc[len(calc)-1]
					}
				}
			case xlsb.BeginPCDFAtbl, xlsb.BeginPCDFGroup:
				depth++
			case xlsb.EndPCDFAtbl, xlsb.EndPCDFGroup:
				depth--
			case xlsb.BeginPName:
				// A field the formula of the calculated field refers to.
				if cur != nil && depth == 0 && len(cur.fields) < maxCacheFields {
					cur.fields = append(cur.fields, f.U32())
				}
			case xlsb.EndPCDField:
				cur, depth = nil, 0
			}
		}
	})
	if err != nil && len(names) == 0 && src.typ == "" && src.sheet == "" && src.name == "" {
		return src, false // nothing of it could be read
	}
	if src.typ == "" {
		src.typ = "worksheet"
	}
	for _, c := range calc {
		book := &xlsb.Book{Fields: make([]string, len(c.fields))}
		for i, fd := range c.fields {
			if uint64(fd) < uint64(len(names)) {
				book.Fields[i] = names[fd]
			}
		}
		text, ok := xlsb.Formula(c.rgce, c.extra, book, xlsb.Place{})
		if !ok {
			b.unreadNames++
			continue
		}
		switch text = verbatim([]byte(text), &b.lost); {
		case text == "":
		case len(b.calculated) < maxCalculated:
			b.calculated = append(b.calculated, calcField{name: c.name, formula: text})
		default:
			b.lost.Add(warn.CapCalculated, 1)
		}
	}
	return src, true
}

// binCalc is a calculated field of a binary cache: its formula, and the
// fields the formula numbers, by their place in the cache.
type binCalc struct {
	name        string
	rgce, extra []byte
	fields      []uint32
}

// binaryFields is cacheFields for a definition in records: the fields,
// with the values each takes and how a record packs its value, and the
// records part.
func (b *Book) binaryFields(target string, lost *warn.Lost) (fields []cacheField, recordsPart string, err error) {
	rels, _ := b.wb.pkg.Rels(target)
	byID := map[string]opc.Rel{}
	for _, rel := range rels {
		byID[rel.ID] = rel
	}
	items := 0
	err = records(b.wb.pkg, target, func(r *xlsb.Reader) {
		var buf []byte
		var cur *cacheField // the field whose values are being read
		inValues, inGroup := false, false
		for r.Next() {
			f := xlsb.NewFields(r.Data())
			id := r.ID()
			switch id {
			case xlsb.BeginPivotCacheDef:
				f.Skip(3) // versions
				f.U8()    // flags
				f.U32()   // items kept that no record uses
				f.F64()   // when it was refreshed
				flags := f.U8()
				f.U32() // the number of records
				if flags&1 != 0 {
					f.String(nil) // who refreshed it
				}
				if flags&2 != 0 {
					buf = f.String(buf[:0])
					if rel, found := byID[string(buf)]; found && !rel.External && ns.IsRel(rel.Type, relPivotRecords) {
						recordsPart = rel.Target
					}
				}
			case xlsb.BeginPCDField:
				cur = nil
				if len(fields) >= maxCacheFields {
					lost.Add(warn.CapPivotFields, 1)
					continue
				}
				flags := f.U16()
				f.Skip(18)
				buf = f.String(buf[:0])
				var fd cacheField
				fd.name, _ = clean.String(buf, clean.Prose)
				fd.source = flags&0x04 != 0
				fd.pack = 's'
				fields = append(fields, fd)
				cur = &fields[len(fields)-1]
			case xlsb.EndPCDField:
				cur = nil
			case xlsb.BeginPCDFAtbl:
				inValues = true
				if cur == nil {
					continue
				}
				flags := f.U16()
				n := f.U32()
				switch {
				case n > 0:
					cur.pack = 'x'
				case flags&0x40 != 0: // numbers only
					cur.pack = 'n'
				case flags&0x04 != 0 && flags&0x08 == 0: // dates, and no text
					cur.pack = 'd'
				}
			case xlsb.EndPCDFAtbl:
				inValues = false
			case xlsb.BeginPCDFGroup:
				inGroup = true
			case xlsb.EndPCDFGroup:
				inGroup = false
			case xlsb.BeginPCDIRun:
				if cur == nil || !inValues || inGroup {
					continue
				}
				run, more := cacheRun(&f, maxSharedItems-items)
				for _, v := range run {
					cur.items = append(cur.items, v)
					items++
				}
				lost.Add(warn.CapPivotItems, more)
			default:
				if cur == nil || !inValues || inGroup {
					continue
				}
				if items >= maxSharedItems {
					if _, ok := cacheItem(id, &f); ok {
						lost.Add(warn.CapPivotItems, 1)
					}
					continue
				}
				if v, ok := cacheItem(id, &f); ok {
					cur.items = append(cur.items, v)
					items++
				}
			}
		}
	})
	return fields, recordsPart, err
}

// cacheItem reads a value of a cache (BrtPCDI*, BrtPCDIA*) as text, and
// reports whether the record is one.
func cacheItem(id int, f *xlsb.Fields) (string, bool) {
	switch id {
	case xlsb.PCDIMissing, xlsb.PCDIAMissing:
		return "", true
	case xlsb.PCDINumber, xlsb.PCDIANumber:
		return cacheNumber(f.F64()), true
	case xlsb.PCDIBoolean, xlsb.PCDIABoolean:
		if f.U8() != 0 {
			return "TRUE", true
		}
		return "FALSE", true
	case xlsb.PCDIError, xlsb.PCDIAError:
		return xlsb.ErrorText(f.U8()), true
	case xlsb.PCDIString, xlsb.PCDIAString:
		t, _ := clean.String(f.String(nil), clean.Prose)
		return t, true
	case xlsb.PCDIDatetime, xlsb.PCDIADatetime:
		return cacheDate(f), true
	}
	return "", false
}

// cacheRun reads a run of values of one type (BrtBeginPCDIRun), at most n.
func cacheRun(f *xlsb.Fields, n int) (out []string, more int64) {
	kind := f.U16()
	count := int64(f.U32())
	if count > int64(n) && !f.Short {
		// The run holds more than the bound leaves room for: as many as
		// it says, past those read.
		more = count - int64(n)
	}
	for i := int64(0); i < count && i < int64(n) && !f.Short && f.Left() > 0; i++ {
		switch kind {
		case 0x01:
			out = append(out, cacheNumber(f.F64()))
		case 0x02:
			t, _ := clean.String(f.String(nil), clean.Prose)
			out = append(out, t)
		case 0x10:
			out = append(out, xlsb.ErrorText(f.U8()))
		case 0x20:
			out = append(out, cacheDate(f))
		default:
			return out, 0
		}
	}
	if f.Short && len(out) > 0 {
		out = out[:len(out)-1] // the last was cut short
	}
	return out, more
}

// cacheNumber writes a number as cacheValue does an XML cache's.
func cacheNumber(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return ""
	}
	return strconv.FormatFloat(v, 'g', 15, 64)
}

// cacheDate reads a date and time of a cache (PCDIDateTime) as an XML
// cache writes it, ISO 8601, with a date at midnight a date alone.
func cacheDate(f *xlsb.Fields) string {
	y, mo := f.U16(), f.U16()
	d, h, mi, s := f.U8(), f.U8(), f.U8(), f.U8()
	if f.Short || y > 9999 || mo < 1 || mo > 12 || d < 1 || d > 31 || h > 23 || mi > 59 || s > 59 {
		return ""
	}
	t := fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:%02d", y, mo, d, h, mi, s)
	return strings.TrimSuffix(t, "T00:00:00")
}

// binaryRecords is cacheRecords for a records part in records: each
// record (BrtPCRRecord) packs a value for each field of the source, in
// order; or (BrtPCRRecordDt) is followed by a value record for each.
func (b *Book) binaryRecords(target string, fields []cacheField, add func([]string) bool) (cut bool, err error) {
	var source []int // the fields of the source, by place
	for i, f := range fields {
		if f.source {
			source = append(source, i)
		}
	}
	err = records(b.wb.pkg, target, func(r *xlsb.Reader) {
		var row []string // a record given by the value records after it
		next := 0
		flush := func() bool {
			if row == nil {
				return true
			}
			ok := add(row)
			row = nil
			return ok
		}
		for r.Next() {
			f := xlsb.NewFields(r.Data())
			id := r.ID()
			switch {
			case id == xlsb.PCRRecord:
				if !flush() {
					cut = true
					return
				}
				out := make([]string, len(fields))
				for _, i := range source {
					out[i] = packedValue(&f, &fields[i])
					if f.Short {
						out[i] = "" // what there is of a value cut short
						break
					}
				}
				if !add(out) {
					cut = true
					return
				}
			case id == xlsb.PCRRecordDt:
				if !flush() {
					cut = true
					return
				}
				row, next = make([]string, len(fields)), 0
			case row != nil:
				if next < len(source) {
					fd := &fields[source[next]]
					if id == xlsb.PCDIIndex {
						if v := f.U32(); uint64(v) < uint64(len(fd.items)) {
							row[source[next]] = fd.items[v]
						}
					} else if v, ok := cacheItem(id, &f); ok {
						row[source[next]] = v
					}
				}
				next++
			}
		}
		if !flush() {
			cut = true
		}
	})
	return cut, err
}

// packedValue reads a field's value of a packed record.
func packedValue(f *xlsb.Fields, fd *cacheField) string {
	switch fd.pack {
	case 'x':
		if v := f.U32(); !f.Short && uint64(v) < uint64(len(fd.items)) {
			return fd.items[v]
		}
		return ""
	case 'n':
		v := f.F64()
		if f.Short {
			return ""
		}
		return cacheNumber(v)
	case 'd':
		return cacheDate(f)
	}
	t, _ := clean.String(f.String(nil), clean.Prose)
	return t
}

// binPivot reads a binary pivot table part, as pivots does an XML one:
// its name, and the cells it fills, the rows above its first row of values
// being its headers.
func (sh *sheet) binPivot(target string, out []declaredTable) []declaredTable {
	sh.loadBin(target, func(r *xlsb.Reader) {
		t := declaredTable{role: gofastcore.RolePivot}
		for r.Next() {
			f := xlsb.NewFields(r.Data())
			switch r.ID() {
			case xlsb.BeginSXView:
				f.Skip(32) // flags, the data field's place, versions, format, cache
				t.name = clean.Name(string(f.String(nil)))
			case xlsb.BeginSXLocation:
				at, ok := binRect(&f)
				if !ok {
					return
				}
				f.I32() // the first row of headers
				first := f.I32()
				t.at = at
				if n := int64(first) - int64(at.r0); !f.Short && n > 0 {
					t.headerRows = int(min(n, int64(at.r1-at.r0+1)))
				}
				out = append(out, t)
				return
			}
		}
	})
	return out
}
