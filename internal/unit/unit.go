// Package unit is what the readers of the three formats have in common
// when a document is read a part at a time: a Word document's one body,
// a workbook's sheets, a presentation's slides.
package unit

import (
	"context"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
)

// Result is what reading one part gives: the part, and what reading it
// found out about the document as a whole.
type Result struct {
	Part gofastcore.Part
	Tally
}

// Tally is what reading one part found out about the document as a
// whole. It is small, and is kept when the part itself has been handed
// on.
type Tally struct {
	// Unread counts content of kinds that are not read: sound and video
	// clips, dialog sheets.
	Unread map[string]int
	// Damaged names the supporting parts that could not be read to
	// their end.
	Damaged []string
	// External counts references to things outside the file other than
	// hyperlinks; Programs, actions that start a program or run a macro.
	External, Programs int
	// Macros says the part is a sheet of macros.
	Macros bool
	// Limited says a limit cut the reading short, in this part or in
	// something it shares with others. What was read up to a limit
	// depends on what else was being read at the time, so a document
	// with a part so marked is read again, in order.
	Limited bool
	// Rels is the part's relationships, which name the files embedded
	// in it.
	Rels []opc.Rel
	// Warnings are warnings about the document that the reader has
	// made itself, and Children the embedded files it has extracted. A
	// reader that leaves both to the caller sets neither.
	Warnings []gofastcore.Warning
	Children []*gofastcore.Document
}

// Source is a document opened by the reader of its format. All its
// methods are safe for concurrent use.
type Source interface {
	// Len returns the number of parts.
	Len() int
	// Stored returns the index of the stored part (gofastcore.PartStored),
	// which is the last, or -1 if the document has none.
	Stored() int
	// Noun names a part of this format, in the plural: "sheets".
	Noun() string
	// Begin starts a pass over the document: the limits count from here.
	Begin()
	// Read reads the part with the given index, from 0. It always returns
	// a part: one that could not be read says so in its warnings.
	Read(ctx context.Context, index int) *Result
	// Limited reports whether a limit has cut short anything the parts
	// share since the source was opened.
	Limited() bool
	// Summary turns what was found out while the parts were read into
	// warnings about the document. tallies are those of the parts read,
	// in the order of the parts.
	Summary(tallies []*Tally) []gofastcore.Warning
}
