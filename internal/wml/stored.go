package wml

import (
	"context"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/stored"
	"github.com/AOShei/go-fast-ooxml/internal/unit"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// What a Word document holds that is shown on no page: its document
// variables (names and values that fields and macros read, kept in the
// settings), the code of its VBA project, and custom XML data. They are
// given in the document's stored part. They are not flagged for being
// stored, which is routine; a variable or a value out of proportion for
// its kind is (stored.MaxWords). Custom XML that a content control is
// bound to is shown there, and is never out of proportion.

// maxVariables bounds the document variables read.
const maxVariables = 1 << 14

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
			for s.Next() != xmlscan.EOF && !p.Full() && n < maxVariables {
				if s.Kind() != xmlscan.Start || !s.Is(ns.Word, "docVar") || s.Depth() != 3 {
					continue
				}
				n++
				name, _ := s.Attr(ns.Word, "name")
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
				blk := gofastcore.Block{Kind: gofastcore.KindParagraph, Text: text,
					Attrs: map[string]string{gofastcore.AttrStored: gofastcore.StoredDocumentVariable, gofastcore.AttrName: label}}
				if n := stored.Words(value); n > stored.MaxWords {
					// A variable is a name and a few words; one that
					// holds a paragraph is out of proportion.
					blk.Hidden = gofastcore.HiddenInvisible
					p.Long("a document variable", label, n)
				}
				p.Add(blk)
			}
			c.Close()
			if s.Err() != nil {
				p.Damaged = append(p.Damaged, b.settings)
			}
		}
	}
	b.extra.Macros(ctx, b.pkg, p)
	b.extra.XML(ctx, b.pkg, p, b.bound)
	res := &unit.Result{}
	res.Limited = p.Finish()
	res.Part, res.Damaged = p.Part, p.Damaged
	return res
}

// bound reports whether a content control of the body shows the data of
// the custom XML store item with the given ID. The body is looked through
// for its bindings the first time it is asked.
func (b *Body) bound(itemID string) bool {
	if itemID == "" {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.bindings == nil {
		b.bindings = map[string]bool{}
		var s xmlscan.Scanner
		if c, err := b.pkg.Scan(b.main, &s, scanOptions); err == nil {
			for s.Next() != xmlscan.EOF {
				if s.Kind() == xmlscan.Start && s.Is(ns.Word, "dataBinding") && len(b.bindings) < 1<<12 {
					if v, ok := s.Attr(ns.Word, "storeItemID"); ok {
						b.bindings[strings.ToUpper(string(v))] = true
					}
				}
			}
			c.Close()
		}
	}
	return b.bindings[itemID]
}
