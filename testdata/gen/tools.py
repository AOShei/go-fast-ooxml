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
# not part of the baseline measurements.
ORACLES = {
    "openpyxl-cells": ("openpyxl", openpyxl_cells, {"xlsx"}),
}


def tool_version(tool):
    return version({**TOOLS, **ORACLES}[tool][0])
