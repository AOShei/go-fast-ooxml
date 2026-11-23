"""Run a command and report its wall time, CPU time and peak memory.

    python -I measure.py OUT.json -- CMD ARGS...

Peak memory is the largest sum of the resident sizes of the process and
all its descendants, sampled every 20 ms (psutil). CPU time is user plus
system of the process and its waited-for children (os.wait4).
"""
import json, os, subprocess, sys, threading, time
import psutil

out = sys.argv[1]
cmd = sys.argv[sys.argv.index("--") + 1:]
t0 = time.perf_counter()
p = subprocess.Popen(cmd)
peak = 0
done = False


def sample():
    global peak
    pp = psutil.Process(p.pid)
    while not done:
        try:
            rss = pp.memory_info().rss + sum(c.memory_info().rss for c in pp.children(recursive=True))
            peak = max(peak, rss)
        except psutil.Error:
            pass
        time.sleep(0.02)


th = threading.Thread(target=sample, daemon=True)
th.start()
_, status, ru = os.wait4(p.pid, 0)
wall = time.perf_counter() - t0
done = True
th.join()
json.dump({"cmd": cmd, "status": os.waitstatus_to_exitcode(status), "wall_s": wall,
           "cpu_s": ru.ru_utime + ru.ru_stime, "peak_rss_mb": peak / 2**20}, open(out, "w"))
print(f"{wall:.2f} s wall, {ru.ru_utime + ru.ru_stime:.2f} s CPU, {peak / 2**20:.0f} MB peak")
