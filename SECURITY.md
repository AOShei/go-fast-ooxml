# Security

This library is written to read files from people who are not trusted: an
upload, an attachment, a crawl. This page says what it promises about such
files, what it leaves to the caller, and how to report a problem.

## Reporting a vulnerability

Use GitHub's private reporting for this repository (the **Security** tab,
**Report a vulnerability**). Please do not open a public issue for something
that lets a file crash the library, exhaust memory, or read outside the
input. A file that
reproduces the problem is the most useful thing to send.

Any of the following is a bug to report, whatever the file:

- a panic, a stack overflow, or a call that does not return;
- memory or time beyond what the limits below allow;
- a result that is neither a valid document nor one of the shared errors;
- output that differs with the number of workers;
- text the file declares hidden or drawn with no ink that comes out without
  that reason, where the README lists the declaration for the format.

## What a file cannot do

| | |
|---|---|
| **Run anything** | Macros are never run, formulas never evaluated, fields never updated. Their presence is reported (`input.active_content`); the code of VBA projects and macro sheets, and the codes of fields, are read as text and marked as stored data |
| **Reach anything** | No file other than the input is opened and no network connection is made. Links to other files, remote templates and linked pictures are reported (`input.external_reference`), never followed. The packages that read file content do not import `os`, `net` or `os/exec`, and a test fails if one starts to |
| **Use XML against the reader** | The library has its own XML reader. Document type declarations are refused, so there are no entities to expand and nothing external to include. Nesting is bounded at 512 elements |
| **Crash the caller** | No panic on any input: every index, length and count taken from a file is checked, and every list a file can lengthen has a bound |
| **Write what it likes into the output** | Text is valid UTF-8 with control characters removed. Names taken from the file (parts, sheets, authors, error messages) are cleaned and cut to a length before they appear in a warning or a field |
| **Lose what it declares about display** | Text a file declares hidden or drawn with no ink, in the ways the README lists, keeps that reason. Nothing is judged from appearance, so text can be out of sight with no reason; see below |
| **Be read two ways unnoticed** | An archive that names a member twice, or whose directory cannot be read so that its members are found by their own headers, says so (`input.ambiguous`, `input.repaired`) |

## Limits

Every cost a file can impose is bounded by a limit with a default. A limit
that is reached either refuses the file (`ErrLimitExceeded`) or cuts the
document there with a `limit.exceeded` warning; which, is in the table.

| Limit | Default | Where | When reached |
|---|---|---|---|
| Size of the file | 1 GiB | `Options.Limits.MaxInputBytes` | Refused before anything is read |
| Bytes decompressed, by the document and the files embedded in it together | 2 GiB | `Options.Limits.MaxDecodedBytes` | The part being read is cut off; a file whose own structure needs more is refused; embedded files after that are listed, not read |
| Text the document holds, with the files embedded in it | 256 MiB | `Options.Limits.MaxTextBytes` | The part being read is cut off; embedded files after that are listed, not read |
| Members of the archive | 100,000 | `Extractor.MaxArchiveMembers` | Refused |
| Cells kept from a workbook, or from the tables of a presentation | 20 million | `Extractor.MaxCells` | The sheet or slide is cut off |
| Rounds of hashing to try a password | 1 million | `Extractor.MaxSpinCount` | Refused |
| Password checks of a document and the files embedded in it, together | 10 times the above | | Further encrypted files are listed, not opened |
| Depth of embedded documents | 4 | `Options.Limits.MaxDepth` | Listed, not opened |
| Nesting of XML elements | 512 | fixed | The part is cut off |
| A record of a binary workbook (`.xlsb`) | 16 MiB, read as its bytes arrive, not all at once from its stated size | fixed | The part is cut off |
| A formula of a binary workbook turned back into text | 32 KiB of text, 4,096 operands waiting for their operator | fixed | The formula is not read back, and is counted in `ooxml.unread_content` |
| Code of a VBA project, decompressed | what is left of the text and decompression budgets | | The module is cut off; what it decompresses to is charged to the decompression budget too |
| Data after the end of the archive | 1 MiB read, and only what follows within the archive's last 64 KiB is found | fixed | The rest is not read; `input.trailing_data` says how many bytes there are |

A zero in a limit means its default and a negative number means none.

A document can embed thousands of files. The limits on decompressed
bytes, on text and on password checks are therefore the document's and
its embedded files' together: an embedded file has what the document has
left, and what it uses is gone for the next. This holds across the go-fast
libraries, for a caller whose child extractor passes its context on.

There is no test of compression ratio: Office files compress a hundred to
one and more, and a ratio that stops bombs stops real files. The budget of
decompressed bytes is what stops a bomb, and it is counted as bytes are
read, so a bomb costs time up to the budget and little memory.

### What the defaults still allow

The defaults are set so that no real file is refused. For files from
strangers they allow more than most services want to spend:

- **Time.** Decompressing up to the budget takes a few seconds per
  gigabyte. Pass a context with a deadline: every reader stops when it is
  cancelled, including a password check in progress.
- **Output size.** A small file can say a long text many times: one
  shared string that thousands of cells refer to, the text of a master
  that every slide repeats. What is repeated is stored once, so the limit
  on decompressed bytes does not see it; the worst case in the corpus is
  110 KB that would become 2 GiB of cell text. `MaxTextBytes` is what
  bounds it: the text of blocks and table cells, counted as often as it
  occurs, in a document and the files embedded in it together. The
  targets of links and the alternative text of pictures count in the same
  way: one address, stored once, may be linked to millions of times. So do
  blocks and cells themselves, 128 bytes a block and 16 a cell, empty or
  not: a picture placed ten thousand times, or a grid of empty cells, is
  small in the file and large in the result. Every
  other string that comes from the file (a name, a list label, an author)
  is held to a fixed length, 512 bytes or less. The
  default of 256 MiB is some ten times the text of a workbook of two
  million cells; a service that expects documents of a few megabytes can
  set it far lower and lose nothing.
- **Memory.** A document is held in memory whole by `Extract`. `File.Parts`
  and `File.Rows` hold a part or a band of rows at a time.
- **Encrypted files.** Trying a password costs what the file says it
  costs, up to `MaxSpinCount`: about a hundredth of a second at what Office
  writes, about a tenth at the default limit.

## What a file declares about display

Documents are fed to language models, and text a human reviewer cannot see
is one way to smuggle instructions into one. This library does not judge
that: it extracts everything, and where the file itself declares text not
shown (hidden formatting, a hidden sheet, row, column, slide or shape) or
drawn with no ink, the block says so (`Block.Display`, spans, table
regions). The caller can leave such text out (`RenderOptions.ExcludeDisplay`).

Nothing is judged from appearance: not colour against the background, not
size, not position, not what covers what. **A block with no reason says
nothing about whether a reader can see it, or whether it is harmful.**
Threat detection is outside this library; the README's *What the file
declares* section lists, per format, exactly what is reported. Characters
that display as nothing are kept in the text as they are, for whatever
checks the caller runs.

Content that is never on a page but is extracted by design (properties,
comments, speaker's notes, alternative text, what the file stores, data
after the end of the archive) has its own place in the model. A consumer
that feeds a model decides what to do with each.

## What is not promised

- **Fidelity to what Office displays.** The library does no page layout
  and evaluates no formula. It reports stored values.
- **Secrecy of a password in memory.** The password and the key derived
  from it are ordinary Go values and are not wiped.
- **Authenticity.** Digital signatures are not verified. For encrypted
  files the integrity value is checked and a mismatch reported
  (`ooxml.integrity_mismatch`), which shows damage or tampering by someone
  without the password and nothing more.
- **Denial of service within the limits.** A file may cost what the limits
  allow. Set them for the service.

## How this is tested

- Fuzz targets for the XML reader, the archive and the recovery of one
  cut short, each format's main part, the records and formulas of binary
  workbooks, number formats, the compound-file container and the
  encryption information; all run in CI on every pull request.
- A soak run over 6,598 files: the corpus and the Office files in the test
  suites of LibreOffice, Apache Tika, pandoc and ten other projects,
  among them their collections of crash cases and fuzzer findings. Every
  file gives a valid document or a shared error, the same for any number
  of workers and a part at a time (`bench/soak.json`).
- Files cut short at hundreds of places, and an archive's members found
  by their headers in every form a member's length is written in.
- Worst-case tests for each format: bombs, deep nesting, huge counts,
  quadratic shapes of input, each held to a time and a memory ceiling.
- `go test -race` on Linux, macOS and Windows.
- The only dependency is `go-fast-core`; both use the standard library and
  nothing else, and CI fails if that changes.
