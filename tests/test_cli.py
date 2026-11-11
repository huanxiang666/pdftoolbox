"""CLI end-to-end tests (in-process main() calls)."""

from __future__ import annotations

import json

import pymupdf
import pytest

from conftest import make_image, make_pdf

from pdftoolbox.cli import main


def test_cli_merge(tmp_path):
    a = make_pdf(tmp_path / "a.pdf", pages=2, marker="A")
    b = make_pdf(tmp_path / "b.pdf", pages=1, marker="B")
    out = tmp_path / "m.pdf"
    code = main(["merge", str(a), str(b), "-o", str(out)])
    assert code == 0
    doc = pymupdf.open(str(out))
    assert doc.page_count == 3
    doc.close()


def test_cli_merge_directory_expansion(tmp_path):
    src = tmp_path / "in"
    src.mkdir()
    make_pdf(src / "a.pdf", pages=1, marker="A")
    make_pdf(src / "b.pdf", pages=1, marker="B")
    (src / "notes.txt").write_text("x")
    out = tmp_path / "m.pdf"
    code = main(["merge", str(src), "-o", str(out)])
    assert code == 0
    doc = pymupdf.open(str(out))
    assert doc.page_count == 2
    doc.close()


def test_cli_split(tmp_path):
    src = make_pdf(tmp_path / "s.pdf", pages=3)
    outdir = tmp_path / "parts"
    assert main(["split", str(src), "-o", str(outdir), "--ranges", "1-2,3"]) == 0
    assert len(list(outdir.glob("*.pdf"))) == 2


def test_cli_rotate_number_watermark_clean(tmp_path):
    src = make_pdf(tmp_path / "d.pdf", pages=4, blank_pages=(4,))
    rot = tmp_path / "rot.pdf"
    assert main(["rotate", str(src), "-o", str(rot), "--angle", "90", "--pages", "1"]) == 0
    num = tmp_path / "num.pdf"
    assert main(["number", str(rot), "-o", str(num), "--start-page", "2",
                 "--first-number", "5", "--format", "第 {n} 页"]) == 0
    doc = pymupdf.open(str(num))
    assert "第 5 页" in doc[1].get_text()
    doc.close()
    wm = tmp_path / "wm.pdf"
    assert main(["watermark", str(num), "-o", str(wm), "--text", "样本",
                 "--opacity", "0.2", "--pages", "1-2"]) == 0
    doc = pymupdf.open(str(wm))
    assert "样本" in doc[0].get_text() and "样本" not in doc[2].get_text()
    doc.close()
    clean = tmp_path / "clean.pdf"
    assert main(["clean", str(wm), "-o", str(clean)]) == 0
    doc = pymupdf.open(str(clean))
    # Page 4 started blank, but 'number' stamped "第 N 页" onto it — a page
    # with text is never treated as blank, so all 4 pages survive. (Real
    # workflow tip: run 'clean' BEFORE 'number' to drop blanks first.)
    assert doc.page_count == 4
    doc.close()


def test_cli_clean_dry_run_reports(tmp_path, capsys):
    src = make_pdf(tmp_path / "c.pdf", pages=3, blank_pages=(2,))
    code = main(["clean", str(src), "--dry-run"])
    assert code == 0
    captured = capsys.readouterr()
    assert "blank" in captured.out.lower()


def test_cli_info_json(tmp_path, capsys):
    src = make_pdf(tmp_path / "i.pdf", pages=2)
    assert main(["info", str(src), "--json"]) == 0
    captured = capsys.readouterr()
    data = json.loads(captured.out)
    assert data["pages"] == 2


def test_cli_check_runs(capsys):
    assert main(["check"]) == 0
    assert "pdftoolbox" in capsys.readouterr().out


def test_cli_error_exit_code(tmp_path, capsys):
    bad = tmp_path / "ghost.pdf"
    code = main(["rotate", str(bad), "--angle", "90"])
    assert code == 1
    assert "error" in capsys.readouterr().err.lower()


def test_cli_unexpected_error_no_traceback(tmp_path, capsys):
    """Regression (review): OS-level failures (output path under a regular
    file) must produce a readable error + exit 1, never a bare traceback."""
    src = make_pdf(tmp_path / "a.pdf", pages=1)
    blocker = tmp_path / "blocker"
    blocker.write_text("i am a file, not a directory")
    bad_out = blocker / "sub" / "out.pdf"
    code = main(["rotate", str(src), "-o", str(bad_out), "--angle", "90"])
    assert code == 1
    err = capsys.readouterr().err
    assert "error:" in err
    assert "Traceback" not in err


def test_cli_usage_error():
    with pytest.raises(SystemExit) as excinfo:
        main(["nosuchcommand"])
    assert excinfo.value.code == 2


def test_cli_ensure_utf8_stdio(monkeypatch):
    """CJK output must not crash under a cp1252-style stdout (Windows CI)."""
    import io
    import sys

    from pdftoolbox.cli import _ensure_utf8_stdio

    buf = io.BytesIO()
    monkeypatch.setattr(sys, "stdout", io.TextIOWrapper(buf, encoding="cp1252", errors="strict"))
    _ensure_utf8_stdio()
    sys.stdout.write("合并完成 第 1 页")
    sys.stdout.flush()
    assert "合并完成" in buf.getvalue().decode("utf-8")
