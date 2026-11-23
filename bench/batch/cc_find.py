"""Find Word, Excel and PowerPoint files in Common Crawl's columnar index.

    python -I cc_find.py PATHS_GZ OUTDIR PREFIX...

For the media types of Word, Excel and PowerPoint documents. Reads the index files whose name starts
with one of the PREFIXes, one at a time, keeps the rows fetched whole
(status 200, not truncated), and writes url, mime, warc file, offset and
length to OUTDIR/<index file>.tsv. A file already written is skipped; a
refused request is retried after a pause.
"""
import gzip, os, sys, time, polars as pl

paths = [l.strip() for l in gzip.open(sys.argv[1], "rt") if "subset=warc" in l]
out = sys.argv[2]
prefixes = sys.argv[3:]
os.makedirs(out, exist_ok=True)
mimes = ["application/vnd.openxmlformats-officedocument.wordprocessingml.document",
         "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
         "application/vnd.openxmlformats-officedocument.presentationml.presentation"]
for p in paths:
    name = p.rsplit("/", 1)[1]
    if prefixes and not any(name.startswith(x) for x in prefixes):
        continue
    dest = os.path.join(out, name[:10] + ".tsv")
    if os.path.exists(dest):
        continue
    for attempt in range(6):
        try:
            df = (pl.scan_parquet("https://data.commoncrawl.org/" + p)
                    .filter(pl.col("content_mime_detected").is_in(mimes))
                    .select("url", "content_mime_detected", "fetch_status", "content_truncated",
                            "warc_filename", "warc_record_offset", "warc_record_length")
                    .collect())
            break
        except OSError as e:
            print(name[:10], "retrying:", str(e)[:80], file=sys.stderr, flush=True)
            time.sleep(60 * (attempt + 1))
    else:
        continue
    df = df.filter((pl.col("fetch_status") == 200) & pl.col("content_truncated").is_null())
    df.write_csv(dest + ".tmp", separator="\t")
    os.rename(dest + ".tmp", dest)
    print(name[:10], df.group_by("content_mime_detected").len().sort("len").rows(), file=sys.stderr, flush=True)
