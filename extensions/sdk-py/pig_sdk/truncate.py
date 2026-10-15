"""Output truncation helpers for tool results, mirroring Pi's exports of core/tools/truncate.ts.

Truncation uses two independent limits, a line count and a UTF-8 byte count; the one hit first wins.
"""
from dataclasses import dataclass
from typing import Optional

DEFAULT_MAX_LINES = 2000
DEFAULT_MAX_BYTES = 50 * 1024
GREP_MAX_LINE_LENGTH = 500


@dataclass
class TruncationResult:
    """Pi's TruncationResult, with Python names."""

    content: str
    truncated: bool
    truncated_by: Optional[str]  # "lines", "bytes" or None (Pi's null)
    total_lines: int
    total_bytes: int
    output_lines: int
    output_bytes: int
    last_line_partial: bool
    first_line_exceeds_limit: bool
    max_lines: int
    max_bytes: int


def _utf8(text: str) -> bytes:
    """Encode as Buffer.from(text, "utf-8") does: a lone surrogate becomes U+FFFD and a surrogate pair held as two code points is one character."""
    try:
        return text.encode("utf-8")
    except UnicodeEncodeError:
        return text.encode("utf-16-le", "surrogatepass").decode("utf-16-le", "replace").encode("utf-8")


def _byte_length(text: str) -> int:
    return len(_utf8(text))


def _split_lines_for_counting(content: str) -> list:
    if content == "":
        return []
    lines = content.split("\n")
    if content.endswith("\n"):
        lines.pop()
    return lines


def format_size(size: int) -> str:
    """Pi's formatSize: "512B", "1.5KB" or "2.0MB", rounding the one decimal half up like JavaScript's toFixed."""
    if size < 1024:
        return "%dB" % size
    if size < 1024 * 1024:
        return _tenths(size, 1024) + "KB"
    return _tenths(size, 1024 * 1024) + "MB"


def _tenths(size: int, unit: int) -> str:
    # unit is a power of two, so the quotient is exact in a double and toFixed(1) rounds an exact tie up.
    scaled = (size * 10 + unit // 2) // unit
    return "%d.%d" % (scaled // 10, scaled % 10)


def _unchanged(content: str, lines: list, max_lines: int, max_bytes: int, total_bytes: int) -> TruncationResult:
    return TruncationResult(content, False, None, len(lines), total_bytes, len(lines), total_bytes, False, False, max_lines, max_bytes)


def truncate_head(content: str, max_lines: Optional[int] = None, max_bytes: Optional[int] = None) -> TruncationResult:
    """Keep the first lines and bytes of ``content`` (file reads). It never returns a partial line: when the first line alone exceeds
    the byte limit the content is empty and ``first_line_exceeds_limit`` is set. An omitted limit takes its default; 0 is used as given."""
    max_lines = DEFAULT_MAX_LINES if max_lines is None else max_lines
    max_bytes = DEFAULT_MAX_BYTES if max_bytes is None else max_bytes
    total_bytes = _byte_length(content)
    lines = _split_lines_for_counting(content)
    total_lines = len(lines)
    if total_lines <= max_lines and total_bytes <= max_bytes:
        return _unchanged(content, lines, max_lines, max_bytes, total_bytes)
    if _byte_length(lines[0]) > max_bytes:
        return TruncationResult("", True, "bytes", total_lines, total_bytes, 0, 0, False, True, max_lines, max_bytes)
    kept: list = []
    output_bytes = 0
    truncated_by = "lines"
    for i in range(min(len(lines), max(max_lines, 0))):
        line_bytes = _byte_length(lines[i]) + (1 if i > 0 else 0)
        if output_bytes + line_bytes > max_bytes:
            truncated_by = "bytes"
            break
        kept.append(lines[i])
        output_bytes += line_bytes
    if len(kept) >= max_lines and output_bytes <= max_bytes:
        truncated_by = "lines"
    output = "\n".join(kept)
    return TruncationResult(output, True, truncated_by, total_lines, total_bytes, len(kept), _byte_length(output), False, False, max_lines, max_bytes)


def truncate_tail(content: str, max_lines: Optional[int] = None, max_bytes: Optional[int] = None) -> TruncationResult:
    """Keep the last lines and bytes of ``content`` (command output, where errors and final results come last). When the last line alone
    exceeds the byte limit it keeps the end of that line and sets ``last_line_partial``."""
    max_lines = DEFAULT_MAX_LINES if max_lines is None else max_lines
    max_bytes = DEFAULT_MAX_BYTES if max_bytes is None else max_bytes
    total_bytes = _byte_length(content)
    lines = _split_lines_for_counting(content)
    total_lines = len(lines)
    if total_lines <= max_lines and total_bytes <= max_bytes:
        return _unchanged(content, lines, max_lines, max_bytes, total_bytes)
    kept: list = []
    output_bytes = 0
    truncated_by = "lines"
    last_line_partial = False
    i = len(lines) - 1
    while i >= 0 and len(kept) < max_lines:
        line_bytes = _byte_length(lines[i]) + (1 if kept else 0)
        if output_bytes + line_bytes > max_bytes:
            truncated_by = "bytes"
            if not kept:
                partial = _truncate_to_bytes_from_end(lines[i], max_bytes)
                kept.append(partial)
                output_bytes = _byte_length(partial)
                last_line_partial = True
            break
        kept.append(lines[i])
        output_bytes += line_bytes
        i -= 1
    if len(kept) >= max_lines and output_bytes <= max_bytes:
        truncated_by = "lines"
    kept.reverse()
    output = "\n".join(kept)
    return TruncationResult(output, True, truncated_by, total_lines, total_bytes, len(kept), _byte_length(output), last_line_partial, False, max_lines, max_bytes)


def _truncate_to_bytes_from_end(text: str, max_bytes: int) -> str:
    data = _utf8(text)
    if len(data) <= max_bytes:
        return text
    start = max(len(data) - max_bytes, 0)
    while start < len(data) and (data[start] & 0xC0) == 0x80:
        start += 1
    return data[start:].decode("utf-8", "replace")


def truncate_line(line: str, max_chars: int = GREP_MAX_LINE_LENGTH) -> tuple:
    """Cut a line longer than ``max_chars`` UTF-16 code units (a JavaScript string's length) to that many units plus ``... [truncated]``.
    Returns ``(text, was_truncated)``. A cut inside a surrogate pair keeps the lone surrogate, as JavaScript does, and a negative ``max_chars``
    counts back from the end of the line, as String.prototype.slice and Python slicing both do."""
    units = line.encode("utf-16-le", "surrogatepass")
    if len(units) // 2 <= max_chars:
        return line, False
    cut = units[: max_chars * 2].decode("utf-16-le", "surrogatepass")
    return cut + "... [truncated]", True
