"""GUI smoke test — skipped without tkinter or a usable display."""

from __future__ import annotations

import os
import sys
import time
from pathlib import Path

import pytest

pytest.importorskip("tkinter")

if sys.platform.startswith("linux") and not (os.environ.get("DISPLAY") or os.environ.get("WAYLAND_DISPLAY")):
    pytest.skip("no display available for GUI smoke test", allow_module_level=True)

from conftest import make_pdf  # noqa: E402

from pdftoolbox.gui import App  # noqa: E402


@pytest.fixture
def app():
    try:
        instance = App()
    except Exception as exc:  # headless session without X/Wayland access
        pytest.skip(f"cannot create Tk window: {exc}")
    instance.root.withdraw()
    yield instance
    try:
        instance.root.destroy()
    except Exception:
        pass


def _drain(app, timeout=20.0):
    """Process Tk events until the worker puts a terminal message."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        app.root.update()
        if not app.q.empty():
            # let _poll_queue consume it
            app.root.update()
            return True
        time.sleep(0.02)
    return False


def test_gui_has_all_tabs(app):
    titles = [app.nb.tab(i, "text").strip() for i in app.nb.tabs()]
    for expected in ("合并", "拆分", "旋转", "页码", "水印", "删空白页", "信息"):
        assert any(expected in t for t in titles), f"missing tab {expected}: {titles}"


def test_gui_number_operation_end_to_end(app, tmp_path):
    src = make_pdf(tmp_path / "g.pdf", pages=2, marker="G")
    out = tmp_path / "g-num.pdf"
    app.num_in_var.set(str(src))
    app.num_out_var.set(str(out))
    app.num_start_var.set("1")
    app.num_first_var.set("7")
    app.num_fmt_var.set("{n}")
    app._number_run()
    assert _drain(app), "operation did not finish in time"
    assert out.exists()
    import pymupdf

    doc = pymupdf.open(str(out))
    assert "7" in doc[0].get_text()
    doc.close()


def test_gui_merge_list_management(app, tmp_path):
    a = make_pdf(tmp_path / "a.pdf", pages=1, marker="A")
    b = make_pdf(tmp_path / "b.pdf", pages=1, marker="B")
    app.merge_files.extend([str(a), str(b)])
    for f in app.merge_files:
        app.merge_listbox.insert("end", f)

    def select(i):
        # Mimic a real mouse click: Tk's selection_set ADDS to the selection,
        # a click replaces it — so clear first.
        app.merge_listbox.selection_clear(0, "end")
        app.merge_listbox.selection_set(i)

    select(0)
    app._merge_move(1)
    assert app.merge_files == [str(b), str(a)]
    select(0)
    app._merge_remove()
    assert app.merge_files == [str(a)]
