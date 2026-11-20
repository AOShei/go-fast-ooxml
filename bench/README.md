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
| msoffcrypto-tool | all three, encrypted | oracle for decryption: the bytes an encrypted file decrypts to; producer of an encrypted fixture |

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
| `comparison.json`, `comparison.md` | `python bench/compare.py` | This library against the tools in use today, on the large files and the corpus, each measured as a process. The table in the top-level README is written by the same run |
| `soak.json` | `go test -run TestSoak -soak-record .` | What became of each of 6,598 files: how many were read, what the rest were, which warnings and display reasons occur |
| `accuracy.json` | `go test -run TestBaselineAccuracy -update .` | How well each tool recovers the known text of each fixture: precision, recall, F1 and word order, scored by the same code that scores this library |
| `baseline.json`, `baseline.md` | `python bench/run_baseline.py` | Speed and peak memory of each tool on the large generated files and on the corpus, and how many corpus files each could not read |
| `word.txt` | `go test -run '^$' -bench 'Extract/large.docx' -count 3 .` | Word extraction on the large generated file, single-threaded |
| `excel.txt` | `go test -run '^$' -bench 'Extract/large.xlsx' -benchmem -count 3 .` | Excel extraction on the large generated workbook (2 million cells, 100 MB of XML), with one worker and with all |
| `powerpoint.txt` | `go test -run '^$' -bench 'Extract/large.pptx' -benchmem -count 3 .` | PowerPoint extraction on the large generated presentation (1,500 slides), with one worker and with all |
| `encrypted.txt` | `go test -run '^$' -bench 'Encrypted\|Extract/large.xlsx' -count 3 .` | What encryption adds: the check of a password, and the large workbook encrypted beside the same workbook unencrypted, in one run |
| `xmlscan.txt` | `go test -run '^$' -bench . -count 3 ./internal/xmlscan` | The library's XML tokenizer against `encoding/xml` on the XML of the large generated files. Compare the two within one run: the ratio holds when the machine is busy, the absolute numbers do not |

Timings come from one machine and compare tools on that machine only.

## Reproducing

```sh
python3 testdata/fetch.py                                          # corpus, about 106 MB
python3 testdata/soak.py                                           # soak set, about 290 MB on disk
../bench-env/.venv-ooxml/bin/python testdata/gen/gen.py --large    # fixtures and large files
go test -run TestBaselineAccuracy -update .
../bench-env/.venv-ooxml/bin/python bench/run_baseline.py          # several minutes
../bench-env/.venv-ooxml/bin/python bench/compare.py               # several minutes; rewrites the README's table
go test -run TestSoak -v -soak-record .                            # under a minute
go test -run Budgets -v -speed .                                   # allocation budgets and speed targets
```

`baseline.md` is the record the targets were set from, before the library
existed. `comparison.md` is the same measurement repeated with the library
in it.

## Comparison with the tools in use today

`comparison.md` has the tables; this is what they say.

On the large files, as whole processes, the other tools take 36 to 40
times as long on Word, 32 to 222 times on Excel and 20 to 98 times on
PowerPoint, and each uses more memory. The exception is docx2txt, which
gives the text of a Word file and nothing else: it takes one and a half
times as long.

On the 788 files of the corpus:

| | Word (195) | Excel (477) | PowerPoint (116) |
|---|---|---|---|
| This library: read, refused with a reason | 178, 17 | 457, 20 | 105, 11 |
| Best other tool: read, failed with an exception | 176, 19 (docx2txt) | 444, 33 (openpyxl, read-only) | 100, 16 (python-pptx) |
| This library: time for all it read | 0.4 s | 4.5 s | 0.4 s |
| Fastest other tool | 1.2 s | 38.3 s | 0.7 s |

Most of the Excel time is one hostile file, which is cut off at 2 GiB of
text after about four seconds (see the soak run below). Small files are where the tools are closest: the median Word file takes
this library 1.6 ms as a process, most of which is starting it.

The measurement favours the other tools in one way: their time does not
count importing the library, this library's counts the whole process.

## Targets and budgets

The speed targets of the plan, on the large generated files, as measured
by `go test -run TestLargeFileBudgets -speed` (the best of seven runs, in
the library, not counting the start of a process):

| File | Target, one worker | Measured | Target, all workers | Measured |
|---|---|---|---|---|
| `large.docx` | 0.11 s | 0.062 s | 0.11 s | 0.061 s |
| `large.xlsx` | 0.80 s | 0.58 s | 0.30 s | 0.17 s |
| `large.pptx` | 0.10 s | 0.045 s | 0.04 s | 0.009 s |

Time is the machine's as much as the library's, so the test holds it only
when asked to. What it always holds, and CI with it, is allocation, which
does not depend on the machine (`TestAllocationBudgets`): what one more
paragraph, cell or slide costs, measured as the difference between a
document and one twice as long.

| One more | Allocations | Bytes allocated | Budget |
|---|---|---|---|
| Word paragraph | 1.0 | 1,220 | 1.5 and 1,800 |
| Excel cell | 0.004 | 208 | 0.02 and 320 |
| PowerPoint slide | 63 | 4,980 | 95 and 7,500 |

A paragraph costs one allocation: its text. Cells of a sheet share a few
large allocations. A slide is a member of the archive with relationships
of its own, which is most of its 63.

## Soak run

`TestSoak` reads every file of the corpus and of the soak set: 6,598
files, 355 MB. The soak set (`testdata/soak.py`) is the Office files in the
test suites of LibreOffice, Apache Tika, pandoc, PhpSpreadsheet, PHPWord,
ClosedXML, Excelize, mammoth.js, python-docx, python-pptx, the Open XML SDK
and ShapeCrawler, and Apache POI's binary workbooks (its other files are
in the corpus), at pinned commits: 5,810 files that those projects keep
because something once went wrong with them, among them LibreOffice's
collections of crash cases and fuzzer findings.

Each file is read with one worker and with eight, with spans, with
pictures, under a small cell limit and a small decompression budget, and a
part at a time. For each: a valid document or a shared error; the same
document for one worker and eight; the same text with spans; a document
that survives its JSON form; parts read one at a time equal to the
document's. Nothing is asserted about what a file says.

The run is also made under the race detector (`go test -race -run
TestSoak`, three minutes): no data race in any of the readers. The test
program stops itself if its heap passes a ceiling (4 GiB; it reaches 1.5),
and does not write out a document of more than 64 MB of text to compare
it: two files of the set are made to produce that much.

| | Files |
|---|---|
| Read | 6,521 (3,328 Word, 1,920 Excel, 1,273 PowerPoint) |
| Refused, each with a reason | 50 |
| Crashes, hangs, invalid documents | 0 |

| Refused because | Files |
|---|---|
| Needs a password | 24 (17 of them from the soak set; 13 of those are opened with the password their project's tests give) |
| Not an archive at all | 9 |
| An archive of another format (4 OpenDocument files named `.docx`, a package with only a theme, a plain zip) | 6 |
| An archive without a readable directory, with too little left to read a document from | 5 |
| A package whose own structure is damaged | 4 |
| Legacy binary format; encryption with a cipher other than AES | 2 |

38 files are archives whose directory cannot be read: cut off before it
(19) or damaged there (19). Most are fuzzer and crash cases; four are
test files made by cutting a file short, and none is among the files
published on data.gov.uk. 33 of the 38 are now read by their members' own
headers and carry `input.repaired`; before recovery was added all 38 were
refused. `Divino_Revelado.pptx`, a presentation cut at 524 KB, gives 12
slides. The count is small, and says little about how often it happens:
a file cut in transit in someone's pipeline is not in anybody's test
suite.

Among the documents read, 36 have a part that failed (a sheet the
workbook lists and does not contain, XML that breaks off, a member cut
short) and 28 have a damaged part that is not the main one; each
says so in its warnings. 269 documents have text the file declares hidden,
and 6 text drawn with no ink.

The run found one defect: asking for spans changed how consecutive
paragraphs of code in a Word document were grouped. It is fixed, and
`TestSpansChangeNothingElse` holds every sample document to it.

The slowest file was a hostile one from Apache POI
(`poc-shared-strings.xlsx`): 110 KB in which thousands of cells refer to
one shared string of a megabyte. Within the limits as they were it became
2 GiB of text, after about four seconds. It is why the model now has a
limit on the text a document may hold (`Limits.MaxTextBytes`, 256 MiB by
default): the sheet is cut off there, and the soak run takes 17 seconds
where it took 40.

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
| 179 | Displayed as nothing by the format; the value is kept here, with the reason `hidden` | agreeing |
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
| The same through `Rows`, 1,000 rows to a band | 20 to 25 MB |
| A sheet of 1.6 million cells, whole | 169 MB |
| The same through `Rows` | 10 to 11 MB |
| A sheet of 200,000 cells through `Rows` | 7 to 8 MB |
| 3,000 slides with 28 MB of text, whole | 56 MB |
| The same through `Parts` | 11 to 13 MB |

The figures include garbage not yet collected, which is why they move from
run to run and are not flat. The tests are therefore of proportions, which
hold on any system and under the race detector: a band at a time takes
under a third of the sheet whole, and a part at a time under a third of the
document whole and less than its text alone. On Linux without the race
detector `Rows` is also held under 48 MB.

**`Rows` with more than one worker.** Measured on 2026-11-20 on the first
sheet of `large.xlsx` (half a million cells), best of five rounds of ten:

| | Time |
|---|---|
| One worker | 222 ms (224 with the sheet read in chunks, as now) |
| More than one: decompressing on its own goroutine, ahead of the scanner | 138 ms, 1.62 times as fast |

The sheet's part is read twice: once for what comes after the rows (merged
cells), which the first band needs, and once for the rows. A profile puts
39% of the time in decompressing, 38% in the first pass, and 5% in making
the tables of the bands. Decompressing beside the scanner is the one split
that pays: making the tables on a goroutine of their own could save at most
the 5%, and the two passes cannot overlap, since the rows wait on the merged
cells. Decompressing ahead reaches the ceiling a profile set for it (1.6).
Reading the rows of one band on several workers while one scans was put at
2.7 at best, before the first pass was measured, which it would not speed
up. It was decided against: it would need the scanner's state (positions
taken from the row or cell before, the cut where a limit falls, the order
of warnings) handed between goroutines, and a second parser to find where
rows begin that agrees with the scanner on every damaged file, for a gain
only on a very large sheet read through `Rows`.

The bands are the same with one worker and with four, within the limits and
with a limit on decompressed bytes that cuts sheets off, over the 1,863
workbooks of the corpus and the soak set (`TestRowsTheSameWithAnyWorkers`).
For that the sheet is read in chunks of 256 KiB both ways, and the
decompression budget is charged a chunk at a time as the scanner takes it,
so that where the limit cuts a sheet does not depend on how far ahead the
decompressing has got.

**A large part read ahead.** With more than one worker, a Word body or a
sheet of 1 MiB or more (as the archive gives its size) is decompressed on
a goroutine of its own, ahead of its reading, in `Extract` and `File` as
in `Rows`. Measured on 2026-11-20 on the 226 Word and Excel files of the
corpus whose largest body or sheet is 128 KiB or more, read ahead and not,
best of ten:

| Largest body or sheet | Files | Read ahead, against not (geometric mean) |
|---|---|---|
| 128 to 512 KiB | 144 | 0.95 to 0.98: slower |
| 512 KiB to 1 MiB | 31 | 1.10 |
| 1 to 2 MiB | 25 | 1.16 |
| 2 to 8 MiB | 19 | 1.19 to 1.23 |
| 8 MiB and more | 7 | 1.26 |

Under 512 KiB the cost of starting the read-ahead is more than it saves,
hence the line at 1 MiB; its buffers are made only as it needs them. On
every Word, Excel and PowerPoint file of the corpus and the soak set
(6,375), one at a time with all workers, the whole run takes 6.77 s where
it took 7.32 (1.08 times as fast; best of two), and 1.69 s where it took
1.77 with 24 files read at once, where the large files are the last to
finish. The documents are the same, byte for byte, in all of these runs.

Which way a part is read depends on its size, not on the workers: a part
of 1 MiB or more is read in chunks, its budget charged a chunk at a time,
whether or not ahead. The document is the same with one worker and with
four (`TestLargePartsTheSameWithAnyWorkers`): on made files whose large
part is broken partway, under limits on decompressed bytes that cut the
large part or what comes after it, on text and on parts, and on the 51
files of the corpus and soak set with a part read ahead, with no limits
and with 3 MB decompressed.

The cells that `Rows` gives are the cells of the sheet read whole, with the
same display reasons: checked on the fixtures and on every sixth corpus
workbook under 400 KB (`TestRowsOnFixturesAndCorpus`: 74 workbooks, 189
sheets, 159,654 cells).

Every file of the corpus goes through the command
(`go test ./cmd/gofastooxml`), with the files embedded in it extracted too:
773 give a valid document and 15 are refused with a reason (files that
need a password, a legacy binary file, files that are not packages,
archives with too little left of them).

## Binary workbooks

Excel is the only program that writes `.xlsb`, so the tests check the
reader three ways (2026-11-20):

- **Against the same workbook as XML.** `TestXlsbAsXlsx` and
  `TestXlsbAsXlsxMore` write each case twice, as `.xlsx` and as `.xlsb`
  (records made by `internal/ooxmltest/xlsb.go`), and compare the two
  documents whole, JSON for JSON, with cells' details and spans: values of
  every kind and record form, number formats, dates from 1900 and 1904,
  symbol fonts, hidden rows, columns and sheets, merged cells, formulas
  with shared and array formulas, defined names, references to other
  sheets, links, drop-down lists, declared tables, comments. The limits
  on cells and text cut both at the same cell, and reading by bands gives
  the same bands (`TestXlsbLimitsAndBands`).
- **Against pyxlsb.** Every cell pyxlsb reads from the 24 binary
  workbooks of the soak set it can open holds the same text, truth value
  or stored number here: 905 cells (`TestXlsbAgainstPyxlsb`).
- **Against LibreOffice.** Each workbook saved as `.xlsx` by LibreOffice
  and read by this library has the same formula in every cell where both
  have one: 324 formulas over 26 workbooks
  (`TestXlsbFormulasAgainstLibreOffice`). The other differences are
  LibreOffice's own: it lays pivot tables out again ("Total Result" for
  "Grand Total"), recalculates some errors, and gives a cell holding TRUE
  the formula `TRUE()`.

Two of Apache POI's files (`Simple.xlsb`, and the same file in Tika's
suite) write four bytes more than the format has in each entry of their
sheet list. pyxlsb stops on them and LibreOffice reads three empty sheets
of other names; here each sheet says that the workbook lists it but does
not contain it. One formula of the set is not read back: it refers to a
sheet of another workbook (`bug66682.xlsb`); LibreOffice gives it as
`#REF!`.

The paired tests showed that the `.xlsx` reader counted a formula whose
result is empty text as stored without a result
(`ooxml.formula_no_value`); it has one, and 32 fewer documents of the soak
set carry the warning.

## Fuzz campaign

Every fuzz target, run for the release (twelve processes, on the machine
of `baseline.md` while it was busy with other work):

| Target | What it is given | Time | Executions | Result |
|---|---|---|---|---|
| `FuzzOpen` | A whole file | 10 min | 33 million | clean |
| `FuzzDocx` | The parts of a Word document in a sound package | 10 min | 7.8 million | clean |
| `FuzzXlsx` | The parts of a workbook | 10 min | 2.4 million | clean, after one finding |
| `FuzzPptx` | The parts of a presentation | 10 min | 2.5 million | clean |
| `FuzzXML` | XML, compared token for token with `encoding/xml` | 10 min | 81 million | clean, after two findings |
| `FuzzCFB` | A compound file | 5 min | 68 million | clean |
| `FuzzEncryptionInfo` | The encryption information of a protected file | 5 min | 63 million | clean |
| `FuzzNumFmt` | A number format and a value | 5 min | 85 million | clean |
| `FuzzRecover` | Bytes behind the start of an archive, read by its members' own headers | 6 min | 48 million | clean |

The readers of binary workbooks came after the campaign, and were fuzzed
when they came (2026-11-20, four processes each): `FuzzXlsb`, the parts of
a binary workbook, 10 minutes, clean; `FuzzFormula`, a compiled formula
and its place, 5 minutes and 68 million executions, clean after one
finding (a count past the end of a formula was allocated before it was
checked); `FuzzRecords`, a part as records, 5 minutes and 37 million,
clean. The release campaign runs them with the others.

`FuzzRecover` came with the recovery of archives cut short, after the
campaign; `FuzzOpen` was then run again with files cut short among its
seeds (6 minutes, 7.4 million executions, clean).

The findings, each now a seed that every test run replays, and a test of
its own:

- **A character reference with many zeros in front** (`&#0000100000;`) was
  left as written: the XML reader took a reference to be at most twelve
  bytes. It reads them at any length now, as `encoding/xml` does
  (`TestCharacterReferencesWithLeadingZeros`).
- **A sheet that gives one cell twice, rows out of order.** Read whole, the
  sheet has the cell once; a band at a time (`Rows`), it is in each band it
  falls in, since rows are put in order within a band only. That is the
  behaviour, and is now documented; the check in the fuzz target was
  wrong to count cells instead of comparing them by place
  (`TestRowsOfASheetThatGivesACellTwice`).
- **An encoding declaration with a space after the equals sign**
  (`<?xml encoding= "0"?>`): `encoding/xml` does not see such a
  declaration and reads the bytes as UTF-8. This reader sees it, as XML
  says it should, and refuses an encoding it cannot read. The comparison
  now knows the difference (`TestEncodingDeclarationWithSpaces`).

None was a crash, a hang or a way past a limit.

## Encrypted files

Fourteen files are decrypted both by this library and by msoffcrypto-tool
(`TestDecryptionAgainstMsoffcrypto`): the nine files of the corpus that
Microsoft Office encrypted, of both schemes; the three fixtures in
`testdata/encrypted`, encrypted by LibreOffice and by msoffcrypto-tool; and
two the test encrypts itself. Eleven decrypt to the same bytes with both.
The other three msoffcrypto-tool does not open, and what this library
decrypts from them is a package that reads to its end:

| File | Why the tool does not open it |
|---|---|
| `bug53475-password-is-pass.docx` | An AES-256 key from a SHA-1 hash: the hash has to be lengthened, which the tool does not do |
| `protected_agile.docx` | The value a password is checked against is a SHA-1 hash, 20 bytes stored in 32; the tool compares all 32 |
| `extenxls_pwd123.xlsx` | The compound file states the encrypted stream longer than the sectors it gives it; the package inside is whole |

What encryption costs (`bench/encrypted.txt`; the encrypted and the
unencrypted workbook are timed in one run, since the machine was busy):

| | Unencrypted | Standard scheme | Agile scheme |
|---|---|---|---|
| Checking a password | | 2.6 ms (50,000 rounds of SHA-1) | 12.6 ms (100,000 rounds of SHA-512) |
| `large.xlsx`, one worker | 0.59 s | 0.60 s | 0.63 s |
| `large.xlsx`, all workers | 0.176 s | 0.175 s | 0.203 s |

The agile scheme's extra 30 to 40 ms on this file of 20 MB is the check of
the password, the keyed hash over the whole encrypted file, and the
decrypting. A file of 32 MB with a small document in it is read with a
peak heap under 1 MB (`TestEncryptedFileIsNotHeldDecrypted`): the package
is decrypted as it is read, not held.

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

## What files declare, on the corpus

Over the corpus and the soak set (3,236 Word documents, 1,873 workbooks,
1,218 presentations), files and blocks with each reason, measured on
2026-11-20 with the build that stopped judging appearance, beside the one
before it:

| | Files before | Files now | Blocks now |
|---|---|---|---|
| Word, `hidden` | 27 (`invisible`) | 112 | 366 |
| Word, `no_ink` | 2 (`transparent`) | 2 | 2 |
| Word, reasons removed: `low_contrast`, `tiny`, `off_page`, `clipped` | 24, 3, 2, 1 | | |
| Excel, `hidden` | 119 (`invisible`) | 97 | 534 |
| Excel, reasons removed: `low_contrast`, `tiny`, `invisible_chars` | 22, 2, 3 | | |
| PowerPoint, `hidden` | 49 (`invisible`) | 51 | 588 |
| PowerPoint, `no_ink` | 3 (`transparent`) | 4 | 6 |
| PowerPoint, reasons removed: `low_contrast`, `off_page`, `occluded` | 13, 5, 2 | | |

Word's `hidden` rose because a header or footer the document's own
settings switch off (a first-page one with no different first page, an
even-page one without different odd and even pages) is declared hidden
again: before, it was marked only when it held more than 200 words, a rule
that is gone. A header or footer part that no section names is used by
nothing and has no reason. Every field code is now kept, as stored data:
2,867 codes in 517 Word files, where 249 in 62 files were kept before; a
code in a header or footer that is switched off takes its reason. Notes
are 96 of the 366 hidden blocks, and 24 of the 112 files have hidden notes
and nothing else hidden. Excel's fell because the hidden
stand-in Excel writes for each form control (the control itself is in the
legacy drawing, and shown) is no longer taken for a hidden shape: about 20
workbooks of buttons and check boxes. Text with no fill in PowerPoint was
`invisible` and is `no_ink`.

Five files have data after the end of their archive, each a single line
feed, reported with `input.trailing_data`.

Before this, the library judged colour, size, position and overlap. An
audit of every file those checks marked, each rendered with LibreOffice, found no
malicious content in the corpus and eight kinds of false alarm, all in
those judgements: the colours of notes pages, a table's own background,
text boxes over shapes, text over pictures placed behind it, embossed
text, and the stand-ins for form controls. Those checks were removed for
the whole go-fast series.
