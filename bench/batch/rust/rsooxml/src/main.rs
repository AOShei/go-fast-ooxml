//! rsooxml extracts the text of every file in a list with one Rust tool, as
//! gooxml does with go-fast-ooxml and run_py.py with a Python tool: one
//! process, WORKERS threads reading the files in turn (interleaved), the time
//! of each call (after the bytes are read), whether it failed, and the length
//! of the text; the text goes to OUTDIR/text/NAME.txt, one line a file to
//! OUTDIR/results.jsonl. A panic in a tool is caught and counted as a failure.
//!
//!     rsooxml TOOL LIST OUTDIR WORKERS
//!
//! TOOL: calamine (xlsx), docx-rs (docx), pptx-to-md (pptx), kreuzberg (all).
//! kreuzberg is built as its own binary (../rskreuzberg, feature kz): it
//! bundles calamine 0.36 and needs zip 8, while calamine 0.38 needs zip 9.
use std::io::{BufRead, Cursor, Write};
use std::path::Path;
use std::sync::{Arc, Mutex};
use std::time::Instant;

#[cfg(feature = "native")]
fn calamine(path: &Path, b: Vec<u8>) -> Result<String, String> {
    use calamine::{open_workbook_auto_from_rs, Reader};
    let _ = path;
    let mut wb = open_workbook_auto_from_rs(Cursor::new(b)).map_err(|e| e.to_string())?;
    let mut out = String::new();
    for name in wb.sheet_names().to_owned() {
        let range = wb.worksheet_range(&name).map_err(|e| e.to_string())?;
        for row in range.rows() {
            let cells: Vec<String> = row.iter().map(|c| c.to_string()).collect();
            out.push_str(&cells.join("\t"));
            out.push('\n');
        }
        out.push('\n');
    }
    Ok(out)
}

#[cfg(feature = "native")]
fn docx_rs(path: &Path, b: Vec<u8>) -> Result<String, String> {
    use docx_rs::*;
    let _ = path;
    fn runs(children: &[ParagraphChild], out: &mut String) {
        for c in children {
            match c {
                ParagraphChild::Run(r) => run(r, out),
                ParagraphChild::Hyperlink(h) => runs(&h.children, out),
                ParagraphChild::Insert(i) => {
                    for c in &i.children {
                        if let InsertChild::Run(r) = c {
                            run(r, out)
                        }
                    }
                }
                _ => {}
            }
        }
    }
    fn run(r: &Run, out: &mut String) {
        for c in &r.children {
            match c {
                RunChild::Text(t) => out.push_str(&t.text),
                RunChild::Tab(_) => out.push('\t'),
                RunChild::Break(_) => out.push('\n'),
                _ => {}
            }
        }
    }
    fn table(t: &Table, out: &mut String) {
        for TableChild::TableRow(row) in &t.rows {
            for TableRowChild::TableCell(cell) in &row.cells {
                for c in &cell.children {
                    match c {
                        TableCellContent::Paragraph(p) => {
                            runs(&p.children, out);
                            out.push('\t');
                        }
                        TableCellContent::Table(t) => table(t, out),
                        _ => {}
                    }
                }
            }
            out.push('\n');
        }
    }
    let docx = read_docx_with_options(&b, ReadDocxOptions::default().with_image_previews(false))
        .map_err(|e| e.to_string())?;
    let mut out = String::new();
    for c in &docx.document.children {
        match c {
            DocumentChild::Paragraph(p) => {
                runs(&p.children, &mut out);
                out.push('\n');
            }
            DocumentChild::Table(t) => table(t, &mut out),
            _ => {}
        }
    }
    Ok(out)
}

#[cfg(feature = "native")]
fn pptx_to_md(path: &Path, _b: Vec<u8>) -> Result<String, String> {
    use pptx_to_md::{ParserConfig, PresentationContainer};
    // Images are not extracted (the default embeds each as base64), so that
    // the tool does what the others do: the text.
    let config = ParserConfig::builder().extract_images(false).build();
    let mut p = PresentationContainer::open(path, config).map_err(|e| e.to_string())?;
    p.convert_to_md().map_err(|e| e.to_string())
}

#[cfg(feature = "kz")]
fn kreuzberg(path: &Path, b: Vec<u8>) -> Result<String, String> {
    let mime = match path.extension().and_then(|e| e.to_str()).unwrap_or("") {
        "docx" => "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
        "xlsx" => "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
        "pptx" => "application/vnd.openxmlformats-officedocument.presentationml.presentation",
        e => return Err(format!("no media type for .{e}")),
    };
    let config = kreuzberg::ExtractionConfig::default();
    kreuzberg::extract_bytes_sync(&b, mime, &config)
        .map(|r| r.content)
        .map_err(|e| e.to_string())
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    let (tool, list, out) = (a[1].clone(), a[2].clone(), a[3].clone());
    let workers: usize = a[4].parse().unwrap();
    let f: fn(&Path, Vec<u8>) -> Result<String, String> = match tool.as_str() {
        #[cfg(feature = "native")]
        "calamine" => calamine,
        #[cfg(feature = "native")]
        "docx-rs" => docx_rs,
        #[cfg(feature = "native")]
        "pptx-to-md" => pptx_to_md,
        #[cfg(feature = "kz")]
        "kreuzberg" => kreuzberg,
        t => panic!("unknown tool {t}"),
    };
    let files: Vec<String> = std::io::BufReader::new(std::fs::File::open(&list).unwrap())
        .lines()
        .map(|l| l.unwrap())
        .filter(|l| !l.is_empty())
        .collect();
    let text_dir = Path::new(&out).join("text");
    std::fs::create_dir_all(&text_dir).unwrap();
    let files = Arc::new(files);
    let results = Arc::new(Mutex::new(vec![String::new(); files.len()]));
    // A tool's panic is counted as a failure of that file; keep the default
    // hook from printing each one.
    std::panic::set_hook(Box::new(|_| {}));
    let mut threads = Vec::new();
    for k in 0..workers {
        let (files, results, text_dir) = (files.clone(), results.clone(), text_dir.clone());
        threads.push(std::thread::spawn(move || {
            let mut i = k;
            while i < files.len() {
                let path = Path::new(&files[i]);
                let b = std::fs::read(path).unwrap();
                let name = path.file_name().unwrap().to_string_lossy().to_string();
                let t0 = Instant::now();
                let r = std::panic::catch_unwind(|| f(path, b)).unwrap_or_else(|_| Err("panic".into()));
                let s = t0.elapsed().as_secs_f64();
                let (err, chars) = match &r {
                    Ok(t) => {
                        std::fs::write(text_dir.join(format!("{name}.txt")), t).unwrap();
                        (serde_json::Value::Null, t.chars().count())
                    }
                    Err(e) => (serde_json::Value::String(e.clone()), 0),
                };
                let line = serde_json::json!({"file": name, "s": s, "err": err, "chars": chars}).to_string();
                results.lock().unwrap()[i] = line;
                i += workers;
            }
        }));
    }
    for t in threads {
        t.join().unwrap();
    }
    let mut w = std::fs::File::create(Path::new(&out).join("results.jsonl")).unwrap();
    for l in results.lock().unwrap().iter() {
        writeln!(w, "{l}").unwrap();
    }
}
