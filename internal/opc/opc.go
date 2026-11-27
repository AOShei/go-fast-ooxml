// Package opc is what is Office's own about a package, on go-fast-core's
// opc, which reads the container (the zip archive, the content types, the
// relationships, the limits on decompression): which part is the document
// and of what kind (Main), a Visio package that has no table of content
// types, and the same package written as one XML document (Flat OPC,
// flat.go) or made of the elements of an Office 2003 file (NewSections).
//
// A Package is go-fast-core's, with these beside it; the names the readers
// use of core's package are given here too, so that they have one opc.
package opc

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/AOShei/go-fast-core/opc"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
)

// Errors of go-fast-core's opc, which this package returns as they are.
var (
	ErrNotZip          = opc.ErrNotZip
	ErrCutOff          = opc.ErrCutOff
	ErrDamaged         = opc.ErrDamaged
	ErrNotFound        = opc.ErrNotFound
	ErrBudget          = opc.ErrBudget
	ErrTooManyParts    = opc.ErrTooManyParts
	ErrEncryptedMember = opc.ErrEncryptedMember
)

// Errors of this package.
var (
	// ErrNotPackage: a zip archive with no content-types table, so some
	// other zip-based format.
	ErrNotPackage = errors.New("opc: zip archive is not an Office Open XML package")
	// ErrNoMainPart: a package with no Word, Excel, PowerPoint or Visio
	// main part.
	ErrNoMainPart = errors.New("opc: package holds no Word, Excel, PowerPoint or Visio document")
)

// Limits bounds what a package may cost (go-fast-core's opc.Limits).
type Limits = opc.Limits

// Rel is one relationship of a part or of the package (go-fast-core's
// opc.Rel).
type Rel = opc.Rel

// AheadAt is the size from which ScanPart reads a part in chunks
// (go-fast-core's opc.AheadAt).
const AheadAt = opc.AheadAt

// Spare reports whether a reader given this many workers has one to spare
// for reading ahead (go-fast-core's opc.Spare).
func Spare(workers int) bool { return opc.Spare(workers) }

// RelsName returns the name of the relationships part of source ("" for
// the package).
func RelsName(source string) string { return opc.RelsName(source) }

// Canonical returns the form of a part name used as a key.
func Canonical(name string) string { return opc.Canonical(name) }

// Package is an opened Office package: go-fast-core's, with what is
// Office's own beside it. It is safe for concurrent use.
type Package struct {
	*opc.Package

	flat       bool        // a Flat OPC package, or the sections of an Office 2003 file
	sections   bool        // the parts are elements of a file with no package (NewSections)
	flatDamage *FlatDamage // where a Flat OPC package breaks off
	untyped    bool        // a Visio package with no content-types table, known by its relationships
	refs       *Refs       // where the parts' references to no character are counted
}

// Open reads the archive's directory and the content-types table.
//
// A zip archive with no table is read when it was recovered (some programs
// write the table last, and it is lost with the end of the file; the
// document is then known by where its main part is, and the parts by the
// relationships that lead to them), and when it is a Visio package whose
// relationships lead to its drawing, as some programs write them. Any
// other is ErrNotPackage, naming the format it says it is in a mimetype
// member, as OpenDocument and EPUB do.
func Open(r io.ReaderAt, size int64, lim Limits) (*Package, error) {
	cp, err := opc.Open(r, size, lim)
	if err != nil {
		return nil, err
	}
	p := &Package{Package: cp}
	switch {
	case cp.Typed(), cp.Recovered() != nil:
		return p, nil
	case p.visioWithoutTypes():
		p.untyped = true
		return p, nil
	}
	if kind := cp.Mimetype(); strings.HasPrefix(kind, "application/") {
		return nil, fmt.Errorf("%w: it is %s", ErrNotPackage, kind)
	}
	return nil, ErrNotPackage
}

// visioWithoutTypes reports whether the package's relationships lead to
// a Visio drawing that is in the archive: what makes a zip archive with
// no table of content types a Visio package.
func (p *Package) visioWithoutTypes() bool {
	rels, err := p.Rels("")
	if err != nil {
		return false
	}
	for _, r := range rels {
		if !r.External && r.Type == RelVisioDocument && p.Has(r.Target) {
			return true
		}
	}
	return false
}

// Untyped reports whether the package is a Visio package with no table of
// content types, read by its relationships.
func (p *Package) Untyped() bool { return p.untyped }

// Format is one of the document families.
type Format string

const (
	Docx Format = "docx"
	Xlsx Format = "xlsx"
	Pptx Format = "pptx"
	Vsdx Format = "vsdx"
)

// RelVisioDocument is the type of the relationship from a Visio package to
// its drawing, in place of the officeDocument relationship of the other
// formats.
const RelVisioDocument = "http://schemas.microsoft.com/visio/2010/relationships/document"

type kind struct {
	format Format
	mime   string
}

// mainKinds maps the content type of a main part to the family and to the
// media type of a file of that kind.
var mainKinds = map[string]kind{
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml":   {Docx, "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
	"application/vnd.openxmlformats-officedocument.wordprocessingml.template.main+xml":   {Docx, "application/vnd.openxmlformats-officedocument.wordprocessingml.template"},
	"application/vnd.ms-word.document.macroEnabled.main+xml":                             {Docx, "application/vnd.ms-word.document.macroEnabled.12"},
	"application/vnd.ms-word.template.macroEnabledTemplate.main+xml":                     {Docx, "application/vnd.ms-word.template.macroEnabled.12"},
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml":         {Xlsx, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
	"application/vnd.openxmlformats-officedocument.spreadsheetml.template.main+xml":      {Xlsx, "application/vnd.openxmlformats-officedocument.spreadsheetml.template"},
	"application/vnd.ms-excel.sheet.macroEnabled.main+xml":                               {Xlsx, "application/vnd.ms-excel.sheet.macroEnabled.12"},
	"application/vnd.ms-excel.template.macroEnabled.main+xml":                            {Xlsx, "application/vnd.ms-excel.template.macroEnabled.12"},
	"application/vnd.ms-excel.sheet.binary.macroEnabled.main":                            {Xlsx, "application/vnd.ms-excel.sheet.binary.macroEnabled.12"},
	"application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml": {Pptx, "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
	"application/vnd.openxmlformats-officedocument.presentationml.slideshow.main+xml":    {Pptx, "application/vnd.openxmlformats-officedocument.presentationml.slideshow"},
	"application/vnd.openxmlformats-officedocument.presentationml.template.main+xml":     {Pptx, "application/vnd.openxmlformats-officedocument.presentationml.template"},
	"application/vnd.ms-powerpoint.presentation.macroEnabled.main+xml":                   {Pptx, "application/vnd.ms-powerpoint.presentation.macroEnabled.12"},
	"application/vnd.ms-powerpoint.slideshow.macroEnabled.main+xml":                      {Pptx, "application/vnd.ms-powerpoint.slideshow.macroEnabled.12"},
	"application/vnd.ms-powerpoint.template.macroEnabled.main+xml":                       {Pptx, "application/vnd.ms-powerpoint.template.macroEnabled.12"},
	"application/vnd.ms-visio.drawing.main+xml":                                          {Vsdx, "application/vnd.ms-visio.drawing"},
	"application/vnd.ms-visio.drawing.macroEnabled.main+xml":                             {Vsdx, "application/vnd.ms-visio.drawing.macroEnabled.12"},
	"application/vnd.ms-visio.template.main+xml":                                         {Vsdx, "application/vnd.ms-visio.template"},
	"application/vnd.ms-visio.template.macroEnabled.main+xml":                            {Vsdx, "application/vnd.ms-visio.template.macroEnabled.12"},
	"application/vnd.ms-visio.stencil.main+xml":                                          {Vsdx, "application/vnd.ms-visio.stencil"},
	"application/vnd.ms-visio.stencil.macroEnabled.main+xml":                             {Vsdx, "application/vnd.ms-visio.stencil.macroEnabled.12"},
}

// conventional are the usual locations of the main part, used when a
// package's content types or relationships do not identify it.
var conventional = []struct {
	name string
	kind
}{
	{"word/document.xml", mainKinds["application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"]},
	{"xl/workbook.xml", mainKinds["application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"]},
	{"xl/workbook.bin", mainKinds["application/vnd.ms-excel.sheet.binary.macroEnabled.main"]},
	{"ppt/presentation.xml", mainKinds["application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"]},
	{"visio/document.xml", mainKinds["application/vnd.ms-visio.drawing.main+xml"]},
}

// Main finds the main part of the package and says what kind of document
// it is.
//
// It follows the package's officeDocument relationship and reads the
// target's content type, which is how Office itself decides. A package
// whose relationship or content type is missing or wrong is still
// identified when a main part sits in its conventional place.
func (p *Package) Main() (part string, format Format, mime string, err error) {
	rels, relErr := p.Rels("")
	if errors.Is(relErr, ErrBudget) {
		return "", "", "", relErr
	}
	for _, r := range rels {
		if r.External || !(ns.IsRel(r.Type, ns.RelOfficeDocument) || r.Type == RelVisioDocument) || !p.Has(r.Target) {
			continue
		}
		if k, ok := mainKinds[p.ContentType(r.Target)]; ok {
			return r.Target, k.format, k.mime, nil
		}
		// The relationship is right and the content type is not one of
		// ours: go by where the part is.
		for _, c := range conventional {
			if r.Target == c.name {
				return r.Target, c.format, c.mime, nil
			}
		}
	}
	// No usable relationship. A declared main part still identifies the file.
	for _, name := range p.Names() {
		if ct, ok := p.Override(name); ok {
			if k, ok := mainKinds[ct]; ok {
				return name, k.format, k.mime, nil
			}
		}
	}
	for _, c := range conventional {
		if p.Has(c.name) {
			return c.name, c.format, c.mime, nil
		}
	}
	return "", "", "", ErrNoMainPart
}
