"""The part of pi-tui's color model that renders a color as an SGR sequence.

Ports ``.upstream/v0.99.2/packages/tui/src/colors.ts`` (``foregroundAnsi``, ``backgroundAnsi``, ``styleTextWithAnsi``) and ``oklab.ts`` (``oklabToLinearSrgb``). Oklab is Björn Ottosson's color space (https://bottosson.github.io/posts/colorpicker/, MIT).
"""

from __future__ import annotations

import math
from typing import Any

_LAB_TO_LMS = (
    (1.0, 0.3963377773761749, 0.2158037573099136),
    (1.0, -0.1055613458156586, -0.0638541728258133),
    (1.0, -0.0894841775298119, -1.2914855480194092),
)
_LMS_TO_LINEAR_SRGB = (
    (4.0767416360759583, -3.3077115392580629, 0.2309699031821043),
    (-1.2684379732850315, 2.6097573492876882, -0.341319376002657),
    (-0.0041960761386756, -0.7034186179359362, 1.7076146940746117),
)
_BASIC_COLORS = (
    (0, 0, 0), (128, 0, 0), (0, 128, 0), (128, 128, 0), (0, 0, 128), (128, 0, 128), (0, 128, 128), (192, 192, 192),
    (128, 128, 128), (255, 0, 0), (0, 255, 0), (255, 255, 0), (0, 0, 255), (255, 0, 255), (0, 255, 255), (255, 255, 255),
)
_CUBE_VALUES = (0, 95, 135, 175, 215, 255)
_GRAY_VALUES = tuple(8 + i * 10 for i in range(24))


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


def _js_round(value: float) -> int:
    return math.floor(value + 0.5)


def _linear_to_srgb(value: float) -> float:
    if value > 0.0031308:
        return 1.055 * value ** (1 / 2.4) - 0.055
    return 12.92 * value


def _oklab_to_linear_srgb(lightness: float, a: float, b: float) -> tuple[float, float, float]:
    cubed = []
    for row in _LAB_TO_LMS:
        lms = row[0] * lightness + row[1] * a + row[2] * b
        cubed.append(lms * lms * lms)
    r, g, bl = (row[0] * cubed[0] + row[1] * cubed[1] + row[2] * cubed[2] for row in _LMS_TO_LINEAR_SRGB)
    return (r, g, bl)


def _in_gamut(linear: tuple[float, float, float]) -> bool:
    epsilon = 1e-7
    return all(-epsilon <= channel <= 1 + epsilon for channel in linear)


def _linear_to_rgb(linear: tuple[float, float, float]) -> tuple[int, int, int]:
    r, g, b = (_js_round(min(1.0, max(0.0, _linear_to_srgb(channel))) * 255) for channel in linear)
    return (r, g, b)


def _oklch_to_rgb(lightness: float, c: float, h: float) -> tuple[int, int, int]:
    """Map an OKLCH color into sRGB, keeping its hue and reducing chroma until it fits; the achromatic color is the fallback."""
    radians = (h * math.pi) / 180
    cos, sin = math.cos(radians), math.sin(radians)

    def at_chroma(chroma: float) -> tuple[float, float, float]:
        return _oklab_to_linear_srgb(lightness, chroma * cos, chroma * sin)

    direct = at_chroma(c)
    if _in_gamut(direct):
        return _linear_to_rgb(direct)
    linear = at_chroma(0.0)
    low, high = 0.0, c
    for _ in range(20):
        chroma = (low + high) / 2
        candidate = at_chroma(chroma)
        if _in_gamut(candidate):
            low = chroma
            linear = candidate
        else:
            high = chroma
    return _linear_to_rgb(linear)


def _color_to_rgb(color: dict[str, Any]) -> tuple[float, float, float]:
    kind = color["kind"]
    if kind == "indexed":
        index = color["index"]
        if index < 16:
            return _BASIC_COLORS[index]
        if index < 232:
            cube = index - 16
            return (_CUBE_VALUES[cube // 36], _CUBE_VALUES[(cube % 36) // 6], _CUBE_VALUES[cube % 6])
        gray = 8 + (index - 232) * 10
        return (gray, gray, gray)
    if kind == "rgb":
        return (color["r"], color["g"], color["b"])
    return _oklch_to_rgb(color["l"], color["c"], color["h"])


def _find_closest(values: tuple[int, ...], target: float) -> int:
    closest, distance = 0, math.inf
    for index, value in enumerate(values):
        d = abs(target - value)
        if d < distance:
            closest, distance = index, d
    return closest


def _color_distance(first: tuple[float, float, float], second: tuple[float, float, float]) -> float:
    dr, dg, db = first[0] - second[0], first[1] - second[1], first[2] - second[2]
    return dr * dr * 0.299 + dg * dg * 0.587 + db * db * 0.114


def _rgb_to_ansi256(color: tuple[float, float, float]) -> int:
    r_index, g_index, b_index = (_find_closest(_CUBE_VALUES, channel) for channel in color)
    cube_color = (_CUBE_VALUES[r_index], _CUBE_VALUES[g_index], _CUBE_VALUES[b_index])
    cube_index = 16 + 36 * r_index + 6 * g_index + b_index
    gray = _js_round(0.299 * color[0] + 0.587 * color[1] + 0.114 * color[2])
    gray_offset = _find_closest(_GRAY_VALUES, gray)
    gray_value = _GRAY_VALUES[gray_offset]
    spread = max(color) - min(color)
    if spread < 10 and _color_distance(color, (gray_value, gray_value, gray_value)) < _color_distance(color, cube_color):
        return 232 + gray_offset
    return cube_index


def color_ansi(color: dict[str, Any], mode: str, background: bool) -> str:
    """The SGR sequence selecting ``color`` as the foreground or background in ``mode``."""
    layer = 48 if background else 38
    if color["kind"] == "indexed":
        return f"\x1b[{layer};5;{color['index']}m"
    rgb = _color_to_rgb(color)
    if mode == "truecolor":
        return f"\x1b[{layer};2;{_js_round(rgb[0])};{_js_round(rgb[1])};{_js_round(rgb[2])}m"
    return f"\x1b[{layer};5;{_rgb_to_ansi256(rgb)}m"


def style_text_with_ansi(text: str, fg_ansi: str, bg_ansi: str, attributes: dict[str, Any]) -> str:
    """Wrap ``text`` in the color sequences and attributes, closing them in reverse order of opening. An empty sequence is unset."""
    prefix = ""
    suffix = ""
    if fg_ansi:
        prefix += fg_ansi
        suffix = "\x1b[39m"
    if bg_ansi:
        prefix += bg_ansi
        suffix = "\x1b[49m" + suffix
    bold, dim = bool(attributes.get("bold")), bool(attributes.get("dim"))
    if bold:
        prefix += "\x1b[1m"
    if dim:
        prefix += "\x1b[2m"
    if bold or dim:
        suffix = "\x1b[22m" + suffix
    for key, open_code, close_code in (("italic", 3, 23), ("underline", 4, 24), ("inverse", 7, 27), ("strikethrough", 9, 29)):
        if attributes.get(key):
            prefix += f"\x1b[{open_code}m"
            suffix = f"\x1b[{close_code}m" + suffix
    return prefix + text + suffix
