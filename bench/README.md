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
