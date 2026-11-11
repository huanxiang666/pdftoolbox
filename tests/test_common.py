"""Unit tests for common helpers: page specs, colors, positions, atomic save."""

from __future__ import annotations

import pytest

from pdftoolbox.common import (
    OpError,
    format_pages,
    parse_color,
    parse_pages,
    resolve_position,
    save_pdf,
)


class TestParsePages:
    def test_all_forms(self):
        assert parse_pages(None, 10) == list(range(10))
        assert parse_pages("", 10) == list(range(10))
        assert parse_pages("all", 10) == list(range(10))
        assert parse_pages("3", 10) == [2]
        assert parse_pages("2-4", 10) == [1, 2, 3]
        assert parse_pages("5-", 7) == [4, 5, 6]
        assert parse_pages("-3", 10) == [0, 1, 2]
        assert parse_pages("1-3,5,8-", 10) == [0, 1, 2, 4, 7, 8, 9]

    def test_dedup_and_sort(self):
        assert parse_pages("3,1,1-2", 10) == [0, 1, 2]

    def test_errors(self):
        with pytest.raises(OpError, match="out of bounds"):
            parse_pages("11", 10)
        with pytest.raises(OpError, match="out of bounds"):
            parse_pages("5-3", 10)
        with pytest.raises(OpError, match="invalid"):
            parse_pages("abc", 10)
        with pytest.raises(OpError, match="empty"):
            parse_pages(",,", 10)
        with pytest.raises(OpError, match="no pages"):
            parse_pages("1", 0)


class TestColors:
    def test_named(self):
        assert parse_color("red", (0, 0, 0)) == pytest.approx((0.85, 0.1, 0.1))
        assert parse_color("BLACK", (0, 0, 0)) == (0.0, 0.0, 0.0)

    def test_hex(self):
        assert parse_color("#ff0000", (0, 0, 0)) == pytest.approx((1.0, 0.0, 0.0))
        assert parse_color("#0f0", (0, 0, 0)) == pytest.approx((0.0, 1.0, 0.0))

    def test_rgb_triple(self):
        assert parse_color("0.5, 0.25, 1", (0, 0, 0)) == pytest.approx((0.5, 0.25, 1.0))

    def test_default_and_invalid(self):
        assert parse_color(None, (0.1, 0.2, 0.3)) == (0.1, 0.2, 0.3)
        assert parse_color("", (0.1, 0.2, 0.3)) == (0.1, 0.2, 0.3)
        with pytest.raises(OpError):
            parse_color("notacolor", (0, 0, 0))
        with pytest.raises(OpError):
            parse_color("#12345", (0, 0, 0))


def test_positions():
    assert resolve_position("bottom-center") == (0.5, 1.0)
    assert resolve_position("CENTER") == (0.5, 0.5)
    with pytest.raises(OpError, match="unknown position"):
        resolve_position("middle")


def test_format_pages():
    assert format_pages([0, 1, 2], 3) == "all"
    assert format_pages([0, 4], 9) == "1,5"
    assert format_pages(list(range(20)), 30).endswith(")")


def test_save_pdf_atomic_and_inplace_safe(tmp_path):
    import pymupdf

    src = tmp_path / "in.pdf"
    doc = pymupdf.open()
    doc.new_page()
    doc.save(str(src))
    doc.close()

    # in-place: load into memory, modify, save over the same path
    doc = pymupdf.open(stream=src.read_bytes(), filetype="pdf")
    doc.new_page()
    save_pdf(doc, src)
    check = pymupdf.open(str(src))
    assert check.page_count == 2
    check.close()
    # no leftover .part files
    assert not list(tmp_path.glob("*.part*"))
