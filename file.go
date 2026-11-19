package gofastooxml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/media"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/pml"
	"github.com/AOShei/go-fast-ooxml/internal/props"
	"github.com/AOShei/go-fast-ooxml/internal/sml"
	"github.com/AOShei/go-fast-ooxml/internal/unit"
	"github.com/AOShei/go-fast-ooxml/internal/wml"
)

// File is a Word, Excel or PowerPoint file opened for reading a part at a
// time: a Word document's one body, a workbook's sheets, a presentation's
// slides. Opening a file identifies it and reads its properties and what
// its parts share; the parts are read when they are asked for, so a
// caller that handles one part and lets it go holds little at a time
// however large the file is.
//
// The parts are the same as those in the document that Extract returns,
// each complete: its warnings are on it and its hidden text is flagged
// (Part.MarkHidden has been called).
//
// A File is safe for concurrent use. A limit of Options.Limits counts
// over one pass: one call of Document, Part or Children, or one run
// through Parts or Rows. Passes that run at the same time share it.
type File struct {
	e        Extractor
	opts     gofastcore.Options
	r        io.ReaderAt // the file
	size     int64
	body     io.ReaderAt // the package: the file, or what it decrypts to
	bodySize int64
	closer   io.Closer
	source   gofastcore.Source
	meta     gofastcore.Metadata
	opened   []gofastcore.Warning // warnings about the document known when it was opened
	within   int64                // for a file embedded in a document: the bytes that document has left to decompress; 0 otherwise
	checked  gofastcore.Hidden
	parts    int
	stored   int // the index of the stored part, or -1
	noun     string
	sheets   []Sheet

	mu      sync.Mutex
	sess    *session            // the reading in use
	clean   bool                // no limit has cut it short, so it can be used again
	tallies map[int]*unit.Tally // what the parts read so far found out, by index
}

// session is one opening of the package: its archive, with its
// decompression budget, and the format's reader on it.
type session struct {
	pkg *opc.Package
	src unit.Source
}

// Sheet is a sheet of a workbook as the workbook lists it.
type Sheet struct {
	// Number is the sheet's part number, from 1.
	Number int
	// Name is the sheet's name.
	Name string
	// Hidden says the workbook marks the sheet hidden.
	Hidden bool
}

// Open opens the named file with default settings. It is
// (&Extractor{}).Open.
func Open(path string, opts *gofastcore.Options) (*File, error) {
	return (&Extractor{}).Open(path, opts)
}

// OpenBytes opens a file held in memory with default settings.
func OpenBytes(data []byte, opts *gofastcore.Options) (*File, error) {
	return (&Extractor{}).OpenBytes(data, opts)
}

// OpenReaderAt opens a file of the given size read through r, with
// default settings.
func OpenReaderAt(r io.ReaderAt, size int64, opts *gofastcore.Options) (*File, error) {
	return (&Extractor{}).OpenReaderAt(r, size, opts)
}

// Open opens the named file. The file name is recorded as the source
// name unless opts.Name is set. Close the File when done with it.
func (e *Extractor) Open(path string, opts *gofastcore.Options) (*File, error) {
	return e.openFile(context.Background(), path, opts)
}

func (e *Extractor) openFile(ctx context.Context, path string, opts *gofastcore.Options) (*File, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	var o gofastcore.Options
	if opts != nil {
		o = *opts
	}
	if o.Name == "" {
		o.Name = path
	}
	f, err := e.open(ctx, file, info.Size(), &o)
	if err != nil {
		file.Close()
		return nil, err
	}
	f.closer = file
	return f, nil
}

// OpenBytes opens a file held in memory. The bytes must not change while
// the File is in use.
func (e *Extractor) OpenBytes(data []byte, opts *gofastcore.Options) (*File, error) {
	return e.OpenReaderAt(bytes.NewReader(data), int64(len(data)), opts)
}

// OpenReaderAt opens a file of the given size read through r, which must
// stay readable until the File is closed. The errors are those of
// Extract.
//
// A password-protected file is opened with Options.Password. Trying a
// password takes as long as the file's encryption is made to take: a
// hundredth of a second is usual.
func (e *Extractor) OpenReaderAt(r io.ReaderAt, size int64, opts *gofastcore.Options) (*File, error) {
	return e.open(context.Background(), r, size, opts)
}

func (e *Extractor) open(ctx context.Context, r io.ReaderAt, size int64, opts *gofastcore.Options) (*File, error) {
	f := &File{r: r, size: size, body: r, bodySize: size, tallies: map[int]*unit.Tally{}}
	if e != nil {
		f.e = *e
	}
	if opts != nil {
		f.opts = *opts
	}
	if r == nil {
		return nil, fmt.Errorf("gofastooxml: no input to read: %w", gofastcore.ErrMalformed)
	}
	// A file embedded in a document may decompress what that document
	// has left to decompress, and no more: the limit is the document's
	// and its embedded files' together.
	if parent := gofastcore.DecodeBudgetFrom(ctx); parent != nil {
		f.within = max(parent.Left(), 1)
	}
	if limit := gofastcore.ResolveLimit(f.opts.Limits.MaxInputBytes, DefaultMaxInputBytes); size > limit {
		return nil, fmt.Errorf("gofastooxml: input is %d bytes, limit is %d: %w", size, limit, gofastcore.ErrLimitExceeded)
	}
	if size < 4 {
		return nil, fmt.Errorf("gofastooxml: input is %d bytes: %w", size, gofastcore.ErrMalformed)
	}
	// A compound file is a password-protected package, which is opened
	// here, or a file of the formats before these, which is not read.
	if err := f.unlock(ctx); err != nil {
		return nil, err
	}
	sess, err := f.open(true)
	if err != nil {
		return nil, err
	}
	f.sess, f.clean = sess, !sess.src.Limited()
	f.parts, f.noun, f.checked = sess.src.Len(), sess.src.Noun(), sess.src.HiddenChecked()
	f.stored = sess.src.Stored()
	if book, ok := sess.src.(*sml.Book); ok {
		for i, sh := range book.Sheets() {
			f.sheets = append(f.sheets, Sheet{Number: i + 1, Name: sh.Name, Hidden: sh.Hidden})
		}
	}
	return f, nil
}

// open opens the package and the format's reader on it. first says this
// is the opening of the File, which keeps what is found out about the
// document; a later opening goes through the same steps, so that it
// meets every limit where the first did.
func (f *File) open(first bool) (*session, error) {
	opts := &f.opts
	members := int(gofastcore.ResolveLimit(int64(f.e.MaxArchiveMembers), DefaultMaxArchiveMembers))
	decoded := gofastcore.ResolveLimit(opts.Limits.MaxDecodedBytes, DefaultMaxDecodedBytes)
	if f.within > 0 {
		decoded = min(decoded, f.within)
	}
	pkg, err := opc.Open(f.body, f.bodySize, opc.Limits{MaxDecodedBytes: decoded, MaxMembers: members})
	if err != nil {
		return nil, wrap(err)
	}
	main, format, mime, err := pkg.Main()
	if err != nil {
		if pkg.Recovered() != nil {
			err = opc.ErrCutOff // the part that says what the file is was lost with its end
		}
		return nil, wrap(err)
	}
	meta, err := props.Read(pkg)
	if first {
		f.source = gofastcore.Source{Format: string(format), MIME: mime, Name: opts.Name, Size: f.size}
		f.meta = meta.Metadata
		warn := func(severity gofastcore.Severity, code, message string) {
			f.opened = append(f.opened, gofastcore.Warning{Code: code, Severity: severity, Message: message})
		}
		for _, name := range meta.Damaged {
			warn(gofastcore.SeverityWarning, WarnPropertiesDamaged, clean.Label(name))
		}
		if meta.Truncated > 0 {
			warn(gofastcore.SeverityWarning, WarnPropertyTruncated, fmt.Sprintf("%d values", meta.Truncated))
		}
		if meta.Removed > 0 {
			warn(gofastcore.SeverityInfo, WarnControlCharsRemoved, fmt.Sprintf("%d in document properties", meta.Removed))
		}
		if hiddenInMetadata(&f.meta) {
			warn(gofastcore.SeverityWarning, gofastcore.WarnTextHidden, "document properties contain invisible characters")
		}
		if rec := pkg.Recovered(); rec != nil {
			message := fmt.Sprintf("the archive's directory cannot be read: the file is cut short or damaged. %d members were found by their own headers", rec.Members)
			if rec.Cut != "" {
				message += "; the last, " + clean.Label(rec.Cut) + ", breaks off"
			} else {
				message += "; any that followed are lost"
			}
			warn(gofastcore.SeverityWarning, gofastcore.WarnInputRepaired, message)
		}
		if n := pkg.Ambiguous(); n > 0 {
			warn(gofastcore.SeverityWarning, gofastcore.WarnInputAmbiguous,
				fmt.Sprintf("%d archive members are named twice or share a name; programs differ in which they read", n))
		}
		if pkg.HasContentType(vbaContentType) {
			warn(gofastcore.SeverityInfo, gofastcore.WarnInputActiveContent, "a VBA project (macros); its code is read as text into the stored part, and none of it is run")
		}
		if errors.Is(err, opc.ErrBudget) {
			warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "decompressed size limit reached while reading document properties")
		}
	}
	sess := &session{pkg: pkg}
	switch format {
	case opc.Docx:
		sess.src = wml.Open(pkg, main, opts)
	case opc.Xlsx:
		sess.src = sml.Open(pkg, main, opts, sml.Config{
			MaxCells: gofastcore.ResolveLimit(f.e.MaxCells, DefaultMaxCells),
		})
	default:
		sess.src = pml.Open(pkg, main, opts, pml.Config{
			MaxCells: gofastcore.ResolveLimit(f.e.MaxCells, DefaultMaxCells),
		})
	}
	return sess, nil
}

// Close closes the file if Open opened it. The File must not be used
// afterwards.
func (f *File) Close() error {
	if f.closer == nil {
		return nil
	}
	return f.closer.Close()
}

// Format returns the kind of file: "docx", "xlsx" or "pptx". A
// macro-enabled file or a template is the format it is a variant of.
func (f *File) Format() string { return f.source.Format }

// Source returns what is known of where the document came from, as in
// gofastcore.Document.Source.
func (f *File) Source() gofastcore.Source { return f.source }

// Metadata returns the document's properties.
func (f *File) Metadata() gofastcore.Metadata { return f.meta }

// NumParts returns the number of parts: 1 for a Word document, the
// number of sheets of a workbook, the number of slides of a
// presentation. A workbook or presentation with none has one part, which
// says so. A document that stores what it shows nowhere (defined names,
// macro code) has one more, last, of kind gofastcore.PartStored; it comes
// with the whole document, not with a range of parts.
func (f *File) NumParts() int { return f.parts }

// Sheets lists the sheets of a workbook in order, without reading them.
// It is empty for other formats.
func (f *File) Sheets() []Sheet { return append([]Sheet(nil), f.sheets...) }

// HiddenChecked returns the reasons for hidden text that are checked
// completely for this file's format, as in
// gofastcore.Document.HiddenChecked. It is known when the file is opened
// and does not change.
func (f *File) HiddenChecked() gofastcore.Hidden { return f.checked }

// acquire returns a reading to make a pass with: the one in use if no
// limit has cut it short, otherwise a new one, since what a reading
// keeps after meeting a limit is what the limit left of it.
func (f *File) acquire() (*session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.clean {
		sess, err := f.open(false)
		if err != nil {
			return nil, err
		}
		f.sess, f.clean = sess, true
	}
	return f.sess, nil
}

// spoil records that a limit has cut a reading short.
func (f *File) spoil(sess *session) {
	f.mu.Lock()
	if f.sess == sess {
		f.clean = false
	}
	f.mu.Unlock()
}

// selection returns the indexes of the parts Options.Parts asks for, cut
// to Options.Limits.MaxParts.
func (f *File) selection() (selected []int, asked int, err error) {
	whole := f.opts.Parts == (gofastcore.PartRange{})
	for i := 0; i < f.parts; i++ {
		// The stored part comes with the whole document only.
		if i == f.stored && !whole {
			continue
		}
		if f.opts.Parts.Contains(i + 1) {
			selected = append(selected, i)
		}
	}
	if len(selected) == 0 {
		return nil, 0, fmt.Errorf("gofastooxml: the %s has %d %s, numbered from 1: %w", f.kind(), f.parts, f.noun, gofastcore.ErrNoPartsSelected)
	}
	asked = len(selected)
	if limit := f.opts.Limits.MaxParts; limit > 0 && asked > limit {
		selected = selected[:limit]
	}
	return selected, asked, nil
}

// kind names the kind of document.
func (f *File) kind() string {
	switch f.source.Format {
	case "xlsx":
		return "workbook"
	case "pptx":
		return "presentation"
	}
	return "Word document"
}

// pass reads the parts with the given indexes and hands each to yield,
// in order, until yield returns false. window is how many parts may be
// read ahead of the one being handed on; zero means all of them.
//
// With more than one worker, which parts are read before a limit is
// reached depends on which worker gets there first. What a limit leaves
// out must not, so when one is met the parts are read again in order on
// a reading of their own, and those already handed on, which are the
// same in both, are passed over.
func (f *File) pass(ctx context.Context, selected []int, window int, yield func(index int, res *unit.Result) bool) (*session, error) {
	sess, err := f.acquire()
	if err != nil {
		return nil, err
	}
	sess.src.Begin()
	gofastcore.TextBudgetFrom(ctx).Reset()
	workers := f.opts.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	workers = min(workers, len(selected))
	if workers <= 1 {
		limited := false
		for _, index := range selected {
			res := sess.src.Read(ctx, index)
			if err := ctx.Err(); err != nil {
				return sess, err
			}
			limited = limited || res.Limited || sess.src.Limited()
			if limited {
				f.spoil(sess)
			}
			if !yield(index, res) {
				break
			}
		}
		return sess, nil
	}
	if window <= 0 {
		window = len(selected)
	}
	handed := 0
	for handed < len(selected) {
		batch := selected[handed:min(handed+window, len(selected))]
		results := make([]*unit.Result, len(batch))
		var wg sync.WaitGroup
		var next atomic.Int64
		for w := 0; w < min(workers, len(batch)); w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					k := int(next.Add(1)) - 1
					if k >= len(batch) || ctx.Err() != nil {
						return
					}
					results[k] = sess.src.Read(ctx, batch[k])
				}
			}()
		}
		wg.Wait()
		if err := ctx.Err(); err != nil {
			return sess, err
		}
		limited := sess.src.Limited()
		for _, res := range results {
			limited = limited || res.Limited
		}
		if limited {
			f.spoil(sess)
			return f.inOrder(ctx, selected, handed, yield)
		}
		for k, res := range results {
			handed++
			if !yield(batch[k], res) {
				return sess, nil
			}
		}
	}
	return sess, nil
}

// inOrder reads the parts one after another on a reading of its own,
// and hands on those after the first handed, which were handed on
// before.
func (f *File) inOrder(ctx context.Context, selected []int, handed int, yield func(index int, res *unit.Result) bool) (*session, error) {
	sess, err := f.open(false)
	if err != nil {
		return nil, err
	}
	// It has met the limit too by the end, so it is kept only for what
	// it knows of the document, not to read with again.
	f.mu.Lock()
	f.sess, f.clean = sess, false
	f.mu.Unlock()
	// The reading starts over: what the first attempt took of the text
	// the document may hold is given back.
	sess.src.Begin()
	gofastcore.TextBudgetFrom(ctx).Reset()
	for k, index := range selected {
		res := sess.src.Read(ctx, index)
		if err := ctx.Err(); err != nil {
			return sess, err
		}
		if k >= handed && !yield(index, res) {
			break
		}
	}
	return sess, nil
}

// once leaves the bytes of a picture with the first block that shows it
// among the parts it is given, in order, and takes them from the others.
type once map[string]bool

func (seen once) keep(part *gofastcore.Part) {
	for i := range part.Blocks {
		img := part.Blocks[i].Image
		if img == nil || img.Data == nil {
			continue
		}
		if seen[img.ID] {
			img.Data = nil
		}
		seen[img.ID] = true
	}
}

// record keeps what reading a part found out about the document. Most
// parts find out nothing, and of those nothing is kept: what a File holds
// on to does not grow with the parts read.
func (f *File) record(index int, t *unit.Tally) {
	// Of its relationships only those that embed a file are needed.
	var embeds []opc.Rel
	for _, rel := range t.Rels {
		if !rel.External && ns.IsRel(rel.Type, ns.RelOLEObject, ns.RelPackage, ns.RelAltChunk) {
			embeds = append(embeds, rel)
		}
	}
	t.Rels = embeds
	empty := len(t.Unread) == 0 && len(t.Damaged) == 0 && t.External == 0 && t.Programs == 0 && !t.Macros && !t.Limited &&
		len(t.Rels) == 0 && len(t.Warnings) == 0 && len(t.Children) == 0
	f.mu.Lock()
	if empty {
		delete(f.tallies, index)
	} else {
		f.tallies[index] = t
	}
	f.mu.Unlock()
}

// known returns copies of what the parts read so far found out, in the
// order of the parts, with the indexes of those parts.
func (f *File) known() (*session, []int, []*unit.Tally) {
	f.mu.Lock()
	defer f.mu.Unlock()
	indexes := make([]int, 0, len(f.tallies))
	for index := range f.tallies {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	out := make([]*unit.Tally, len(indexes))
	for i, index := range indexes {
		tally := *f.tallies[index]
		out[i] = &tally
	}
	return f.sess, indexes, out
}

// embedded extracts the files embedded in the parts whose tallies are
// given, through Options.Child, each file once. An embedded file that
// could not be read is added to the damaged parts of the tally of the
// part that embeds it.
func (f *File) embedded(ctx context.Context, sess *session, tallies []*unit.Tally) []*gofastcore.Document {
	var out []*gofastcore.Document
	seen := map[string]bool{}
	limited := false
	for _, t := range tallies {
		if t.Children != nil {
			out = append(out, t.Children...) // the reader extracted them itself
			continue
		}
		limited = limited || t.Limited
		// With a limit met, or no text left to take, the embedded files
		// are listed and not read.
		children, bad, err := media.Embedded(ctx, sess.pkg, &f.opts, t.Rels, seen, !limited && !gofastcore.TextBudgetFrom(ctx).Spent())
		out = append(out, children...)
		if len(bad) > 0 {
			t.Damaged = append(t.Damaged[:len(t.Damaged):len(t.Damaged)], bad...)
		}
		limited = limited || err != nil
	}
	return out
}

// Document reads the parts Options.Parts selects and returns the
// document, the same one Extract returns.
func (f *File) Document(ctx context.Context) (*gofastcore.Document, error) {
	ctx, done := f.tree(ctx)
	defer done()
	selected, asked, err := f.selection()
	if err != nil {
		return nil, err
	}
	doc := gofastcore.New(f.source.Format)
	doc.Source, doc.Metadata, doc.HiddenChecked = f.source, f.meta, f.checked
	doc.Warnings = append(doc.Warnings, f.opened...)
	if asked > len(selected) {
		doc.Warnings = append(doc.Warnings, f.capped(asked, len(selected)))
	}
	pictures := once{}
	var tallies []*unit.Tally
	sess, err := f.pass(ctx, selected, 0, func(index int, res *unit.Result) bool {
		pictures.keep(&res.Part)
		doc.Parts = append(doc.Parts, res.Part)
		tally, kept := res.Tally, res.Tally
		tallies = append(tallies, &tally)
		f.record(index, &kept)
		return true
	})
	if err != nil {
		return nil, err
	}
	doc.Children = f.embedded(ctx, sess, tallies)
	doc.Warnings = append(doc.Warnings, sess.src.Summary(tallies)...)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return doc, nil
}

// capped is the warning that fewer parts are given than were asked for.
func (f *File) capped(asked, given int) gofastcore.Warning {
	return gofastcore.Warning{Code: gofastcore.WarnLimitExceeded, Severity: gofastcore.SeverityError,
		Message: fmt.Sprintf("%d %s were asked for; the first %d are given", asked, f.noun, given)}
}

// Parts returns an iterator over the parts Options.Parts selects, in
// order. Each part is read when the loop reaches it, a few ahead with
// more than one worker, and is the caller's to keep or let go. The bytes
// of a picture that several parts show (Options.Images) come with the
// first of them.
//
// The iterator yields an error, with an empty part, and stops: when no
// part is selected (gofastcore.ErrNoPartsSelected), when ctx is
// cancelled, or when the file can no longer be read. A part that is
// damaged or cut short by a limit is not an error: it is yielded with a
// warning that says so.
//
// What the parts say about the document as a whole is in Warnings and
// Children once they have been read.
func (f *File) Parts(ctx context.Context) iter.Seq2[gofastcore.Part, error] {
	return func(yield func(gofastcore.Part, error) bool) {
		ctx, done := f.tree(ctx)
		defer done()
		selected, _, err := f.selection()
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			yield(gofastcore.Part{}, err)
			return
		}
		workers := f.opts.Workers
		if workers <= 0 {
			workers = runtime.GOMAXPROCS(0)
		}
		pictures := once{}
		stopped := false
		_, err = f.pass(ctx, selected, 4*workers, func(index int, res *unit.Result) bool {
			pictures.keep(&res.Part)
			tally := res.Tally
			f.record(index, &tally)
			stopped = !yield(res.Part, nil)
			return !stopped
		})
		if err != nil && !stopped {
			yield(gofastcore.Part{}, err)
		}
	}
}

// Part reads the part with the given number, from 1, whether or not
// Options.Parts selects it.
func (f *File) Part(ctx context.Context, number int) (gofastcore.Part, error) {
	ctx, done := f.tree(ctx)
	defer done()
	if number < 1 || number > f.parts {
		return gofastcore.Part{}, fmt.Errorf("gofastooxml: part %d: the %s has %d %s, numbered from 1: %w", number, f.kind(), f.parts, f.noun, gofastcore.ErrNoPartsSelected)
	}
	if err := ctx.Err(); err != nil {
		return gofastcore.Part{}, err
	}
	var part gofastcore.Part
	_, err := f.pass(ctx, []int{number - 1}, 1, func(index int, res *unit.Result) bool {
		part = res.Part
		tally := res.Tally
		f.record(index, &tally)
		return true
	})
	if err != nil {
		return gofastcore.Part{}, err
	}
	return part, nil
}

// Rows returns an iterator over the cells of a sheet of a workbook, a
// band of rows at a time. number is the sheet's part number, from 1, and
// band the number of rows that have cells to a band. Each band is
// yielded as a part with the sheet's kind, number and name, holding the
// tables its cells make, with hidden text flagged as in the sheet read
// whole.
//
// It is for sheets too long to hold: what is held at a time is one band,
// whatever the length of the sheet, and the limits on cells and text do
// not apply. In exchange the sheet's part is read twice; a table that
// runs past the end of a band is cut there, and goes on in the next band
// as a table marked Continued; the tables a sheet declares
// are not looked up, so every table is origin inferred; and comments,
// shapes and pictures are not read (Part reads them). A sheet with no
// cells yields one part with no blocks.
//
// Bands follow the order of the rows in the file, which in a sound
// workbook is the order of the sheet. Rows a file gives out of order are
// put in order within a band and not across bands, and a cell a damaged
// file gives twice, which the sheet read whole has once, is in each band
// it falls in.
//
// The iterator yields an error and stops as Parts does; for a file that
// is not a workbook the error wraps gofastcore.ErrUnsupported.
func (f *File) Rows(ctx context.Context, number, band int) iter.Seq2[gofastcore.Part, error] {
	return func(yield func(gofastcore.Part, error) bool) {
		sess, err := f.acquire()
		var book *sml.Book
		switch {
		case err != nil:
		case f.source.Format != "xlsx":
			err = fmt.Errorf("gofastooxml: a %s has no rows: %w", f.kind(), gofastcore.ErrUnsupported)
		case number < 1 || number > len(f.sheets):
			err = fmt.Errorf("gofastooxml: sheet %d: the workbook has %d sheets, numbered from 1: %w", number, len(f.sheets), gofastcore.ErrNoPartsSelected)
		default:
			book = sess.src.(*sml.Book)
			err = ctx.Err()
		}
		if err != nil {
			yield(gofastcore.Part{}, err)
			return
		}
		book.Begin()
		stopped := false
		tally := book.Rows(ctx, number-1, band, func(part gofastcore.Part) bool {
			if ctx.Err() != nil {
				return false
			}
			stopped = !yield(part, nil)
			return !stopped
		})
		if tally.Limited || book.Limited() {
			f.spoil(sess)
		}
		if err := ctx.Err(); err != nil && !stopped {
			yield(gofastcore.Part{}, err)
		}
	}
}

// Warnings returns the warnings about the document as a whole, as in
// gofastcore.Document.Warnings: those known when the file was opened,
// and those that the parts read so far give rise to (content of a kind
// that is not read, damaged supporting parts, references to things
// outside the file). It is complete once every part has been read.
func (f *File) Warnings() []gofastcore.Warning {
	out := append([]gofastcore.Warning(nil), f.opened...)
	if selected, asked, err := f.selection(); err == nil && asked > len(selected) {
		out = append(out, f.capped(asked, len(selected)))
	}
	sess, _, tallies := f.known()
	return append(out, sess.src.Summary(tallies)...)
}

// Children extracts the files embedded in the parts read so far (a
// workbook in a slide, a document in a document) through Options.Child,
// and returns them as in gofastcore.Document.Children. With no
// Options.Child each is listed, not extracted.
func (f *File) Children(ctx context.Context) ([]*gofastcore.Document, error) {
	ctx, done := f.tree(ctx)
	defer done()
	sess, indexes, tallies := f.known()
	sess.src.Begin()
	out := f.embedded(ctx, sess, tallies)
	// An embedded file that could not be read is a damaged part of the
	// document, which Warnings reports.
	f.mu.Lock()
	for i, index := range indexes {
		if t := f.tallies[index]; t != nil && len(tallies[i].Damaged) > len(t.Damaged) {
			t.Damaged = tallies[i].Damaged
		}
	}
	f.mu.Unlock()
	return out, ctx.Err()
}
