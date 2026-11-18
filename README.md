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
| PowerPoint | Every slide is a part, with the size of the slide. Its title is a heading and the slide's name; body text is list items with their bullets or numbers, or paragraphs; tables keep their merged cells; pictures, speaker's notes and comments are read, and so is the text the layout and master put on every slide. Every block has its place on the slide. Hidden text is flagged (see below). Slides are read in parallel |

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

## PowerPoint presentations

**One part per slide**, in the order of the presentation, hidden slides
included. `Width` and `Height` are the size of the slide in points, and
every block that comes from a shape has `loc.bbox`: the shape's box on the
slide, after the groups it is in have moved, scaled, turned and mirrored
it. A placeholder that does not say where it is has the place of the
placeholder it takes after, on the layout or the master.

| On the slide | In the model |
|---|---|
| Title | `heading` level 1, and the part's `Name`. A title of several paragraphs is one heading |
| Subtitle | `heading` level 2 |
| Body text and text boxes | `list_item` where the paragraph has a bullet or a number, with its level, and the bullet or the number as shown in `Marker` (`•`, `3.`, `b)`, `IV.`); `paragraph` where it has none |
| Table | `table`, with merged cells, and the first row as header where the table says it is one |
| Picture | `image`, with its description as text, when `Options.Images` asks for pictures |
| Date, footer and slide-number placeholders | `footer`. A slide-number field shows the slide's number |
| Text the layout or the master has of its own | `header` if it is in the upper half of the slide, `footer` if in the lower; on every slide that shows it, once where layout and master repeat each other |
| Speaker's notes | `note` blocks of type `speaker`, one for each paragraph |
| Comments, in both the older and the threaded form | `note` blocks of type `comment`, with author and date; replies follow the comment |

Whether a paragraph has a bullet, how large its text is and what colour it
has are often not said on the slide: they come from the placeholder on the
layout, from the one on the master, from the master's style for that kind
of text, or from the presentation's defaults. All of those are read. The
"Click to edit" text of the placeholders on layouts and masters is not on
any slide and is not extracted.

**Reading order.** Shapes are stored in the order they were drawn, not the
order they are read in. The blocks of a slide are given title first, then
from the top down, and from left to right among shapes that start beside
each other. A group is read as one thing, at the place of the group. Shapes
that lie wholly outside the slide come after those on it, then the layout's
and master's text, the notes and the comments. The order is a reading of
the layout; what each block is and where it ends are declared, so blocks
stay `origin: native`.

**Limits.** `Extractor.MaxCells` bounds the cells of tables as it does for
workbooks. The text of a master is on every slide that uses it, so a long
master and many slides can ask for much more than the file holds: at most
about a million blocks are repeated in this way over a presentation, and a
slide past that says so with `limit.exceeded`. What a limit leaves out is
the same for any number of workers.

**Not read:** charts, diagrams (SmartArt) and sound and video clips, each
counted in `ooxml.unread_content`; pictures on layouts and masters; the
notes master and handout master; animations, other than to know which
shapes they move (see hidden text). An action that starts a program or
runs a macro is reported as `input.active_content` and never run.

## Hidden text

Text that a person reading the document cannot see is extracted like any other
text and flagged, never dropped. A block's `Hidden` field says why. Where only
part of a paragraph is hidden the reason is on a span over that part, and in a
table on the cells concerned. Every part that has flagged text carries a
`text.hidden` warning, and `RenderOptions.ExcludeHidden` leaves the flagged
text out of rendered output.

`Document.HiddenChecked` lists the reasons that are checked completely for
the format. For Word and Excel they are `invisible`, `tiny`, `low_contrast`
and `invisible_chars`. For PowerPoint, where every shape has a place, they
are those and `off_page`, `occluded` and `transparent`.

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

What is flagged in a PowerPoint presentation:

| Reason | What is flagged |
|---|---|
| `invisible` | A hidden slide: the part and all its blocks, its notes included. A shape or a group marked hidden. Text with no fill and no outline. Text in a table cell that a merged cell covers |
| `tiny` | Text under 2 points, with the size taken from the run, the paragraph's level in the shape, the placeholders it takes after, the master's styles or the presentation's defaults, and after any shrinking to fit the shape |
| `low_contrast` | Text whose colour cannot be told from what it is seen against. The colour is the run's, or inherited as the size is, or the one the shape's style or the table's style gives it. What it is seen against is the run's highlight, the fill of its shape or table cell, or, for a shape with no fill, the last thing drawn under it: a filled rectangle on the slide, the layout or the master, or the slide's background. Theme colours go through the colour map in force, including one a slide or layout replaces, and lightened, darkened and partly transparent colours are worked out |
| `transparent` | Text filled with a colour of zero opacity |
| `off_page` | A shape that lies wholly outside the slide |
| `occluded` | Text whose shape lies wholly under a later shape that hides it: a rectangle filled with one opaque colour, or a JPEG or a PNG that has no transparency. A shape that an animation acts on is not counted as hiding anything |
| `clipped` | Text in a shape that cuts off what does not fit and is too low for one line. Flagged where found; it is not in `HiddenChecked` |
| `invisible_chars` | Characters with no visible form, found by the shared model itself |

A slide with more overlapping shapes than the checks compare (some hundreds
of thousands of pairs) carries `ooxml.hidden_unchecked`: its text was not all
compared with what lies under and over it.

A flag states a fact, so where the facts cannot be established nothing is
flagged. What this means for a reviewer:

- **Backgrounds that are not one known colour.** Against a gradient, a
  picture or a striped pattern, text colour is not judged. On a slide that
  includes text over a picture, over a shape that is not a rectangle, or
  over the edge of one: only a rectangle of one colour that lies under all
  of the text's shape is taken as what the text is seen against.
- **Tables in PowerPoint whose style is not in the file.** Office has
  table styles built in, and a file may name one without defining it (files
  written by python-pptx do). What such a style gives a cell is not known,
  so text there is judged only against a fill the cell itself states.
- **What a slide looks like during the show.** Animations are not played.
  Text that an animation reveals, hides or moves is judged where the slide
  stores it.
- **Conditional formatting in Excel.** It changes a cell's colours by its
  value, which is not worked out. Text in a range it applies to is not
  judged against its fill.
- **Table styles in Word.** The shading a table's style gives cells is used; the text
  colour it gives them is not. Whether a cell is in the last row or column is
  used only where the file states it.
- **Position in Word and Excel.** This library does no page layout. Text
  covered by a floating shape or picture, or pushed off the page, is not
  detected there. On a slide, where shapes say where they are, it is; but
  text that runs out of its shape is taken to be where the shape is.
- **Fonts and spacing.** A font whose glyphs are blank, or spacing that piles
  characters on each other, is not detected.
- **Content never shown on a page but extracted by design**: document
  properties, alternative text of pictures, comments, speaker's notes. Each
  has its own place in the model (metadata, `image` blocks, `note` blocks),
  so a consumer can treat it apart.
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
| `internal/pml` | The PowerPoint reader: presentation, layouts and masters, the shape tree of a slide, placeholders and what they inherit, tables and table styles, reading order, what covers what, notes and comments |
| `internal/dml` | DrawingML, which all three formats use for what is drawn: colours and fills, where a shape lies, and the text of shapes with its list styles |
| `internal/theme`, `internal/media`, `internal/hidden`, `internal/label`, `internal/warn` | Shared by the readers: theme colours and fill styles, pictures and embedded files, settling which text is hidden, bullets and numerals, warning codes |
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
