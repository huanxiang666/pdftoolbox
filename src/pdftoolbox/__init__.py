"""pdftoolbox — cross-platform PDF toolbox.

Operations: merge (PDF + Word + images), split, rotate, page numbering,
watermark (text/image), blank-page removal, info.

All PDF work runs on PyMuPDF; Word documents are converted through an
external LibreOffice/MS-Word bridge (auto-detected).
"""

from __future__ import annotations

__version__ = "0.1.0"

PDF_EXTENSIONS = (".pdf",)
IMAGE_EXTENSIONS = (".png", ".jpg", ".jpeg", ".bmp", ".tif", ".tiff", ".gif", ".webp")
WORD_EXTENSIONS = (".doc", ".docx", ".odt", ".rtf", ".wps")
SUPPORTED_INPUT_EXTENSIONS = PDF_EXTENSIONS + IMAGE_EXTENSIONS + WORD_EXTENSIONS
