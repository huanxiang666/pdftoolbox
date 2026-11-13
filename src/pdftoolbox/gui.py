"""Tkinter GUI for pdftoolbox (entry point: pdftoolbox-gui / pdftoolbox gui).

One tab per operation. All work runs on a background thread; the Tk main loop
only drains a queue, keeping the UI responsive and thread-safe.

Hardening over v0.1:
* Windows DPI awareness (crisp UI on high-resolution screens).
* Native ttk theme when available (vista/xpnative/clam).
* Startup is wrapped: any failure becomes a readable dialog, never a silent
  exit — "点开就崩" must be impossible to miss.
* Unexpected callback errors are logged and shown, not raised through Tk.
* Output files/folders open in the system file manager with one click.
* Quote-wrapped paths pasted from Explorer (复制为路径) are accepted.
* The GUI imports no PDF engine at startup: a broken pymupdf degrades each
  operation to a readable error instead of killing the window.
"""

from __future__ import annotations

import multiprocessing
import os
import queue
import subprocess
import sys
import threading
import tkinter as tk
import traceback
from pathlib import Path
from tkinter import filedialog, messagebox, ttk
from tkinter.scrolledtext import ScrolledText

from . import SUPPORTED_INPUT_EXTENSIONS, __version__
from .common import POSITIONS, OpError

_POLL_MS = 100
_POSITION_CHOICES = sorted(POSITIONS)
_COLOR_CHOICES = ["black", "gray", "red", "blue", "green", "orange",
                  "purple", "cyan", "magenta", "#000000", "#666666"]
_FILETYPES = [
    ("全部支持格式", " ".join("*" + e for e in SUPPORTED_INPUT_EXTENSIONS)),
    ("PDF", "*.pdf"),
    ("Word", "*.doc *.docx *.odt *.rtf *.wps"),
    ("图片", "*.png *.jpg *.jpeg *.bmp *.tif *.tiff *.gif *.webp"),
    ("所有文件", "*.*"),
]


def enable_dpi_awareness() -> None:
    """Best-effort per-monitor DPI awareness on Windows; ignore all failures."""
    if sys.platform != "win32":
        return
    try:
        import ctypes

        for attempt in (
            # PER_MONITOR_AWARE_V2 (Windows 10 1703+)
            lambda: ctypes.windll.user32.SetProcessDpiAwarenessContext(-4),
            # PROCESS_PER_MONITOR_DPI_AWARE (Vista+)
            lambda: ctypes.windll.shcore.SetProcessDpiAwareness(2),
            lambda: ctypes.windll.user32.SetProcessDPIAware(),
        ):
            try:
                attempt()
                return
            except Exception:
                continue
    except Exception:
        pass


def _clean_path(value: str) -> str:
    """Strip whitespace and the quotes Explorer adds via 复制为路径."""
    v = (value or "").strip()
    if len(v) >= 2 and v[0] == v[-1] and v[0] in ("\"", "'"):
        v = v[1:-1].strip()
    return v


def _open_in_file_manager(path: str) -> None:
    """Reveal a file (or open a directory) in the system file manager."""
    p = Path(path)
    if sys.platform == "win32":
        if p.is_file():
            subprocess.Popen(["explorer", "/select,", str(p)])
        else:
            os.startfile(str(p if p.is_dir() else p.parent))
    elif sys.platform == "darwin":
        subprocess.Popen(["open", "-R", str(p)] if p.is_file() else ["open", str(p)])
    else:
        target = p if p.is_dir() else p.parent
        subprocess.Popen(["xdg-open", str(target)])


class App:
    def __init__(self):
        self.q: "queue.Queue[tuple]" = queue.Queue()
        self._busy = False
        self._quit_when_idle = False  # close requested while an op is running
        self._last_output = ""

        self.root = tk.Tk()
        self.root.report_callback_exception = self._on_callback_exception
        self.root.title(f"pdftoolbox {__version__} — PDF 工具箱")
        # Fit small screens: Tk's geometry is the CLIENT area — with the
        # title bar added, a hardcoded 720px overflows a 864px display and
        # pushes the status bar/log off the bottom edge.
        screen_h = self.root.winfo_screenheight()
        screen_w = self.root.winfo_screenwidth()
        win_h = min(720, max(560, int(screen_h * 0.80)))
        win_w = min(980, max(860, int(screen_w * 0.85)))
        self.root.geometry(f"{win_w}x{win_h}")
        self.root.minsize(860, 620)
        self._apply_theme()

        # Pack the status bar and log FIRST, anchored to the bottom edge:
        # the expanding notebook can then never push them out of view
        # (previously the merge tab's bottom rows could be clipped).
        bottom = ttk.Frame(self.root, padding=(10, 0, 10, 6))
        bottom.pack(side="bottom", fill="x")
        self.status_var = tk.StringVar(value="就绪")
        self.status_label = ttk.Label(bottom, textvariable=self.status_var)
        self.status_label.pack(side="left")
        self.progress = ttk.Progressbar(bottom, mode="indeterminate", length=140)
        self.progress.pack(side="left", padx=(10, 0))
        self.open_out_btn = ttk.Button(bottom, text="打开输出位置",
                                       command=self._open_last_output, state="disabled")
        self.open_out_btn.pack(side="right", padx=(8, 0))
        ttk.Button(bottom, text="环境检查", command=self.show_check).pack(side="right")

        self.log = ScrolledText(self.root, height=8, state="disabled", wrap="word")
        self.log.pack(side="bottom", fill="x", padx=10, pady=(0, 10))

        self.nb = ttk.Notebook(self.root)
        self.nb.pack(fill="both", expand=True, padx=10, pady=(10, 4))
        self._build_merge_tab()
        self._build_split_tab()
        self._build_rotate_tab()
        self._build_number_tab()
        self._build_watermark_tab()
        self._build_clean_tab()
        self._build_info_tab()

        self._poll_after_id: str | None = None
        self.root.protocol("WM_DELETE_WINDOW", self._on_close)
        # Any destroy path (including a bare root.destroy()) cancels the
        # pending poll, so no "invalid command name" after-callback warnings.
        self.root.bind("<Destroy>", self._on_root_destroy, add="+")
        self._poll_after_id = self.root.after(_POLL_MS, self._poll_queue)

    def _apply_theme(self) -> None:
        """Prefer the native theme so the app does not look like Windows 95."""
        try:
            style = ttk.Style(self.root)
            available = style.theme_names()
            for theme in ("vista", "xpnative", "clam"):
                if theme in available:
                    style.theme_use(theme)
                    break
        except Exception:
            pass

    # ------------------------------------------------------------ utilities
    def _log(self, text: str) -> None:
        self.log.configure(state="normal")
        self.log.insert("end", text.rstrip() + "\n")
        self.log.see("end")
        self.log.configure(state="disabled")

    def _on_callback_exception(self, exc_type, exc, tb) -> None:
        """Tk routes callback exceptions here instead of crashing the app."""
        msg = "".join(traceback.format_exception_only(exc_type, exc)).strip()
        traceback.print_exception(exc_type, exc, tb)
        try:
            self._log(f"  界面异常: {msg}")
            messagebox.showerror("pdftoolbox", f"发生意外错误：\n{msg}")
        except Exception:
            pass

    def _run(self, label: str, fn) -> None:
        """Run fn() on a worker thread; fn returns an OpResult or raises."""
        if self._quit_when_idle:
            messagebox.showinfo("pdftoolbox", "正在等待当前操作完成后退出，请稍候")
            return
        if self._busy:
            messagebox.showinfo("pdftoolbox", "上一个操作还在进行中")
            return
        self._busy = True
        self.status_var.set(f"{label} …")
        self.progress.start(12)
        self._log(f"—— {label} ——")

        def worker():
            try:
                result = fn()
                self.q.put(("done", label, result))
            except OpError as exc:
                self.q.put(("operror", label, str(exc)))
            except ImportError as exc:
                self.q.put(("operror", label,
                            f"缺少依赖组件: {exc}（请运行环境检查，或 pip install pymupdf pillow）"))
            except Exception as exc:
                self.q.put(("error", label, f"{exc.__class__.__name__}: {exc}"))

        threading.Thread(target=worker, daemon=True).start()

    def _cancel_poll(self) -> None:
        """Cancel the pending queue-poll timer, if any (id is stale-safe)."""
        if self._poll_after_id is not None:
            try:
                self.root.after_cancel(self._poll_after_id)
            except Exception:
                pass
            self._poll_after_id = None

    def _poll_queue(self) -> None:
        # Whether invoked by the timer or directly: guarantee no duplicate
        # poll timer stays pending (a second one would outlive destroy()).
        self._cancel_poll()
        try:
            while True:
                msg = self.q.get_nowait()
                kind, label = msg[0], msg[1]
                self._busy = False
                self.progress.stop()
                if kind == "done":
                    result = msg[2]
                    self.status_var.set(f"{label} 完成")
                    for line in result.messages:
                        self._log(f"  {line}")
                    if result.output:
                        self._last_output = result.output
                        self._log(f"  输出: {Path(result.output).resolve()}")
                        self.open_out_btn.configure(state="normal")
                elif kind == "operror":
                    self.status_var.set(f"{label} 失败")
                    self._log(f"  错误: {msg[2]}")
                    messagebox.showerror("pdftoolbox", str(msg[2]))
                else:
                    self.status_var.set(f"{label} 出错")
                    self._log(f"  异常: {msg[2]}")
                    messagebox.showerror("pdftoolbox", msg[2])
        except queue.Empty:
            pass
        except tk.TclError:
            return  # window is being torn down; nothing to reschedule
        if self._quit_when_idle and not self._busy:
            # Deferred close: the running operation has finished (its atomic
            # save completed), so destroying now cannot cut a write in half.
            self._destroy()
            return
        try:
            self._poll_after_id = self.root.after(_POLL_MS, self._poll_queue)
        except tk.TclError:
            pass  # root already gone

    def _on_root_destroy(self, event) -> None:
        if getattr(event, "widget", None) is not self.root:
            return
        self._cancel_poll()
        # Detach every Tk variable NOW, on the main thread. Otherwise cyclic
        # GC can finalize them later on a worker thread, where Variable.__del__
        # calls into the dead Tcl interpreter — deadlocking against the main
        # thread's Tcl event loop (observed as hung operations and "main
        # thread is not in main loop" RuntimeErrors). Variable.__del__ is a
        # no-op once _tk is None.
        for name, value in list(vars(self).items()):
            if isinstance(value, tk.Variable):
                try:
                    value._tk = None  # neutralize the finalizer
                except Exception:
                    pass
                setattr(self, name, None)

    def _destroy(self) -> None:
        """Cancel the pending queue poll, then destroy the window."""
        self._cancel_poll()
        self.root.destroy()

    def _open_last_output(self) -> None:
        if not self._last_output:
            return
        try:
            _open_in_file_manager(self._last_output)
        except Exception as exc:
            messagebox.showerror("pdftoolbox", f"无法打开输出位置：{exc}")

    # ------------------------------------------------------------- merge tab
    def _build_merge_tab(self) -> None:
        tab = ttk.Frame(self.nb, padding=8)
        self.nb.add(tab, text=" 合并 ")
        self.merge_files: list = []

        # Pack the fixed bottom rows BEFORE the expanding listbox so they can
        # never be clipped off the tab's bottom edge.
        hint = ttk.Frame(tab)
        hint.pack(side="bottom", fill="x")
        self.merge_hint_row = hint
        ttk.Label(hint, text="合并顺序 = 列表顺序（Word 需要 WPS/LibreOffice/MS Word，见环境检查）"
                  ).pack(side="left")
        ttk.Label(hint, text="图片DPI:").pack(side="right")
        self.merge_dpi_var = tk.StringVar(value="96")
        ttk.Combobox(hint, textvariable=self.merge_dpi_var, width=6,
                     values=["96", "150", "300"], state="readonly").pack(side="right", padx=(4, 4))

        self.merge_out_var = tk.StringVar()
        row = ttk.Frame(tab)
        row.pack(side="bottom", fill="x", pady=(6, 0))
        self.merge_out_row = row
        ttk.Label(row, text="输出 PDF:").pack(side="left")
        ttk.Entry(row, textvariable=self.merge_out_var).pack(side="left", fill="x", expand=True, padx=4)
        ttk.Button(row, text="另存为…", command=lambda: self._save_as(self.merge_out_var)).pack(side="left")
        self.merge_run = ttk.Button(row, text="开始合并", command=self._merge_run)
        self.merge_run.pack(side="left", padx=(8, 0))

        listbox = tk.Listbox(tab, height=8)
        listbox.pack(fill="both", expand=True, side="left")
        self.merge_listbox = listbox
        side = ttk.Frame(tab)
        side.pack(side="right", fill="y", padx=(8, 0))
        ttk.Button(side, text="添加文件…", command=self._merge_add).pack(fill="x", pady=2)
        ttk.Button(side, text="添加文件夹…", command=self._merge_add_dir).pack(fill="x", pady=2)
        ttk.Button(side, text="上移", command=lambda: self._merge_move(-1)).pack(fill="x", pady=2)
        ttk.Button(side, text="下移", command=lambda: self._merge_move(1)).pack(fill="x", pady=2)
        ttk.Button(side, text="移除选中", command=self._merge_remove).pack(fill="x", pady=2)
        ttk.Button(side, text="清空", command=self._merge_clear).pack(fill="x", pady=2)

    def _merge_add(self) -> None:
        paths = filedialog.askopenfilenames(filetypes=_FILETYPES)
        for p in paths:
            self.merge_files.append(p)
            self.merge_listbox.insert(tk.END, p)

    def _merge_add_dir(self) -> None:
        d = filedialog.askdirectory()
        if not d:
            return
        try:
            from .ingest import collect_inputs
        except ImportError as exc:
            messagebox.showerror("pdftoolbox", f"缺少依赖组件: {exc}")
            return
        files, _ignored = collect_inputs([d], recursive=False)
        if not files:
            messagebox.showinfo("pdftoolbox", "该文件夹内没有支持的文件（PDF/Word/图片）")
            return
        for f in files:
            self.merge_files.append(str(f))
            self.merge_listbox.insert(tk.END, str(f))
        self._log(f"从文件夹添加 {len(files)} 个文件: {d}")

    def _merge_remove(self) -> None:
        for idx in reversed(self.merge_listbox.curselection()):
            self.merge_listbox.delete(idx)
            del self.merge_files[idx]

    def _merge_clear(self) -> None:
        self.merge_listbox.delete(0, tk.END)
        self.merge_files.clear()

    def _merge_move(self, direction: int) -> None:
        sel = self.merge_listbox.curselection()
        if len(sel) != 1:
            return
        i = sel[0]
        j = i + direction
        if 0 <= j < len(self.merge_files):
            self.merge_files[i], self.merge_files[j] = self.merge_files[j], self.merge_files[i]
            self.merge_listbox.delete(0, tk.END)
            for f in self.merge_files:
                self.merge_listbox.insert(tk.END, f)
            # selection_set ADDS to the current selection in Tk — clear first,
            # otherwise a later "remove selected" would delete two entries.
            self.merge_listbox.selection_clear(0, tk.END)
            self.merge_listbox.selection_set(j)

    def _merge_run(self) -> None:
        if not self.merge_files:
            messagebox.showwarning("pdftoolbox", "请先添加要合并的文件")
            return
        out = _clean_path(self.merge_out_var.get())
        if not out:
            first = Path(self.merge_files[0])
            out = str(first.with_name(f"{first.stem}-merged.pdf"))
        files = [Path(f) for f in self.merge_files]
        try:
            dpi = int(self.merge_dpi_var.get())
        except ValueError:
            dpi = 96

        def job():
            # Import inside the worker: a missing/broken pymupdf then becomes
            # a friendly per-operation error, not a main-thread traceback.
            from . import ops

            return ops.merge(files, out, dpi=dpi)

        self._run(f"合并 {len(files)} 个文件", job)

    def _save_as(self, var: tk.StringVar) -> None:
        path = filedialog.asksaveasfilename(
            defaultextension=".pdf", filetypes=[("PDF", "*.pdf")])
        if path:
            var.set(path)

    # ------------------------------------------------------------- split tab
    def _build_split_tab(self) -> None:
        tab = ttk.Frame(self.nb, padding=8)
        self.nb.add(tab, text=" 拆分 ")
        self.split_in_var = tk.StringVar()
        self.split_out_var = tk.StringVar()
        self.split_mode_var = tk.StringVar(value="ranges")
        self.split_ranges_var = tk.StringVar(value="1")

        self._file_row(tab, "输入 PDF:", self.split_in_var, dir_var=self.split_out_var)
        row = ttk.Frame(tab)
        row.pack(fill="x", pady=4)
        ttk.Label(row, text="输出目录:").pack(side="left")
        ttk.Entry(row, textvariable=self.split_out_var).pack(side="left", fill="x", expand=True, padx=4)
        ttk.Button(row, text="选择…", command=self._split_pick_dir).pack(side="left")

        mode = ttk.Frame(tab)
        mode.pack(fill="x", pady=4)
        ttk.Radiobutton(mode, text="按范围拆分（每段一个文件）", variable=self.split_mode_var,
                        value="ranges").pack(side="left")
        ttk.Entry(mode, textvariable=self.split_ranges_var, width=24).pack(side="left", padx=8)
        ttk.Label(mode, text='如 "1-3,5,8-"').pack(side="left")
        ttk.Radiobutton(mode, text="每页一个文件", variable=self.split_mode_var,
                        value="pages").pack(side="left", padx=(24, 0))
        ttk.Button(tab, text="开始拆分", command=self._split_run).pack(anchor="e", pady=(8, 0))

    def _split_pick_dir(self) -> None:
        d = filedialog.askdirectory()
        if d:
            self.split_out_var.set(d)

    def _split_run(self) -> None:
        src = _clean_path(self.split_in_var.get())
        out_dir = _clean_path(self.split_out_var.get())
        if not src:
            messagebox.showwarning("pdftoolbox", "请选择输入 PDF")
            return
        if not out_dir:
            p = Path(src)
            out_dir = str(p.with_name(p.stem + "-split"))
            self.split_out_var.set(out_dir)
        per_page = self.split_mode_var.get() == "pages"
        ranges = None if per_page else self.split_ranges_var.get().strip()

        def job():
            from . import ops

            return ops.split(src, out_dir, per_page=per_page, ranges=ranges)

        self._run("拆分", job)

    # ------------------------------------------------------------ rotate tab
    def _build_rotate_tab(self) -> None:
        tab = ttk.Frame(self.nb, padding=8)
        self.nb.add(tab, text=" 旋转 ")
        self.rot_in_var = tk.StringVar()
        self.rot_out_var = tk.StringVar()
        self.rot_angle_var = tk.StringVar(value="90")
        self.rot_pages_var = tk.StringVar(value="all")
        self._file_row(tab, "输入 PDF:", self.rot_in_var, save_var=self.rot_out_var)
        row = ttk.Frame(tab)
        row.pack(fill="x", pady=4)
        ttk.Label(row, text="角度(顺时针):").pack(side="left")
        ttk.Combobox(row, textvariable=self.rot_angle_var, values=["90", "180", "270"],
                     width=6, state="readonly").pack(side="left", padx=4)
        ttk.Label(row, text="页码:").pack(side="left", padx=(16, 0))
        ttk.Entry(row, textvariable=self.rot_pages_var, width=16).pack(side="left", padx=4)
        ttk.Label(row, text='如 "all" 或 "1-3,5"').pack(side="left")
        ttk.Button(tab, text="开始旋转", command=self._rotate_run).pack(anchor="e", pady=(8, 0))

    def _rotate_run(self) -> None:
        src = _clean_path(self.rot_in_var.get())
        if not src:
            messagebox.showwarning("pdftoolbox", "请选择输入 PDF")
            return
        out = _clean_path(self.rot_out_var.get()) or None
        angle = int(self.rot_angle_var.get())
        pages = self.rot_pages_var.get().strip() or "all"

        def job():
            from . import ops

            return ops.rotate(src, out, angle, pages)

        self._run("旋转", job)

    # ------------------------------------------------------------ number tab
    def _build_number_tab(self) -> None:
        tab = ttk.Frame(self.nb, padding=8)
        self.nb.add(tab, text=" 页码 ")
        self.num_in_var = tk.StringVar()
        self.num_out_var = tk.StringVar()
        self.num_start_var = tk.StringVar(value="1")
        self.num_first_var = tk.StringVar(value="1")
        self.num_fmt_var = tk.StringVar(value="{n}")
        self.num_pos_var = tk.StringVar(value="bottom-center")
        self.num_size_var = tk.StringVar(value="11")
        self.num_margin_var = tk.StringVar(value="24")
        self.num_color_var = tk.StringVar(value="black")
        self.num_opacity_var = tk.StringVar(value="1.0")
        self._file_row(tab, "输入 PDF:", self.num_in_var, save_var=self.num_out_var)
        row1 = ttk.Frame(tab)
        row1.pack(fill="x", pady=4)
        ttk.Label(row1, text="从第几页开始:").pack(side="left")
        ttk.Entry(row1, textvariable=self.num_start_var, width=6).pack(side="left", padx=4)
        ttk.Label(row1, text="起始编号:").pack(side="left", padx=(12, 0))
        ttk.Entry(row1, textvariable=self.num_first_var, width=6).pack(side="left", padx=4)
        ttk.Label(row1, text="格式:").pack(side="left", padx=(12, 0))
        fmt_box = ttk.Combobox(row1, textvariable=self.num_fmt_var, width=22,
                               values=["{n}", "{n} / {t}", "第 {n} 页 共 {t} 页", "- {n} -"])
        fmt_box.pack(side="left", padx=4)
        row2 = ttk.Frame(tab)
        row2.pack(fill="x", pady=4)
        ttk.Label(row2, text="位置:").pack(side="left")
        ttk.Combobox(row2, textvariable=self.num_pos_var, values=_POSITION_CHOICES,
                     width=16, state="readonly").pack(side="left", padx=4)
        ttk.Label(row2, text="字号:").pack(side="left", padx=(12, 0))
        ttk.Entry(row2, textvariable=self.num_size_var, width=6).pack(side="left", padx=4)
        ttk.Label(row2, text="边距(pt):").pack(side="left", padx=(12, 0))
        ttk.Entry(row2, textvariable=self.num_margin_var, width=6).pack(side="left", padx=4)
        row3 = ttk.Frame(tab)
        row3.pack(fill="x", pady=4)
        ttk.Label(row3, text="颜色:").pack(side="left")
        ttk.Combobox(row3, textvariable=self.num_color_var, values=_COLOR_CHOICES,
                     width=12).pack(side="left", padx=4)
        ttk.Label(row3, text="透明度(0-1):").pack(side="left", padx=(12, 0))
        ttk.Entry(row3, textvariable=self.num_opacity_var, width=6).pack(side="left", padx=4)
        ttk.Label(row3, text="（{n}=页码值 {t}=文档总页数，支持中文）").pack(side="left", padx=(12, 0))
        ttk.Button(tab, text="添加页码", command=self._number_run).pack(anchor="e", pady=(8, 0))

    def _number_run(self) -> None:
        src = _clean_path(self.num_in_var.get())
        if not src:
            messagebox.showwarning("pdftoolbox", "请选择输入 PDF")
            return
        # Read every Tk variable HERE on the main thread — the worker thread
        # must never touch Tcl state (queue-only threading model).
        out = _clean_path(self.num_out_var.get()) or None
        try:
            start_page = int(self.num_start_var.get().strip() or 1)
            first_number = int(self.num_first_var.get().strip() or 1)
            fontsize = float(self.num_size_var.get().strip() or 11)
            margin = float(self.num_margin_var.get().strip() or 24)
            opacity = float(self.num_opacity_var.get().strip() or 1.0)
        except ValueError:
            messagebox.showerror("pdftoolbox", "起始页/起始编号/字号/边距/透明度必须是数字")
            return
        fmt = self.num_fmt_var.get() or "{n}"
        position = self.num_pos_var.get()
        color = self.num_color_var.get().strip() or "black"

        def job():
            from . import ops

            return ops.number_pages(
                src, out, start_page=start_page, first_number=first_number,
                fmt=fmt, position=position, margin=margin, fontsize=fontsize,
                color=color, opacity=opacity,
            )

        self._run("页码", job)

    # --------------------------------------------------------- watermark tab
    def _build_watermark_tab(self) -> None:
        tab = ttk.Frame(self.nb, padding=8)
        self.nb.add(tab, text=" 水印 ")
        self.wm_in_var = tk.StringVar()
        self.wm_out_var = tk.StringVar()
        self.wm_mode_var = tk.StringVar(value="text")
        self.wm_text_var = tk.StringVar(value="机密")
        self.wm_image_var = tk.StringVar()
        self.wm_opacity_var = tk.StringVar(value="0.25")
        self.wm_angle_var = tk.StringVar(value="45")
        self.wm_size_var = tk.StringVar(value="36")
        self.wm_tile_var = tk.BooleanVar(value=False)
        self.wm_pages_var = tk.StringVar(value="all")
        self.wm_color_var = tk.StringVar(value="gray")
        self.wm_pos_var = tk.StringVar(value="center")
        self.wm_imgscale_var = tk.StringVar(value="0.25")
        self._file_row(tab, "输入 PDF:", self.wm_in_var, save_var=self.wm_out_var)

        mode = ttk.Frame(tab)
        mode.pack(fill="x", pady=4)
        ttk.Radiobutton(mode, text="文字水印", variable=self.wm_mode_var, value="text").pack(side="left")
        ttk.Entry(mode, textvariable=self.wm_text_var, width=20).pack(side="left", padx=4)
        ttk.Checkbutton(mode, text="平铺整页", variable=self.wm_tile_var).pack(side="left", padx=8)
        ttk.Radiobutton(mode, text="图片水印", variable=self.wm_mode_var, value="image").pack(side="left", padx=(16, 0))
        ttk.Entry(mode, textvariable=self.wm_image_var, width=24).pack(side="left", padx=4)
        ttk.Button(mode, text="选择…", command=self._wm_pick_image).pack(side="left")

        opts = ttk.Frame(tab)
        opts.pack(fill="x", pady=4)
        ttk.Label(opts, text="不透明度:").pack(side="left")
        ttk.Entry(opts, textvariable=self.wm_opacity_var, width=6).pack(side="left", padx=4)
        ttk.Label(opts, text="角度:").pack(side="left", padx=(12, 0))
        ttk.Entry(opts, textvariable=self.wm_angle_var, width=6).pack(side="left", padx=4)
        ttk.Label(opts, text="字号:").pack(side="left", padx=(12, 0))
        ttk.Entry(opts, textvariable=self.wm_size_var, width=6).pack(side="left", padx=4)
        ttk.Label(opts, text="页码:").pack(side="left", padx=(12, 0))
        ttk.Entry(opts, textvariable=self.wm_pages_var, width=10).pack(side="left", padx=4)

        opts2 = ttk.Frame(tab)
        opts2.pack(fill="x", pady=4)
        ttk.Label(opts2, text="颜色:").pack(side="left")
        ttk.Combobox(opts2, textvariable=self.wm_color_var, values=_COLOR_CHOICES,
                     width=12).pack(side="left", padx=4)
        ttk.Label(opts2, text="位置:").pack(side="left", padx=(12, 0))
        ttk.Combobox(opts2, textvariable=self.wm_pos_var, values=_POSITION_CHOICES,
                     width=16, state="readonly").pack(side="left", padx=4)
        ttk.Label(opts2, text="图片缩放(0-1):").pack(side="left", padx=(12, 0))
        ttk.Entry(opts2, textvariable=self.wm_imgscale_var, width=6).pack(side="left", padx=4)
        ttk.Button(tab, text="加水印", command=self._watermark_run).pack(anchor="e", pady=(8, 0))

    def _wm_pick_image(self) -> None:
        path = filedialog.askopenfilename(
            filetypes=[("图片", "*.png *.jpg *.jpeg *.bmp *.gif *.webp"), ("所有文件", "*.*")])
        if path:
            self.wm_image_var.set(path)
            self.wm_mode_var.set("image")

    def _watermark_run(self) -> None:
        src = _clean_path(self.wm_in_var.get())
        if not src:
            messagebox.showwarning("pdftoolbox", "请选择输入 PDF")
            return
        # Read all Tk variables on the main thread (see _number_run).
        out = _clean_path(self.wm_out_var.get()) or None
        mode = self.wm_mode_var.get()
        text = self.wm_text_var.get().strip() if mode == "text" else None
        image = _clean_path(self.wm_image_var.get()) if mode == "image" else None
        pages = self.wm_pages_var.get().strip() or "all"
        tile = bool(self.wm_tile_var.get())
        color = self.wm_color_var.get().strip() or "gray"
        position = self.wm_pos_var.get()
        try:
            opacity = float(self.wm_opacity_var.get().strip() or 0.25)
            angle = float(self.wm_angle_var.get().strip() or 45)
            fontsize = float(self.wm_size_var.get().strip() or 36)
            image_scale = float(self.wm_imgscale_var.get().strip() or 0.25)
        except ValueError:
            messagebox.showerror("pdftoolbox", "不透明度/角度/字号/图片缩放必须是数字")
            return

        def job():
            from . import ops

            return ops.watermark(
                src, out, text=text or None, image=image or None,
                pages=pages, opacity=opacity, angle=angle, fontsize=fontsize,
                color=color, position=position, tile=tile, image_scale=image_scale,
            )

        self._run("水印", job)

    # ------------------------------------------------------------- clean tab
    def _build_clean_tab(self) -> None:
        tab = ttk.Frame(self.nb, padding=8)
        self.nb.add(tab, text=" 删空白页 ")
        self.clean_in_var = tk.StringVar()
        self.clean_out_var = tk.StringVar()
        self.clean_threshold_var = tk.StringVar(value="0.002")
        self.clean_dpi_var = tk.StringVar(value="48")
        self.clean_dryrun_var = tk.BooleanVar(value=False)
        self._file_row(tab, "输入 PDF:", self.clean_in_var, save_var=self.clean_out_var)
        row = ttk.Frame(tab)
        row.pack(fill="x", pady=4)
        ttk.Label(row, text="墨迹阈值:").pack(side="left")
        ttk.Entry(row, textvariable=self.clean_threshold_var, width=8).pack(side="left", padx=4)
        ttk.Label(row, text="检测DPI:").pack(side="left", padx=(12, 0))
        ttk.Entry(row, textvariable=self.clean_dpi_var, width=6).pack(side="left", padx=4)
        ttk.Label(row, text="（低于该非白像素比例判为空白；有文字的页永不删）").pack(side="left", padx=(8, 0))
        ttk.Checkbutton(row, text="仅检测不删除", variable=self.clean_dryrun_var).pack(side="left", padx=(16, 0))
        ttk.Button(tab, text="删除空白页", command=self._clean_run).pack(anchor="e", pady=(8, 0))

    def _clean_run(self) -> None:
        src = _clean_path(self.clean_in_var.get())
        if not src:
            messagebox.showwarning("pdftoolbox", "请选择输入 PDF")
            return
        # Read all Tk variables on the main thread (see _number_run).
        out = _clean_path(self.clean_out_var.get()) or None
        dry_run = bool(self.clean_dryrun_var.get())
        try:
            threshold = float(self.clean_threshold_var.get().strip() or 0.002)
            dpi = int(self.clean_dpi_var.get().strip() or 48)
        except ValueError:
            messagebox.showerror("pdftoolbox", "墨迹阈值/检测DPI必须是数字")
            return

        def job():
            from . import ops

            return ops.clean_blank(src, out, threshold=threshold, dpi=dpi, dry_run=dry_run)

        self._run("删空白页", job)

    # -------------------------------------------------------------- info tab
    def _build_info_tab(self) -> None:
        tab = ttk.Frame(self.nb, padding=8)
        self.nb.add(tab, text=" 信息 ")
        self.info_in_var = tk.StringVar()
        self._file_row(tab, "输入 PDF:", self.info_in_var)
        ttk.Button(tab, text="查看信息", command=self._info_run).pack(anchor="e", pady=(8, 0))

    def _info_run(self) -> None:
        src = _clean_path(self.info_in_var.get())
        if not src:
            messagebox.showwarning("pdftoolbox", "请选择输入 PDF")
            return

        def run():
            import json as _json

            from . import ops

            result = ops.pdf_info(src)
            result.messages.append(_json.dumps(result.details, ensure_ascii=False, indent=2, default=str))
            return result

        self._run("信息", run)

    # ------------------------------------------------------------- shared UI
    def _file_row(self, parent, label: str, var: tk.StringVar,
                  save_var: tk.StringVar | None = None,
                  dir_var: tk.StringVar | None = None) -> None:
        row = ttk.Frame(parent)
        row.pack(fill="x", pady=4)
        ttk.Label(row, text=label).pack(side="left")
        ttk.Entry(row, textvariable=var).pack(side="left", fill="x", expand=True, padx=4)

        def pick():
            path = filedialog.askopenfilename(filetypes=_FILETYPES)
            if path:
                var.set(path)
                p = Path(path)
                if save_var is not None and not save_var.get().strip():
                    save_var.set(str(p.with_name(p.stem + "-out.pdf")))
                if dir_var is not None and not dir_var.get().strip():
                    dir_var.set(str(p.with_name(p.stem + "-split")))

        ttk.Button(row, text="选择…", command=pick).pack(side="left")
        if save_var is not None:
            ttk.Label(row, text="输出:").pack(side="left", padx=(12, 0))
            ttk.Entry(row, textvariable=save_var, width=30).pack(side="left", padx=4)

    def show_check(self) -> None:
        import io as _io

        from .cli import run_check

        buf = _io.StringIO()
        old = sys.stdout
        try:
            sys.stdout = buf
            run_check()
        finally:
            sys.stdout = old
        win = tk.Toplevel(self.root)
        win.title("环境检查")
        win.geometry("680x430")
        text = tk.Text(win, wrap="word")
        text.pack(fill="both", expand=True, padx=8, pady=8)
        text.insert("1.0", buf.getvalue())
        text.configure(state="disabled")

    def _on_close(self) -> None:
        if self._busy:
            choice = messagebox.askyesnocancel(
                "pdftoolbox",
                "有操作正在进行。\n\n"
                "是 — 等待本次操作完成后自动退出（推荐）\n"
                "否 — 立即强制退出（写盘是原子的，不会破坏原文件）\n"
                "取消 — 继续工作",
            )
            if choice is None:
                return  # keep working
            if choice:
                # Defer the close until the worker reports completion, so a
                # conversion/save is never terminated mid-write.
                self._quit_when_idle = True
                self.status_var.set("等待当前操作完成后退出 …")
                self._log("将在当前操作完成后自动退出")
                return
            # explicit force-quit: fall through to destroy
        self._destroy()


def main() -> int:
    multiprocessing.freeze_support()
    try:
        import tkinter  # noqa: F401
    except ImportError as exc:
        print(f"GUI unavailable: tkinter is missing from this Python ({exc}).", file=sys.stderr)
        print("The CLI works regardless: pdftoolbox --help", file=sys.stderr)
        return 2
    enable_dpi_awareness()
    try:
        app = App()
    except Exception as exc:
        # Never die silently: a windowed exe has no console, so show the
        # reason in a bare Tk dialog before exiting.
        traceback.print_exc()
        try:
            root = tk.Tk()
            root.withdraw()
            messagebox.showerror(
                "pdftoolbox 启动失败",
                f"{exc.__class__.__name__}: {exc}\n\n请运行 pdftoolbox check 查看环境。",
            )
            root.destroy()
        except Exception:
            pass
        return 1
    app.root.mainloop()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
