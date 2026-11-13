"""Isolated Windows Office automation; never attach to a user's active document.

The parent bounds the Python worker's lifetime. An unresponsive out-of-process
Office server can survive a timeout; do not kill arbitrary Word/WPS processes.
Only a validated PDF is committed to the requested destination.

Staging notes: Office saves through an internal temp+rename cycle, so the
staging directory must grant the Office process delete/rename rights. Some
sandboxes (and DSH workspaces) carry a deny-delete ACL that breaks third-party
savers, therefore staging prefers the real system temp and degrades through
several writable locations.
"""
from __future__ import annotations

import ctypes
import importlib.util
import multiprocessing
import os
import shutil
import sys
import tempfile
import uuid
from pathlib import Path

from .common import OpError


def com_available() -> bool:
    return sys.platform == "win32" and importlib.util.find_spec("pythoncom") is not None


def registered_progid(progids: tuple[str, ...]) -> str | None:
    if not com_available():
        return None
    import winreg

    for progid in progids:
        for view in (0, winreg.KEY_WOW64_64KEY, winreg.KEY_WOW64_32KEY):
            try:
                with winreg.OpenKey(winreg.HKEY_CLASSES_ROOT, progid + r"\CLSID",
                                    0, winreg.KEY_READ | view) as key:
                    if winreg.QueryValueEx(key, None)[0]:
                        return progid
            except OSError:
                continue
    return None


def real_temp() -> Path:
    """The OS temp dir, ignoring environment overrides."""
    try:
        buf = ctypes.create_unicode_buffer(520)
        if ctypes.windll.kernel32.GetTempPathW(520, buf):
            return Path(buf.value)
    except Exception:
        pass
    return Path(tempfile.gettempdir())


def _make_dir(candidates) -> Path | None:
    for parent in candidates:
        try:
            return Path(tempfile.mkdtemp(prefix="pdftoolbox-office-", dir=str(parent)))
        except OSError:
            continue
    return None


class _Staging:
    def __init__(self, output: Path, cleanup):
        self.output = output
        self._cleanup = cleanup

    def cleanup(self) -> None:
        try:
            self._cleanup()
        except OSError:
            pass


def make_staging_dir(destination: Path) -> Path:
    """A fresh directory Office/LibreOffice can save into."""
    d = _make_dir([real_temp(), Path(tempfile.gettempdir()),
                   destination.parent, Path.home()])
    if d is None:
        raise OpError("没有可写的转换暂存目录（系统临时目录与输出目录均不可写）")
    return d


def make_staging(destination: Path) -> _Staging:
    """Pick a location Office can save into.

    Order matters: a fresh dir in the real temp (normal machines); then a
    brand-new path inside the real temp without creating anything (covers
    sandboxes that deny our mkdir but not Office's own writes); then other
    writable dirs; finally a bare temp file we own.
    """
    rt = real_temp()
    d = _make_dir([rt])
    if d is not None:
        return _Staging(d / "converted.pdf",
                        lambda: shutil.rmtree(d, ignore_errors=True))
    bare = rt / ("pdftoolbox-office-%s.pdf" % uuid.uuid4().hex)
    if rt.is_dir():
        return _Staging(bare, lambda: bare.unlink(missing_ok=True))
    d = _make_dir([Path(tempfile.gettempdir()), destination.parent, Path.home()])
    if d is not None:
        return _Staging(d / "converted.pdf",
                        lambda: shutil.rmtree(d, ignore_errors=True))
    fd, name = tempfile.mkstemp(prefix="pdftoolbox-office-", suffix=".pdf")
    os.close(fd)
    return _Staging(Path(name), lambda p=Path(name): p.unlink(missing_ok=True))


def make_cache_dir(destination: Path) -> str:
    """Writable dir for win32com gencache; an existing dir also works."""
    d = _make_dir([real_temp(), Path(tempfile.gettempdir()), destination.parent])
    return str(d) if d is not None else tempfile.gettempdir()


def _export_document(app, src: str, output: str) -> None:
    """Use a fresh empty application, disable macros, and close only our file."""
    if app.Documents.Count:
        raise RuntimeError("Office 返回了已有文档的实例；请保存并关闭 WPS/Word 文档后重试")
    doc = None
    try:
        # Do not open a document if disabling its macros fails.
        app.AutomationSecurity = 3  # msoAutomationSecurityForceDisable
        app.Visible = False
        app.DisplayAlerts = 0
        doc = app.Documents.Open(src, False, True)
        # Some WPS editions expose ExportAsFixedFormat but return E_FAIL.
        # Try the compatible PDF SaveAs interface in our isolated staging
        # location; the source is read-only and never saved on close.
        failures = []
        for method in ("ExportAsFixedFormat", "SaveAs2", "SaveAs"):
            try:
                getattr(doc, method)(output, 17)
                return
            except Exception as exc:
                failures.append(f"{method}: {exc}")
        raise RuntimeError("PDF 导出失败；" + "; ".join(failures))
    finally:
        if doc is not None:
            doc.Close(False)
        # WPS may share its server even after DispatchEx. Never quit if a
        # user has opened another document during our operation.
        if app.Documents.Count == 0:
            app.Quit()


def _com_worker(progid: str, src: str, output: str, cache: str, connection) -> None:
    initialized = False
    try:
        import win32com
        # Per-worker cache, disposed with the conversion staging directory.
        # No writes to the global gen_py cache or the frozen install tree.
        win32com.__gen_path__ = cache
        import pythoncom
        from win32com.client import dynamic

        pythoncom.CoInitialize()
        initialized = True
        # Dynamic dispatch avoids generated stubs and stale gencache wrappers.
        obj = pythoncom.CoCreateInstance(
            progid, None, pythoncom.CLSCTX_LOCAL_SERVER, pythoncom.IID_IDispatch)
        app = dynamic.Dispatch(obj)
        _export_document(app, src, output)
        connection.send((True, ""))
    except Exception:
        import traceback
        connection.send((False, traceback.format_exc()))
    finally:
        if initialized:
            pythoncom.CoUninitialize()
        connection.close()


def convert_com(src: Path, destination: Path, progid: str, timeout: int) -> None:
    if not com_available():
        raise OpError("WPS/Word 转换需要 Windows 和 pywin32；源码安装请运行 pip install pywin32")
    if timeout <= 0:
        raise OpError("word-timeout must be > 0")
    ctx = multiprocessing.get_context("spawn")
    receive, send = ctx.Pipe(duplex=False)
    staging = make_staging(destination)
    cache = make_cache_dir(destination)
    process = ctx.Process(target=_com_worker,
                          args=(progid, str(src.resolve()), str(staging.output),
                                cache, send))
    started = False
    timed_out = False
    try:
        process.start()
        started = True
        send.close()
        if not receive.poll(timeout):
            timed_out = True
            raise OpError(
                f"{progid} 转换超时（{timeout}s）。请检查 WPS/Word 是否有授权或密码弹窗；"
                "输出未提交，Office 进程可能仍在运行")
        try:
            ok, detail = receive.recv()
        except EOFError as exc:
            raise OpError("Office 转换进程意外退出，输出未提交") from exc
        if not ok:
            raise OpError(f"{progid} 转换失败：{detail}")
        if not staging.output.is_file() or staging.output.read_bytes()[:5] != b"%PDF-":
            raise OpError("Office 未生成有效 PDF，输出未提交")
        # Copy onto the destination filesystem before atomic replacement.
        fd, name = tempfile.mkstemp(prefix=".office-", suffix=".pdf",
                                    dir=str(destination.parent))
        os.close(fd)
        try:
            shutil.copyfile(staging.output, name)
            os.replace(name, destination)
        finally:
            Path(name).unlink(missing_ok=True)
    finally:
        if started:
            process.join(1)
            if process.is_alive():
                process.terminate()
                process.join(5)
        send.close()
        receive.close()
        if not timed_out:
            staging.cleanup()
            if cache != tempfile.gettempdir():
                shutil.rmtree(cache, ignore_errors=True)
