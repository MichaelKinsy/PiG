"""pi-coding-agent's ``truncateToVisualLines`` (``modes/interactive/components/visual-truncate.ts``)."""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class VisualTruncateResult:
    """pi-coding-agent's ``VisualTruncateResult``: the visual lines kept and how many were dropped."""

    visual_lines: list[str]
    skipped_count: int


# ECMAScript white space and line terminators, which ``String.prototype.trim`` removes.
_JS_SPACE = "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"


def _render_plain_text(text: str, padding_x: int, width: int) -> list[str]:
    """pi-tui's ``Text(text, paddingX, 0).render(width)``: tabs as three spaces, the text wrapped in the width less the padding on both sides (the padding
    shrinks to fit a narrow width), every line padded to the width."""
    from .tui import visible_width, wrap_text_with_ansi

    if text.strip(_JS_SPACE) == "":
        return []
    normalized = text.replace("\t", "   ")
    padding_x = min(padding_x, max(0, (width - 1) // 2))
    content_width = max(1, width - padding_x * 2)
    margin = " " * max(0, padding_x)
    lines = []
    for line in wrap_text_with_ansi(normalized, content_width):
        with_margins = margin + line + margin
        lines.append(with_margins + " " * max(0, width - visible_width(with_margins)))
    return lines or [""]


def truncate_to_visual_lines(text: str, max_visual_lines: int, width: int, padding_x: int = 0, keep: str = "end") -> VisualTruncateResult:
    """pi-coding-agent's ``truncateToVisualLines``: ``text`` wrapped at ``width`` as a ``Text`` with horizontal padding ``padding_x`` renders it (Pi's default is 0,
    right for a result that is placed in a ``Box``; use 1 for a plain ``Container``), cut to at most ``max_visual_lines`` visual lines, from the end unless ``keep``
    is ``"start"``."""
    if not text:
        return VisualTruncateResult([], 0)
    all_lines = _render_plain_text(text, padding_x, width)
    if len(all_lines) <= max_visual_lines:
        return VisualTruncateResult(all_lines, 0)
    kept = _slice(all_lines, 0, max_visual_lines) if keep == "start" else _slice(all_lines, -max_visual_lines, None)
    return VisualTruncateResult(kept, len(all_lines) - max_visual_lines)


def _slice(lines: list[str], start: int, end: int | None) -> list[str]:
    """``Array.prototype.slice(start, end)``: a negative index counts from the end."""
    n = len(lines)

    def resolve(index: int) -> int:
        return max(0, n + index) if index < 0 else min(index, n)

    start_index = resolve(start)
    end_index = n if end is None else resolve(end)
    return [] if start_index >= end_index else lines[start_index:end_index]
