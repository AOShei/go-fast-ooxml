package gofastooxml_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// keyPair is a private key and its certificate, made once for all the
// tests.
type keyPair struct {
	key  *rsa.PrivateKey
	cert []byte
}

var keyPairs = sync.OnceValue(func() map[string]keyPair {
	out := map[string]keyPair{}
	for _, name := range []string{"Alice", "Bob", "Mallory"} {
		key, cert := ooxmltest.Certificate(name, 2048)
		out[name] = keyPair{key, cert}
	}
	return out
})

// A file whose key is encrypted for certificates as well as for a
// password: Options.Keys opens it and gives the document the file gives
// unencrypted, as the password does. The warning says how it was opened
// and whom the key is encrypted for.
func TestCertificateEncryptedFiles(t *testing.T) {
	k := keyPairs()
	how := ooxmltest.Encryption{SpinCount: 100, Certificates: []ooxmltest.Recipient{{Cert: k["Alice"].cert}, {Cert: k["Bob"].cert, OAEP: true}}}
	n := 0
	for _, s := range samples(t) {
		if !strings.HasPrefix(s.name, "word ") && !strings.HasPrefix(s.name, "many ") {
			continue
		}
		n++
		opts := &gofastcore.Options{Images: gofastcore.ImagesData, Child: childExtractor, Workers: 4}
		plain, err := extract(t, s.data, opts)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := withoutDecrypted(t, plain)
		data := ooxmltest.Encrypt(s.data, "pw", how)
		for name, set := range map[string]func(*gofastcore.Options){
			"Alice's key": func(o *gofastcore.Options) { o.Keys = []crypto.Decrypter{k["Alice"].key} },
			"Bob's key, after one that is not the file's": func(o *gofastcore.Options) { o.Keys = []crypto.Decrypter{k["Mallory"].key, k["Bob"].key} },
			"the password":           func(o *gofastcore.Options) { o.Password = "pw" },
			"a key and the password": func(o *gofastcore.Options) { o.Keys, o.Password = []crypto.Decrypter{k["Alice"].key}, "pw" },
		} {
			with := *opts
			set(&with)
			doc, err := extract(t, data, &with)
			if err != nil {
				t.Errorf("%s, %s: %v", s.name, name, err)
				continue
			}
			ooxmltest.CheckDocument(t, doc)
			got, said := withoutDecrypted(t, doc)
			if got != want {
				t.Errorf("%s, %s: the document differs from that of the file unencrypted", s.name, name)
			}
			if len(said) != 1 || said[0].Code != gofastooxml.WarnDecrypted || !strings.Contains(said[0].Message, "the key is encrypted for a password and for 2 certificates: CN=Alice; CN=Bob") {
				t.Errorf("%s, %s: warnings %v", s.name, name, said)
				continue
			}
			byKey := strings.Contains(said[0].Message, "decrypted with the private key given for its certificate CN=")
			if byKey == (name == "the password") {
				t.Errorf("%s, %s: %q", s.name, name, said[0].Message)
			}
		}
	}
	if n < 3 {
		t.Errorf("%d samples", n)
	}
}

// What nothing given opens is refused with each of the shared errors that
// says what would open it.
func TestCertificateEncryptedFilesNotOpened(t *testing.T) {
	k := keyPairs()
	plain := ooxmltest.Docx(para(run(text("for Alice")))).Bytes()
	both := ooxmltest.Encrypt(plain, "pw", ooxmltest.Encryption{SpinCount: 10, Certificates: []ooxmltest.Recipient{{Cert: k["Alice"].cert}}})
	only := ooxmltest.Encrypt(plain, "pw", ooxmltest.Encryption{SpinCount: 10, NoPassword: true, Certificates: []ooxmltest.Recipient{{Cert: k["Alice"].cert}}})
	mallory := []crypto.Decrypter{k["Mallory"].key}
	for _, c := range []struct {
		name     string
		data     []byte
		password string
		keys     []crypto.Decrypter
		want     []error
		not      []error
		says     string
	}{
		{"nothing given", both, "", nil, []error{gofastcore.ErrPasswordRequired, gofastcore.ErrKeyRequired}, []error{gofastcore.ErrWrongPassword}, "no password given; no private key given"},
		{"another password", both, "guess", nil, []error{gofastcore.ErrWrongPassword, gofastcore.ErrKeyRequired}, []error{gofastcore.ErrPasswordRequired}, "the password given does not open it; no private key given"},
		{"another key", both, "", mallory, []error{gofastcore.ErrPasswordRequired, gofastcore.ErrKeyRequired}, nil, "the private key given is for none of its certificates"},
		{"keys that are not the file's", both, "", []crypto.Decrypter{k["Mallory"].key, k["Bob"].key}, []error{gofastcore.ErrKeyRequired}, nil, "none of the 2 private keys given is for one of its certificates"},
		{"only a certificate, nothing given", only, "", nil, []error{gofastcore.ErrKeyRequired}, []error{gofastcore.ErrPasswordRequired, gofastcore.ErrWrongPassword}, "(agile, AES-256, SHA512; the key is encrypted for 1 certificate: CN=Alice): no private key given"},
		{"only a certificate, a password", only, "pw", mallory, []error{gofastcore.ErrKeyRequired}, []error{gofastcore.ErrPasswordRequired, gofastcore.ErrWrongPassword}, "the private key given is for none of its certificates"},
	} {
		doc, err := extract(t, c.data, &gofastcore.Options{Password: c.password, Keys: c.keys})
		if doc != nil || err == nil {
			t.Errorf("%s: read", c.name)
			continue
		}
		for _, e := range c.want {
			if !errors.Is(err, e) {
				t.Errorf("%s: %v is not %v", c.name, err, e)
			}
		}
		for _, e := range c.not {
			if errors.Is(err, e) {
				t.Errorf("%s: %v is %v", c.name, err, e)
			}
		}
		if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %q does not say %q", c.name, err, c.says)
		}
	}
	// Only a certificate: Alice's key opens it.
	doc, err := extract(t, only, &gofastcore.Options{Keys: []crypto.Decrypter{k["Alice"].key}})
	if err != nil || !strings.Contains(doc.Text(), "for Alice") {
		t.Errorf("only a certificate, Alice's key: %v", err)
	}
}

// A key costs no rounds of hashing: a file that asks more of a password
// than the limit allows is opened with a key all the same.
func TestKeysAreNotBoundByTheSpinCount(t *testing.T) {
	k := keyPairs()
	data := ooxmltest.Encrypt(ooxmltest.Docx(para(run(text("x")))).Bytes(), "pw", ooxmltest.Encryption{SpinCount: 5000, Certificates: []ooxmltest.Recipient{{Cert: k["Alice"].cert}}})
	e := &gofastooxml.Extractor{MaxSpinCount: 1000}
	if _, err := e.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), &gofastcore.Options{Password: "pw"}); !errors.Is(err, gofastcore.ErrLimitExceeded) {
		t.Errorf("the password: %v", err)
	}
	if _, err := e.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), &gofastcore.Options{Keys: []crypto.Decrypter{k["Alice"].key}}); err != nil {
		t.Errorf("Alice's key: %v", err)
	}
}

// The keys are passed down to the files embedded in a document, as the
// password is; without one that opens it, an embedded file is listed with
// the reason.
func TestCertificateEncryptedEmbeddedFile(t *testing.T) {
	k := keyPairs()
	inner := ooxmltest.Encrypt(ooxmltest.Xlsx(ooxmltest.Sheet{Name: "Inner", Data: xlRow(1, xlStr("A1", "for Alice only"))}).Bytes(), "inner", ooxmltest.Encryption{SpinCount: 50, Certificates: []ooxmltest.Recipient{{Cert: k["Alice"].cert}}})
	pkg := ooxmltest.Docx(para(run(text("See the attached workbook."))))
	pkg.Rel("word/document.xml", ooxmltest.RelPackage, "embeddings/book.xlsx")
	pkg.AddBytes("word/embeddings/book.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", inner)
	data := pkg.Bytes()
	for name, keys := range map[string][]crypto.Decrypter{"Alice": {k["Alice"].key}, "Mallory": {k["Mallory"].key}} {
		doc, err := extract(t, data, &gofastcore.Options{Child: childExtractor, Keys: keys})
		if err != nil {
			t.Fatal(err)
		}
		if len(doc.Children) != 1 {
			t.Fatalf("%s: %d children", name, len(doc.Children))
		}
		child := doc.Children[0]
		read := strings.Contains(child.Text(), "for Alice only")
		if read != (name == "Alice") {
			t.Errorf("%s: read %v, warnings %v", name, read, child.Warnings)
		}
		if name == "Mallory" && (len(child.Warnings) != 1 || child.Warnings[0].Code != gofastcore.WarnChildEncrypted) {
			t.Errorf("Mallory: warnings %v", child.Warnings)
		}
	}
}

// A certificate whose key is not the package's, with a verifier made for
// that key, decrypts to what is not a package: refused, never read as
// something else.
func TestCertificateForAnotherKey(t *testing.T) {
	k := keyPairs()
	data := ooxmltest.Encrypt(ooxmltest.Docx(para(run(text("x")))).Bytes(), "pw", ooxmltest.Encryption{SpinCount: 10, Certificates: []ooxmltest.Recipient{{Cert: k["Alice"].cert, Key: bytes.Repeat([]byte{9}, 32)}}})
	doc, err := extract(t, data, &gofastcore.Options{Keys: []crypto.Decrypter{k["Alice"].key}})
	if doc != nil || !errors.Is(err, gofastcore.ErrMalformed) {
		t.Errorf("%v", err)
	}
}
