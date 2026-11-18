# go-fast-ooxml

Extracts Word, Excel and PowerPoint files (`.docx`, `.xlsx`, `.pptx` and their
macro and template variants) into the shared go-fast document model,
[`gofastcore.Document`](https://github.com/AOShei/go-fast-core). Standard
library and go-fast modules only.

**Status: under construction, nothing released.**

| Format | State |
|---|---|
| Word | Text and structure are extracted: headings, list items with their computed labels, tables with merged cells, footnotes, comments, tracked changes, headers and footers, text boxes, pictures, embedded files. Hidden text is flagged (see below). With `Options.Spans`, bold, italic, underline, strike-through, superscript, subscript and links are reported as spans |
| Excel, PowerPoint | The file is identified and its properties read. Content is not extracted yet, and `Extract` says so: the one part it returns carries a `part.failed` warning |

## Hidden text

Text that a person reading the document cannot see is extracted like any other
text and flagged, never dropped. A block's `Hidden` field says why. Where only
part of a paragraph is hidden the reason is on a span over that part, and in a
table on the cells concerned. Every part that has flagged text carries a
`text.hidden` warning, and `RenderOptions.ExcludeHidden` leaves the flagged
text out of rendered output.

What is flagged in a Word document:

| Reason | What is flagged |
|---|---|
| `invisible` | Text formatted as hidden, on the run or through its character style, its paragraph's style or the document defaults. A header or footer that no page shows: one no section names, a first-page one where no section has a different first page, an even-page one where the document does not have different odd and even pages. A footnote, text box or picture anchored in hidden text |
| `tiny` | A font size under 2 points, resolved through the same layers. Characters narrowed, or text fitted into a width, so that it is under 2 points |
| `low_contrast` | A text colour set explicitly (on the run, through a style, or as a Word 2010 text fill) that cannot be told from what is behind it: the run's shading or highlight, the paragraph's shading, the table cell's shading whether set on the cell or given by the table's style, the fill of the shape or text box the text is in, the page colour where Word displays it. Theme colours are resolved. Text in the "automatic" colour is never flagged: Word draws it to contrast with its background |
| `transparent` | A Word 2010 text fill that is fully transparent, or no fill |
| `clipped` | Text in a text box of fixed size under 2 points in either direction, or in a table row of an exact height under 2 points |
| `invisible_chars` | Characters with no visible form, found by the shared model itself |

`Document.HiddenChecked` lists the reasons that are checked completely. For
Word they are `invisible`, `tiny`, `low_contrast` and `invisible_chars`.
`transparent` and `clipped` are flagged where found but only the forms above
are looked for.

A flag states a fact, so where the facts cannot be established nothing is
flagged. What this means for a reviewer:

- **Backgrounds that are not one known colour.** Against a gradient, a
  picture, a striped pattern, or a shape colour that the theme lightens or
  darkens, text colour is not judged.
- **Table styles.** The shading a table's style gives cells is used; the text
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
  through this library: field codes, custom XML data, document variables.

The contrast test is the shared model's (`gofastcore.LowContrast`): bright
yellow text on a white page is under its threshold and is flagged.

## Layout

| Path | Content |
|---|---|
| `extract.go` | The public entry points: `Extract`, `ExtractFile`, `Extractor` |
| `internal/xmlscan` | Pull tokenizer for XML: no allocation per token, bounded memory, namespace-aware, refuses DTDs |
| `internal/opc` | The package container: zip archive, content types, relationships, decompression budget |
| `internal/wml` | The Word reader: styles, numbering, body, tables, notes, character formatting and what lies behind text |
| `internal/props`, `internal/clean`, `internal/ns` | Document properties; text conventions of the shared model; namespace and relationship names |
| `internal/ooxmltest` | Test harness: in-memory package builder, scorers, oracle wrappers, manifest loader, the checks every extracted document must pass |
| `testdata/fixtures`, `testdata/expect`, `testdata/fixtures.json` | Small generated files with exactly known content, the expected result of each in the shared model's JSON form, and their manifest |
| `testdata/gen` | The fixture generator and the adapters for the comparison tools |
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
