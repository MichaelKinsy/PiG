"""The theme's wire colors (``{"kind": "rgb", ...}`` dicts) over pi-tui's color model.

The conversions and the SGR rendering are ``pig_sdk.tui``'s own (``foreground_ansi``, ``background_ansi``, ``style_text_with_ansi``); this module only
validates the dicts the host sends and an extension passes, and hands the tui color to them. The tui module loads on first use, so an extension that
never styles a color does not import it.
"""

from __future__ import annotations

import math
from typing import Any


def is_number(value: Any) -> bool:
    return isinstance(value, (int, float)) and not isinstance(value, bool) and math.isfinite(value)


def parse_color(value: Any) -> dict[str, Any] | None:
    """A copy of a wire color, or ``None`` when ``value`` is not a well-formed pi-tui Color."""
    if not isinstance(value, dict):
        return None
    kind = value.get("kind")
    if kind == "indexed":
        index = value.get("index")
        if is_number(index) and index == int(index) and 0 <= index <= 255:
            return {"kind": "indexed", "index": int(index)}
    elif kind == "rgb":
        if all(is_number(value.get(key)) for key in "rgb"):
            return {"kind": "rgb", "r": value["r"], "g": value["g"], "b": value["b"]}
    elif kind == "oklch":
        if all(is_number(value.get(key)) for key in "lch"):
            return {"kind": "oklch", "l": value["l"], "c": value["c"], "h": value["h"]}
    return None


def _tui_color(color: dict[str, Any]):
    from .tui import _colors

    if color["kind"] == "indexed":
        return _colors.IndexedColor(color["index"])
    if color["kind"] == "rgb":
        return _colors.RgbColorValue(color["r"], color["g"], color["b"])
    return _colors.OklchColorValue(color["l"], color["c"], color["h"])


def color_ansi(color: dict[str, Any], mode: str, background: bool) -> str:
    """The SGR sequence selecting ``color`` as the foreground or background in ``mode``."""
    from .tui import _colors

    render = _colors.background_ansi if background else _colors.foreground_ansi
    return render(_tui_color(color), mode)


def style_text_with_ansi(text: str, fg_ansi: str, bg_ansi: str, attributes: dict[str, Any]) -> str:
    """Wrap ``text`` in the color sequences and attributes, closing them in reverse order of opening. An empty sequence is unset."""
    from .tui import _colors

    options = _colors.TextAttributes(**{key: bool(attributes.get(key)) for key in ("bold", "dim", "italic", "underline", "inverse", "strikethrough")})
    return _colors.style_text_with_ansi(text, fg_ansi, bg_ansi, options)
