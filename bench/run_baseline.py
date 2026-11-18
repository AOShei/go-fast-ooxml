#!/usr/bin/env python3
"""Measure the tools people use today: the speed and memory baseline.

  ../bench-env/.venv-ooxml/bin/python bench/run_baseline.py

Runs every comparison tool on every file it applies to: the committed
fixtures, the large generated files (testdata/gen/gen.py --large) and the
downloaded corpus (testdata/fetch.py). Each measurement is a process of its
own (bench/one.py) with a time limit and a memory limit.

Writes bench/baseline.json (the numbers) and bench/baseline.md (the table).
Accuracy is measured separately, by `go test -run TestBaselineAccuracy`, so
that one scorer judges both these tools and the library.
"""
import concurrent.futures
import json
import os
import platform
import statistics
import subprocess
import sys
from importlib.metadata import version

BENCH = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(BENCH)
TESTDATA = os.path.join(ROOT, "testdata")
sys.path.insert(0, os.path.join(TESTDATA, "gen"))
import tools  # noqa: E402

TIMEOUT = 120      # seconds per file
PARALLEL = 6       # corpus measurements at once; the large files run alone


def measure(job):
    tool, path = job
    try:
        p = subprocess.run([sys.executable, os.path.join(BENCH, "one.py"), tool, path],
                           capture_output=True, text=True, timeout=TIMEOUT)
        if p.returncode == 0 and p.stdout.strip():
            return json.loads(p.stdout)
        error = "Killed" if p.returncode < 0 else "Crash"
    except subprocess.TimeoutExpired:
        error = "Timeout"
    return {"tool": tool, "file": path, "bytes": os.path.getsize(path), "error": error}


def files(manifest_name, need_present):
    with open(os.path.join(TESTDATA, manifest_name), encoding="utf-8") as f:
        manifest = json.load(f)
    out = []
    for e in manifest:
        path = os.path.join(TESTDATA, e["path"])
        if os.path.exists(path):
            out.append((e["format"], path, e))
        elif need_present:
            sys.exit(f"missing {path}")
    return out


def pct(values, q):
    values = sorted(values)
    return values[min(len(values) - 1, int(q * len(values)))]


def summarise(results):
    ok = [r for r in results if "error" not in r]
    errors = {}
    for r in results:
        if "error" in r:
            errors[r["error"]] = errors.get(r["error"], 0) + 1
    out = {"files": len(results), "ok": len(ok), "errors": errors}
    if ok:
        seconds = sum(r["seconds"] for r in ok)
        megabytes = sum(r["bytes"] for r in ok) / 1e6
        out.update({
            "megabytes": round(megabytes, 2),
            "seconds": round(seconds, 3),
            "mb_per_second": round(megabytes / seconds, 2) if seconds else None,
            "files_per_second": round(len(ok) / seconds, 1) if seconds else None,
            "median_ms": round(1000 * statistics.median(r["seconds"] for r in ok), 2),
            "p95_ms": round(1000 * pct([r["seconds"] for r in ok], 0.95), 2),
            "max_peak_rss_mb": round(max(r["peak_rss_mb"] for r in ok), 1),
            "import_seconds": round(statistics.median(r["import_seconds"] for r in ok), 3),
            "import_rss_mb": round(statistics.median(r["import_rss_mb"] for r in ok), 1),
        })
    return out


def cpu_model():
    try:
        with open("/proc/cpuinfo") as f:
            for line in f:
                if line.startswith("model name"):
                    return line.split(":", 1)[1].strip()
    except OSError:
        pass
    return platform.processor()


def main():
    corpus = files("corpus.json", need_present=False)
    fixtures = files("fixtures.json", need_present=True)
    large_dir = os.path.join(TESTDATA, "corpus", "generated")
    large = [(n.rsplit(".", 1)[1], os.path.join(large_dir, n)) for n in sorted(os.listdir(large_dir))] \
        if os.path.isdir(large_dir) else []
    if not corpus:
        print("no corpus files present: run python3 testdata/fetch.py", file=sys.stderr)
    if not large:
        print("no large files present: run testdata/gen/gen.py --large", file=sys.stderr)

    report = {
        "machine": {"cpu": cpu_model(), "cores": os.cpu_count(), "os": platform.platform(), "python": platform.python_version()},
        "versions": {t: tools.tool_version(t) for t in tools.TOOLS},
        "limits": {"timeout_seconds": TIMEOUT, "memory_gb": 8, "parallel_corpus_runs": PARALLEL},
        "note": "Each file is extracted once in a fresh process; 'seconds' excludes the import of the library, which is reported per tool.",
        "corpus": {}, "fixtures": {}, "large": [],
    }
    for tool, (_, _, formats) in tools.TOOLS.items():
        for fmt in sorted(formats):
            key = f"{tool} / {fmt}"
            jobs = [(tool, path) for f, path, _ in corpus if f == fmt]
            if jobs:
                with concurrent.futures.ThreadPoolExecutor(PARALLEL) as pool:
                    results = list(pool.map(measure, jobs))
                report["corpus"][key] = summarise(results)
                print(f"corpus   {key:26} {report['corpus'][key]}", flush=True)
            jobs = [(tool, path) for f, path, _ in fixtures if f == fmt]
            report["fixtures"][key] = summarise([measure(j) for j in jobs])
            for f, path in large:
                if f != fmt:
                    continue
                r = measure((tool, path))
                r["file"] = os.path.basename(path)
                for k in ("seconds", "import_seconds", "peak_rss_mb", "import_rss_mb"):
                    if k in r:
                        r[k] = round(r[k], 3)
                if "seconds" in r:
                    r["mb_per_second"] = round(r["bytes"] / 1e6 / r["seconds"], 2)
                    r["words_per_second"] = round(r["words"] / r["seconds"])
                report["large"].append(r)
                print(f"large    {key:26} {r}", flush=True)

    with open(os.path.join(BENCH, "baseline.json"), "w", encoding="utf-8") as f:
        json.dump(report, f, indent=1)
        f.write("\n")
    write_markdown(report)


def write_markdown(report):
    m = report["machine"]
    lines = [
        "# Baseline: the tools in use today",
        "",
        "Generated by `bench/run_baseline.py`; do not edit. Numbers are from one machine and are for comparison on that machine only.",
        "",
        f"Machine: {m['cpu']}, {m['cores']} logical cores, {m['os']}, Python {m['python']}.",
        "",
        "Versions: " + ", ".join(f"{t} {v}" for t, v in sorted(set((tools.TOOLS[t][0], v) for t, v in report["versions"].items()))) + ".",
        "",
        "Each file is extracted once in a fresh process. Times exclude importing the library, which is listed separately because a pipeline pays it once per process.",
        "",
        "## Large files",
        "",
        "Generated by `testdata/gen/gen.py --large`. MB/s is measured on the file as stored (compressed).",
        "",
        "| Tool | File | Size (MB) | Words | Seconds | MB/s | Words/s | Peak memory (MB) |",
        "|---|---|---|---|---|---|---|---|",
    ]
    for r in report["large"]:
        if "error" in r:
            lines.append(f"| {r['tool']} | {r['file']} | {r['bytes'] / 1e6:.1f} | | {r['error']} | | | |")
        else:
            lines.append(f"| {r['tool']} | {r['file']} | {r['bytes'] / 1e6:.1f} | {r['words']:,} | {r['seconds']:.2f} | "
                         f"{r['mb_per_second']:.2f} | {r['words_per_second']:,} | {r['peak_rss_mb']:.0f} |")
    lines += [
        "",
        "## Corpus",
        "",
        "Real files from Apache POI's test suite and data.gov.uk. Files a tool could not read are counted, with the kind of failure.",
        "",
        "| Tool / format | Files | Read | Failed | MB/s | Files/s | Median ms | p95 ms | Peak memory (MB) | Import s | Import memory (MB) |",
        "|---|---|---|---|---|---|---|---|---|---|---|",
    ]
    for key, s in report["corpus"].items():
        failed = ", ".join(f"{n} {e}" for e, n in sorted(s["errors"].items(), key=lambda kv: -kv[1])) or "0"
        if s["ok"]:
            lines.append(f"| {key} | {s['files']} | {s['ok']} | {failed} | {s['mb_per_second']} | {s['files_per_second']} | "
                         f"{s['median_ms']} | {s['p95_ms']} | {s['max_peak_rss_mb']} | {s['import_seconds']} | {s['import_rss_mb']} |")
        else:
            lines.append(f"| {key} | {s['files']} | 0 | {failed} | | | | | | | |")
    with open(os.path.join(BENCH, "baseline.md"), "w", encoding="utf-8") as f:
        f.write("\n".join(lines) + "\n")


if __name__ == "__main__":
    main()
