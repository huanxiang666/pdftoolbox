"""Shared helpers: errors, page-range parsing, positions, colors, atomic save.

This module deliberately does NOT import pymupdf at module level: the GUI and
CLI import common at startup, and a missing/broken pymupdf must degrade to a
readable error per operation, never to a crash at launch.
"""

from __future__ import annotations

import os
from pathlib import Path
from typing import TYPE_CHECKING

if TYPE_CHECKING:  # pragma: no cover - typing only
    import pymupdf


class OpError(RuntimeError):
    """User-facing operation error (bad arguments, unsupported input, ...)."""


def ensure_pymupdf():
    """Import pymupdf or raise a readable OpError telling the user what to do."""
    try:
        import pymupdf

        return pymupdf
    except ImportError as exc:
        raise OpError(
            "PDF engine (pymupdf) is not installed or failed to load: "
            f"{exc}. Install it with: pip install pymupdf"
        ) from exc


def parse_pages(spec: str | None, total: int) -> list[int]:
    """Parse a 1-based page spec into sorted unique 0-based indices.

    Accepted: None/""/"all" (every page), single numbers ("3"), ranges
    ("2-7"), open-ended ranges ("5-"), and comma combinations ("1-3,5,8-").
    Raises OpError for malformed specs or out-of-range numbers.
    """
    if total <= 0:
        raise OpError("document has no pages")
    if spec is None or str(spec).strip().lower() in ("", "all"):
        return list(range(total))
    indices: set[int] = set()
    for part in str(spec).split(","):
        part = part.strip()
        if not part:
            continue
        if "-" in part:
            lo_s, _, hi_s = part.partition("-")
            lo_s, hi_s = lo_s.strip(), hi_s.strip()
            try:
                lo = int(lo_s) if lo_s else 1
                hi = int(hi_s) if hi_s else total
            except ValueError:
                raise OpError(f"invalid page range: {part!r}") from None
            if lo < 1 or hi > total or lo > hi:
                raise OpError(
                    f"page range {part!r} out of bounds (document has {total} pages)"
                )
            indices.update(range(lo - 1, hi))
        else:
            try:
                num = int(part)
            except ValueError:
                raise OpError(f"invalid page number: {part!r}") from None
            if num < 1 or num > total:
                raise OpError(f"page {num} out of bounds (document has {total} pages)")
            indices.add(num - 1)
    if not indices:
        raise OpError(f"page selection {spec!r} is empty")
    return sorted(indices)


def format_pages(indices: list[int], total: int) -> str:
    """Human-readable 1-based summary of a page index list."""
    if len(indices) == total:
        return "all"
    nums = [i + 1 for i in indices]
    if len(nums) <= 8:
        return ",".join(str(n) for n in nums)
    return f"{nums[0]}-{nums[-1]} ({len(nums)} pages)"


#: (fx, fy) anchor fractions for the 9 placement positions.
POSITIONS: dict[str, tuple[float, float]] = {
    "top-left": (0.0, 0.0),
    "top-center": (0.5, 0.0),
    "top-right": (1.0, 0.0),
    "middle-left": (0.0, 0.5),
    "center": (0.5, 0.5),
    "middle-right": (1.0, 0.5),
    "bottom-left": (0.0, 1.0),
    "bottom-center": (0.5, 1.0),
    "bottom-right": (1.0, 1.0),
}

_NAMED_COLORS = {
    "black": (0.0, 0.0, 0.0),
    "white": (1.0, 1.0, 1.0),
    "red": (0.85, 0.1, 0.1),
    "green": (0.1, 0.55, 0.15),
    "blue": (0.1, 0.2, 0.8),
    "gray": (0.55, 0.55, 0.55),
    "grey": (0.55, 0.55, 0.55),
    "yellow": (0.95, 0.8, 0.05),
    "orange": (0.95, 0.55, 0.05),
    "purple": (0.5, 0.2, 0.7),
    "cyan": (0.1, 0.7, 0.8),
    "magenta": (0.8, 0.15, 0.7),
}


def parse_color(value: str | None, default: tuple[float, float, float]) -> tuple[float, float, float]:
    """Parse 'red' / '#rrggbb' / '#rgb' / '0.5,0.2,0.9' into an RGB 0..1 tuple."""
    if value is None or str(value).strip() == "":
        return default
    s = str(value).strip().lower()
    if s in _NAMED_COLORS:
        return _NAMED_COLORS[s]
    if s.startswith("#"):
        h = s[1:]
        try:
            if len(h) == 3:
                return tuple(int(c * 2, 16) / 255 for c in h)  # type: ignore[return-value]
            if len(h) == 6:
                return (int(h[0:2], 16) / 255, int(h[2:4], 16) / 255, int(h[4:6], 16) / 255)
        except ValueError:
            pass
        raise OpError(f"invalid hex color: {value!r} (expected #rgb or #rrggbb)")
    parts = [p.strip() for p in s.split(",")]
    if len(parts) == 3:
        try:
            rgb = tuple(float(p) for p in parts)
            if all(0.0 <= c <= 1.0 for c in rgb):
                return rgb  # type: ignore[return-value]
        except ValueError:
            pass
    raise OpError(f"invalid color: {value!r} (use a name, #rrggbb, or 'r,g,b' with 0..1 values)")


def resolve_position(name: str, default: str = "center") -> tuple[float, float]:
    key = (name or default).strip().lower()
    if key not in POSITIONS:
        raise OpError(f"unknown position {name!r} (choose from: {', '.join(POSITIONS)})")
    return POSITIONS[key]


def save_pdf(doc: "pymupdf.Document", path: Path | str, *, garbage: int = 3) -> Path:
    """Atomically save a document to path (tmp file + os.replace)."""
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(f"{path.name}.part{os.getpid()}")
    try:
        doc.save(str(tmp), garbage=garbage, deflate=True)
        doc.close()
        os.replace(tmp, path)
    except Exception:
        if not doc.is_closed:
            doc.close()
        try:
            if tmp.exists():
                tmp.unlink()
        except OSError:
            pass
        raise
    return path
