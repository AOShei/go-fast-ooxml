#!/bin/bash
# The whole comparison, for each worker count in WORKERS (default "1 24"):
# the mixed batch with every tool that reads all three formats, then each
# format with its own tools. DATA is the directory of the lists (default
# .testdata/bench/ooxml beside this checkout), RESULTS where the runs go.
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
D=${DATA:-$(cd "$HERE/../../.." && pwd)/.testdata/bench/ooxml}
R=${RESULTS:-$D/results}
run() { "$HERE/run_all.sh" "$@"; }
run "$D/all.list" "$R/all" 3 go go-gogc200 go-gogc400 rs-kreuzberg
run "$D/all.list" "$R/all" 1 markitdown
run "$D/docx.list" "$R/docx" 3 go rs-docx-rs rs-kreuzberg
run "$D/docx.list" "$R/docx" 1 python-docx docx2txt
run "$D/xlsx.list" "$R/xlsx" 3 go rs-calamine rs-kreuzberg
run "$D/xlsx.list" "$R/xlsx" 1 openpyxl
run "$D/pptx.list" "$R/pptx" 3 go rs-pptx-to-md rs-kreuzberg
run "$D/pptx.list" "$R/pptx" 1 python-pptx
