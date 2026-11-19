package gofastooxml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/cfb"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/crypt"
)

var compoundSignature = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// unlock looks at whether the file is a compound file and, if it is a
// password-protected package, decrypts it: f.body becomes the package.
// For a file that is not a compound file it does nothing.
//
// The passwords tried are Options.Password, if there is one, and the
// password Excel encrypts with when a workbook is protected and none is
// set, which Excel itself tries without asking. Each is verified against
// the file before anything is decrypted with it.
func (f *File) unlock(ctx context.Context) error {
	var head [8]byte
	if n, _ := f.r.ReadAt(head[:], 0); n < len(head) || !bytes.Equal(head[:], compoundSignature) {
		return nil
	}
	const legacy = "compound file: a legacy binary Office format (.doc, .xls, .ppt), which this library does not read"
	cf, err := cfb.Open(f.r, f.size)
	if err != nil {
		// Too damaged to say what it holds.
		return fmt.Errorf("gofastooxml: a compound file, the container of legacy binary and of encrypted Office files, that cannot be read: %s: %w", clean.Label(err.Error()), gofastcore.ErrMalformed)
	}
	switch {
	case cf.Has("EncryptionInfo") && cf.Has("EncryptedPackage"):
	case cf.Has("\x06DataSpaces") || cf.Has("EncryptedPackage") || cf.Has("\x09DRMContent"):
		return fmt.Errorf("gofastooxml: encrypted Office document that is not protected with a password (rights management): %w", gofastcore.ErrUnsupported)
	default:
		return fmt.Errorf("gofastooxml: %s: %w", legacy, gofastcore.ErrUnsupported)
	}
	fail := func(err error) error {
		shared := gofastcore.ErrMalformed
		switch {
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return err
		case errors.Is(err, crypt.ErrUnsupported):
			shared = gofastcore.ErrUnsupported
		case errors.Is(err, crypt.ErrSpinCount):
			shared = gofastcore.ErrLimitExceeded
		}
		return fmt.Errorf("gofastooxml: encrypted Office document: %s: %w", clean.Label(err.Error()), shared)
	}
	stream, err := cf.Open("EncryptionInfo")
	if err != nil {
		return fail(err)
	}
	info, err := crypt.Parse(stream, stream.Size())
	if err != nil {
		return fail(err)
	}
	encrypted, err := cf.Open("EncryptedPackage")
	if err != nil {
		return fail(err)
	}

	passwords := []string{crypt.DefaultPassword}
	if f.opts.Password != "" && f.opts.Password != crypt.DefaultPassword {
		passwords = []string{f.opts.Password, crypt.DefaultPassword}
	}
	maxSpin := int(gofastcore.ResolveLimit(int64(f.e.MaxSpinCount), DefaultMaxSpinCount))
	var key []byte
	with := ""
	for _, password := range passwords {
		key, err = info.Key(ctx, password, maxSpin)
		if err == nil {
			with = "the password given"
			if password == crypt.DefaultPassword {
				with = "the password Excel uses when none is set"
			}
			break
		}
		if !errors.Is(err, crypt.ErrPassword) {
			return fail(err)
		}
	}
	switch {
	case key != nil:
	case f.opts.Password == "":
		return fmt.Errorf("gofastooxml: encrypted Office document (%s): %w", info, gofastcore.ErrPasswordRequired)
	default:
		return fmt.Errorf("gofastooxml: encrypted Office document (%s): %w", info, gofastcore.ErrWrongPassword)
	}
	pkg, err := info.Open(encrypted, encrypted.Size(), key)
	if err != nil {
		return fail(err)
	}
	f.body, f.bodySize = pkg, pkg.Size()
	f.opened = append(f.opened, gofastcore.Warning{Code: WarnDecrypted, Severity: gofastcore.SeverityInfo,
		Message: fmt.Sprintf("encrypted (%s); decrypted with %s", info, with)})
	intact, checked, err := info.Intact(ctx, encrypted, encrypted.Size(), key)
	if err != nil {
		return fail(err)
	}
	if checked && !intact {
		f.opened = append(f.opened, gofastcore.Warning{Code: WarnIntegrityMismatch, Severity: gofastcore.SeverityWarning,
			Message: "the encrypted file is not what its integrity value was made of: it was changed after it was encrypted, or damaged"})
	}
	return nil
}

var _ io.ReaderAt = (*crypt.Package)(nil)
