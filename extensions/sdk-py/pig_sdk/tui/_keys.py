"""pi-tui's keys.ts: raw terminal input against key identifiers.

A port of the pinned Pi release's ``keys.ts`` (legacy sequences, Kitty CSI-u with alternate keys and event types, xterm modifyOtherKeys).
JavaScript number semantics are kept where they decide a result: ``parseInt`` of an over-long digit run is a float, bitwise operators
truncate to int32, and ``String.fromCharCode`` keeps the low 16 bits.
"""

from __future__ import annotations

import math
import os
import re

_kitty_protocol_active = False


def set_kitty_protocol_active(active: bool) -> None:
    """Set the global Kitty keyboard protocol state (pi-tui's ``setKittyProtocolActive``)."""
    global _kitty_protocol_active
    _kitty_protocol_active = bool(active)


def is_kitty_protocol_active() -> bool:
    """Whether the Kitty keyboard protocol is active (pi-tui's ``isKittyProtocolActive``)."""
    return _kitty_protocol_active


KeyId = str


class _KeyHelper:
    """pi-tui's ``Key`` object: named key identifiers and modifier builders."""

    escape = "escape"
    esc = "esc"
    enter = "enter"
    return_ = "return"
    tab = "tab"
    space = "space"
    backspace = "backspace"
    delete = "delete"
    insert = "insert"
    clear = "clear"
    home = "home"
    end = "end"
    page_up = "pageUp"
    page_down = "pageDown"
    up = "up"
    down = "down"
    left = "left"
    right = "right"
    f1, f2, f3, f4, f5, f6 = "f1", "f2", "f3", "f4", "f5", "f6"
    f7, f8, f9, f10, f11, f12 = "f7", "f8", "f9", "f10", "f11", "f12"
    backtick = "`"
    hyphen = "-"
    equals = "="
    leftbracket = "["
    rightbracket = "]"
    backslash = "\\"
    semicolon = ";"
    quote = "'"
    comma = ","
    period = "."
    slash = "/"
    exclamation = "!"
    at = "@"
    hash = "#"
    dollar = "$"
    percent = "%"
    caret = "^"
    ampersand = "&"
    asterisk = "*"
    leftparen = "("
    rightparen = ")"
    underscore = "_"
    plus = "+"
    pipe = "|"
    tilde = "~"
    leftbrace = "{"
    rightbrace = "}"
    colon = ":"
    lessthan = "<"
    greaterthan = ">"
    question = "?"

    @staticmethod
    def ctrl(key: str) -> str:
        return f"ctrl+{key}"

    @staticmethod
    def shift(key: str) -> str:
        return f"shift+{key}"

    @staticmethod
    def alt(key: str) -> str:
        return f"alt+{key}"

    @staticmethod
    def super(key: str) -> str:
        return f"super+{key}"

    @staticmethod
    def ctrl_shift(key: str) -> str:
        return f"ctrl+shift+{key}"

    @staticmethod
    def shift_ctrl(key: str) -> str:
        return f"shift+ctrl+{key}"

    @staticmethod
    def ctrl_alt(key: str) -> str:
        return f"ctrl+alt+{key}"

    @staticmethod
    def alt_ctrl(key: str) -> str:
        return f"alt+ctrl+{key}"

    @staticmethod
    def shift_alt(key: str) -> str:
        return f"shift+alt+{key}"

    @staticmethod
    def alt_shift(key: str) -> str:
        return f"alt+shift+{key}"

    @staticmethod
    def ctrl_super(key: str) -> str:
        return f"ctrl+super+{key}"

    @staticmethod
    def super_ctrl(key: str) -> str:
        return f"super+ctrl+{key}"

    @staticmethod
    def shift_super(key: str) -> str:
        return f"shift+super+{key}"

    @staticmethod
    def super_shift(key: str) -> str:
        return f"super+shift+{key}"

    @staticmethod
    def alt_super(key: str) -> str:
        return f"alt+super+{key}"

    @staticmethod
    def super_alt(key: str) -> str:
        return f"super+alt+{key}"

    @staticmethod
    def ctrl_shift_alt(key: str) -> str:
        return f"ctrl+shift+alt+{key}"

    @staticmethod
    def ctrl_shift_super(key: str) -> str:
        return f"ctrl+shift+super+{key}"


Key = _KeyHelper()

_SYMBOL_KEYS = frozenset("`-=[]\\;',./!@#$%^&*()_+|~{}:<>?")
_SHIFTED_SYMBOL_KEYS = frozenset("~!@#$%^&*()_+|{}:<>?")
_MODIFIER_NAMES = ("shift", "alt", "ctrl", "super")
_SHIFT, _ALT, _CTRL, _SUPER = 1, 2, 4, 8
_LOCK_MASK = 64 + 128

_ESCAPE, _TAB, _ENTER, _SPACE, _BACKSPACE, _KP_ENTER = 27, 9, 13, 32, 127, 57414
_UP, _DOWN, _RIGHT, _LEFT = -1, -2, -3, -4
_DELETE, _INSERT, _PAGE_UP, _PAGE_DOWN, _HOME, _END = -10, -11, -12, -13, -14, -15

_FUNCTIONAL_EQUIVALENTS = {
    57399: 48, 57400: 49, 57401: 50, 57402: 51, 57403: 52, 57404: 53, 57405: 54, 57406: 55, 57407: 56, 57408: 57,
    57409: 46, 57410: 47, 57411: 42, 57412: 45, 57413: 43, 57415: 61, 57416: 44,
    57417: _LEFT, 57418: _RIGHT, 57419: _UP, 57420: _DOWN, 57421: _PAGE_UP, 57422: _PAGE_DOWN,
    57423: _HOME, 57424: _END, 57425: _INSERT, 57426: _DELETE,
}  # fmt: skip

_LEGACY_KEY_SEQUENCES = {
    "up": ("\x1b[A", "\x1bOA"),
    "down": ("\x1b[B", "\x1bOB"),
    "right": ("\x1b[C", "\x1bOC"),
    "left": ("\x1b[D", "\x1bOD"),
    "home": ("\x1b[H", "\x1bOH", "\x1b[1~", "\x1b[7~"),
    "end": ("\x1b[F", "\x1bOF", "\x1b[4~", "\x1b[8~"),
    "insert": ("\x1b[2~",),
    "delete": ("\x1b[3~",),
    "pageUp": ("\x1b[5~", "\x1b[[5~"),
    "pageDown": ("\x1b[6~", "\x1b[[6~"),
    "clear": ("\x1b[E", "\x1bOE"),
    "f1": ("\x1bOP", "\x1b[11~", "\x1b[[A"),
    "f2": ("\x1bOQ", "\x1b[12~", "\x1b[[B"),
    "f3": ("\x1bOR", "\x1b[13~", "\x1b[[C"),
    "f4": ("\x1bOS", "\x1b[14~", "\x1b[[D"),
    "f5": ("\x1b[15~", "\x1b[[E"),
    "f6": ("\x1b[17~",),
    "f7": ("\x1b[18~",),
    "f8": ("\x1b[19~",),
    "f9": ("\x1b[20~",),
    "f10": ("\x1b[21~",),
    "f11": ("\x1b[23~",),
    "f12": ("\x1b[24~",),
}
_LEGACY_SHIFT_SEQUENCES = {
    "up": ("\x1b[a",), "down": ("\x1b[b",), "right": ("\x1b[c",), "left": ("\x1b[d",), "clear": ("\x1b[e",),
    "insert": ("\x1b[2$",), "delete": ("\x1b[3$",), "pageUp": ("\x1b[5$",), "pageDown": ("\x1b[6$",),
    "home": ("\x1b[7$",), "end": ("\x1b[8$",),
}  # fmt: skip
_LEGACY_CTRL_SEQUENCES = {
    "up": ("\x1bOa",), "down": ("\x1bOb",), "right": ("\x1bOc",), "left": ("\x1bOd",), "clear": ("\x1bOe",),
    "insert": ("\x1b[2^",), "delete": ("\x1b[3^",), "pageUp": ("\x1b[5^",), "pageDown": ("\x1b[6^",),
    "home": ("\x1b[7^",), "end": ("\x1b[8^",),
}  # fmt: skip
_LEGACY_SEQUENCE_KEY_IDS = {
    "\x1bOA": "up", "\x1bOB": "down", "\x1bOC": "right", "\x1bOD": "left", "\x1bOH": "home", "\x1bOF": "end",
    "\x1b[E": "clear", "\x1bOE": "clear", "\x1bOe": "ctrl+clear", "\x1b[e": "shift+clear",
    "\x1b[2~": "insert", "\x1b[2$": "shift+insert", "\x1b[2^": "ctrl+insert",
    "\x1b[3$": "shift+delete", "\x1b[3^": "ctrl+delete", "\x1b[[5~": "pageUp", "\x1b[[6~": "pageDown",
    "\x1b[a": "shift+up", "\x1b[b": "shift+down", "\x1b[c": "shift+right", "\x1b[d": "shift+left",
    "\x1bOa": "ctrl+up", "\x1bOb": "ctrl+down", "\x1bOc": "ctrl+right", "\x1bOd": "ctrl+left",
    "\x1b[5$": "shift+pageUp", "\x1b[6$": "shift+pageDown", "\x1b[7$": "shift+home", "\x1b[8$": "shift+end",
    "\x1b[5^": "ctrl+pageUp", "\x1b[6^": "ctrl+pageDown", "\x1b[7^": "ctrl+home", "\x1b[8^": "ctrl+end",
    "\x1bOP": "f1", "\x1bOQ": "f2", "\x1bOR": "f3", "\x1bOS": "f4",
    "\x1b[11~": "f1", "\x1b[12~": "f2", "\x1b[13~": "f3", "\x1b[14~": "f4",
    "\x1b[[A": "f1", "\x1b[[B": "f2", "\x1b[[C": "f3", "\x1b[[D": "f4", "\x1b[[E": "f5",
    "\x1b[15~": "f5", "\x1b[17~": "f6", "\x1b[18~": "f7", "\x1b[19~": "f8", "\x1b[20~": "f9",
    "\x1b[21~": "f10", "\x1b[23~": "f11", "\x1b[24~": "f12",
    "\x1bb": "alt+left", "\x1bf": "alt+right", "\x1bp": "alt+up", "\x1bn": "alt+down",
}  # fmt: skip


def _num(text: str) -> float:
    """``Number.parseInt(text, 10)`` for a run of ASCII digits: a float, infinite when it overflows."""
    try:
        return float(int(text))
    except OverflowError:
        return math.inf


def _i32(value: float) -> int:
    """JavaScript ``ToInt32``."""
    if not math.isfinite(value):
        return 0
    n = int(value) & 0xFFFFFFFF
    return n - 0x100000000 if n >= 0x80000000 else n


def _from_char_code(value: float) -> str:
    """``String.fromCharCode(value)`` for one value: its low 16 bits."""
    return chr(_i32(value) & 0xFFFF)


def _normalize_functional(codepoint: float) -> float:
    return _FUNCTIONAL_EQUIVALENTS.get(codepoint, codepoint) if math.isfinite(codepoint) else codepoint


def _normalize_shifted_letter(codepoint: float, modifier: float) -> float:
    if _i32(modifier) & ~_LOCK_MASK & _SHIFT and 65 <= codepoint <= 90:
        return codepoint + 32
    return codepoint


def _logical_shifted_symbol(codepoint, modifier, allowed):
    if codepoint is None or not _i32(modifier) & _SHIFT:
        return None
    normalized = _normalize_functional(codepoint)
    if _from_char_code(normalized) not in allowed:
        return None
    return normalized, _i32(modifier) & ~_SHIFT


_CSI_U = re.compile(r"\x1b\[([0-9]+)(?::([0-9]*))?(?::([0-9]+))?(?:;([0-9]+))?(?::([0-9]+))?u\Z")
_ARROW = re.compile(r"\x1b\[1;([0-9]+)(?::([0-9]+))?([ABCD])\Z")
_FUNC = re.compile(r"\x1b\[([0-9]+)(?:;([0-9]+))?(?::([0-9]+))?~\Z")
_HOME_END = re.compile(r"\x1b\[1;([0-9]+)(?::([0-9]+))?([HF])\Z")
_MODIFY_OTHER = re.compile(r"\x1b\[27;([0-9]+);([0-9]+)~\Z")
_FUNC_CODES = {2: _INSERT, 3: _DELETE, 5: _PAGE_UP, 6: _PAGE_DOWN, 7: _HOME, 8: _END}


def _parse_kitty(data: str):
    """``(codepoint, shifted_key, base_layout_key, modifier)`` or ``None``."""
    m = _CSI_U.match(data)
    if m:
        shifted = _num(m.group(2)) if m.group(2) else None
        base = _num(m.group(3)) if m.group(3) else None
        mod_value = _num(m.group(4)) if m.group(4) else 1
        return _num(m.group(1)), shifted, base, mod_value - 1
    m = _ARROW.match(data)
    if m:
        return {"A": _UP, "B": _DOWN, "C": _RIGHT, "D": _LEFT}[m.group(3)], None, None, _num(m.group(1)) - 1
    m = _FUNC.match(data)
    if m:
        codepoint = _FUNC_CODES.get(_num(m.group(1)))
        if codepoint is not None:
            mod_value = _num(m.group(2)) if m.group(2) else 1
            return codepoint, None, None, mod_value - 1
    m = _HOME_END.match(data)
    if m:
        return (_HOME if m.group(3) == "H" else _END), None, None, _num(m.group(1)) - 1
    return None


def _matches_kitty(data: str, expected_codepoint: int, expected_modifier: int) -> bool:
    parsed = _parse_kitty(data)
    if parsed is None:
        return False
    codepoint, shifted, base, modifier = parsed
    actual_mod = _i32(modifier) & ~_LOCK_MASK
    expected_mod = expected_modifier & ~_LOCK_MASK
    normalized = _normalize_shifted_letter(_normalize_functional(codepoint), modifier)
    normalized_expected = _normalize_shifted_letter(_normalize_functional(expected_codepoint), expected_modifier)
    if actual_mod == expected_mod and normalized == normalized_expected:
        return True
    logical = _logical_shifted_symbol(shifted, actual_mod, _SYMBOL_KEYS)
    if logical and (logical[1] & ~_LOCK_MASK) == expected_mod and logical[0] == _normalize_functional(expected_codepoint):
        return True
    if actual_mod != expected_mod:
        return False
    if base is not None and base == expected_codepoint:
        is_latin_letter = 97 <= normalized <= 122
        is_known_symbol = _from_char_code(normalized) in _SYMBOL_KEYS
        if not is_latin_letter and not is_known_symbol:
            return True
    return False


def _parse_modify_other(data: str):
    m = _MODIFY_OTHER.match(data)
    if not m:
        return None
    return _num(m.group(2)), _num(m.group(1)) - 1


def _matches_modify_other(data: str, expected_keycode: int, expected_modifier: int) -> bool:
    parsed = _parse_modify_other(data)
    return parsed is not None and parsed[0] == expected_keycode and parsed[1] == expected_modifier


def _matches_printable_modify_other(data: str, expected_keycode: int, expected_modifier: int) -> bool:
    parsed = _parse_modify_other(data)
    if parsed is None:
        return False
    codepoint, modifier = parsed
    if modifier == expected_modifier and _normalize_shifted_letter(codepoint, modifier) == _normalize_shifted_letter(expected_keycode, expected_modifier):
        return True
    logical = _logical_shifted_symbol(codepoint, modifier, _SHIFTED_SYMBOL_KEYS)
    return logical is not None and logical[1] == expected_modifier and logical[0] == expected_keycode


def _is_windows_terminal_session() -> bool:
    env = os.environ
    return bool(env.get("WT_SESSION")) and not env.get("SSH_CONNECTION") and not env.get("SSH_CLIENT") and not env.get("SSH_TTY")


def _matches_raw_backspace(data: str, expected_modifier: int) -> bool:
    if data == "\x7f":
        return expected_modifier == 0
    if data != "\x08":
        return False
    return expected_modifier == _CTRL if _is_windows_terminal_session() else expected_modifier == 0


def _raw_ctrl_char(key: str) -> str | None:
    char = key.lower()
    code = ord(char[0])
    if 97 <= code <= 122 or char in ("[", "\\", "]", "_"):
        return chr(code & 0x1F)
    if char == "-":
        return chr(31)
    return None


def _legacy(data: str, sequences) -> bool:
    return data in sequences


def _legacy_modifier(data: str, key: str, modifier: int) -> bool:
    if modifier == _SHIFT:
        return data in _LEGACY_SHIFT_SEQUENCES[key]
    if modifier == _CTRL:
        return data in _LEGACY_CTRL_SEQUENCES[key]
    return False


def _parse_key_id(key_id: str):
    key = key_id.lower()
    modifiers: set[str] = set()
    while True:
        modifier = next((name for name in _MODIFIER_NAMES if key.startswith(name + "+")), None)
        if modifier is None:
            break
        if modifier in modifiers:
            return None
        modifiers.add(modifier)
        key = key[len(modifier) + 1 :]
    if not key or ("+" in key and key != "+"):
        return None
    return key, modifiers


_FUNCTIONAL_KEYS = {
    "insert": _INSERT, "delete": _DELETE, "home": _HOME, "end": _END, "pageup": _PAGE_UP, "pagedown": _PAGE_DOWN,
}  # fmt: skip
_FUNCTIONAL_NAMES = {"insert": "insert", "delete": "delete", "home": "home", "end": "end", "pageup": "pageUp", "pagedown": "pageDown"}
_ARROWS = {"up": _UP, "down": _DOWN, "left": _LEFT, "right": _RIGHT}


def matches_key(data: str, key_id: str) -> bool:
    """Whether raw terminal ``data`` is the key ``key_id`` (pi-tui's ``matchesKey``)."""
    parsed = _parse_key_id(key_id)
    if parsed is None:
        return False
    key, names = parsed
    modifier = 0
    if "shift" in names:
        modifier |= _SHIFT
    if "alt" in names:
        modifier |= _ALT
    if "ctrl" in names:
        modifier |= _CTRL
    if "super" in names:
        modifier |= _SUPER
    kitty_active = _kitty_protocol_active

    if key in ("escape", "esc"):
        if modifier != 0:
            return False
        return data == "\x1b" or _matches_kitty(data, _ESCAPE, 0) or _matches_modify_other(data, _ESCAPE, 0)

    if key == "space":
        if not kitty_active:
            if modifier == _CTRL and data == "\x00":
                return True
            if modifier == _ALT and data == "\x1b ":
                return True
        if modifier == 0:
            return data == " " or _matches_kitty(data, _SPACE, 0) or _matches_modify_other(data, _SPACE, 0)
        return _matches_kitty(data, _SPACE, modifier) or _matches_modify_other(data, _SPACE, modifier)

    if key == "tab":
        if modifier == _SHIFT:
            return data == "\x1b[Z" or _matches_kitty(data, _TAB, _SHIFT) or _matches_modify_other(data, _TAB, _SHIFT)
        if modifier == 0:
            return data == "\t" or _matches_kitty(data, _TAB, 0)
        return _matches_kitty(data, _TAB, modifier) or _matches_modify_other(data, _TAB, modifier)

    if key in ("enter", "return"):
        if modifier == _SHIFT:
            if _matches_kitty(data, _ENTER, _SHIFT) or _matches_kitty(data, _KP_ENTER, _SHIFT):
                return True
            if _matches_modify_other(data, _ENTER, _SHIFT):
                return True
            if kitty_active:
                return data == "\x1b\r" or data == "\n"
            return False
        if modifier == _ALT:
            if _matches_kitty(data, _ENTER, _ALT) or _matches_kitty(data, _KP_ENTER, _ALT):
                return True
            if _matches_modify_other(data, _ENTER, _ALT):
                return True
            if not kitty_active:
                return data == "\x1b\r"
            return False
        if modifier == 0:
            return (
                data == "\r"
                or (not kitty_active and data == "\n")
                or data == "\x1bOM"
                or _matches_kitty(data, _ENTER, 0)
                or _matches_kitty(data, _KP_ENTER, 0)
            )
        return _matches_kitty(data, _ENTER, modifier) or _matches_kitty(data, _KP_ENTER, modifier) or _matches_modify_other(data, _ENTER, modifier)

    if key == "backspace":
        if modifier == _ALT:
            if data == "\x1b\x7f" or data == "\x1b\b":
                return True
            return _matches_kitty(data, _BACKSPACE, _ALT) or _matches_modify_other(data, _BACKSPACE, _ALT)
        if modifier == _CTRL:
            if _matches_raw_backspace(data, _CTRL):
                return True
            return _matches_kitty(data, _BACKSPACE, _CTRL) or _matches_modify_other(data, _BACKSPACE, _CTRL)
        if modifier == 0:
            return _matches_raw_backspace(data, 0) or _matches_kitty(data, _BACKSPACE, 0) or _matches_modify_other(data, _BACKSPACE, 0)
        return _matches_kitty(data, _BACKSPACE, modifier) or _matches_modify_other(data, _BACKSPACE, modifier)

    if key in _FUNCTIONAL_KEYS:
        name = _FUNCTIONAL_NAMES[key]
        code = _FUNCTIONAL_KEYS[key]
        if modifier == 0:
            return _legacy(data, _LEGACY_KEY_SEQUENCES[name]) or _matches_kitty(data, code, 0)
        if _legacy_modifier(data, name, modifier):
            return True
        return _matches_kitty(data, code, modifier)

    if key == "clear":
        if modifier == 0:
            return _legacy(data, _LEGACY_KEY_SEQUENCES["clear"])
        return _legacy_modifier(data, "clear", modifier)

    if key in _ARROWS:
        code = _ARROWS[key]
        if key == "up" and modifier == _ALT:
            return data == "\x1bp" or _matches_kitty(data, code, _ALT)
        if key == "down" and modifier == _ALT:
            return data == "\x1bn" or _matches_kitty(data, code, _ALT)
        if key == "left" and modifier == _ALT:
            return data == "\x1b[1;3D" or (not kitty_active and data == "\x1bB") or data == "\x1bb" or _matches_kitty(data, code, _ALT)
        if key == "right" and modifier == _ALT:
            return data == "\x1b[1;3C" or (not kitty_active and data == "\x1bF") or data == "\x1bf" or _matches_kitty(data, code, _ALT)
        if key == "left" and modifier == _CTRL:
            return data == "\x1b[1;5D" or _legacy_modifier(data, "left", _CTRL) or _matches_kitty(data, code, _CTRL)
        if key == "right" and modifier == _CTRL:
            return data == "\x1b[1;5C" or _legacy_modifier(data, "right", _CTRL) or _matches_kitty(data, code, _CTRL)
        if modifier == 0:
            return _legacy(data, _LEGACY_KEY_SEQUENCES[key]) or _matches_kitty(data, code, 0)
        if _legacy_modifier(data, key, modifier):
            return True
        return _matches_kitty(data, code, modifier)

    if re.fullmatch(r"f(?:[1-9]|1[0-2])", key):
        if modifier != 0:
            return False
        return _legacy(data, _LEGACY_KEY_SEQUENCES[key])

    if len(key) == 1 and ("a" <= key <= "z" or "0" <= key <= "9" or key in _SYMBOL_KEYS):
        codepoint = ord(key)
        raw_ctrl = _raw_ctrl_char(key)
        is_letter = "a" <= key <= "z"
        is_digit = "0" <= key <= "9"
        if modifier == _CTRL + _ALT and not kitty_active and raw_ctrl:
            if data == "\x1b" + raw_ctrl:
                return True
        if modifier == _ALT and not kitty_active and (is_letter or is_digit or key in _SYMBOL_KEYS):
            if data == "\x1b" + key:
                return True
        if modifier == _CTRL:
            if raw_ctrl and data == raw_ctrl:
                return True
            return _matches_kitty(data, codepoint, _CTRL) or _matches_printable_modify_other(data, codepoint, _CTRL)
        if modifier == _SHIFT + _CTRL:
            return _matches_kitty(data, codepoint, _SHIFT + _CTRL) or _matches_printable_modify_other(data, codepoint, _SHIFT + _CTRL)
        if modifier == _SHIFT:
            if is_letter and data == key.upper():
                return True
            return _matches_kitty(data, codepoint, _SHIFT) or _matches_printable_modify_other(data, codepoint, _SHIFT)
        if modifier != 0:
            return _matches_kitty(data, codepoint, modifier) or _matches_printable_modify_other(data, codepoint, modifier)
        return data == key or _matches_kitty(data, codepoint, 0) or _matches_printable_modify_other(data, codepoint, 0)
    return False


def is_key_release(data: str) -> bool:
    """Whether ``data`` is a Kitty key-release event (pi-tui's ``isKeyRelease``)."""
    if "\x1b[200~" in data:
        return False
    return any(marker in data for marker in (":3u", ":3~", ":3A", ":3B", ":3C", ":3D", ":3H", ":3F"))


def is_key_repeat(data: str) -> bool:
    """Whether ``data`` is a Kitty key-repeat event (pi-tui's ``isKeyRepeat``)."""
    if "\x1b[200~" in data:
        return False
    return any(marker in data for marker in (":2u", ":2~", ":2A", ":2B", ":2C", ":2D", ":2H", ":2F"))


def _format_key_name(key_name: str, modifier: float) -> str | None:
    effective = _i32(modifier) & ~_LOCK_MASK
    if effective & ~(_SHIFT | _CTRL | _ALT | _SUPER):
        return None
    mods = []
    if effective & _SHIFT:
        mods.append("shift")
    if effective & _CTRL:
        mods.append("ctrl")
    if effective & _ALT:
        mods.append("alt")
    if effective & _SUPER:
        mods.append("super")
    return "+".join(mods) + "+" + key_name if mods else key_name


_NAMED = {
    _ESCAPE: "escape", _TAB: "tab", _ENTER: "enter", _KP_ENTER: "enter", _SPACE: "space", _BACKSPACE: "backspace",
    _DELETE: "delete", _INSERT: "insert", _HOME: "home", _END: "end", _PAGE_UP: "pageUp", _PAGE_DOWN: "pageDown",
    _UP: "up", _DOWN: "down", _LEFT: "left", _RIGHT: "right",
}  # fmt: skip


def _format_parsed_key(codepoint: float, modifier: float, base_layout_key: float | None = None) -> str | None:
    normalized = _normalize_functional(codepoint)
    identity = _normalize_shifted_letter(normalized, modifier)
    is_latin_letter = 97 <= identity <= 122
    is_digit = 48 <= identity <= 57
    is_known_symbol = _from_char_code(identity) in _SYMBOL_KEYS
    effective = identity if is_latin_letter or is_digit or is_known_symbol else (base_layout_key if base_layout_key is not None else identity)
    key_name = _NAMED.get(effective)
    if key_name is None:
        if 48 <= effective <= 57 or 97 <= effective <= 122 or _from_char_code(effective) in _SYMBOL_KEYS:
            key_name = _from_char_code(effective)
    if not key_name:
        return None
    return _format_key_name(key_name, modifier)


def parse_key(data: str) -> str | None:
    """The key identifier raw terminal ``data`` stands for, or ``None`` (pi-tui's ``parseKey``)."""
    kitty = _parse_kitty(data)
    if kitty:
        codepoint, shifted, base, modifier = kitty
        logical = _logical_shifted_symbol(shifted, modifier, _SYMBOL_KEYS)
        return _format_parsed_key(logical[0] if logical else codepoint, logical[1] if logical else modifier, base)
    other = _parse_modify_other(data)
    if other:
        logical = _logical_shifted_symbol(other[0], other[1], _SHIFTED_SYMBOL_KEYS)
        return _format_parsed_key(logical[0] if logical else other[0], logical[1] if logical else other[1])
    active = _kitty_protocol_active
    if active and (data == "\x1b\r" or data == "\n"):
        return "shift+enter"
    legacy = _LEGACY_SEQUENCE_KEY_IDS.get(data)
    if legacy:
        return legacy
    if data == "\x1b":
        return "escape"
    if data == "\x1c":
        return "ctrl+\\"
    if data == "\x1d":
        return "ctrl+]"
    if data == "\x1f":
        return "ctrl+-"
    if data == "\x1b\x1b":
        return "ctrl+alt+["
    if data == "\x1b\x1c":
        return "ctrl+alt+\\"
    if data == "\x1b\x1d":
        return "ctrl+alt+]"
    if data == "\x1b\x1f":
        return "ctrl+alt+-"
    if data == "\t":
        return "tab"
    if data == "\r" or (not active and data == "\n") or data == "\x1bOM":
        return "enter"
    if data == "\x00":
        return "ctrl+space"
    if data == " ":
        return "space"
    if data == "\x7f":
        return "backspace"
    if data == "\x08":
        return "ctrl+backspace" if _is_windows_terminal_session() else "backspace"
    if data == "\x1b[Z":
        return "shift+tab"
    if not active and data == "\x1b\r":
        return "alt+enter"
    if not active and data == "\x1b ":
        return "alt+space"
    if data == "\x1b\x7f" or data == "\x1b\b":
        return "alt+backspace"
    if not active and data == "\x1bB":
        return "alt+left"
    if not active and data == "\x1bF":
        return "alt+right"
    if not active and len(data) == 2 and data[0] == "\x1b":
        code = ord(data[1])
        if 1 <= code <= 26:
            return f"ctrl+alt+{chr(code + 96)}"
        key = data[1]
        if 97 <= code <= 122 or 48 <= code <= 57 or key in _SYMBOL_KEYS:
            return f"alt+{key}"
    if data == "\x1b[A":
        return "up"
    if data == "\x1b[B":
        return "down"
    if data == "\x1b[C":
        return "right"
    if data == "\x1b[D":
        return "left"
    if data == "\x1b[H" or data == "\x1bOH":
        return "home"
    if data == "\x1b[F" or data == "\x1bOF":
        return "end"
    if data == "\x1b[3~":
        return "delete"
    if data == "\x1b[5~":
        return "pageUp"
    if data == "\x1b[6~":
        return "pageDown"
    if len(data) == 1:
        code = ord(data)
        if 1 <= code <= 26:
            return f"ctrl+{chr(code + 96)}"
        if 32 <= code <= 126:
            return data
    return None


_PRINTABLE_ALLOWED = _SHIFT | _LOCK_MASK


def _from_code_point(value: float) -> str | None:
    """``String.fromCodePoint(value)``, or ``None`` where JavaScript throws."""
    if not math.isfinite(value) or value != int(value) or value < 0 or value > 0x10FFFF:
        return None
    return chr(int(value))


def decode_kitty_printable(data: str) -> str | None:
    """The character of a plain or shifted Kitty CSI-u key event (pi-tui's ``decodeKittyPrintable``)."""
    m = _CSI_U.match(data)
    if not m:
        return None
    codepoint = _num(m.group(1))
    shifted = _num(m.group(2)) if m.group(2) else None
    mod_value = _num(m.group(4)) if m.group(4) else 1
    modifier = _i32(mod_value - 1) if math.isfinite(mod_value) else 0
    if modifier & ~_PRINTABLE_ALLOWED:
        return None
    if modifier & (_ALT | _CTRL):
        return None
    effective = codepoint
    if modifier & _SHIFT and shifted is not None:
        effective = shifted
    effective = _normalize_functional(effective)
    if not math.isfinite(effective) or effective < 32:
        return None
    return _from_code_point(effective)


def _decode_modify_other_printable(data: str) -> str | None:
    parsed = _parse_modify_other(data)
    if parsed is None:
        return None
    codepoint, modifier = parsed
    if _i32(modifier) & ~_LOCK_MASK & ~_SHIFT:
        return None
    if not math.isfinite(codepoint) or codepoint < 32:
        return None
    return _from_code_point(codepoint)


def decode_printable_key(data: str) -> str | None:
    """:func:`decode_kitty_printable`, or the character of a modifyOtherKeys event (pi-tui's ``decodePrintableKey``)."""
    printable = decode_kitty_printable(data)
    return printable if printable is not None else _decode_modify_other_printable(data)
