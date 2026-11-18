# go-fast-ooxml

Extracts Word, Excel and PowerPoint files (`.docx`, `.xlsx`, `.pptx` and their
macro and template variants) into the shared go-fast document model,
[`gofastcore.Document`](https://github.com/AOShei/go-fast-core). Standard
library and go-fast modules only.

**Status: under construction.** The test harness, fixtures, corpus manifest and
baselines exist. There is no extraction code yet, and nothing is released.

## Layout

| Path | Content |
|---|---|
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
