// Package gofastooxml extracts Word, Excel and PowerPoint files (Office Open
// XML: .docx, .xlsx, .pptx and their macro and template variants, and
// Excel's binary workbooks, .xlsb) into the shared go-fast document model,
// gofastcore.Document.
//
// It uses the standard library and go-fast-core only.
//
// A Word document is one part; a workbook has a part for each sheet and a
// presentation one for each slide. What a file stores and shows nowhere
// (the code of a VBA project, defined names, custom XML) is in one more
// part, last, of kind gofastcore.PartStored. Headings, list items with their labels,
// tables with merged cells, pictures, notes and comments are read as what
// the file declares them to be. Everything the file holds is extracted;
// where the file declares text not shown (hidden formatting, a hidden
// sheet, row, column, slide or shape) or drawn with no ink, the block says
// so (gofastcore.Display). Nothing is judged from appearance; see the
// README.
//
// Extract and ExtractFile return a whole document. Open returns a File,
// which reads a part when it is asked for, so that a caller which handles
// one part and lets it go holds little at a time however large the file
// is; File.Rows does the same within a sheet too long to hold. The
// command is in cmd/gofastooxml.
//
// A file encrypted with a password is opened with Options.Password, and
// one whose key is also encrypted for a certificate with the certificate's
// private key in Options.Keys; see the README for what is and is not
// read.
//
// Files are taken to come from people who are not trusted. Nothing in a
// file is run or followed, every cost a file can impose has a limit
// (Options.Limits and the fields of Extractor), and every reader stops
// when its context is cancelled. A file that cannot be read gives one of
// the shared errors of gofastcore; one that is damaged in part gives what
// could be read, with warnings. SECURITY.md says what is promised.
package gofastooxml
