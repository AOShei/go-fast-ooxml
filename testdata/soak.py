#!/usr/bin/env python3
"""Fetch the soak set: the Office files in other projects' test suites.

  python3 testdata/soak.py            # fetch into testdata/corpus/soak
  python3 testdata/soak.py --list     # print what is there

The corpus (testdata/corpus.json) is a few hundred files, each recorded and
measured. The soak set is for volume: thousands of files that other
programs keep because something once went wrong with them. Nothing is
asserted about any one of them; every one must be survived, and the run
(go test -run TestSoak) counts what happens.

Each source is a public repository at a pinned commit, so the set is the
same wherever it is fetched. Only files with an Office extension are
downloaded (a partial clone with a sparse checkout), and nothing is
committed here. The files stay under their projects' licences.
"""
import os
import subprocess
import sys

ROOT = os.path.dirname(os.path.abspath(__file__))
DEST = os.path.join(ROOT, "corpus", "soak")

EXTENSIONS = ("docx", "docm", "dotx", "dotm", "xlsx", "xlsm", "xltx", "xltm", "xlsb", "pptx", "pptm", "ppsx", "ppsm", "potx", "potm")

# (directory, repository, commit, licence[, extensions]). A source with
# extensions of its own gives only files with those: Apache POI's other
# Office files are in the corpus already (fetch.py), its .xlsb files not.
SOURCES = [
    ("libreoffice", "https://github.com/LibreOffice/core.git", "970e4f2194616485e9585b370f965cb61a34e096", "MPL-2.0"),
    ("pandoc", "https://github.com/jgm/pandoc.git", "f5d266050e4e149c7c9a699cfbe45aff64bb9f7c", "GPL-2.0-or-later"),
    ("tika", "https://github.com/apache/tika.git", "6ef8f501b1610fcb36d275332d2337e3113de74d", "Apache-2.0"),
    ("phpspreadsheet", "https://github.com/PHPOffice/PhpSpreadsheet.git", "bec63df430ac79e0cf643928800c8fa19742b9ef", "MIT"),
    ("phpword", "https://github.com/PHPOffice/PHPWord.git", "63339365b9b0c9e3d99b16e8107fc9779c3e0d21", "LGPL-3.0"),
    ("mammoth", "https://github.com/mwilliamson/mammoth.js.git", "5171a9072f8b7343ab1c9ed3a15d6d372cb59b67", "BSD-2-Clause"),
    ("python-docx", "https://github.com/python-openxml/python-docx.git", "e45454602b53e8e572b179ccf1c91093ec9f4ed7", "MIT"),
    ("python-pptx", "https://github.com/scanny/python-pptx.git", "278b47b1dedd5b46ee84c286e77cdfb0bf4594be", "MIT"),
    ("excelize", "https://github.com/qax-os/excelize.git", "ec40786f0b6554bbed6fae2a33e8eef52342b0d2", "BSD-3-Clause"),
    ("closedxml", "https://github.com/ClosedXML/ClosedXML.git", "057f5976c788d97e8eac86d1a528bd5411d9386e", "MIT"),
    ("open-xml-sdk", "https://github.com/OfficeDev/Open-XML-SDK.git", "431ab05cf160248cc3885a4a766026d4f8243792", "MIT"),
    ("shapecrawler", "https://github.com/ShapeCrawler/ShapeCrawler.git", "2ab474918ac89e7bbb8d7c649aca708d298171e2", "MIT"),
    ("poi", "https://github.com/apache/poi.git", "f66927954a29cf1f5b86614fdf1816e6321c8aea", "Apache-2.0", ("xlsb",)),
]


def git(cwd, *args):
    env = dict(os.environ, GIT_TERMINAL_PROMPT="0", GIT_LFS_SKIP_SMUDGE="1")
    subprocess.run(["git", "-c", "core.hooksPath=/dev/null", "-c", "advice.detachedHead=false", *args], cwd=cwd, check=True, env=env)


def fetch(name, url, commit, extensions=EXTENSIONS):
    """Check out the Office files of one repository at one commit.

    The clone holds no file contents until the checkout asks for them, and
    the sparse patterns ask only for the extensions above.
    """
    dest = os.path.join(DEST, name)
    if not os.path.isdir(os.path.join(dest, ".git")):
        os.makedirs(dest, exist_ok=True)
        git(dest, "init", "-q")
        git(dest, "remote", "add", "origin", url)
        git(dest, "config", "remote.origin.promisor", "true")
        git(dest, "config", "remote.origin.partialclonefilter", "blob:none")
        git(dest, "sparse-checkout", "init", "--no-cone")
    patterns = [f"*.{e}" for e in extensions] + [f"*.{e.upper()}" for e in extensions]
    git(dest, "sparse-checkout", "set", "--no-cone", *patterns)
    git(dest, "fetch", "-q", "--depth", "1", "--filter=blob:none", "origin", commit)
    git(dest, "checkout", "-q", "--detach", "FETCH_HEAD")


def present():
    out = []
    for base, dirs, names in os.walk(DEST):
        dirs[:] = [d for d in dirs if d != ".git"]
        for n in names:
            if n.rsplit(".", 1)[-1].lower() in EXTENSIONS:
                out.append(os.path.join(base, n))
    return sorted(out)


def main():
    if "--list" not in sys.argv:
        for name, url, commit, _, *extensions in SOURCES:
            print(f"{name}: {url} at {commit[:12]}", flush=True)
            fetch(name, url, commit, *extensions)
    files = present()
    by = {}
    for f in files:
        key = (os.path.relpath(f, DEST).split(os.sep)[0], f.rsplit(".", 1)[-1].lower())
        n, size = by.get(key, (0, 0))
        by[key] = (n + 1, size + os.path.getsize(f))
    for (source, ext), (n, size) in sorted(by.items()):
        print(f"{source:16} {ext:5} {n:6} files {size / 1e6:9.1f} MB")
    print(f"{len(files)} files, {sum(os.path.getsize(f) for f in files) / 1e6:.0f} MB")


if __name__ == "__main__":
    main()
