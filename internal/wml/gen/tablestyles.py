#!/usr/bin/env python3
"""Generate ../presetstyles.go: Word's built-in table styles.

  python3 internal/wml/gen/tablestyles.py     # after testdata/fetch.py and testdata/soak.py

A table names its style by an identifier. Word writes the definition of a
built-in style into a document once the style is used there, but not
every program does, and the standard does not publish Word's styles. What
Word wrote into public files does: the documents of the test corpus and
the soak set, fetched at fixed hashes and commits.

Only documents whose properties say Word wrote them are read, and of those
only table styles with a name Word gives its own ("Grid Table 4 - Accent
1", "Table Grid"): Word writes the names of its own styles in English,
whatever the language of the program, and the identifier in that language
("Tabellengitternetz"). Definitions are compared by name, without their
identifiers; where copies disagree, the one most documents have is kept,
and a tie stops the generator. Every identifier seen for a name is
recorded, so that a document that names a style by a German identifier
finds it.

What is compared, and kept, is what the reader takes from a table style:
the shading of the whole table and of each of its parts, the size of its
bands, and the style it is based on. Copies of a style differ in fonts,
borders and spacing from one version of Word to another; that is left
out.
"""
import xml.etree.ElementTree as ET
import collections
import os
import re
import subprocess
import sys
import zipfile
import zlib

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", ".."))
CORPUS = os.path.join(ROOT, "testdata", "corpus")
EXTENSIONS = (".docx", ".docm", ".dotx", ".dotm")
STYLE = re.compile(rb'<w:style\b[^>]*w:type="table"[^>]*>.*?</w:style>', re.S)
ID = re.compile(rb'\sw:styleId="([^"]*)"')
NAME = re.compile(rb'<w:name w:val="([^"]*)"')
# Word's own table styles, by their English names.
BUILT_IN = re.compile(
    r"^(Table Grid|Table Normal|Normal Table|Table Grid Light|Table Theme|Plain Table [1-5]"
    r"|(Grid|List) Table [1-7]( Light| Colorful)?( - Accent [1-6])?"
    r"|(Light|Medium|Dark|Colorful) (Shading|List|Grid)( [1-3])?( - Accent [1-6])?"
    r"|Table (Simple|Classic|Colorful|Columns|Grid|List|3D effects|Contemporary|Elegant|Professional|Subtle|Web) [1-8])$")
W = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
SHADE = ("val", "color", "themeColor", "themeTint", "themeShade", "fill", "themeFill", "themeFillTint", "themeFillShade")


def q(tag):
    return "{%s}%s" % (W, tag)


def projection(body, ids):
    """What the reader takes from a style, as canonical XML; "" if nothing."""
    wrapped = b'<w:styles xmlns:w="' + W.encode() + b'">' + body + b"</w:styles>"
    try:
        style = ET.fromstring(wrapped).find(q("style"))
    except ET.ParseError:
        return None
    parts = []
    based = style.find(q("basedOn"))
    if based is not None:
        parent = ids.get(based.get(q("val")), based.get(q("val")))
        parts.append(f'<w:basedOn w:val="{esc(parent)}"/>')
    tbl = style.find(q("tblPr"))
    bands = []
    for tag in ("tblStyleRowBandSize", "tblStyleColBandSize"):
        el = tbl.find(q(tag)) if tbl is not None else None
        if el is not None:
            bands.append(f'<w:{tag} w:val="{esc(el.get(q("val"), ""))}"/>')
    shd = tbl.find(q("shd")) if tbl is not None else None
    tbl_shd = shade(shd)
    parts.append("<w:tblPr>" + "".join(bands) + tbl_shd + "</w:tblPr>")
    tc = style.find(q("tcPr"))
    if tc is not None and tc.find(q("shd")) is not None:
        parts.append("<w:tcPr>" + shade(tc.find(q("shd"))) + "</w:tcPr>")
    for pr in style.findall(q("tblStylePr")):
        tc = pr.find(q("tcPr"))
        if tc is not None and tc.find(q("shd")) is not None:
            parts.append(f'<w:tblStylePr w:type="{esc(pr.get(q("type"), ""))}"><w:tcPr>' + shade(tc.find(q("shd"))) + "</w:tcPr></w:tblStylePr>")
    return "".join(parts)


def shade(el):
    if el is None:
        return ""
    attrs = "".join(f' w:{a}="{esc(el.get(q(a)))}"' for a in SHADE if el.get(q(a)) is not None)
    return f"<w:shd{attrs}/>"


def esc(v):
    return v.replace("&", "&amp;").replace('"', "&quot;").replace("<", "&lt;")


def written_by_word(z):
    try:
        app = z.read("docProps/app.xml")
    except (KeyError, zipfile.BadZipFile, EOFError, zlib.error):
        return False
    m = re.search(rb"<Application>([^<]*)</Application>", app)
    return bool(m) and b"Microsoft" in m.group(1) and b"Word" in m.group(1)


def main():
    if not os.path.isdir(CORPUS):
        sys.exit("the corpus is needed: python3 testdata/fetch.py and python3 testdata/soak.py")
    found = collections.defaultdict(collections.Counter)  # name -> definition -> documents
    source, ids = {}, {}
    styles = []  # (path, identifier, name, body), read first: a style's parent is named by identifier
    for base, dirs, names in os.walk(CORPUS):
        dirs[:] = sorted(d for d in dirs if d != ".git")
        for n in sorted(names):
            if not n.lower().endswith(EXTENSIONS):
                continue
            path = os.path.join(base, n)
            try:
                z = zipfile.ZipFile(path)
                if "word/styles.xml" not in z.namelist() or not written_by_word(z):
                    continue
                data = z.read("word/styles.xml")
            except (zipfile.BadZipFile, OSError, KeyError, EOFError, zlib.error, ValueError, NotImplementedError):
                continue
            local = {}
            found_here = []
            for m in STYLE.finditer(data):
                body = m.group(0)
                named, sid = NAME.search(body), ID.search(body)
                if not named or not sid:
                    continue
                name = named.group(1).decode("utf-8", "replace")
                if not BUILT_IN.match(name):
                    continue
                local[sid.group(1).decode("utf-8", "replace")] = name
                found_here.append((name, body))
            for sid, name in local.items():
                ids.setdefault(sid, name)
            for name, body in found_here:
                key = projection(body, local)
                if key is None:
                    continue
                found[name][key] += 1
                source.setdefault((name, key), os.path.relpath(path, ROOT))
    out = [
        "// Code generated by gen/tablestyles.py from table styles Word wrote into the",
        "// documents of the test corpus; DO NOT EDIT.",
        "",
        "package wml",
        "",
        "// presetTableStyles are Word's built-in table styles, by name: the content",
        "// of w:style, as far as the reader takes it (the shading of the table and",
        "// of its parts, its bands, the style it is based on, by name).",
        "var presetTableStyles = map[string]string{",
    ]
    for name in sorted(found):
        ranked = sorted(found[name].items(), key=lambda kv: (-kv[1], source[(name, kv[0])]))
        (key, n), *others = ranked
        if others and others[0][1] == n:
            print(f"{name}: definitions disagree and none is in more documents than another; left out")
            continue
        if others:
            print(f"{name}: {len(others) + 1} definitions; kept the one {n} of {sum(found[name].values())} documents have")
        out.append(f"\t// {source[(name, key)]}")
        out.append(f"\t{go_string(name)}: {go_string(key)},")
    out.append("}")
    out.append("")
    out.append("// presetTableIDs names the built-in table style an identifier stands for,")
    out.append("// in English or in the language of the Word that wrote it.")
    out.append("var presetTableIDs = map[string]string{")
    for sid in sorted(ids):
        out.append(f"\t{go_string(sid)}: {go_string(ids[sid])},")
    out.append("}")
    path = os.path.join(HERE, "..", "presetstyles.go")
    with open(path, "w", encoding="utf-8") as f:
        f.write("\n".join(out) + "\n")
    subprocess.run(["gofmt", "-w", path], check=True)
    print(f"presetstyles.go written: {len(found)} table styles, {len(ids)} identifiers")


def go_string(s):
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


if __name__ == "__main__":
    main()
