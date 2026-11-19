# Changelog

What changes for someone who uses the library. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). Before 1.0 a
minor version may change the API.

## Unreleased

To be released as 0.1.0, the first release, with `go-fast-core` v0.1.0,
when the work planned before the release is done.

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
  names, merged cells, comments, drop-down lists, text in shapes with its
  links, pictures, embedded files. Sheets are read in parallel.
- **PowerPoint** (`.pptx`, `.pptm`, `.ppsx`, `.ppsm`, `.potx`, `.potm`):
  every slide as a part, with titles, body text with bullets and numbers,
  tables, pictures, speaker's notes, comments, the text and pictures of
  layouts and masters, the header and footer of notes pages and handouts,
  and the place of every block on the slide. Slides are read in parallel.
- **Symbol fonts** (Symbol, Wingdings, Wingdings 2 and 3, Webdings) in all
  three, written as the characters Unicode gives their symbols.
- **Password-protected files** of all three, in both encryption schemes
  Office has used (2007, and 2010 and later), with `Options.Password`.
- **Pictures** in all three are blocks, with their place, size and
  alternative text, by default; their bytes with `ImagesData`.

### API

- `Extract`, `ExtractFile` and `Extractor`, which implements
  `gofastcore.Extractor`: the whole file as a `gofastcore.Document`.
- With `Options.Cells`, the formulas of cells (shared formulas moved to
  each cell) and the types and stored values of what they hold; with
  `Options.Spans`, cell hyperlinks; both in `Table.Cells`.
- A last part of kind `stored` for what a workbook stores and shows on no
  sheet: defined names and the code of Excel 4 macro sheets, flagged as
  hidden.
- Excel's drop-down lists as `note` blocks of type `choices` at their
  cells: the values, one per line, or the reference they are taken from.
- Charts in all three formats, both kinds, as a caption with the title
  and tables of their data with role `chart`, the chart's type, name and
  axis titles; series a filter leaves out, flagged invisible; Excel chart
  sheets; the workbook a chart embeds as an embedded file.
- SmartArt in all three formats: the text of each node as list items in
  the model's order and nesting, placed and judged for hidden text by the
  drawing Office made of it; nodes the layout draws nowhere flagged
  invisible.
- `Open`, `OpenBytes`, `OpenReaderAt` and `File`: a part at a time
  (`Parts`, `Part`), a sheet in bands of rows (`Rows`), embedded files
  (`Children`), without holding the document.
- The command `gofastooxml`: JSON, Markdown or text.

### Safety

- Limits on input size, decompressed bytes, the text a document may hold
  (link targets included, as often as they are written, and a cost for
  every block and every table cell), archive members,
  cells and password-hashing rounds; no crash, hang or unbounded
  allocation on any input. See `SECURITY.md`.
- A file cut short, which has lost the directory of its archive, is read
  by its members' own headers as far as they go, and says so
  (`input.repaired`).
- Hidden text is extracted and flagged with its reason: hidden formatting,
  tiny text, text in the colour of its background, transparent text, text
  off the slide or under another shape, invisible characters. See the
  README for what each format's checks cover.
- Contrast in tables is judged against what their table style paints:
  Excel's 60 built-in styles from the standard, the workbook's own, and
  PowerPoint's built-in styles that a file names without defining.
- A file protected by rights management is refused with a message that
  says what it is.
- Macros, external references and encrypted content are reported, never
  run, followed or guessed at.

### Not in this release

- Pivot tables, the binary workbook format
  (`.xlsb`), and the legacy binary formats (`.doc`, `.xls`, `.ppt`).
- Files encrypted for a certificate or protected by rights management.
