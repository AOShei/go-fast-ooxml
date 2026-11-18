# go-fast-ooxml

Extracts Word, Excel and PowerPoint files (`.docx`, `.xlsx`, `.pptx` and their
macro and template variants) into the shared go-fast document model,
[`gofastcore.Document`](https://github.com/AOShei/go-fast-core). Standard
library and go-fast modules only.

**Status: under construction, nothing released.**

| Format | State |
|---|---|
| Word | Text and structure are extracted: headings, list items with their computed labels, tables with merged cells, footnotes, comments, tracked changes, headers and footers, text boxes, pictures, embedded files. Hidden text is flagged (see below). With `Options.Spans`, bold, italic, underline, strike-through, superscript, subscript and links are reported as spans |
| Excel | Every sheet is a part; its cells are tables, with what each cell displays: numbers through their number formats, dates in ISO 8601, formulas as their stored results. Declared tables, merged cells, comments, text in shapes, pictures and embedded files are read. Hidden text is flagged (see below). Sheets are read in parallel |
| PowerPoint | The file is identified and its properties read. Content is not extracted yet, and `Extract` says so: the one part it returns carries a `part.failed` warning |

## Excel workbooks

**Cells are given as displayed.** A cell holding 1234.5 with the format
`#,##0.00` is `1,234.50`; 0.125 as `0.0%` is `12.5%`. The number-format
engine (`internal/numfmt`) handles sections for negative numbers, zero and
text, conditions, thousands and scaling, percent, scientific and
engineering notation, fractions, durations, currency and literal text. It
agrees with LibreOffice on all 6,048 pairs of a table of formats and
values, and on the cells of 446 real workbooks (see `bench/README.md`).

Four things are not as displayed, on purpose:

- **Dates and times are in ISO 8601** whatever the format: `2026-03-04`,
  `2026-03-04T14:30:00`, `14:30:00`. `03/04/2026` is two different days
  depending on where the file was made. What is written follows the value,
  so nothing it holds is lost: a cell formatted to show only the date still
  gives its time of day if it has one. Durations (`[h]:mm`) are not dates
  and are given as displayed.
- **General numbers keep all fifteen digits** a spreadsheet stores. Excel
  shows a number of twelve digits or more in scientific notation; an
  account number should not become `1.23457E+11`.
- **Padding is one space.** Formats can ask for blank space as wide as some
  character, or for a character repeated to fill the cell. Neither is
  reproduced.
- **A value its format displays as nothing is written out**, and flagged
  as hidden, except zero: formats blank zeros as a matter of course.

A formula gives its stored result. Formulas are not evaluated, and one
stored without a result is an empty cell, reported as
`ooxml.formula_no_value`.

**Tables.** A table the workbook declares is one table block, with its
header row. The other cells are grouped into blocks separated by empty rows
and columns; those boundaries are a reading of the layout, so such tables
have `origin: inferred`. Every block has its place on the sheet in
`loc.range`. An area whose cells are so scattered that the rectangle around
them is nearly all empty is written as one table per run of adjacent cells,
with `ooxml.region_split`. A table of more than about a million cells is
written as several, each a run of its rows.

**Limits.** Besides the limits of `gofastcore.Options`, `Extractor.MaxCells`
bounds the cells kept from a workbook (20 million by default), and the text
of the cells is bounded by `Limits.MaxDecodedBytes`, counted wherever it is
used: a long string that a million cells refer to is small in the file and
large in the result. A sheet that reaches a limit is cut off there, with
`limit.exceeded`.

**Not read:** formula text, charts, pivot table definitions (what a pivot
table shows is in the cells and is read), data validation lists, and the
macros of macro sheets and VBA projects, which are reported as
`input.active_content`.

## Hidden text

Text that a person reading the document cannot see is extracted like any other
text and flagged, never dropped. A block's `Hidden` field says why. Where only
part of a paragraph is hidden the reason is on a span over that part, and in a
table on the cells concerned. Every part that has flagged text carries a
`text.hidden` warning, and `RenderOptions.ExcludeHidden` leaves the flagged
text out of rendered output.

`Document.HiddenChecked` lists the reasons that are checked completely for
the format; for both Word and Excel they are `invisible`, `tiny`,
`low_contrast` and `invisible_chars`.

What is flagged in a Word document:

| Reason | What is flagged |
|---|---|
| `invisible` | Text formatted as hidden, on the run or through its character style, its paragraph's style or the document defaults. A header or footer that no page shows: one no section names, a first-page one where no section has a different first page, an even-page one where the document does not have different odd and even pages. A footnote, text box or picture anchored in hidden text |
| `tiny` | A font size under 2 points, resolved through the same layers. Characters narrowed, or text fitted into a width, so that it is under 2 points |
| `low_contrast` | A text colour set explicitly (on the run, through a style, or as a Word 2010 text fill) that cannot be told from what is behind it: the run's shading or highlight, the paragraph's shading, the table cell's shading whether set on the cell or given by the table's style, the fill of the shape or text box the text is in, the page colour where Word displays it. Theme colours are resolved. Text in the "automatic" colour is never flagged: Word draws it to contrast with its background |
| `transparent` | A Word 2010 text fill that is fully transparent, or no fill |
| `clipped` | Text in a text box of fixed size under 2 points in either direction, or in a table row of an exact height under 2 points |
| `invisible_chars` | Characters with no visible form, found by the shared model itself |

In Word, `transparent` and `clipped` are flagged where found but only the
forms above are looked for.

What is flagged in an Excel workbook:

| Reason | What is flagged |
|---|---|
| `invisible` | A hidden or very hidden sheet: the part and all its blocks. Hidden rows and columns, including those of no height or width. A value its number format displays as nothing (`;;;`), other than zero. Text in a shape marked hidden |
| `tiny` | A cell or a run of shape text in a font under 2 points |
| `low_contrast` | A cell whose font colour, named in its style, cannot be told from its fill, or from white where it has none. Shape text in the colour of its shape. Theme colours, tints and the old colour palette are resolved |
| `invisible_chars` | Characters with no visible form, found by the shared model itself |

In a table the flags are on regions of cells (`table.hidden`); cells side by
side or one under another with the same reasons are one region, so a hidden
column is one entry however long it is.

A flag states a fact, so where the facts cannot be established nothing is
flagged. What this means for a reviewer:

- **Backgrounds that are not one known colour.** Against a gradient, a
  picture, a striped pattern, or a shape colour that the theme lightens or
  darkens, text colour is not judged.
- **Conditional formatting in Excel.** It changes a cell's colours by its
  value, which is not worked out. Text in a range it applies to is not
  judged against its fill.
- **Table styles in Word.** The shading a table's style gives cells is used; the text
  colour it gives them is not. Whether a cell is in the last row or column is
  used only where the file states it.
- **Position.** This library does no page layout. Text covered by a floating
  shape or picture, or pushed off the page, is not detected.
- **Fonts and spacing.** A font whose glyphs are blank, or spacing that piles
  characters on each other, is not detected.
- **Content never shown on a page but extracted by design**: document
  properties, alternative text of pictures, comments. Each has its own place
  in the model (metadata, `image` blocks, `note` blocks), so a consumer can
  treat it apart.
- **Content not extracted at all**, which therefore cannot reach a model
  through this library: field codes, custom XML data, document variables;
  in Excel, formula text, defined names, data validation lists and pivot
  caches.

The contrast test is the shared model's (`gofastcore.LowContrast`): bright
yellow text on a white page is under its threshold and is flagged.

## Layout

| Path | Content |
|---|---|
| `extract.go` | The public entry points: `Extract`, `ExtractFile`, `Extractor` |
| `internal/xmlscan` | Pull tokenizer for XML: no allocation per token, bounded memory, namespace-aware, refuses DTDs |
| `internal/opc` | The package container: zip archive, content types, relationships, decompression budget |
| `internal/wml` | The Word reader: styles, numbering, body, tables, notes, character formatting and what lies behind text |
| `internal/sml` | The Excel reader: workbook, shared strings, cell styles, the streaming sheet reader, finding tables on a sheet, comments, shapes |
| `internal/numfmt` | Number formats: what a cell displays for the value it holds |
| `internal/theme`, `internal/media`, `internal/hidden`, `internal/warn` | Shared by the readers: theme colours, pictures and embedded files, settling which text is hidden, warning codes |
| `internal/props`, `internal/clean`, `internal/ns` | Document properties; text conventions of the shared model; namespace and relationship names |
| `internal/ooxmltest` | Test harness: in-memory package builder, scorers, oracle wrappers, manifest loader, the checks every extracted document must pass |
| `testdata/fixtures`, `testdata/expect`, `testdata/fixtures.json` | Small generated files with exactly known content, the expected result of each in the shared model's JSON form, and their manifest |
| `testdata/gen` | The fixture generator and the adapters for the comparison tools |
| `testdata/numfmt` | What LibreOffice displays for 6,048 pairs of a number format and a value: the table the number-format engine is held to |
| `testdata/corpus.json`, `testdata/fetch.py` | Manifest of 783 real files from Apache POI's test suite and data.gov.uk, and the script that downloads them. The files are not in the repository |
| `bench` | Baseline measurements of the tools in use today; see `bench/README.md` |

## Running the tests

```sh
go test ./...
```

Tests that need LibreOffice, pandoc, the Python comparison tools or the
downloaded corpus skip themselves when those are absent. To run everything,
see `bench/README.md` and `python3 testdata/fetch.py`.

## Licence

MIT. The corpus files keep their own licences, recorded per file in
`testdata/corpus.json`.
