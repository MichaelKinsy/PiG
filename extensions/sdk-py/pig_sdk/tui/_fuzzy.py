"""pi-tui's fuzzy.ts: ordered-subsequence matching with Pi's scores (lower is better).

Matching runs over UTF-16 code units, as JavaScript strings do, so the indexes in the score agree with Pi's for astral characters.
"""

from __future__ import annotations

import re
from array import array
from collections.abc import Callable, Sequence
from dataclasses import dataclass
from typing import TypeVar

from ._lower import JS_LOWER
from ._text import _is_js_space, _trim_end, _trim_start

T = TypeVar("T")

_SIGMA = "\u03a3"


def js_lower(text: str) -> str:
    """``String.prototype.toLowerCase``: the Unicode of Node's ICU for every code point, Final_Sigma by Python's own rule."""
    if text.isascii():
        return text.lower()
    out = [JS_LOWER.get(ord(c), c) if c != _SIGMA else c for c in text]
    if _SIGMA in text:
        # Final_Sigma needs the cased/case-ignorable context; Python implements it, so lowercase a copy whose other characters are 1:1.
        probe = "".join("A" if (c != _SIGMA and ord(c) in JS_LOWER) else c for c in text).lower()
        out = [probe[i] if c == _SIGMA else o for i, (c, o) in enumerate(zip(text, out))]
    return "".join(out)


def _units(text: str) -> array:
    return array("H", text.encode("utf-16-le", "surrogatepass"))


@dataclass(frozen=True)
class FuzzyMatch:
    """pi-tui's ``FuzzyMatch``: whether every query character appears in order, and the score (lower is better)."""

    matches: bool
    score: float


_BOUNDARY = frozenset(map(ord, "-_./:"))
_ALNUM_DIGITS = re.compile(r"([a-z]+)([0-9]+)\Z")
_DIGITS_ALNUM = re.compile(r"([0-9]+)([a-z]+)\Z")


def _is_boundary(unit: int) -> bool:
    if unit in _BOUNDARY:
        return True
    if unit >= 0xD800 and unit <= 0xDFFF:
        return False
    return _is_js_space(chr(unit))


def _match_query(query: array, text: array) -> FuzzyMatch:
    if len(query) == 0:
        return FuzzyMatch(True, 0)
    if len(query) > len(text):
        return FuzzyMatch(False, 0)
    query_index = 0
    score = 0.0
    last = -1
    consecutive = 0
    while query_index < len(query):
        try:
            i = text.index(query[query_index], last + 1)
        except ValueError:
            break
        is_boundary = i == 0 or _is_boundary(text[i - 1])
        if last == i - 1:
            consecutive += 1
            score -= consecutive * 5
        else:
            consecutive = 0
            if last >= 0:
                score += (i - last - 1) * 2
        if is_boundary:
            score -= 10
        score += i * 0.1
        last = i
        query_index += 1
    if query_index < len(query):
        return FuzzyMatch(False, 0)
    if query == text:
        score -= 100
    return FuzzyMatch(True, score)


def fuzzy_match(query: str, text: str) -> FuzzyMatch:
    """Whether every character of ``query`` appears in ``text`` in order, and Pi's score (pi-tui's ``fuzzyMatch``)."""
    query_lower = js_lower(query)
    text_units = _units(js_lower(text))
    primary = _match_query(_units(query_lower), text_units)
    if primary.matches:
        return primary
    alpha_num = _ALNUM_DIGITS.match(query_lower)
    numeric_alpha = _DIGITS_ALNUM.match(query_lower)
    if alpha_num:
        swapped = alpha_num.group(2) + alpha_num.group(1)
    elif numeric_alpha:
        swapped = numeric_alpha.group(2) + numeric_alpha.group(1)
    else:
        swapped = ""
    if not swapped:
        return primary
    swapped_match = _match_query(_units(swapped), text_units)
    if not swapped_match.matches:
        return primary
    return FuzzyMatch(True, swapped_match.score + 5)


def fuzzy_filter(items: Sequence[T], query: str, get_text: Callable[[T], str]) -> list[T]:
    """The items whose text matches every whitespace- or slash-separated token of ``query``, best first (pi-tui's ``fuzzyFilter``).

    A blank query returns the items unchanged.
    """
    if not _trim_start(_trim_end(query)):
        return list(items)
    tokens = [t for t in re.split(r"[/" + "".join(c for c in _JS_SPACES) + r"]+", _trim_start(_trim_end(query))) if t]
    if not tokens:
        return list(items)
    results: list[tuple[float, int, T]] = []
    for index, item in enumerate(items):
        text = get_text(item)
        total = 0.0
        all_match = True
        for token in tokens:
            match = fuzzy_match(token, text)
            if match.matches:
                total += match.score
            else:
                all_match = False
                break
        if all_match:
            results.append((total, index, item))
    results.sort(key=lambda r: (r[0], r[1]))
    return [r[2] for r in results]


_JS_SPACES = "\t\n\v\f\r \xa0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"
