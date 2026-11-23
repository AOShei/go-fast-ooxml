# Changelog

What changes for someone who uses the library. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). Before 1.0 a
minor version may change the API.

## [Unreleased]

### Fixed

- **Text joined from many pieces no longer costs time with the square of
  their number.** Consecutive code paragraphs of a Word document, and the
  cells a merged cell covers in Word and PowerPoint tables, were joined by
  copying the text so far for each piece: 60,000 code paragraphs, a file
  of 166 KB, took six seconds and allocated tens of gigabytes. Each is now
  appended once.
- **The runs of a workbook's strings are in their own fonts.** A cell in a
  symbol font whose string has runs in other fonts (a check mark in
  Wingdings, then words in Arial) had all its text read as codes of the
  symbol font, and a run in a symbol font in a cell that is not was left
  as codes. Each run is now in the font it names, and one that names none
  in the cell's: shared and inline strings of `.xlsx`, and the rich
  strings of `.xlsb`.
- Two warning messages said less than is read. `input.active_content` on
  Excel 4.0 macro sheets said the macros are not read; their formulas are
  read as text into the stored part, and none is run. The
  `limit.exceeded` message on the calculated entries of a pivot cache
  named fields and items; only calculated fields are read and counted.
  Codes and severities are unchanged.
- A scanner reset for another part no longer shows the last name and
  text of the part it read before. Until its first token, and as the
  text of the first start tags, it gave the previous part's; no reader
  looks there, and the output over 6,615 corpus files is unchanged.
  Found by the go-fast-core agent in go-fast-odf's copy of the scanner.

### Changed

- **The command writes JSON a block at a time, and a table a row at a
  time,** not each part encoded whole first. The bytes are the same. A
  workbook of 64 MB of text peaks at 18 MB rather than 1.7 GB, and
  `large.docx` at 43 MB rather than 110.
- **With more than one worker, parts are handed on as soon as they and
  those before them are read,** not a batch of four parts a worker at a time:
  `File.Parts` holds what is read ahead, not a whole batch. When a limit
  is met, what is not yet handed on is read again in order, as before.
- Number formats find the fifteen digits of most numbers (whole, or with
  up to four decimals) without the general conversion; the output is the
  same, checked on fifty million numbers.
- A sheet's text is kept in pieces that start at 4 KiB and double up to
  256 KiB, rather than 256 KiB from the first.
- **A part's XML scanner window holds the part, not 64 KiB whatever its
  size.** Most parts of a document are a few kilobytes; each had a new
  64 KiB window, which was most of what reading a Word document
  allocated. On 1,411 Word documents from Common Crawl, allocation falls
  from 2.33 to 1.15 GB and CPU time by 13% (one worker); with 24
  workers, the 1,980 Word, Excel and PowerPoint files take 0.59 s rather
  than 0.66. The output is the same.

## [0.2.0] - 2026-11-23

Requires `go-fast-core` v0.1.2.

### Changed

- **Events every go-fast library can meet are raised with core's shared
  codes**, at the severity core gives them (the same as before). The code
  strings in the output change:

  | Was | Now | Severity |
  |---|---|---|
  | `ooxml.control_characters_removed` | `text.control_characters_removed` | info |
  | `ooxml.unread_content` | `input.unread_content` | warning |
  | `ooxml.part_damaged` | `input.stream_damaged` | warning |
  | `ooxml.properties_damaged` | `input.stream_damaged` | warning |
  | `ooxml.formula_no_value` | `table.formula_no_value` | info |
  | `ooxml.region_split` | `table.region_split` | info |
  | `ooxml.decrypted` | `input.decrypted` | info |

  The messages say what they said; damaged document properties now name
  them: "the document properties (docProps/core.xml) could not be read
  to their end; the properties read before the damage are kept".
  `ooxml.table_flattened` and `ooxml.integrity_mismatch` stay this
  library's own.

### Added

- **`Metadata.Encrypted` is set on a file that was decrypted**: by the
  agile or the standard scheme, with the password given, the password
  Excel uses when none is set, or a private key from `Options.Keys`. It is
  set whenever `input.decrypted` is raised, and on no other file.

### Deprecated

- `WarnControlCharsRemoved`, `WarnUnreadContent`, `WarnPartDamaged`,
  `WarnPropertiesDamaged`, `WarnFormulaNoValue`, `WarnRegionSplit` and
  `WarnDecrypted`. Each is now the shared code's value
  (`gofastcore.WarnTextControlCharactersRemoved`,
  `WarnInputUnreadContent`, `WarnInputStreamDamaged`,
  `WarnTableFormulaNoValue`, `WarnTableRegionSplit`,
  `WarnInputDecrypted`), so `w.Code == gofastooxml.WarnPartDamaged` still
  matches; use the core names. The deprecated names will be removed in a
  later release.

## [0.1.1] - 2026-11-22

Requires `go-fast-core` v0.1.1.

### Fixed

- **What a fixed bound leaves out is said.** Some fifty fixed bounds
  (a slide table's 16,384 columns, a 64 KiB field code, 16,384 document
  variables or defined names, a 64-byte list label, a chart's 1,024
  series, SmartArt's 65,536 nodes, a sheet's comments, links, drop-down
  lists and drawing parts, pivot caches, VBA modules) cut content with no
  warning. Each now says what it left out with `limit.exceeded`, severity
  error, once per part with a count; what belongs to no part is said on
  the document. `SECURITY.md` lists them.
- **Fields nested more than 64 deep** no longer unbalance the fields
  around them: their ends had closed the outer fields early, so later
  text could be read as a field's code, or a link given to the wrong
  text.
- A field code over 64 KiB is cut at 64 KiB wherever it comes from; one
  that was a single long piece was kept whole, and the nested code that
  took an outer one past the bound was dropped.
- A VBA project larger than 256 MiB is `limit.exceeded`, not
  `ooxml.part_damaged`. An encrypted file whose container has more than
  65,536 entries is refused with `ErrLimitExceeded`, not `ErrMalformed`.
- Data after the end of the archive past the first 1 MiB is said with
  `limit.exceeded` as well as in `input.trailing_data`.
- **Names and labels cut at their bound are said** where they reach the
  output: authors, names of sheets, shapes, tables, pictures, embedded
  files, VBA modules and defined names, a cell's stored value. A
  paragraph identifier longer than 32 bytes, which was dropped, is
  said too. Each cut gives how many bytes it left out. The bound on a
  name rises from 512 bytes to 1,024, so that a name of 255 characters,
  the most Excel allows, is whole in any script.

### Changed

- **Cut document properties are `limit.exceeded`**, severity error, in
  place of `ooxml.property_truncated` (severity warning): a value cut at
  64 KiB, custom properties past 256, and a custom property's name, now
  cut at 1,024 bytes (it was 256). `WarnPropertyTruncated` is kept,
  deprecated, and no longer raised.

- **`ooxml.part_damaged` is one warning**, with how many parts and the
  first eight, in place of one per part: "2 parts could not be read to
  their end (a, b)". Drawings in a Word document that name a chart or
  diagram the document does not have are likewise one warning, where
  there was one per drawing.
- A chart cut at about two million points is `limit.exceeded` at
  severity error, once per part, where it was severity warning once per
  chart.
- The document's warnings are added through `Document.Warn`, which keeps
  at most 1,000 (core v0.1.1).

## [0.1.0] - 2026-11-21

The first release. Requires `go-fast-core` v0.1.0.

**For code built against `main` before this release:** the series no longer
judges whether text is visible. It reports what a file declares, in
`Block.Display` (`hidden`, `no_ink`; JSON `display`, schema 0.2), in place
of `Block.Hidden` and its reasons. Removed: the reasons `tiny`,
`low_contrast`, `off_page`, `occluded`, `clipped`, `transparent` (now
`no_ink`) and `invisible_chars`; the warnings `text.hidden`,
`ooxml.hidden_unchecked` and `ooxml.unshown_text_long`;
`File.HiddenChecked`, `Options.DeepHidden` and the command's
`-deep-hidden`; the command's `-exclude-hidden` is `-exclude-display`.
Invisible characters are kept in the text with no reason. Word field codes
are all kept, marked `stored`, where only some were kept and rendered; a
header or footer part no section names, and a SmartArt node the drawing
leaves out, have no reason. A formula whose result is empty text has a
result: `ooxml.formula_no_value` no longer counts it.

### Formats

- **Word** (`.docx`, `.docm`, `.dotx`, `.dotm`): headings, paragraphs, list
  items with their computed labels, tables with merged cells, footnotes and
  endnotes numbered as the document's settings and sections say, comments,
  tracked changes, headers and footers, text boxes, pictures, embedded
  files; spans for bold, italic, underline, strike-through, superscript,
  subscript and links.
- **Excel** (`.xlsx`, `.xlsm`, `.xltx`, `.xltm`): every sheet as a part,
  its cells as tables with what each cell displays (number formats, dates
  in ISO 8601, stored results of formulas), declared tables with their
  names, pivot tables, merged cells, comments, drop-down lists, text in shapes with its
  links, pictures, embedded files. Sheets are read in parallel.
- **Excel binary workbooks** (`.xlsb`): read into what the same workbook
  as `.xlsx` gives, with the same formats, tables, display reasons and
  limits, and a band of rows at a time. Formulas, which this format
  stores compiled, are turned back into the text Excel shows: those of
  cells, defined names, drop-down lists, macro sheets and the calculated
  fields of pivot caches. That includes references to a table's columns
  by name and to other workbooks, and calls of add-in functions and of
  the functions and commands of macro sheets. One that cannot be read
  back (through a DDE or OLE link, or to a table or name the workbook
  does not have) is left out and counted in `ooxml.unread_content`.
  Pivot tables have their names and roles, and caches whose source is
  gone are stored with their records, as in `.xlsx`.
- **PowerPoint** (`.pptx`, `.pptm`, `.ppsx`, `.ppsm`, `.potx`, `.potm`):
  every slide as a part, with titles, body text with bullets and numbers,
  tables, pictures, speaker's notes, comments, the text and pictures of
  layouts and masters, the header and footer of notes pages and handouts,
  and the place of every block on the slide. Slides are read in parallel.
- **Symbol fonts** (Symbol, Wingdings, Wingdings 2 and 3, Webdings) in all
  three, written as the characters Unicode gives their symbols.
- **Password-protected files** of all three, in both encryption schemes
  Office has used (2007, and 2010 and later), with `Options.Password`.
- **Files encrypted for a certificate**, with the private key in
  `Options.Keys` (RSA, any `crypto.Decrypter`), or `-key` in the command.
  Built from the specification and an encryptor written for the tests: no
  such file made by Office was available.
- **Pictures** in all three are blocks, with their place, size and
  alternative text, by default; their bytes with `ImagesData`.

### API

- `Extract`, `ExtractFile` and `Extractor`, which implements
  `gofastcore.Extractor`: the whole file as a `gofastcore.Document`.
- With `Options.Cells`, the formulas of cells (shared formulas moved to
  each cell) and the types and stored values of what they hold; with
  `Options.Spans`, cell hyperlinks; both in `Table.Cells`.
- A last part of kind `stored`, in all three formats, for what a file
  stores and shows nowhere: the code of VBA projects, custom XML data,
  Word's document variables, Excel's defined names, the code of Excel 4
  macro sheets, the formulas of pivot caches' calculated fields, and data
  appended after the end of the zip archive (`after_end`, with
  `input.trailing_data`).
- Every code of a Word field, as a `note` block of type `field` after its
  paragraph, marked `stored` `field_code`: in place, and left out of
  rendered text unless `RenderOptions.Stored` is set.
- Word's Horizontal Line as a `thematic_break` block, in the body. A
  paragraph that only has a border is not one, and section, page and
  column breaks give no block.
- Equations in Word and PowerPoint written in UnicodeMath, with their
  structure; a Word display equation as a `formula` block.
- Excel's drop-down lists as `note` blocks of type `choices` at their
  cells: the values, one per line, or the reference they are taken from.
- Charts in all three formats, both kinds, as a caption with the title
  and tables of their data with role `chart`, the chart's type, name and
  axis titles; series a filter leaves out, with the reason `hidden`; Excel chart
  sheets; the workbook a chart embeds as an embedded file.
- Pivot tables, as one table with role `pivot` and the pivot table's name;
  the records of pivot caches whose source is not in the workbook, in the
  stored part.
- SmartArt in all three formats: the text of each node as list items in
  the model's order and nesting, placed by the drawing Office made of it.
- `Open`, `OpenBytes`, `OpenReaderAt` and `File`: a part at a time
  (`Parts`, `Part`), a sheet in bands of rows (`Rows`, about 1.6 times
  as fast with more than one worker, which decompresses the sheet ahead of
  the reading), embedded files
  (`Children`), without holding the document. `Children` after `Parts`
  has what the parts left of the text the document may hold, as in
  `Document`.
- With more than one worker, a Word body or a sheet of 1 MiB or more is
  decompressed ahead of its reading, as `Rows` does: 1.16 to 1.26 times as
  fast for such a file, with the same document.
- The command `gofastooxml`: JSON, written a part at a time as the parts
  are read, Markdown or text, with the render
  flags every go-fast command shares (`-furniture`, `-stored[=kinds]`,
  `-part-markers`, `-exclude-display`, `-children`).

### Safety

- Limits on input size, decompressed bytes, the text a document may hold
  (link targets included, as often as they are written, and a cost for
  every block and every table cell), archive members,
  cells and password-hashing rounds; no crash, hang or unbounded
  allocation on any input. See `SECURITY.md`.
- A file cut short, which has lost the directory of its archive, is read
  by its members' own headers as far as they go, and says so
  (`input.repaired`).
- Everything a file holds is extracted. Where the file declares text not
  shown or drawn with no ink, the block says so (`Display`: `hidden`,
  `no_ink`): hidden formatting; hidden sheets, rows, columns, slides and
  shapes; headers and footers the document's settings switch off; a
  number format that shows nothing; text with no fill or a fill of no
  opacity. Nothing is judged from appearance (colour, size, position, what
  covers what), and a block with no reason says nothing about whether a
  reader can see it. See the README.
- A picture that fills a shape in Word is a picture block.
- A file protected by rights management is refused with a message that
  says what it is.
- Macros, external references and encrypted content are reported, never
  run, followed or guessed at.

### Not in this release

- The legacy binary formats (`.doc`, `.xls`, `.ppt`).
- Files protected by rights management.

[Unreleased]: https://github.com/AOShei/go-fast-ooxml/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/AOShei/go-fast-ooxml/releases/tag/v0.2.0
[0.1.1]: https://github.com/AOShei/go-fast-ooxml/releases/tag/v0.1.1
[0.1.0]: https://github.com/AOShei/go-fast-ooxml/releases/tag/v0.1.0
