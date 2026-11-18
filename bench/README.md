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

Timings come from one machine and compare tools on that machine only.

## Reproducing

```sh
python3 testdata/fetch.py                                          # corpus, about 106 MB
../bench-env/.venv-ooxml/bin/python testdata/gen/gen.py --large    # fixtures and large files
go test -run TestBaselineAccuracy -update .
../bench-env/.venv-ooxml/bin/python bench/run_baseline.py          # several minutes
```
