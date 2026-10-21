"""pi-tui's colors.ts and oklab.ts: colors, OKLCH/OKHSL conversion, mixing and ANSI styling.

A port of the pinned Pi release. JavaScript semantics the numbers depend on are kept explicitly: ``%`` is ``fmod``, a division by zero is an
infinity or NaN, ``Math.round`` rounds halves toward +Infinity, and a number prints in JavaScript's shortest form.
"""

from __future__ import annotations

import math
import re
from dataclasses import dataclass
from decimal import Decimal
from typing import Literal, Union

_NAN = math.nan
_INF = math.inf


def js_number_string(value: float) -> str:
    """``Number.prototype.toString()`` of a float or int."""
    if isinstance(value, bool):
        value = int(value)
    value = float(value)
    if value != value:
        return "NaN"
    if value == _INF:
        return "Infinity"
    if value == -_INF:
        return "-Infinity"
    if value == 0:
        return "0"
    sign = "-" if value < 0 else ""
    digits_tuple = Decimal(repr(abs(value))).as_tuple()
    digits = "".join(map(str, digits_tuple.digits)).rstrip("0") or "0"
    n = len(digits_tuple.digits) + digits_tuple.exponent  # value = 0.DIGITS x 10**n
    k = len(digits)
    if k <= n <= 21:
        return sign + digits + "0" * (n - k)
    if 0 < n <= 21:
        return sign + digits[:n] + "." + digits[n:]
    if -6 < n <= 0:
        return sign + "0." + "0" * (-n) + digits
    e = n - 1
    mantissa = digits[0] + ("." + digits[1:] if k > 1 else "")
    return f"{sign}{mantissa}e{'-' if e < 0 else '+'}{abs(e)}"


def _round(value: float) -> float:
    """``Math.round``."""
    if value != value or value in (_INF, -_INF) or value == 0:
        return value
    if 0 < value < 0.5:
        return 0.0
    if -0.5 <= value < 0:
        return -0.0
    rounded = math.floor(value)
    return float(rounded + 1 if value - rounded >= 0.5 else rounded)


def _max(a: float, b: float) -> float:
    return _NAN if a != a or b != b else (a if a > b else b)


def _min(a: float, b: float) -> float:
    return _NAN if a != a or b != b else (a if a < b else b)


def _div(a: float, b: float) -> float:
    try:
        return a / b
    except ZeroDivisionError:
        if a != a or a == 0:
            return _NAN
        return math.copysign(_INF, a) * math.copysign(1.0, b)


def _sqrt(value: float) -> float:
    return math.sqrt(value) if value >= 0 else (value if value != value else _NAN)


def _pow(base: float, exponent: float) -> float:
    try:
        result = base**exponent
    except ZeroDivisionError:
        return _INF
    except OverflowError:
        return _INF if base > 0 or float(exponent).is_integer() and exponent % 2 == 0 else -_INF
    return _NAN if isinstance(result, complex) else result


def _fmod(a: float, b: float) -> float:
    return _NAN if not math.isfinite(a) else math.fmod(a, b)


@dataclass(frozen=True)
class IndexedColor:
    """An ANSI 256-color palette index (pi-tui's ``IndexedColor``)."""

    index: int
    kind: Literal["indexed"] = "indexed"


@dataclass(frozen=True)
class RgbColorValue:
    """An sRGB color with channels 0-255 (pi-tui's ``RgbColorValue``)."""

    r: float
    g: float
    b: float
    kind: Literal["rgb"] = "rgb"


@dataclass(frozen=True)
class OklchColorValue:
    """An OKLCH color (pi-tui's ``OklchColorValue``)."""

    l: float  # noqa: E741
    c: float
    h: float
    kind: Literal["oklch"] = "oklch"


Color = Union[IndexedColor, RgbColorValue, OklchColorValue]
TerminalColorMode = Literal["256color", "truecolor"]
ColorMixSpace = Literal["oklch", "srgb"]


@dataclass(frozen=True)
class RgbColor:
    """The channels :func:`color_to_rgb` returns (pi-tui's ``RgbColor``)."""

    r: float
    g: float
    b: float


@dataclass(frozen=True)
class OklchChannels:
    l: float  # noqa: E741
    c: float
    h: float


@dataclass(frozen=True)
class OkhslChannels:
    h: float
    s: float
    l: float  # noqa: E741


@dataclass(frozen=True)
class TextAttributes:
    """The non-color attributes of :func:`style_text`."""

    bold: bool = False
    dim: bool = False
    italic: bool = False
    underline: bool = False
    inverse: bool = False
    strikethrough: bool = False


@dataclass(frozen=True)
class TextStyle(TextAttributes):
    """:class:`TextAttributes` plus an optional foreground and background color."""

    fg: Color | None = None
    bg: Color | None = None


def _require_finite(value: float, name: str) -> None:
    if not math.isfinite(value):
        raise ValueError(f"{name} must be finite")


def indexed_color(index: int) -> IndexedColor:
    if isinstance(index, bool) or not isinstance(index, (int, float)) or index != index or math.isinf(index) or index != int(index) or index < 0 or index > 255:
        raise ValueError(f"ANSI color index must be an integer from 0 to 255: {js_number_string(index)}")
    return IndexedColor(int(index))


def rgb_color(r: float, g: float, b: float) -> RgbColorValue:
    for name, value in (("r", r), ("g", g), ("b", b)):
        _require_finite(value, name)
        if value < 0 or value > 255:
            raise ValueError(f"{name} must be between 0 and 255: {js_number_string(value)}")
    return RgbColorValue(r, g, b)


def oklch_color(l: float, c: float, h: float) -> OklchColorValue:  # noqa: E741
    _require_finite(l, "l")
    _require_finite(c, "c")
    _require_finite(h, "h")
    if l < 0 or l > 1:
        raise ValueError(f"l must be between 0 and 1: {js_number_string(l)}")
    if c < 0:
        raise ValueError(f"c must not be negative: {js_number_string(c)}")
    return OklchColorValue(l, c, _fmod(_fmod(h, 360) + 360, 360))


# ------------------------------------------------------------------ oklab.ts
_LINEAR_SRGB_TO_LMS = (
    (0.4122214694707629, 0.5363325372617349, 0.0514459932675022),
    (0.2119034958178251, 0.6806995506452344, 0.1073969535369405),
    (0.0883024591900564, 0.2817188391361215, 0.6299787016738222),
)
_LMS_TO_LAB = (
    (0.210454268309314, 0.793617774702305, -0.0040720430116193),
    (1.9779985324311684, -2.42859224204858, 0.450593709617411),
    (0.0259040424655478, 0.7827717124575296, -0.8086757549230774),
)
_LAB_TO_LMS = (
    (1, 0.3963377773761749, 0.2158037573099136),
    (1, -0.1055613458156586, -0.0638541728258133),
    (1, -0.0894841775298119, -1.2914855480194092),
)
_LMS_TO_LINEAR_SRGB = (
    (4.0767416360759583, -3.3077115392580629, 0.2309699031821043),
    (-1.2684379732850315, 2.6097573492876882, -0.341319376002657),
    (-0.0041960761386756, -0.7034186179359362, 1.7076146940746117),
)
_SATURATION_FIT = (
    ((-1.8817031, -0.80936501), (1.19086277, 1.76576728, 0.59662641, 0.75515197, 0.56771245)),
    ((1.8144408, -1.19445267), (0.73956515, -0.45954404, 0.08285427, 0.12541073, -0.14503204)),
    ((0.13110758, 1.81333971), (1.35733652, -0.00915799, -1.1513021, -0.50559606, 0.00692167)),
)
_K1 = 0.206
_K2 = 0.03
_K3 = (1 + _K1) / (1 + _K2)
_MAX_VALUE = 1.7976931348623157e308


def _multiply(m, v):
    x, y, z = v
    return (m[0][0] * x + m[0][1] * y + m[0][2] * z, m[1][0] * x + m[1][1] * y + m[1][2] * z, m[2][0] * x + m[2][1] * y + m[2][2] * z)


def oklab_to_okhsl_lightness(x: float) -> float:
    return 0.5 * (_K3 * x - _K1 + _sqrt(_pow(_K3 * x - _K1, 2) + 4 * _K2 * _K3 * x))


def _okhsl_to_oklab_lightness(x: float) -> float:
    return _div(x * x + _K1 * x, _K3 * (x + _K2))


def _linear_to_srgb(value: float) -> float:
    return 1.055 * _pow(value, 1 / 2.4) - 0.055 if value > 0.0031308 else 12.92 * value


def _srgb_to_linear(value: float) -> float:
    return value / 12.92 if value <= 0.04045 else _pow((value + 0.055) / 1.055, 2.4)


def _oklab_to_linear_srgb(lab):
    lms = _multiply(_LAB_TO_LMS, lab)
    return _multiply(_LMS_TO_LINEAR_SRGB, tuple(_pow(v, 3) for v in lms))


def _cbrt(value: float) -> float:
    return math.cbrt(value) if math.isfinite(value) else value


def _linear_srgb_to_oklab(rgb):
    return _multiply(_LMS_TO_LAB, tuple(_cbrt(v) for v in _multiply(_LINEAR_SRGB_TO_LMS, rgb)))


def _rgb_to_oklab(rgb: RgbColor):
    return _linear_srgb_to_oklab(tuple(_srgb_to_linear(v / 255) for v in (rgb.r, rgb.g, rgb.b)))


def _linear_srgb_to_rgb(linear) -> RgbColor:
    r, g, b = (_round(_min(1, _max(0, _linear_to_srgb(v))) * 255) for v in linear)
    return RgbColor(r, g, b)


def _lms_slopes(a: float, b: float):
    return tuple(row[1] * a + row[2] * b for row in _LAB_TO_LMS)


def _max_saturation(a: float, b: float) -> float:
    channel = 2
    for index, ((x, y), _) in enumerate(_SATURATION_FIT):
        if index == 2 or x * a + y * b > 1:
            channel = index
            break
    k0, k1, k2, k3, k4 = _SATURATION_FIT[channel][1]
    weights = _LMS_TO_LINEAR_SRGB[channel]
    saturation = k0 + k1 * a + k2 * b + k3 * a * a + k4 * a * b
    slopes = _lms_slopes(a, b)
    base = [1 + saturation * k for k in slopes]

    def dot(values):
        total = 0
        for index, value in enumerate(values):
            total = total + weights[index] * value
        return total

    f = dot([_pow(v, 3) for v in base])
    f1 = dot([3 * slopes[i] * _pow(v, 2) for i, v in enumerate(base)])
    f2 = dot([6 * _pow(slopes[i], 2) * v for i, v in enumerate(base)])
    return saturation - _div(f * f1, f1 * f1 - 0.5 * f * f2)


def _cusp(a: float, b: float):
    saturation = _max_saturation(a, b)
    lms = _oklab_to_linear_srgb((1, saturation * a, saturation * b))
    peak = lms[0]
    for v in lms[1:]:
        peak = _max(peak, v)
    lightness = _cbrt(_div(1, peak))
    return lightness, lightness * saturation


def _max_chroma(a: float, b: float, lightness: float, cusp) -> float:
    cusp_l, cusp_c = cusp
    if lightness <= cusp_l:
        return _div(cusp_c * lightness, cusp_l)
    t = _div(cusp_c * (lightness - 1), cusp_l - 1)
    slopes = _lms_slopes(a, b)
    lms = [lightness + t * k for k in slopes]
    cubes = [_pow(v, 3) for v in lms]
    first = [3 * slopes[i] * _pow(v, 2) for i, v in enumerate(lms)]
    second = [6 * _pow(slopes[i], 2) * v for i, v in enumerate(lms)]
    steps = []
    for row in _LMS_TO_LINEAR_SRGB:
        f = row[0] * cubes[0] + row[1] * cubes[1] + row[2] * cubes[2] - 1
        f1 = row[0] * first[0] + row[1] * first[1] + row[2] * first[2]
        f2 = row[0] * second[0] + row[1] * second[1] + row[2] * second[2]
        u = _div(f1, f1 * f1 - 0.5 * f * f2)
        steps.append(-f * u if u >= 0 else _MAX_VALUE)
    smallest = steps[0]
    for s in steps[1:]:
        smallest = _min(smallest, s)
    return t + smallest


def _chroma_stops(lightness: float, a: float, b: float):
    peak = _cusp(a, b)
    c_max = _max_chroma(a, b, lightness, peak)
    k = _div(c_max, _min(lightness * _div(peak[1], peak[0]), (1 - lightness) * _div(peak[1], 1 - peak[0])))
    mid_s = 0.11516993 + _div(
        1,
        7.4477897 + 4.1590124 * b + a * (-2.19557347 + 1.75198401 * b + a * (-2.13704948 - 10.02301043 * b + a * (-4.24894561 + 5.38770819 * b + 4.69891013 * a))),
    )
    mid_t = 0.11239642 + _div(
        1,
        1.6132032 - 0.68124379 * b + a * (0.40370612 + 0.90148123 * b + a * (-0.27087943 + 0.6122399 * b + a * (0.00299215 - 0.45399568 * b - 0.14661872 * a))),
    )
    c_mid = 0.9 * k * _sqrt(_sqrt(_div(1, _div(1, _pow(lightness * mid_s, 4)) + _div(1, _pow((1 - lightness) * mid_t, 4)))))
    c0 = _sqrt(_div(1, _div(1, _pow(lightness * 0.4, 2)) + _div(1, _pow((1 - lightness) * 0.8, 2))))
    return c0, c_mid, c_max


def _okhsl_to_rgb(hue: float, saturation: float, lightness: float) -> RgbColor:
    big_l = _okhsl_to_oklab_lightness(lightness)
    lab = (big_l, 0.0, 0.0)
    if 0 < big_l < 1 and saturation > 0:
        angle = _div(2 * math.pi * _fmod(_fmod(hue, 360) + 360, 360), 360)
        a = math.cos(angle)
        b = math.sin(angle)
        c0, c_mid, c_max = _chroma_stops(big_l, a, b)
        if saturation < 0.8:
            t = 1.25 * saturation
            k1 = 0.8 * c0
            chroma = _div(t * k1, 1 - (1 - _div(k1, c_mid)) * t)
        else:
            t = 5 * (saturation - 0.8)
            k1 = _div(0.2 * _pow(c_mid, 2) * _pow(1.25, 2), c0)
            chroma = c_mid + _div(t * k1, 1 - (1 - _div(k1, c_max - c_mid)) * t)
        lab = (big_l, chroma * a, chroma * b)
    return _linear_srgb_to_rgb(_oklab_to_linear_srgb(lab))


def _rgb_to_okhsl(rgb: RgbColor) -> OkhslChannels:
    big_l, lab_a, lab_b = _rgb_to_oklab(rgb)
    chroma = math.hypot(lab_a, lab_b)
    lightness = oklab_to_okhsl_lightness(big_l)
    if chroma < 1e-9 or lightness <= 0 or lightness >= 1:
        return OkhslChannels(0, 0, lightness)
    hue = _fmod(math.atan2(lab_b, lab_a) * 180 / math.pi + 360, 360)
    c0, c_mid, c_max = _chroma_stops(big_l, lab_a / chroma, lab_b / chroma)
    if chroma < c_mid:
        k1 = 0.8 * c0
        saturation = 0.8 * _div(chroma, k1 + (1 - _div(k1, c_mid)) * chroma)
    else:
        k1 = _div(0.2 * _pow(c_mid, 2) * _pow(1.25, 2), c0)
        offset = chroma - c_mid
        saturation = 0.8 + 0.2 * _div(offset, k1 + (1 - _div(k1, c_max - c_mid)) * offset)
    return OkhslChannels(hue, _min(1, _max(0, saturation)), lightness)


# ------------------------------------------------------------------ colors.ts
def okhsl_color(h: float, s: float, l: float) -> RgbColorValue:  # noqa: E741
    """An OKHSL color converted to sRGB (pi-tui's ``okhslColor``)."""
    _require_finite(h, "h")
    _require_finite(s, "s")
    _require_finite(l, "l")
    if s < 0 or s > 1:
        raise ValueError(f"s must be between 0 and 1: {js_number_string(s)}")
    if l < 0 or l > 1:
        raise ValueError(f"l must be between 0 and 1: {js_number_string(l)}")
    rgb = _okhsl_to_rgb(h, s, l)
    return rgb_color(rgb.r, rgb.g, rgb.b)


def color_to_okhsl(color: Color) -> OkhslChannels:
    return _rgb_to_okhsl(color_to_rgb(color))


_NUMBER = r"[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:e[+-]?[0-9]+)?"
_SPACE = "[\t\n\v\f\r \xa0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000\ufeff]"
_OKLCH = re.compile(rf"oklch\({_SPACE}*({_NUMBER})(%)?{_SPACE}+({_NUMBER}){_SPACE}+({_NUMBER})(?:deg)?{_SPACE}*\)\Z", re.I | re.A)
_OKHSL = re.compile(rf"okhsl\({_SPACE}*({_NUMBER})(?:deg)?{_SPACE}+({_NUMBER})(%)?{_SPACE}+({_NUMBER})(%)?{_SPACE}*\)\Z", re.I | re.A)
_HEX = re.compile(r"#([0-9a-f]{3}|[0-9a-f]{6})\Z", re.I | re.A)


def parse_color(value: str | int | float) -> Color:
    """A color from ``"#rgb"``, ``"#rrggbb"``, ``"oklch(...)"``, ``"okhsl(...)"`` or a palette index (pi-tui's ``parseColor``)."""
    if not isinstance(value, str):
        return indexed_color(value)
    m = _HEX.match(value)
    if m:
        digits = m.group(1)
        if len(digits) == 3:
            digits = "".join(d + d for d in digits)
        return rgb_color(int(digits[0:2], 16), int(digits[2:4], 16), int(digits[4:6], 16))
    m = _OKLCH.match(value)
    if m:
        lightness = float(m.group(1)) / (100 if m.group(2) else 1)
        return oklch_color(lightness, float(m.group(3)), float(m.group(4)))
    m = _OKHSL.match(value)
    if m:
        saturation = float(m.group(2)) / (100 if m.group(3) else 1)
        lightness = float(m.group(4)) / (100 if m.group(5) else 1)
        return okhsl_color(float(m.group(1)), saturation, lightness)
    raise ValueError(f"Invalid color value: {value}")


_BASIC_COLORS = (
    (0, 0, 0), (128, 0, 0), (0, 128, 0), (128, 128, 0), (0, 0, 128), (128, 0, 128), (0, 128, 128), (192, 192, 192),
    (128, 128, 128), (255, 0, 0), (0, 255, 0), (255, 255, 0), (0, 0, 255), (255, 0, 255), (0, 255, 255), (255, 255, 255),
)  # fmt: skip
_CUBE_VALUES = (0, 95, 135, 175, 215, 255)
_GRAY_VALUES = tuple(8 + i * 10 for i in range(24))


def _indexed_to_rgb(index: int) -> RgbColor:
    if index < 16:
        return RgbColor(*_BASIC_COLORS[index])
    if index < 232:
        cube = index - 16
        return RgbColor(_CUBE_VALUES[cube // 36], _CUBE_VALUES[(cube % 36) // 6], _CUBE_VALUES[cube % 6])
    gray = 8 + (index - 232) * 10
    return RgbColor(gray, gray, gray)


def _in_gamut(linear) -> bool:
    epsilon = 1e-7
    return all(-epsilon <= v <= 1 + epsilon for v in linear)


def _oklch_to_rgb(l: float, c: float, h: float) -> RgbColor:  # noqa: E741
    radians = h * math.pi / 180
    cos = math.cos(radians)
    sin = math.sin(radians)

    def at_chroma(chroma: float):
        return _oklab_to_linear_srgb((l, chroma * cos, chroma * sin))

    direct = at_chroma(c)
    if _in_gamut(direct):
        return _linear_srgb_to_rgb(direct)
    linear = at_chroma(0)
    low = 0.0
    high = c
    for _ in range(20):
        chroma = (low + high) / 2
        candidate = at_chroma(chroma)
        if _in_gamut(candidate):
            low = chroma
            linear = candidate
        else:
            high = chroma
    return _linear_srgb_to_rgb(linear)


def color_to_rgb(color: Color) -> RgbColor:
    """The sRGB channels of ``color`` (pi-tui's ``colorToRgb``)."""
    if color.kind == "indexed":
        return _indexed_to_rgb(color.index)
    if color.kind == "rgb":
        return RgbColor(color.r, color.g, color.b)
    return _oklch_to_rgb(color.l, color.c, color.h)


def color_to_oklch(color: Color) -> OklchChannels:
    if color.kind == "oklch":
        return OklchChannels(color.l, color.c, color.h)
    l, a, b = _rgb_to_oklab(color_to_rgb(color))  # noqa: E741
    return OklchChannels(l, math.hypot(a, b), _fmod(math.atan2(b, a) * 180 / math.pi + 360, 360))


def color_to_hex(color: Color) -> str:
    rgb = color_to_rgb(color)
    return "#" + "".join(format(int(_round(v)), "x").rjust(2, "0") for v in (rgb.r, rgb.g, rgb.b))


def mix_colors(first: Color, second: Color, amount: float, space: ColorMixSpace = "oklch") -> Color:
    """``first`` blended toward ``second`` by ``amount`` in ``space`` (pi-tui's ``mixColors``)."""
    _require_finite(amount, "amount")
    if amount < 0 or amount > 1:
        raise ValueError(f"amount must be between 0 and 1: {js_number_string(amount)}")
    if space == "srgb":
        a = color_to_rgb(first)
        b = color_to_rgb(second)
        return rgb_color(a.r + (b.r - a.r) * amount, a.g + (b.g - a.g) * amount, a.b + (b.b - a.b) * amount)
    a = color_to_oklch(first)
    b = color_to_oklch(second)
    first_hue = b.h if a.c < 1e-7 else a.h
    second_hue = first_hue if b.c < 1e-7 else b.h
    hue_delta = _fmod(second_hue - first_hue + 540, 360) - 180
    return oklch_color(a.l + (b.l - a.l) * amount, a.c + (b.c - a.c) * amount, first_hue + hue_delta * amount)


def _find_closest(values, target: float) -> int:
    closest_index = 0
    closest_distance = _INF
    for index, value in enumerate(values):
        distance = abs(target - value)
        if distance < closest_distance:
            closest_index = index
            closest_distance = distance
    return closest_index


def _color_distance(first: RgbColor, second: RgbColor) -> float:
    dr = first.r - second.r
    dg = first.g - second.g
    db = first.b - second.b
    return dr * dr * 0.299 + dg * dg * 0.587 + db * db * 0.114


def _rgb_to_ansi256(color: RgbColor) -> int:
    r_index = _find_closest(_CUBE_VALUES, color.r)
    g_index = _find_closest(_CUBE_VALUES, color.g)
    b_index = _find_closest(_CUBE_VALUES, color.b)
    cube_color = RgbColor(_CUBE_VALUES[r_index], _CUBE_VALUES[g_index], _CUBE_VALUES[b_index])
    cube_index = 16 + 36 * r_index + 6 * g_index + b_index
    gray = _round(0.299 * color.r + 0.587 * color.g + 0.114 * color.b)
    gray_offset = _find_closest(_GRAY_VALUES, gray)
    gray_value = _GRAY_VALUES[gray_offset]
    spread = max(color.r, color.g, color.b) - min(color.r, color.g, color.b)
    if spread < 10 and _color_distance(color, RgbColor(gray_value, gray_value, gray_value)) < _color_distance(color, cube_color):
        return 232 + gray_offset
    return cube_index


def _color_ansi(color: Color, mode: TerminalColorMode, background: bool) -> str:
    layer = 48 if background else 38
    if color.kind == "indexed":
        return f"\x1b[{layer};5;{color.index}m"
    rgb = color_to_rgb(color)
    if mode == "truecolor":
        r, g, b = (js_number_string(_round(v)) for v in (rgb.r, rgb.g, rgb.b))
        return f"\x1b[{layer};2;{r};{g};{b}m"
    return f"\x1b[{layer};5;{_rgb_to_ansi256(rgb)}m"


def foreground_ansi(color: Color, mode: TerminalColorMode) -> str:
    return _color_ansi(color, mode, False)


def background_ansi(color: Color, mode: TerminalColorMode) -> str:
    return _color_ansi(color, mode, True)


def style_text(text: str, options: TextStyle, mode: TerminalColorMode) -> str:
    """``text`` wrapped in the SGR sequences of ``options`` (pi-tui's ``styleText``)."""
    fg = foreground_ansi(options.fg, mode) if options.fg else None
    bg = background_ansi(options.bg, mode) if options.bg else None
    return style_text_with_ansi(text, fg, bg, options)


def style_text_with_ansi(text: str, fg_ansi: str | None, bg_ansi: str | None, options: TextAttributes) -> str:
    """Like :func:`style_text` with precomputed color sequences, e.g. cached theme colors; colors in ``options`` are ignored."""
    prefix = ""
    suffix = ""
    if fg_ansi:
        prefix += fg_ansi
        suffix = "\x1b[39m"
    if bg_ansi:
        prefix += bg_ansi
        suffix = "\x1b[49m" + suffix
    if options.bold:
        prefix += "\x1b[1m"
    if options.dim:
        prefix += "\x1b[2m"
    if options.bold or options.dim:
        suffix = "\x1b[22m" + suffix
    if options.italic:
        prefix += "\x1b[3m"
        suffix = "\x1b[23m" + suffix
    if options.underline:
        prefix += "\x1b[4m"
        suffix = "\x1b[24m" + suffix
    if options.inverse:
        prefix += "\x1b[7m"
        suffix = "\x1b[27m" + suffix
    if options.strikethrough:
        prefix += "\x1b[9m"
        suffix = "\x1b[29m" + suffix
    return f"{prefix}{text}{suffix}"
