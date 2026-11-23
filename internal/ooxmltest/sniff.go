package ooxmltest

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"strings"
)

// mainTypes maps the content type of a package's main part to the format.
var mainTypes = map[string]string{
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml":   "docx",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.template.main+xml":   "docx",
	"application/vnd.ms-word.document.macroEnabled.main+xml":                             "docx",
	"application/vnd.ms-word.template.macroEnabledTemplate.main+xml":                     "docx",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml":         "xlsx",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.template.main+xml":      "xlsx",
	"application/vnd.ms-excel.sheet.macroEnabled.main+xml":                               "xlsx",
	"application/vnd.ms-excel.template.macroEnabled.main+xml":                            "xlsx",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml": "pptx",
	"application/vnd.openxmlformats-officedocument.presentationml.slideshow.main+xml":    "pptx",
	"application/vnd.openxmlformats-officedocument.presentationml.template.main+xml":     "pptx",
	"application/vnd.ms-powerpoint.presentation.macroEnabled.main+xml":                   "pptx",
	"application/vnd.ms-powerpoint.slideshow.macroEnabled.main+xml":                      "pptx",
	"application/vnd.ms-powerpoint.template.macroEnabled.main+xml":                       "pptx",
}

// Errors returned by Sniff.
var (
	ErrNotZip     = errors.New("ooxmltest: not a zip archive")
	ErrNotPackage = errors.New("ooxmltest: zip archive without readable content types")
	ErrNotOffice  = errors.New("ooxmltest: package is not a Word, Excel or PowerPoint file")
)

// Sniff reports whether data is a Word, Excel or PowerPoint package, from
// the content type of its main part. It is the harness's own reading,
// independent of the library's package reader, used to check the
// manifests.
func Sniff(data []byte) (format string, err error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", ErrNotZip
	}
	for _, f := range zr.File {
		if f.Name != "[Content_Types].xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", ErrNotPackage
		}
		// The limit keeps a hostile archive from expanding in memory.
		types, err := io.ReadAll(io.LimitReader(rc, 1<<20))
		rc.Close()
		if err != nil {
			return "", ErrNotPackage
		}
		// A damaged package can declare several main parts. The first one
		// declared wins, so that the answer does not depend on map order.
		first := len(types)
		for ct, f := range mainTypes {
			if i := strings.Index(string(types), `"`+ct+`"`); i >= 0 && i < first {
				first, format = i, f
			}
		}
		if format == "" {
			return "", ErrNotOffice
		}
		return format, nil
	}
	return "", ErrNotPackage
}
