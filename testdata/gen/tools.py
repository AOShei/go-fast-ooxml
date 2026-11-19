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


# Adapters that are test oracles only: they do not extract text, so they are

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

# not part of the baseline measurements.
ORACLES = {
    "openpyxl-cells": ("openpyxl", openpyxl_cells, {"xlsx"}),
    "openpyxl-formulas": ("openpyxl", openpyxl_formulas, {"xlsx"}),
    "python-pptx-slides": ("python-pptx", python_pptx_slides, {"pptx"}),
    "msoffcrypto-decrypt": ("msoffcrypto-tool", msoffcrypto_decrypt, {"docx", "xlsx", "pptx"}),
}


def tool_version(tool):
    return version({**TOOLS, **ORACLES}[tool][0])
