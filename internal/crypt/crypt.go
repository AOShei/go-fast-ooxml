// Package crypt opens password-protected Office Open XML files. Such a
// file is a compound file with two streams: EncryptionInfo, which says
// how the package was encrypted, and EncryptedPackage, which is the
// package, a zip archive, encrypted.
//
// Office has two ways of encrypting a package with a password, and this
// package reads both: the one Office 2007 introduced ("standard": AES in
// ECB mode, SHA-1) and the one Office 2010 introduced and all later
// versions and LibreOffice write ("agile": AES in CBC mode, a hash
// algorithm the file names, the parameters as XML).
//
// The agile scheme can also encrypt the package's key for certificates,
// as well as for the password: whoever holds the private key of one of
// them opens the file without the password. This package reads that too,
// with RSA keys.
//
// The password, or the private key, is verified before anything is
// decrypted: the file stores a value that only the right one decrypts to
// match. The package is decrypted as it is read, a piece at a time, so a
// large file is never held in memory whole.
package crypt

import (
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"strconv"
	"sync"
	"unicode/utf16"

	"github.com/AOShei/go-fast-core/xmlscan"
)

// Errors, each wrapped with what was wrong.
var (
	// ErrPassword: the password does not open the file.
	ErrPassword = errors.New("crypt: the password does not open the file")
	// ErrNoKey: none of the private keys is for a certificate the file
	// is encrypted for.
	ErrNoKey = errors.New("crypt: no private key given opens the file")
	// ErrUnsupported: the file is encrypted in a way this package does
	// not read: a cipher other than AES, an encryption provider outside
	// the file.
	ErrUnsupported = errors.New("crypt: unsupported encryption")
	// ErrMalformed: the encryption information contradicts itself.
	ErrMalformed = errors.New("crypt: malformed encryption information")
	// ErrSpinCount: the file asks for more rounds of hashing than the
	// caller allows.
	ErrSpinCount = errors.New("crypt: spin count over the limit")
)

// DefaultPassword is the password Excel encrypts a workbook with when
// the workbook is protected but no password to open it is set. Excel
// tries it without asking, and so do other programs.
const DefaultPassword = "VelvetSweatshop"

// params is how one thing is encrypted in the agile scheme.
type params struct {
	blockSize, keyBytes, hashSize int
	newHash                       func() hash.Hash
	hashName                      string
	salt                          []byte
}

// Info is the content of an EncryptionInfo stream.
type Info struct {
	agile bool

	// Standard: AES in ECB mode with a key derived by SHA-1.
	keyBytes              int
	salt                  []byte
	verifier, verifierSum []byte // both encrypted

	// Agile: how the package is encrypted, how the key that does it is
	// encrypted with the password, and the values to check both by.
	data, key              params
	spin                   int
	verifierIn, verifierIs []byte // encrypted: a random value, and its hash
	keyValue               []byte // encrypted: the key of the package
	hmacKey, hmacValue     []byte // encrypted: the key and the value of the integrity check
	password               bool   // the key is encrypted for a password

	// Agile: the certificates the key of the package is encrypted for.
	recipients []recipient
}

// recipient is a certificate the key of the package is encrypted for
// ([MS-OFFCRYPTO] 2.3.4.10, CertificateKeyEncryptor).
type recipient struct {
	cert     []byte // the certificate, DER
	keyValue []byte // the key of the package, encrypted with the certificate's public key
	verifier []byte // HMAC of cert, keyed with the key of the package
}

// maxInfo bounds the size of an EncryptionInfo stream. Office writes a
// few hundred bytes to a few kilobytes.
const maxInfo = 1 << 20

// Parse reads an EncryptionInfo stream.
func Parse(r io.ReaderAt, size int64) (*Info, error) {
	if size < 8 || size > maxInfo {
		return nil, fmt.Errorf("%w: %d bytes", ErrMalformed, size)
	}
	raw := make([]byte, size)
	if _, err := r.ReadAt(raw, 0); err != nil && err != io.EOF {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	le := binary.LittleEndian
	major, minor := le.Uint16(raw), le.Uint16(raw[2:])
	switch {
	case major == 4 && minor == 4:
		return parseAgile(raw[8:])
	case (major == 2 || major == 3 || major == 4) && minor == 2:
		return parseStandard(raw)
	case (major == 3 || major == 4) && minor == 3:
		return nil, fmt.Errorf("%w: extensible encryption, done by a provider outside the file", ErrUnsupported)
	}
	return nil, fmt.Errorf("%w: version %d.%d", ErrUnsupported, major, minor)
}

// Flags and algorithm numbers of the standard scheme.
const (
	flagCryptoAPI = 0x04
	flagExternal  = 0x10
	flagAES       = 0x20
	algAES128     = 0x660E
	algAES192     = 0x660F
	algAES256     = 0x6610
	algSHA1       = 0x8004
)

func parseStandard(raw []byte) (*Info, error) {
	le := binary.LittleEndian
	if len(raw) < 12 {
		return nil, fmt.Errorf("%w: no header", ErrMalformed)
	}
	flags, headerSize := le.Uint32(raw[4:]), int64(le.Uint32(raw[8:]))
	if headerSize < 32 || 12+headerSize > int64(len(raw)) {
		return nil, fmt.Errorf("%w: a header of %d bytes", ErrMalformed, headerSize)
	}
	header, rest := raw[12:12+headerSize], raw[12+headerSize:]
	switch {
	case flags&flagExternal != 0:
		return nil, fmt.Errorf("%w: encryption by a provider outside the file", ErrUnsupported)
	case flags&flagAES == 0 || flags&flagCryptoAPI == 0:
		return nil, fmt.Errorf("%w: a cipher other than AES (flags %#x)", ErrUnsupported, flags)
	}
	alg, algHash, keyBits := le.Uint32(header[8:]), le.Uint32(header[12:]), le.Uint32(header[16:])
	in := &Info{}
	switch alg {
	case algAES128, 0:
		in.keyBytes = 16
	case algAES192:
		in.keyBytes = 24
	case algAES256:
		in.keyBytes = 32
	default:
		return nil, fmt.Errorf("%w: cipher %#x", ErrUnsupported, alg)
	}
	if keyBits != 0 && int(keyBits) != in.keyBytes*8 {
		return nil, fmt.Errorf("%w: a key of %d bits for cipher %#x", ErrMalformed, keyBits, alg)
	}
	if algHash != algSHA1 && algHash != 0 {
		return nil, fmt.Errorf("%w: hash %#x", ErrUnsupported, algHash)
	}
	// The verifier: a salt, a random value encrypted, and its hash
	// encrypted.
	if len(rest) < 4+16+16+4+32 || le.Uint32(rest) != 16 {
		return nil, fmt.Errorf("%w: the password verifier", ErrMalformed)
	}
	in.salt, in.verifier = rest[4:20], rest[20:36]
	if size := le.Uint32(rest[36:]); size != sha1.Size {
		return nil, fmt.Errorf("%w: a verifier hash of %d bytes", ErrMalformed, size)
	}
	in.verifierSum = rest[40:72]
	return in, nil
}

var scanOptions = &xmlscan.Options{}

func parseAgile(xml []byte) (*Info, error) {
	in := &Info{agile: true}
	s := xmlscan.NewBytes(xml, scanOptions)
	var sawData, sawKey bool
	var bad error
	// The attributes are the same for the package and for its key.
	read := func(p *params) {
		num := func(name string) int {
			v, _ := s.AttrAny(name)
			n, err := strconv.Atoi(string(v))
			if err != nil || n < 0 || n > 1<<16 {
				bad = fmt.Errorf("%w: %s=%q", ErrMalformed, name, v)
			}
			return n
		}
		saltSize, keyBits := num("saltSize"), num("keyBits")
		p.blockSize, p.hashSize = num("blockSize"), num("hashSize")
		if v, _ := s.AttrAny("cipherAlgorithm"); string(v) != "AES" {
			bad = fmt.Errorf("%w: cipher %q", ErrUnsupported, v)
		}
		if v, _ := s.AttrAny("cipherChaining"); string(v) != "ChainingModeCBC" {
			bad = fmt.Errorf("%w: chaining mode %q", ErrUnsupported, v)
		}
		v, _ := s.AttrAny("hashAlgorithm")
		p.hashName = string(v)
		switch p.hashName {
		case "SHA1", "SHA-1":
			p.newHash = sha1.New
		case "SHA256", "SHA-256":
			p.newHash = sha256.New
		case "SHA384", "SHA-384":
			p.newHash = sha512.New384
		case "SHA512", "SHA-512":
			p.newHash = sha512.New
		case "MD5":
			p.newHash = md5.New
		default:
			bad = fmt.Errorf("%w: hash %q", ErrUnsupported, v)
			return
		}
		p.salt = attr64(s, "saltValue", &bad)
		p.keyBytes = keyBits / 8
		switch {
		case bad != nil:
		case keyBits != 128 && keyBits != 192 && keyBits != 256:
			bad = fmt.Errorf("%w: a key of %d bits", ErrMalformed, keyBits)
		case p.blockSize != aes.BlockSize:
			bad = fmt.Errorf("%w: blocks of %d bytes", ErrMalformed, p.blockSize)
		case p.hashSize != p.newHash().Size():
			bad = fmt.Errorf("%w: %s with a hash of %d bytes", ErrMalformed, p.hashName, p.hashSize)
		case len(p.salt) != saltSize || saltSize == 0:
			bad = fmt.Errorf("%w: a salt of %d bytes where %d are stated", ErrMalformed, len(p.salt), saltSize)
		}
	}
	for s.Next() != xmlscan.EOF && bad == nil {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch string(s.Local()) {
		case "keyData":
			if !sawData {
				read(&in.data)
				sawData = true
			}
		case "dataIntegrity":
			in.hmacKey, in.hmacValue = attr64(s, "encryptedHmacKey", &bad), attr64(s, "encryptedHmacValue", &bad)
		case "encryptedKey":
			// The key encrypted with the password, or for a certificate,
			// which has other attributes.
			_, password := s.AttrAny("spinCount")
			if _, cert := s.AttrAny("X509Certificate"); cert && !password {
				// One that cannot be read is passed over, as one whose
				// verifier does not match must be.
				var skip error
				r := recipient{cert: attr64n(s, "X509Certificate", &skip, maxCert), keyValue: attr64(s, "encryptedKeyValue", &skip), verifier: attr64(s, "certVerifier", &skip)}
				if skip == nil && len(r.cert) > 0 && len(r.keyValue) > 0 && len(r.verifier) > 0 {
					in.recipients = append(in.recipients, r)
				}
				continue
			}
			if !password || sawKey {
				continue
			}
			read(&in.key)
			sawKey = true
			v, _ := s.AttrAny("spinCount")
			n, err := strconv.Atoi(string(v))
			if err != nil || n < 0 || n > 1<<30 {
				bad = fmt.Errorf("%w: spinCount=%q", ErrMalformed, v)
			}
			in.spin = n
			in.verifierIn = attr64(s, "encryptedVerifierHashInput", &bad)
			in.verifierIs = attr64(s, "encryptedVerifierHashValue", &bad)
			in.keyValue = attr64(s, "encryptedKeyValue", &bad)
		}
	}
	switch {
	case bad != nil:
		return nil, bad
	case s.Err() != nil:
		return nil, fmt.Errorf("%w: %v", ErrMalformed, s.Err())
	case !sawData:
		return nil, fmt.Errorf("%w: no keyData", ErrMalformed)
	case !sawKey && len(in.recipients) == 0:
		return nil, fmt.Errorf("%w: the key is encrypted neither for a password nor for a certificate", ErrUnsupported)
	}
	if len(in.hmacKey)%aes.BlockSize != 0 || len(in.hmacValue)%aes.BlockSize != 0 || len(in.hmacKey) > 0 && (len(in.hmacKey) < in.data.hashSize || len(in.hmacValue) < in.data.hashSize) {
		in.hmacKey, in.hmacValue = nil, nil // not usable; the check is not made
	}
	if !sawKey {
		// The specification has every file carry a key encrypted for a
		// password; this one has only certificates.
		return in, nil
	}
	in.password = true
	for name, v := range map[string][]byte{"encryptedVerifierHashInput": in.verifierIn, "encryptedVerifierHashValue": in.verifierIs, "encryptedKeyValue": in.keyValue} {
		if len(v) == 0 || len(v)%aes.BlockSize != 0 {
			return nil, fmt.Errorf("%w: %s of %d bytes", ErrMalformed, name, len(v))
		}
	}
	if len(in.verifierIs) < in.key.hashSize || len(in.keyValue) < in.data.keyBytes || len(in.verifierIn) < len(in.key.salt) {
		return nil, fmt.Errorf("%w: the encrypted values are shorter than what they hold", ErrMalformed)
	}
	return in, nil
}

// attr64 reads an attribute that is base64.
func attr64(s *xmlscan.Scanner, name string, bad *error) []byte {
	return attr64n(s, name, bad, 1<<12)
}

// maxCert bounds the size of a certificate in the encryption information.
// One is a kilobyte or two; a long chain of extensions makes it more.
const maxCert = 1 << 16

// attr64n reads an attribute that is base64, of at most limit bytes.
func attr64n(s *xmlscan.Scanner, name string, bad *error, limit int) []byte {
	v, ok := s.AttrAny(name)
	if !ok {
		return nil
	}
	if base64.StdEncoding.DecodedLen(len(v)) > limit+3 {
		*bad = fmt.Errorf("%w: %s is not base64 of a sensible length", ErrMalformed, name)
		return nil
	}
	out, err := base64.StdEncoding.DecodeString(string(v))
	if err != nil || len(out) > limit {
		*bad = fmt.Errorf("%w: %s is not base64 of a sensible length", ErrMalformed, name)
		return nil
	}
	return out
}

// Agile reports whether the file uses the scheme of Office 2010 and
// later.
func (in *Info) Agile() bool { return in.agile }

// SpinCount returns the number of rounds of hashing between a password
// and its key: what a guess at the password costs.
func (in *Info) SpinCount() int {
	if !in.agile {
		return 50000
	}
	return in.spin
}

// Password reports whether a password opens the file: always, but for an
// agile file that has its key encrypted only for certificates.
func (in *Info) Password() bool { return !in.agile || in.password }

// Certificates returns the number of certificates the key of the package
// is encrypted for.
func (in *Info) Certificates() int { return len(in.recipients) }

// Subjects returns the subjects of the certificates the key of the
// package is encrypted for, as they name themselves, in the file's order;
// "" for one that cannot be read as a certificate.
func (in *Info) Subjects() []string {
	out := make([]string, len(in.recipients))
	for i, r := range in.recipients {
		if c, err := x509.ParseCertificate(r.cert); err == nil {
			out[i] = c.Subject.String()
		}
	}
	return out
}

// String describes the encryption: "agile, AES-256, SHA512".
func (in *Info) String() string {
	if !in.agile {
		return fmt.Sprintf("standard, AES-%d, SHA1", in.keyBytes*8)
	}
	return fmt.Sprintf("agile, AES-%d, %s", in.data.keyBytes*8, in.data.hashName)
}

// passwordBytes returns a password as the schemes hash it: in UTF-16, low
// byte first.
func passwordBytes(password string) []byte {
	units := utf16.Encode([]rune(password))
	out := make([]byte, 2*len(units))
	for i, u := range units {
		binary.LittleEndian.PutUint16(out[2*i:], u)
	}
	return out
}

// spun hashes the password with the salt, then the result again and
// again, each time with the number of the round in front. It looks at ctx
// every few thousand rounds.
func spun(ctx context.Context, h hash.Hash, salt []byte, password string, rounds int) ([]byte, error) {
	h.Write(salt)
	h.Write(passwordBytes(password))
	sum := h.Sum(nil)
	var count [4]byte
	for i := 0; i < rounds; i++ {
		if i&0xFFF == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		binary.LittleEndian.PutUint32(count[:], uint32(i))
		h.Reset()
		h.Write(count[:])
		h.Write(sum)
		sum = h.Sum(sum[:0])
	}
	return sum, nil
}

// fit returns b cut or padded to n bytes. The padding is the byte 0x36,
// as the scheme has it.
func fit(b []byte, n int) []byte {
	out := make([]byte, n)
	for i := copy(out, b); i < n; i++ {
		out[i] = 0x36
	}
	return out
}

// The agile scheme derives a key for each purpose by hashing the spun
// password hash with one of these.
var (
	blockVerifierIn = []byte{0xfe, 0xa7, 0xd2, 0x76, 0x3b, 0x4b, 0x9e, 0x79}
	blockVerifierIs = []byte{0xd7, 0xaa, 0x0f, 0x6d, 0x30, 0x61, 0x34, 0x4e}
	blockKeyValue   = []byte{0x14, 0x6e, 0x0b, 0xe7, 0xab, 0xac, 0xd0, 0xd6}
	blockHmacKey    = []byte{0x5f, 0xb2, 0xad, 0x01, 0x0c, 0xb9, 0xe1, 0xf6}
	blockHmacValue  = []byte{0xa0, 0x67, 0x7f, 0x02, 0xb2, 0x2c, 0x84, 0x33}
)

// cbc decrypts src, a whole number of blocks, with AES in CBC mode.
func cbc(key, iv, src []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	out := make([]byte, len(src))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, src)
	return out, nil
}

// Key derives the key of the package from the password and verifies it.
// The error is ErrPassword if the password is not the file's, ErrSpinCount
// if the file asks for more than maxSpin rounds of hashing (zero for no
// limit), or the context's.
func (in *Info) Key(ctx context.Context, password string, maxSpin int) ([]byte, error) {
	if !in.Password() {
		return nil, fmt.Errorf("%w: the key is encrypted only for certificates", ErrPassword)
	}
	if maxSpin > 0 && in.SpinCount() > maxSpin {
		return nil, fmt.Errorf("%w: %d rounds, limit %d", ErrSpinCount, in.SpinCount(), maxSpin)
	}
	if !in.agile {
		return in.standardKey(ctx, password)
	}
	h := in.key.newHash()
	sum, err := spun(ctx, h, in.key.salt, password, in.spin)
	if err != nil {
		return nil, err
	}
	derive := func(purpose []byte) []byte {
		h.Reset()
		h.Write(sum)
		h.Write(purpose)
		return fit(h.Sum(nil), in.key.keyBytes)
	}
	iv := fit(in.key.salt, in.key.blockSize)
	// A random value and its hash, each encrypted with a key of its own:
	// the password is right if the one decrypts to the hash of the other.
	input, err := cbc(derive(blockVerifierIn), iv, in.verifierIn)
	if err != nil {
		return nil, err
	}
	value, err := cbc(derive(blockVerifierIs), iv, in.verifierIs)
	if err != nil {
		return nil, err
	}
	h.Reset()
	h.Write(input[:len(in.key.salt)])
	if subtle.ConstantTimeCompare(h.Sum(nil), value[:in.key.hashSize]) != 1 {
		return nil, ErrPassword
	}
	key, err := cbc(derive(blockKeyValue), iv, in.keyValue)
	if err != nil {
		return nil, err
	}
	return key[:in.data.keyBytes], nil
}

// maxDecrypts bounds the decryptions with private keys one file may ask
// for. Each costs a millisecond or more, and a key held in hardware or
// another process may cost much more; a file names few certificates.
const maxDecrypts = 64

// CertificateKey derives the key of the package with one of the private
// keys, and verifies it. A key is tried on a certificate whose public key
// is its own; on any certificate of its size, if the certificate cannot
// be read. subject is that of the certificate the key opened. The error
// is ErrNoKey if none of the keys opens the file.
func (in *Info) CertificateKey(keys []crypto.Decrypter) (key []byte, subject string, err error) {
	tried := 0
	seen := map[string]bool{}
	for _, r := range in.recipients {
		if seen[string(r.keyValue)] {
			continue
		}
		seen[string(r.keyValue)] = true
		var certKey *rsa.PublicKey
		subject = ""
		if c, err := x509.ParseCertificate(r.cert); err == nil {
			pub, ok := c.PublicKey.(*rsa.PublicKey)
			if !ok {
				continue // only RSA encrypts a key in the scheme
			}
			certKey, subject = pub, c.Subject.String()
		}
		for _, k := range keys {
			if k == nil {
				continue
			}
			pub, ok := k.Public().(*rsa.PublicKey)
			if !ok || pub.Size() != len(r.keyValue) || certKey != nil && !certKey.Equal(pub) {
				continue
			}
			// The scheme does not say how the key is padded for RSA.
			// PKCS #1 v1.5 is what Office's cryptographic interface
			// gives by default; OAEP is tried when that does not give the
			// key. The verifier decides either way. A key padded with OAEP
			// reads as PKCS #1 v1.5 padding about once in 256, so padding
			// that reads is no sign of which one was used.
			for _, opts := range []crypto.DecrypterOpts{&rsa.PKCS1v15DecryptOptions{}, &rsa.OAEPOptions{Hash: crypto.SHA1}} {
				if tried == maxDecrypts {
					return nil, "", fmt.Errorf("%w: %d decryptions tried, the most allowed", ErrNoKey, maxDecrypts)
				}
				tried++
				plain, err := k.Decrypt(rand.Reader, r.keyValue, opts)
				if err == nil && len(plain) == in.data.keyBytes && in.certVerified(r, plain) {
					return plain, subject, nil
				}
			}
		}
	}
	return nil, "", ErrNoKey
}

// certVerified reports whether the certificate's verifier is the HMAC of
// the certificate keyed with key, as it is when key is the key of the
// package.
func (in *Info) certVerified(r recipient, key []byte) bool {
	mac := hmac.New(in.data.newHash, key)
	mac.Write(r.cert)
	sum := mac.Sum(nil)
	return len(r.verifier) >= len(sum) && hmac.Equal(sum, r.verifier[:len(sum)])
}

func (in *Info) standardKey(ctx context.Context, password string) ([]byte, error) {
	h := sha1.New()
	sum, err := spun(ctx, h, in.salt, password, 50000)
	if err != nil {
		return nil, err
	}
	// The final hash, with a block number of zero after it, is spread
	// over two hashes to make a key of any of the lengths.
	h.Reset()
	h.Write(sum)
	h.Write([]byte{0, 0, 0, 0})
	final := h.Sum(nil)
	var key []byte
	for _, pad := range []byte{0x36, 0x5C} {
		var buf [64]byte
		for i := range buf {
			buf[i] = pad
		}
		for i, b := range final {
			buf[i] ^= b
		}
		h.Reset()
		h.Write(buf[:])
		key = h.Sum(key)
	}
	key = key[:in.keyBytes]
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	var verifier [16]byte
	var sum2 [32]byte
	block.Decrypt(verifier[:], in.verifier)
	block.Decrypt(sum2[:16], in.verifierSum[:16])
	block.Decrypt(sum2[16:], in.verifierSum[16:])
	want := sha1.Sum(verifier[:])
	if subtle.ConstantTimeCompare(want[:], sum2[:sha1.Size]) != 1 {
		return nil, ErrPassword
	}
	return key, nil
}

// segment is the length of the pieces the agile scheme encrypts a
// package in, each with a starting value of its own.
const segment = 4096

// Package is a decrypted package. It is read at any offset, as a zip
// archive needs, and decrypts what is asked for as it is asked for. It
// is safe for concurrent use.
type Package struct {
	in    *Info
	src   io.ReaderAt // the EncryptedPackage stream
	size  int64       // of the package
	block cipher.Block
	pool  sync.Pool
}

type scratch struct {
	buf []byte
	h   hash.Hash
	iv  []byte
}

// Open returns the package in the EncryptedPackage stream src, of the
// given length, decrypted with key.
func (in *Info) Open(src io.ReaderAt, length int64, key []byte) (*Package, error) {
	var head [8]byte
	if length < 8 {
		return nil, fmt.Errorf("%w: an encrypted package of %d bytes", ErrMalformed, length)
	}
	if _, err := src.ReadAt(head[:], 0); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	// The stream begins with the length of the package; what follows is
	// the package, padded to a whole number of blocks.
	size := int64(binary.LittleEndian.Uint64(head[:]))
	if size < 0 || size > length-8 {
		return nil, fmt.Errorf("%w: a package of %d bytes in an encrypted stream of %d", ErrMalformed, size, length)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	p := &Package{in: in, src: src, size: size, block: block}
	p.pool.New = func() any {
		sc := &scratch{buf: make([]byte, segment)}
		if in.agile {
			sc.h = in.data.newHash()
		}
		return sc
	}
	return p, nil
}

// Size returns the length of the package.
func (p *Package) Size() int64 { return p.size }

// ReadAt reads from the package at the given offset, as io.ReaderAt.
func (p *Package) ReadAt(out []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("crypt: negative offset")
	}
	sc := p.pool.Get().(*scratch)
	defer p.pool.Put(sc)
	n := 0
	for n < len(out) && off < p.size {
		// The piece of the stream that holds this offset: a segment of
		// the agile scheme, or as much as the buffer takes of the other,
		// which has no segments. Either starts on a block.
		start := off &^ (segment - 1)
		length := min(int64(segment), (p.size-start+aes.BlockSize-1)&^(aes.BlockSize-1))
		buf := sc.buf[:length]
		if got, err := p.src.ReadAt(buf, 8+start); int64(got) < length {
			if err == nil || err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return n, fmt.Errorf("%w: the encrypted package ends early: %v", ErrMalformed, err)
		}
		if p.in.agile {
			var index [4]byte
			binary.LittleEndian.PutUint32(index[:], uint32(start/segment))
			sc.h.Reset()
			sc.h.Write(p.in.data.salt)
			sc.h.Write(index[:])
			sc.iv = sc.h.Sum(sc.iv[:0])
			iv := sc.iv
			if len(iv) < aes.BlockSize {
				iv = fit(iv, aes.BlockSize)
			}
			cipher.NewCBCDecrypter(p.block, iv[:aes.BlockSize]).CryptBlocks(buf, buf)
		} else {
			for i := 0; i < len(buf); i += aes.BlockSize {
				p.block.Decrypt(buf[i:i+aes.BlockSize], buf[i:i+aes.BlockSize])
			}
		}
		within := off - start
		chunk := copy(out[n:], buf[within:min(length, p.size-start)])
		n += chunk
		off += int64(chunk)
	}
	if n < len(out) {
		return n, io.EOF
	}
	return n, nil
}

// Intact checks the integrity value that the agile scheme stores: a
// keyed hash over the whole encrypted stream. checked is false if the
// file has none (the standard scheme, or an agile file without it); ok
// says the stream is what was encrypted.
func (in *Info) Intact(ctx context.Context, src io.ReaderAt, length int64, key []byte) (ok, checked bool, err error) {
	if !in.agile || len(in.hmacKey) == 0 {
		return false, false, nil
	}
	h := in.data.newHash()
	iv := func(purpose []byte) []byte {
		h.Reset()
		h.Write(in.data.salt)
		h.Write(purpose)
		return fit(h.Sum(nil), in.data.blockSize)
	}
	macKey, err := cbc(key, iv(blockHmacKey), in.hmacKey)
	if err != nil {
		return false, false, err
	}
	want, err := cbc(key, iv(blockHmacValue), in.hmacValue)
	if err != nil {
		return false, false, err
	}
	mac := hmac.New(in.data.newHash, macKey[:in.data.hashSize])
	buf := make([]byte, 64<<10)
	for off := int64(0); off < length; {
		if ctx.Err() != nil {
			return false, false, ctx.Err()
		}
		n, err := src.ReadAt(buf[:min(int64(len(buf)), length-off)], off)
		mac.Write(buf[:n])
		off += int64(n)
		if err != nil && off < length {
			return false, true, nil // it ends early: not what was encrypted
		}
	}
	return hmac.Equal(mac.Sum(nil), want[:in.data.hashSize]), true, nil
}
