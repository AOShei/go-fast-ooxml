"""Text extraction with the tools people use today, one adapter per tool.

Shared by oracle.py (one file, for the Go tests) and bench/run_baseline.py
(timings). Every adapter takes a path and returns plain text. Adapters import
their library lazily so that one tool can be measured without loading the
others.
"""
from importlib.metadata import version


def python_docx(path):
    import docx
    from docx.table import Table

    out = []

    def table_text(tbl):
        for row in tbl.rows:
            seen = set()
            cells = []
            for cell in row.cells:
                # A merged cell is returned once per grid column it covers.
                if id(cell._tc) in seen:
                    continue
                seen.add(id(cell._tc))
                cells.append(cell.text)
            out.append("\t".join(cells))

    d = docx.Document(path)
    for item in d.iter_inner_content():
        if isinstance(item, Table):
            table_text(item)
        else:
            out.append(item.text)
    return "\n".join(out)


def docx2txt_(path):
    import docx2txt

    return docx2txt.process(path)


def _openpyxl(path, read_only):
    import openpyxl

    wb = openpyxl.load_workbook(path, read_only=read_only, data_only=True)
    out = []
    for ws in wb.worksheets:
        out.append(ws.title)
        for row in ws.iter_rows(values_only=True):
            cells = ["" if v is None else str(v) for v in row]
            if any(cells):
                out.append("\t".join(cells))
    wb.close()
    return "\n".join(out)


def openpyxl_(path):
    return _openpyxl(path, read_only=False)


def openpyxl_readonly(path):
    return _openpyxl(path, read_only=True)


def openpyxl_cells(path):
    """What openpyxl knows about a workbook's cells beyond their text, as
    JSON. Per sheet: the cells that hold dates or times, with their values
    in ISO 8601; the cells whose value a formula gives, including every cell
    an array formula fills; and the ranges pivot tables are written to. It
    is the oracle for dates, which this library writes in ISO 8601 and
    LibreOffice as displayed, and it says where LibreOffice may show
    something the file does not store: it recalculates formulas and lays
    pivot tables out again.
    """
    import datetime as dt
    import json

    import openpyxl
    from openpyxl.utils import range_boundaries, get_column_letter

    def cells_of(ref, limit=200000):
        try:
            c0, r0, c1, r1 = range_boundaries(ref)
        except Exception:
            return []
        if None in (c0, r0, c1, r1) or (c1 - c0 + 1) * (r1 - r0 + 1) > limit:
            return []
        return [f"{get_column_letter(c)}{r}" for r in range(r0, r1 + 1) for c in range(c0, c1 + 1)]

    values = openpyxl.load_workbook(path, data_only=True)
    formulas = openpyxl.load_workbook(path, data_only=False)
    out = []
    for ws in values.worksheets:
        dates, has_formula, pivots = {}, [], []
        for row in ws.iter_rows():
            for cell in row:
                v = cell.value
                if isinstance(v, (dt.datetime, dt.date, dt.time)):
                    dates[cell.coordinate] = v.isoformat()
                elif isinstance(v, dt.timedelta):
                    dates[cell.coordinate] = "duration"
        if ws.title in formulas.sheetnames:
            fs = formulas[ws.title]
            for row in fs.iter_rows():
                for cell in row:
                    if cell.data_type == "f":
                        has_formula.append(cell.coordinate)
                        ref = getattr(cell.value, "ref", None)
                        if ref:
                            has_formula.extend(cells_of(ref))
            for pivot in getattr(fs, "_pivots", []):
                ref = getattr(getattr(pivot, "location", None), "ref", None)
                if ref:
                    pivots.append(ref)
        out.append({"name": ws.title, "dates": dates, "formulas": has_formula, "pivots": pivots})
    return json.dumps(out)


def python_pptx(path):
    from pptx import Presentation

    out = []

    def shape_text(shape):
        if shape.shape_type == 6:  # group
            for s in shape.shapes:
                shape_text(s)
        elif getattr(shape, "has_table", False) and shape.has_table:
            for row in shape.table.rows:
                out.append("\t".join(c.text for c in row.cells))
        elif shape.has_text_frame:
            out.append(shape.text_frame.text)

    prs = Presentation(path)
    for slide in prs.slides:
        for shape in slide.shapes:
            shape_text(shape)
        if slide.has_notes_slide:
            out.append(slide.notes_slide.notes_text_frame.text)
    return "\n".join(out)


def python_pptx_slides(path):
    """What python-pptx reads from each slide, as JSON: the text of its
    shapes apart from the date, footer and slide-number placeholders, the
    text of those, its title and its speaker's notes. It is the oracle for
    the words of a presentation: the plain adapter above mixes the slide's
    furniture into its text, and this library tags furniture.
    """
    import json

    from pptx import Presentation
    from pptx.enum.shapes import PP_PLACEHOLDER

    furniture_types = {PP_PLACEHOLDER.DATE, PP_PLACEHOLDER.FOOTER, PP_PLACEHOLDER.SLIDE_NUMBER, PP_PLACEHOLDER.HEADER}

    def walk(shape, body, furniture):
        if shape.shape_type == 6:  # group
            for s in shape.shapes:
                walk(s, body, furniture)
        elif getattr(shape, "has_table", False) and shape.has_table:
            for row in shape.table.rows:
                body.append("\t".join(c.text for c in row.cells))
        elif shape.has_text_frame:
            kind = None
            if shape.is_placeholder:
                try:
                    kind = shape.placeholder_format.type
                except Exception:
                    kind = None
            (furniture if kind in furniture_types else body).append(shape.text_frame.text)

    out = []
    for slide in Presentation(path).slides:
        body, furniture = [], []
        for shape in slide.shapes:
            walk(shape, body, furniture)
        title = None
        try:
            if slide.shapes.title is not None and slide.shapes.title.has_text_frame:
                title = slide.shapes.title.text_frame.text
        except Exception:
            title = None
        notes = ""
        if slide.has_notes_slide and slide.notes_slide.notes_text_frame is not None:
            notes = slide.notes_slide.notes_text_frame.text
        out.append({"body": body, "furniture": furniture, "title": title, "notes": notes,
                    "hidden": slide._element.get("show") in ("0", "false")})
    return json.dumps(out, ensure_ascii=False)


# The passwords of the encrypted files in the corpus and of the encrypted
# fixtures. They are test passwords, published with the files.
PASSWORDS = ["VelvetSweatshop", "pass", "solrcell", "tika", "correct horse", "s3cret", "p\u00e4ssw\u00f6rd \u00fc", "secret", "Test001!!", "pwd123", "this is a test"]


def msoffcrypto_decrypt(path):
    """Decrypt a password-protected file with msoffcrypto-tool, trying the
    known test passwords, and report as JSON which password opened it and the
    SHA-256 and length of what it decrypts to. It is the oracle for
    decryption: this library must decrypt to the same bytes.
    """
    import hashlib
    import io
    import json

    import msoffcrypto

    refused, failed = "no password tried", ""
    for password in PASSWORDS:
        with open(path, "rb") as f:
            office = msoffcrypto.OfficeFile(f)
            try:
                office.load_key(password=password, verify_password=True)
            except Exception as e:
                refused = f"{type(e).__name__}: {e}"
                continue
            try:
                out = io.BytesIO()
                office.decrypt(out)
            except Exception as e:  # the password was taken, and decrypting failed
                failed = f"{type(e).__name__}: {e}"
                continue
        data = out.getvalue()
        return json.dumps({"password": password, "sha256": hashlib.sha256(data).hexdigest(), "size": len(data)})
    raise ValueError("no known password opens it: " + (failed or refused))


_markitdown = None


def markitdown_(path):
    global _markitdown
    if _markitdown is None:
        from markitdown import MarkItDown

        _markitdown = MarkItDown()
    return _markitdown.convert(path).text_content


# tool name -> (distribution name, adapter, formats)
TOOLS = {
    "python-docx": ("python-docx", python_docx, {"docx"}),
    "docx2txt": ("docx2txt", docx2txt_, {"docx"}),
    "openpyxl": ("openpyxl", openpyxl_, {"xlsx"}),
    "openpyxl-readonly": ("openpyxl", openpyxl_readonly, {"xlsx"}),
    "python-pptx": ("python-pptx", python_pptx, {"pptx"}),
    "markitdown": ("markitdown", markitdown_, {"docx", "xlsx", "pptx"}),
}


def openpyxl_formulas(path):
    """The formula text and the hyperlink of each cell, as openpyxl reads
    them, as JSON: per sheet, by cell reference, the formula without its
    "=" (a shared formula as openpyxl moves it to the cell; an array
    formula in its first cell) and where the cell links to (an address, or
    "#" and a place in the workbook). It is the oracle for Table.Cells.
    """
    import json

    import openpyxl
    from openpyxl.worksheet.formula import ArrayFormula, DataTableFormula

    wb = openpyxl.load_workbook(path, data_only=False)
    out = []
    for ws in wb.worksheets:
        formulas, links = {}, {}
        for row in ws.iter_rows():
            for c in row:
                v = c.value
                if isinstance(v, ArrayFormula):
                    if v.text:
                        formulas[c.coordinate] = v.text[1:] if v.text.startswith("=") else v.text
                elif isinstance(v, DataTableFormula):
                    pass
                elif isinstance(v, str) and c.data_type == "f" and v.startswith("="):
                    formulas[c.coordinate] = v[1:]
                h = c.hyperlink
                if h is not None:
                    if h.target:
                        links[c.coordinate] = h.target
                    elif h.location:
                        links[c.coordinate] = "#" + h.location
        out.append({"sheet": ws.title, "formulas": formulas, "links": links})
    return json.dumps(out)


def openpyxl_validations(path):
    """The drop-down lists of a workbook, as openpyxl reads them, as JSON:
    per sheet, the cells each list is for, its first formula, and whether
    its arrow is hidden. openpyxl reads the lists in the sheet's own
    element only, not the newer ones kept in its extension list. It is
    the oracle for notes of type choices.
    """
    import json

    import openpyxl

    wb = openpyxl.load_workbook(path)
    out = []
    for ws in wb.worksheets:
        lists = []
        for dv in ws.data_validations.dataValidation:
            if dv.type != "list" or dv.formula1 is None:
                continue
            lists.append({"cells": sorted(str(r) for r in dv.sqref.ranges), "formula": dv.formula1,
                          "hidden_arrow": bool(dv.showDropDown)})
        out.append({"sheet": ws.title, "lists": lists})
    return json.dumps(out, ensure_ascii=False)


def _number(v):
    if v is None:
        return None
    try:
        return float(v)
    except (TypeError, ValueError):
        return None


def python_pptx_charts(path):
    """The charts of each slide, as python-pptx reads them, as JSON: per
    slide, per chart in the order of the shapes, the name of each series,
    the categories (the innermost level), the values as numbers, and the
    number format of the values. It is the oracle for chart tables in
    presentations.
    """
    import json

    from pptx import Presentation

    def walk(shapes, out):
        for shape in shapes:
            if shape.shape_type == 6:  # group
                walk(shape.shapes, out)
            elif getattr(shape, "has_chart", False) and shape.has_chart:
                chart = shape.chart
                series = []
                for plot in chart.plots:
                    try:
                        cats = [str(c) for c in plot.categories]
                    except Exception:
                        cats = []
                    for ser in plot.series:
                        code = ""
                        cache = ser._element.xpath("./c:val/c:numRef/c:numCache/c:formatCode | ./c:yVal/c:numRef/c:numCache/c:formatCode")
                        if cache:
                            code = cache[0].text or ""
                        try:
                            values = [_number(v) for v in ser.values]
                        except Exception:
                            values = []
                        # A point may have a number format of its own.
                        formats = [None] * len(values)
                        for pt in ser._element.xpath("./c:val/c:numRef/c:numCache/c:pt | ./c:yVal/c:numRef/c:numCache/c:pt"):
                            idx, own = int(pt.get("idx", "-1")), pt.get("formatCode")
                            if own is not None and 0 <= idx < len(formats):
                                formats[idx] = own
                        series.append({"name": ser.name or "", "cats": cats, "vals": values, "format": code, "formats": formats})
                out.append({"name": shape.name, "series": series})

    slides = []
    for slide in Presentation(path).slides:
        charts = []
        walk(slide.shapes, charts)
        slides.append(charts)
    return json.dumps(slides, ensure_ascii=False)


def openpyxl_charts(path):
    """The charts of each worksheet, as openpyxl reads them, as JSON: per
    sheet, per chart, the name of each series, its categories as the cache
    holds them (the innermost level), its values as numbers and their
    number format. It is the oracle for chart tables in workbooks.
    """
    import json

    import openpyxl

    def points(ref):
        """idx -> v of a reference's cache (num or str), innermost level."""
        if ref is None:
            return None, "", []
        for kind in ("numRef", "strRef", "multiLvlStrRef", "numLit", "strLit"):
            r = getattr(ref, kind, None)
            if r is None:
                continue
            cache = r
            if kind.endswith("Ref"):
                cache = getattr(r, "numCache", None) or getattr(r, "strCache", None) or getattr(r, "multiLvlStrCache", None)
            if cache is None:
                return [], "", []
            if kind == "multiLvlStrRef":
                lvls = getattr(cache, "lvl", None) or []
                pts = lvls[0].pt if lvls else []
            else:
                pts = cache.pt or []
            n = getattr(cache, "ptCount", None) or 0
            n = max([n] + [p.idx + 1 for p in pts])
            out, formats = [None] * n, [None] * n
            for p in pts:
                out[p.idx] = p.v
                formats[p.idx] = getattr(p, "formatCode", None)
            return out, getattr(cache, "formatCode", None) or "", formats
        return None, "", []

    wb = openpyxl.load_workbook(path)
    sheets = []
    for ws in list(wb.worksheets) + list(getattr(wb, "chartsheets", [])):
        charts = []
        for chart in getattr(ws, "_charts", []):
            series = []
            plots = [chart] + list(getattr(chart, "_charts", []) or [])
            seen = set()
            for plot in plots:
                for ser in plot.series:
                    if id(ser) in seen:
                        continue
                    seen.add(id(ser))
                    name = ""
                    if ser.tx is not None:
                        if ser.tx.strRef is not None and ser.tx.strRef.strCache is not None and ser.tx.strRef.strCache.pt:
                            # A name from several cells is shown with spaces between.
                            name = " ".join((p.v or "").strip() for p in ser.tx.strRef.strCache.pt).strip()
                        elif ser.tx.v is not None:
                            name = ser.tx.v
                    cats, _, _ = points(ser.cat if ser.cat is not None else getattr(ser, "xVal", None))
                    vals, code, formats = points(ser.val if ser.val is not None else getattr(ser, "yVal", None))
                    series.append({"name": name, "cats": [("" if c is None else str(c)) for c in (cats or [])],
                                   "vals": [_number(v) for v in (vals or [])], "format": code, "formats": formats})
            charts.append({"series": series})
        sheets.append({"sheet": ws.title, "charts": charts})
    return json.dumps(sheets, ensure_ascii=False)


def openpyxl_pivots(path):
    """The pivot tables of a workbook, as openpyxl reads them, as JSON:
    per sheet, each pivot table's name and the range it fills; and the
    caches whose source is not in the workbook (another workbook, a
    connection, scenarios, or a sheet, name or table the workbook does not
    have), judged here from what openpyxl reads, with the names of their
    fields and the number of their records. It is the oracle for pivot
    tables and for the stored part's pivot caches.
    """
    import json

    import openpyxl

    wb = openpyxl.load_workbook(path)
    sheets = {n.lower() for n in wb.sheetnames}
    names = {n.lower() for n in wb.defined_names}
    tables = set()
    for ws in wb.worksheets:
        names |= {n.lower() for n in ws.defined_names}
        tables |= {n.lower() for n in ws.tables}
    out, caches, seen = [], [], set()
    for ws in wb.worksheets:
        pivots = []
        for pt in getattr(ws, "_pivots", []):
            pivots.append({"name": pt.name or "", "ref": pt.location.ref if pt.location is not None else ""})
            cache = pt.cache
            if cache is None or id(cache) in seen:
                continue
            seen.add(id(cache))
            src = cache.cacheSource
            gone = False
            if src is None:
                continue
            if src.type in ("external", "scenario"):
                gone = True
            elif src.type == "worksheet" and src.worksheetSource is not None:
                w = src.worksheetSource
                if getattr(w, "id", None):
                    gone = True
                elif w.sheet:
                    gone = w.sheet.lower() not in sheets
                elif w.name:
                    gone = w.name.lower() not in names and w.name.lower() not in tables
            if not gone:
                continue
            records = None
            if cache.records is not None:
                records = len(cache.records.r or [])
            caches.append({"fields": [f.name or "" for f in cache.cacheFields], "records": records})
        out.append({"sheet": ws.title, "pivots": pivots})
    return json.dumps({"sheets": out, "caches": caches}, ensure_ascii=False)


# Adapters that are test oracles only: they do not extract text, so they are
# not part of the baseline measurements.
ORACLES = {
    "openpyxl-cells": ("openpyxl", openpyxl_cells, {"xlsx"}),
    "openpyxl-formulas": ("openpyxl", openpyxl_formulas, {"xlsx"}),
    "openpyxl-validations": ("openpyxl", openpyxl_validations, {"xlsx"}),
    "python-pptx-slides": ("python-pptx", python_pptx_slides, {"pptx"}),
    "python-pptx-charts": ("python-pptx", python_pptx_charts, {"pptx"}),
    "openpyxl-charts": ("openpyxl", openpyxl_charts, {"xlsx"}),
    "openpyxl-pivots": ("openpyxl", openpyxl_pivots, {"xlsx"}),
    "msoffcrypto-decrypt": ("msoffcrypto-tool", msoffcrypto_decrypt, {"docx", "xlsx", "pptx"}),
}


def tool_version(tool):
    return version({**TOOLS, **ORACLES}[tool][0])
