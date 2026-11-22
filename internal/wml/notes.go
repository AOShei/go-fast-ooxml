package wml

import (
	"strconv"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// How footnotes and endnotes are numbered: in which form (1, i, a, *),
// from which number, and whether the count starts again with each
// section. The document's settings say it for the whole document, and a
// section may say otherwise for itself. A section's properties come at
// its end, after its text, so a note's number is settled only when the
// document has been read: a note is given a placeholder mark meanwhile.

// Values of notePr.restart.
const (
	restartUnset int8 = iota
	restartContinuous
	restartSection
	restartPage // on each page; there are no pages here, so the count goes on
)

// notePr is w:footnotePr or w:endnotePr; zero fields are unset.
type notePr struct {
	format  string
	start   int
	restart int8
}

// noteMark is a note referred to without a mark of its own.
type noteMark struct {
	kind    int // 0 footnote, 1 endnote
	section int // the section it is in
}

// placeholder starts the mark of a note whose number is not settled yet.
// No mark from the file starts with it: marks are cleaned of control
// characters.
const placeholder = "\x00"

func readNotePr(s *xmlscan.Scanner) notePr {
	var pr notePr
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start || s.Depth() != depth+1 {
			continue
		}
		v, _ := s.Attr(ns.Word, "val")
		switch {
		case s.Is(ns.Word, "numFmt"):
			pr.format = string(v)
		case s.Is(ns.Word, "numStart"):
			if n, err := strconv.Atoi(string(v)); err == nil && n >= 0 && n < 1<<16 {
				pr.start = max(n, 1)
			}
		case s.Is(ns.Word, "numRestart"):
			switch string(v) {
			case "continuous":
				pr.restart = restartContinuous
			case "eachSect":
				pr.restart = restartSection
			case "eachPage":
				pr.restart = restartPage
			}
		}
		s.Skip()
	}
	return pr
}

// noteMark records a note referred to without a mark of its own and
// returns its placeholder.
func (r *reader) noteMark(kind int) string {
	r.noteMarks = append(r.noteMarks, noteMark{kind: kind, section: len(r.sections)})
	return placeholder + strconv.Itoa(len(r.noteMarks)-1)
}

// maxNoteSections bounds the sections whose note numbering is kept.
const maxNoteSections = warn.MaxNoteSections

// endSection records the note properties of a section that has ended.
func (r *reader) endSection(props [2]notePr) {
	if len(r.sections) < maxNoteSections {
		r.sections = append(r.sections, props)
	} else if props != ([2]notePr{}) {
		// A section past the bound numbers its notes as the document
		// does: a loss only where it says otherwise.
		r.lost.Add(warn.CapNoteSections, 1)
	}
}

// effective returns how notes of a kind are numbered in a section.
func (r *reader) effective(kind, section int) notePr {
	pr := r.noteProps[kind]
	if section < len(r.sections) {
		own := r.sections[section][kind]
		if own.format != "" {
			pr.format = own.format
		}
		if own.start != 0 {
			pr.start = own.start
		}
		if own.restart != restartUnset {
			pr.restart = own.restart
		}
	}
	if pr.format == "" {
		pr.format = [2]string{"decimal", "lowerRoman"}[kind]
	}
	if pr.start == 0 {
		pr.start = 1
	}
	return pr
}

// numberNotes replaces the placeholder marks of notes by their numbers.
// The count of each kind runs through the document in the order the notes
// are referred to. It begins at the first section's first number, and
// begins again at a section that restarts it or names a first number of
// its own.
func (r *reader) numberNotes(blocks []gofastcore.Block) {
	if len(r.noteMarks) == 0 {
		return
	}
	numbers := make([]string, len(r.noteMarks))
	var value, section [2]int
	for k := range 2 {
		value[k] = r.effective(k, 0).start - 1
	}
	for i, m := range r.noteMarks {
		k := m.kind
		for section[k] < m.section {
			section[k]++
			if next := section[k]; next < len(r.sections) {
				if pr := r.effective(k, next); r.sections[next][k].start != 0 || pr.restart == restartSection {
					value[k] = pr.start - 1
				}
			}
		}
		value[k]++
		numbers[i] = formatNumber(value[k], r.effective(k, m.section).format)
	}
	for i := range blocks {
		b := &blocks[i]
		if b.Kind != gofastcore.KindFootnote || !strings.HasPrefix(b.Marker, placeholder) {
			continue
		}
		n, err := strconv.Atoi(b.Marker[len(placeholder):])
		if err != nil || n < 0 || n >= len(numbers) {
			b.Marker = ""
			continue
		}
		b.Marker = numbers[n]
	}
}
