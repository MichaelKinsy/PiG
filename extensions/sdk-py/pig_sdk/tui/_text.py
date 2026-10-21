"""pi-tui's utils.ts: terminal width, grapheme, ANSI, wrap, truncate and slice helpers.

A line-by-line port of the pinned Pi release's ``utils.ts``. The Unicode data comes from ``_tables`` (generated from the same sources Pi's
runtime reads: get-east-asian-width, ICU property escapes, ``Intl.Segmenter`` grapheme rules). The vectors in ``test/parity/sdk-bulk`` are
what Pi's own functions return, and ``tests/test_tui_vectors.py`` holds this module to them.
"""

from __future__ import annotations

import re
from bisect import bisect_left
from collections.abc import Iterator
from functools import lru_cache

from ._tables import PROP_RANGES, RGI_SEQUENCES

CURSOR_MARKER = "\x1b_pi:c\x07"

_W2 = 1 << 0
_ZW = 1 << 1
_NP = 1 << 2
_MARK = 1 << 3
_TSM = 1 << 4
_RGI1 = 1 << 5
_EXT_PICT = 1 << 6
_CJK = 1 << 7
_JS_SPACE = 1 << 14

_GB_OTHER, _GB_CR, _GB_LF, _GB_CONTROL, _GB_EXTEND, _GB_ZWJ, _GB_RI, _GB_PREPEND, _GB_SPACING_MARK = range(9)
_GB_L, _GB_V, _GB_T, _GB_LV, _GB_LVT = range(9, 14)
_INCB_CONSONANT, _INCB_EXTEND, _INCB_LINKER = 1, 2, 3

_HIS = tuple(r[1] for r in PROP_RANGES)


@lru_cache(maxsize=4096)
def _props(cp: int) -> int:
    i = bisect_left(_HIS, cp)
    if i >= len(PROP_RANGES) or PROP_RANGES[i][0] > cp:
        return 0
    return PROP_RANGES[i][2]


def _gcb(p: int) -> int:
    return (p >> 8) & 0xF


def _incb(p: int) -> int:
    return (p >> 12) & 0x3


def _east_asian_width(cp: int) -> int:
    return 2 if _props(cp) & _W2 else 1


def _u16len(s: str) -> int:
    return len(s) + sum(1 for c in s if ord(c) > 0xFFFF)


def _is_js_space(ch: str) -> bool:
    return bool(_props(ord(ch)) & _JS_SPACE)


def _trim_end(s: str) -> str:
    end = len(s)
    while end > 0 and _is_js_space(s[end - 1]):
        end -= 1
    return s[:end]


def _trim_start(s: str) -> str:
    start = 0
    while start < len(s) and _is_js_space(s[start]):
        start += 1
    return s[start:]


def _graphemes(text: str) -> Iterator[str]:
    """Extended grapheme clusters as ICU's ``Intl.Segmenter`` yields them (UAX #29 with GB9c)."""
    n = len(text)
    i = 0
    while i < n:
        start = i
        p = _props(ord(text[i]))
        prev = _gcb(p)
        incb_state = 1 if _incb(p) == _INCB_CONSONANT else 0
        emoji_state = 1 if p & _EXT_PICT else 0
        ri_count = 1 if prev == _GB_RI else 0
        i += 1
        while i < n:
            q = _props(ord(text[i]))
            cur = _gcb(q)
            if prev == _GB_CR and cur == _GB_LF:
                brk = False
            elif prev in (_GB_CONTROL, _GB_CR, _GB_LF):
                brk = True
            elif cur in (_GB_CONTROL, _GB_CR, _GB_LF):
                brk = True
            elif prev == _GB_L and cur in (_GB_L, _GB_V, _GB_LV, _GB_LVT):
                brk = False
            elif prev in (_GB_LV, _GB_V) and cur in (_GB_V, _GB_T):
                brk = False
            elif prev in (_GB_LVT, _GB_T) and cur == _GB_T:
                brk = False
            elif cur in (_GB_EXTEND, _GB_ZWJ):
                brk = False
            elif cur == _GB_SPACING_MARK:
                brk = False
            elif prev == _GB_PREPEND:
                brk = False
            elif incb_state == 2 and _incb(q) == _INCB_CONSONANT:
                brk = False
            elif emoji_state == 2 and q & _EXT_PICT:
                brk = False
            elif prev == _GB_RI and cur == _GB_RI and ri_count % 2 == 1:
                brk = False
            else:
                brk = True
            if brk:
                break
            qi = _incb(q)
            if qi == _INCB_CONSONANT:
                incb_state = 1
            elif qi == _INCB_LINKER:
                if incb_state >= 1:
                    incb_state = 2
            elif qi != _INCB_EXTEND:
                incb_state = 0
            if q & _EXT_PICT:
                emoji_state = 1
            elif cur == _GB_EXTEND and emoji_state == 1:
                pass
            elif cur == _GB_ZWJ and emoji_state == 1:
                emoji_state = 2
            else:
                emoji_state = 0
            ri_count = ri_count + 1 if cur == _GB_RI else 0
            prev = cur
            i += 1
        yield text[start:i]


def _could_be_emoji(segment: str) -> bool:
    cp = ord(segment[0])
    return (
        0x1F000 <= cp <= 0x1FBFF
        or 0x2300 <= cp <= 0x23FF
        or 0x2600 <= cp <= 0x27BF
        or 0x2B50 <= cp <= 0x2B55
        or "\ufe0f" in segment
        or _u16len(segment) > 2
    )


def _is_rgi_emoji(segment: str) -> bool:
    if len(segment) == 1:
        return bool(_props(ord(segment)) & _RGI1)
    return segment in RGI_SEQUENCES


def _grapheme_width(segment: str) -> int:
    if len(segment) == 1:
        code = ord(segment)
        if 0x20 <= code <= 0x7E:
            return 1
        if code == 0x09:
            return 3
    flags = [_props(ord(c)) for c in segment]
    if all(f & _TSM for f in flags):
        return len(segment)
    if all(f & _ZW for f in flags):
        return 0
    if _could_be_emoji(segment) and _is_rgi_emoji(segment):
        return 2
    start = 0
    while start < len(segment) and flags[start] & _NP:
        start += 1
    if start == len(segment):
        return 0
    cp = ord(segment[start])
    if 0x1F1E6 <= cp <= 0x1F1FF:
        return 2
    width = _east_asian_width(cp)
    follows_mark = False
    for k in range(start + 1, len(segment)):
        c = ord(segment[k])
        f = flags[k]
        if f & _TSM:
            width += 1
            follows_mark = False
        elif f & _MARK:
            follows_mark = True
        elif not f & _NP:
            if follows_mark or 0xFF00 <= c <= 0xFFEF:
                width += _east_asian_width(c)
            elif c in (0x0E33, 0x0EB3):
                width += 1
            follows_mark = False
    return width


_CSI_END = re.compile("[mGKHJ]")
_OSC_END = re.compile("\x07|\x1b\\\\")
_PRINTABLE_ASCII = re.compile("[\x20-\x7e]*")


def _is_printable_ascii(s: str) -> bool:
    return _PRINTABLE_ASCII.fullmatch(s) is not None


def _ansi_code_length(s: str, pos: int) -> int:
    """Length of the ANSI/OSC/APC escape sequence starting at ``pos``, or 0 when there is none."""
    if pos >= len(s) or s[pos] != "\x1b":
        return 0
    nxt = s[pos + 1] if pos + 1 < len(s) else ""
    if nxt == "[":
        m = _CSI_END.search(s, pos + 2)
        return m.end() - pos if m else 0
    if nxt in ("]", "_"):
        m = _OSC_END.search(s, pos + 2)
        return m.end() - pos if m else 0
    return 0


def _extract_ansi_code(s: str, pos: int) -> str | None:
    length = _ansi_code_length(s, pos)
    return s[pos : pos + length] if length > 0 else None


def _ascii_visible_width(s: str) -> int:
    width = 0
    i = 0
    n = len(s)
    while i < n:
        code = ord(s[i])
        if 0x20 <= code <= 0x7E:
            width += 1
            i += 1
        elif code == 0x09:
            width += 3
            i += 1
        elif code == 0x1B:
            length = _ansi_code_length(s, i)
            if length == 0:
                return -1
            i += length
        else:
            return -1
    return width


def visible_width(text: str) -> int:
    """The terminal columns of ``text``, ignoring ANSI/OSC/APC sequences (pi-tui's ``visibleWidth``)."""
    if not text:
        return 0
    ascii_width = _ascii_visible_width(text)
    if ascii_width != -1:
        return ascii_width
    clean = text.replace("\t", "   ") if "\t" in text else text
    esc = clean.find("\x1b")
    if esc != -1:
        parts: list[str] = []
        copy_from = 0
        while esc != -1:
            length = _ansi_code_length(clean, esc)
            if length > 0:
                parts.append(clean[copy_from:esc])
                esc += length
                copy_from = esc
            else:
                esc += 1
            esc = clean.find("\x1b", esc)
        clean = "".join(parts) + clean[copy_from:]
    return sum(_grapheme_width(seg) for seg in _graphemes(clean))


def strip_terminal_sequences(text: str) -> str:
    """``text`` without its ANSI, OSC and APC control sequences (pi-tui's ``stripTerminalSequences``)."""
    if "\x1b" not in text:
        return text
    out: list[str] = []
    i = 0
    n = len(text)
    while i < n:
        length = _ansi_code_length(text, i)
        if length:
            i += length
            continue
        out.append(text[i])
        i += 1
    return "".join(out)


_OSC8_LINK = re.compile("\x1b\\]8;[^;]*;([^\x07\x1b]*)(?:\x07|\x1b\\\\)\\Z")


def _text_end(line: str, i: int) -> int:
    end = i
    n = len(line)
    while end < n and _ansi_code_length(line, end) == 0:
        end += 1
    return end


def get_osc8_link_at_column(line: str, column: int) -> str | None:
    """The OSC 8 hyperlink target covering the visible ``column`` of ``line`` (pi-tui's ``getOsc8LinkAtColumn``)."""
    active: str | None = None
    current = 0
    i = 0
    n = len(line)
    while i < n:
        length = _ansi_code_length(line, i)
        if length:
            m = _OSC8_LINK.match(line[i : i + length])
            if m:
                active = m.group(1) or None
            i += length
            continue
        end = _text_end(line, i)
        for seg in _graphemes(line[i:end]):
            width = 3 if seg == "\t" else _grapheme_width(seg)
            if current <= column < current + width:
                return active
            current += width
        i = end
    return None


_NOT_OSC8 = object()


def _parse_osc8(code: str):
    """``_NOT_OSC8`` (JS undefined), ``None`` (a close) or ``(params, url, terminator)``."""
    if not code.startswith("\x1b]8;"):
        return _NOT_OSC8
    terminator = "\x07" if code.endswith("\x07") else "\x1b\\"
    body = code[4 : -1 if terminator == "\x07" else -2]
    sep = body.find(";")
    if sep == -1:
        return _NOT_OSC8
    url = body[sep + 1 :]
    if not url:
        return None
    return (body[:sep], url, terminator)


def _active_osc8_close(prefix: str) -> str:
    if "\x1b]8;" not in prefix:
        return ""
    active = None
    i = 0
    n = len(prefix)
    while i < n:
        length = _ansi_code_length(prefix, i)
        if length:
            link = _parse_osc8(prefix[i : i + length])
            if link is not _NOT_OSC8:
                active = link
            i += length
        else:
            i += 1
    return "\x1b]8;;" + active[2] if active else ""


_SGR = re.compile("\x1b\\[([0-9;]*)m")


def _js_int(text: str) -> int | None:
    """``Number.parseInt(text, 10)`` for the digit strings an SGR parameter holds; ``None`` is NaN."""
    return int(text) if text.isascii() and text.isdigit() else None


class _AnsiCodeTracker:
    """Track the active SGR attributes and OSC 8 hyperlink to preserve styling across line breaks."""

    def __init__(self) -> None:
        self.clear()

    def _reset(self) -> None:
        self.bold = self.dim = self.italic = self.underline = False
        self.blink = self.inverse = self.hidden = self.strikethrough = False
        self.fg: str | None = None
        self.bg: str | None = None

    def clear(self) -> None:
        self._reset()
        self.hyperlink = None

    def process(self, code: str) -> None:
        link = _parse_osc8(code)
        if link is not _NOT_OSC8:
            self.hyperlink = link
            return
        if not code.endswith("m"):
            return
        m = _SGR.search(code)
        if not m:
            return
        params = m.group(1)
        if params in ("", "0"):
            self._reset()
            return
        parts = params.split(";")
        i = 0
        while i < len(parts):
            c = _js_int(parts[i])
            if c in (38, 48):
                if i + 1 < len(parts) and parts[i + 1] == "5" and i + 2 < len(parts):
                    color = f"{parts[i]};{parts[i + 1]};{parts[i + 2]}"
                    if c == 38:
                        self.fg = color
                    else:
                        self.bg = color
                    i += 3
                    continue
                if i + 1 < len(parts) and parts[i + 1] == "2" and i + 4 < len(parts):
                    color = ";".join(parts[i : i + 5])
                    if c == 38:
                        self.fg = color
                    else:
                        self.bg = color
                    i += 5
                    continue
            if c == 0:
                self._reset()
            elif c == 1:
                self.bold = True
            elif c == 2:
                self.dim = True
            elif c == 3:
                self.italic = True
            elif c == 4:
                self.underline = True
            elif c == 5:
                self.blink = True
            elif c == 7:
                self.inverse = True
            elif c == 8:
                self.hidden = True
            elif c == 9:
                self.strikethrough = True
            elif c == 21:
                self.bold = False
            elif c == 22:
                self.bold = self.dim = False
            elif c == 23:
                self.italic = False
            elif c == 24:
                self.underline = False
            elif c == 25:
                self.blink = False
            elif c == 27:
                self.inverse = False
            elif c == 28:
                self.hidden = False
            elif c == 29:
                self.strikethrough = False
            elif c == 39:
                self.fg = None
            elif c == 49:
                self.bg = None
            elif c is not None:
                if 30 <= c <= 37 or 90 <= c <= 97:
                    self.fg = str(c)
                elif 40 <= c <= 47 or 100 <= c <= 107:
                    self.bg = str(c)
            i += 1

    def active_codes(self) -> str:
        codes = []
        if self.bold:
            codes.append("1")
        if self.dim:
            codes.append("2")
        if self.italic:
            codes.append("3")
        if self.underline:
            codes.append("4")
        if self.blink:
            codes.append("5")
        if self.inverse:
            codes.append("7")
        if self.hidden:
            codes.append("8")
        if self.strikethrough:
            codes.append("9")
        if self.fg:
            codes.append(self.fg)
        if self.bg:
            codes.append(self.bg)
        result = f"\x1b[{';'.join(codes)}m" if codes else ""
        if self.hyperlink:
            params, url, terminator = self.hyperlink
            result += f"\x1b]8;{params};{url}{terminator}"
        return result

    def active_background_code(self) -> str:
        return f"\x1b[{self.bg}m" if self.bg else ""

    def line_end_reset(self) -> str:
        result = ""
        if self.underline:
            result += "\x1b[24m"
        if self.hyperlink:
            result += "\x1b]8;;" + self.hyperlink[2]
        return result


def _update_tracker(text: str, tracker: _AnsiCodeTracker) -> None:
    i = text.find("\x1b")
    while i != -1:
        length = _ansi_code_length(text, i)
        if length > 0:
            tracker.process(text[i : i + length])
            i += length
        else:
            i += 1
        i = text.find("\x1b", i)


def get_active_background_ansi(text: str) -> str:
    """The background SGR sequence active at the end of ``text`` (pi-tui's ``getActiveBackgroundAnsi``)."""
    tracker = _AnsiCodeTracker()
    _update_tracker(text, tracker)
    return tracker.active_background_code()


def _is_cjk_break(segment: str) -> bool:
    return any(_props(ord(c)) & _CJK for c in segment)


def _split_into_tokens(text: str) -> list[str]:
    tokens: list[str] = []
    current = ""
    pending = ""
    kind: str | None = None
    i = 0
    n = len(text)
    while i < n:
        length = _ansi_code_length(text, i)
        if length:
            pending += text[i : i + length]
            i += length
            continue
        end = text.find("\x1b", i + 1)
        while end != -1 and _ansi_code_length(text, end) == 0:
            end = text.find("\x1b", end + 1)
        if end == -1:
            end = n
        chunk = text[i:end]
        ascii_chunk = _is_printable_ascii(chunk)
        for segment in chunk if ascii_chunk else _graphemes(chunk):
            is_space = segment == " "
            if not ascii_chunk and not is_space and _is_cjk_break(segment):
                if current:
                    tokens.append(current)
                    current = ""
                    kind = None
                tokens.append(pending + segment)
                pending = ""
                continue
            seg_kind = "space" if is_space else "word"
            if current and kind != seg_kind:
                tokens.append(current)
                current = ""
                kind = None
            if pending:
                current += pending
                pending = ""
            kind = seg_kind
            current += segment
        i = end
    if pending:
        if current:
            current += pending
        elif tokens:
            tokens[-1] += pending
        else:
            current = pending
    if current:
        tokens.append(current)
    return tokens


_LINE_BREAK = re.compile("\r\n|\r|\n")


def wrap_text_with_ansi(text: str, width: int) -> list[str]:
    """``text`` word-wrapped to ``width`` columns, carrying SGR styling and OSC 8 links across lines (pi-tui's ``wrapTextWithAnsi``)."""
    if not text:
        return [""]
    result: list[str] = []
    tracker = _AnsiCodeTracker()
    for input_line in _LINE_BREAK.split(text):
        prefix = tracker.active_codes() if result else ""
        result.extend(_wrap_single_line(prefix + input_line, width))
        _update_tracker(input_line, tracker)
    return result if result else [""]


def _wrap_single_line(line: str, width: int) -> list[str]:
    if not line:
        return [""]
    if visible_width(line) <= width:
        return [line]
    wrapped: list[str] = []
    tracker = _AnsiCodeTracker()
    current = ""
    current_len = 0
    for token in _split_into_tokens(line):
        token_len = visible_width(token)
        is_whitespace = _trim_start(_trim_end(token)) == ""
        if token_len > width and not is_whitespace:
            if current:
                current += tracker.line_end_reset()
                wrapped.append(current)
                current = ""
                current_len = 0
            broken = _break_long_word(token, width, tracker)
            wrapped.extend(broken[:-1])
            current = broken[-1]
            current_len = visible_width(current)
            continue
        if current_len + token_len > width and current_len > 0:
            to_wrap = _trim_end(current) + tracker.line_end_reset()
            wrapped.append(to_wrap)
            if is_whitespace:
                current = tracker.active_codes()
                current_len = 0
            else:
                current = tracker.active_codes() + token
                current_len = token_len
        else:
            current += token
            current_len += token_len
        _update_tracker(token, tracker)
    if current:
        wrapped.append(current)
    return [_trim_end(x) for x in wrapped] if wrapped else [""]


def _break_long_word(word: str, width: int, tracker: _AnsiCodeTracker) -> list[str]:
    lines: list[str] = []
    current = tracker.active_codes()
    current_width = 0
    segments: list[tuple[bool, str]] = []
    i = 0
    n = len(word)
    while i < n:
        length = _ansi_code_length(word, i)
        if length:
            segments.append((True, word[i : i + length]))
            i += length
        else:
            end = _text_end(word, i)
            segments.extend((False, g) for g in _graphemes(word[i:end]))
            i = end
    for is_ansi, value in segments:
        if is_ansi:
            current += value
            tracker.process(value)
            continue
        if not value:
            continue
        w = visible_width(value)
        if current_width + w > width:
            current += tracker.line_end_reset()
            lines.append(current)
            current = tracker.active_codes()
            current_width = 0
        current += value
        current_width += w
    if current:
        lines.append(current)
    return lines if lines else [""]


def _truncate_fragment(text: str, max_width: int) -> tuple[str, int]:
    if max_width <= 0 or not text:
        return "", 0
    if _is_printable_ascii(text):
        clipped = text[:max_width]
        return clipped, len(clipped)
    has_ansi = "\x1b" in text
    has_tabs = "\t" in text
    if not has_ansi and not has_tabs:
        result: list[str] = []
        width = 0
        for seg in _graphemes(text):
            w = _grapheme_width(seg)
            if width + w > max_width:
                break
            result.append(seg)
            width += w
        return "".join(result), width
    result = []
    width = 0
    i = 0
    n = len(text)
    pending = ""
    while i < n:
        length = _ansi_code_length(text, i)
        if length:
            pending += text[i : i + length]
            i += length
            continue
        if text[i] == "\t":
            if width + 3 > max_width:
                break
            if pending:
                result.append(pending)
                pending = ""
            result.append("\t")
            width += 3
            i += 1
            continue
        end = i
        while end < n and text[end] != "\t" and _ansi_code_length(text, end) == 0:
            end += 1
        for seg in _graphemes(text[i:end]):
            w = _grapheme_width(seg)
            if width + w > max_width:
                return "".join(result), width
            if pending:
                result.append(pending)
                pending = ""
            result.append(seg)
            width += w
        i = end
    return "".join(result), width


def _finalize_truncated(prefix: str, prefix_width: int, ellipsis: str, ellipsis_width: int, max_width: int, pad: bool) -> str:
    reset = "\x1b[0m"
    close = _active_osc8_close(prefix)
    visible = prefix_width + ellipsis_width
    result = f"{prefix}{close}{reset}{ellipsis}{reset}" if ellipsis else f"{prefix}{close}{reset}"
    return result + " " * max(0, max_width - visible) if pad else result


def truncate_to_width(text: str, max_width: int, ellipsis: str = "...", pad: bool = False) -> str:
    """``text`` cut to at most ``max_width`` columns with ``ellipsis`` appended when it was cut (pi-tui's ``truncateToWidth``)."""
    if max_width <= 0:
        return ""
    if not text:
        return " " * max_width if pad else ""
    ellipsis_width = visible_width(ellipsis)
    if ellipsis_width >= max_width:
        text_width = visible_width(text)
        if text_width <= max_width:
            return text + " " * (max_width - text_width) if pad else text
        clipped, clipped_width = _truncate_fragment(ellipsis, max_width)
        if clipped_width == 0:
            return " " * max_width if pad else ""
        return _finalize_truncated("", 0, clipped, clipped_width, max_width, pad)
    if _is_printable_ascii(text):
        if len(text) <= max_width:
            return text + " " * (max_width - len(text)) if pad else text
        target = max_width - ellipsis_width
        return _finalize_truncated(text[:target], target, ellipsis, ellipsis_width, max_width, pad)
    target = max_width - ellipsis_width
    result: list[str] = []
    pending = ""
    visible_so_far = 0
    kept = 0
    contiguous = True
    overflowed = False
    exhausted = False
    has_ansi = "\x1b" in text
    has_tabs = "\t" in text
    if not has_ansi and not has_tabs:
        for seg in _graphemes(text):
            w = _grapheme_width(seg)
            if contiguous and kept + w <= target:
                result.append(seg)
                kept += w
            else:
                contiguous = False
            visible_so_far += w
            if visible_so_far > max_width:
                overflowed = True
                break
        exhausted = not overflowed
    else:
        i = 0
        n = len(text)
        while i < n:
            length = _ansi_code_length(text, i)
            if length:
                pending += text[i : i + length]
                i += length
                continue
            if text[i] == "\t":
                if contiguous and kept + 3 <= target:
                    if pending:
                        result.append(pending)
                        pending = ""
                    result.append("\t")
                    kept += 3
                else:
                    contiguous = False
                    pending = ""
                visible_so_far += 3
                if visible_so_far > max_width:
                    overflowed = True
                    break
                i += 1
                continue
            end = i
            while end < n and text[end] != "\t" and _ansi_code_length(text, end) == 0:
                end += 1
            for seg in _graphemes(text[i:end]):
                w = _grapheme_width(seg)
                if contiguous and kept + w <= target:
                    if pending:
                        result.append(pending)
                        pending = ""
                    result.append(seg)
                    kept += w
                else:
                    contiguous = False
                    pending = ""
                visible_so_far += w
                if visible_so_far > max_width:
                    overflowed = True
                    break
            if overflowed:
                break
            i = end
        exhausted = i >= n
    if not overflowed and exhausted:
        return text + " " * max(0, max_width - visible_so_far) if pad else text
    return _finalize_truncated("".join(result), kept, ellipsis, ellipsis_width, max_width, pad)


def slice_with_width(line: str, start_col: int, length: int, strict: bool = False) -> tuple[str, int]:
    """``(text, width)`` of the visible columns ``[start_col, start_col + length)`` of ``line`` (pi-tui's ``sliceWithWidth``)."""
    if length <= 0:
        return "", 0
    end_col = start_col + length
    result: list[str] = []
    result_width = 0
    current = 0
    i = 0
    pending = ""
    n = len(line)
    while i < n:
        code_len = _ansi_code_length(line, i)
        if code_len:
            code = line[i : i + code_len]
            if start_col <= current < end_col:
                result.append(pending + code)
                pending = ""
            elif current < start_col:
                pending += code
            i += code_len
            continue
        text_end = _text_end(line, i)
        for seg in _graphemes(line[i:text_end]):
            w = _grapheme_width(seg)
            in_range = start_col <= current < end_col
            fits = not strict or current + w <= end_col
            if in_range and fits:
                if pending:
                    result.append(pending)
                    pending = ""
                result.append(seg)
                result_width += w
            current += w
            if current >= end_col:
                break
        i = text_end
        if current >= end_col:
            break
    return "".join(result), result_width


def slice_by_column(line: str, start_col: int, length: int, strict: bool = False) -> str:
    """The visible columns ``[start_col, start_col + length)`` of ``line`` with ANSI codes preserved (pi-tui's ``sliceByColumn``)."""
    return slice_with_width(line, start_col, length, strict)[0]


def extract_segments(line: str, before_end: int, after_start: int, after_len: int, strict_after: bool = False) -> tuple[str, int, str, int]:
    """``(before, before_width, after, after_width)`` in one pass (pi-tui's ``extractSegments``)."""
    before: list[str] = []
    before_width = 0
    after: list[str] = []
    after_width = 0
    current = 0
    i = 0
    pending_before = ""
    after_started = False
    after_end = after_start + after_len
    tracker = _AnsiCodeTracker()
    n = len(line)
    while i < n:
        code_len = _ansi_code_length(line, i)
        if code_len:
            code = line[i : i + code_len]
            tracker.process(code)
            if current < before_end:
                pending_before += code
            elif after_start <= current < after_end and (after_started or code.startswith("\x1b_")):
                after.append(code)
            i += code_len
            continue
        text_end = _text_end(line, i)
        done = False
        for seg in _graphemes(line[i:text_end]):
            w = _grapheme_width(seg)
            if current < before_end and current + w <= before_end:
                if pending_before:
                    before.append(pending_before)
                    pending_before = ""
                before.append(seg)
                before_width += w
            elif after_start <= current < after_end:
                if not strict_after or current + w <= after_end:
                    if not after_started:
                        after.append(tracker.active_codes())
                        after_started = True
                    after.append(seg)
                    after_width += w
            current += w
            if (current >= before_end) if after_len <= 0 else (current >= after_end):
                done = True
                break
        i = text_end
        if done or ((current >= before_end) if after_len <= 0 else (current >= after_end)):
            break
    return "".join(before), before_width, "".join(after), after_width
