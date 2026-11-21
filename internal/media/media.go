// Package media reads what a document carries besides its text: pictures
// and embedded files. Word, Excel and PowerPoint packages hold both the
// same way.
package media

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/gif" // sizes of pictures are read from their headers
	_ "image/jpeg"
	_ "image/png"
	"io"
	"path"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
)

// Image describes the picture in the named part: its name in the archive,
// its type, its size in pixels where the format is one the standard
// library can read a header of, and with ImagesData its bytes as stored.
// The error is opc.ErrBudget if the decompression budget ran out, and nil
// otherwise: a picture that cannot be read is still named.
func Image(pkg *opc.Package, name string, mode gofastcore.ImageMode) (*gofastcore.Image, error) {
	img := &gofastcore.Image{ID: clean.Label(pkg.Written(name)), MIME: clean.Label(pkg.ContentType(name))}
	if !pkg.Has(name) {
		return img, nil
	}
	rc, err := pkg.Open(name)
	if err != nil {
		return img, nil
	}
	defer rc.Close()
	if mode == gofastcore.ImagesData {
		data, err := io.ReadAll(rc)
		if err != nil {
			return img, budget(err)
		}
		img.Data = data
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
			img.Width, img.Height = cfg.Width, cfg.Height
		}
		return img, nil
	}
	// Only the header is needed for the size.
	cfg, _, err := image.DecodeConfig(io.LimitReader(rc, 1<<20))
	if err == nil {
		img.Width, img.Height = cfg.Width, cfg.Height
	}
	return img, budget(err)
}

func budget(err error) error {
	if errors.Is(err, opc.ErrBudget) {
		return opc.ErrBudget
	}
	return nil
}

// listed is an embedded file that is named and not read, because a limit
// was reached before it. The child extractor is not called for it: there
// is nothing to give it.
func listed(name, mime string, size int64) *gofastcore.Document {
	doc := gofastcore.New("")
	doc.Source.Name, doc.Source.MIME, doc.Source.Size = name, mime, size
	doc.Warn(gofastcore.SeverityWarning, gofastcore.WarnChildNotExtracted, "a limit was reached before this file could be read")
	return doc
}

// Embedded hands the files a part embeds (another document, an OLE
// object) to the caller's child extractor and returns the results. seen
// records the parts already handed over, so that a file embedded from two
// places is given once. With no child extractor, or with read false, each
// is listed without being read. The error is opc.ErrBudget if the budget
// ran out while reading one.
func Embedded(ctx context.Context, pkg *opc.Package, opts *gofastcore.Options, rels []opc.Rel, seen map[string]bool, read bool) ([]*gofastcore.Document, []string, error) {
	var out []*gofastcore.Document
	var damaged []string
	var failure error
	for _, rel := range rels {
		if rel.External || !ns.IsRel(rel.Type, ns.RelOLEObject, ns.RelPackage, ns.RelAltChunk) {
			continue
		}
		if seen[rel.Target] || !pkg.Has(rel.Target) {
			continue
		}
		seen[rel.Target] = true
		name, mime, size := clean.Label(path.Base(pkg.Written(rel.Target))), clean.Label(pkg.ContentType(rel.Target)), pkg.Size(rel.Target)
		if opts.Child == nil {
			// Listed, not read: there is nobody to read it.
			out = append(out, opts.ExtractChild(ctx, name, mime, nil, size))
			continue
		}
		if !read || failure != nil {
			out = append(out, listed(name, mime, size))
			continue
		}
		rc, err := pkg.Open(rel.Target)
		if err != nil {
			damaged = append(damaged, rel.Target)
			continue
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			if errors.Is(err, opc.ErrBudget) {
				failure = opc.ErrBudget
				out = append(out, listed(name, mime, size))
				continue
			}
			damaged = append(damaged, rel.Target)
		}
		// The limit on decompressed bytes is the document's and its
		// embedded files' together. The package has counted what the
		// document read so far; the budget the child draws on is brought
		// to that, and what the child used is then taken from what the
		// package may still read.
		decoded := gofastcore.DecodeBudgetFrom(ctx)
		decoded.Settle(pkg.Budget())
		out = append(out, opts.ExtractChild(ctx, name, mime, bytes.NewReader(data), int64(len(data))))
		if left := decoded.Left(); left < pkg.Budget() {
			pkg.Restore(max(left, 0))
		}
	}
	return out, damaged, failure
}
