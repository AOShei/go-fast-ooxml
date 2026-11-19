package sml

import (
	"context"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/unit"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// What a workbook holds that is shown on no sheet: its defined names (a
// name can stand for a text that appears nowhere else) and the formulas
// of its Excel 4 macro sheets, which are programs. Nobody who opens the
// workbook sees them, so they are a place to put text for a model; they
// are read as data, never evaluated, and given in the document's stored
// part (gofastcore.PartStored), flagged as hidden.

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
	if len(b.names) == 0 && !b.macroSheets {
		return -1
	}
	return max(len(b.refs), 1)
}

// readStored reads the stored part: the defined names, then the code of
// each macro sheet.
func (b *Book) readStored(ctx context.Context) *unit.Result {
	res := &unit.Result{Part: gofastcore.Part{Kind: gofastcore.PartStored, Number: b.storedIndex() + 1, Hidden: gofastcore.HiddenInvisible}}
	part := &res.Part
	room := gofastcore.TextBudgetFrom(ctx)
	cut := false
	add := func(blk gofastcore.Block) bool {
		if cut || !room.Take(gofastcore.BlockCost+len(blk.Text)) {
			cut = true
			return false
		}
		part.Blocks = append(part.Blocks, blk)
		return true
	}
	for _, n := range b.names {
		if !add(gofastcore.Block{Kind: gofastcore.KindParagraph, Text: n.name + ": " + n.formula,
			Attrs: map[string]string{gofastcore.AttrStored: gofastcore.StoredDefinedName, gofastcore.AttrName: n.name}}) {
			break
		}
	}
	for _, ref := range b.refs {
		if cut || ctx.Err() != nil {
			break
		}
		if ref.kind != ns.RelMacrosheet && ref.kind != ns.RelIntlMacrosheet || ref.target == "" {
			continue
		}
		code, damaged := b.macroCode(ref.target, room.Left())
		if damaged {
			res.Tally.Damaged = append(res.Tally.Damaged, ref.target)
		}
		if code == "" {
			continue
		}
		add(gofastcore.Block{Kind: gofastcore.KindCode, Text: code,
			Attrs: map[string]string{gofastcore.AttrStored: gofastcore.StoredMacro, gofastcore.AttrName: ref.name, gofastcore.AttrLanguage: "excel4"}})
	}
	if cut {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the limit on the amount of text was reached; what the workbook stores is cut off there")
		res.Tally.Limited = true
	}
	if len(part.Blocks) == 0 {
		part.Warn(gofastcore.SeverityInfo, gofastcore.WarnPartNoText, "")
	}
	part.MarkHidden()
	return res
}

// macroCode returns the formulas of a macro sheet, one cell to a line
// ("A1\t=ECHO(FALSE)"), as Excel shows them when it shows formulas, up to
// about room bytes. damaged says the part could not be read to its end.
func (b *Book) macroCode(target string, room int64) (string, bool) {
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
