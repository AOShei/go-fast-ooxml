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
// Extract and ExtractFile return a whole document. Open returns a File,
// which reads a part when it is asked for, so that a caller which handles
// one part and lets it go holds little at a time however large the file
// is; File.Rows does the same within a sheet too long to hold. The
// command is in cmd/gofastooxml.
//
// The package is under construction and nothing is released:
// password-protected files are still to come.
package gofastooxml
