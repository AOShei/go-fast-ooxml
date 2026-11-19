#!/usr/bin/env python3
"""Generate ../presetstyles.go: PowerPoint's built-in table styles.

  python3 internal/pml/gen/tablestyles.py     # after testdata/fetch.py and testdata/soak.py

A slide's table names its style by an identifier, and PowerPoint's own
styles are built into PowerPoint: a file holds the definition of a style
only once the style has been used in it, and some programs (python-pptx)
name one without defining it. The standard does not publish them. What
PowerPoint wrote into public files does, and that is where they are taken
from: the presentations of the test corpus and the soak set, both fetched
at fixed hashes and commits, so the result is the same wherever it is run.

Only files whose properties say PowerPoint wrote them are read, and of
those only styles named as PowerPoint names its own: "Medium Style 2 -
Accent 1", or the same in another language ("Dunkle Formatvorlage 1 -
Akzent 1"). A style without a name, or named "Table_0", was written by
another program that left PowerPoint's name in the file's properties.
What is compared, and kept, is what the reader takes from a style: for
each part of a table, the colour of its text and its fill. Copies of a
style differ in borders and font references between versions of
PowerPoint; that is left out, as is the name, which PowerPoint writes in
the language of the program. Where copies disagree, the definition most
files have is kept; a tie leaves the style out. Each style's source file
is recorded in the generated code.
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
EXTENSIONS = (".pptx", ".pptm", ".ppsx", ".ppsm", ".potx", ".potm")
STYLE = re.compile(rb'<a:tblStyle\b[^>]*styleId="(\{[0-9A-Fa-f-]{36}\})"[^>]*>.*?</a:tblStyle>', re.S)
NAME = re.compile(rb'\sstyleName="[^"]*"')
STYLE_NAME = re.compile(rb'\sstyleName="([^"]*)"')
ENGLISH = re.compile(r"^(No Style, (No Grid|Table Grid)|(Themed|Light|Medium|Dark) Style \d( - Accent \d(/Accent \d)?)?)$")
LOCALIZED = re.compile(r"^\D+ \d(\s*[-\u2013\u2014]\s*\S.*)?$")


A = "http://schemas.openxmlformats.org/drawingml/2006/main"
PARTS = ("tblBg", "wholeTbl", "band1H", "band2H", "band1V", "band2V", "lastCol", "firstCol", "lastRow", "seCell", "swCell", "firstRow", "neCell", "nwCell")


def projection(gid, body):
    """What the reader takes from a style, as canonical XML; None if it cannot be read."""
    wrapped = b'<a:tblStyleLst xmlns:a="' + A.encode() + b'">' + body + b"</a:tblStyleLst>"
    try:
        style = ET.fromstring(wrapped).find("{%s}tblStyle" % A)
    except ET.ParseError:
        return None
    out = [f'<a:tblStyle styleId="{gid}">']
    for part in PARTS:
        el = style.find("{%s}%s" % (A, part))
        if el is None:
            continue
        inner = []
        tx = el.find("{%s}tcTxStyle" % A)
        if tx is not None:
            colour = [c for c in tx if c.tag.split("}")[1] not in ("fontRef", "font")]
            if colour:
                inner.append("<a:tcTxStyle>" + "".join(xml(c) for c in colour) + "</a:tcTxStyle>")
        tc = el.find("{%s}tcStyle" % A)
        fill = None
        if tc is not None:
            fill = tc.find("{%s}fill" % A)
            ref = tc.find("{%s}fillRef" % A)
            if fill is not None:
                inner.append("<a:tcStyle>" + xml(fill) + "</a:tcStyle>")
            elif ref is not None:
                inner.append("<a:tcStyle>" + xml(ref) + "</a:tcStyle>")
        if part == "tblBg":
            for child in el:
                if child.tag.split("}")[1] in ("fill", "fillRef"):
                    inner.append(xml(child))
        if inner:
            out.append(f"<a:{part}>" + "".join(inner) + f"</a:{part}>")
    out.append("</a:tblStyle>")
    return "".join(out)


def xml(el):
    """An element as a: prefixed XML, attributes in order of name."""
    tag = el.tag.split("}")[1]
    attrs = "".join(f' {k}="{v}"' for k, v in sorted(el.attrib.items()))
    inner = "".join(xml(c) for c in el)
    return f"<a:{tag}{attrs}/>" if not inner else f"<a:{tag}{attrs}>{inner}</a:{tag}>"


def written_by_powerpoint(z):
    try:
        app = z.read("docProps/app.xml")
    except (KeyError, zipfile.BadZipFile, EOFError, zlib.error):
        return False
    m = re.search(rb"<Application>([^<]*)</Application>", app)
    return bool(m) and b"PowerPoint" in m.group(1)


def main():
    if not os.path.isdir(CORPUS):
        sys.exit("the corpus is needed: python3 testdata/fetch.py and python3 testdata/soak.py")
    found = collections.defaultdict(collections.Counter)  # id -> definition -> files
    source = {}
    for base, dirs, names in os.walk(CORPUS):
        dirs[:] = sorted(d for d in dirs if d != ".git")
        for n in sorted(names):
            if not n.lower().endswith(EXTENSIONS):
                continue
            path = os.path.join(base, n)
            try:
                z = zipfile.ZipFile(path)
                if "ppt/tableStyles.xml" not in z.namelist() or not written_by_powerpoint(z):
                    continue
                data = z.read("ppt/tableStyles.xml")
            except (zipfile.BadZipFile, OSError, KeyError, EOFError, zlib.error, ValueError, NotImplementedError):
                continue
            for m in STYLE.finditer(data):
                gid = m.group(1).decode().upper()
                named = STYLE_NAME.search(m.group(0))
                name = named.group(1).decode("utf-8", "replace") if named else ""
                if not (ENGLISH.match(name) or LOCALIZED.match(name)):
                    continue
                key = projection(gid, re.sub(rb">\s+<", b"><", m.group(0)))
                if key is None:
                    continue
                found[gid][key] += 1
                source.setdefault((gid, key), os.path.relpath(path, ROOT))
    out = [
        "// Code generated by gen/tablestyles.py from table styles PowerPoint wrote",
        "// into the presentations of the test corpus; DO NOT EDIT.",
        "",
        "package pml",
        "",
        "// presetTableStyles are PowerPoint's built-in table styles, by identifier:",
        "// for each part of a table, the colour of its text and its fill, as",
        "// PowerPoint writes them into a file that uses the style.",
        "var presetTableStyles = map[string]string{",
    ]
    for gid in sorted(found):
        definitions = found[gid]
        ranked = sorted(definitions.items(), key=lambda kv: (-kv[1], source[(gid, kv[0])]))
        (key, n), *others = ranked
        if others and others[0][1] == n:
            print(f"{gid}: definitions disagree and none is in more files than another; left out")
            continue
        if others:
            print(f"{gid}: {len(others) + 1} definitions; kept the one {n} of {sum(definitions.values())} files have")
        out.append(f"\t// {source[(gid, key)]}")
        out.append(f"\t\"{gid}\": {go_string(key)},")
    out.append("}")
    with open(os.path.join(HERE, "..", "presetstyles.go"), "w", encoding="utf-8") as f:
        f.write("\n".join(out) + "\n")
    subprocess.run(["gofmt", "-w", os.path.join(HERE, "..", "presetstyles.go")], check=True)
    print(f"presetstyles.go written: {len(found)} table styles")


def go_string(s):
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


if __name__ == "__main__":
    main()
