package wml

import (
	"context"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/stored"
	"github.com/AOShei/go-fast-ooxml/internal/unit"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// What a Word document holds that is shown on no page: its document
// variables (names and values that fields and macros read, kept in the
// settings), the code of its VBA project, and custom XML data. They are
// given in the document's stored part, with no display reason: the file
// declares nothing about their display.

// maxVariables bounds the document variables read.
const maxVariables = warn.MaxVariables

// findStored names what the stored part is read from: the settings part,
// if it has document variables, and the package's VBA project and custom
// XML data.
func findStored(pkg *opc.Package, main string) (settings string, extra stored.Source) {
	rels, _ := pkg.Rels(main)
	extra = stored.Find(pkg, rels)
	for _, rel := range rels {
		if rel.External || !ns.IsRel(rel.Type, ns.RelSettings) || !pkg.Has(rel.Target) {
			continue
		}
		var s xmlscan.Scanner
		c, err := pkg.Scan(rel.Target, &s, scanOptions)
		if err != nil {
			continue
		}
		found := false
		for s.Next() != xmlscan.EOF && !found {
			if s.Kind() == xmlscan.Start && s.Depth() == 2 {
				found = s.Is(ns.Word, "docVars")
				s.Skip()
			}
		}
		c.Close()
		if found {
			return rel.Target, extra
		}
	}
	return "", extra
}

// readStored reads the stored part, which is numbered 2.
func (b *Body) readStored(ctx context.Context) *unit.Result {
	p := stored.NewPart(ctx, 2)
	if b.settings != "" {
		var s xmlscan.Scanner
		c, err := b.pkg.Scan(b.settings, &s, scanOptions)
		if err != nil {
			p.Damaged = append(p.Damaged, b.settings)
		} else {
			n := 0
			for s.Next() != xmlscan.EOF && !p.Full() {
				if s.Kind() != xmlscan.Start || !s.Is(ns.Word, "docVar") || s.Depth() != 3 {
					continue
				}
				if n >= maxVariables {
					p.Lost.Add(warn.CapVariables, 1)
					continue
				}
				n++
				name, _ := s.Attr(ns.Word, "name")
				if clean.LabelCut(string(name)) {
					p.Lost.Add(warn.CapName, 1)
				}
				label := clean.Name(string(name))
				val, _ := s.Attr(ns.Word, "val")
				value, _ := clean.String(val, clean.Prose)
				text := label + ": " + value
				switch {
				case value == "" && label == "":
					continue
				case value == "":
					text = label
				case label == "":
					text = value
				}
				p.Add(gofastcore.Block{Kind: gofastcore.KindParagraph, Text: text,
					Attrs: map[string]string{gofastcore.AttrStored: gofastcore.StoredDocumentVariable, gofastcore.AttrName: label}})
			}
			c.Close()
			if s.Err() != nil {
				p.Damaged = append(p.Damaged, b.settings)
			}
		}
	}
	b.extra.Macros(ctx, b.pkg, p)
	b.extra.XML(ctx, b.pkg, p)
	b.extra.AfterEnd(b.pkg, p)
	res := &unit.Result{}
	res.Limited = p.Finish()
	res.Part, res.Damaged = p.Part, p.Damaged
	return res
}
