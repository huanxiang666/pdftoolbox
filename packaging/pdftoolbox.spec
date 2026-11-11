# -*- mode: python ; coding: utf-8 -*-
"""PyInstaller spec for pdftoolbox.

Builds one onedir bundle (dist/pdftoolbox/) containing two executables:
  pdftoolbox      — console CLI
  pdftoolbox-gui  — windowed Tk GUI

Build:  pyinstaller packaging/pdftoolbox.spec --noconfirm
"""
import os

from PyInstaller.utils.hooks import collect_all

datas, binaries, hiddenimports = [], [], []
for _pkg in ("pymupdf",):
    _d, _b, _h = collect_all(_pkg)
    datas += _d
    binaries += _b
    hiddenimports += _h

_icon_path = os.path.join(SPECPATH, "icon.ico")
icon = _icon_path if os.path.exists(_icon_path) else None

_common = dict(
    pathex=[os.path.join(SPECPATH, os.pardir, "src")],
    datas=datas,
    binaries=binaries,
    hiddenimports=hiddenimports,
    hookspath=[],
    runtime_hooks=[],
    # onnxruntime ships inside pymupdf wheels for optional OCR/AI features we
    # never call; matplotlib/pandas/etc. are unused heavy neighbors.
    excludes=[
        "IPython", "notebook", "sphinx", "pandas",
        "matplotlib", "PyQt5", "PyQt6", "PySide6",
        "onnxruntime",
    ],
    noarchive=False,
)

a_cli = Analysis([os.path.join(SPECPATH, "run_cli.py")], **_common)
pyz_cli = PYZ(a_cli.pure)
exe_cli = EXE(
    pyz_cli,
    a_cli.scripts,
    exclude_binaries=True,
    name="pdftoolbox",
    debug=False,
    strip=False,
    upx=False,
    console=True,
    icon=icon,
)

a_gui = Analysis([os.path.join(SPECPATH, "run_gui.py")], **_common)
pyz_gui = PYZ(a_gui.pure)
exe_gui = EXE(
    pyz_gui,
    a_gui.scripts,
    exclude_binaries=True,
    name="pdftoolbox-gui",
    debug=False,
    strip=False,
    upx=False,
    console=False,
    icon=icon,
)

coll = COLLECT(
    exe_cli,
    exe_gui,
    a_cli.binaries,
    a_cli.datas,
    a_gui.binaries,
    a_gui.datas,
    strip=False,
    upx=False,
    name="pdftoolbox",
)
