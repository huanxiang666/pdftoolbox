"""PyInstaller launcher for the GUI executable (windowed, no console)."""
import multiprocessing
import sys


def _fatal(msg: str) -> None:
    """A windowed exe has no console: surface fatal startup errors in a dialog."""
    try:
        import ctypes

        ctypes.windll.user32.MessageBoxW(None, msg, "pdftoolbox 启动失败", 0x10)
    except Exception:
        try:
            print(msg, file=sys.stderr)
        except Exception:
            pass


if __name__ == "__main__":
    # freeze_support MUST run before importing pdftoolbox.* : the office
    # bridge spawns worker processes, and a spawned child re-imports this
    # launcher — without freeze_support first it would recursively open
    # another window instead of becoming a worker.
    multiprocessing.freeze_support()
    try:
        from pdftoolbox.gui import main
    except Exception as exc:
        _fatal(f"pdftoolbox 组件加载失败: {exc.__class__.__name__}: {exc}")
        sys.exit(1)
    try:
        sys.exit(main())
    except SystemExit:
        raise
    except Exception as exc:
        _fatal(f"pdftoolbox 运行失败: {exc.__class__.__name__}: {exc}")
        sys.exit(1)
