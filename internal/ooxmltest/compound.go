package ooxmltest

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	crand "crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash"
	"math/big"
	"math/rand"
	"time"
	"unicode/utf16"

	"github.com/AOShei/go-fast-core/cfb/cfbtest"
)

// This file writes what the library only reads: packages encrypted with a
// password, in compound files that go-fast-core's cfbtest writes. It is written from the published
// descriptions of the formats (MS-CFB, MS-OFFCRYPTO), on its own, so
// that a test which encrypts with it and decrypts with the library
// checks one reading of the description against another. The library's
// reading is also held to files that Microsoft Office and LibreOffice
// wrote and to another program that decrypts them.

// Encryption says how Encrypt encrypts a package.
type Encryption struct {
	// Standard selects the scheme of Office 2007; the default is the
	// agile scheme of Office 2010 and later.
	Standard bool
	// KeyBits is 128, 192 or 256; zero means 256 for agile and 128 for
	// standard.
	KeyBits int
	// Hash is SHA1, SHA256, SHA384 or SHA512 (agile only; default
	// SHA512).
	Hash string
	// SpinCount is the number of rounds of hashing (agile only; default
	// 1000, so that tests are quick; Office uses 100,000).
	SpinCount int
	// Seed makes the salts and keys the same on every run.
	Seed int64
	// NoIntegrity leaves the integrity check out (agile only).
	NoIntegrity bool
	// Certificates are certificates the key of the package is encrypted
	// for as well as for the password (agile only).
	Certificates []Recipient
	// NoPassword leaves out the key encrypted for the password, which the
	// description requires of every file (agile only).
	NoPassword bool
}

// Recipient is a certificate the key of a package is encrypted for.
type Recipient struct {
	// Cert is the certificate, DER, with an RSA public key.
	Cert []byte
	// OAEP pads the key with OAEP (SHA-1) for RSA, in place of PKCS #1
	// v1.5.
	OAEP bool
	// BadVerifier stores a verifier that does not match.
	BadVerifier bool
	// Key, if set, is encrypted in place of the key of the package.
	Key []byte
	// NotACertificate stores bytes that are not a certificate in place
	// of Cert, with the key encrypted for Cert all the same.
	NotACertificate bool
}

// Certificate returns a new RSA key of the given size and a certificate
// for it, signed by itself, naming name.
func Certificate(name string, bits int) (*rsa.PrivateKey, []byte) {
	key, err := rsa.GenerateKey(crand.Reader, bits)
	if err != nil {
		panic(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:     time.Date(2036, 1, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage:     x509.KeyUsageKeyEncipherment,
	}
	der, err := x509.CreateCertificate(crand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	return key, der
}

// Encrypt returns the package encrypted with the password: a compound
// file with the EncryptionInfo and EncryptedPackage streams, as Office
// writes one.
func Encrypt(pkg []byte, password string, how Encryption) []byte {
	info, encrypted := EncryptStreams(pkg, password, how)
	return cfbtest.Compound(map[string][]byte{
		"EncryptionInfo":   info,
		"EncryptedPackage": encrypted,
		// What Office puts beside them to say how the package is
		// transformed. Nothing in it is needed to decrypt.
		"\x06DataSpaces/Version": []byte("\x3c\x00\x00\x00M\x00i\x00c\x00r\x00o\x00s\x00o\x00f\x00t\x00"),
	})
}

// EncryptStreams returns the two streams of an encrypted package.
func EncryptStreams(pkg []byte, password string, how Encryption) (info, encrypted []byte) {
	rng := rand.New(rand.NewSource(how.Seed + 1))
	random := func(n int) []byte {
		b := make([]byte, n)
		rng.Read(b)
		return b
	}
	le := binary.LittleEndian
	units := utf16.Encode([]rune(password))
	pw := make([]byte, 2*len(units))
	for i, u := range units {
		le.PutUint16(pw[2*i:], u)
	}
	spin := func(h hash.Hash, salt []byte, rounds int) []byte {
		h.Reset()
		h.Write(salt)
		h.Write(pw)
		sum := h.Sum(nil)
		for i := 0; i < rounds; i++ {
			var n [4]byte
			le.PutUint32(n[:], uint32(i))
			h.Reset()
			h.Write(n[:])
			h.Write(sum)
			sum = h.Sum(nil)
		}
		return sum
	}
	padded := func(b []byte, to int) []byte {
		return append(append([]byte(nil), b...), make([]byte, (to-len(b)%to)%to)...)
	}
	size := make([]byte, 8)
	le.PutUint64(size, uint64(len(pkg)))

	if how.Standard {
		keyBits := how.KeyBits
		if keyBits == 0 {
			keyBits = 128
		}
		salt := random(16)
		h := sha1.New()
		sum := spin(h, salt, 50000)
		h.Reset()
		h.Write(sum)
		h.Write([]byte{0, 0, 0, 0})
		final := h.Sum(nil)
		var key []byte
		for _, pad := range []byte{0x36, 0x5C} {
			buf := bytes.Repeat([]byte{pad}, 64)
			for i, b := range final {
				buf[i] ^= b
			}
			x := sha1.Sum(buf)
			key = append(key, x[:]...)
		}
		block, _ := aes.NewCipher(key[:keyBits/8])
		ecb := func(b []byte) []byte {
			out := padded(b, 16)
			for i := 0; i < len(out); i += 16 {
				block.Encrypt(out[i:i+16], out[i:i+16])
			}
			return out
		}
		verifier := random(16)
		sumV := sha1.Sum(verifier)
		alg := map[int]uint32{128: 0x660E, 192: 0x660F, 256: 0x6610}[keyBits]
		var header bytes.Buffer
		for _, v := range []uint32{0x24, 0, alg, 0x8004, uint32(keyBits), 0x18, 0, 0} {
			binary.Write(&header, le, v)
		}
		for _, u := range utf16.Encode([]rune("Microsoft Enhanced RSA and AES Cryptographic Provider\x00")) {
			binary.Write(&header, le, u)
		}
		var out bytes.Buffer
		binary.Write(&out, le, []uint16{4, 2})
		binary.Write(&out, le, []uint32{0x24, uint32(header.Len())})
		out.Write(header.Bytes())
		binary.Write(&out, le, uint32(16))
		out.Write(salt)
		out.Write(ecb(verifier))
		binary.Write(&out, le, uint32(20))
		out.Write(ecb(sumV[:]))
		return out.Bytes(), append(size, ecb(pkg)...)
	}

	keyBits, name, rounds := how.KeyBits, how.Hash, how.SpinCount
	if keyBits == 0 {
		keyBits = 256
	}
	if name == "" {
		name = "SHA512"
	}
	if rounds == 0 {
		rounds = 1000
	}
	newHash := map[string]func() hash.Hash{"SHA1": sha1.New, "SHA256": sha256.New, "SHA384": sha512.New384, "SHA512": sha512.New}[name]
	h := newHash()
	fit := func(b []byte, n int) []byte {
		out := bytes.Repeat([]byte{0x36}, n)
		copy(out, b)
		return out
	}
	sumOf := func(parts ...[]byte) []byte {
		h.Reset()
		for _, p := range parts {
			h.Write(p)
		}
		return h.Sum(nil)
	}
	cbc := func(key, iv, plain []byte) []byte {
		block, _ := aes.NewCipher(key)
		out := padded(plain, 16)
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, out)
		return out
	}
	dataSalt, keySalt := random(16), random(16)
	secret := random(keyBits / 8)
	spunSum := spin(h, keySalt, rounds)
	derive := func(purpose []byte) []byte { return fit(sumOf(spunSum, purpose), keyBits/8) }
	verifier := random(16)
	encVerifierIn := cbc(derive([]byte{0xfe, 0xa7, 0xd2, 0x76, 0x3b, 0x4b, 0x9e, 0x79}), keySalt, verifier)
	encVerifierIs := cbc(derive([]byte{0xd7, 0xaa, 0x0f, 0x6d, 0x30, 0x61, 0x34, 0x4e}), keySalt, sumOf(verifier))
	encKey := cbc(derive([]byte{0x14, 0x6e, 0x0b, 0xe7, 0xab, 0xac, 0xd0, 0xd6}), keySalt, secret)

	encrypted = size
	for i := 0; i*4096 < len(pkg); i++ {
		var n [4]byte
		le.PutUint32(n[:], uint32(i))
		encrypted = append(encrypted, cbc(secret, fit(sumOf(dataSalt, n[:]), 16), pkg[i*4096:min(len(pkg), i*4096+4096)])...)
	}
	integrity := ""
	if !how.NoIntegrity {
		macKey := random(h.Size())
		mac := hmac.New(newHash, macKey)
		mac.Write(encrypted)
		encMacKey := cbc(secret, fit(sumOf(dataSalt, []byte{0x5f, 0xb2, 0xad, 0x01, 0x0c, 0xb9, 0xe1, 0xf6}), 16), macKey)
		encMacValue := cbc(secret, fit(sumOf(dataSalt, []byte{0xa0, 0x67, 0x7f, 0x02, 0xb2, 0x2c, 0x84, 0x33}), 16), mac.Sum(nil))
		integrity = fmt.Sprintf(`<dataIntegrity encryptedHmacKey="%s" encryptedHmacValue="%s"/>`, base64.StdEncoding.EncodeToString(encMacKey), base64.StdEncoding.EncodeToString(encMacValue))
	}
	b64 := base64.StdEncoding.EncodeToString
	common := fmt.Sprintf(`saltSize="16" blockSize="16" keyBits="%d" hashSize="%d" cipherAlgorithm="AES" cipherChaining="ChainingModeCBC" hashAlgorithm="%s"`, keyBits, h.Size(), name)
	encryptors := ""
	if !how.NoPassword {
		encryptors = `<keyEncryptor uri="http://schemas.microsoft.com/office/2006/keyEncryptor/password">` +
			fmt.Sprintf(`<p:encryptedKey spinCount="%d" `, rounds) + common + ` saltValue="` + b64(keySalt) + `"` +
			` encryptedVerifierHashInput="` + b64(encVerifierIn) + `" encryptedVerifierHashValue="` + b64(encVerifierIs) + `" encryptedKeyValue="` + b64(encKey) + `"/>` +
			`</keyEncryptor>`
	}
	// For each certificate: the key encrypted with its public key, and
	// the HMAC of the certificate keyed with the key, which says that it
	// is this file's key that was encrypted.
	for _, r := range how.Certificates {
		cert, err := x509.ParseCertificate(r.Cert)
		if err != nil {
			panic(err)
		}
		pub := cert.PublicKey.(*rsa.PublicKey)
		plain := secret
		if r.Key != nil {
			plain = r.Key
		}
		var encrypted []byte
		if r.OAEP {
			encrypted, err = rsa.EncryptOAEP(sha1.New(), crand.Reader, pub, plain, nil)
		} else {
			encrypted, err = rsa.EncryptPKCS1v15(crand.Reader, pub, plain)
		}
		if err != nil {
			panic(err)
		}
		stored := r.Cert
		if r.NotACertificate {
			stored = []byte("these bytes are not a certificate")
		}
		mac := hmac.New(newHash, plain)
		mac.Write(stored)
		verifier := mac.Sum(nil)
		if r.BadVerifier {
			verifier[0] ^= 1
		}
		encryptors += `<keyEncryptor uri="http://schemas.microsoft.com/office/2006/keyEncryptor/certificate">` +
			`<c:encryptedKey encryptedKeyValue="` + b64(encrypted) + `" X509Certificate="` + b64(stored) + `" certVerifier="` + b64(verifier) + `"/>` +
			`</keyEncryptor>`
	}
	xml := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\r\n" +
		`<encryption xmlns="http://schemas.microsoft.com/office/2006/encryption" xmlns:p="http://schemas.microsoft.com/office/2006/keyEncryptor/password" xmlns:c="http://schemas.microsoft.com/office/2006/keyEncryptor/certificate">` +
		`<keyData ` + common + ` saltValue="` + b64(dataSalt) + `"/>` + integrity +
		`<keyEncryptors>` + encryptors + `</keyEncryptors></encryption>`
	info = append([]byte{4, 0, 4, 0, 0x40, 0, 0, 0}, xml...)
	return info, encrypted
}
