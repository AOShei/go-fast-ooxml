"""Measure one comparison tool on one file, in a process of its own.

Usage: one.py <tool> <file>

Prints one JSON object: the time to import the library, the time to extract
the file once, the peak memory of the process and what the import alone
costs, the number of words extracted, or the error. Run by run_baseline.py;
a process per measurement keeps one file's memory out of the next, and lets
a hostile file be stopped without losing the rest of the run.
"""
import json
import os
import resource
import sys
import time

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "testdata", "gen"))

MEMORY_LIMIT = 8 << 30  # bytes of address space; a decompression bomb stops here


def rss_mb():
    return resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 1024  # Linux reports KiB


def main():
    tool, path = sys.argv[1], sys.argv[2]
    resource.setrlimit(resource.RLIMIT_AS, (MEMORY_LIMIT, MEMORY_LIMIT))
    out = {"tool": tool, "file": path, "bytes": os.path.getsize(path)}
    t0 = time.perf_counter()
    import tools

    adapter = tools.TOOLS[tool][1]
    try:
        adapter(os.devnull)  # forces the lazy import; the file is not a package
    except Exception:
        pass
    out["import_seconds"] = time.perf_counter() - t0
    out["import_rss_mb"] = rss_mb()
    t1 = time.perf_counter()
    try:
        text = adapter(path)
        out["seconds"] = time.perf_counter() - t1
        out["words"] = len(text.split())
    except MemoryError:
        out["error"] = "MemoryError"
    except Exception as e:
        out["error"] = type(e).__name__
    out["peak_rss_mb"] = rss_mb()
    json.dump(out, sys.stdout)


if __name__ == "__main__":
    main()
