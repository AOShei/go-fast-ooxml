# A batch of real files, against Rust and Python tools

How this library reads many files in one process, which is how a service
or an indexing job uses it. The files are Word, Excel and PowerPoint
documents from the web, from Common Crawl. The other tools are the Rust and
Python libraries in use for these formats. Each run also measures the
memory and speed that Go's garbage collector setting (`GOGC`) trades.

The tables are in [`results.md`](results.md). This page gives the method
and what the tables say.

## The files

There are 1,980 documents: 1,411 Word, 499 Excel and 70 PowerPoint, 466 MB
in all. They were drawn at random from crawl `CC-MAIN-2025-43`, from the
files served whole with an Office media type, at most ten from one host.
They are not filtered: some are damaged, and one "workbook" is a zip
archive that is not an Office package.

Every tool reads the same lists:
- **The mixed batch:** all 1,980 files, shuffled (seed 1).
- **Each format alone:** for the tools that read only one.

## The method

- **One long-lived process for each tool.** `WORKERS` threads, goroutines
  or processes read the files in turn: 1, then 24 (the machine has 24
  hardware threads). Each file is read from memory, so disk speed does not
  count. A file has one worker, as `-j` gives it in the command.
- **What is timed.** The whole process, by `measure.py`: wall time, CPU
  time (user and system) and peak memory. Peak memory is the largest sum of
  the resident sizes of the process tree, sampled every 20 ms. Each tool
  writes each file's text, its time and whether it failed.
- **Repeats.** Each run is repeated three times, once for the Python tools.
  The tables give the fastest wall time, its CPU time and the median peak.
- **No tool runs a thread pool of its own.** markitdown's file-type model
  (onnxruntime) is held to one thread in each worker.

| Tool | Version | Reads |
|---|---|---|
| this library (`gooxml/`) | this checkout, Go 1.27 | all three |
| kreuzberg (`rust/rskreuzberg`) | 4.10.4 | all three |
| calamine (`rust/rsooxml`) | 0.38.0 | Excel |
| docx-rs | 0.4.22 | Word |
| pptx-to-md | 2.0.0 | PowerPoint |
| markitdown | 0.1.9 | all three |
| openpyxl (read-only, stored values) | 3.1.5 | Excel |
| python-docx, docx2txt | 1.2.0, 0.9 | Word |
| python-pptx | 1.0.2 | PowerPoint |

The machine is a 12-core Ryzen 9 9900X. It was shared during the runs: the
load from other work was 1.5 to 4 at the times most runs began. Every run
in the tables was made within the same 7 minutes, so they share those
conditions; the one exception is markitdown with one worker, which takes 13
minutes and was run earlier the same day. Compare tools within these
tables, not with runs of another day.

## What the tables say

**Speed.** This library is the fastest on the mixed batch and on Word and
Excel, with one worker or 24. On the 70 presentations, kreuzberg is faster
with one worker (0.10 s against 0.12 s); with 24 workers the three fastest
tools take 0.03 to 0.04 s.

| Batch, one worker | This library | Fastest other tool |
|---|---|---|
| All 1,980 files | 4.31 s, 111 MB | kreuzberg: 11.3 s, 2.9 GB |
| Word | 1.69 s, 35 MB | kreuzberg: 1.75 s, 116 MB |
| Excel | 2.44 s, 105 MB | calamine: 4.30 s, 1.6 GB |
| PowerPoint | 0.12 s, 52 MB | kreuzberg: 0.10 s, 27 MB |

**Memory.** This library keeps a file's text, not a grid of its cells: the
mixed batch peaks at 111 MB with one worker and 275 MB with 24. calamine
and kreuzberg build each sheet's range of cells, and use 1.6 to 11.7 GB.

**Text.** The other tools give more characters, and none of the difference
is text missed:
- **Excel:** calamine and kreuzberg write a tab for every empty cell in a
  sheet's range, and number formats differ (`1,000.00` against `1000`). The
  characters other than spaces are 66.2 million here against 67.1 million
  for calamine.
- **Word:** kreuzberg repeats a merged cell's text in each row and column
  the merge covers.

**Failures.** Every tool that reads Excel fails on the archive that is not
an Office package; this library says so in its error. Beyond it, docx-rs
panics on 4 Word files, python-docx fails on 2, openpyxl on 2 more
workbooks and markitdown on 6 workbooks.

## The garbage collector

Go's collector runs when the heap has grown by `GOGC` percent since the
last collection; the default is 100. A larger value collects less often,
for more memory. This library leaves the setting to the program that uses
it. Here is the trade on the mixed batch:

| GOGC | One worker | 24 workers |
|---|---|---|
| 100 (default) | 4.31 s, 111 MB | 0.58 s, 275 MB |
| 200 | 4.06 s (6% less), 112 MB | 0.50 s (14% less), 466 MB (1.7 times) |
| 400 | 3.94 s (9% less), 141 MB (1.3 times) | 0.47 s (19% less), 790 MB (2.9 times) |

With many workers the collector has more to do, and a larger `GOGC` saves
more time for more memory. On one of the large generated files, read with
one worker, `GOGC=400` saves 6 to 8%. Set it
with the environment (`GOGC=200`) or `debug.SetGCPercent`.
`debug.SetMemoryLimit` keeps a larger `GOGC` within a bound.

## Where the time goes

A CPU profile of each batch with one worker shows:

| | Word | Excel |
|---|---|---|
| Inflating the parts and checking their CRC (`compress/flate`, `hash/crc32`) | 31% | 27% |
| Scanning the XML (`internal/xmlscan`, without the inflating it calls) | 28% | 28% |
| Allocating and collecting | 12% | 13% |
| The rest: the readers, building the document, writing the text | 29% | 32% |

**Inflating.** This library depends only on the standard library and the
go-fast modules. An experiment outside it measured what a faster
decompressor would give. Every deflated part of the batch was inflated from
memory, on one core, best of 7:

| Decompressor | Word parts (797 MB) | Excel parts (485 MB) |
|---|---|---|
| `compress/flate` (standard library) | 712 MB/s | 683 MB/s |
| klauspost/compress | 905 MB/s (79% of the time) | 879 MB/s (78%) |
| C zlib 1.3.2 | 978 MB/s (73%) | 1,122 MB/s (61%) |

Inflating as fast as klauspost/compress would take about 6% off the CPU
time of reading Word or Excel, and as fast as C zlib 8 to 10%.

**Scanning.** `BenchmarkReal` in `internal/xmlscan` scans the XML parts of
the batch, with the file of parts named by `XMLSCAN_PARTS` (4-byte length,
then the part, for each). It skips itself without one.

## Reproducing

Run from the repository root. The data goes in `../.testdata/bench/ooxml`.

```sh
# The Python environments: the tools, and psutil, polars and warcio for the scripts
#   ../bench-env/.venv-ooxml  (requirements-ooxml.txt)
#   ../bench-env/.venv-text   (requirements-text.txt)
H=../bench-env/.venv-text/bin/python
D=../.testdata/bench/ooxml

# Find the files in Common Crawl's index, then fetch a sample
curl -s https://data.commoncrawl.org/crawl-data/CC-MAIN-2025-43/cc-index-table.paths.gz > $D/../cc-index-table.paths.gz
for p in part-00001 part-00003 part-00004 $(seq -f 'part-%05g' 10 10 220); do
  $H -I bench/batch/cc_find.py $D/../cc-index-table.paths.gz $D/found $p
done
$H -I bench/batch/cc_fetch.py $D/found $D docx=2000,xlsx=1000,pptx=1000 10

# The lists
for e in docx xlsx pptx; do find $PWD/$D/$e -type f | sort > $D/$e.list; done
cat $D/docx.list $D/xlsx.list $D/pptx.list > $D/all.list
python3 -c "import random; p='$D/all.list'; l=open(p).read().split(); random.Random(1).shuffle(l); open(p,'w').write('\n'.join(l)+'\n')"

# The programs
go build -o bench/batch/gooxml/gooxml ./bench/batch/gooxml
(cd bench/batch/rust/rsooxml && CARGO_TARGET_DIR=../target cargo build --release)
(cd bench/batch/rust/rskreuzberg && CARGO_RESOLVER_INCOMPATIBLE_RUST_VERSIONS=fallback CARGO_TARGET_DIR=../target cargo build --release)

# The runs (about 25 minutes, most of it markitdown), then the tables
bench/batch/bench.sh
for b in all docx xlsx pptx; do echo $D/results/$b; done | xargs $H -I bench/batch/report.py
```

`cc_fetch.py` takes the same files again only from the same index files, in
the same order, with the same seed. `rskreuzberg` is built apart from
`rsooxml`: kreuzberg bundles calamine 0.36, which needs zip 8, while
calamine 0.38 needs zip 9. Its lockfile pins zip 8.6.0.
