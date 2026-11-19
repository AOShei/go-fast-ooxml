package gofastooxml_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/cfb"
	"github.com/AOShei/go-fast-ooxml/internal/crypt"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// withoutDecrypted returns the document's JSON form without what differs
// between a file and the same file encrypted: its size, and the warning
// that it was decrypted.
func withoutDecrypted(t testing.TB, doc *gofastcore.Document) (string, []gofastcore.Warning) {
	t.Helper()
	copy := *doc
	copy.Source.Size = 0
	var said []gofastcore.Warning
	copy.Warnings = nil
	for _, w := range doc.Warnings {
		if w.Code == gofastooxml.WarnDecrypted || w.Code == gofastooxml.WarnIntegrityMismatch {
			said = append(said, w)
		} else {
			copy.Warnings = append(copy.Warnings, w)
		}
	}
	return asJSON(t, &copy), said
}

// A file encrypted with a password gives, with the password, the
// document it gives unencrypted; without it, or with another, an error
// that says which.
func TestEncryptedFilesGiveTheSameDocument(t *testing.T) {
	hows := map[string]ooxmltest.Encryption{
		"agile":             {SpinCount: 100},
		"agile, SHA-1":      {SpinCount: 100, Hash: "SHA1", KeyBits: 128},
		"agile, unchecked":  {SpinCount: 100, NoIntegrity: true},
		"standard":          {Standard: true},
		"standard, AES-256": {Standard: true, KeyBits: 256},
	}
	for _, s := range samples(t) {
		if !strings.HasPrefix(s.name, "many ") && !strings.HasPrefix(s.name, "word ") && s.name != "hidden" {
			continue
		}
		opts := &gofastcore.Options{Images: gofastcore.ImagesData, Child: childExtractor, Workers: 4}
		plain, err := extract(t, s.data, opts)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := withoutDecrypted(t, plain)
		for name, how := range hows {
			name = s.name + ", " + name
			data := ooxmltest.Encrypt(s.data, "open sesame", how)
			with := *opts
			with.Password = "open sesame"
			doc, err := extract(t, data, &with)
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			ooxmltest.CheckDocument(t, doc)
			got, said := withoutDecrypted(t, doc)
			if got != want {
				t.Errorf("%s: the document differs from that of the file unencrypted", name)
			}
			if doc.Source.Size != int64(len(data)) {
				t.Errorf("%s: size %d, the file has %d bytes", name, doc.Source.Size, len(data))
			}
			scheme := "agile"
			if how.Standard {
				scheme = "standard"
			}
			if len(said) != 1 || said[0].Code != gofastooxml.WarnDecrypted || said[0].Severity != gofastcore.SeverityInfo ||
				!strings.Contains(said[0].Message, scheme) || !strings.Contains(said[0].Message, "the password given") {
				t.Errorf("%s: warnings %v", name, said)
			}

			if doc, err := extract(t, data, opts); doc != nil || !errors.Is(err, gofastcore.ErrPasswordRequired) {
				t.Errorf("%s, no password: %v", name, err)
			}
			wrong := *opts
			wrong.Password = "open sesame "
			if doc, err := extract(t, data, &wrong); doc != nil || !errors.Is(err, gofastcore.ErrWrongPassword) {
				t.Errorf("%s, the wrong password: %v", name, err)
			}
		}
	}
}

// Excel encrypts a workbook that is protected, but has no password to
// open it, with a password of its own, and opens such a file without
// asking. So does this library, whatever password it is given, and says
// so.
func TestTheDefaultPassword(t *testing.T) {
	plain := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: xlRow(1, xlStr("A1", "protected, not secret"))}).Bytes()
	for _, how := range []ooxmltest.Encryption{{SpinCount: 100}, {Standard: true}} {
		data := ooxmltest.Encrypt(plain, "VelvetSweatshop", how)
		for _, password := range []string{"", "something else", "VelvetSweatshop"} {
			doc, err := extract(t, data, &gofastcore.Options{Password: password})
			if err != nil {
				t.Errorf("%+v, password %q: %v", how, password, err)
				continue
			}
			_, said := withoutDecrypted(t, doc)
			if len(said) != 1 || !strings.Contains(said[0].Message, "the password Excel uses when none is set") || !strings.Contains(doc.Text(), "protected, not secret") {
				t.Errorf("%+v, password %q: %v", how, password, said)
			}
		}
	}
}

// The fixtures encrypted by other programs: msoffcrypto-tool, which
// writes the scheme of Office 2010, and LibreOffice, which writes that of
// Office 2007.
func TestEncryptedFixtures(t *testing.T) {
	files := ooxmltest.LoadManifest(t, "encrypted.json")
	if len(files) < 3 {
		t.Fatalf("%d encrypted fixtures", len(files))
	}
	for _, f := range files {
		t.Run(f.Name, func(t *testing.T) {
			data := f.Read(t, false)
			if int64(len(data)) != f.Size {
				t.Errorf("size is %d, manifest says %d", len(data), f.Size)
			}
			doc, err := extract(t, data, &gofastcore.Options{Password: f.Password})
			if err != nil {
				t.Fatal(err)
			}
			ooxmltest.CheckDocument(t, doc)
			if doc.Source.Format != f.Format {
				t.Errorf("format %q, manifest says %q", doc.Source.Format, f.Format)
			}
			// LibreOffice saves the file again, and does not write which
			// row of a table is its header.
			ooxmltest.CompareBlocks(t, doc, expected(t, f), ooxmltest.CompareOptions{Hidden: true, NoHeaderRows: strings.Contains(f.Producer, "LibreOffice")})
			_, said := withoutDecrypted(t, doc)
			if len(said) != 1 || said[0].Code != gofastooxml.WarnDecrypted {
				t.Errorf("warnings: %v", said)
			}
			if _, err := extract(t, data, nil); !errors.Is(err, gofastcore.ErrPasswordRequired) {
				t.Errorf("no password: %v", err)
			}
			if _, err := extract(t, data, &gofastcore.Options{Password: f.Password + "!"}); !errors.Is(err, gofastcore.ErrWrongPassword) {
				t.Errorf("the wrong password: %v", err)
			}
		})
	}
}

// decryptedBytes returns what the encrypted file decrypts to, through the
// packages the library uses.
func decryptedBytes(t testing.TB, data []byte, password string) []byte {
	t.Helper()
	cf, err := cfb.Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	stream, err := cf.Open("EncryptionInfo")
	if err != nil {
		t.Fatal(err)
	}
	info, err := crypt.Parse(stream, stream.Size())
	if err != nil {
		t.Fatal(err)
	}
	key, err := info.Key(context.Background(), password, 0)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cf.Open("EncryptedPackage")
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := info.Open(encrypted, encrypted.Size(), key)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, pkg.Size())
	if _, err := pkg.ReadAt(out, 0); err != nil && err != io.EOF {
		t.Fatal(err)
	}
	return out
}

// corpusPasswords are the passwords of the encrypted files in the corpus,
// as Apache POI's tests give them. All were written by Microsoft Office.
var corpusPasswords = map[string]string{
	"poi/document/bug53475-password-is-pass.docx":     "pass",
	"poi/document/bug53475-password-is-solrcell.docx": "solrcell",
	"poi/spreadsheet/protected_passtika.xlsx":         "tika",
	"poi/spreadsheet/58616.xlsx":                      "", // Excel's own password: none is needed
	"poi/poifs/protect.xlsx":                          "",
	"poi/poifs/protected_agile.docx":                  "",
	"poi/poifs/protected_sha512.xlsx":                 "this is a test",
	"poi/poifs/60320-protected.xlsx":                  "Test001!!",
	"poi/poifs/extenxls_pwd123.xlsx":                  "pwd123",
}

// toolCannot lists the files msoffcrypto-tool does not open, with what it
// says. For these, what this library decrypts is checked by being a
// package that reads to its end.
var toolCannot = map[string]string{
	// It does not lengthen a SHA-1 hash to an AES-256 key.
	"poi/document/bug53475-password-is-pass.docx": "Invalid key size (160)",
	// The compound file states the stream longer than the sectors it
	// gives it; the package inside is whole.
	"poi/poifs/extenxls_pwd123.xlsx": "not a multiple of the block length",
	// The password's check value is a SHA-1 hash, 20 bytes stored in 32;
	// the tool compares all 32.
	"poi/poifs/protected_agile.docx": "Key verification failed",
}

// The encrypted files of the corpus, which Microsoft Office wrote, and
// the encrypted fixtures, against msoffcrypto-tool: both must decrypt a
// file to the same bytes.
func TestDecryptionAgainstMsoffcrypto(t *testing.T) {
	if _, ok := ooxmltest.PythonBin(t); !ok {
		t.Skip("the Python comparison environment is not set up")
	}
	type item struct {
		name, password string
		data           []byte
	}
	var items []item
	for _, f := range ooxmltest.LoadManifest(t, "encrypted.json") {
		items = append(items, item{f.Name, f.Password, f.Read(t, false)})
	}
	for _, f := range ooxmltest.LoadManifest(t, "corpus.json") {
		password, ok := corpusPasswords[f.Name]
		if !ok {
			continue
		}
		if data, err := os.ReadFile(f.AbsPath(t)); err == nil {
			items = append(items, item{f.Name, password, data})
		}
	}
	// And files this test encrypts itself: the other program must read
	// what the test's own writer writes, or the tests that use it prove
	// little.
	own := ooxmltest.Docx(para(run(text("Encrypted by the test.")))).Bytes()
	items = append(items,
		item{"written here, agile", "secret", ooxmltest.Encrypt(own, "secret", ooxmltest.Encryption{SpinCount: 1000})},
		item{"written here, standard", "secret", ooxmltest.Encrypt(own, "secret", ooxmltest.Encryption{Standard: true})})

	agreed, alone := 0, 0
	for _, it := range items {
		doc, err := extract(t, it.data, &gofastcore.Options{Password: it.password})
		if err != nil {
			t.Errorf("%s: %v", it.name, err)
			continue
		}
		ooxmltest.CheckDocument(t, doc)
		for _, w := range doc.Warnings {
			if w.Code == gofastooxml.WarnIntegrityMismatch {
				t.Errorf("%s: %s", it.name, w.Message)
			}
		}
		for _, p := range doc.Parts {
			if p.Has(gofastcore.WarnPartFailed) {
				t.Errorf("%s: part %d failed: %v", it.name, p.Number, p.Warnings)
			}
		}
		password := it.password
		if password == "" {
			password = crypt.DefaultPassword
		}
		sum := sha256.Sum256(decryptedBytes(t, it.data, password))
		res := ooxmltest.Python(t, "msoffcrypto-decrypt", it.data, "bin")
		if res.Error != "" {
			if why, known := toolCannot[it.name]; !known || !strings.Contains(res.Error, why) {
				t.Errorf("%s: msoffcrypto-tool: %s", it.name, res.Error)
			}
			alone++
			continue
		}
		var theirs struct {
			Password, SHA256 string
			Size             int
		}
		if err := json.Unmarshal([]byte(res.Text), &theirs); err != nil {
			t.Fatal(err)
		}
		if theirs.Password != password || theirs.SHA256 != hex.EncodeToString(sum[:]) {
			t.Errorf("%s: msoffcrypto-tool decrypts with %q to %s (%d bytes); this library with %q to %x", it.name, theirs.Password, theirs.SHA256, theirs.Size, password, sum)
		}
		agreed++
	}
	t.Logf("%d encrypted files decrypt to the same bytes as with msoffcrypto-tool; %d more that it cannot open are read", agreed, alone)
	if agreed < 5 {
		t.Errorf("only %d files were compared", agreed)
	}
}

// An encrypted file that was changed after it was encrypted: the file
// says so itself, and that is passed on.
func TestIntegrity(t *testing.T) {
	plain := ooxmltest.Docx(para(run(text("The content.")))).Bytes()
	info, encrypted := ooxmltest.EncryptStreams(plain, "p", ooxmltest.Encryption{SpinCount: 50})
	// The integrity value is replaced by another of the same length: the
	// package is as it was, and decrypts, but is not vouched for.
	at := bytes.Index(info, []byte(`encryptedHmacValue="`)) + len(`encryptedHmacValue="`)
	forged := append([]byte(nil), info...)
	copy(forged[at:], "AAAAAAAAAAAAAAAAAAAAAA")
	for name, c := range map[string]struct {
		info []byte
		want bool
	}{"as written": {info, false}, "the integrity value changed": {forged, true}} {
		data := ooxmltest.Compound(map[string][]byte{"EncryptionInfo": c.info, "EncryptedPackage": encrypted})
		doc, err := extract(t, data, &gofastcore.Options{Password: "p"})
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		_, said := withoutDecrypted(t, doc)
		mismatch := false
		for _, w := range said {
			mismatch = mismatch || w.Code == gofastooxml.WarnIntegrityMismatch && w.Severity == gofastcore.SeverityWarning
		}
		if mismatch != c.want || !strings.Contains(doc.Text(), "The content.") {
			t.Errorf("%s: warnings %v", name, said)
		}
	}
	// A byte of the package changed: the document may be damaged or not
	// readable at all, but if one is returned it carries the warning.
	for _, at := range []int{20, len(encrypted) / 2, len(encrypted) - 20} {
		changed := append([]byte(nil), encrypted...)
		changed[at] ^= 0x40
		data := ooxmltest.Compound(map[string][]byte{"EncryptionInfo": info, "EncryptedPackage": changed})
		doc, err := extract(t, data, &gofastcore.Options{Password: "p"})
		checkOutcome(t, "a changed package", doc, err)
		if doc != nil && !warned(doc, gofastooxml.WarnIntegrityMismatch) {
			t.Errorf("a byte changed at %d: no warning", at)
		}
	}
}

// What an encrypted file may cost to open is bounded, and opening it can
// be given up.
func TestEncryptedFileLimits(t *testing.T) {
	plain := ooxmltest.Docx(para(run(text("x")))).Bytes()
	data := ooxmltest.Encrypt(plain, "p", ooxmltest.Encryption{SpinCount: 5000})
	opts := &gofastcore.Options{Password: "p"}
	ctx := context.Background()
	open := func(e *gofastooxml.Extractor, data []byte, opts *gofastcore.Options) error {
		_, err := e.Extract(ctx, bytes.NewReader(data), int64(len(data)), opts)
		return err
	}
	if err := open(&gofastooxml.Extractor{MaxSpinCount: 4999}, data, opts); !errors.Is(err, gofastcore.ErrLimitExceeded) {
		t.Errorf("more rounds than allowed: %v", err)
	}
	if err := open(&gofastooxml.Extractor{MaxSpinCount: 5000}, data, opts); err != nil {
		t.Errorf("as many rounds as allowed: %v", err)
	}
	if err := open(&gofastooxml.Extractor{MaxSpinCount: -1}, data, opts); err != nil {
		t.Errorf("no limit: %v", err)
	}
	// The default allows what Office writes, ten times over, and no more:
	// a file cannot make a guess cost what it likes.
	info, encrypted := ooxmltest.EncryptStreams(plain, "p", ooxmltest.Encryption{SpinCount: 10})
	costly := ooxmltest.Compound(map[string][]byte{"EncryptionInfo": bytes.Replace(info, []byte(`spinCount="10"`), []byte(`spinCount="1000001"`), 1), "EncryptedPackage": encrypted})
	within(t, 2*time.Second, "a file that asks for over a million rounds", func() {
		if err := open(&gofastooxml.Extractor{}, costly, nil); !errors.Is(err, gofastcore.ErrLimitExceeded) {
			t.Errorf("over the default limit, no password: %v", err)
		}
	})
	if err := open(&gofastooxml.Extractor{}, data, &gofastcore.Options{Password: "p", Limits: gofastcore.Limits{MaxInputBytes: int64(len(data)) - 1}}); !errors.Is(err, gofastcore.ErrLimitExceeded) {
		t.Errorf("larger than MaxInputBytes: %v", err)
	}
	// The decompression budget counts what the package expands to, as for
	// a file that is not encrypted.
	if doc, err := extract(t, data, &gofastcore.Options{Password: "p", Limits: gofastcore.Limits{MaxDecodedBytes: 300}}); err == nil && !doc.Parts[0].Has(gofastcore.WarnLimitExceeded) && !doc.Parts[0].Has(gofastcore.WarnPartFailed) {
		t.Errorf("a decompression budget of 300 bytes: %v", doc.Parts[0].Warnings)
	}
	// Given up while the password is being tried.
	slow := ooxmltest.Compound(map[string][]byte{"EncryptionInfo": bytes.Replace(info, []byte(`spinCount="10"`), []byte(`spinCount="900000000"`), 1), "EncryptedPackage": encrypted})
	cancelled, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	within(t, 5*time.Second, "giving up on a slow password check", func() {
		e := &gofastooxml.Extractor{MaxSpinCount: -1}
		if _, err := e.Extract(cancelled, bytes.NewReader(slow), int64(len(slow)), opts); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("a deadline during the password check: %v", err)
		}
	})
}

// Encrypted files that are not what they should be: refused with one of
// the shared errors, never read as something else.
func TestEncryptedFilesThatCannotBeOpened(t *testing.T) {
	plain := ooxmltest.Docx(para(run(text("x")))).Bytes()
	info, encrypted := ooxmltest.EncryptStreams(plain, "p", ooxmltest.Encryption{SpinCount: 10})
	standard, _ := ooxmltest.EncryptStreams(plain, "p", ooxmltest.Encryption{Standard: true})
	notZip, notZipPackage := ooxmltest.EncryptStreams([]byte("this is not a zip archive, though it was encrypted like one"), "p", ooxmltest.Encryption{SpinCount: 10})
	for name, c := range map[string]struct {
		streams map[string][]byte
		want    error
	}{
		"no package":                       {map[string][]byte{"EncryptionInfo": info}, gofastcore.ErrUnsupported},
		"no encryption information":        {map[string][]byte{"EncryptedPackage": encrypted}, gofastcore.ErrUnsupported},
		"an empty package stream":          {map[string][]byte{"EncryptionInfo": info, "EncryptedPackage": {}}, gofastcore.ErrMalformed},
		"a package stream cut short":       {map[string][]byte{"EncryptionInfo": info, "EncryptedPackage": encrypted[:len(encrypted)/2]}, gofastcore.ErrMalformed},
		"encryption information cut short": {map[string][]byte{"EncryptionInfo": info[:200], "EncryptedPackage": encrypted}, gofastcore.ErrMalformed},
		"another cipher":                   {map[string][]byte{"EncryptionInfo": bytes.ReplaceAll(info, []byte(`cipherAlgorithm="AES"`), []byte(`cipherAlgorithm="RC2"`)), "EncryptedPackage": encrypted}, gofastcore.ErrUnsupported},
		// The password is right for the information, which is not this
		// package's: what it decrypts is not a package.
		"the other scheme's information": {map[string][]byte{"EncryptionInfo": standard, "EncryptedPackage": encrypted}, gofastcore.ErrMalformed},
		"what decrypts is not a package": {map[string][]byte{"EncryptionInfo": notZip, "EncryptedPackage": notZipPackage}, gofastcore.ErrMalformed},
	} {
		data := ooxmltest.Compound(c.streams)
		doc, err := extract(t, data, &gofastcore.Options{Password: "p"})
		if doc != nil || !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	// An encrypted file cut at every length, and with bytes changed
	// anywhere: an error of a known kind or a valid document, promptly.
	data := ooxmltest.Encrypt(plain, "p", ooxmltest.Encryption{SpinCount: 10})
	for n := 0; n < len(data); n += 101 {
		doc, err := extract(t, data[:n], &gofastcore.Options{Password: "p"})
		if err == nil || doc != nil {
			t.Errorf("cut at %d of %d bytes: read", n, len(data))
			continue
		}
		if !errors.Is(err, gofastcore.ErrMalformed) && !errors.Is(err, gofastcore.ErrUnsupported) {
			t.Errorf("cut at %d bytes: %v", n, err)
		}
	}
}

// An encrypted file is read through the file handle as any other is: a
// part at a time, in rows, from many goroutines at once.
func TestEncryptedFileThroughTheHandle(t *testing.T) {
	var sheets []ooxmltest.Sheet
	for i := 1; i <= 12; i++ {
		var rows strings.Builder
		for r := 1; r <= 200; r++ {
			rows.WriteString(xlRow(r, xlNum("A"+strconv.Itoa(r), strconv.Itoa(r*i)), xlStr("B"+strconv.Itoa(r), fmt.Sprintf("sheet %d row %d", i, r))))
		}
		sheets = append(sheets, ooxmltest.Sheet{Name: "S" + strconv.Itoa(i), Data: rows.String()})
	}
	plain := ooxmltest.Xlsx(sheets...).Bytes()
	want, err := extract(t, plain, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, how := range []ooxmltest.Encryption{{SpinCount: 100}, {Standard: true}} {
		data := ooxmltest.Encrypt(plain, "p", how)
		f, err := gofastooxml.OpenBytes(data, &gofastcore.Options{Password: "p", Workers: 6})
		if err != nil {
			t.Fatal(err)
		}
		if f.Format() != "xlsx" || f.NumParts() != 12 || len(f.Sheets()) != 12 || f.Source().Size != int64(len(data)) {
			t.Errorf("%+v: %s, %d parts, source %+v", how, f.Format(), f.NumParts(), f.Source())
		}
		if said := f.Warnings(); len(said) != 1 || said[0].Code != gofastooxml.WarnDecrypted {
			t.Errorf("%+v: warnings %v", how, said)
		}
		var wg sync.WaitGroup
		for g := 0; g < 6; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				n := 0
				for part, err := range f.Parts(context.Background()) {
					if err != nil || asJSON(t, part) != asJSON(t, want.Parts[n]) {
						t.Errorf("%+v: part %d differs from the unencrypted file's (%v)", how, n+1, err)
						return
					}
					n++
				}
				var bands []gofastcore.Part
				for band, err := range f.Rows(context.Background(), g+1, 50) {
					if err != nil {
						t.Error(err)
						return
					}
					bands = append(bands, band)
				}
				sameCells(t, fmt.Sprintf("sheet %d of the encrypted file in bands", g+1), cellsOf(t, bands...), cellsOf(t, want.Parts[g]))
			}()
		}
		wg.Wait()
	}
}

// A document may have an encrypted file embedded in it. The password is
// passed down; without one that opens it the file is listed, with the
// reason.
func TestEncryptedEmbeddedFile(t *testing.T) {
	inner := ooxmltest.Encrypt(ooxmltest.Xlsx(ooxmltest.Sheet{Name: "Inner", Data: xlRow(1, xlStr("A1", "inside the encrypted workbook"))}).Bytes(), "inner", ooxmltest.Encryption{SpinCount: 50})
	pkg := ooxmltest.Docx(para(run(text("See the attached workbook."))))
	pkg.Rel("word/document.xml", ooxmltest.RelPackage, "embeddings/book.xlsx")
	pkg.AddBytes("word/embeddings/book.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", inner)
	data := pkg.Bytes()
	for password, want := range map[string]string{"inner": "", "": gofastcore.WarnChildEncrypted, "outer": gofastcore.WarnChildEncrypted} {
		doc, err := extract(t, data, &gofastcore.Options{Child: childExtractor, Password: password})
		if err != nil {
			t.Fatal(err)
		}
		ooxmltest.CheckDocument(t, doc)
		if len(doc.Children) != 1 {
			t.Fatalf("password %q: %d children", password, len(doc.Children))
		}
		child := doc.Children[0]
		switch {
		case want == "" && !strings.Contains(child.Text(), "inside the encrypted workbook"):
			t.Errorf("password %q: the embedded workbook was not read: %v", password, child.Warnings)
		case want != "" && (len(child.Parts) != 0 || len(child.Warnings) != 1 || child.Warnings[0].Code != want):
			t.Errorf("password %q: %d parts, warnings %v", password, len(child.Parts), child.Warnings)
		}
	}
}

// The package inside an encrypted file is decrypted as it is read, never
// as a whole: what is held to read a small document out of a large
// encrypted file is a small part of the file.
func TestEncryptedFileIsNotHeldDecrypted(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a large file")
	}
	// Bytes that do not compress, so that the package is as large as
	// they are.
	blob := make([]byte, 32<<20)
	x := uint64(0x9E3779B97F4A7C15)
	for i := range blob {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		blob[i] = byte(x >> 32)
	}
	pkg := ooxmltest.Docx(para(run(text("The text of a document with a large attachment."))))
	pkg.AddBytes("word/media/recording.bin", "application/octet-stream", blob)
	plain := pkg.Bytes()
	blob = nil
	dir := t.TempDir()
	for name, how := range map[string]ooxmltest.Encryption{"agile": {SpinCount: 100}, "standard": {Standard: true}} {
		path := dir + "/" + name + ".docx"
		data := ooxmltest.Encrypt(plain, "p", how)
		size := len(data)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		data = nil
		var doc *gofastcore.Document
		peak := peakHeap(func() {
			var err error
			doc, err = gofastooxml.ExtractFile(context.Background(), path, &gofastcore.Options{Password: "p"})
			if err != nil {
				t.Fatal(err)
			}
		})
		t.Logf("%s: a file of %d MB read with a peak heap of %.1f MB", name, size>>20, float64(peak)/(1<<20))
		if !strings.Contains(doc.Text(), "a large attachment") || warned(doc, gofastooxml.WarnIntegrityMismatch) {
			t.Errorf("%s: %q, warnings %v", name, doc.Text(), doc.Warnings)
		}
		if peak > uint64(size)/4 {
			t.Errorf("%s: peak heap %d bytes for a file of %d", name, peak, size)
		}
	}
}
