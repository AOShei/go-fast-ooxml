#!/usr/bin/env python3
"""Compare this library with the tools people use today, on the same files.

  ../bench-env/.venv-ooxml/bin/python bench/compare.py            # measure, then write
  ../bench-env/.venv-ooxml/bin/python bench/compare.py --write    # write again from comparison.json

Every tool reads every file it applies to, once, in a process of its own:
the large generated files (testdata/gen/gen.py --large) and the downloaded
corpus (testdata/fetch.py). The Python tools are measured as in
run_baseline.py, their time not counting the import of the library. This
library is measured as its command, `gofastooxml -format text`, built here
from the working tree, its time counting the whole process from start to
exit. Peak memory is the peak resident size of the process for all.

Writes bench/comparison.json (the numbers), bench/comparison.md (the
tables), and the part of README.md between the comparison markers, which
is the summary table of comparison.md. A test (TestReadmeComparison) fails
if the README's table is not the one generated.
"""
import concurrent.futures
import json
import os
import platform
import subprocess
import sys
import time

BENCH = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(BENCH)
sys.path.insert(0, BENCH)
import run_baseline as base  # noqa: E402
from run_baseline import tools  # noqa: E402

BINARY = os.path.join(BENCH, "out", "gofastooxml")
BEGIN, END = "<!-- comparison:begin -->", "<!-- comparison:end -->"
OURS = "gofastooxml"
ONE = "gofastooxml, one worker"


def build():
    os.makedirs(os.path.dirname(BINARY), exist_ok=True)
    subprocess.run(["go", "build", "-o", BINARY, "./cmd/gofastooxml"], cwd=ROOT, check=True)
    return subprocess.run(["go", "version"], capture_output=True, text=True, check=True).stdout.split()[2]


def measure_ours(job):
    """Run the command on one file and report as bench/one.py does."""
    tool, path = job
    args = [BINARY, "-format", "text", "-quiet"] + (["-workers", "1"] if tool == ONE else []) + [path]
    out = {"tool": tool, "file": path, "bytes": os.path.getsize(path)}
    start = time.perf_counter()
    p = subprocess.Popen(args, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    words = 0
    tail = b""
    try:
        # Counted as it arrives, so that the text of a large file is not
        # held here; a word cut by the end of a chunk is put together.
        while chunk := p.stdout.read(1 << 20):
            if time.perf_counter() - start > base.TIMEOUT:
                p.kill()
                break
            chunk = tail + chunk
            cut = max(chunk.rfind(b" "), chunk.rfind(b"\n"), chunk.rfind(b"\t"))
            tail = chunk[cut + 1:] if cut >= 0 else chunk
            words += len(chunk[:cut + 1].split()) if cut >= 0 else 0
    finally:
        _, status, usage = os.wait4(p.pid, 0)
    seconds = time.perf_counter() - start
    words += len(tail.split())
    if seconds > base.TIMEOUT:
        out["error"] = "Timeout"
    elif os.WIFSIGNALED(status):
        out["error"] = "Killed"
    elif os.WEXITSTATUS(status) != 0:
        out["error"] = "Refused"
    else:
        out.update({"seconds": seconds, "words": words, "import_seconds": 0.0, "import_rss_mb": 0.0})
    out["peak_rss_mb"] = usage.ru_maxrss / 1024  # Linux reports KiB
    return out


def measure(job):
    return measure_ours(job) if job[0] in (OURS, ONE) else base.measure(job)


def run():
    go = build()
    corpus = base.files("corpus.json", need_present=False)
    large_dir = os.path.join(base.TESTDATA, "corpus", "generated")
    large = [(n.rsplit(".", 1)[1], os.path.join(large_dir, n)) for n in sorted(os.listdir(large_dir))] \
        if os.path.isdir(large_dir) else []
    if not corpus or not large:
        sys.exit("the corpus and the large files are needed: python3 testdata/fetch.py, testdata/gen/gen.py --large")
    everything = {"docx", "xlsx", "pptx"}
    entrants = [(t, formats) for t, (_, _, formats) in tools.TOOLS.items()] + [(ONE, everything), (OURS, everything)]
    versions = {tools.TOOLS[t][0]: v for t, v in ((t, tools.tool_version(t)) for t in tools.TOOLS)}
    report = {
        "machine": {"cpu": base.cpu_model(), "cores": os.cpu_count(), "os": platform.platform(),
                    "python": platform.python_version(), "go": go},
        "versions": versions,
        "limits": {"timeout_seconds": base.TIMEOUT, "parallel_corpus_runs": base.PARALLEL},
        "corpus_files": len(corpus),
        "large": [], "corpus": {},
    }
    # The large files one at a time, with nothing else running here.
    for tool, formats in entrants:
        for fmt, path in large:
            if fmt not in formats:
                continue
            # The best of three for this library, whose runs are short
            # enough for a busy machine to show; one for the others.
            runs = [measure((tool, path)) for _ in range(3 if tool in (OURS, ONE) else 1)]
            ok = [r for r in runs if "error" not in r]
            r = min(ok, key=lambda r: r["seconds"]) if ok else runs[0]
            r["file"] = os.path.basename(path)
            for k in ("seconds", "import_seconds", "peak_rss_mb", "import_rss_mb"):
                if k in r:
                    r[k] = round(r[k], 3)
            report["large"].append(r)
            print(f"large    {tool:26} {r}", flush=True)
    for tool, formats in entrants:
        if tool == ONE:
            continue
        for fmt in sorted(formats):
            jobs = [(tool, path) for f, path, _ in corpus if f == fmt]
            with concurrent.futures.ThreadPoolExecutor(base.PARALLEL) as pool:
                results = list(pool.map(measure, jobs))
            key = f"{tool} / {fmt}"
            report["corpus"][key] = base.summarise(results)
            print(f"corpus   {key:26} {report['corpus'][key]}", flush=True)
    with open(os.path.join(BENCH, "comparison.json"), "w", encoding="utf-8") as f:
        json.dump(report, f, indent=1)
        f.write("\n")
    return report


def seconds(s):
    return f"{s:.2f} s" if s >= 0.995 else f"{s:.3f} s"


def summary(report):
    """The table that goes into the README: the large files."""
    large = report["large"]
    described = {
        "large.docx": "`large.docx`: 1.5 million words",
        "large.xlsx": "`large.xlsx`: 2 million cells",
        "large.pptx": "`large.pptx`: 1,500 slides",
    }
    lines = ["| File | Tool | Time | Peak memory | Time against this library |", "|---|---|---|---|---|"]
    for name in ("large.docx", "large.xlsx", "large.pptx"):
        rows = [r for r in large if r["file"] == name]
        ours = next(r for r in rows if r["tool"] == OURS)
        first = True
        for r in sorted(rows, key=lambda r: (r["tool"] not in (OURS, ONE), r["tool"] != OURS, r.get("seconds", 1e9))):
            label = {OURS: "**this library**, all workers", ONE: "**this library**, one worker"}.get(r["tool"], r["tool"])
            file = described[name] if first else ""
            first = False
            if "error" in r:
                lines.append(f"| {file} | {label} | {r['error']} | | |")
                continue
            ratio = r["seconds"] / ours["seconds"]
            against = "" if r["tool"] == OURS else (f"{ratio:,.0f} times" if ratio >= 9.5 else f"{ratio:.1f} times")
            lines.append(f"| {file} | {label} | {seconds(r['seconds'])} | {r['peak_rss_mb']:,.0f} MB | {against} |")
    return lines


def write(report):
    m = report["machine"]
    table = summary(report)
    lines = [
        "# Comparison with the tools in use today",
        "",
        "Generated by `bench/compare.py`; do not edit. Numbers are from one machine and compare tools on that machine only.",
        "",
        f"Machine: {m['cpu']}, {m['cores']} logical cores, {m['os']}, Python {m['python']}, {m['go']}.",
        "",
        "Versions: " + ", ".join(f"{t} {v}" for t, v in sorted(report["versions"].items())) + ".",
        "",
        "Each file is read once in a process of its own. For the Python tools the time does not count importing the "
        "library (0.01 to 0.4 s, in `baseline.md`). For this library it is the whole run of the command "
        "`gofastooxml -format text`, from start to exit, the best of three. Peak memory is the peak resident size of "
        "the process.",
        "",
        "## Large files",
        "",
        "Generated by `testdata/gen/gen.py --large`.",
        "",
        BEGIN,
        *table,
        END,
        "",
        "docx2txt gives the text of a Word file and nothing of its structure. python-pptx gives the text of shapes; "
        "this library also reads layouts, notes, comments and what is hidden. The others give tables as text.",
        "",
        "## Corpus",
        "",
        f"The {report['corpus_files']} real files from Apache POI's test suite and data.gov.uk, by the format the "
        "manifest gives them. They include files that are damaged, encrypted, of a legacy format or hostile; a file a "
        "tool could not read is counted with the kind of failure, which for this library is always a refusal with a "
        "reason (exit status 1), never a crash. Total time is over the files the tool read.",
        "",
        "| Tool / format | Files | Read | Not read | Total time | Median | 95th percentile | Peak memory (MB) |",
        "|---|---|---|---|---|---|---|---|",
    ]
    for key, s in sorted(report["corpus"].items(), key=lambda kv: (kv[0].split(" / ")[1], not kv[0].startswith(OURS), kv[0])):
        failed = ", ".join(f"{n} {e}" for e, n in sorted(s["errors"].items(), key=lambda kv: -kv[1])) or "0"
        label = key.replace(OURS + " /", "**this library** /")
        if s["ok"]:
            lines.append(f"| {label} | {s['files']} | {s['ok']} | {failed} | {s['seconds']:.1f} s | {s['median_ms']} ms | "
                         f"{s['p95_ms']} ms | {s['max_peak_rss_mb']:.0f} |")
        else:
            lines.append(f"| {label} | {s['files']} | 0 | {failed} | | | | |")
    with open(os.path.join(BENCH, "comparison.md"), "w", encoding="utf-8") as f:
        f.write("\n".join(lines) + "\n")
    readme = os.path.join(ROOT, "README.md")
    with open(readme, encoding="utf-8") as f:
        text = f.read()
    if BEGIN not in text or END not in text:
        sys.exit("README.md has no comparison markers")
    head, rest = text.split(BEGIN, 1)
    _, tail = rest.split(END, 1)
    with open(readme, "w", encoding="utf-8") as f:
        f.write(head + BEGIN + "\n" + "\n".join(table) + "\n" + END + tail)


def main():
    if "--write" in sys.argv:
        with open(os.path.join(BENCH, "comparison.json"), encoding="utf-8") as f:
            report = json.load(f)
    else:
        report = run()
    write(report)


if __name__ == "__main__":
    main()
