"""Zero-dependency .docx -> PDF fallback renderer (basic layout subset).

Used when no LibreOffice/Word/WPS PDF-export capability is available (for
example WPS automation builds that ship without the PDF export component).
Supports: paragraphs, heading sizes, bold runs, tables with wrapped cells,
page flow with CJK text (built-in china-s font, searchable output).
Not supported (silently skipped): images, floating objects, complex styles.
"""
from __future__ import annotations

from pathlib import Path

from .common import OpError, ensure_pymupdf, save_pdf

_FONT = "china-s"  # built-in CJK font: Chinese + ASCII, searchable text
_A4 = (595.0, 842.0)


def _wrap(text: str, size: float, width: float, pymupdf) -> list[str]:
    lines: list[str] = []
    for paragraph in text.split("\n"):
        current = ""
        for ch in paragraph:
            trial = current + ch
            if pymupdf.get_text_length(trial, fontname=_FONT, fontsize=size) > width and current:
                lines.append(current)
                current = ch
            else:
                current = trial
        lines.append(current)
    return lines or [""]


def _draw(page, x: float, y: float, line: str, size: float, bold: bool) -> None:
    # render_mode 2 = fill + stroke: visually bolder without duplicating text
    page.insert_text((x, y), line, fontname=_FONT, fontsize=size,
                     render_mode=2 if bold else 0)


def render_docx_to_pdf(src: Path | str, dest: Path | str,
                       fontsize: float = 10.5, margin: float = 56.0) -> Path:
    try:
        import docx as _docx
        from docx.table import Table as _Table
        from docx.text.paragraph import Paragraph as _Paragraph
    except ImportError as exc:
        raise OpError("内置 .docx 渲染器需要 python-docx：pip install python-docx") from exc
    pymupdf = ensure_pymupdf()
    src = Path(src)
    try:
        document = _docx.Document(str(src))
    except Exception as exc:
        raise OpError(f"cannot parse {src.name} as .docx: {exc}") from exc

    out = pymupdf.open()
    page = out.new_page(width=_A4[0], height=_A4[1])
    state = {"y": margin}
    usable = _A4[0] - 2 * margin

    def ensure(space: float) -> None:
        if state["y"] + space > _A4[1] - margin:
            page_new = out.new_page(width=_A4[0], height=_A4[1])
            state["page"] = page_new
            state["y"] = margin

    def current():
        return state.get("page", page)

    def block(text: str, size: float, bold: bool = False, after: float = 6.0) -> None:
        for line in _wrap(text or "", size, usable, pymupdf):
            ensure(size * 1.6)
            _draw(current(), margin, state["y"] + size, line, size, bold)
            state["y"] += size * 1.6
        state["y"] += after

    def table(tbl) -> None:
        rows = tbl.rows
        if not rows:
            return
        ncols = max(len(r.cells) for r in rows)
        colw = usable / max(ncols, 1)
        for row in rows:
            cells = [_wrap(c.text, fontsize - 1, colw - 8, pymupdf) for c in row.cells]
            height = max((len(c) for c in cells), default=1) * (fontsize * 1.5) + 6
            ensure(height)
            pg = current()
            top = state["y"]
            if top + height > _A4[1] - margin:  # re-check after ensure()
                pg = current()
                top = state["y"]
            for i, cell in enumerate(cells):
                x = margin + i * colw
                pg.draw_rect(pymupdf.Rect(x, top, x + colw, top + height), color=(0.7, 0.7, 0.7))
                yy = top + fontsize
                for line in cell:
                    _draw(pg, x + 4, yy, line, fontsize - 1, False)
                    yy += fontsize * 1.5
            state["y"] = top + height
        state["y"] += 6

    for child in document.element.body.iterchildren():
        tag = child.tag.rsplit("}", 1)[-1]
        if tag == "p":
            para = _Paragraph(child, document)
            text = para.text
            style = (para.style.name or "").lower()
            bold = any(r.bold for r in para.runs if r.bold is not None) or style.startswith("heading") or style == "title"
            if style == "title":
                size = fontsize * 2.0
            elif style.startswith("heading"):
                level = "".join(ch for ch in style if ch.isdigit()) or "1"
                size = fontsize * (1.9 - min(int(level), 6) * 0.15)
            else:
                size = fontsize
            block(text, size, bold, after=6 if text.strip() else 2)
        elif tag == "tbl":
            table(_Table(child, document))
    if out.page_count == 0 or (out.page_count == 1 and not state["y"] > margin):
        pass  # empty document still yields one blank page
    save_pdf(out, dest)
    return Path(dest)
