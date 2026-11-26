package gofastooxml

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
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
	// DefaultMaxCells bounds the cells kept from a workbook, over all its
	// sheets. Each cell kept costs memory in the result whatever the size
	// of the file, so this is what bounds the memory a workbook can ask
	// for.
	DefaultMaxCells = 20_000_000
	// DefaultMaxSpinCount bounds the rounds of hashing an encrypted file
	// may ask for to turn a password into a key. Office writes 100,000;
	// the number is the file's to choose, and each round costs time.
	DefaultMaxSpinCount = 1_000_000
)

// Warning codes specific to this library. Each states a fact about the
// file. The codes shared by all go-fast libraries are in gofastcore, and
// this library raises these of them besides the ones about parts, limits
// and embedded documents:
//
//	input.active_content      the package contains a VBA project, a sheet of
//	                          Excel 4.0 macros, or an action on a slide
//	                          that starts a program or runs a macro
//	input.external_reference  a remote template, a linked picture, object
//	                          or media clip, a link to another workbook
//	input.repaired            the archive's directory could not be read (the
//	                          file is cut short); the members were found
//	                          by their own headers, as far as they go
//	input.ambiguous           the archive names a member twice, in two ways,
//	                          or has two members with one name; each member
//	                          is read under the name in its header, and the
//	                          first of two with one name is taken
//	text.tracked_changes      a Word document has tracked changes
//	input.trailing_data       data follows the end of the zip archive; its
//	                          text is in the stored part (after_end)
//	structure.heading_level_clamped
//	text.control_characters_removed
//	                          control characters with no meaning as text
//	                          were removed from extracted strings
//	input.unread_content      the document holds content of a kind this
//	                          version does not read: a sound or video
//	                          clip, an Excel dialog sheet, a formula of a
//	                          binary workbook that cannot be read back
//	input.stream_damaged      a supporting part (styles, numbering, notes, a
//	                          header), the document properties or an
//	                          embedded file could not be read to the end,
//	                          or a drawing names a chart or diagram the
//	                          document does not have; what was read is used
//	text.unmapped_glyphs      characters of a symbol font, or of macro code
//	                          in a code page, with no character in Unicode
//	                          appear as U+FFFD
//	table.formula_no_value    cells hold formulas with no stored result
//	table.region_split        an area of a sheet too thinly filled to be a
//	                          table was written a row at a time
//	input.decrypted           the file was encrypted and has been decrypted;
//	                          the message names the scheme and what it was
//	                          decrypted with (Metadata.Encrypted is set)
const (
	// WarnTableFlattened: a table whose declared grid is far larger than
	// the cells it holds was written as lines of text, cells separated by
	// tabs, instead of as a table. Raised while a Word document is read.
	WarnTableFlattened = wml.WarnTableFlattened
	// WarnIntegrityMismatch: the encrypted file carries a keyed hash of
	// itself, and the file is not what that hash was made of: it was
	// changed after it was encrypted, or damaged. What it decrypts to is
	// extracted all the same.
	WarnIntegrityMismatch = "ooxml.integrity_mismatch"
)

// Names of codes this library raised under its own prefix before
// go-fast-core v0.1.2 made them shared. Each is now the shared code, so a
// test such as w.Code == WarnPartDamaged still matches; the code strings
// in the output changed with them. They will be removed in a later
// release.
const (
	// WarnPropertiesDamaged was ooxml.properties_damaged.
	//
	// Deprecated: use gofastcore.WarnInputStreamDamaged; the message names
	// the document properties.
	WarnPropertiesDamaged = gofastcore.WarnInputStreamDamaged
	// WarnPropertyTruncated was raised for a property value cut at 64 KiB,
	// and for custom properties past 256.
	//
	// Deprecated: those cuts are now limit.exceeded
	// (gofastcore.WarnLimitExceeded), severity error, as every other bound
	// reports what it cuts; this code is no longer raised.
	WarnPropertyTruncated = "ooxml.property_truncated"
	// WarnControlCharsRemoved was ooxml.control_characters_removed.
	//
	// Deprecated: use gofastcore.WarnTextControlCharactersRemoved.
	WarnControlCharsRemoved = gofastcore.WarnTextControlCharactersRemoved
	// WarnUnreadContent was ooxml.unread_content.
	//
	// Deprecated: use gofastcore.WarnInputUnreadContent.
	WarnUnreadContent = gofastcore.WarnInputUnreadContent
	// WarnPartDamaged was ooxml.part_damaged.
	//
	// Deprecated: use gofastcore.WarnInputStreamDamaged.
	WarnPartDamaged = gofastcore.WarnInputStreamDamaged
	// WarnFormulaNoValue was ooxml.formula_no_value.
	//
	// Deprecated: use gofastcore.WarnTableFormulaNoValue.
	WarnFormulaNoValue = gofastcore.WarnTableFormulaNoValue
	// WarnRegionSplit was ooxml.region_split.
	//
	// Deprecated: use gofastcore.WarnTableRegionSplit.
	WarnRegionSplit = gofastcore.WarnTableRegionSplit
	// WarnDecrypted was ooxml.decrypted.
	//
	// Deprecated: use gofastcore.WarnInputDecrypted, or test
	// Metadata.Encrypted.
	WarnDecrypted = gofastcore.WarnInputDecrypted
)

// Extractor extracts Word, Excel, PowerPoint and Visio files. The zero value is
// ready to use and safe for concurrent use. Its fields are the settings
// that only this library has; everything else is in gofastcore.Options.
type Extractor struct {
	// MaxArchiveMembers bounds the number of files in the zip archive.
	// Zero means DefaultMaxArchiveMembers; negative means no limit.
	MaxArchiveMembers int
	// MaxCells bounds the cells kept from a workbook, over all its
	// sheets, from the tables of a presentation, over all its slides, or
	// from the shape data and data recordsets of a Visio drawing. A sheet
	// or slide that reaches it is cut off there, and so is a page's
	// shape data or the drawing's recordsets, with a limit.exceeded
	// warning. Zero means DefaultMaxCells; negative means
	// no limit.
	MaxCells int64
	// MaxSpinCount bounds the rounds of hashing a password-protected
	// file may ask for when a password is tried. A file that asks for
	// more is refused with ErrLimitExceeded. Zero means
	// DefaultMaxSpinCount; negative means no limit.
	MaxSpinCount int
}

var _ gofastcore.Extractor = (*Extractor)(nil)

// Formats lists the formats this library extracts, as they appear in
// gofastcore.Source.Format. Macro-enabled files and templates are reported
// as the format they are a variant of, a binary workbook (.xlsb) as
// "xlsx", and every kind of Visio package (drawings, templates and
// stencils, with macros or without) as "vsdx".
func (e *Extractor) Formats() []string {
	return []string{"docx", "xlsx", "pptx", "vsdx", formatSpreadsheetML, formatWordML}
}

// Extract reads a Word, Excel, PowerPoint or Visio file with default settings. It
// is (&Extractor{}).Extract.
func Extract(ctx context.Context, r io.ReaderAt, size int64, opts *gofastcore.Options) (*gofastcore.Document, error) {
	return (&Extractor{}).Extract(ctx, r, size, opts)
}

// ExtractFile opens the named file and extracts it, with default
// settings. It is (&Extractor{}).ExtractFile.
func ExtractFile(ctx context.Context, path string, opts *gofastcore.Options) (*gofastcore.Document, error) {
	return (&Extractor{}).ExtractFile(ctx, path, opts)
}

// ExtractFile opens the named file and extracts it. The file name is
// recorded as the document's source name unless opts.Name is set.
func (e *Extractor) ExtractFile(ctx context.Context, path string, opts *gofastcore.Options) (*gofastcore.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := e.openFile(ctx, path, opts)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Document(ctx)
}

// Extract reads size bytes from r and returns the document.
//
// The format is decided from the package itself, never from a file name.
// An error wraps one of the gofastcore errors:
//
//   - ErrMalformed: neither a zip archive nor XML, or a package too
//     damaged to identify.
//   - ErrUnsupported: a zip archive or package that is not a Word, Excel
//     or PowerPoint file; XML that is not one of the Office formats saved
//     as XML; a legacy binary Office file; a file encrypted in a way other
//     than with a password or for a certificate.
//   - ErrPasswordRequired, ErrWrongPassword: the file is encrypted, and
//     Options.Password is empty or does not open it.
//   - ErrKeyRequired: the file's key is also encrypted for certificates,
//     and no key in Options.Keys is the private key of one of them. The
//     error wraps it beside ErrPasswordRequired or ErrWrongPassword when
//     a password would open the file too.
//   - ErrLimitExceeded: the input is larger than Limits.MaxInputBytes, or
//     a limit was reached before anything could be read.
//   - ErrNoPartsSelected: Options.Parts selects none of the document's
//     parts.
//
// It is OpenReaderAt followed by Document.
func (e *Extractor) Extract(ctx context.Context, r io.ReaderAt, size int64, opts *gofastcore.Options) (*gofastcore.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := e.open(ctx, r, size, opts)
	if err != nil {
		return nil, err
	}
	return f.Document(ctx)
}

const vbaContentType = "application/vnd.ms-office.vbaProject"

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
