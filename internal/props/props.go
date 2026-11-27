// Package props reads the document properties of an Office Open XML
// package into the shared model's Metadata.
package props

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-core/clean"
	"github.com/AOShei/go-fast-core/xmlscan"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
)

// Bounds on what a file can put into Metadata.Extra through its custom
// properties.
const (
	maxCustom     = warn.MaxCustomProps
	maxValueBytes = warn.MaxPropertyValue
	maxCustomName = warn.MaxPropertyName
	customPrefix  = "custom."

	conventionCore = "docprops/core.xml"
	conventionApp  = "docprops/app.xml"
	conventionCust = "docprops/custom.xml"
)

// Result is what Read found.
type Result struct {
	Metadata gofastcore.Metadata
	// Removed counts control characters taken out of property values.
	Removed int
	// Damaged names property parts that could not be read to the end.
	// What was read before the damage is kept.
	Damaged []string
	// What the bounds left out: values cut at their size bound and the
	// bytes cut off them, custom property names likewise, and custom
	// properties past the bound on their number.
	ValuesCut, ValueBytes int64
	NamesCut, NameBytes   int64
	CustomPast            int64
}

// Warnings returns what the bounds left out as limit.exceeded warnings,
// one per bound.
func (r *Result) Warnings() []gofastcore.Warning {
	var l warn.Lost
	l.AddCut(warn.CapPropertyValue, r.ValuesCut, r.ValueBytes)
	l.AddCut(warn.CapPropertyName, r.NamesCut, r.NameBytes)
	l.Add(warn.CapCustomProps, r.CustomPast)
	return l.Warnings()
}

var scanOptions = &xmlscan.Options{Spaces: ns.Spaces}

// Read fills a Metadata from the package's core, extended and custom
// property parts. The parts are found through the package's relationships,
// then in their conventional places. A package with no properties gives an
// empty Metadata.
//
// The only error returned is opc.ErrBudget: a damaged properties part costs
// that part's values, not the document.
func Read(p *opc.Package) (Result, error) {
	var res Result
	core, app, custom := conventionCore, conventionApp, conventionCust
	rels, err := p.Rels("")
	if errors.Is(err, opc.ErrBudget) {
		return res, err
	}
	for _, r := range rels {
		switch {
		case r.External:
		case ns.IsRel(r.Type, ns.RelCoreProps):
			core = r.Target
		case ns.IsRel(r.Type, ns.RelExtProps, ns.RelExtPropsStrict):
			app = r.Target
		case ns.IsRel(r.Type, ns.RelCustomProps, ns.RelCustomStrict):
			custom = r.Target
		}
	}
	for _, part := range []struct {
		name string
		read func(*xmlscan.Scanner, *Result)
	}{{core, readCore}, {app, readApp}, {custom, readCustom}} {
		if !p.Has(part.name) {
			continue
		}
		var s xmlscan.Scanner
		c, err := p.Scan(part.name, &s, scanOptions)
		if err != nil {
			res.Damaged = append(res.Damaged, part.name)
			continue
		}
		part.read(&s, &res)
		c.Close()
		if err := s.Err(); err != nil {
			if errors.Is(err, opc.ErrBudget) {
				return res, err
			}
			res.Damaged = append(res.Damaged, part.name)
		}
	}
	return res, nil
}

// value reads the text of the current element as a property value.
func value(s *xmlscan.Scanner, res *Result) string {
	var buf [256]byte
	text := s.AppendText(buf[:0])
	if len(text) > maxValueBytes {
		n := maxValueBytes
		for n > 0 && !utf8.RuneStart(text[n]) {
			n-- // do not cut a character in two
		}
		res.ValuesCut++
		res.ValueBytes += int64(len(text) - n)
		text = text[:n]
	}
	// Property values are text to be read: ligatures are written as
	// their letters here as everywhere.
	v, removed := clean.String(text, clean.Prose)
	res.Removed += removed
	return v
}

func extra(m *gofastcore.Metadata, key, v string) {
	if v == "" {
		return
	}
	if m.Extra == nil {
		m.Extra = map[string]string{}
	}
	m.Extra[key] = v
}

func readCore(s *xmlscan.Scanner, res *Result) {
	m := &res.Metadata
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start || s.Depth() != 2 {
			continue
		}
		space, local := s.Space(), string(s.Local())
		v := value(s, res)
		if v == "" {
			continue
		}
		switch {
		case space == ns.DC && local == "title":
			m.Title = v
		case space == ns.DC && local == "creator":
			// One entry, as written: the field is free text, and splitting
			// it on commas or semicolons would be a guess.
			m.Authors = []string{v}
		case space == ns.DC && local == "subject":
			m.Subject = v
		case space == ns.CoreProps && local == "keywords":
			m.Keywords = []string{v}
		case space == ns.DC && local == "language":
			m.Language = v
		case space == ns.DCTerms && local == "created":
			m.Created = parseTime(v)
		case space == ns.DCTerms && local == "modified":
			m.Modified = parseTime(v)
		case space == ns.DC && local == "description":
			extra(m, "description", v)
		case space == ns.CoreProps && local == "lastModifiedBy":
			extra(m, "last_modified_by", v)
		case space == ns.CoreProps && local == "revision":
			extra(m, "revision", v)
		case space == ns.CoreProps && local == "category":
			extra(m, "category", v)
		case space == ns.CoreProps && local == "contentStatus":
			extra(m, "content_status", v)
		}
	}
}

func readApp(s *xmlscan.Scanner, res *Result) {
	m := &res.Metadata
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start || s.Depth() != 2 || s.Space() != ns.ExtProps {
			continue
		}
		switch string(s.Local()) {
		case "Application":
			m.Application = value(s, res)
		case "AppVersion":
			extra(m, "app_version", value(s, res))
		case "Company":
			extra(m, "company", value(s, res))
		case "Manager":
			extra(m, "manager", value(s, res))
		case "Template":
			extra(m, "template", value(s, res))
		default:
			s.Skip()
		}
	}
}

func readCustom(s *xmlscan.Scanner, res *Result) {
	m := &res.Metadata
	n := 0
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start || s.Depth() != 2 || !s.Is(ns.CustomProps, "property") {
			continue
		}
		raw, _ := s.Attr(xmlscan.NoSpace, "name")
		if len(raw) > maxCustomName {
			n := maxCustomName
			for n > 0 && !utf8.RuneStart(raw[n]) {
				n-- // do not cut a character in two
			}
			res.NamesCut++
			res.NameBytes += int64(len(raw) - n)
			raw = raw[:n]
		}
		name, removed := clean.String(raw, clean.Trim)
		res.Removed += removed
		name = strings.ToValidUTF8(name, "")
		v := value(s, res)
		if name == "" {
			continue
		}
		if n >= maxCustom {
			res.CustomPast++
			continue
		}
		n++
		extra(m, customPrefix+strings.ToLower(name), v)
	}
}

var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02T15:04",
	"2006-01-02",
	"2006-01",
	"2006",
}

// parseTime reads a W3CDTF date, the form the properties use, and returns
// the zero time for anything else. A value without a zone is taken as UTC.
func parseTime(v string) time.Time {
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// Read2003 fills a Metadata from the properties of a document of Office
// 2003's XML formats (WordprocessingML and SpreadsheetML 2003): the parts
// props and custom hold their o:DocumentProperties and
// o:CustomDocumentProperties elements. Either may be missing. The keys are
// those of the .docx and .xlsx properties they correspond to.
func Read2003(p *opc.Package, props, custom string) (Result, error) {
	var res Result
	for _, part := range []struct {
		name string
		read func(*xmlscan.Scanner, *Result)
	}{{props, read2003}, {custom, readCustom2003}} {
		if !p.Has(part.name) {
			continue
		}
		var s xmlscan.Scanner
		c, err := p.Scan(part.name, &s, scanOptions)
		if err != nil {
			res.Damaged = append(res.Damaged, part.name)
			continue
		}
		part.read(&s, &res)
		c.Close()
		if err := s.Err(); err != nil {
			if errors.Is(err, opc.ErrBudget) {
				return res, err
			}
			res.Damaged = append(res.Damaged, part.name)
		}
	}
	return res, nil
}

func read2003(s *xmlscan.Scanner, res *Result) {
	m := &res.Metadata
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start || s.Depth() != 2 || s.Space() != ns.Office {
			continue
		}
		local := string(s.Local())
		v := value(s, res)
		if v == "" {
			continue
		}
		switch local {
		case "Title":
			m.Title = v
		case "Subject":
			m.Subject = v
		case "Author":
			m.Authors = []string{v}
		case "Keywords":
			m.Keywords = []string{v}
		case "Created":
			m.Created = parseTime(v)
		case "LastSaved":
			m.Modified = parseTime(v)
		case "Description":
			extra(m, "description", v)
		case "LastAuthor":
			extra(m, "last_modified_by", v)
		case "Revision":
			extra(m, "revision", v)
		case "Category":
			extra(m, "category", v)
		case "Company":
			extra(m, "company", v)
		case "Manager":
			extra(m, "manager", v)
		case "Version":
			extra(m, "app_version", v)
		case "Template":
			extra(m, "template", v)
		}
	}
}

// readCustom2003 reads o:CustomDocumentProperties, whose elements are
// named after the properties, with spaces and other characters a name
// cannot hold written as _xHHHH_.
func readCustom2003(s *xmlscan.Scanner, res *Result) {
	m := &res.Metadata
	n := 0
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start || s.Depth() != 2 {
			continue
		}
		raw := unescapeName(s.Local())
		if len(raw) > maxCustomName {
			k := maxCustomName
			for k > 0 && !utf8.RuneStart(raw[k]) {
				k--
			}
			res.NamesCut++
			res.NameBytes += int64(len(raw) - k)
			raw = raw[:k]
		}
		name, removed := clean.String(raw, clean.Trim)
		res.Removed += removed
		name = strings.ToValidUTF8(name, "")
		v := value(s, res)
		if name == "" {
			continue
		}
		if n >= maxCustom {
			res.CustomPast++
			continue
		}
		n++
		extra(m, customPrefix+strings.ToLower(name), v)
	}
}

// unescapeName decodes _xHHHH_ in an element's name.
func unescapeName(b []byte) []byte {
	if !bytes.Contains(b, []byte("_x")) {
		return append([]byte(nil), b...)
	}
	var out []byte
	for i := 0; i < len(b); i++ {
		if i+7 <= len(b) && b[i] == '_' && b[i+1] == 'x' && b[i+6] == '_' {
			if v, err := strconv.ParseUint(string(b[i+2:i+6]), 16, 16); err == nil && v != 0 {
				out = utf8.AppendRune(out, rune(v))
				i += 6
				continue
			}
		}
		out = append(out, b[i])
	}
	return out
}
