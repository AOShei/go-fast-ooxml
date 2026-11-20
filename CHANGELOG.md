# Changelog

What changes for someone who uses the library. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). Before 1.0 a
minor version may change the API.

## Unreleased

To be released as 0.1.0, the first release, with `go-fast-core` v0.1.0,
when the work planned before the release is done.

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
leaves out, have no reason.

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
- A last part of kind `stored`, in all three formats, for what a file
  stores and shows nowhere: the code of VBA projects, custom XML data,
  Word's document variables, Excel's defined names, the code of Excel 4
  macro sheets, the formulas of pivot caches' calculated fields, and data
  appended after the end of the zip archive (`after_end`, with
  `input.trailing_data`).
- Every code of a Word field, as a `note` block of type `field` after its
  paragraph, marked `stored` `field_code`: in place, and left out of
  rendered text unless `RenderOptions.Stored` is set.
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
  (`Parts`, `Part`), a sheet in bands of rows (`Rows`), embedded files
  (`Children`), without holding the document.
- The command `gofastooxml`: JSON, Markdown or text, with the render
  flags every go-fast command shares (`-furniture`, `-stored`,
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

- The binary workbook format
  (`.xlsb`), and the legacy binary formats (`.doc`, `.xls`, `.ppt`).
- Files encrypted for a certificate or protected by rights management.
