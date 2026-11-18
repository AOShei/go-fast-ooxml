#!/usr/bin/env python3
"""Download the test corpus of go-fast-ooxml.

The corpus is real Word, Excel and PowerPoint files from public sources. The
files are not committed; testdata/corpus.json lists each one by URL and
SHA-256, and this script downloads them into testdata/corpus/.

  python3 testdata/fetch.py            download what the manifest lists
  python3 testdata/fetch.py --rebuild  query the sources again and rewrite
                                       the manifest (changes what is tested;
                                       review the diff)
  python3 testdata/fetch.py --describe recompute producer and note of every
                                       entry from the downloaded files,
                                       without using the network

Standard library only. Sources:

  poi    The test files of Apache POI (Apache-2.0), pinned to one commit.
         Many were attached to bug reports by users of Microsoft Office, and
         some are deliberately damaged, encrypted or hostile.
  govuk  Files published on data.gov.uk under the Open Government Licence.

A file that is no longer available, or whose content has changed, is reported
and skipped; tests skip corpus files that are absent.
"""
import concurrent.futures
import hashlib
import io
import json
import os
import re
import sys
import urllib.parse
import urllib.request
import zipfile

HERE = os.path.dirname(os.path.abspath(__file__))
MANIFEST = os.path.join(HERE, "corpus.json")
USER_AGENT = "corpus-fetch/0.1"
MAX_BYTES = 25 * 1024 * 1024

FORMATS = {
    "docx": "docx", "docm": "docx", "dotx": "docx", "dotm": "docx",
    "xlsx": "xlsx", "xlsm": "xlsx", "xltx": "xlsx", "xltm": "xlsx",
    "pptx": "pptx", "pptm": "pptx", "ppsx": "pptx", "ppsm": "pptx", "potx": "pptx", "potm": "pptx",
}

POI_REPO = "apache/poi"
POI_DIRS = ("document", "spreadsheet", "slideshow", "openxml4j", "xmldsign", "integration")
GOVUK_API = "https://data.gov.uk/api/action/package_search"
GOVUK_WANT = {"DOCX": 70, "XLSX": 90, "PPTX": 40}


def get(url, limit=MAX_BYTES):
    req = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})
    with urllib.request.urlopen(req, timeout=60) as resp:
        data = resp.read(limit + 1)
    if len(data) > limit:
        raise ValueError(f"larger than {limit} bytes")
    return data


def get_json(url):
    return json.loads(get(url, limit=64 * 1024 * 1024))


MAIN_PARTS = {
    "wordprocessingml.document.main+xml": "docx", "wordprocessingml.template.main+xml": "docx",
    "ms-word.document.macroEnabled.main+xml": "docx", "ms-word.template.macroEnabledTemplate.main+xml": "docx",
    "spreadsheetml.sheet.main+xml": "xlsx", "spreadsheetml.template.main+xml": "xlsx",
    "ms-excel.sheet.macroEnabled.main+xml": "xlsx", "ms-excel.template.macroEnabled.main+xml": "xlsx",
    "presentationml.presentation.main+xml": "pptx", "presentationml.slideshow.main+xml": "pptx",
    "presentationml.template.main+xml": "pptx", "ms-powerpoint.presentation.macroEnabled.main+xml": "pptx",
    "ms-powerpoint.slideshow.macroEnabled.main+xml": "pptx", "ms-powerpoint.template.macroEnabled.main+xml": "pptx",
}


def describe(data, fmt):
    """What the file says about itself: (producer, note). The note is empty
    for a sound package of the format its extension claims."""
    if data[:8] == b"\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1":
        return "", "compound file, not a zip package: encrypted or legacy format"
    try:
        with zipfile.ZipFile(io.BytesIO(data)) as z:
            names = set(z.namelist())
            if "[Content_Types].xml" not in names:
                return "", "zip archive without [Content_Types].xml"
            types = z.read("[Content_Types].xml").decode("utf-8", "replace")
            found = {f for ct, f in MAIN_PARTS.items() if ct + '"' in types}
            note = ""
            if not found:
                note = "package has no Word, Excel or PowerPoint main part"
            elif found != {fmt}:
                note = "main part is " + "/".join(sorted(found)) + ", extension says " + fmt
            producer = ""
            if "docProps/app.xml" in names:
                app = z.read("docProps/app.xml").decode("utf-8", "replace")
                m = re.search(r"<(?:\w+:)?Application>([^<]*)<", app)
                v = re.search(r"<(?:\w+:)?AppVersion>([^<]*)<", app)
                if m:
                    producer = m.group(1).strip() + (" " + v.group(1).strip() if v else "")
            return producer, note
    except Exception as e:  # hostile archives raise many things
        return "", f"not a readable zip archive ({type(e).__name__})"


def entry(name, path, url, data, licence):
    ext = path.rsplit(".", 1)[-1].lower()
    producer, note = describe(data, FORMATS[ext])
    e = {"name": name, "format": FORMATS[ext], "path": path, "url": url,
         "sha256": hashlib.sha256(data).hexdigest(), "size": len(data)}
    if producer:
        e["producer"] = producer
    e["licence"] = licence
    if note:
        e["note"] = note
    return e


def store(path, data):
    full = os.path.join(HERE, path)
    os.makedirs(os.path.dirname(full), exist_ok=True)
    with open(full, "wb") as f:
        f.write(data)


def parallel(fn, items, workers=6):
    with concurrent.futures.ThreadPoolExecutor(workers) as pool:
        return list(pool.map(fn, items))


# ---------------------------------------------------------------------- build

def build_poi():
    commit = get_json(f"https://api.github.com/repos/{POI_REPO}/commits/trunk")["sha"]
    root = get_json(f"https://api.github.com/repos/{POI_REPO}/git/trees/{commit}")
    test_data = next(t["sha"] for t in root["tree"] if t["path"] == "test-data")
    tree = get_json(f"https://api.github.com/repos/{POI_REPO}/git/trees/{test_data}?recursive=1")
    if tree.get("truncated"):
        sys.exit("the POI file listing was truncated; narrow POI_DIRS")
    wanted = [t for t in tree["tree"]
              if t["type"] == "blob" and t["path"].split("/")[0] in POI_DIRS
              and t["path"].rsplit(".", 1)[-1].lower() in FORMATS and t.get("size", 0) <= MAX_BYTES]
    print(f"poi: commit {commit[:12]}, {len(wanted)} files")
    licence = f"Apache-2.0 (Apache POI test data, commit {commit[:12]})"

    def one(t):
        url = f"https://raw.githubusercontent.com/{POI_REPO}/{commit}/test-data/" + urllib.parse.quote(t["path"])
        try:
            data = get(url)
        except Exception as e:
            print(f"  skipped {t['path']}: {e}", file=sys.stderr)
            return None
        path = "corpus/poi/" + t["path"]
        store(path, data)
        return entry("poi/" + t["path"], path, url, data, licence)

    return [e for e in parallel(one, wanted) if e]


def build_govuk():
    out = []
    for fmt, want in GOVUK_WANT.items():
        candidates, seen, start = [], set(), 0
        while len(candidates) < want * 3 and start < 1000:
            page = get_json(f"{GOVUK_API}?fq=res_format:{fmt}&rows=100&start={start}")["result"]["results"]
            if not page:
                break
            start += 100
            for pkg in page:
                if pkg.get("license_id") != "uk-ogl":
                    continue
                publisher = (pkg.get("organization") or {}).get("title", "")
                per_dataset = 0
                for res in pkg.get("resources", []):
                    url = res.get("url", "")
                    ext = urllib.parse.urlparse(url).path.rsplit(".", 1)[-1].lower()
                    if (res.get("format") or "").upper() != fmt or ext not in FORMATS or url in seen:
                        continue
                    seen.add(url)
                    candidates.append((url, ext, publisher))
                    per_dataset += 1
                    if per_dataset == 2:  # spread across publishers and datasets
                        break

        def one(c):
            url, ext, publisher = c
            try:
                data = get(url)
            except Exception:
                return None
            if data[:2] != b"PK":
                return None  # an error page, or a mislabelled legacy file
            digest = hashlib.sha256(data).hexdigest()
            path = f"corpus/govuk/{digest[:16]}.{ext}"
            store(path, data)
            licence = "Open Government Licence v3.0 (data.gov.uk" + (f"; publisher: {publisher}" if publisher else "") + ")"
            return entry(f"govuk/{digest[:16]}.{ext}", path, url, data, licence)

        got, hashes = [], set()
        for e in parallel(one, candidates):
            if e and e["sha256"] not in hashes and len(got) < want:
                hashes.add(e["sha256"])
                got.append(e)
        print(f"govuk: {fmt}: {len(got)} files from {len(candidates)} candidates")
        out += got
    return out


def rebuild():
    manifest = build_poi() + build_govuk()
    manifest.sort(key=lambda e: e["name"])
    kept = {e["path"] for e in manifest}
    # Remove downloads that did not make it into the manifest.
    for base, _, files in os.walk(os.path.join(HERE, "corpus", "govuk")):
        for f in files:
            if f"corpus/govuk/{f}" not in kept:
                os.remove(os.path.join(base, f))
    with open(MANIFEST, "w", encoding="utf-8") as f:
        json.dump(manifest, f, ensure_ascii=False, indent=1)
        f.write("\n")
    summary(manifest)


# ---------------------------------------------------------------------- fetch

def fetch():
    with open(MANIFEST, encoding="utf-8") as f:
        manifest = json.load(f)

    def one(e):
        full = os.path.join(HERE, e["path"])
        if os.path.exists(full):
            with open(full, "rb") as f:
                if hashlib.sha256(f.read()).hexdigest() == e["sha256"]:
                    return "present"
        try:
            data = get(e["url"])
        except Exception as err:
            print(f"  unavailable: {e['name']}: {err}", file=sys.stderr)
            return "unavailable"
        if hashlib.sha256(data).hexdigest() != e["sha256"]:
            print(f"  changed at the source, skipped: {e['name']}", file=sys.stderr)
            return "changed"
        store(e["path"], data)
        return "downloaded"

    results = parallel(one, manifest)
    for status in ("present", "downloaded", "unavailable", "changed"):
        print(f"{status}: {results.count(status)}")


def redescribe():
    with open(MANIFEST, encoding="utf-8") as f:
        manifest = json.load(f)
    out = []
    for e in manifest:
        full = os.path.join(HERE, e["path"])
        if not os.path.exists(full):
            out.append(e)
            continue
        with open(full, "rb") as f:
            out.append(entry(e["name"], e["path"], e["url"], f.read(), e["licence"]))
    with open(MANIFEST, "w", encoding="utf-8") as f:
        json.dump(out, f, ensure_ascii=False, indent=1)
        f.write("\n")
    summary(out)


def summary(manifest):
    by = {}
    for e in manifest:
        key = (e["name"].split("/")[0], e["format"])
        n, size = by.get(key, (0, 0))
        by[key] = (n + 1, size + e["size"])
    for (source, fmt), (n, size) in sorted(by.items()):
        print(f"{source:6} {fmt}: {n:4} files, {size / 1e6:7.1f} MB")
    print(f"total: {len(manifest)} files, {sum(e['size'] for e in manifest) / 1e6:.1f} MB")


if __name__ == "__main__":
    if "--rebuild" in sys.argv:
        rebuild()
    elif "--describe" in sys.argv:
        redescribe()
    else:
        fetch()
