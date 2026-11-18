package gofastooxml

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/props"
	"github.com/AOShei/go-fast-ooxml/internal/wml"
)

// Defaults for the limits of gofastcore.Options.Limits, used where a limit
// is zero. A negative limit means no limit.
const (
	// DefaultMaxInputBytes bounds the size of the file.
	DefaultMaxInputBytes = 1 << 30
	// DefaultMaxDecodedBytes bounds the decompressed bytes read from the
	// archive, over all its parts. It is what stops a decompression bomb.
	DefaultMaxDecodedBytes = 2 << 30
	// DefaultMaxArchiveMembers bounds the number of files in the archive.
	DefaultMaxArchiveMembers = 100_000
)

// Warning codes specific to this library. Each states a fact about the
// file. The codes shared by all go-fast libraries are in gofastcore, and
// this library raises these of them besides the ones about parts, limits
// and embedded documents:
//
//	input.active_content      the package contains a VBA project
//	input.external_reference  a remote template, a linked picture or object
//	input.ambiguous           the archive names a member twice, in two ways,
//	                          or has two members with one name; each member
//	                          is read under the name in its header, and the
//	                          first of two with one name is taken
//	text.tracked_changes      a Word document has tracked changes
//	text.hidden               text a reader cannot see
//	structure.heading_level_clamped
const (
	// WarnPropertiesDamaged: a document-properties part could not be read
	// to its end; the properties read before the damage are kept.
	WarnPropertiesDamaged = "ooxml.properties_damaged"
	// WarnPropertyTruncated: a property value was longer than 64 KiB and
	// was cut there, or there were more than 256 custom properties and the
	// rest were left out.
	WarnPropertyTruncated = "ooxml.property_truncated"
	// WarnControlCharsRemoved: control characters with no meaning as text
	// were removed from extracted strings.
	WarnControlCharsRemoved = "ooxml.control_characters_removed"
	// WarnNotImplemented: content extraction for this format is not
	// implemented in this version; only properties were read.
	WarnNotImplemented = "ooxml.not_implemented"

	// The codes below are raised while a Word document is read.

	// WarnUnreadContent: the document holds content of a kind this version
	// does not read: a chart, a diagram.
	WarnUnreadContent = wml.WarnUnreadContent
	// WarnPartDamaged: a supporting part (styles, numbering, notes, a
	// header) could not be read to its end; what was read is used.
	WarnPartDamaged = wml.WarnPartDamaged
	// WarnTableFlattened: a table whose declared grid is far larger than
	// the cells it holds was written as lines of text, cells separated by
	// tabs, instead of as a table.
	WarnTableFlattened = wml.WarnTableFlattened
)

// Extractor extracts Word, Excel and PowerPoint files. The zero value is
// ready to use and safe for concurrent use. Its fields are the settings
// that only this library has; everything else is in gofastcore.Options.
type Extractor struct {
	// MaxArchiveMembers bounds the number of files in the zip archive.
	// Zero means DefaultMaxArchiveMembers; negative means no limit.
	MaxArchiveMembers int
}

var _ gofastcore.Extractor = (*Extractor)(nil)

// Formats lists the formats this library extracts, as they appear in
// gofastcore.Source.Format. Macro-enabled files and templates are reported
// as the format they are a variant of.
func (e *Extractor) Formats() []string { return []string{"docx", "xlsx", "pptx"} }

// Extract reads a Word, Excel or PowerPoint file with default settings. It
// is (&Extractor{}).Extract.
func Extract(ctx context.Context, r io.ReaderAt, size int64, opts *gofastcore.Options) (*gofastcore.Document, error) {
	return (&Extractor{}).Extract(ctx, r, size, opts)
}

// ExtractFile opens the named file and extracts it. The file name is
// recorded as the document's source name unless opts.Name is set.
func ExtractFile(ctx context.Context, path string, opts *gofastcore.Options) (*gofastcore.Document, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	var o gofastcore.Options
	if opts != nil {
		o = *opts
	}
	if o.Name == "" {
		o.Name = path
	}
	return (&Extractor{}).Extract(ctx, f, info.Size(), &o)
}

// Extract reads size bytes from r and returns the document.
//
// The format is decided from the package itself, never from a file name.
// An error wraps one of the gofastcore errors:
//
//   - ErrMalformed: not a zip archive, or a package too damaged to
//     identify.
//   - ErrUnsupported: a zip archive or package that is not a Word, Excel
//     or PowerPoint file; an encrypted or legacy binary Office file.
//   - ErrLimitExceeded: the input is larger than Limits.MaxInputBytes, or
//     a limit was reached before anything could be read.
func (e *Extractor) Extract(ctx context.Context, r io.ReaderAt, size int64, opts *gofastcore.Options) (*gofastcore.Document, error) {
	if opts == nil {
		opts = &gofastcore.Options{}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit := gofastcore.ResolveLimit(opts.Limits.MaxInputBytes, DefaultMaxInputBytes); size > limit {
		return nil, fmt.Errorf("gofastooxml: input is %d bytes, limit is %d: %w", size, limit, gofastcore.ErrLimitExceeded)
	}
	if size < 4 {
		return nil, fmt.Errorf("gofastooxml: input is %d bytes: %w", size, gofastcore.ErrMalformed)
	}
	if reason := compoundFile(r, size); reason != "" {
		return nil, fmt.Errorf("gofastooxml: %s: %w", reason, gofastcore.ErrUnsupported)
	}

	members := int(gofastcore.ResolveLimit(int64(e.MaxArchiveMembers), DefaultMaxArchiveMembers))
	pkg, err := opc.Open(r, size, opc.Limits{
		MaxDecodedBytes: gofastcore.ResolveLimit(opts.Limits.MaxDecodedBytes, DefaultMaxDecodedBytes),
		MaxMembers:      members,
	})
	if err != nil {
		return nil, wrap(err)
	}
	main, format, mime, err := pkg.Main()
	if err != nil {
		return nil, wrap(err)
	}
	// A document of these formats that is not split into pages has one
	// part, or one per sheet or slide; a range must select at least one.
	if format == opc.Docx && !opts.Parts.Contains(1) {
		return nil, fmt.Errorf("gofastooxml: a Word document has one part, numbered 1: %w", gofastcore.ErrNoPartsSelected)
	}

	doc := gofastcore.New(string(format))
	doc.Source.MIME = mime
	doc.Source.Name = opts.Name
	doc.Source.Size = size
	// Invisible characters are checked for in everything that is
	// extracted. The other hidden-text checks arrive with the formats.
	doc.HiddenChecked = gofastcore.HiddenChars

	meta, err := props.Read(pkg)
	doc.Metadata = meta.Metadata
	for _, name := range meta.Damaged {
		doc.Warn(gofastcore.SeverityWarning, WarnPropertiesDamaged, clean.Label(name))
	}
	if meta.Truncated > 0 {
		doc.Warn(gofastcore.SeverityWarning, WarnPropertyTruncated, fmt.Sprintf("%d values", meta.Truncated))
	}
	if meta.Removed > 0 {
		doc.Warn(gofastcore.SeverityInfo, WarnControlCharsRemoved, fmt.Sprintf("%d in document properties", meta.Removed))
	}
	if hiddenInMetadata(&doc.Metadata) {
		doc.Warn(gofastcore.SeverityWarning, gofastcore.WarnTextHidden, "document properties contain invisible characters")
	}
	if n := pkg.Ambiguous(); n > 0 {
		doc.Warn(gofastcore.SeverityWarning, gofastcore.WarnInputAmbiguous,
			fmt.Sprintf("%d archive members are named twice or share a name; programs differ in which they read", n))
	}
	if pkg.HasContentType(vbaContentType) {
		doc.Warn(gofastcore.SeverityInfo, gofastcore.WarnInputActiveContent, "a VBA project (macros); it is not read")
	}
	if errors.Is(err, opc.ErrBudget) {
		doc.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "decompressed size limit reached while reading document properties")
	}

	switch format {
	case opc.Docx:
		wml.Extract(ctx, pkg, main, opts, doc)
	default:
		// Excel and PowerPoint: the container and the properties are read,
		// the content is not yet. The one part says so, as a failed part,
		// so that no consumer takes an empty document for an empty file.
		part := gofastcore.Part{Kind: partKinds[format], Number: 1}
		part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "content extraction is not implemented for this format in this version")
		part.MarkHidden()
		doc.Parts = append(doc.Parts, part)
		doc.Warn(gofastcore.SeverityError, WarnNotImplemented, "only document properties were read")
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return doc, nil
}

const vbaContentType = "application/vnd.ms-office.vbaProject"

var partKinds = map[opc.Format]gofastcore.PartKind{
	opc.Docx: gofastcore.PartBody,
	opc.Xlsx: gofastcore.PartSheet,
	opc.Pptx: gofastcore.PartSlide,
}

// wrap turns a container error into one that wraps the matching shared
// error.
func wrap(err error) error {
	shared := gofastcore.ErrMalformed
	switch {
	case errors.Is(err, opc.ErrNotPackage), errors.Is(err, opc.ErrNoMainPart), errors.Is(err, opc.ErrEncryptedMember):
		shared = gofastcore.ErrUnsupported
	case errors.Is(err, opc.ErrBudget), errors.Is(err, opc.ErrTooManyParts):
		shared = gofastcore.ErrLimitExceeded
	}
	// The message can quote names from the file; it is made safe to print
	// and to store, since a caller may put it into a document's warnings.
	return fmt.Errorf("gofastooxml: %s: %w", clean.Label(err.Error()), shared)
}

// hiddenInMetadata reports whether any property value holds invisible
// characters. Properties cannot carry a block flag, so the finding becomes
// a document-level warning.
func hiddenInMetadata(m *gofastcore.Metadata) bool {
	found := func(s string) bool { return gofastcore.InvisibleChars(s) != nil }
	if found(m.Title) || found(m.Subject) || found(m.Language) || found(m.Application) {
		return true
	}
	for _, s := range m.Authors {
		if found(s) {
			return true
		}
	}
	for _, s := range m.Keywords {
		if found(s) {
			return true
		}
	}
	for k, v := range m.Extra {
		if found(k) || found(v) {
			return true
		}
	}
	return false
}

var compoundSignature = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// encryptedPackage is the name of the stream that holds an encrypted Office
// Open XML file inside a compound file, in UTF-16.
var encryptedPackage = []byte("E\x00n\x00c\x00r\x00y\x00p\x00t\x00e\x00d\x00P\x00a\x00c\x00k\x00a\x00g\x00e\x00")

// compoundFile reports why the input cannot be read if it is a compound
// (OLE) file and not a zip archive, and "" otherwise. A password-protected
// Word, Excel or PowerPoint file is stored that way, and so are the legacy
// binary formats.
func compoundFile(r io.ReaderAt, size int64) string {
	var head [512]byte
	n, _ := r.ReadAt(head[:min(int64(len(head)), size)], 0)
	if n < len(compoundSignature) || !bytes.Equal(head[:len(compoundSignature)], compoundSignature) {
		return ""
	}
	const legacy = "compound file: a legacy binary Office format (.doc, .xls, .ppt), which this library does not read"
	if n < 52 {
		return legacy
	}
	// The directory lists the streams. Look for the encrypted package in
	// its first sectors.
	shift := binary.LittleEndian.Uint16(head[30:32])
	first := binary.LittleEndian.Uint32(head[48:52])
	if shift < 7 || shift > 16 || first > 1<<24 {
		return legacy
	}
	offset := (int64(first) + 1) << shift
	if offset >= size {
		return legacy
	}
	dir := make([]byte, min(int64(4)<<shift, size-offset))
	if m, _ := r.ReadAt(dir, offset); bytes.Contains(dir[:m], encryptedPackage) {
		return "encrypted Office document: password-protected files are not supported in this version"
	}
	return legacy
}
