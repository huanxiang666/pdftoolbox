"""Input ingestion: PDF / images natively, Word via external converter.

Word documents (.doc/.docx/.odt/.rtf/.wps) are converted to PDF through
LibreOffice (``soffice``, all platforms) or — on Windows with MS Word
installed — the ``docx2pdf`` COM bridge. Set ``PDFTOOLBOX_WORD_CONVERTER``
to force a specific soffice binary.
"""

from __future__ import annotations

import io
import os
import shutil
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from pathlib import Path

import pymupdf

from . import IMAGE_EXTENSIONS, PDF_EXTENSIONS, SUPPORTED_INPUT_EXTENSIONS, WORD_EXTENSIONS
from .common import OpError

#: Environment variable that forces a specific soffice executable.
ENV_WORD_CONVERTER = "PDFTOOLBOX_WORD_CONVERTER"

KIND_SOFFICE = "soffice"
KIND_DOCX2PDF = "docx2pdf"

_SOFFICE_CANDIDATES = (
    "soffice",
    "libreoffice",
    "/usr/bin/soffice",
    "/usr/lib/libreoffice/program/soffice",
    "/snap/bin/libreoffice",
    "/opt/libreoffice/program/soffice",
    "/Applications/LibreOffice.app/Contents/MacOS/soffice",
    "C:/Program Files/LibreOffice/program/soffice.exe",
    "C:/Program Files (x86)/LibreOffice/program/soffice.exe",
)

WORD_INSTALL_HINT = """\
No Word converter found — .doc/.docx/.odt/.rtf input requires one of:
  1) LibreOffice (free, all platforms): https://www.libreoffice.org/download/
     Linux: sudo apt install libreoffice-writer  (or libreoffice)
     Then 'soffice' is auto-detected; override with
     PDFTOOLBOX_WORD_CONVERTER=/path/to/soffice
  2) Windows + Microsoft Word: pip install pdftoolbox[word]  (docx2pdf bridge)
PDF and image inputs work without any external tool.
未找到 Word 转换器。转换 .doc/.docx 需要安装 LibreOffice（免费，全平台，
Linux: sudo apt install libreoffice-writer），或 Windows 上安装 MS Word 并
pip install docx2pdf。PDF 与图片输入无需任何外部工具。"""


@dataclass(frozen=True)
class WordConverter:
    kind: str  # KIND_SOFFICE | KIND_DOCX2PDF
    path: str  # soffice executable; empty for docx2pdf

    @property
    def display_name(self) -> str:
        if self.kind == KIND_DOCX2PDF:
            return "docx2pdf (Microsoft Word COM)"
        return f"LibreOffice ({self.path})"


def detect_word_converter() -> WordConverter | None:
    """Locate a Word->PDF converter. Order: env var, soffice, docx2pdf."""
    env = os.environ.get(ENV_WORD_CONVERTER, "").strip()
    if env:
        path = shutil.which(env) or (env if Path(env).is_file() else None)
        if path:
            return WordConverter(kind=KIND_SOFFICE, path=str(path))
        print(
            f"warning: {ENV_WORD_CONVERTER}={env!r} is not an executable file; "
            "falling back to auto-detection",
            file=sys.stderr,
        )
    for candidate in _SOFFICE_CANDIDATES:
        found = shutil.which(candidate) if os.sep not in candidate.replace("/", os.sep) or not candidate.startswith(("/", "C:")) else None
        if found is None:
            p = Path(candidate)
            found = str(p) if p.is_file() else None
        if found:
            return WordConverter(kind=KIND_SOFFICE, path=found)
    if sys.platform == "win32":
        try:
            import docx2pdf  # noqa: F401

            return WordConverter(kind=KIND_DOCX2PDF, path="")
        except ImportError:
            pass
    return None


def convert_word_to_pdf(
    src: Path,
    out_dir: Path,
    converter: WordConverter,
    timeout: int = 300,
) -> Path:
    """Convert one Word document to PDF inside out_dir. Returns the PDF path."""
    src = Path(src)
    out_dir = Path(out_dir)
    out_dir.mkdir(parents=True, exist_ok=True)
    expected = out_dir / (src.stem + ".pdf")
    if expected.exists():
        expected.unlink()

    if converter.kind == KIND_DOCX2PDF:
        try:
            from docx2pdf import convert as docx2pdf_convert
        except ImportError as exc:
            raise OpError(f"docx2pdf unavailable: {exc}") from exc
        try:
            docx2pdf_convert(str(src), str(expected))
        except Exception as exc:
            raise OpError(f"Word conversion failed: {exc.__class__.__name__}: {exc}") from exc
    else:
        # Isolated per-run LibreOffice profile: parallel soffice instances
        # fight over the default user profile and randomly fail.
        with tempfile.TemporaryDirectory(prefix="pdftoolbox-lo-") as profile:
            profile_uri = Path(profile).as_uri()
            cmd = [
                converter.path,
                f"-env:UserInstallation={profile_uri}",
                "--headless",
                "--norestore",
                "--convert-to",
                "pdf",
                "--outdir",
                str(out_dir),
                str(src),
            ]
            try:
                proc = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
            except subprocess.TimeoutExpired as exc:
                raise OpError(f"LibreOffice timed out after {timeout}s on {src.name}") from exc
            except OSError as exc:
                raise OpError(f"failed to run {converter.path}: {exc}") from exc
            detail = ((proc.stderr or "") + (proc.stdout or "")).strip()[-400:]
            if not expected.exists():
                raise OpError(
                    f"LibreOffice produced no PDF for {src.name} (exit {proc.returncode})"
                    + (f": {detail}" if detail else "")
                )
    if not expected.exists():
        raise OpError(f"conversion produced no output for {src.name}")
    return expected


def open_image(src: Path, dpi: int = 96) -> "pymupdf.Document":
    """Load an image file as a single-page PDF document sized at ``dpi``.

    Uses the image's native pixel size (w_pt = px * 72 / dpi). Honors EXIF
    orientation (phone photos) by re-encoding the transposed pixels.
    LIMIT: multi-frame GIF/TIFF use the first frame only.
    """
    if dpi <= 0:
        raise OpError(f"image dpi must be > 0, got {dpi}")
    try:
        from PIL import Image, ImageOps

        with Image.open(src) as im:
            orientation = None
            try:
                orientation = im.getexif().get(0x0112)
            except Exception:
                orientation = None
            if orientation is not None and orientation != 1:
                im2 = ImageOps.exif_transpose(im)
                w_px, h_px = im2.size
                if im2.mode == "P":
                    im2 = im2.convert("RGBA")
                buf = io.BytesIO()
                if im2.mode in ("RGBA", "LA", "P"):
                    im2.save(buf, format="PNG")
                else:
                    if im2.mode != "RGB":
                        im2 = im2.convert("RGB")
                    im2.save(buf, format="JPEG", quality=92)
                data = buf.getvalue()
            else:
                w_px, h_px = im.size
                data = Path(src).read_bytes()
    except OpError:
        raise
    except Exception as exc:
        raise OpError(f"cannot read image {src.name}: {exc.__class__.__name__}: {exc}") from exc

    w_pt = w_px * 72.0 / dpi
    h_pt = h_px * 72.0 / dpi
    try:
        doc = pymupdf.open()
        page = doc.new_page(width=w_pt, height=h_pt)
        page.insert_image(page.rect, stream=data)
        return doc
    except Exception as exc:
        raise OpError(f"cannot embed image {src.name}: {exc.__class__.__name__}: {exc}") from exc


def open_any(
    src: Path | str,
    dpi: int = 96,
    word_timeout: int = 300,
    converter: WordConverter | None = None,
) -> "pymupdf.Document":
    """Open any supported input as an in-memory PDF document.

    Word files are converted through the external converter; the converted
    file is loaded into memory so the temp directory can be released.
    """
    src = Path(src)
    if not src.exists():
        raise OpError(f"input not found: {src}")
    ext = src.suffix.lower()

    if ext in PDF_EXTENSIONS:
        data = src.read_bytes()
        try:
            doc = pymupdf.open(stream=data, filetype="pdf")
        except Exception as exc:
            raise OpError(f"cannot open PDF {src.name}: {exc.__class__.__name__}: {exc}") from exc
        if doc.needs_pass:
            doc.close()
            raise OpError(f"{src.name} is password-protected; decrypt it first")
        return doc

    if ext in IMAGE_EXTENSIONS:
        return open_image(src, dpi=dpi)

    if ext in WORD_EXTENSIONS:
        conv = converter or detect_word_converter()
        if conv is None:
            raise OpError(WORD_INSTALL_HINT.replace("\n", " "))
        with tempfile.TemporaryDirectory(prefix="pdftoolbox-word-") as tmp:
            pdf_path = convert_word_to_pdf(src, Path(tmp), conv, timeout=word_timeout)
            data = pdf_path.read_bytes()
        try:
            return pymupdf.open(stream=data, filetype="pdf")
        except Exception as exc:
            raise OpError(f"converted PDF unreadable for {src.name}: {exc}") from exc

    raise OpError(f"unsupported input type: {src.name} (supported: {', '.join(SUPPORTED_INPUT_EXTENSIONS)})")


def collect_inputs(
    paths: list[str] | tuple[str, ...],
    recursive: bool = False,
) -> tuple[list[Path], list[Path]]:
    """Expand files/directories into an ordered input list for merging.

    Order matters for merge: explicit files keep their given order (and may
    repeat on purpose), directories expand in-place with sorted contents.
    Returns (files, ignored).
    """
    files: list[Path] = []
    ignored: list[Path] = []

    def add_file(p: Path) -> None:
        if p.suffix.lower() in SUPPORTED_INPUT_EXTENSIONS:
            files.append(p)
        else:
            ignored.append(p)

    for raw in paths:
        p = Path(raw).expanduser()
        if p.is_dir():
            pattern = "**/*" if recursive else "*"
            found = False
            for entry in sorted(p.glob(pattern), key=lambda q: str(q).lower()):
                if entry.is_file() and entry.suffix.lower() in SUPPORTED_INPUT_EXTENSIONS:
                    add_file(entry)
                    found = True
            if not found:
                ignored.append(p)
        elif p.is_file():
            add_file(p)
        else:
            ignored.append(p)
    return files, ignored
