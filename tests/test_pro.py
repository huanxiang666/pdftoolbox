"""Tests for the v1.2 hardening and GUI/CLI improvements."""

from __future__ import annotations

import os
import sys
import time
from types import SimpleNamespace

import pytest

from pdftoolbox.cli import main
from pdftoolbox.common import ensure_pymupdf
from pdftoolbox.gui import _clean_path
from pdftoolbox.ingest import KIND_SOFFICE, detect_word_converter

_NEEDS_DISPLAY = pytest.mark.skipif(
    sys.platform.startswith("linux")
    and not (os.environ.get("DISPLAY") or os.environ.get("WAYLAND_DISPLAY")),
    reason="no display available for GUI test",
)


def _make_app():
    import pdftoolbox.gui as gui_mod

    pytest.importorskip("tkinter")
    try:
        app = gui_mod.App()
    except Exception as exc:
        pytest.skip(f"cannot create Tk window: {exc}")
    app.root.withdraw()
    return app


def _drain(app, timeout=20.0):
    """Pump Tk events until the running operation reports completion."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        app.root.update()
        if not app._busy:
            return True
        time.sleep(0.02)
    return False


def test_cli_no_args_is_friendly(capsys):
    """Bare `pdftoolbox` (e.g. double-clicked exe) prints guidance, not a
    bare argparse usage error."""
    assert main([]) == 0
    out = capsys.readouterr().out
    assert "pdftoolbox gui" in out
    assert "图形界面" in out


def test_ensure_pymupdf_returns_module():
    pymupdf = ensure_pymupdf()
    assert hasattr(pymupdf, "open")


def test_clean_path_strips_explorer_quotes():
    assert _clean_path('  "C:\\some dir\\a.pdf"  ') == "C:\\some dir\\a.pdf"
    assert _clean_path("'a.pdf'") == "a.pdf"
    assert _clean_path("  a.pdf  ") == "a.pdf"
    assert _clean_path("") == ""
    assert _clean_path('"mismatched') == '"mismatched'


def test_detect_word_converter_env_override(tmp_path, monkeypatch):
    fake = tmp_path / "soffice.exe"
    fake.write_text("fake")
    monkeypatch.setenv("PDFTOOLBOX_WORD_CONVERTER", str(fake))
    conv = detect_word_converter()
    assert conv is not None and conv.kind == KIND_SOFFICE
    assert conv.path == str(fake)


def test_detect_word_converter_bad_env_falls_back(tmp_path, monkeypatch, capsys):
    monkeypatch.setenv("PDFTOOLBOX_WORD_CONVERTER", str(tmp_path / "missing.exe"))
    conv = detect_word_converter()  # must not raise
    assert conv is None or conv.path != str(tmp_path / "missing.exe")
    assert "warning" in capsys.readouterr().err.lower()


@_NEEDS_DISPLAY
def test_gui_v12_widgets_exist():
    tk = pytest.importorskip("tkinter")
    del tk
    from pdftoolbox.gui import App

    try:
        app = App()
    except Exception as exc:
        pytest.skip(f"cannot create Tk window: {exc}")
    app.root.withdraw()
    try:
        # New v1.2 controls wired to ops parameters that previously existed
        # only in the CLI.
        assert app.num_margin_var.get() == "24"
        assert app.num_color_var.get() == "black"
        assert app.num_opacity_var.get() == "1.0"
        assert app.wm_color_var.get() == "gray"
        assert app.wm_pos_var.get() == "center"
        assert app.wm_imgscale_var.get() == "0.25"
        assert app.clean_dpi_var.get() == "48"
        assert app.merge_dpi_var.get() == "96"
        # Output-reveal button starts disabled.
        assert "disabled" in app.open_out_btn.state()
    finally:
        app.root.destroy()


@_NEEDS_DISPLAY
def test_gui_callback_exception_does_not_raise(monkeypatch):
    pytest.importorskip("tkinter")
    import pdftoolbox.gui as gui_mod

    # The hook shows a modal dialog; stub it out so the test cannot block.
    shown = []
    monkeypatch.setattr(gui_mod.messagebox, "showerror",
                        lambda *a, **k: shown.append(a))

    try:
        app = gui_mod.App()
    except Exception as exc:
        pytest.skip(f"cannot create Tk window: {exc}")
    app.root.withdraw()
    try:
        # Routed through report_callback_exception: logged, not raised.
        try:
            raise ValueError("boom")
        except ValueError as exc:
            app._on_callback_exception(type(exc), exc, exc.__traceback__)
        app.root.update()  # would re-raise if the hook had propagated
        assert shown, "error dialog should have been shown"
    finally:
        app.root.destroy()


@_NEEDS_DISPLAY
def test_gui_ops_import_failure_is_friendly(monkeypatch):
    """Regression: handlers must import pdftoolbox.ops INSIDE the worker.

    If the import happens on the main thread (before _run), a missing pymupdf
    explodes through the Tk callback as a generic crash dialog instead of the
    friendly 'missing dependency' message produced by the worker path.
    """
    import pdftoolbox.gui as gui_mod

    shown = []
    monkeypatch.setattr(gui_mod.messagebox, "showerror",
                        lambda *a, **k: shown.append(a))
    monkeypatch.setitem(sys.modules, "pdftoolbox.ops", None)  # import raises
    import pdftoolbox

    if hasattr(pdftoolbox, "ops"):
        # `from . import ops` falls back to the package attribute when the
        # submodule was imported earlier in the session — remove it too so
        # the ImportError path is deterministic in any test order.
        monkeypatch.delattr(pdftoolbox, "ops")

    app = _make_app()
    try:
        app.num_in_var.set("x.pdf")
        app._number_run()
        assert _drain(app), "operation did not finish in time"
        assert shown, "expected a friendly error dialog"
        assert any("缺少依赖组件" in str(args) or "pymupdf" in str(args)
                   for args in shown)
        log_text = app.log.get("1.0", "end")
        assert "缺少依赖组件" in log_text or "pymupdf" in log_text
    finally:
        app.root.destroy()


@_NEEDS_DISPLAY
@pytest.mark.parametrize("choice", [True, None, False])
def test_gui_close_while_busy(monkeypatch, choice):
    """Close while busy: True = wait for the op then auto-quit (never kill a
    write mid-flight); None = keep working; False = force-quit now."""
    import tkinter as tk

    import pdftoolbox.gui as gui_mod

    monkeypatch.setattr(gui_mod.messagebox, "askyesnocancel",
                        lambda *a, **k: choice)
    app = _make_app()
    app._busy = True

    def destroyed() -> bool:
        try:
            return not app.root.winfo_exists()
        except tk.TclError:
            return True

    try:
        app._on_close()
        if choice is None:
            assert not destroyed(), "cancel must keep the window"
            assert not app._quit_when_idle
            return
        if choice is False:
            assert destroyed(), "force-quit must destroy immediately"
            assert app._poll_after_id is None, "pending poll must be cancelled"
            return
        # choice True: window survives until the worker reports completion
        assert not destroyed(), "wait-quit must not destroy while busy"
        assert app._quit_when_idle
        app._busy = False
        app.q.put(("done", "测试", SimpleNamespace(messages=[], output="")))
        app._poll_queue()
        assert destroyed(), "window must close once the operation finished"
        assert app._poll_after_id is None
    finally:
        if not destroyed():
            app.root.destroy()


@_NEEDS_DISPLAY
def test_gui_fixed_rows_not_clipped():
    """Regression: merge tab bottom rows and the status bar/log must be
    fully inside the window (they were clipped off the bottom edge)."""
    app = _make_app()
    try:
        app.root.deiconify()
        app.root.update_idletasks()
        app.root.update()
        app.nb.select(0)  # merge tab
        app.root.update_idletasks()

        root_bottom = app.root.winfo_rooty() + app.root.winfo_height()
        for name in ("merge_out_row", "merge_hint_row", "log", "status_label"):
            w = getattr(app, name)
            assert w.winfo_ismapped(), f"{name} not visible"
            bottom = w.winfo_rooty() + w.winfo_height()
            assert bottom <= root_bottom + 2, (
                f"{name} bottom edge {bottom} beyond window bottom {root_bottom}"
            )
    finally:
        app.root.destroy()
