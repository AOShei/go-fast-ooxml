# Batch results

Written by `report.py` from the runs of `bench.sh`, 2026-11-23, on this checkout (go-fast-ooxml after PR #44). The method is in [`README.md`](README.md).

- **Wall** is the fastest of the repetitions, and **CPU** that run's user and system time. **Peak memory** is the median of the repetitions' peaks.
- **Failed** counts the files that raised an error or panicked.
- **Text** is the characters each tool gave. The tools write different things: the tab of an empty cell, a merged cell's text once or for each cell it covers.
- **Slowest file** is the longest single call.
- "this library v0.2.0" is the release before this work, for comparison.


### All 1,980 files, mixed

| Tool | Workers | Wall | CPU | Peak memory | Files | Failed | Text (M chars) | Slowest file |
|---|---|---|---|---|---|---|---|---|
| this library, GOGC=400 | 1 | 3.94 s | 4.17 s | 141 MB | 1980 | 1 | 101.8 | 0.17 s |
| this library, GOGC=200 | 1 | 4.06 s | 4.46 s | 112 MB | 1980 | 1 | 101.8 | 0.17 s |
| **this library** | 1 | 4.31 s | 5.10 s | 111 MB | 1980 | 1 | 101.8 | 0.17 s |
| this library v0.2.0 | 1 | 4.66 s | 5.70 s | 105 MB | 1980 | 1 | 101.8 | 0.19 s |
| kreuzberg | 1 | 11.31 s | 11.25 s | 2,900 MB | 1980 | 1 | 306.7 | 1.51 s |
| markitdown | 1 | 808.64 s | 805.75 s | 7,081 MB | 1980 | 6 | 125.6 | 60.00 s |
| this library, GOGC=400 | 24 | 0.47 s | 7.02 s | 790 MB | 1980 | 1 | 101.8 | 0.20 s |
| this library, GOGC=200 | 24 | 0.50 s | 7.51 s | 466 MB | 1980 | 1 | 101.8 | 0.23 s |
| **this library** | 24 | 0.58 s | 8.30 s | 275 MB | 1980 | 1 | 101.8 | 0.23 s |
| this library v0.2.0 | 24 | 0.69 s | 9.37 s | 293 MB | 1980 | 1 | 101.8 | 0.27 s |
| kreuzberg | 24 | 3.81 s | 22.86 s | 11,668 MB | 1980 | 1 | 306.7 | 3.32 s |
| markitdown | 24 | 114.54 s | 1102.17 s | 26,318 MB | 1980 | 6 | 125.6 | 60.00 s |

### Word: 1,411 files

| Tool | Workers | Wall | CPU | Peak memory | Files | Failed | Text (M chars) | Slowest file |
|---|---|---|---|---|---|---|---|---|
| **this library** | 1 | 1.69 s | 1.91 s | 35 MB | 1411 | 0 | 22.8 | 0.06 s |
| kreuzberg | 1 | 1.75 s | 1.75 s | 116 MB | 1411 | 0 | 25.1 | 0.18 s |
| docx-rs | 1 | 4.07 s | 4.06 s | 233 MB | 1411 | 4 | 22.5 | 0.33 s |
| docx2txt | 1 | 13.00 s | 12.94 s | 393 MB | 1411 | 0 | 23.1 | 1.46 s |
| python-docx | 1 | 16.42 s | 16.37 s | 1,094 MB | 1411 | 2 | 24.4 | 1.03 s |
| **this library** | 24 | 0.28 s | 3.29 s | 129 MB | 1411 | 0 | 22.8 | 0.15 s |
| kreuzberg | 24 | 0.37 s | 2.97 s | 433 MB | 1411 | 0 | 25.1 | 0.25 s |
| docx-rs | 24 | 0.79 s | 6.54 s | 966 MB | 1411 | 4 | 22.5 | 0.50 s |
| python-docx | 24 | 2.27 s | 25.90 s | 3,754 MB | 1411 | 2 | 24.4 | 1.44 s |
| docx2txt | 24 | 2.84 s | 20.54 s | 1,347 MB | 1411 | 0 | 23.1 | 1.77 s |

### Excel: 499 files

| Tool | Workers | Wall | CPU | Peak memory | Files | Failed | Text (M chars) | Slowest file |
|---|---|---|---|---|---|---|---|---|
| **this library** | 1 | 2.44 s | 3.00 s | 105 MB | 499 | 1 | 78.6 | 0.17 s |
| calamine | 1 | 4.30 s | 4.28 s | 1,622 MB | 499 | 1 | 280.3 | 0.63 s |
| kreuzberg | 1 | 10.31 s | 10.28 s | 2,893 MB | 499 | 1 | 281.2 | 1.68 s |
| openpyxl | 1 | 32.86 s | 32.76 s | 311 MB | 499 | 3 | 327.7 | 2.63 s |
| **this library** | 24 | 0.40 s | 4.56 s | 298 MB | 499 | 1 | 78.6 | 0.24 s |
| calamine | 24 | 1.16 s | 6.92 s | 6,634 MB | 499 | 1 | 280.3 | 1.05 s |
| kreuzberg | 24 | 3.35 s | 18.36 s | 11,738 MB | 499 | 1 | 281.2 | 3.00 s |
| openpyxl | 24 | 4.07 s | 46.07 s | 2,249 MB | 499 | 3 | 327.7 | 3.13 s |

### PowerPoint: 70 files

| Tool | Workers | Wall | CPU | Peak memory | Files | Failed | Text (M chars) | Slowest file |
|---|---|---|---|---|---|---|---|---|
| kreuzberg | 1 | 0.10 s | 0.10 s | 27 MB | 70 | 0 | 0.4 | 0.02 s |
| **this library** | 1 | 0.12 s | 0.14 s | 52 MB | 70 | 0 | 0.4 | 0.01 s |
| pptx-to-md | 1 | 0.17 s | 0.17 s | 12 MB | 70 | 0 | 0.5 | 0.02 s |
| python-pptx | 1 | 0.65 s | 0.65 s | 224 MB | 70 | 0 | 0.4 | 0.05 s |
| **this library** | 24 | 0.03 s | 0.25 s | 176 MB | 70 | 0 | 0.4 | 0.02 s |
| pptx-to-md | 24 | 0.04 s | 0.29 s | 73 MB | 70 | 0 | 0.5 | 0.02 s |
| kreuzberg | 24 | 0.04 s | 0.28 s | 137 MB | 70 | 0 | 0.4 | 0.02 s |
| python-pptx | 24 | 0.21 s | 2.98 s | 1,496 MB | 70 | 0 | 0.4 | 0.07 s |
