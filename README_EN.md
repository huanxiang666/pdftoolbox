# pdftoolbox — Cross-platform PDF Toolbox

[![CI](https://github.com/huanxiang666/pdftoolbox/actions/workflows/ci.yml/badge.svg)](https://github.com/huanxiang666/pdftoolbox/actions/workflows/ci.yml)
[![License: AGPL-3.0](https://img.shields.io/badge/license-AGPL--3.0-blue.svg)](LICENSE)

**Windows / Linux** PDF toolbox with CLI and GUI: **merge** (PDF + Word + images), **split**, **rotate**, **page numbers from any start page**, **watermarks** (text/image, tiled), **blank-page removal**, **info**.

中文文档：[README.md](README.md) · Sister project: [dwg2pdf](https://github.com/huanxiang666/dwg2pdf)

## Install

**Binaries (recommended):** grab `pdftoolbox-x.y.z-windows-x64.zip` or `pdftoolbox-x.y.z-linux-x64.tar.gz` from [Releases](https://github.com/huanxiang666/pdftoolbox/releases), unpack, run `pdftoolbox-gui(.exe)` or `pdftoolbox(.exe)`.

**pip (Python 3.10+):** `pip install git+https://github.com/huanxiang666/pdftoolbox.git`

## Word support

PDF and image inputs work out of the box. Merging `.doc/.docx/.odt/.rtf/.wps` needs **LibreOffice** (`sudo apt install libreoffice-writer`, or libreoffice.org) or **MS Word on Windows** (`pip install "pdftoolbox[word]"`). Auto-detected; override with `PDFTOOLBOX_WORD_CONVERTER`. `pdftoolbox check` shows the environment status.

## Usage

```bash
pdftoolbox merge report.docx photo.png annex.pdf -o combined.pdf
pdftoolbox split big.pdf -o parts --per-page
pdftoolbox split big.pdf -o parts --ranges "1-3,5,8-"
pdftoolbox rotate in.pdf -o out.pdf --angle 90 --pages 1-3
pdftoolbox number in.pdf -o out.pdf --start-page 2 --first-number 10 --format "第 {n} 页 共 {t} 页"
pdftoolbox watermark in.pdf -o out.pdf --text "机密" --tile --opacity 0.18
pdftoolbox clean in.pdf -o out.pdf            # remove blank pages (--dry-run to preview)
pdftoolbox info in.pdf --json
pdftoolbox gui                                 # graphical interface
```

Exit codes: `0` ok · `1` operation failed · `2` usage error. Page specs: `"3"`, `"2-7"`, `"5-"`, `"1-3,5"`, `all`.

Highlights: atomic saves (in-place output is safe), CJK-capable searchable page numbers/watermarks, EXIF-orientation-aware image embedding, blank detection that never drops pages containing text.

## Development & release

```bash
pip install -e ".[dev]" && pytest -q        # 62+ tests
pyinstaller packaging/pdftoolbox.spec       # dist/pdftoolbox/
```

Push a `v*` tag — GitHub Actions builds Windows/Linux bundles, smoke-tests the frozen binaries, and publishes a Release.

## License

**AGPL-3.0** (bundles [PyMuPDF](https://pymupdf.org/), AGPL). [Pillow](https://python-pillow.org/) (HPND). External converters (LibreOffice MPL-2.0, docx2pdf MIT) run as separate processes. See [LICENSE](LICENSE) and [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md).
