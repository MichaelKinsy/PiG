"""Pi's formatDimensionNote (utils/image-resize.ts:115-123, exported from index.ts)."""
import math
from dataclasses import dataclass
from decimal import ROUND_HALF_UP, Decimal, localcontext
from typing import Optional

from .llm_convert import _js_number_to_string


@dataclass(frozen=True)
class ResizedImage:
    """The dimensions of Pi's ResizedImage (utils/image-resize-core.ts) that format_dimension_note reads. Dimensions are JavaScript numbers."""
    original_width: float
    original_height: float
    width: float
    height: float
    was_resized: bool


def format_dimension_note(result: ResizedImage) -> Optional[str]:
    """Pi's formatDimensionNote: the note that tells the model how a resized image's coordinates map to the original, and None when the image
    was not resized."""
    if not result.was_resized:
        return None
    scale = _js_divide(float(result.original_width), float(result.width))
    return (
        "[Image: original " + _js_number_to_string(result.original_width) + "x" + _js_number_to_string(result.original_height)
        + ", displayed at " + _js_number_to_string(result.width) + "x" + _js_number_to_string(result.height)
        + ". Multiply coordinates by " + _js_to_fixed_2(scale) + " to map to original image.]"
    )


def _js_divide(numerator: float, denominator: float) -> float:
    """IEEE division, which Python raises on a zero denominator instead of returning an infinity or NaN."""
    if denominator != 0:
        return numerator / denominator
    if numerator == 0 or math.isnan(numerator):
        return math.nan
    return math.copysign(math.inf, numerator) * math.copysign(1.0, denominator)


def _js_to_fixed_2(value: float) -> str:
    """Number.prototype.toFixed(2): the exact decimal value of the double rounded half up in magnitude (a tie goes to the larger n, which Python's
    half-even round does not do for 0.125), "-" for any x < 0 even when it rounds to zero, and Number::toString from 1e21 on."""
    if math.isnan(value):
        return "NaN"
    if abs(value) >= 1e21:
        return _js_number_to_string(value)
    sign = "-" if value < 0 else ""
    with localcontext() as context:
        context.prec = 1200
        rounded = Decimal(abs(value)).quantize(Decimal("0.01"), rounding=ROUND_HALF_UP)
    return sign + format(rounded, "f")
