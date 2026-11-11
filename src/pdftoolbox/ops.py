"""PDF operations: merge, split, rotate, page numbers, watermark, blank-page
removal, info. Every function validates its inputs, raises OpError with a
user-readable message, and saves atomically (tmp file + os.replace), so the
output path may safely equal the input path."""

from __future__ import annotations

import io
from dataclasses import dataclass, field
from pathlib import Path

import pymupdf
from PIL import Image

from .common import OpError, format_pages, parse_color, parse_pages, resolve_position, save_pdf
from .ingest import WordConverter, open_any

#: Placeholder tokens supported by number/watermark format strings.
FORMAT_TOKENS = ("{n}", "{t}")


@dataclass
class OpResult:
    """Uniform result of every operation."""

    output: str = ""
    pages: int = 0
    messages: list[str] = field(default_factory=list)
    details: dict = field(default_factory=dict)


def _open_src(src: Path | str) -> "pymupdf.Document":
    """Open an existing PDF (in-memory, so in-place saves are safe)."""
    src = Path(src)
    if not src.exists():
        raise OpError(f"input not found: {src}")
    if src.suffix.lower() != ".pdf":
        raise OpError(f"{src.name}: this operation takes a PDF input "
                      f"(use 'merge' to combine Word/images into a PDF)")
    return open_any(src)


def _default_output(src: Path, suffix: str) -> Path:
    return src.with_name(f"{src.stem}-{suffix}.pdf")


# --------------------------------------------------------------------- merge
def merge(
    inputs: list[Path] | list[str],
    output: Path | str,
    dpi: int = 96,
    word_timeout: int = 300,
    converter: WordConverter | None = None,
) -> OpResult:
    """Merge PDFs, Word documents and images into one PDF, in the given order."""
    inputs = [Path(p) for p in inputs]
    if len(inputs) < 1:
        raise OpError("merge needs at least one input file")
    out = pymupdf.open()
    result = OpResult(output=str(output))
    total_pages = 0
    try:
        for src in inputs:
            doc = open_any(src, dpi=dpi, word_timeout=word_timeout, converter=converter)
            try:
                n = doc.page_count
                out.insert_pdf(doc)
                total_pages += n
                result.messages.append(f"{src.name}: {n} page(s)")
            finally:
                doc.close()
        if out.page_count == 0:
            raise OpError("merge produced 0 pages (all inputs empty?)")
        save_pdf(out, output)
        out = None  # save_pdf closed it
    finally:
        if out is not None and not out.is_closed:
            out.close()
    result.pages = total_pages
    return result


# --------------------------------------------------------------------- split
def _sanitize(name: str) -> str:
    keep = "-_."
    return "".join(c if (c.isalnum() or c in keep) else "" for c in name) or "part"


def split(
    src: Path | str,
    out_dir: Path | str,
    per_page: bool = False,
    ranges: str | None = None,
    prefix: str | None = None,
) -> OpResult:
    """Split a PDF: one file per page, or one file per comma-separated range."""
    src = Path(src)
    doc = _open_src(src)
    try:
        total = doc.page_count
        out_dir = Path(out_dir)
        out_dir.mkdir(parents=True, exist_ok=True)
        prefix = prefix or src.stem
        outputs: list[str] = []

        def emit(indices: list[int], name: str) -> None:
            part = pymupdf.open()
            try:
                for idx in indices:
                    part.insert_pdf(doc, from_page=idx, to_page=idx)
                target = out_dir / name
                save_pdf(part, target)
                part = None
                outputs.append(str(target))
            finally:
                if part is not None and not part.is_closed:
                    part.close()

        if per_page:
            for i in range(total):
                emit([i], f"{_sanitize(prefix)}-{i + 1:04d}.pdf")
        elif ranges:
            for seg in str(ranges).split(","):
                seg = seg.strip()
                if not seg:
                    continue
                indices = parse_pages(seg, total)  # validates one segment
                emit(indices, f"{_sanitize(prefix)}-{_sanitize(seg.replace(' ', ''))}.pdf")
        else:
            raise OpError("split needs --per-page or --ranges")
        result = OpResult(output=str(out_dir), pages=total,
                          details={"files": outputs, "count": len(outputs)})
        result.messages = [f"{len(outputs)} file(s) written to {out_dir}"]
        return result
    finally:
        doc.close()


# -------------------------------------------------------------------- rotate
def rotate(src: Path | str, output: Path | str | None, angle: int, pages: str | None = None) -> OpResult:
    """Rotate pages clockwise by angle (90/180/270, negatives allowed)."""
    if angle % 90 != 0:
        raise OpError(f"angle must be a multiple of 90, got {angle}")
    angle = angle % 360
    if angle == 0:
        raise OpError("angle 0 rotates nothing (use 90, 180 or 270)")
    src = Path(src)
    output = Path(output) if output else _default_output(src, f"rot{angle}")
    doc = _open_src(src)
    try:
        total = doc.page_count
        indices = parse_pages(pages, total)
        for i in indices:
            page = doc[i]
            page.set_rotation((page.rotation + angle) % 360)
        save_pdf(doc, output)
        doc = None
    finally:
        if doc is not None and not doc.is_closed:
            doc.close()
    return OpResult(output=str(output), pages=len(indices),
                    messages=[f"rotated page(s) {format_pages(indices, total)} by {angle}°"])


# ------------------------------------------------------------- page numbering
def _font_for(text: str) -> str:
    """ASCII text uses Helvetica; anything else uses the builtin CJK font so
    Chinese formats like '第 {n} 页' render (and stay searchable)."""
    return "helv" if text.isascii() else "china-s"


def _text_width(text: str, fontname: str, fontsize: float) -> float:
    try:
        return pymupdf.get_text_length(text, fontname=fontname, fontsize=fontsize)
    except Exception:
        # Fallback estimate when metrics for the font are unavailable.
        return fontsize * 0.55 * len(text)


def number_pages(
    src: Path | str,
    output: Path | str | None,
    start_page: int = 1,
    first_number: int = 1,
    fmt: str = "{n}",
    position: str = "bottom-center",
    margin: float = 24,
    fontsize: float = 11,
    color: str | None = "black",
    opacity: float = 1.0,
) -> OpResult:
    """Stamp page numbers from ``start_page`` onward.

    ``fmt`` may contain {n} (the page's number value) and {t} (total pages of
    the document). Number value of page P = first_number + (P - start_page).
    """
    if "{n}" not in fmt:
        raise OpError("number format must contain {n}")
    if start_page < 1:
        raise OpError(f"start-page must be >= 1, got {start_page}")
    if not 0 < opacity <= 1:
        raise OpError(f"opacity must be in (0, 1], got {opacity}")
    src = Path(src)
    output = Path(output) if output else _default_output(src, "numbered")
    fx, fy = resolve_position(position)
    rgb = parse_color(color, (0.0, 0.0, 0.0))
    doc = _open_src(src)
    try:
        total = doc.page_count
        if start_page > total:
            raise OpError(f"start-page {start_page} is beyond the last page ({total})")
        stamped = 0
        for i in range(start_page - 1, total):
            page = doc[i]
            n = first_number + (i - (start_page - 1))
            text = fmt.format(n=n, t=total)
            fontname = _font_for(text)
            tw = _text_width(text, fontname, fontsize)
            rect = page.rect
            usable_w = max(rect.width - 2 * margin, tw)
            x = rect.x0 + margin + fx * (usable_w - tw)
            if fy >= 1.0:
                y = rect.y1 - margin
            elif fy <= 0.0:
                y = rect.y0 + margin + fontsize
            else:
                y = rect.y0 + rect.height / 2 + fontsize / 3
            page.insert_text(
                (x, y), text, fontsize=fontsize, fontname=fontname,
                color=rgb, fill_opacity=opacity, overlay=True,
            )
            stamped += 1
        save_pdf(doc, output)
        doc = None
    finally:
        if doc is not None and not doc.is_closed:
            doc.close()
    return OpResult(output=str(output), pages=stamped,
                    messages=[f"numbered pages {start_page}-{stamped + start_page - 1} "
                              f"(first number {first_number}, format {fmt!r})"])


# ----------------------------------------------------------------- watermark
def _image_bytes_with_opacity(path: Path, opacity: float) -> bytes:
    img = Image.open(path)
    img = img.convert("RGBA")
    if opacity < 1.0:
        alpha = img.getchannel("A").point(lambda a: int(a * opacity))
        img.putalpha(alpha)
    buf = io.BytesIO()
    img.save(buf, format="PNG")
    return buf.getvalue()


def watermark(
    src: Path | str,
    output: Path | str | None,
    text: str | None = None,
    image: str | None = None,
    pages: str | None = None,
    opacity: float = 0.25,
    angle: float = 45,
    fontsize: float = 36,
    color: str | None = "gray",
    position: str = "center",
    tile: bool = False,
    image_scale: float = 0.25,
) -> OpResult:
    """Stamp a text or image watermark on the selected pages.

    Text mode: single centered rotated stamp, or a tiled grid when tile=True.
    Image mode: scaled to ``image_scale`` of page width, anchored at position.
    """
    if bool(text) == bool(image):
        raise OpError("watermark needs exactly one of --text or --image")
    if not 0 < opacity <= 1:
        raise OpError(f"opacity must be in (0, 1], got {opacity}")
    src = Path(src)
    output = Path(output) if output else _default_output(src, "watermark")
    rgb = parse_color(color, (0.6, 0.6, 0.6))
    fx, fy = resolve_position(position)
    img_stream: bytes | None = None
    img_aspect = 1.0
    if image:
        img_path = Path(image)
        if not img_path.exists():
            raise OpError(f"watermark image not found: {img_path}")
        img_stream = _image_bytes_with_opacity(img_path, opacity)
        with Image.open(img_path) as probe:
            img_aspect = probe.height / max(probe.width, 1)
        opacity_for_insert = 1.0  # alpha already baked into the PNG
    else:
        opacity_for_insert = opacity

    doc = _open_src(src)
    try:
        total = doc.page_count
        indices = parse_pages(pages, total)
        for i in indices:
            page = doc[i]
            rect = page.rect
            if text:
                fontname = _font_for(text)
                tw = _text_width(text, fontname, fontsize)
                pivot = pymupdf.Matrix(angle)
                if tile:
                    step_x = tw * abs(pymupdf.Matrix(angle).a) + abs(pymupdf.Matrix(angle).b) * fontsize + fontsize * 3
                    step_y = fontsize * 4
                    # simple diagonal grid covering the page
                    y = rect.y0
                    row = 0
                    while y < rect.y1 + step_y:
                        x = rect.x0 + (step_x / 2 if row % 2 else 0)
                        while x < rect.x1 + step_x:
                            p = pymupdf.Point(x, y)
                            page.insert_text(
                                p, text, fontsize=fontsize, fontname=fontname,
                                color=rgb, fill_opacity=opacity_for_insert,
                                morph=(p, pivot), overlay=True,
                            )
                            x += step_x
                        y += step_y
                        row += 1
                else:
                    cx = rect.x0 + margin_x(rect, fx, tw)
                    cy = (rect.y0 + fontsize if fy <= 0 else
                          rect.y1 - fontsize / 2 if fy >= 1 else
                          rect.y0 + rect.height / 2 + fontsize / 3)
                    p = pymupdf.Point(cx, cy)
                    center = pymupdf.Point(rect.x0 + rect.width / 2, rect.y0 + rect.height / 2)
                    page.insert_text(
                        p, text, fontsize=fontsize, fontname=fontname,
                        color=rgb, fill_opacity=opacity_for_insert,
                        morph=(center if fx == 0.5 and 0 < fy < 1 else p, pivot),
                        overlay=True,
                    )
            else:
                w = rect.width * image_scale
                h = w * img_aspect
                if h > rect.height * 0.9:
                    h = rect.height * 0.9
                    w = h / img_aspect
                x = rect.x0 + fx * (rect.width - w)
                y = rect.y0 + fy * (rect.height - h)
                page.insert_image(
                    pymupdf.Rect(x, y, x + w, y + h),
                    stream=img_stream, overlay=True, keep_proportion=True,
                )
        save_pdf(doc, output)
        doc = None
    finally:
        if doc is not None and not doc.is_closed:
            doc.close()
    kind = f"text {text!r}" if text else f"image {Path(str(image)).name}"
    return OpResult(output=str(output), pages=len(indices),
                    messages=[f"watermark ({kind}, opacity {opacity}, angle {angle}°) "
                              f"on {format_pages(indices, total)}"])


def margin_x(rect, fx: float, text_width: float) -> float:
    """Horizontal placement of a watermark text baseline origin."""
    gap = 36
    if fx <= 0:
        return gap
    if fx >= 1:
        return rect.width - text_width - gap
    return (rect.width - text_width) / 2


# --------------------------------------------------------------- blank pages
def find_blank_pages(
    doc: "pymupdf.Document", threshold: float = 0.002, dpi: int = 48
) -> list[int]:
    """0-based indices of visually blank pages.

    A page is blank when it has no extractable text AND its low-res raster
    contains less than ``threshold`` of ink (non-near-white pixel fraction).
    Text pages are never dropped, even white-on-white text: deleting them
    could destroy invisible-but-real content.
    """
    blanks: list[int] = []
    for i, page in enumerate(doc):
        if page.get_text().strip():
            continue
        pix = page.get_pixmap(dpi=dpi, colorspace=pymupdf.csGRAY)
        total_px = pix.width * pix.height
        if total_px == 0:
            blanks.append(i)
            continue
        img = Image.frombytes("L", (pix.width, pix.height), pix.samples)
        hist = img.histogram()
        ink = sum(hist[:245]) / total_px
        if ink < threshold:
            blanks.append(i)
    return blanks


def clean_blank(
    src: Path | str,
    output: Path | str | None = None,
    threshold: float = 0.002,
    dpi: int = 48,
    dry_run: bool = False,
) -> OpResult:
    """Remove visually blank pages. dry_run only reports them."""
    if not 0 < threshold < 1:
        raise OpError(f"threshold must be in (0,1), got {threshold}")
    src = Path(src)
    doc = _open_src(src)
    try:
        total = doc.page_count
        blanks = find_blank_pages(doc, threshold=threshold, dpi=dpi)
        result = OpResult(pages=total, details={"blank_pages": [b + 1 for b in blanks]})
        if dry_run:
            result.messages = [f"{len(blanks)} blank page(s): {[b + 1 for b in blanks] or 'none'}"]
            return result
        if not blanks:
            result.output = str(src)
            result.messages = ["no blank pages found; output identical to input"]
            if output:
                save_pdf(doc, output)
                doc = None
                result.output = str(output)
            return result
        if len(blanks) == total:
            raise OpError(
                f"all {total} pages are blank; refusing to produce an empty PDF"
            )
        keep = [i for i in range(total) if i not in set(blanks)]
        doc.select(keep)
        target = Path(output) if output else _default_output(src, "clean")
        save_pdf(doc, target)
        doc = None
        result.output = str(target)
        result.pages = len(keep)
        result.messages = [f"removed {len(blanks)} blank page(s) {[b + 1 for b in blanks]}; "
                           f"{len(keep)} page(s) kept"]
        return result
    finally:
        if doc is not None and not doc.is_closed:
            doc.close()


# ---------------------------------------------------------------------- info
def pdf_info(src: Path | str) -> OpResult:
    """Summarize a PDF: pages, sizes, rotation, text presence, metadata."""
    src = Path(src)
    doc = _open_src(src)
    try:
        sizes: dict[str, int] = {}
        text_pages = 0
        rotated = 0
        for page in doc:
            key = f"{page.rect.width:.0f}x{page.rect.height:.0f}pt"
            sizes[key] = sizes.get(key, 0) + 1
            if page.get_text().strip():
                text_pages += 1
            if page.rotation:
                rotated += 1
        meta = {k: v for k, v in (doc.metadata or {}).items() if v}
        details = {
            "file": str(src),
            "bytes": src.stat().st_size,
            "pages": doc.page_count,
            "page_sizes": sizes,
            "rotated_pages": rotated,
            "pages_with_text": text_pages,
            "encrypted": doc.is_encrypted,
            "metadata": meta,
        }
        result = OpResult(output=str(src), pages=doc.page_count, details=details)
        result.messages = [
            f"{doc.page_count} pages, {src.stat().st_size / 1e6:.2f} MB, "
            f"{text_pages} page(s) with text, sizes: {sizes}"
        ]
        return result
    finally:
        doc.close()
