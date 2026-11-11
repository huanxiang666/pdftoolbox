# Third-Party Notices

pdftoolbox bundles or invokes the following third-party components. Each
component remains under its own license.

## Bundled Python libraries (shipped inside the frozen executables)

- **PyMuPDF** — the PDF engine (merge/split/rotate/text/image operations).
  Copyright (C) Artifex Software, Inc. License: AGPL-3.0 (commercial licensing
  available from Artifex). https://pymupdf.org/
  Because pdftoolbox bundles PyMuPDF, the combined executables are distributed
  under the AGPL-3.0 (see LICENSE).

- **Pillow (PIL)** — image sizing, EXIF orientation, alpha handling.
  License: HPND (MIT-CMU style). https://python-pillow.org/

- **Python** standard library and the CPython runtime embedded by PyInstaller.
  License: PSF License Agreement. https://docs.python.org/3/license.html

- **Tk/Tcl** — the Tkinter GUI runtime, embedded by PyInstaller on some
  platforms. License: BSD-style. https://www.tcl.tk/software/tcltk/license.html

- MuPDF (inside PyMuPDF) embeds the **Droid Sans Fallback** font for CJK text.
  License: Apache-2.0.

## Optional external converters (separate processes, NOT bundled)

- **LibreOffice** — Word/ODF/RTF to PDF conversion in headless mode.
  License: MPL-2.0. https://www.libreoffice.org/
- **docx2pdf** (optional Python extra `word`, Windows only) — bridges to
  Microsoft Word via COM. License: MIT. https://pypi.org/project/docx2pdf/
- **Microsoft Word** — user-provided; its own license applies.

## Packaging tooling (build time only, not shipped)

- **PyInstaller** — License: GPL-2.0-or-later with a bootloader exception that
  keeps produced executables under this project's license. https://pyinstaller.org/
