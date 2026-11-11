"""Tkinter GUI for pdftoolbox (entry point: pdftoolbox-gui / pdftoolbox gui).

One tab per operation. All work runs on a background thread; the Tk main loop
only drains a queue, keeping the UI responsive and thread-safe.
"""

from __future__ import annotations

import multiprocessing
import queue
import sys
import threading
import tkinter as tk
from pathlib import Path
from tkinter import filedialog, messagebox, ttk
from tkinter.scrolledtext import ScrolledText

from . import SUPPORTED_INPUT_EXTENSIONS, __version__
from .common import POSITIONS, OpError

_POLL_MS = 100
_POSITION_CHOICES = sorted(POSITIONS)
_FILETYPES = [
    ("全部支持格式", " ".join("*" + e for e in SUPPORTED_INPUT_EXTENSIONS)),
    ("PDF", "*.pdf"),
    ("Word", "*.doc *.docx *.odt *.rtf *.wps"),
    ("图片", "*.png *.jpg *.jpeg *.bmp *.tif *.tiff *.gif *.webp"),
    ("所有文件", "*.*"),
]


class App:
    def __init__(self):
        self.q: "queue.Queue[tuple]" = queue.Queue()
        self._busy = False

        self.root = tk.Tk()
        self.root.title(f"pdftoolbox {__version__} — PDF 工具箱")
        self.root.geometry("920x680")
        self.root.minsize(820, 600)

        self.nb = ttk.Notebook(self.root)
        self.nb.pack(fill="both", expand=True, padx=10, pady=(10, 4))
        self._build_merge_tab()
        self._build_split_tab()
        self._build_rotate_tab()
        self._build_number_tab()
        self._build_watermark_tab()
        self._build_clean_tab()
        self._build_info_tab()

        bottom = ttk.Frame(self.root, padding=(10, 0, 10, 10))
        bottom.pack(fill="x")
        self.status_var = tk.StringVar(value="就绪")
        ttk.Label(bottom, textvariable=self.status_var).pack(side="left")
        ttk.Button(bottom, text="环境检查", command=self.show_check).pack(side="right")
        self.log = ScrolledText(self.root, height=8, state="disabled", wrap="word")
        self.log.pack(fill="x", padx=10, pady=(0, 10))

        self.root.protocol("WM_DELETE_WINDOW", self._on_close)
        self.root.after(_POLL_MS, self._poll_queue)

    # ------------------------------------------------------------ utilities
    def _log(self, text: str) -> None:
        self.log.configure(state="normal")
        self.log.insert("end", text.rstrip() + "\n")
        self.log.see("end")
        self.log.configure(state="disabled")

    def _pick_files(self, var: tk.Variable, multiple: bool = True) -> None:
        paths = (filedialog.askopenfilenames(filetypes=_FILETYPES) if multiple
                 else filedialog.askopenfilename(filetypes=_FILETYPES))
        if paths:
            var.set(paths if multiple else [paths])

    def _run(self, label: str, fn) -> None:
        """Run fn() on a worker thread; fn returns an OpResult or raises."""
        if self._busy:
            messagebox.showinfo("pdftoolbox", "上一个操作还在进行中")
            return
        self._busy = True
        self.status_var.set(f"{label} …")
        self._log(f"—— {label} ——")

        def worker():
            try:
                result = fn()
                self.q.put(("done", label, result))
            except OpError as exc:
                self.q.put(("operror", label, str(exc)))
            except Exception as exc:
                self.q.put(("error", label, f"{exc.__class__.__name__}: {exc}"))

        threading.Thread(target=worker, daemon=True).start()

    def _poll_queue(self) -> None:
        try:
            while True:
                msg = self.q.get_nowait()
                kind, label = msg[0], msg[1]
                self._busy = False
                if kind == "done":
                    result = msg[2]
                    self.status_var.set(f"{label} 完成")
                    for line in result.messages:
                        self._log(f"  {line}")
                    if result.output:
                        self._log(f"  输出: {Path(result.output).resolve()}")
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
        self.root.after(_POLL_MS, self._poll_queue)

    def _out_path(self, var: tk.StringVar, default_hint: str) -> str:
        value = var.get().strip()
        return value or default_hint

    # ------------------------------------------------------------- merge tab
    def _build_merge_tab(self) -> None:
        tab = ttk.Frame(self.nb, padding=8)
        self.nb.add(tab, text=" 合并 ")
        self.merge_files: list = []
        listbox = tk.Listbox(tab, height=8)
        listbox.pack(fill="both", expand=True, side="left")
        self.merge_listbox = listbox
        side = ttk.Frame(tab)
        side.pack(side="right", fill="y", padx=(8, 0))
        ttk.Button(side, text="添加文件…", command=self._merge_add).pack(fill="x", pady=2)
        ttk.Button(side, text="上移", command=lambda: self._merge_move(-1)).pack(fill="x", pady=2)
        ttk.Button(side, text="下移", command=lambda: self._merge_move(1)).pack(fill="x", pady=2)
        ttk.Button(side, text="移除选中", command=self._merge_remove).pack(fill="x", pady=2)
        ttk.Button(side, text="清空", command=self._merge_clear).pack(fill="x", pady=2)

        bottom = ttk.Frame(tab)
        bottom.pack(side="bottom", fill="x")
        ttk.Label(bottom, text="合并顺序 = 列表顺序（Word 需要 LibreOffice/MS Word，见环境检查）").pack(side="left")

        self.merge_out_var = tk.StringVar()
        row = ttk.Frame(tab)
        row.pack(side="bottom", fill="x", pady=(6, 0))
        ttk.Label(row, text="输出 PDF:").pack(side="left")
        ttk.Entry(row, textvariable=self.merge_out_var).pack(side="left", fill="x", expand=True, padx=4)
        ttk.Button(row, text="另存为…", command=lambda: self._save_as(self.merge_out_var)).pack(side="left")
        self.merge_run = ttk.Button(row, text="开始合并", command=self._merge_run)
        self.merge_run.pack(side="left", padx=(8, 0))

    def _merge_add(self) -> None:
        paths = filedialog.askopenfilenames(filetypes=_FILETYPES)
        for p in paths:
            self.merge_files.append(p)
            self.merge_listbox.insert(tk.END, p)

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
        out = self.merge_out_var.get().strip()
        if not out:
            first = Path(self.merge_files[0])
            out = str(first.with_name(f"{first.stem}-merged.pdf"))
        files = [Path(f) for f in self.merge_files]

        from . import ops

        self._run(f"合并 {len(files)} 个文件", lambda: ops.merge(files, out))

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

        self._file_row(tab, "输入 PDF:", self.split_in_var)
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
        from . import ops

        src = self.split_in_var.get().strip()
        out_dir = self.split_out_var.get().strip()
        if not src or not out_dir:
            messagebox.showwarning("pdftoolbox", "请填写输入 PDF 和输出目录")
            return
        per_page = self.split_mode_var.get() == "pages"
        ranges = None if per_page else self.split_ranges_var.get().strip()
        self._run("拆分", lambda: ops.split(src, out_dir, per_page=per_page, ranges=ranges))

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
        from . import ops

        src = self.rot_in_var.get().strip()
        if not src:
            messagebox.showwarning("pdftoolbox", "请选择输入 PDF")
            return
        out = self.rot_out_var.get().strip() or None
        angle = int(self.rot_angle_var.get())
        pages = self.rot_pages_var.get().strip() or "all"
        self._run("旋转", lambda: ops.rotate(src, out, angle, pages))

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
        ttk.Label(row2, text="（{n}=页码值 {t}=文档总页数，支持中文）").pack(side="left", padx=(12, 0))
        ttk.Button(tab, text="添加页码", command=self._number_run).pack(anchor="e", pady=(8, 0))

    def _number_run(self) -> None:
        from . import ops

        src = self.num_in_var.get().strip()
        if not src:
            messagebox.showwarning("pdftoolbox", "请选择输入 PDF")
            return
        # Read every Tk variable HERE on the main thread — the worker thread
        # must never touch Tcl state (queue-only threading model).
        out = self.num_out_var.get().strip() or None
        try:
            start_page = int(self.num_start_var.get().strip() or 1)
            first_number = int(self.num_first_var.get().strip() or 1)
            fontsize = float(self.num_size_var.get().strip() or 11)
        except ValueError:
            messagebox.showerror("pdftoolbox", "起始页/起始编号/字号必须是数字")
            return
        fmt = self.num_fmt_var.get() or "{n}"
        position = self.num_pos_var.get()
        self._run("页码", lambda: ops.number_pages(
            src, out, start_page=start_page, first_number=first_number,
            fmt=fmt, position=position, fontsize=fontsize,
        ))

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
        ttk.Button(tab, text="加水印", command=self._watermark_run).pack(anchor="e", pady=(8, 0))

    def _wm_pick_image(self) -> None:
        path = filedialog.askopenfilename(
            filetypes=[("图片", "*.png *.jpg *.jpeg *.bmp *.gif *.webp"), ("所有文件", "*.*")])
        if path:
            self.wm_image_var.set(path)
            self.wm_mode_var.set("image")

    def _watermark_run(self) -> None:
        from . import ops

        src = self.wm_in_var.get().strip()
        if not src:
            messagebox.showwarning("pdftoolbox", "请选择输入 PDF")
            return
        # Read all Tk variables on the main thread (see _number_run).
        out = self.wm_out_var.get().strip() or None
        mode = self.wm_mode_var.get()
        text = self.wm_text_var.get().strip() if mode == "text" else None
        image = self.wm_image_var.get().strip() if mode == "image" else None
        pages = self.wm_pages_var.get().strip() or "all"
        tile = bool(self.wm_tile_var.get())
        try:
            opacity = float(self.wm_opacity_var.get().strip() or 0.25)
            angle = float(self.wm_angle_var.get().strip() or 45)
            fontsize = float(self.wm_size_var.get().strip() or 36)
        except ValueError:
            messagebox.showerror("pdftoolbox", "不透明度/角度/字号必须是数字")
            return
        self._run("水印", lambda: ops.watermark(
            src, out, text=text or None, image=image or None,
            pages=pages, opacity=opacity, angle=angle, fontsize=fontsize,
            tile=tile,
        ))

    # ------------------------------------------------------------- clean tab
    def _build_clean_tab(self) -> None:
        tab = ttk.Frame(self.nb, padding=8)
        self.nb.add(tab, text=" 删空白页 ")
        self.clean_in_var = tk.StringVar()
        self.clean_out_var = tk.StringVar()
        self.clean_threshold_var = tk.StringVar(value="0.002")
        self.clean_dryrun_var = tk.BooleanVar(value=False)
        self._file_row(tab, "输入 PDF:", self.clean_in_var, save_var=self.clean_out_var)
        row = ttk.Frame(tab)
        row.pack(fill="x", pady=4)
        ttk.Label(row, text="墨迹阈值:").pack(side="left")
        ttk.Entry(row, textvariable=self.clean_threshold_var, width=8).pack(side="left", padx=4)
        ttk.Label(row, text="（低于该非白像素比例判为空白；有文字的页永不删）").pack(side="left")
        ttk.Checkbutton(row, text="仅检测不删除", variable=self.clean_dryrun_var).pack(side="left", padx=(16, 0))
        ttk.Button(tab, text="删除空白页", command=self._clean_run).pack(anchor="e", pady=(8, 0))

    def _clean_run(self) -> None:
        from . import ops

        src = self.clean_in_var.get().strip()
        if not src:
            messagebox.showwarning("pdftoolbox", "请选择输入 PDF")
            return
        # Read all Tk variables on the main thread (see _number_run).
        out = self.clean_out_var.get().strip() or None
        dry_run = bool(self.clean_dryrun_var.get())
        try:
            threshold = float(self.clean_threshold_var.get().strip() or 0.002)
        except ValueError:
            messagebox.showerror("pdftoolbox", "墨迹阈值必须是数字")
            return
        self._run("删空白页", lambda: ops.clean_blank(
            src, out, threshold=threshold, dry_run=dry_run,
        ))

    # -------------------------------------------------------------- info tab
    def _build_info_tab(self) -> None:
        tab = ttk.Frame(self.nb, padding=8)
        self.nb.add(tab, text=" 信息 ")
        self.info_in_var = tk.StringVar()
        self._file_row(tab, "输入 PDF:", self.info_in_var)
        ttk.Button(tab, text="查看信息", command=self._info_run).pack(anchor="e", pady=(8, 0))

    def _info_run(self) -> None:
        import json as _json

        from . import ops

        src = self.info_in_var.get().strip()
        if not src:
            messagebox.showwarning("pdftoolbox", "请选择输入 PDF")
            return

        def run():
            result = ops.pdf_info(src)
            result.messages.append(_json.dumps(result.details, ensure_ascii=False, indent=2, default=str))
            return result

        self._run("信息", run)

    # ------------------------------------------------------------- shared UI
    def _file_row(self, parent, label: str, var: tk.StringVar,
                  save_var: tk.StringVar | None = None) -> None:
        row = ttk.Frame(parent)
        row.pack(fill="x", pady=4)
        ttk.Label(row, text=label).pack(side="left")
        ttk.Entry(row, textvariable=var).pack(side="left", fill="x", expand=True, padx=4)

        def pick():
            path = filedialog.askopenfilename(filetypes=_FILETYPES)
            if path:
                var.set(path)
                if save_var is not None and not save_var.get().strip():
                    p = Path(path)
                    save_var.set(str(p.with_name(p.stem + "-out.pdf")))

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
            if not messagebox.askyesno("pdftoolbox", "操作进行中，确定退出吗？"):
                return
            self.root.destroy()
            import os

            os._exit(0)
        self.root.destroy()


def main() -> int:
    multiprocessing.freeze_support()
    try:
        import tkinter  # noqa: F401
    except ImportError as exc:
        print(f"GUI unavailable: tkinter is missing from this Python ({exc}).", file=sys.stderr)
        print("The CLI works regardless: pdftoolbox --help", file=sys.stderr)
        return 2
    App().root.mainloop()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
