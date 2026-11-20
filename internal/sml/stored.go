package sml

import (
	"context"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/stored"
	"github.com/AOShei/go-fast-ooxml/internal/unit"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// What a workbook holds that is shown on no sheet: its defined names (a
// name can stand for a text that appears nowhere else), the formulas of
// its Excel 4 macro sheets and the code of its VBA project, which are
// programs, the records of pivot caches whose source is gone and the
// formulas of calculated fields (pivot.go), and custom XML data. They
// are read as data, never evaluated, and given in the document's stored
// part (gofastcore.PartStored), with no display reason.

// definedName is one name the workbook defines.
type definedName struct {
	name    string // with its sheet in front, for a name local to a sheet
	formula string
}

// maxNames bounds the defined names kept from a workbook.
const maxNames = 1 << 14

// readNames reads definedNames in the workbook part.
func (b *Book) readNames(s *xmlscan.Scanner) {
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start || !s.Is(ns.Sheet, "definedName") || len(b.names) >= maxNames {
			continue
		}
		v, _ := s.Attr(xmlscan.NoSpace, "name")
		name := clean.Name(string(v))
		local, isLocal := intAttr(s, "localSheetId")
		b.raw = s.AppendText(b.raw[:0])
		f := verbatim(b.raw)
		if name == "" || f == "" {
			continue
		}
		b.names = append(b.names, definedName{name: name, formula: f})
		if isLocal {
			b.localNames = append(b.localNames, localName{index: len(b.names) - 1, sheet: local})
		}
	}
}

// localName is a name local to a sheet, named by its index, which the
// sheets' names replace once they are known.
type localName struct {
	index, sheet int
}

// storedIndex returns the index of the stored part, or -1 if the workbook
// has nothing for it.
func (b *Book) storedIndex() int {
	if len(b.names) == 0 && !b.macroSheets && b.goneCaches() == 0 && len(b.calculated) == 0 && b.extra.Empty() {
		return -1
	}
	return max(len(b.refs), 1)
}

// readStored reads the stored part: the defined names, the code of each
// macro sheet and of the VBA project, the records of pivot caches whose
// source is gone and the formulas of calculated fields, then the custom
// XML data.
func (b *Book) readStored(ctx context.Context) *unit.Result {
	p := stored.NewPart(ctx, b.storedIndex()+1)
	res := &unit.Result{}
	formula := func(name, text, what string) gofastcore.Block {
		return gofastcore.Block{Kind: gofastcore.KindParagraph, Text: text,
			Attrs: map[string]string{gofastcore.AttrStored: what, gofastcore.AttrName: name}}
	}
	for _, n := range b.names {
		if !p.Add(formula(n.name, n.name+": "+n.formula, gofastcore.StoredDefinedName)) {
			break
		}
	}
	for _, ref := range b.refs {
		if p.Full() || ctx.Err() != nil {
			break
		}
		if ref.kind != ns.RelMacrosheet && ref.kind != ns.RelIntlMacrosheet || ref.target == "" {
			continue
		}
		code, damaged := b.macroCode(ref.target, p.Room().Left())
		if damaged {
			p.Damaged = append(p.Damaged, ref.target)
		}
		if code == "" {
			continue
		}
		p.Add(gofastcore.Block{Kind: gofastcore.KindCode, Text: code,
			Attrs: map[string]string{gofastcore.AttrStored: gofastcore.StoredMacro, gofastcore.AttrName: ref.name, gofastcore.AttrLanguage: "excel4"}})
	}
	b.extra.Macros(ctx, b.wb.pkg, p)
	// The records of pivot caches whose source is gone.
	for _, c := range b.caches {
		if p.Full() || ctx.Err() != nil {
			break
		}
		if c.gone == "" {
			continue
		}
		block, ok, full, damaged := b.cacheTable(c, p.Room())
		if damaged {
			p.Damaged = append(p.Damaged, c.target)
		}
		if ok {
			p.Blocks = append(p.Blocks, block)
		}
		if full {
			p.Cut()
		}
	}
	// The formulas of calculated fields.
	for _, f := range b.calculated {
		if !p.Add(formula(f.name, f.name+": ="+f.formula, gofastcore.StoredPivotCache)) {
			break
		}
	}
	b.extra.XML(ctx, b.wb.pkg, p)
	b.extra.AfterEnd(b.wb.pkg, p)
	res.Limited = p.Finish()
	res.Part, res.Damaged = p.Part, p.Damaged
	return res
}

// macroCode returns the formulas of a macro sheet, one cell to a line
// ("A1\t=ECHO(FALSE)"), as Excel shows them when it shows formulas, up to
// about room bytes. damaged says the part could not be read to its end.
func (b *Book) macroCode(target string, room int64) (string, bool) {
	if binary(b.wb.pkg, target) {
		return b.binMacroCode(target, room)
	}
	var sb strings.Builder
	damaged := false
	var s xmlscan.Scanner
	c, err := b.wb.pkg.Scan(target, &s, scanOptions)
	if err != nil {
		return "", true
	}
	defer c.Close()
	var ref []byte
	for s.Next() != xmlscan.EOF && int64(sb.Len()) < room {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(ns.Sheet, "c"):
			v, _ := s.Attr(xmlscan.NoSpace, "r")
			ref = append(ref[:0], v...)
		case s.Is(ns.Sheet, "f"):
			b.raw = s.AppendText(b.raw[:0])
			if f := verbatim(b.raw); f != "" {
				if sb.Len() > 0 {
					sb.WriteByte('\n')
				}
				sb.WriteString(clean.Label(string(ref)))
				sb.WriteString("\t=")
				sb.WriteString(strings.ReplaceAll(f, "\n", " "))
			}
		}
	}
	if s.Err() != nil {
		damaged = true
	}
	return sb.String(), damaged
}
