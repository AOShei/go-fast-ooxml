# Baselines and comparison environment

The tools people use today for these formats are the baseline this library is
measured against, and they double as test oracles.

| Tool | Formats | Role |
|---|---|---|
| python-docx | Word | comparison, oracle for tables |
| docx2txt | Word | comparison |
| openpyxl (normal and read-only mode) | Excel | comparison, oracle for raw cell values |
| python-pptx | PowerPoint | comparison, oracle |
| markitdown | all three | comparison |
| LibreOffice (headless) | all three | oracle: text export with computed list numbering, CSV export with formatted cell values; second producer of every fixture |
| pandoc | Word | oracle for heading levels |

## Setting up

The Python tools are pinned in `../../bench-env/requirements-ooxml.txt` and live
in their own environment, apart from the PDF tools:

```sh
cd ../bench-env
uv venv --python 3.13 .venv-ooxml
uv pip install --python .venv-ooxml/bin/python -r requirements-ooxml.txt
```

Tests find the interpreter at `../bench-env/.venv-ooxml/bin/python`, or where
`GOFASTOOXML_PYTHON` points. LibreOffice and pandoc are found on `PATH`. Any
test whose tool is missing skips itself.

## What is recorded here

| File | Produced by | Content |
|---|---|---|
| `accuracy.json` | `go test -run TestBaselineAccuracy -update .` | How well each tool recovers the known text of each fixture: precision, recall, F1 and word order, scored by the same code that scores this library |
| `baseline.json`, `baseline.md` | `python bench/run_baseline.py` | Speed and peak memory of each tool on the large generated files and on the corpus, and how many corpus files each could not read |
| `word.txt` | `go test -run '^$' -bench 'Extract/large.docx' -count 3 .` | Word extraction on the large generated file, single-threaded |
| `excel.txt` | `go test -run '^$' -bench 'Extract/large.xlsx' -benchmem -count 3 .` | Excel extraction on the large generated workbook (2 million cells, 100 MB of XML), with one worker and with all |
| `powerpoint.txt` | `go test -run '^$' -bench 'Extract/large.pptx' -benchmem -count 3 .` | PowerPoint extraction on the large generated presentation (1,500 slides), with one worker and with all |
| `xmlscan.txt` | `go test -run '^$' -bench . -count 3 ./internal/xmlscan` | The library's XML tokenizer against `encoding/xml` on the XML of the large generated files. Compare the two within one run: the ratio holds when the machine is busy, the absolute numbers do not |

Timings come from one machine and compare tools on that machine only.

## Reproducing

```sh
python3 testdata/fetch.py                                          # corpus, about 106 MB
../bench-env/.venv-ooxml/bin/python testdata/gen/gen.py --large    # fixtures and large files
go test -run TestBaselineAccuracy -update .
../bench-env/.venv-ooxml/bin/python bench/run_baseline.py          # several minutes
```

## Accuracy on the corpus

`go test -run TestWordCorpus -v .` compares every sound Word file in the corpus
with three other programs and holds the library to floors set a little under
what was measured:

| Measure | Against | Measured | Floor |
|---|---|---|---|
| Words of the body found | python-docx | 0.995 mean over 174 files | 0.99 |
| Words of the whole document found | LibreOffice text export | 236,681 of 237,399 (0.997) | 0.99 |
| Computed list and heading labels agreeing | LibreOffice text export | 352 of 352 | 0.98 |
| Headings found at the same level | pandoc | 75 of 75 | 0.98 |

Notes on reading these numbers:

- LibreOffice writes a footnote's number straight after the word it hangs on
  and leaves the footnote's text out, so a word is also counted as found
  without such a number, and text this library has beyond LibreOffice
  (footnotes, comments) is not counted against it.
- Nearly all of the words not found are in one file whose body is an embedded
  HTML document. That is reported as an embedded file, to be read by an HTML
  extractor.
- python-docx repeats the text of a merged table cell for every row it covers,
  which is the reason for the lowest per-file scores.
- pandoc knows a heading by its style name only. This library also goes by a
  declared outline level, Word's own definition, and finds 269 more.
- One file is cut off at the nesting limit on purpose and is not scored; one
  is an ambiguous archive that programs read differently and is not scored.

## Excel accuracy on the corpus

`go test -run TestExcelCorpus -v .` compares every cell of every sound
workbook in the corpus with what LibreOffice displays for it (its CSV
export, "as shown"). Dates are compared with openpyxl's values instead,
since this library writes them in ISO 8601 and not as displayed.

As measured: 446 workbooks, 6,941,917 cells compared, **2 differ** without
a named reason. The floor is 0.9999.

Every way the two can differ without either being wrong is a named class,
counted on each run:

| Cells | Class | Counted as |
|---|---|---|
| 127,228 | Dates and times: ISO 8601 here, as displayed there; equal to openpyxl's value | agreeing |
| 179 | Displayed as nothing by the format; the value is kept here and flagged as hidden | agreeing |
| 5 | The fifteenth digit: 2.3561944901923448 is 2.35619449019234 to fifteen digits; LibreOffice rounds to sixteen first and shows ...235 | agreeing |
| 2 | The same number in another notation (`1E+20`, `1E+020`) | agreeing |
| 1,788 | Text longer than a cell can hold in Excel (one file, made to attack readers, with a string of a megabyte in each cell); LibreOffice shortens or drops it | left out |
| 626 | Sheets with pivot tables, which LibreOffice builds again with its own labels and formats | left out |
| 458 | LibreOffice shows an error | left out |
| 155 | Sheets LibreOffice reads nothing from (one damaged file) | left out |
| 101 | Formula cells where LibreOffice calculated another result than the file stores | left out |
| 40 | Logical values: Excel shows TRUE or FALSE whatever the number format; LibreOffice puts them through it | left out |
| 3 | Numbers a format writes in the digits of another script; ASCII digits here | left out |
| 2 | Dates not checked: openpyxl cannot read the file | left out |

The two that differ, each by design: a date the file stores as text in ISO
form already, which is given as stored (LibreOffice displays it by its
format); and a negative date serial, which Excel cannot show as a date and
which is given as the number.

The number formats themselves are held to a table: `testdata/numfmt/libreoffice.tsv`
is what LibreOffice displays for 84 formats applied to 72 values, and
`go test ./internal/numfmt` requires the engine to give the same for all
6,048 pairs, apart from 204 where LibreOffice shows an error, 174 where the
format displays nothing and the value is kept, and 23 very large or very
small numbers in another notation. `TestNumberFormatTable` checks the table
against LibreOffice itself.

## Excel speed and memory

On `large.xlsx` (19.9 MB, four sheets, 2 million cells, 100 MB of XML), on
the machine of `baseline.md`:

| | Time | Peak memory |
|---|---|---|
| openpyxl, read-only mode | 9.45 s | 308 MB |
| This library, one worker | 0.58 s | 127 MB |
| This library, all workers | 0.17 s | 172 to 220 MB over five runs |

Memory is the largest size the heap reached during `Extract`, garbage
included (`TestLargeWorkbookMemory`, which holds it under 300 MB).

## Reading a part at a time

`File.Parts` and `File.Rows` hand on one part, or one band of a sheet's
rows, at a time. What they hold is measured as the Excel reader's memory
is, by `TestRowsMemoryDoesNotGrowWithTheSheet` and
`TestPartsMemoryDoesNotGrowWithTheDocument`:

| | Peak memory |
|---|---|
| `large.xlsx`, 2 million cells, whole (`Extract`, one worker) | 127 MB |
| The same through `Rows`, 1,000 rows to a band | 20 MB |
| A sheet of 200,000 cells through `Rows` | 7 MB |
| A sheet of 1.6 million cells through `Rows` | 11 MB |
| 300 slides through `Parts`, file on disk | 1 MB |
| 3,000 slides (8 MB of text) through `Parts`, file on disk | 3 to 4 MB |

The figures include garbage not yet collected, which is why they are not
flat. The tests hold `Rows` under 48 MB and `Parts` under 6 MB.

The cells that `Rows` gives are the cells of the sheet read whole, with the
same hidden flags: checked on the fixtures and on every sixth corpus
workbook under 400 KB (`TestRowsOnFixturesAndCorpus`: 74 workbooks, 189
sheets, 159,654 cells).

Every file of the corpus goes through the command
(`go test ./cmd/gofastooxml`), with the files embedded in it extracted too:
737 give a valid document and 46 are refused with a reason.

## PowerPoint accuracy on the corpus

`go test -run TestPowerPointCorpus -v .` compares every sound presentation
in the corpus with python-pptx, slide by slide (the `python-pptx-slides`
adapter in `testdata/gen/tools.py`).

python-pptx reads the shapes of a slide and nothing of its layout or
master, and it does not tell a slide's footer from its text. So the words
are compared twice. Everything python-pptx reads must be found. And what
this library reads apart from furniture (the date, footer and slide-number
placeholders, and the layout's and master's own text) must be what
python-pptx reads apart from those placeholders.

As measured: 100 presentations, 676 slides; python-pptx cannot read 5 more,
which this library reads.

| What | Result | Floor |
|---|---|---|
| Words python-pptx reads that are found (mean recall) | 1.0000 | 0.999 |
| Words of the body against python-pptx (mean F1) | 1.0000 | 0.99 |
| Slides with a title where the slide's name is that title | 381 of 381 | 0.99 |
| Slides hidden or not, as python-pptx says | 676 of 676 | all |

Order is checked on the fixtures and in tests with shapes in known places,
not against python-pptx, which gives shapes in the order they are stored.

## PowerPoint speed

On `large.pptx` (3.5 MB, 1,500 slides, each with notes, 150 tables), on the
machine of `baseline.md` (`bench/powerpoint.txt`):

| | Time |
|---|---|
| python-pptx | 0.37 s |
| This library, one worker | 0.048 s |
| This library, all workers | 0.010 s |

## Hidden text on the corpus

`HIDDEN_SURVEY=1 go test -run TestWordHiddenSurvey -v .` lists every piece of
text flagged as hidden in the corpus's Word files. It is how false alarms are
found: the first run flagged white text in coloured shapes and text boxes, in
three files, which is why the fill of a shape is now read.

As measured: 176 files read, 6 with flagged text, 10 blocks. Each was checked
against the file by hand, and a separate search of the raw XML for hidden,
tiny and white runs agrees:

| What | Files | Flagged |
|---|---|---|
| Text formatted as hidden (form markers pasted from a web page) | 1 | yes |
| White text on the white page (a banner pasted from a web page) | 1 | yes |
| Footers that no page shows: one never named by a section, two even-page footers in documents without different odd and even pages | 3 | yes |
| Bright yellow text on white, under the shared contrast threshold | 1 | yes |
| White text on dark paragraph or cell shading | 2 | no |
| White text on a dark highlight | 1 | no |
| White text in a filled shape or text box | 3 | no |

For Excel, `HIDDEN_SURVEY=1 go test -run TestExcelHiddenSurvey -v .` does
the same. As measured: 449 workbooks read, 31 with hidden text.

| What | Cells | Workbooks |
|---|---|---|
| On hidden sheets | 3,601 | 11 |
| In hidden columns | 6,822 | 6 |
| In hidden rows | 2,084 | 12 |
| Values their format displays as nothing | 164 | 1 |
| Text in the colour of its cell | 517 | 8 |
| Text in hidden shapes (macro buttons) | 25 | 5 |
| Invisible characters (zero-width spaces before a postcode) | 2 | 2 |

The cells flagged for their colour were checked against the styles in the
files: white text on unfilled cells, text in the colour of its fill (white
on white, yellow on yellow, black on black), and the linked cells of check
boxes in a one-point white font. Cells under conditional formatting are not
judged, since their colours depend on their values.

For PowerPoint, `HIDDEN_SURVEY=1 go test -run TestPowerPointHiddenSurvey -v .`.
As measured: 105 presentations read, 7 with hidden text. The first runs
flagged much more, and each false alarm changed the reader: titles in white
on a fill their own shape's style gives them (a shape's own style comes
before what it inherits from a placeholder), table headers in the colour a
table style gives them (it comes before the presentation's default colour),
and text under a picture that an animation brings in (a shape that an
animation acts on does not count as covering anything).

| What | Blocks | Presentations | Checked |
|---|---|---|---|
| On hidden slides | 40 | 2 | The slides are marked `show="0"` |
| White text in shapes with no fill on the white slide: leftovers beside pictures and under diagrams | 17 | 1 | Five of the slides rendered with LibreOffice: the words are not to be seen |
| Table cells: cyan on pale blue, bright yellow on white | 3 | 2 | Colours read from the table styles in the files; both are under the shared contrast threshold. The files are Apache POI's tests of font colours in tables |
| Text under a grey rectangle drawn after it, a slide number under a full-width box | 5 | 2 | The first rendered with LibreOffice: the rectangle covers the words |
| A legend of the brand's colours beside the slide, on the master | 47 | 1 | The shapes lie to the right of the slide's edge |
