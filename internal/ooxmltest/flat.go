package ooxmltest

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"io"
	"path"
	"strings"
	"unicode/utf8"
)

// FlatOPC writes a package as Word and PowerPoint save one as XML (Flat
// OPC): each member a pkg:part with its content type, its XML inline in
// pkg:xmlData, or its bytes in base64 in pkg:binaryData. A member whose
// XML cannot be put inline as it is (not UTF-8, or with a document type
// declaration) is written in base64, which gives the same bytes. Members
// are written in archive order; of two with one name, the first, as the
// library reads them. ok is false for data that is not a zip archive
// with a content-types table.
func FlatOPC(data []byte) (flat []byte, ok bool) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, false
	}
	read := func(f *zip.File) ([]byte, bool) {
		rc, err := f.Open()
		if err != nil {
			return nil, false
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		return b, err == nil
	}
	defaults, overrides := map[string]string{}, map[string]string{}
	found := false
	for _, f := range zr.File {
		if !strings.EqualFold(strings.TrimLeft(f.Name, "/"), "[Content_Types].xml") {
			continue
		}
		b, ok := read(f)
		if !ok {
			return nil, false
		}
		var types struct {
			Default []struct {
				Extension   string `xml:",attr"`
				ContentType string `xml:",attr"`
			}
			Override []struct {
				PartName    string `xml:",attr"`
				ContentType string `xml:",attr"`
			}
		}
		if xml.Unmarshal(b, &types) != nil {
			return nil, false
		}
		for _, d := range types.Default {
			defaults[strings.ToLower(d.Extension)] = d.ContentType
		}
		for _, o := range types.Override {
			overrides[strings.ToLower(strings.TrimLeft(o.PartName, "/"))] = o.ContentType
		}
		found = true
		break
	}
	if !found {
		return nil, false
	}
	var out bytes.Buffer
	out.WriteString(`<?xml version="1.0" standalone="yes"?>` + "\n")
	out.WriteString(`<pkg:package xmlns:pkg="http://schemas.microsoft.com/office/2006/xmlPackage">`)
	seen := map[string]bool{}
	for _, f := range zr.File {
		name := strings.TrimLeft(strings.ReplaceAll(f.Name, "\\", "/"), "/")
		key := strings.ToLower(name)
		if strings.HasSuffix(f.Name, "/") || key == "[content_types].xml" || seen[key] {
			continue
		}
		seen[key] = true
		b, ok := read(f)
		if !ok {
			return nil, false
		}
		ct, has := overrides[key]
		if !has {
			ct = defaults[strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))]
		}
		out.WriteString("\n<pkg:part pkg:name=\"/" + attr(name) + "\" pkg:contentType=\"" + attr(ct) + "\">")
		if inline, ok := inlineXML(b); ok {
			out.WriteString("<pkg:xmlData>")
			out.Write(inline)
			out.WriteString("</pkg:xmlData>")
		} else {
			out.WriteString("<pkg:binaryData>")
			enc := base64.StdEncoding.EncodeToString(b)
			for len(enc) > 76 {
				out.WriteString(enc[:76])
				out.WriteString("\r\n")
				enc = enc[76:]
			}
			out.WriteString(enc)
			out.WriteString("</pkg:binaryData>")
		}
		out.WriteString("</pkg:part>")
	}
	out.WriteString("\n</pkg:package>\n")
	return out.Bytes(), true
}

func attr(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return strings.ReplaceAll(b.String(), `"`, "&#34;")
}

// inlineXML returns a part's XML as it goes inside pkg:xmlData, without
// its byte order mark and declaration, if it can go there: UTF-8, with
// no document type declaration, and one well-formed element.
func inlineXML(b []byte) ([]byte, bool) {
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	if !utf8.Valid(b) {
		return nil, false
	}
	rest := bytes.TrimLeft(b, " \t\r\n")
	if bytes.HasPrefix(rest, []byte("<?xml")) {
		end := bytes.Index(rest, []byte("?>"))
		if end < 0 {
			return nil, false
		}
		decl := rest[:end]
		if i := bytes.Index(decl, []byte("encoding")); i >= 0 {
			enc := strings.ToLower(strings.Trim(strings.TrimLeft(string(decl[i+8:]), " ="), `"' `))
			if !strings.HasPrefix(enc, "utf-8") && !strings.HasPrefix(enc, "utf8") {
				return nil, false
			}
		}
		rest = rest[end+2:]
	}
	if bytes.Contains(rest, []byte("<!DOCTYPE")) || bytes.Contains(rest, []byte("<?xml ")) {
		return nil, false
	}
	// One element, well formed, and nothing but comments and white space
	// around it.
	d := xml.NewDecoder(bytes.NewReader(rest))
	d.Strict = true
	depth, roots := 0, 0
	for {
		tok, err := d.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, false
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
			}
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(t)) > 0 {
				return nil, false
			}
		case xml.ProcInst, xml.Directive:
			if depth == 0 {
				return nil, false
			}
		}
	}
	if roots != 1 || depth != 0 {
		return nil, false
	}
	return rest, true
}
