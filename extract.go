package gofastooxml

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
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
//	input.ambiguous           the archive names a member twice, in two ways,
//	                          or has two members with one name; each member
//	                          is read under the name in its header, and the
//	                          first of two with one name is taken
//	text.tracked_changes      a Word document has tracked changes
//	text.hidden               text a reader cannot see; the blocks, spans
//	                          and table cells concerned say why
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

	// The codes below are raised while a Word document is read, and the
	// first two for workbooks and presentations as well.

	// WarnUnreadContent: the document holds content of a kind this version
	// does not read: a chart, a diagram, a sound or video clip.
	WarnUnreadContent = wml.WarnUnreadContent
	// WarnPartDamaged: a supporting part (styles, numbering, notes, a
	// header) could not be read to its end; what was read is used.
	WarnPartDamaged = wml.WarnPartDamaged
	// WarnTableFlattened: a table whose declared grid is far larger than
	// the cells it holds was written as lines of text, cells separated by
	// tabs, instead of as a table.
	WarnTableFlattened = wml.WarnTableFlattened

	// The codes below are raised while an Excel workbook is read.

	// WarnFormulaNoValue: cells hold formulas with no stored result. A
	// program that writes a workbook without calculating it leaves them
	// so. The cells are empty; formulas are not evaluated.
	WarnFormulaNoValue = warn.FormulaNoValue
	// WarnRegionSplit: an area of a sheet whose cells are spread so
	// thinly that the rectangle around them is mostly empty was not
	// written as one table. Each run of cells side by side in a row is a
	// table of one row instead.
	WarnRegionSplit = warn.RegionSplit

	// The codes below are raised when a password-protected file is opened.

	// WarnDecrypted: the file was encrypted and has been decrypted, with
	// the password given or with the password Excel uses when none is
	// set. The message names the scheme.
	WarnDecrypted = "ooxml.decrypted"
	// WarnIntegrityMismatch: the encrypted file carries a keyed hash of
	// itself, and the file is not what that hash was made of: it was
	// changed after it was encrypted, or damaged. What it decrypts to is
	// extracted all the same.
	WarnIntegrityMismatch = "ooxml.integrity_mismatch"

	// The code below is raised while a PowerPoint presentation is read.

	// WarnHiddenUnchecked: a slide has more overlapping shapes than the
	// hidden-text checks compare. Some of its text was not compared with
	// what lies under it (low_contrast) or over it (occluded), so the
	// absence of those flags on the slide says less than usual.
	WarnHiddenUnchecked = warn.HiddenUnchecked
)

// Extractor extracts Word, Excel and PowerPoint files. The zero value is
// ready to use and safe for concurrent use. Its fields are the settings
// that only this library has; everything else is in gofastcore.Options.
type Extractor struct {
	// MaxArchiveMembers bounds the number of files in the zip archive.
	// Zero means DefaultMaxArchiveMembers; negative means no limit.
	MaxArchiveMembers int
	// MaxCells bounds the cells kept from a workbook, over all its
	// sheets, or from the tables of a presentation, over all its slides.
	// A sheet or slide that reaches it is cut off there, with a
	// limit.exceeded warning. Zero means DefaultMaxCells; negative means
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
// as the format they are a variant of.
func (e *Extractor) Formats() []string { return []string{"docx", "xlsx", "pptx"} }

// Extract reads a Word, Excel or PowerPoint file with default settings. It
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
//   - ErrMalformed: not a zip archive, or a package too damaged to
//     identify.
//   - ErrUnsupported: a zip archive or package that is not a Word, Excel
//     or PowerPoint file; a legacy binary Office file; a file encrypted
//     in a way other than with a password.
//   - ErrPasswordRequired, ErrWrongPassword: the file is encrypted, and
//     Options.Password is empty or does not open it.
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
