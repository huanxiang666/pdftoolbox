"""Operation tests: merge, split, rotate, number, watermark, clean, info.

All assertions are on user-visible outcomes (page counts, extractable text,
rotations, images), never on internals.
"""

from __future__ import annotations

import pymupdf
import pytest

from conftest import make_image, make_pdf

from pdftoolbox.common import OpError
from pdftoolbox import ops


def page_texts(path) -> list[str]:
    doc = pymupdf.open(str(path))
    try:
        return [p.get_text().strip() for p in doc]
    finally:
        doc.close()


# --------------------------------------------------------------------- merge
class TestMerge:
    def test_order_and_counts(self, tmp_path):
        a = make_pdf(tmp_path / "a.pdf", pages=2, marker="A")
        b = make_pdf(tmp_path / "b.pdf", pages=3, marker="B")
        out = tmp_path / "m.pdf"
        res = ops.merge([a, b], out)
        assert res.pages == 5
        texts = page_texts(out)
        assert texts[0].startswith("A-PAGE-1")
        assert texts[2].startswith("B-PAGE-1")
        assert texts[4].startswith("B-PAGE-3")

    def test_reverse_order_respected(self, tmp_path):
        a = make_pdf(tmp_path / "a.pdf", pages=1, marker="A")
        b = make_pdf(tmp_path / "b.pdf", pages=1, marker="B")
        out = tmp_path / "m.pdf"
        ops.merge([b, a], out)
        assert page_texts(out)[0].startswith("B-PAGE")

    def test_image_input(self, tmp_path):
        a = make_pdf(tmp_path / "a.pdf", pages=1, marker="A")
        img = make_image(tmp_path / "red.png")
        out = tmp_path / "m.pdf"
        res = ops.merge([a, img], out, dpi=96)
        assert res.pages == 2
        doc = pymupdf.open(str(out))
        # image page sized at 96dpi: 120px -> 90pt
        assert doc[1].rect.width == pytest.approx(90, abs=2)
        assert len(doc[1].get_images()) == 1
        doc.close()

    def test_missing_input_raises(self, tmp_path):
        with pytest.raises(OpError, match="not found"):
            ops.merge([tmp_path / "ghost.pdf"], tmp_path / "out.pdf")

    def test_empty_inputs_raises(self):
        with pytest.raises(OpError):
            ops.merge([], "out.pdf")

    def test_unsupported_extension(self, tmp_path):
        bad = tmp_path / "notes.txt"
        bad.write_text("hi")
        with pytest.raises(OpError, match="unsupported"):
            ops.merge([bad], tmp_path / "out.pdf")


# --------------------------------------------------------------------- split
class TestSplit:
    def test_per_page(self, tmp_path):
        src = make_pdf(tmp_path / "s.pdf", pages=4, marker="S")
        res = ops.split(src, tmp_path / "out", per_page=True)
        assert res.details["count"] == 4
        first = page_texts(tmp_path / "out" / "s-0001.pdf")
        assert first == ["S-PAGE-1"]

    def test_ranges(self, tmp_path):
        src = make_pdf(tmp_path / "s.pdf", pages=9, marker="S")
        res = ops.split(src, tmp_path / "out", ranges="1-3,5,8-")
        assert res.details["count"] == 3
        assert len(page_texts(tmp_path / "out" / "s-1-3.pdf")) == 3
        assert page_texts(tmp_path / "out" / "s-5.pdf") == ["S-PAGE-5"]
        assert len(page_texts(tmp_path / "out" / "s-8-.pdf")) == 2

    def test_no_mode_raises(self, tmp_path):
        src = make_pdf(tmp_path / "s.pdf", pages=2)
        with pytest.raises(OpError, match="--per-page"):
            ops.split(src, tmp_path / "out")

    def test_bad_range_raises(self, tmp_path):
        src = make_pdf(tmp_path / "s.pdf", pages=2)
        with pytest.raises(OpError, match="out of bounds"):
            ops.split(src, tmp_path / "out", ranges="1-9")

    def test_duplicate_ranges_get_unique_names(self, tmp_path):
        """Regression (review): '1-2,1-2' must produce two files, not silently
        overwrite one while reporting count=2."""
        src = make_pdf(tmp_path / "s.pdf", pages=4)
        res = ops.split(src, tmp_path / "out", ranges="1-2,1-2")
        assert res.details["count"] == 2
        files = list((tmp_path / "out").glob("*.pdf"))
        assert len(files) == 2


# -------------------------------------------------------------------- rotate
class TestRotate:
    def test_subset_and_cumulative(self, tmp_path):
        src = make_pdf(tmp_path / "r.pdf", pages=3)
        out = tmp_path / "r90.pdf"
        ops.rotate(src, out, 90, "1-2")
        doc = pymupdf.open(str(out))
        assert [p.rotation for p in doc] == [90, 90, 0]
        doc.close()
        # rotate again: cumulative
        out2 = tmp_path / "r180.pdf"
        ops.rotate(out, out2, 90, "1")
        doc = pymupdf.open(str(out2))
        assert [p.rotation for p in doc] == [180, 90, 0]
        doc.close()

    def test_negative_angle(self, tmp_path):
        src = make_pdf(tmp_path / "r.pdf", pages=1)
        out = tmp_path / "r-90.pdf"
        ops.rotate(src, out, -90)
        doc = pymupdf.open(str(out))
        assert doc[0].rotation == 270
        doc.close()

    def test_invalid_angles(self, tmp_path):
        src = make_pdf(tmp_path / "r.pdf", pages=1)
        with pytest.raises(OpError, match="multiple of 90"):
            ops.rotate(src, tmp_path / "x.pdf", 45)
        with pytest.raises(OpError, match="rotates nothing"):
            ops.rotate(src, tmp_path / "x.pdf", 360)

    def test_default_output_name(self, tmp_path):
        src = make_pdf(tmp_path / "r.pdf", pages=1)
        res = ops.rotate(src, None, 180)
        assert res.output.endswith("r-rot180.pdf")


# -------------------------------------------------------------------- number
class TestNumber:
    def test_start_page_and_first_number(self, tmp_path):
        src = make_pdf(tmp_path / "n.pdf", pages=5, marker="N")
        out = tmp_path / "n-out.pdf"
        res = ops.number_pages(src, out, start_page=3, first_number=10)
        assert res.pages == 3
        texts = page_texts(out)
        assert "10" not in texts[0] and "11" not in texts[1]  # pages 1-2 untouched
        assert texts[2].endswith("10")
        assert texts[4].endswith("12")

    def test_cjk_format_searchable(self, tmp_path):
        src = make_pdf(tmp_path / "n.pdf", pages=2)
        out = tmp_path / "n-cjk.pdf"
        ops.number_pages(src, out, fmt="第 {n} 页 共 {t} 页")
        doc = pymupdf.open(str(out))
        assert "第 1 页 共 2 页" in doc[0].get_text()
        assert "第 2 页 共 2 页" in doc[1].get_text()
        doc.close()

    def test_positions_differ(self, tmp_path):
        src = make_pdf(tmp_path / "n.pdf", pages=1)
        outs = {}
        for pos in ("top-left", "bottom-right"):
            o = tmp_path / f"n-{pos}.pdf"
            ops.number_pages(src, o, position=pos, margin=20)
            doc = pymupdf.open(str(o))
            blocks = doc[0].get_text("blocks")
            number_block = [b for b in blocks if b[4].strip() == "1"][0]
            outs[pos] = (number_block[0], number_block[1])
            doc.close()
        # top-left stamp sits above and left of bottom-right stamp
        assert outs["top-left"][1] < outs["bottom-right"][1]
        assert outs["top-left"][0] < outs["bottom-right"][0]

    def test_errors(self, tmp_path):
        src = make_pdf(tmp_path / "n.pdf", pages=2)
        with pytest.raises(OpError, match=r"\{n\}"):
            ops.number_pages(src, tmp_path / "x.pdf", fmt="{t} only")
        with pytest.raises(OpError, match="unknown placeholder"):
            ops.number_pages(src, tmp_path / "x.pdf", fmt="{n} {x}")
        with pytest.raises(OpError, match="beyond"):
            ops.number_pages(src, tmp_path / "x.pdf", start_page=9)
        with pytest.raises(OpError, match="opacity"):
            ops.number_pages(src, tmp_path / "x.pdf", opacity=0)
        with pytest.raises(OpError, match="fontsize"):
            ops.number_pages(src, tmp_path / "x.pdf", fontsize=0)


# ----------------------------------------------------------------- watermark
class TestWatermark:
    def test_text_single_searchable(self, tmp_path):
        src = make_pdf(tmp_path / "w.pdf", pages=2)
        out = tmp_path / "w-out.pdf"
        res = ops.watermark(src, out, text="机密 DRAFT", opacity=0.3, angle=30)
        assert res.pages == 2
        doc = pymupdf.open(str(out))
        for page in doc:
            assert "机密 DRAFT" in page.get_text()
        doc.close()

    def test_text_tile_more_than_single(self, tmp_path):
        src = make_pdf(tmp_path / "w.pdf", pages=1)
        single = tmp_path / "w-single.pdf"
        tiled = tmp_path / "w-tile.pdf"
        ops.watermark(src, single, text="WM", tile=False)
        ops.watermark(src, tiled, text="WM", tile=True)
        n_single = page_texts(single)[0].count("WM")
        n_tiled = page_texts(tiled)[0].count("WM")
        assert n_single >= 1
        assert n_tiled > n_single

    def test_page_subset(self, tmp_path):
        src = make_pdf(tmp_path / "w.pdf", pages=3)
        out = tmp_path / "w-sub.pdf"
        ops.watermark(src, out, text="WM", pages="2")
        texts = page_texts(out)
        assert "WM" not in texts[0] and "WM" in texts[1] and "WM" not in texts[2]

    def test_image_watermark(self, tmp_path):
        src = make_pdf(tmp_path / "w.pdf", pages=1)
        img = make_image(tmp_path / "logo.png")
        out = tmp_path / "w-img.pdf"
        ops.watermark(src, out, image=str(img), image_scale=0.3, opacity=0.5)
        doc = pymupdf.open(str(out))
        assert len(doc[0].get_images()) == 1
        doc.close()

    def test_validation(self, tmp_path):
        src = make_pdf(tmp_path / "w.pdf", pages=1)
        with pytest.raises(OpError, match="exactly one"):
            ops.watermark(src, tmp_path / "x.pdf")
        with pytest.raises(OpError, match="exactly one"):
            ops.watermark(src, tmp_path / "x.pdf", text="a", image="b.png")
        with pytest.raises(OpError, match="opacity"):
            ops.watermark(src, tmp_path / "x.pdf", text="a", opacity=1.5)
        with pytest.raises(OpError, match="fontsize"):
            ops.watermark(src, tmp_path / "x.pdf", text="a", fontsize=0)
        with pytest.raises(OpError, match="image-scale"):
            ops.watermark(src, tmp_path / "x.pdf", image=__file__, image_scale=0)
        with pytest.raises(OpError, match="not found"):
            ops.watermark(src, tmp_path / "x.pdf", image=str(tmp_path / "ghost.png"))


# --------------------------------------------------------------- clean blank
class TestCleanBlank:
    def test_detects_and_removes(self, tmp_path):
        src = make_pdf(tmp_path / "c.pdf", pages=6, marker="C",
                       blank_pages=(3,), white_image_pages=(5,))
        out = tmp_path / "c-clean.pdf"
        res = ops.clean_blank(src, out)
        assert res.details["blank_pages"] == [3, 5]
        assert res.pages == 4
        texts = page_texts(out)
        assert texts == ["C-PAGE-1", "C-PAGE-2", "C-PAGE-4", "C-PAGE-6"]

    def test_text_pages_never_removed_even_if_odd(self, tmp_path):
        # a page with only a tiny dot of text is NOT blank regardless of ink
        doc = pymupdf.open()
        p = doc.new_page()
        p.insert_text((300, 400), ".", fontsize=2, color=(0.95, 0.95, 0.95))
        src = tmp_path / "tiny.pdf"
        doc.save(str(src))
        doc.close()
        res = ops.clean_blank(src, tmp_path / "tiny-clean.pdf")
        assert res.details["blank_pages"] == []

    def test_dry_run_writes_nothing(self, tmp_path):
        src = make_pdf(tmp_path / "c.pdf", pages=2, blank_pages=(2,))
        out = tmp_path / "should-not-exist.pdf"
        res = ops.clean_blank(src, out, dry_run=True)
        assert res.details["blank_pages"] == [2]
        assert not out.exists()

    def test_all_blank_refuses(self, tmp_path):
        src = make_pdf(tmp_path / "c.pdf", pages=2, blank_pages=(1, 2))
        with pytest.raises(OpError, match="all 2 pages are blank"):
            ops.clean_blank(src, tmp_path / "x.pdf")

    def test_no_blanks_copies_through(self, tmp_path):
        src = make_pdf(tmp_path / "c.pdf", pages=2)
        out = tmp_path / "c-clean.pdf"
        res = ops.clean_blank(src, out)
        assert res.pages == 2
        assert out.exists()


# ---------------------------------------------------------------------- info
def test_info(tmp_path):
    src = make_pdf(tmp_path / "i.pdf", pages=3, marker="I", blank_pages=(2,))
    res = ops.pdf_info(src)
    d = res.details
    assert d["pages"] == 3
    assert d["pages_with_text"] == 2
    assert sum(d["page_sizes"].values()) == 3


def test_ops_reject_non_pdf_input(tmp_path):
    img = make_image(tmp_path / "x.png")
    with pytest.raises(OpError, match="takes a PDF"):
        ops.rotate(img, tmp_path / "o.pdf", 90)
