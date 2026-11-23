#!/bin/bash
# Run tools over a file list, each run measured by measure.py (wall time,
# CPU time, peak resident memory of the process tree).
#
#   run_all.sh LIST RESULTS REPS TOOLS...
#
# TOOLS: go[-gogcN] (gooxml on this checkout, with GOGC=N, default 100),
# rs-calamine, rs-docx-rs, rs-pptx-to-md, rs-kreuzberg, and the tools of
# run_py.py. Each run is repeated REPS times, into RESULTS/<tool>-<workers>-r<rep>/,
# for each worker count in WORKERS (default "1 24").
#
# PY is the Python with the Python tools, MPY one with psutil, RT the Rust
# release directory, GOOXML the gooxml binary (see README.md).
set -u
LIST=$1; R=$2; REPS=$3; shift 3
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/../.." && pwd)
PY=${PY:-$REPO/../bench-env/.venv-ooxml/bin/python}
MPY=${MPY:-$REPO/../bench-env/.venv-text/bin/python}
RT=${RT:-$HERE/rust/target/release}
GOOXML=${GOOXML:-$HERE/gooxml/gooxml}
# No tool may start a thread pool of its own in each worker (onnxruntime in
# markitdown's magika, BLAS): every tool gets the workers it is given.
export OMP_NUM_THREADS=1 OPENBLAS_NUM_THREADS=1 MKL_NUM_THREADS=1
mkdir -p "$R"
for w in ${WORKERS:-1 24}; do
  for t in "$@"; do
    for rep in $(seq 1 "$REPS"); do
      out="$R/$t-$w-r$rep"
      rm -rf "$out"; mkdir -p "$out"
      gogc=100
      case $t in
        go) cmd=("$GOOXML" "$LIST" "$out" "$w");;
        go-gogc*) gogc=${t#go-gogc}; cmd=("$GOOXML" "$LIST" "$out" "$w");;
        rs-kreuzberg) cmd=("$RT/rskreuzberg" kreuzberg "$LIST" "$out" "$w");;
        rs-*) cmd=("$RT/rsooxml" "${t#rs-}" "$LIST" "$out" "$w");;
        *) cmd=("$PY" -I "$HERE/run_py.py" "$t" "$LIST" "$out" "$w");;
      esac
      echo "== $t w=$w rep=$rep load $(cut -d' ' -f1 /proc/loadavg)"
      GOGC=$gogc "$MPY" -I "$HERE/measure.py" "$out/measure.json" -- "${cmd[@]}" 2>&1 | grep -v -i warning | tail -1
      rm -rf "$out/text"   # the length of the text is kept in results.jsonl
    done
  done
done
