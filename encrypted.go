package gofastooxml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync/atomic"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/cfb"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/crypt"
)

var compoundSignature = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// spinAllowance is how many times MaxSpinCount the password checks of one
// document may cost in all, counting the files embedded in it. One file
// is bounded by MaxSpinCount; a document can embed thousands of encrypted
// files, and each one that does not open costs two checks.
const spinAllowance = 10

// spinKey is the context key under which a document hands the rounds of
// hashing it has left to the extraction of the files embedded in it.
type spinKey struct{}

// allowance returns the rounds of hashing the document may still spend
// on password checks: what the document this file is embedded in has
// left, if the context carries that, or a new allowance. It is nil when
// there is no limit.
func (f *File) allowance(ctx context.Context) *atomic.Int64 {
	if left, ok := ctx.Value(spinKey{}).(*atomic.Int64); ok {
		return left
	}
	limit := gofastcore.ResolveLimit(int64(f.e.MaxSpinCount), DefaultMaxSpinCount)
	if limit > math.MaxInt64/spinAllowance {
		return nil
	}
	left := new(atomic.Int64)
	left.Store(limit * spinAllowance)
	return left
}

// tree returns the context for one reading of this file's parts and of
// the files embedded in it: ctx, carrying what the document and the
// files embedded in it share, which is the text they may hold
// (gofastcore.Limits.MaxTextBytes), the bytes they may decompress
// (MaxDecodedBytes) and what is left for their password checks. A file that is itself embedded draws on what the document it
// is in has left; otherwise each reading counts afresh, as it does every
// limit. done is to be called when the reading is over: it hands what
// the file used back to the document it is embedded in.
func (f *File) tree(ctx context.Context) (_ context.Context, done func()) {
	ctx, room := gofastcore.WithTextBudget(ctx, f.opts.Limits)
	ctx, decoded := gofastcore.WithDecodeBudget(ctx, f.opts.Limits, DefaultMaxDecodedBytes)
	if _, ok := ctx.Value(spinKey{}).(*atomic.Int64); !ok {
		if left := f.allowance(ctx); left != nil {
			ctx = context.WithValue(ctx, spinKey{}, left)
		}
	}
	return ctx, func() {
		// What the package decompressed is counted by the package as it
		// reads. It is settled with the document's budget here, and
		// before each embedded file is read (media.Embedded), so that the
		// document this file is embedded in is told what it cost.
		f.mu.Lock()
		sess := f.sess
		f.mu.Unlock()
		if sess != nil {
			decoded.Settle(sess.pkg.Budget())
		}
		decoded.Close()
		room.Close()
	}
}

// unlock looks at whether the file is a compound file and, if it is a
// password-protected package, decrypts it: f.body becomes the package.
// For a file that is not a compound file it does nothing.
//
// A file whose key is encrypted for certificates is tried first with
// Options.Keys, which costs no rounds of hashing. The passwords tried
// then are Options.Password, if there is one, and the password Excel
// encrypts with when a workbook is protected and none is set, which Excel
// itself tries without asking. Each key and password is verified against
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
	// Rights management ([MS-OFFCRYPTO] 2.2.11, 2.6): the content is
	// encrypted with a key held in a licence that a rights-management
	// server issues to the people the document names.
	rights := cf.Has("\x06DataSpaces", "DataSpaceInfo", "\x09DRMEncryptedDataSpace") ||
		cf.Has("\x06DataSpaces", "TransformInfo", "\x09DRMEncryptedTransform") || cf.Has("\x09DRMContent")
	switch {
	case rights:
		return fmt.Errorf("gofastooxml: %s: %w", rightsManaged, gofastcore.ErrUnsupported)
	case cf.Has("EncryptionInfo") && cf.Has("EncryptedPackage"):
	case cf.Has("\x06DataSpaces") || cf.Has("EncryptedPackage"):
		return fmt.Errorf("gofastooxml: encrypted Office document that is not protected with a password, and whose encryption is not stated: %w", gofastcore.ErrUnsupported)
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

	var key []byte
	with := ""
	if info.Certificates() > 0 && len(f.opts.Keys) > 0 {
		k, subject, err := info.CertificateKey(f.opts.Keys)
		switch {
		case err == nil:
			key, with = k, "the private key given for its certificate"
			if subject != "" {
				with += " " + clean.Label(subject)
			}
		case !errors.Is(err, crypt.ErrNoKey):
			return fail(err)
		}
	}
	passwords := []string{crypt.DefaultPassword}
	if f.opts.Password != "" && f.opts.Password != crypt.DefaultPassword {
		passwords = []string{f.opts.Password, crypt.DefaultPassword}
	}
	if key != nil || !info.Password() {
		passwords = nil
	}
	maxSpin := int(gofastcore.ResolveLimit(int64(f.e.MaxSpinCount), DefaultMaxSpinCount))
	left := f.allowance(ctx)
	for _, password := range passwords {
		if left != nil && int64(info.SpinCount()) <= int64(maxSpin) && left.Add(-int64(info.SpinCount())) < 0 {
			return fmt.Errorf("gofastooxml: encrypted Office document (%s): the password checks of the document and the files embedded in it have used the rounds of hashing allowed, %d times MaxSpinCount: %w",
				info, spinAllowance, gofastcore.ErrLimitExceeded)
		}
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
	if key == nil {
		return refused(info, f.opts.Password != "", len(f.opts.Keys))
	}
	pkg, err := info.Open(encrypted, encrypted.Size(), key)
	if err != nil {
		return fail(err)
	}
	f.body, f.bodySize = pkg, pkg.Size()
	f.opened = append(f.opened, gofastcore.Warning{Code: WarnDecrypted, Severity: gofastcore.SeverityInfo,
		Message: fmt.Sprintf("encrypted (%s); decrypted with %s", describe(info), with)})
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

// describe says how a file is encrypted, and for whom its key is
// encrypted when that is more than a password: "agile, AES-256, SHA512;
// the key is encrypted for a password and for 2 certificates: CN=A,
// CN=B".
func describe(info *crypt.Info) string {
	n := info.Certificates()
	if n == 0 {
		return info.String()
	}
	var b strings.Builder
	b.WriteString(info.String())
	b.WriteString("; the key is encrypted for ")
	if info.Password() {
		b.WriteString("a password and for ")
	}
	if n == 1 {
		b.WriteString("1 certificate")
	} else {
		fmt.Fprintf(&b, "%d certificates", n)
	}
	var names []string
	for _, s := range info.Subjects() {
		if s != "" && len(names) < maxSubjects {
			names = append(names, clean.Label(s))
		}
	}
	if len(names) > 0 {
		b.WriteString(": ")
		b.WriteString(strings.Join(names, "; "))
		if n > len(names) {
			b.WriteString("; ...")
		}
	}
	return b.String()
}

// maxSubjects is how many certificates a message names.
const maxSubjects = 3

// refused is the error for an encrypted file that nothing given opens. It
// wraps each of ErrPasswordRequired or ErrWrongPassword, and
// ErrKeyRequired, that the file's encryption makes apply.
func refused(info *crypt.Info, password bool, keys int) error {
	var why []string
	var errs []any
	if info.Password() {
		if password {
			why, errs = append(why, "the password given does not open it"), append(errs, gofastcore.ErrWrongPassword)
		} else {
			why, errs = append(why, "no password given"), append(errs, gofastcore.ErrPasswordRequired)
		}
	}
	if info.Certificates() > 0 {
		switch keys {
		case 0:
			why = append(why, "no private key given")
		case 1:
			why = append(why, "the private key given is for none of its certificates")
		default:
			why = append(why, fmt.Sprintf("none of the %d private keys given is for one of its certificates", keys))
		}
		errs = append(errs, gofastcore.ErrKeyRequired)
	}
	verbs := strings.TrimSuffix(strings.Repeat("%w; ", len(errs)), "; ")
	return fmt.Errorf("gofastooxml: encrypted Office document (%s): %s: "+verbs, append([]any{describe(info), strings.Join(why, "; ")}, errs...)...)
}

// rightsManaged says what a rights-managed file is, as the error about it.
const rightsManaged = "encrypted Office document protected by rights management (Information Rights Management): " +
	"its key is in a licence that a rights-management server issues to the people the document names, " +
	"and this library contacts no server"
