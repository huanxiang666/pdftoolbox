"""Command line interface for pdftoolbox (subcommand style)."""

from __future__ import annotations

import argparse
import json
import multiprocessing
import sys
from pathlib import Path

from . import SUPPORTED_INPUT_EXTENSIONS, __version__
from .common import POSITIONS, OpError
from .ingest import WORD_INSTALL_HINT, detect_word_converter


def _ensure_utf8_stdio() -> None:
    """Never crash on CJK output under Windows cp1252 consoles (see dwg2pdf)."""
    import io as _io

    for name in ("stdout", "stderr"):
        stream = getattr(sys, name)
        encoding = (getattr(stream, "encoding", "") or "").lower().replace("-", "")
        if "utf8" in encoding:
            continue
        try:
            stream.reconfigure(encoding="utf-8", errors="replace")
            continue
        except Exception:
            pass
        buffer = getattr(stream, "buffer", None)
        if buffer is not None:
            try:
                setattr(sys, name, _io.TextIOWrapper(buffer, encoding="utf-8", errors="replace"))
            except Exception:
                pass


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="pdftoolbox",
        description=(
            "PDF toolbox: merge (PDF/Word/images), split, rotate, page numbers, "
            "watermark, blank-page removal. "
            "PDF 工具箱：合并（PDF/Word/图片）、拆分、旋转、页码、水印、删空白页。"
        ),
    )
    parser.add_argument("--version", action="version", version=f"pdftoolbox {__version__}")
    sub = parser.add_subparsers(dest="command", required=False)

    p = sub.add_parser("merge", help="merge PDF/Word/image files into one PDF (order = argument order)")
    p.add_argument("inputs", nargs="+", help=f"input files ({', '.join(SUPPORTED_INPUT_EXTENSIONS)}) and/or directories")
    p.add_argument("-o", "--output", required=True, help="output PDF path")
    p.add_argument("-r", "--recursive", action="store_true", help="scan input directories recursively")
    p.add_argument("--image-dpi", type=int, default=96, help="DPI used to size images (default: 96)")
    p.add_argument("--word-timeout", type=int, default=300, help="seconds allowed per Word conversion (default: 300)")

    p = sub.add_parser("split", help="split a PDF into per-page files or one file per range")
    p.add_argument("input")
    p.add_argument("-o", "--output-dir", required=True, help="directory for the split files")
    g = p.add_mutually_exclusive_group(required=True)
    g.add_argument("--per-page", action="store_true", help="one PDF per page")
    g.add_argument("--ranges", help='comma-separated page specs, one output file each, e.g. "1-3,5,8-"')
    p.add_argument("--prefix", help="output filename prefix (default: input stem)")

    p = sub.add_parser("rotate", help="rotate pages clockwise")
    p.add_argument("input")
    p.add_argument("-o", "--output", help="output PDF (default: <input>-rot<angle>.pdf next to input)")
    p.add_argument("--angle", type=int, required=True, help="90, 180, 270 (or negatives)")
    p.add_argument("--pages", help='page spec, e.g. "1-3,5" (default: all)')

    p = sub.add_parser("number", help="stamp page numbers starting from a custom page")
    p.add_argument("input")
    p.add_argument("-o", "--output", help="output PDF (default: <input>-numbered.pdf)")
    p.add_argument("--start-page", type=int, default=1, help="first page to stamp (default: 1)")
    p.add_argument("--first-number", type=int, default=1, help="number value shown on start page (default: 1)")
    p.add_argument("--format", dest="fmt", default="{n}", help='number format with {n} and {t}, e.g. "第 {n} 页 / 共 {t} 页"')
    p.add_argument("--position", default="bottom-center", choices=sorted(POSITIONS), help="anchor on the page")
    p.add_argument("--margin", type=float, default=24, help="distance from page edge in pt (default: 24)")
    p.add_argument("--fontsize", type=float, default=11)
    p.add_argument("--color", default="black", help="color name, #rrggbb, or 'r,g,b'")
    p.add_argument("--opacity", type=float, default=1.0)

    p = sub.add_parser("watermark", help="add a text or image watermark")
    p.add_argument("input")
    p.add_argument("-o", "--output", help="output PDF (default: <input>-watermark.pdf)")
    src = p.add_mutually_exclusive_group(required=True)
    src.add_argument("--text", help="watermark text (CJK supported)")
    src.add_argument("--image", help="watermark image file (png/jpg, alpha respected)")
    p.add_argument("--pages", help='page spec (default: all)')
    p.add_argument("--opacity", type=float, default=0.25)
    p.add_argument("--angle", type=float, default=45, help="text rotation in degrees (default: 45)")
    p.add_argument("--fontsize", type=float, default=36)
    p.add_argument("--color", default="gray")
    p.add_argument("--position", default="center", choices=sorted(POSITIONS), help="anchor for single text / image watermark")
    p.add_argument("--tile", action="store_true", help="tile the text watermark over the whole page")
    p.add_argument("--image-scale", type=float, default=0.25, help="image width as fraction of page width")

    p = sub.add_parser("clean", help="remove visually blank pages")
    p.add_argument("input")
    p.add_argument("-o", "--output", help="output PDF (default: <input>-clean.pdf)")
    p.add_argument("--threshold", type=float, default=0.002, help="ink fraction below which a page counts as blank (default: 0.002)")
    p.add_argument("--dpi", type=int, default=48, help="raster DPI used for blank detection (default: 48)")
    p.add_argument("--dry-run", action="store_true", help="only report which pages are blank")

    p = sub.add_parser("info", help="show page count, sizes, metadata")
    p.add_argument("input")
    p.add_argument("--json", action="store_true", help="machine-readable output")

    p = sub.add_parser("check", help="print environment status (Word converter etc.) and exit")

    p = sub.add_parser("gui", help="launch the graphical interface")
    return parser


def run_check() -> int:
    print(f"pdftoolbox {__version__}  |  python {sys.version.split()[0]}  |  {sys.platform}")
    try:
        import pymupdf

        version = getattr(pymupdf, "__version__", None) or pymupdf.VersionBind
        print(f"pymupdf {version}: OK (PDF engine)")
    except Exception as exc:
        print(f"pymupdf: MISSING ({exc}) — pdftoolbox cannot work without it")
    try:
        import PIL

        print(f"pillow {PIL.__version__}: OK (image handling)")
    except Exception as exc:
        print(f"pillow: MISSING ({exc})")
    converter = detect_word_converter()
    if converter:
        print(f"Word converter: {converter.display_name}")
        print("Word input (.doc/.docx/.odt/.rtf/.wps): supported")
    else:
        print("Word converter: not found")
        print(WORD_INSTALL_HINT)
    try:
        import docx  # noqa: F401

        print("built-in .docx fallback renderer: OK (basic layout, no Office needed)")
    except Exception:
        print("built-in .docx fallback renderer: unavailable (pip install python-docx)")
    try:
        import tkinter  # noqa: F401

        print("tkinter: OK (gui available)")
    except Exception:
        print("tkinter: not available on this Python (gui disabled; CLI unaffected)")
    return 0


def _report(result, quiet: bool, as_json: bool = False) -> None:
    if as_json:
        payload = {
            "output": result.output,
            "pages": result.pages,
            "messages": result.messages,
            "details": result.details,
        }
        print(json.dumps(payload, ensure_ascii=False, indent=2, default=str))
        return
    if quiet:
        return
    for line in result.messages:
        print(line)
    if result.output:
        print(f"output: {Path(result.output).resolve()}")


def main(argv: list[str] | None = None) -> int:
    multiprocessing.freeze_support()  # required for PyInstaller-frozen executables
    _ensure_utf8_stdio()  # before argparse: help texts contain CJK
    parser = build_parser()
    args = parser.parse_args(argv)

    if args.command is None:
        # Bare `pdftoolbox` (e.g. a double-clicked exe): show a friendly
        # overview instead of a bare "arguments required" argparse error.
        print(f"pdftoolbox {__version__} — PDF 工具箱\n")
        print("图形界面：pdftoolbox gui   （或双击 pdftoolbox-gui.exe）")
        print("命令行用法：")
        for cmd, desc in (
            ("merge",  "合并 PDF/Word/图片为一个 PDF"),
            ("split",  "拆分 PDF（每页或按页码范围）"),
            ("rotate", "旋转页面"),
            ("number", "添加页码（自定义起始页/编号/格式）"),
            ("watermark", "添加文字/图片水印"),
            ("clean",  "删除空白页"),
            ("info",   "查看 PDF 信息"),
            ("check",  "环境自检"),
            ("gui",    "启动图形界面"),
        ):
            print(f"  pdftoolbox {cmd:<9}  {desc}")
        print("\n完整帮助：pdftoolbox <命令> --help")
        return 0

    if args.command == "check":
        return run_check()

    if args.command == "gui":
        try:
            from . import gui
        except ImportError as exc:
            print(f"GUI unavailable on this Python: {exc}", file=sys.stderr)
            return 2
        try:
            return gui.main()
        except Exception as exc:
            # The GUI reports errors in dialogs; mirror them on stderr so a
            # console-launched `pdftoolbox gui` never dies silently either.
            print(f"error: GUI failed: {exc.__class__.__name__}: {exc}", file=sys.stderr)
            return 1

    try:
        from . import ops
        from .ingest import collect_inputs
    except ImportError as exc:
        # Missing/broken dependency (typically pymupdf): readable one-liner
        # instead of a traceback.
        print(f"error: failed to load a required dependency: {exc}", file=sys.stderr)
        print("hint: pip install pymupdf pillow", file=sys.stderr)
        return 1

    try:
        if args.command == "merge":
            files, ignored = collect_inputs(args.inputs, recursive=args.recursive)
            for path in ignored:
                print(f"warning: ignoring unsupported input: {path}", file=sys.stderr)
            if not files:
                raise OpError("no supported input files found "
                              f"({', '.join(SUPPORTED_INPUT_EXTENSIONS)})")
            result = ops.merge(files, args.output, dpi=args.image_dpi,
                               word_timeout=args.word_timeout)
        elif args.command == "split":
            result = ops.split(args.input, args.output_dir, per_page=args.per_page,
                               ranges=args.ranges, prefix=args.prefix)
        elif args.command == "rotate":
            result = ops.rotate(args.input, args.output, args.angle, args.pages)
        elif args.command == "number":
            result = ops.number_pages(
                args.input, args.output, start_page=args.start_page,
                first_number=args.first_number, fmt=args.fmt,
                position=args.position, margin=args.margin,
                fontsize=args.fontsize, color=args.color, opacity=args.opacity,
            )
        elif args.command == "watermark":
            result = ops.watermark(
                args.input, args.output, text=args.text, image=args.image,
                pages=args.pages, opacity=args.opacity, angle=args.angle,
                fontsize=args.fontsize, color=args.color,
                position=args.position, tile=args.tile,
                image_scale=args.image_scale,
            )
        elif args.command == "clean":
            result = ops.clean_blank(args.input, args.output, threshold=args.threshold,
                                     dpi=args.dpi, dry_run=args.dry_run)
        elif args.command == "info":
            result = ops.pdf_info(args.input)
            _report(result, quiet=False, as_json=args.json)
            return 0
        else:  # pragma: no cover - argparse guards this
            parser.print_help()
            return 2
        _report(result, quiet=False)
        return 0
    except OpError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        print("interrupted", file=sys.stderr)
        return 130
    except Exception as exc:
        # Never leave users with a bare traceback: any unexpected failure
        # (locked output file, disk full, corrupted input edge case) becomes
        # a readable one-liner with the same failure exit code.
        print(f"error: {exc.__class__.__name__}: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
