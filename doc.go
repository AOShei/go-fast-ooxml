// Package gofastooxml extracts Word, Excel and PowerPoint files (Office Open
// XML: .docx, .xlsx, .pptx and their macro and template variants) into the
// shared go-fast document model, gofastcore.Document.
//
// It uses the standard library and go-fast-core only.
//
// A Word document is one part; a workbook has a part for each sheet and a
// presentation one for each slide. Headings, list items with their labels,
// tables with merged cells, pictures, notes and comments are read as what
// the file declares them to be. Text that a reader of the document cannot
// see is extracted like any other and flagged with the reason; see
// gofastcore.Hidden and the README.
//
// The package is under construction and nothing is released: the native
// interface, the command and password-protected files are still to come.
package gofastooxml
