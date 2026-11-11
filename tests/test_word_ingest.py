"""Word ingestion: converter detection, fake-soffice pipeline, error paths."""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

import pytest

from conftest import fake_soffice, make_docx, posix_only  # noqa: F401  (fixture re-export)

from pdftoolbox.common import OpError
from pdftoolbox.ingest import (
    ENV_WORD_CONVERTER,
    KIND_SOFFICE,
    WordConverter,
    convert_word_to_pdf,
    detect_word_converter,
    open_any,
)


def test_env_var_forces_converter(tmp_path, monkeypatch):
    fake = tmp_path / "my-soffice"
    fake.write_text("#!/bin/sh\nexit 0\n")
    fake.chmod(0o755)
    monkeypatch.setenv(ENV_WORD_CONVERTER, str(fake))
    conv = detect_word_converter()
    assert conv is not None
    assert conv.kind == KIND_SOFFICE
    assert conv.path == str(fake)


def test_broken_env_var_warns_and_continues(tmp_path, monkeypatch, capsys):
    monkeypatch.setenv(ENV_WORD_CONVERTER, str(tmp_path / "does-not-exist"))
    detect_word_converter()
    captured = capsys.readouterr()
    assert "warning" in captured.err.lower()


@posix_only
def test_convert_with_fake_soffice(tmp_path, fake_soffice, sample_docx):  # noqa: F811
    conv = detect_word_converter()
    assert conv is not None, "fake soffice must be detected via PATH"
    out_dir = tmp_path / "conv"
    pdf = convert_word_to_pdf(sample_docx, out_dir, conv, timeout=30)
    assert pdf.exists()
    assert pdf.read_bytes()[:5] == b"%PDF-"


@posix_only
def test_convert_failure_surfaces_stderr(tmp_path, monkeypatch):
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    script = bin_dir / "soffice"
    script.write_text("#!/bin/sh\necho 'javaldx.so: missing library' >&2\nexit 77\n")
    script.chmod(0o755)
    monkeypatch.setenv(ENV_WORD_CONVERTER, str(script))
    conv = detect_word_converter()
    docx = make_docx(tmp_path / "f.docx")
    with pytest.raises(OpError, match="produced no PDF"):
        convert_word_to_pdf(docx, tmp_path / "out", conv, timeout=30)


@posix_only
def test_convert_timeout(tmp_path, monkeypatch):
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    script = bin_dir / "soffice"
    script.write_text("#!/bin/sh\nsleep 10\n")
    script.chmod(0o755)
    monkeypatch.setenv(ENV_WORD_CONVERTER, str(script))
    conv = detect_word_converter()
    docx = make_docx(tmp_path / "f.docx")
    with pytest.raises(OpError, match="timed out"):
        convert_word_to_pdf(docx, tmp_path / "out", conv, timeout=1)


def test_open_any_word_without_converter_raises_hint(tmp_path, monkeypatch):
    monkeypatch.setenv(ENV_WORD_CONVERTER, "")
    monkeypatch.setattr(sys, "platform", "linux")
    # Empty PATH so nothing is detected
    monkeypatch.setenv("PATH", str(tmp_path))
    import pdftoolbox.ingest as ingest

    monkeypatch.setattr(ingest, "_SOFFICE_CANDIDATES", ("soffice",))
    docx = make_docx(tmp_path / "f.docx")
    with pytest.raises(OpError, match="LibreOffice"):
        open_any(docx)


def test_open_any_pdf_and_image(tmp_path):
    from conftest import make_image, make_pdf

    pdf = make_pdf(tmp_path / "a.pdf", pages=1)
    doc = open_any(pdf)
    assert doc.page_count == 1
    doc.close()
    img = make_image(tmp_path / "i.png")
    doc = open_any(img, dpi=72)
    assert doc.page_count == 1
    assert doc[0].rect.width == pytest.approx(120, abs=2)  # 120px @72dpi = 120pt
    doc.close()


def test_webp_without_exif_is_reencoded(tmp_path):
    """Regression (review blocker #1): MuPDF cannot decode WebP — such files
    must be re-encoded via Pillow instead of passed through as raw bytes."""
    from PIL import Image

    p = tmp_path / "photo.webp"
    Image.new("RGB", (100, 60), (10, 200, 90)).save(p)
    doc = open_any(p, dpi=96)
    try:
        assert doc.page_count == 1
        assert doc[0].rect.width == pytest.approx(75, abs=2)  # 100px @96dpi
        assert len(doc[0].get_images()) == 1
    finally:
        doc.close()


def test_collect_inputs_natural_order(tmp_path):
    """Directory expansion must sort img2 before img10 (scan-set merge order)."""
    from conftest import make_image

    from pdftoolbox.ingest import collect_inputs

    d = tmp_path / "scans"
    d.mkdir()
    for name in ("img10.png", "img2.png", "img1.png"):
        make_image(d / name)
    files, ignored = collect_inputs([str(d)])
    assert [f.name for f in files] == ["img1.png", "img2.png", "img10.png"]


def test_open_any_missing_and_unsupported(tmp_path):
    with pytest.raises(OpError, match="not found"):
        open_any(tmp_path / "ghost.pdf")
    txt = tmp_path / "x.txt"
    txt.write_text("x")
    with pytest.raises(OpError, match="unsupported"):
        open_any(txt)


def test_encrypted_pdf_rejected(tmp_path):
    import pymupdf

    src = tmp_path / "enc.pdf"
    doc = pymupdf.open()
    doc.new_page()
    doc.save(str(src), encryption=pymupdf.PDF_ENCRYPT_AES_256, user_pw="secret")
    doc.close()
    with pytest.raises(OpError, match="password-protected"):
        open_any(src)


# --------------------------------------------------------------- real e2e
@pytest.mark.wordconv
def test_real_docx_to_pdf_merge(tmp_path, sample_docx, soffice_path):  # noqa: F811
    """End-to-end with real LibreOffice: docx merges into a PDF (CI on ubuntu
    installs libreoffice-writer; skipped elsewhere)."""
    from conftest import make_pdf

    from pdftoolbox import ops

    pdf_part = make_pdf(tmp_path / "p.pdf", pages=1, marker="P")
    out = tmp_path / "merged.pdf"
    res = ops.merge([pdf_part, sample_docx], out)
    assert res.pages >= 2
    import pymupdf

    doc = pymupdf.open(str(out))
    all_text = " ".join(p.get_text() for p in doc)
    assert "P-PAGE-1" in all_text
    assert "pdftoolbox test document" in all_text
    doc.close()
