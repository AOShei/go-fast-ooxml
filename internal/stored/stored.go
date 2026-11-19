// Package stored reads what an Office package of any of the three formats
// keeps for the file as a whole and shows on no page: the code of its VBA
// project, and its custom XML data. Nobody who opens the file sees either,
// so both are places to put text for a model. Each format gives them in its
// stored part (gofastcore.PartStored), after what is its own, flagged as
// hidden. The code is read as text and never run.
package stored

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/vba"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// Relationship types, by their endings, and the VBA project's content type.
const (
	relVBA       = "vbaProject"
	relCustomXML = "customXml"
	vbaType      = "application/vnd.ms-office.vbaProject"
)

// Source names the parts a package keeps for the file as a whole.
type Source struct {
	vba []string // VBA projects
	xml []string // custom XML data
}

// maxCustomXML bounds the custom XML parts read from one package.
const maxCustomXML = 1 << 10

// Find names the parts from the relationships of the package's main part.
// A VBA project that the main part does not name but the package declares
// is found by its content type: Office reads it by that.
func Find(pkg *opc.Package, rels []opc.Rel) Source {
	var src Source
	seen := map[string]bool{}
	for _, rel := range rels {
		if rel.External || !pkg.Has(rel.Target) || seen[rel.Target] {
			continue
		}
		switch {
		case ns.IsRel(rel.Type, relVBA):
			src.vba = append(src.vba, rel.Target)
			seen[rel.Target] = true
		case ns.IsRel(rel.Type, relCustomXML) && len(src.xml) < maxCustomXML:
			// Most hold no text (an empty list of sources, the schema
			// of a document library): those make no stored part.
			seen[rel.Target] = true
			if hasText(pkg, rel.Target) {
				src.xml = append(src.xml, rel.Target)
			}
		}
	}
	for _, name := range pkg.Names() {
		if !seen[name] && len(src.vba) < 16 && pkg.ContentType(name) == vbaType {
			src.vba = append(src.vba, name)
			seen[name] = true
		}
	}
	return src
}

// hasText reports whether a custom XML part has any element text.
func hasText(pkg *opc.Package, name string) bool {
	var s xmlscan.Scanner
	c, err := pkg.Scan(name, &s, scanOptions)
	if err != nil {
		return false
	}
	defer c.Close()
	for s.Next() != xmlscan.EOF {
		if s.Kind() == xmlscan.Text && s.Depth() > 0 && len(bytes.TrimSpace(s.Text())) > 0 {
			return true
		}
	}
	return false
}

// Empty reports whether there is nothing to read.
func (s Source) Empty() bool { return len(s.vba) == 0 && len(s.xml) == 0 }

// Macros reads the modules of the VBA projects into the part.
func (s Source) Macros(ctx context.Context, pkg *opc.Package, p *Part) {
	for _, name := range s.vba {
		if p.Full() || ctx.Err() != nil {
			return
		}
		data, err := readPart(pkg, name)
		if err != nil {
			p.note(name, err)
			continue
		}
		project, err := vba.Read(data, p.room.Left())
		if err != nil {
			p.note(name, err)
			continue
		}
		if project.Damaged {
			p.Damaged = append(p.Damaged, name)
		}
		// What the modules decompress to is charged to the package's
		// budget, as what a part decompresses to is.
		total := int64(0)
		for _, m := range project.Modules {
			total += int64(len(m.Code))
		}
		if !pkg.Charge(total) {
			p.budget = true
			return
		}
		if project.Undecoded > 0 {
			p.undecoded += project.Undecoded
			if !slices.Contains(p.codePages, project.CodePage) {
				p.codePages = append(p.codePages, project.CodePage)
			}
		}
		for _, m := range project.Modules {
			code, _ := clean.String([]byte(m.Code), clean.Verbatim)
			code = strings.TrimRight(code, "\n")
			if code == "" {
				continue
			}
			if !p.Add(gofastcore.Block{Kind: gofastcore.KindCode, Text: code, Attrs: map[string]string{
				gofastcore.AttrStored: gofastcore.StoredMacro, gofastcore.AttrName: clean.Name(m.Name), gofastcore.AttrLanguage: "vba"}}) {
				return
			}
		}
		p.cut = p.cut || project.Cut
	}
}

// XML reads the custom XML data into the part, a block for each part
// that holds text.
func (s Source) XML(ctx context.Context, pkg *opc.Package, p *Part) {
	for _, name := range s.xml {
		if p.Full() || ctx.Err() != nil {
			return
		}
		text, cut, err := customXML(pkg, name, p.room.Left())
		p.note(name, err)
		if text != "" && !p.Add(gofastcore.Block{Kind: gofastcore.KindParagraph, Text: text, Attrs: map[string]string{
			gofastcore.AttrStored: gofastcore.StoredCustomXML, gofastcore.AttrName: clean.Label(path.Base(pkg.Written(name)))}}) {
			return
		}
		p.cut = p.cut || cut
	}
}

// Part is a stored part being made.
type Part struct {
	gofastcore.Part
	// Damaged names the parts that could not be read to their end.
	Damaged []string

	room      *gofastcore.TextBudget
	cut       bool // the limit on text was reached
	budget    bool // the decompression budget ran out
	undecoded int  // bytes of macro code not decoded
	codePages []int
}

// NewPart starts the stored part, which is numbered number. What it holds
// is taken from the text budget in ctx.
func NewPart(ctx context.Context, number int) *Part {
	return &Part{Part: gofastcore.Part{Kind: gofastcore.PartStored, Number: number, Hidden: gofastcore.HiddenInvisible},
		room: gofastcore.TextBudgetFrom(ctx)}
}

// Room is the text budget the part takes from.
func (p *Part) Room() *gofastcore.TextBudget { return p.room }

// Add adds a block, taking its cost from the budget. It reports false,
// and adds nothing more, once the budget has run out.
func (p *Part) Add(b gofastcore.Block) bool {
	if p.Full() || !p.room.Take(gofastcore.BlockCost+len(b.Text)) {
		p.cut = true
		return false
	}
	p.Blocks = append(p.Blocks, b)
	return true
}

// Cut records that the limit on text cut something short.
func (p *Part) Cut() { p.cut = true }

// Full reports whether a limit has stopped the reading.
func (p *Part) Full() bool { return p.cut || p.budget }

// note records a failure to read a part.
func (p *Part) note(name string, err error) {
	switch {
	case err == nil:
	case errors.Is(err, opc.ErrBudget):
		p.budget = true
	default:
		p.Damaged = append(p.Damaged, name)
	}
}

// Finish adds the part's warnings and flags its blocks. It reports
// whether a limit cut the reading short.
func (p *Part) Finish() (limited bool) {
	if p.budget {
		p.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the decompressed size limit was reached; what the file stores is cut off there")
	}
	if p.cut {
		p.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the limit on the amount of text was reached; what the file stores is cut off there")
	}
	if p.undecoded > 0 {
		pages := make([]string, len(p.codePages))
		for i, cp := range p.codePages {
			pages[i] = strconv.Itoa(cp)
		}
		p.Warn(gofastcore.SeverityWarning, gofastcore.WarnTextUnmappedGlyphs, fmt.Sprintf(
			"%d characters of macro code in code page %s, which it does not define or which is not known here, are U+FFFD", p.undecoded, strings.Join(pages, ", ")))
	}
	if len(p.Blocks) == 0 {
		p.Warn(gofastcore.SeverityInfo, gofastcore.WarnPartNoText, "")
	}
	p.MarkHidden()
	return p.cut || p.budget
}

// maxProject bounds the size of a VBA project read.
const maxProject = 1 << 28

// readPart reads a whole part into memory.
func readPart(pkg *opc.Package, name string) ([]byte, error) {
	rc, err := pkg.Open(name)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxProject+1))
	if err == nil && len(data) > maxProject {
		err = errors.New("the VBA project is larger than " + strconv.Itoa(maxProject>>20) + " MiB")
	}
	return data, err
}

var scanOptions = &xmlscan.Options{Spaces: ns.Spaces}

// customXML gives the text a custom XML part holds: a line for each
// element with text of its own, its name, a colon and the text. Attribute
// values are not read: a part that keeps its data in them (the schema of
// a document library, say) gives little or nothing. At most about room
// bytes are given; cut says there was more.
func customXML(pkg *opc.Package, name string, room int64) (text string, cut bool, err error) {
	var s xmlscan.Scanner
	c, err := pkg.Scan(name, &s, scanOptions)
	if err != nil {
		return "", false, err
	}
	defer c.Close()
	var sb strings.Builder
	var own []byte // the text since the last tag
	for s.Next() != xmlscan.EOF {
		switch s.Kind() {
		case xmlscan.Start:
			own = own[:0]
		case xmlscan.Text:
			own = append(own, s.Text()...)
		case xmlscan.End:
			value, _ := clean.String(own, clean.Prose)
			own = own[:0]
			if value == "" {
				continue
			}
			local := clean.Name(string(s.Local()))
			if int64(sb.Len()+len(local)+len(value)+3) > room {
				return sb.String(), true, s.Err()
			}
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(local)
			sb.WriteString(": ")
			sb.WriteString(strings.ReplaceAll(value, "\n", " "))
		}
	}
	return sb.String(), false, s.Err()
}
