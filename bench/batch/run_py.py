"""Extract the text of every file in a list with one Python tool, for
the batch comparison (bench/batch/README.md).

    python -I run_py.py TOOL LIST OUTDIR WORKERS

Each worker process imports the tool once and reads its share of the files
(in order, interleaved). For each file: the seconds the call took
(time.perf_counter, after the bytes are read), whether it raised (and the
exception's type), and the length of the text. The text is written to
OUTDIR/text/NAME.txt. A call longer than 60 s is stopped and counted as a
timeout. OUTDIR/results.jsonl gets one line a file.

TOOL: python-docx, docx2txt (docx); openpyxl (xlsx, read-only mode, the
stored values); python-pptx (pptx); markitdown (all three).
"""
import io, json, os, signal, sys, time
from multiprocessing import Pool

TOOL, LIST, OUT, WORKERS = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
TIMEOUT = 60


def make(tool):
    if tool == "python-docx":
        import docx
        def f(b, ext):
            d = docx.Document(io.BytesIO(b))
            out = [p.text for p in d.paragraphs]
            for t in d.tables:
                for row in t.rows:
                    out.append("\t".join(c.text for c in row.cells))
            return "\n".join(out)
        return f
    if tool == "docx2txt":
        import docx2txt
        return lambda b, ext: docx2txt.process(io.BytesIO(b))
    if tool == "openpyxl":
        import openpyxl
        def f(b, ext):
            wb = openpyxl.load_workbook(io.BytesIO(b), read_only=True, data_only=True)
            out = []
            try:
                for ws in wb.worksheets:
                    for row in ws.iter_rows(values_only=True):
                        out.append("\t".join("" if v is None else str(v) for v in row))
                    out.append("")
            finally:
                wb.close()
            return "\n".join(out)
        return f
    if tool == "python-pptx":
        import pptx
        def f(b, ext):
            p = pptx.Presentation(io.BytesIO(b))
            out = []
            for slide in p.slides:
                for shape in slide.shapes:
                    if shape.has_text_frame:
                        out.append(shape.text_frame.text)
                    if getattr(shape, "has_table", False) and shape.has_table:
                        for row in shape.table.rows:
                            out.append("\t".join(c.text for c in row.cells))
            return "\n".join(out)
        return f
    if tool == "markitdown":
        # markitdown detects file types with magika, an onnxruntime model
        # whose session starts a thread per core in every process; held to
        # one thread here, as every other tool's worker is.
        import onnxruntime as rt
        session = rt.InferenceSession
        def one_thread(*a, **k):
            so = k.get("sess_options") or rt.SessionOptions()
            so.intra_op_num_threads = so.inter_op_num_threads = 1
            k["sess_options"] = so
            return session(*a, **k)
        rt.InferenceSession = one_thread
        from markitdown import MarkItDown, StreamInfo
        md = MarkItDown(enable_plugins=False)
        return lambda b, ext: md.convert_stream(io.BytesIO(b), stream_info=StreamInfo(extension="." + ext)).text_content
    raise SystemExit("unknown tool " + tool)


fn = None


def init():
    global fn
    fn = make(TOOL)


def alarm(signum, frame):
    raise TimeoutError()


def one(path):
    signal.signal(signal.SIGALRM, alarm)
    b = open(path, "rb").read()
    name = os.path.basename(path)
    ext = name.rsplit(".", 1)[-1]
    t0 = time.perf_counter()
    signal.alarm(TIMEOUT)
    try:
        text, err = fn(b, ext) or "", None
    except TimeoutError:
        text, err = "", "timeout"
    except BaseException as e:  # a tool's failure is a result, not a crash of the run
        text, err = "", type(e).__name__
    finally:
        signal.alarm(0)
    s = time.perf_counter() - t0
    if err is None:
        with open(os.path.join(OUT, "text", name + ".txt"), "w", encoding="utf-8", errors="replace") as w:
            w.write(text)
    return {"file": name, "s": s, "err": err, "chars": len(text)}


def share(k):
    return [one(p) for p in FILES[k::WORKERS]]


FILES = [l for l in open(LIST, encoding="utf-8").read().splitlines() if l]

if __name__ == "__main__":
    os.makedirs(os.path.join(OUT, "text"), exist_ok=True)
    if WORKERS == 1:
        init()
        parts = [share(0)]
    else:
        with Pool(WORKERS, initializer=init) as pool:
            parts = pool.map(share, range(WORKERS))
    res = [None] * len(FILES)
    for k, part in enumerate(parts):
        for j, r in enumerate(part):
            res[k + j * WORKERS] = r
    with open(os.path.join(OUT, "results.jsonl"), "w") as w:
        for r in res:
            w.write(json.dumps(r) + "\n")
