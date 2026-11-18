// Package gofastooxml extracts Word, Excel and PowerPoint files (Office Open
// XML: .docx, .xlsx, .pptx and their macro and template variants) into the
// shared go-fast document model, gofastcore.Document.
//
// It uses the standard library and go-fast-core only.
//
// The package is under construction. It identifies a file, enforces the
// limits, and reads the document properties; the content of the document
// is not extracted yet, and Extract says so with a part.failed warning.
package gofastooxml
