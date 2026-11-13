"""Manual integration check against an installed Office suite; not a unit test."""
from pathlib import Path
import hashlib
import multiprocessing


def main():
    from docx import Document
    from pdftoolbox.ingest import detect_wps, convert_word_to_pdf
    from pdftoolbox import ops
    import pymupdf

    root = Path(__file__).resolve().parents[1] / "verification" / "中文 路径"
    root.mkdir(parents=True, exist_ok=True)
    src = root / "中文测试 文档.docx"
    doc = Document()
    doc.add_heading("WPS 转换验证", 0)
    doc.add_paragraph("中文正文与 English marker WPS-ROUNDTRIP-2026")
    table = doc.add_table(rows=2, cols=2)
    table.cell(0, 0).text = "项目"
    table.cell(0, 1).text = "金额"
    table.cell(1, 0).text = "测试"
    table.cell(1, 1).text = "123.45"
    doc.save(src)
    before = hashlib.sha256(src.read_bytes()).hexdigest()
    conv = detect_wps()
    assert conv, "WPS COM unavailable"
    out = convert_word_to_pdf(src, root / "output", conv, timeout=45)
    with pymupdf.open(out) as pdf:
        text = "".join(p.get_text() for p in pdf)
        assert "WPS-ROUNDTRIP-2026" in text and "123.45" in text and "中文" in text, repr(text)
        print("WPS DOCX export: PASS; pages =", pdf.page_count, flush=True)
    result = ops.merge([src, out], root / "mixed.pdf", converter=conv, word_timeout=45)
    assert result.pages >= 2
    assert hashlib.sha256(src.read_bytes()).hexdigest() == before
    print("Mixed DOCX + PDF merge and unchanged source: PASS", flush=True)


if __name__ == "__main__":
    multiprocessing.freeze_support()
    main()
