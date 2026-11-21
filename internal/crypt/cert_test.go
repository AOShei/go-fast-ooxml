package crypt_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/AOShei/go-fast-ooxml/internal/crypt"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// person is a key and its certificate, made once for all the tests.
type person struct {
	key  *rsa.PrivateKey
	cert []byte
}

var people = sync.OnceValue(func() map[string]person {
	out := map[string]person{}
	for name, bits := range map[string]int{"Alice": 2048, "Bob": 2048, "Carol": 3072} {
		key, cert := ooxmltest.Certificate(name, bits)
		out[name] = person{key, cert}
	}
	return out
})

// counting is a private key that counts its decryptions.
type counting struct {
	*rsa.PrivateKey
	n *atomic.Int64
}

func (c counting) Decrypt(r io.Reader, msg []byte, opts crypto.DecrypterOpts) ([]byte, error) {
	c.n.Add(1)
	return c.PrivateKey.Decrypt(r, msg, opts)
}

func parse(t *testing.T, info []byte) *crypt.Info {
	t.Helper()
	in, err := crypt.Parse(bytes.NewReader(info), int64(len(info)))
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// The key of the package encrypted for certificates as well as for the
// password: each certificate's private key opens the package, whichever
// padding the key was encrypted with, and so does the password.
func TestCertificates(t *testing.T) {
	p := people()
	want := content(9000, 7)
	for _, hash := range []string{"SHA1", "SHA256", "SHA512"} {
		for _, bits := range []int{128, 256} {
			how := ooxmltest.Encryption{SpinCount: 20, Hash: hash, KeyBits: bits, Certificates: []ooxmltest.Recipient{
				{Cert: p["Alice"].cert},
				{Cert: p["Carol"].cert, OAEP: true},
			}}
			info, encrypted := ooxmltest.EncryptStreams(want, "pw", how)
			in := parse(t, info)
			if !in.Password() || in.Certificates() != 2 {
				t.Fatalf("%s/%d: password %v, %d certificates", hash, bits, in.Password(), in.Certificates())
			}
			if got := strings.Join(in.Subjects(), "|"); got != "CN=Alice|CN=Carol" {
				t.Errorf("subjects %q", got)
			}
			for _, name := range []string{"Alice", "Carol"} {
				// Among keys that are not the file's, in any order.
				keys := []crypto.Decrypter{p["Bob"].key, p[name].key}
				key, subject, err := in.CertificateKey(keys)
				if err != nil {
					t.Fatalf("%s/%d, %s: %v", hash, bits, name, err)
				}
				if subject != "CN="+name || len(key) != bits/8 {
					t.Errorf("%s/%d, %s: subject %q, key of %d bytes", hash, bits, name, subject, len(key))
				}
				byPassword, err := in.Key(context.Background(), "pw", 0)
				if err != nil || !bytes.Equal(key, byPassword) {
					t.Errorf("%s/%d, %s: the key is not the one the password gives (%v)", hash, bits, name, err)
				}
				pkg, err := in.Open(bytes.NewReader(encrypted), int64(len(encrypted)), key)
				if err != nil {
					t.Fatal(err)
				}
				got := make([]byte, pkg.Size())
				if _, err := pkg.ReadAt(got, 0); err != nil && err != io.EOF || !bytes.Equal(got, want) {
					t.Errorf("%s/%d, %s: the package differs (%v)", hash, bits, name, err)
				}
				if ok, checked, err := in.Intact(context.Background(), bytes.NewReader(encrypted), int64(len(encrypted)), key); !ok || !checked || err != nil {
					t.Errorf("%s/%d, %s: integrity %v %v %v", hash, bits, name, ok, checked, err)
				}
			}
			if _, _, err := in.CertificateKey([]crypto.Decrypter{p["Bob"].key, nil}); !errors.Is(err, crypt.ErrNoKey) {
				t.Errorf("%s/%d: a key for no certificate of the file: %v", hash, bits, err)
			}
		}
	}
}

// A key is tried only on a certificate for its public key: one that is
// not the file's is never asked to decrypt.
func TestOnlyTheKeyOfACertificateIsTried(t *testing.T) {
	p := people()
	info, _ := ooxmltest.EncryptStreams([]byte("x"), "pw", ooxmltest.Encryption{SpinCount: 1, Certificates: []ooxmltest.Recipient{{Cert: p["Alice"].cert}}})
	in := parse(t, info)
	var n atomic.Int64
	if _, _, err := in.CertificateKey([]crypto.Decrypter{counting{p["Bob"].key, &n}}); !errors.Is(err, crypt.ErrNoKey) || n.Load() != 0 {
		t.Errorf("%v after %d decryptions", err, n.Load())
	}
	if _, _, err := in.CertificateKey([]crypto.Decrypter{counting{p["Alice"].key, &n}}); err != nil || n.Load() != 1 {
		t.Errorf("%v after %d decryptions", err, n.Load())
	}
}

// A key padded with OAEP whose block also reads as PKCS #1 v1.5 padding,
// as about one in 256 do: the PKCS #1 v1.5 reading gives no key that
// verifies, and OAEP is tried after it.
func TestOAEPThatReadsAsPKCS1(t *testing.T) {
	p := people()
	for i := range 10000 {
		info, _ := ooxmltest.EncryptStreams([]byte("x"), "pw", ooxmltest.Encryption{SpinCount: 1, Certificates: []ooxmltest.Recipient{{Cert: p["Bob"].cert, OAEP: true}}})
		_, rest, _ := strings.Cut(string(info), `<c:encryptedKey encryptedKeyValue="`)
		value, _, _ := strings.Cut(rest, `"`)
		encrypted, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := rsa.DecryptPKCS1v15(nil, p["Bob"].key, encrypted); err != nil {
			continue
		}
		var n atomic.Int64
		if _, _, err := parse(t, info).CertificateKey([]crypto.Decrypter{counting{p["Bob"].key, &n}}); err != nil || n.Load() != 2 {
			t.Errorf("after %d encryptions: %v after %d decryptions", i+1, err, n.Load())
		}
		return
	}
	t.Fatal("no key padded with OAEP read as PKCS #1 v1.5 in 10,000 encryptions")
}

// What the description says must be passed over is: a certificate whose
// verifier does not match the key it holds. A certificate whose key is
// not the package's, with a verifier made for that key, is not known
// from the encryption information; the package then decrypts to nothing
// readable, and its integrity check fails.
func TestCertificatesThatDoNotOpenTheFile(t *testing.T) {
	p := people()
	want := content(5000, 9)
	info, _ := ooxmltest.EncryptStreams(want, "pw", ooxmltest.Encryption{SpinCount: 1, Certificates: []ooxmltest.Recipient{{Cert: p["Alice"].cert, BadVerifier: true}}})
	if _, _, err := parse(t, info).CertificateKey([]crypto.Decrypter{p["Alice"].key}); !errors.Is(err, crypt.ErrNoKey) {
		t.Errorf("a verifier that does not match: %v", err)
	}
	stale := bytes.Repeat([]byte{7}, 32)
	info, encrypted := ooxmltest.EncryptStreams(want, "pw", ooxmltest.Encryption{SpinCount: 1, Certificates: []ooxmltest.Recipient{{Cert: p["Alice"].cert, Key: stale}}})
	in := parse(t, info)
	key, _, err := in.CertificateKey([]crypto.Decrypter{p["Alice"].key})
	if err != nil || !bytes.Equal(key, stale) {
		t.Fatalf("%v", err)
	}
	if ok, checked, _ := in.Intact(context.Background(), bytes.NewReader(encrypted), int64(len(encrypted)), key); ok || !checked {
		t.Errorf("a key that is not the package's passes the integrity check")
	}
	// A key of another length than the package's is not taken.
	info, _ = ooxmltest.EncryptStreams(want, "pw", ooxmltest.Encryption{SpinCount: 1, Certificates: []ooxmltest.Recipient{{Cert: p["Alice"].cert, Key: stale[:16]}}})
	if _, _, err := parse(t, info).CertificateKey([]crypto.Decrypter{p["Alice"].key}); !errors.Is(err, crypt.ErrNoKey) {
		t.Errorf("a key of 16 bytes for a package of 32: %v", err)
	}
}

// A file that names its certificate many times costs a bounded number of
// decryptions, however many.
func TestDecryptionsAreBounded(t *testing.T) {
	p := people()
	var recipients []ooxmltest.Recipient
	for range 200 {
		recipients = append(recipients, ooxmltest.Recipient{Cert: p["Alice"].cert, BadVerifier: true})
	}
	info, _ := ooxmltest.EncryptStreams([]byte("x"), "pw", ooxmltest.Encryption{SpinCount: 1, Certificates: recipients})
	in := parse(t, info)
	if in.Certificates() != 200 {
		t.Fatalf("%d certificates", in.Certificates())
	}
	var n atomic.Int64
	if _, _, err := in.CertificateKey([]crypto.Decrypter{counting{p["Alice"].key, &n}}); !errors.Is(err, crypt.ErrNoKey) || n.Load() > 64 {
		t.Errorf("%v after %d decryptions", err, n.Load())
	}
}

// A file whose key is encrypted only for certificates, against the
// description, which asks for a password too: a key opens it, a password
// never does.
func TestCertificatesOnly(t *testing.T) {
	p := people()
	info, encrypted := ooxmltest.EncryptStreams([]byte("only for Alice"), "pw", ooxmltest.Encryption{SpinCount: 1, NoPassword: true, Certificates: []ooxmltest.Recipient{{Cert: p["Alice"].cert}}})
	in := parse(t, info)
	if in.Password() || in.Certificates() != 1 {
		t.Fatalf("password %v, %d certificates", in.Password(), in.Certificates())
	}
	if _, err := in.Key(context.Background(), "pw", 0); !errors.Is(err, crypt.ErrPassword) {
		t.Errorf("the password: %v", err)
	}
	key, _, err := in.CertificateKey([]crypto.Decrypter{p["Alice"].key})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := in.Open(bytes.NewReader(encrypted), int64(len(encrypted)), key)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]byte, pkg.Size())
	pkg.ReadAt(got, 0)
	if string(got) != "only for Alice" {
		t.Errorf("%q", got)
	}
	// Neither a password nor a certificate: nothing opens it.
	info, _ = ooxmltest.EncryptStreams([]byte("x"), "pw", ooxmltest.Encryption{SpinCount: 1, NoPassword: true})
	if _, err := crypt.Parse(bytes.NewReader(info), int64(len(info))); !errors.Is(err, crypt.ErrUnsupported) {
		t.Errorf("no key encryptor: %v", err)
	}
}

// Certificate encryptors that cannot be read are passed over, and the
// file is opened by what remains; a certificate that cannot be read as
// one is tried with every key of its size.
func TestCertificateEncryptorsThatCannotBeRead(t *testing.T) {
	p := people()
	info, _ := ooxmltest.EncryptStreams([]byte("x"), "pw", ooxmltest.Encryption{SpinCount: 1, Certificates: []ooxmltest.Recipient{{Cert: p["Alice"].cert}, {Cert: p["Bob"].cert}}})
	// Bob's certificate is spoiled: the attribute is not base64.
	bob := []byte(`X509Certificate="` + b64(p["Bob"].cert) + `"`)
	spoiled := bytes.Replace(info, bob, []byte(`X509Certificate="!!"`), 1)
	in := parse(t, spoiled)
	if in.Certificates() != 1 || !in.Password() {
		t.Fatalf("%d certificates", in.Certificates())
	}
	if _, err := in.Key(context.Background(), "pw", 0); err != nil {
		t.Errorf("the password: %v", err)
	}
	if _, _, err := in.CertificateKey([]crypto.Decrypter{p["Alice"].key}); err != nil {
		t.Errorf("Alice: %v", err)
	}

	info, _ = ooxmltest.EncryptStreams([]byte("x"), "pw", ooxmltest.Encryption{SpinCount: 1, Certificates: []ooxmltest.Recipient{{Cert: p["Alice"].cert, NotACertificate: true}}})
	in = parse(t, info)
	if got := in.Subjects(); len(got) != 1 || got[0] != "" {
		t.Errorf("subjects %q", got)
	}
	var n atomic.Int64
	if _, _, err := in.CertificateKey([]crypto.Decrypter{counting{p["Carol"].key, &n}}); !errors.Is(err, crypt.ErrNoKey) || n.Load() != 0 {
		t.Errorf("a key of another size: %v after %d decryptions", err, n.Load())
	}
	key, subject, err := in.CertificateKey([]crypto.Decrypter{counting{p["Bob"].key, &n}, p["Alice"].key})
	if err != nil || subject != "" || len(key) != 32 {
		t.Errorf("%v, %q", err, subject)
	}
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
