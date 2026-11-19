#!/usr/bin/env python3
"""Generate tables.go: what each code of the symbol fonts is in Unicode.

  python3 internal/symbol/generate.py     # needs curl and pdftotext (poppler)

Sources, both from the Unicode Consortium, fetched and checked by hash:

- Symbol: "Adobe Symbol Encoding to Unicode", table version 1.0.
- Wingdings, Wingdings 2, Wingdings 3, Webdings: "Status of encoding of
  Wingdings and Webdings Symbols" (WG2 N4384, L2/12-368, 2012), whose
  section 6 gives every character of the four fonts and the code point it
  was unified with or encoded as (Unicode 7.0).

Every code point taken from N4384 is checked against the name Python's
unicodedata gives it. The document was written before the final names were
settled, so a name may differ; the known differences are listed below and
were checked by hand. A new difference stops the generator.
"""
import hashlib
import os
import re
import subprocess
import sys
import tempfile
import unicodedata

HERE = os.path.dirname(os.path.abspath(__file__))

SYMBOL = ("https://www.unicode.org/Public/MAPPINGS/VENDORS/ADOBE/symbol.txt",
          "deb78ca840a429311939b9d165890873f71fb23ef223ceeb144a6c6d641a7e52")
N4384 = ("https://www.unicode.org/wg2/docs/n4384.pdf",
         "15839e133b3ab10b84d68f41f25138d9dedbc94a8a6c2dbb155439d597084a0b")

# Names in N4384 that differ from the final ones, checked by hand: the
# document's name, then the character's name in Unicode.
RENAMED = {
    "0066": "HOUSE BUILDINGS", "0082": "RAILWAY TRACK", "0087": "RIGHT SPEAKER",
    "0114": "CANCELLATION X", "0120": "CIRCLED REVERSE SOLIDUS", "0130": "BOYS SYMBOL",
    "0131": "GIRLS SYMBOL", "0142": "ONCOMING AUTOMOBILE", "0151": "SPEAKING HEAD IN SILHOUETTE",
    "0173": "FRAME WITH PICTURE", "0216": "WHITE SUN BEHIND CLOUD WITH RAIN",
    "0232": "CIRCLED LATIN CAPITAL LETTER P", "0236": "GRADUATION CAP", "1038": "BOOK",
    "1120": "X IN A RECTANGLE BOX", "2115": "CIRCLED NUMBER TEN",
    "3075": "DOWNWARDS TRIANGLE-HEADED PAIRED ARROWS",
    "3089": "UPWARDS WHITE ARROW FROM BAR WITH HORIZONTAL BAR",
}

FONTS = ["Webdings", "Wingdings", "Wingdings 2", "Wingdings 3"]  # N4384's sets 0 to 3


def fetch(url, digest, dest):
    subprocess.run(["curl", "-sSfL", "--max-time", "120", "-o", dest, url], check=True)
    got = hashlib.sha256(open(dest, "rb").read()).hexdigest()
    if got != digest:
        sys.exit(f"{url}: sha256 {got}, want {digest}")


def symbol(path):
    table = [0] * 224
    for line in open(path, encoding="latin-1"):
        if line.startswith("#") or not line.strip():
            continue
        uni, code = line.split("\t")[:2]
        c = int(code, 16)
        if 0x20 <= c <= 0xFF and table[c - 0x20] == 0:
            table[c - 0x20] = int(uni, 16)
    return table


def wingdings(path):
    text = subprocess.run(["pdftotext", "-raw", path, "-"], capture_output=True, check=True, text=True).stdout
    lines = text.split("\n")
    start = next(i for i, l in enumerate(lines) if "6. Detailed tables" in l)
    recs, last = [], -1
    for l in lines[start:]:
        t = l.replace("\f", "").strip()
        m = re.match(r"^(\d{4})(?:\s+(.*))?$", t)
        # The IDs come in increasing order; a code point that is all digits
        # (2042) is not taken for one.
        if m and int(m.group(1)) > last and 33 <= int(m.group(1)) % 1000 <= 255 and int(m.group(1)) // 1000 <= 3:
            recs.append([m.group(1), [m.group(2) or ""]])
            last = int(m.group(1))
        elif recs:
            recs[-1][1].append(t)
    tables = [[0] * 224 for _ in FONTS]
    for gid, body in recs:
        cp, name = None, ""
        for t in body:
            if t.startswith(("→", "=")):
                continue  # a reference to another character, or an alias
            t = re.sub(r"^[-]\s*", "", t)  # the font's own glyph
            m = re.match(r"^([0-9A-F]{4,5})\b\s*(.*)$", t)
            if m and cp is None:
                cp, name = int(m.group(1), 16), m.group(2)
            elif cp is not None and not name and re.match(r"^[A-Z][A-Z0-9 \-]{2,}", t):
                name = t
        if cp is None:
            continue  # w-1255: not encoded
        real = unicodedata.name(chr(cp))  # raises if the code point is unassigned
        name = re.split(r" (?:GLYPH|SEE|ALL|THIS|PREFERRED|UNIFIED|UCS|=|\*|→)", re.sub(r"\s+", " ", name.upper()))[0].strip()
        if gid in RENAMED:
            if RENAMED[gid] != real:
                sys.exit(f"w-{gid}: U+{cp:04X} is {real}, expected {RENAMED[gid]}")
        elif not name or not (real.startswith(name) or name.startswith(real)):
            if name:  # a few rows have only a glyph; their code points were checked by hand
                sys.exit(f"w-{gid}: U+{cp:04X} is {real}, the document says {name}")
        tables[int(gid) // 1000][int(gid) % 1000 - 0x20] = cp
    for t in tables:
        t[0] = 0x20  # the space is a space in every font
    return tables


def emit(out, name, comment, table):
    out.append(f"// {comment}")
    out.append(f"var {name} = [224]rune{{")
    for i in range(0, 224, 8):
        out.append("\t" + " ".join(f"0x{c:04X}," for c in table[i:i + 8]))
    out.append("}")
    out.append("")


def main():
    with tempfile.TemporaryDirectory() as tmp:
        sym, n4384 = os.path.join(tmp, "symbol.txt"), os.path.join(tmp, "n4384.pdf")
        fetch(*SYMBOL, sym)
        fetch(*N4384, n4384)
        s, w = symbol(sym), wingdings(n4384)
    out = [
        "// Code generated by generate.py from the Unicode Consortium's \"Adobe Symbol",
        "// Encoding to Unicode\" (table version 1.0) and \"Status of encoding of",
        "// Wingdings and Webdings Symbols\" (WG2 N4384); DO NOT EDIT. See NOTICE.",
        "",
        "package symbol",
        "",
    ]
    emit(out, "symbolFont", "symbolFont maps the codes 0x20 to 0xFF of Symbol; zero means none.", s)
    for name, font, table in zip(("webdings", "wingdings", "wingdings2", "wingdings3"), FONTS, w):
        emit(out, name, f"{name} maps the codes 0x20 to 0xFF of {font}; zero means none.", table)
    with open(os.path.join(HERE, "tables.go"), "w", encoding="utf-8") as f:
        f.write("\n".join(out))
    print("tables.go written:", sum(1 for t in [s] + w for c in t if c), "codes")


if __name__ == "__main__":
    main()
