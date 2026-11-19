# Dewiride PPTX Studio

Source: https://github.com/Dewiride-Open-Source/Dewiride-PPTX-Studio, commit
`875a825d156812dda5fd7a359599d69fe700c972`. Copyright 2026 Dewiride
Technologies. Licensed under the Apache License, Version 2.0 (`LICENSE` here);
its `NOTICE` file is copied here unchanged, as the licence asks.

What is derived from it: the definitions of 19 of PowerPoint's 74 built-in
table styles in `internal/pml/presetstyles.go`, those whose definition no
PowerPoint-written file of this library's test corpus holds. They come from
`corpus/ground-truth/table-styles.json` (sha256
`4d9a420bc013a41b21b797b0909d257240a422d68852a0228881011324e21613`), in which
that project records each style as PowerPoint serialised it.

How it was modified: `internal/pml/gen/tablestyles.py` keeps, of each
definition, only what this library reads (the colour of text and the fill of
each part of a table), and writes it into Go source. The other 55 styles are
taken from files PowerPoint wrote into the test corpus, and agree with the
capture part for part; the generator checks that they do.
