"""Shared fixtures: generated PDFs/images/docx, fake soffice helpers."""

from __future__ import annotations

import shutil
import stat
import sys
from pathlib import Path

import pymupdf
import pytest

DATA_DIR = Path(__file__).resolve().parent / "data"

_EXTRA_TOOL_DIRS = [
    "/usr/bin",
    "/usr/lib/libreoffice/program",
]


def find_tool(name: str) -> str | None:
    found = shutil.which(name)
    if found:
        return found
    for d in _EXTRA_TOOL_DIRS:
        cand = Path(d) / name
        if cand.is_file():
            return str(cand)
    return None


def make_pdf(
    path: Path,
    pages: int = 3,
    marker: str = "DOC",
    blank_pages: tuple[int, ...] = (),
    white_image_pages: tuple[int, ...] = (),
) -> Path:
    """Build a test PDF. Each page i carries text '<marker>-PAGE-<i+1>' unless
    it is listed in blank_pages (empty) or white_image_pages (white image)."""
    doc = pymupdf.open()
    for i in range(pages):
        page = doc.new_page(width=595, height=842)
        if (i + 1) in blank_pages:
            continue
        if (i + 1) in white_image_pages:
            img_path = path.with_suffix(f".white{i}.png")
            from PIL import Image

            Image.new("RGB", (300, 200), "white").save(img_path)
            page.insert_image(page.rect, filename=str(img_path))
            img_path.unlink()
            continue
        page.insert_text((72, 100), f"{marker}-PAGE-{i + 1}", fontname="helv", fontsize=14)
    doc.save(str(path))
    doc.close()
    return path


def make_image(path: Path, color: tuple[int, int, int] = (200, 30, 30), size=(120, 80)) -> Path:
    from PIL import Image

    Image.new("RGB", size, color).save(path)
    return path


def make_docx(path: Path, text: str = "DOCX-FIXTURE 中文内容") -> Path:
    import docx

    d = docx.Document()
    d.add_heading("pdftoolbox test document", 0)
    d.add_paragraph(text)
    d.save(str(path))
    return path


@pytest.fixture
def sample_docx() -> Path:
    path = DATA_DIR / "sample.docx"
    if not path.exists():
        DATA_DIR.mkdir(parents=True, exist_ok=True)
        make_docx(path)
    return path


@pytest.fixture
def soffice_path() -> str:
    tool = find_tool("soffice")
    if not tool:
        pytest.skip("LibreOffice soffice not available")
    return tool


@pytest.fixture
def fake_soffice(tmp_path, monkeypatch):
    """A fake soffice that copies a prepared PDF to <outdir>/<stem>.pdf.

    POSIX only (sh script); detection/execution tests using it are skipped on
    Windows where the real integration runs through CI's LibreOffice instead.
    """
    prepared = make_pdf(tmp_path / "prepared.pdf", pages=2, marker="FAKEWORD")
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    script = bin_dir / "soffice"
    # real invocation: soffice -env:... --headless --norestore --convert-to pdf --outdir OUT SRC
    script.write_text(
        "#!/bin/sh\n"
        'outdir=""; src=""\n'
        "prev=\"\"\n"
        'for a in "$@"; do\n'
        '  if [ "$prev" = "--outdir" ]; then outdir="$a"; fi\n'
        '  case "$a" in -*) ;; *) src="$a";; esac\n'
        '  prev="$a"\n'
        "done\n"
        'base=$(basename "$src"); stem="${base%.*}"\n'
        f'cp "{prepared}" "$outdir/$stem.pdf"\n',
        encoding="utf-8",
    )
    script.chmod(script.stat().st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)
    monkeypatch.setenv("PATH", f"{bin_dir}:{__import__('os').environ.get('PATH', '')}")
    return script


posix_only = pytest.mark.skipif(
    sys.platform == "win32",
    reason="POSIX sh fake-executable test; Windows coverage comes from CI LibreOffice e2e",
)
