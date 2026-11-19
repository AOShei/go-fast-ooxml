# Changelog

What changes for someone who uses the library. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). Before 1.0 a
minor version may change the API.

## Unreleased

To be released as 0.1.0, the first release, once `go-fast-core` has its
v0.1.0 tag.

### Formats

- **Word** (`.docx`, `.docm`, `.dotx`, `.dotm`): headings, paragraphs, list
  items with their computed labels, tables with merged cells, footnotes and
  endnotes, comments, tracked changes, headers and footers, text boxes,
  pictures, embedded files; spans for bold, italic, underline,
  strike-through, superscript, subscript and links.
- **Excel** (`.xlsx`, `.xlsm`, `.xltx`, `.xltm`): every sheet as a part,
  its cells as tables with what each cell displays (number formats, dates
  in ISO 8601, stored results of formulas), declared tables, merged cells,
  comments, text in shapes, pictures, embedded files. Sheets are read in
  parallel.
- **PowerPoint** (`.pptx`, `.pptm`, `.ppsx`, `.ppsm`, `.potx`, `.potm`):
  every slide as a part, with titles, body text with bullets and numbers,
  tables, pictures, speaker's notes, comments, the text of layouts and
  masters, and the place of every block on the slide. Slides are read in
  parallel.
- **Password-protected files** of all three, in both encryption schemes
  Office has used (2007, and 2010 and later), with `Options.Password`.
- **Pictures** in all three are blocks, with their place, size and
  alternative text, by default; their bytes with `ImagesData`.

### API

- `Extract`, `ExtractFile` and `Extractor`, which implements
  `gofastcore.Extractor`: the whole file as a `gofastcore.Document`.
- `Open`, `OpenBytes`, `OpenReaderAt` and `File`: a part at a time
  (`Parts`, `Part`), a sheet in bands of rows (`Rows`), embedded files
  (`Children`), without holding the document.
- The command `gofastooxml`: JSON, Markdown or text.

### Safety

- Limits on input size, decompressed bytes, the text a document may hold
  (link targets included, as often as they are written), archive members,
  cells and password-hashing rounds; no crash, hang or unbounded
  allocation on any input. See `SECURITY.md`.
- A file cut short, which has lost the directory of its archive, is read
  by its members' own headers as far as they go, and says so
  (`input.repaired`).
- Hidden text is extracted and flagged with its reason: hidden formatting,
  tiny text, text in the colour of its background, transparent text, text
  off the slide or under another shape, invisible characters. See the
  README for what each format's checks cover.
- Macros, external references and encrypted content are reported, never
  run, followed or guessed at.

### Not in this release

- Formula text, chart data, pivot tables, SmartArt text, the binary
  workbook format (`.xlsb`), and the legacy binary formats (`.doc`, `.xls`,
  `.ppt`).
- Files encrypted for a certificate or protected by rights management.
