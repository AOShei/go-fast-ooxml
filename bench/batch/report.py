"""Summarise the runs of run_all.sh.

    python -I report.py RESULTS [RESULTS...]

For each tool and worker count, over its repetitions: the fastest wall time,
the CPU time of that run, and the median of the peaks (the peak of one run
is a sample every 20 ms, so it varies more than the time); then the files
read, the files the tool failed on or gave up on, and the characters of text.
Prints a Markdown table per results directory.
"""
import collections, glob, json, os, re, statistics, sys

for R in sys.argv[1:]:
    runs = collections.defaultdict(list)
    for m in sorted(glob.glob(os.path.join(R, "*", "measure.json"))):
        d = os.path.dirname(m)
        tool, w, rep = re.fullmatch(r"(.+)-(\d+)-r(\d+)", os.path.basename(d)).groups()
        meas = json.load(open(m))
        res = [json.loads(l) for l in open(os.path.join(d, "results.jsonl")) if l.strip()]
        runs[(tool, int(w))].append((meas, res))
    print(f"\n### {R}\n")
    print("| Tool | Workers | Wall | CPU | Peak memory | Files | Failed | Text (M chars) | Slowest file |")
    print("|---|---|---|---|---|---|---|---|---|")
    for (tool, w), rs in sorted(runs.items(), key=lambda kv: (kv[0][1], min(m["wall_s"] for m, _ in kv[1]))):
        best = min(rs, key=lambda r: r[0]["wall_s"])
        meas, res = best
        peak = statistics.median(m["peak_rss_mb"] for m, _ in rs)
        failed = sum(1 for r in res if r.get("err"))
        timeouts = sum(1 for r in res if r.get("err") == "timeout")
        chars = sum(r.get("chars", 0) for r in res) / 1e6
        slow = max((r["s"] for r in res), default=0)
        status = "" if meas["status"] == 0 else f" (exit {meas['status']})"
        fail = f"{failed}" + (f" ({timeouts} timeouts)" if timeouts else "")
        print(f"| {tool}{status} | {w} | {meas['wall_s']:.2f} s | {meas['cpu_s']:.2f} s | {peak:,.0f} MB | {len(res)} | {fail} | {chars:.1f} | {slow:.2f} s |")
