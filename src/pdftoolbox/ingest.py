"""PDF/image ingestion and conversion through installed WPS, Word or LibreOffice."""
from __future__ import annotations

import io
import os
import re
import shutil
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from pathlib import Path

from . import IMAGE_EXTENSIONS, PDF_EXTENSIONS, SUPPORTED_INPUT_EXTENSIONS, WORD_EXTENSIONS
from .common import OpError, ensure_pymupdf
from .office_bridge import convert_com, make_staging_dir, registered_progid

ENV_WORD_CONVERTER = "PDFTOOLBOX_WORD_CONVERTER"
KIND_SOFFICE = "soffice"
KIND_WPS_COM = "wps-com"
KIND_WORD_COM = "word-com"
KIND_BUILTIN = "builtin-docx"  # zero-dependency basic-layout renderer
_REENCODE_EXTS = frozenset({".webp"})
_SOFFICE_CANDIDATES = (
    "soffice", "libreoffice", "/usr/bin/soffice",
    "/usr/lib/libreoffice/program/soffice", "/snap/bin/libreoffice",
    "/opt/libreoffice/program/soffice",
    "/Applications/LibreOffice.app/Contents/MacOS/soffice",
)
WORD_INSTALL_HINT = """\
未找到外部文档转换器；.docx 将使用内置基础排版渲染器（无 Office 时保真度有限）。
.doc/.odt/.rtf/.wps 需要安装 WPS Office、Microsoft Word（Windows 自动检测）或
LibreOffice（跨平台：https://www.libreoffice.org/download/）。
No external Word converter found; .docx uses the built-in basic renderer.
Other Word formats need WPS Office, Microsoft Word or LibreOffice."""


@dataclass(frozen=True)
class WordConverter:
    kind: str
    path: str

    @property
    def display_name(self) -> str:
        if self.kind == KIND_WPS_COM:
            return f"WPS Office (COM {self.path})"
        if self.kind == KIND_WORD_COM:
            return f"Microsoft Word-compatible COM ({self.path})"
        if self.kind == KIND_BUILTIN:
            return "built-in .docx basic-layout renderer"
        return f"LibreOffice ({self.path})"


def detect_wps() -> WordConverter | None:
    progid = registered_progid(("kwps.Application", "WPS.Application"))
    return WordConverter(KIND_WPS_COM, progid) if progid else None


def detect_word() -> WordConverter | None:
    # WPS can register this compatibility ProgID too. Registration alone
    # does not prove that genuine Microsoft Word is installed.
    progid = registered_progid(("Word.Application",))
    return WordConverter(KIND_WORD_COM, progid) if progid else None


def _soffice_candidates() -> list[str]:
    candidates = list(_SOFFICE_CANDIDATES)
    if sys.platform == "win32":
        for name in ("ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"):
            root = os.environ.get(name)
            if root:
                try:
                    candidates.extend(str(p) for p in sorted(
                        Path(root).glob("LibreOffice*/program/soffice.exe")))
                except OSError:
                    pass
    return candidates


def detect_word_converter() -> WordConverter | None:
    """Explicit override, then LibreOffice, then WPS/Word registration."""
    env = os.environ.get(ENV_WORD_CONVERTER, "").strip()
    if env.lower() in ("wps", "word"):
        return detect_wps() if env.lower() == "wps" else detect_word()
    if env:
        path = shutil.which(env) or (env if Path(env).is_file() else None)
        if path:
            return WordConverter(KIND_SOFFICE, str(path))
        print(f"warning: {ENV_WORD_CONVERTER}={env!r} is not an executable file; "
              "falling back to auto-detection", file=sys.stderr)
    for candidate in _soffice_candidates():
        path = shutil.which(candidate) or (candidate if Path(candidate).is_file() else None)
        if path:
            return WordConverter(KIND_SOFFICE, str(path))
    return detect_wps() or detect_word()


def _commit(from_path: Path, to_path: Path) -> None:
    try:
        os.replace(from_path, to_path)
    except OSError:  # cross-volume staging
        shutil.copyfile(from_path, to_path)
        from_path.unlink(missing_ok=True)


def convert_word_to_pdf(src: Path, out_dir: Path, converter: WordConverter,
                        timeout: int = 300) -> Path:
    """Convert to a staged file and commit only a valid, nonempty PDF."""
    src = Path(src).resolve()
    out_dir = Path(out_dir).resolve()
    if not src.is_file():
        raise OpError(f"input not found: {src}")
    if timeout <= 0:
        raise OpError("word-timeout must be > 0")
    if converter.kind not in (KIND_WPS_COM, KIND_WORD_COM, KIND_SOFFICE, KIND_BUILTIN):
        raise OpError(f"unknown converter: {converter.kind}")
    out_dir.mkdir(parents=True, exist_ok=True)
    expected = out_dir / (src.stem + ".pdf")
    if converter.kind == KIND_BUILTIN:
        from .docx_render import render_docx_to_pdf

        return render_docx_to_pdf(src, expected)
    if converter.kind in (KIND_WPS_COM, KIND_WORD_COM):
        try:
            convert_com(src, expected, converter.path, timeout)
            return expected
        except OpError:
            # Some WPS automation builds ship without a PDF export component;
            # keep the toolbox useful with the basic-layout renderer.
            if src.suffix.lower() == ".docx":
                from .docx_render import render_docx_to_pdf

                return render_docx_to_pdf(src, expected)
            raise
    stage = make_staging_dir(out_dir)
    try:
        cmd = [converter.path, f"-env:UserInstallation={(stage / 'profile').as_uri()}",
               "--headless", "--norestore", "--convert-to", "pdf",
               "--outdir", str(stage), str(src)]
        try:
            proc = subprocess.run(cmd, capture_output=True, text=True,
                                  errors="replace", timeout=timeout,
                                  creationflags=0x08000000 if sys.platform == "win32" else 0)
        except subprocess.TimeoutExpired as exc:
            raise OpError(f"LibreOffice timed out after {timeout}s on {src.name}") from exc
        except OSError as exc:
            raise OpError(f"failed to run {converter.path}: {exc}") from exc
        produced = stage / (src.stem + ".pdf")
        if proc.returncode or not produced.exists():
            detail = ((proc.stderr or "") + (proc.stdout or ""))[-400:]
            raise OpError(f"LibreOffice produced no PDF for {src.name} "
                          f"(exit {proc.returncode}): {detail}")
        _validate_pdf(produced, src.name)
        _commit(produced, expected)
    finally:
        shutil.rmtree(stage, ignore_errors=True)
    return expected


def _validate_pdf(path: Path, label: str) -> None:
    engine = ensure_pymupdf()
    try:
        with engine.open(str(path)) as doc:
            if not doc.is_pdf or doc.needs_pass or doc.page_count < 1:
                raise ValueError("empty, encrypted or non-PDF output")
    except Exception as exc:
        raise OpError(f"conversion produced no valid PDF for {label}: {exc}") from exc


def open_image(src: Path, dpi: int = 96) -> "pymupdf.Document":
    """Single-frame image PDF, honoring EXIF orientation and requested DPI."""
    if dpi <= 0:
        raise OpError(f"image dpi must be > 0, got {dpi}")
    pymupdf = ensure_pymupdf()
    try:
        from PIL import Image, ImageOps
        with Image.open(src) as im:
            orientation = im.getexif().get(0x0112)
            if src.suffix.lower() in _REENCODE_EXTS or orientation not in (None, 1):
                im2 = ImageOps.exif_transpose(im)
                w_px, h_px = im2.size
                if im2.mode == "P":
                    im2 = im2.convert("RGBA")
                buf = io.BytesIO()
                if im2.mode in ("RGBA", "LA"):
                    im2.save(buf, format="PNG")
                else:
                    im2.convert("RGB").save(buf, format="JPEG", quality=92)
                data = buf.getvalue()
            else:
                w_px, h_px = im.size
                data = src.read_bytes()
    except Exception as exc:
        raise OpError(f"cannot read image {src.name}: {exc}") from exc
    doc = pymupdf.open()
    try:
        page = doc.new_page(width=w_px * 72.0 / dpi, height=h_px * 72.0 / dpi)
        page.insert_image(page.rect, stream=data)
        return doc
    except Exception as exc:
        doc.close()
        raise OpError(f"cannot embed image {src.name}: {exc}") from exc


def open_any(src: Path | str, dpi: int = 96, word_timeout: int = 300,
             converter: WordConverter | None = None) -> "pymupdf.Document":
    src = Path(src)
    if not src.is_file():
        raise OpError(f"input not found: {src}")
    pymupdf = ensure_pymupdf()
    ext = src.suffix.lower()
    if ext in PDF_EXTENSIONS:
        try:
            doc = pymupdf.open(stream=src.read_bytes(), filetype="pdf")
        except Exception as exc:
            raise OpError(f"cannot open PDF {src.name}: {exc}") from exc
        if doc.needs_pass:
            doc.close()
            raise OpError(f"{src.name} is password-protected; decrypt it first")
        return doc
    if ext in IMAGE_EXTENSIONS:
        return open_image(src, dpi=dpi)
    if ext in WORD_EXTENSIONS:
        # Prefer WPS for .wps, without overriding an explicit user choice.
        conv = converter
        if conv is None:
            if ext == ".wps" and not os.environ.get(ENV_WORD_CONVERTER):
                conv = detect_wps()
            conv = conv or detect_word_converter()
        if conv is None and ext == ".docx":
            conv = WordConverter(KIND_BUILTIN, "")
        if conv is None:
            raise OpError(WORD_INSTALL_HINT.replace("\n", " "))
        with tempfile.TemporaryDirectory(prefix="pdftoolbox-word-") as temp:
            pdf = convert_word_to_pdf(src, Path(temp), conv, timeout=word_timeout)
            data = pdf.read_bytes()
        return pymupdf.open(stream=data, filetype="pdf")
    raise OpError(f"unsupported input type: {src.name} (supported: {', '.join(SUPPORTED_INPUT_EXTENSIONS)})")


_NUM_PART = re.compile(r"(\d+)")


def _natural_key(path: Path):
    return [int(t) if t.isdigit() else t for t in _NUM_PART.split(str(path).lower())]


def collect_inputs(paths: list[str] | tuple[str, ...], recursive: bool = False
                   ) -> tuple[list[Path], list[Path]]:
    """Preserve explicit order; expand folders in natural numeric order."""
    files, ignored = [], []
    for raw in paths:
        path = Path(raw).expanduser()
        if path.is_dir():
            found = [p for p in sorted(path.glob("**/*" if recursive else "*"), key=_natural_key)
                     if p.is_file() and p.suffix.lower() in SUPPORTED_INPUT_EXTENSIONS]
            files.extend(found)
            if not found:
                ignored.append(path)
        elif path.is_file() and path.suffix.lower() in SUPPORTED_INPUT_EXTENSIONS:
            files.append(path)
        else:
            ignored.append(path)
    return files, ignored
