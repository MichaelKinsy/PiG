"""pi-coding-agent's keybinding hints (``modes/interactive/components/keybinding-hints.ts``): a key identifier as the interface shows it, and the dim key plus
muted description of a hint such as "esc to interrupt". The keys come from the global keybindings the host published and the colors from the global theme."""

from __future__ import annotations

import sys

from . import _tui_state


def _format_key_part(darwin: bool, part: str, capitalize: bool) -> str:
    # ``part.toLowerCase() === "alt"`` holds only for the ASCII case variants of "alt".
    display = "option" if darwin and part.isascii() and part.lower() == "alt" else part
    if not capitalize or display == "":
        return display
    first = display[0]
    # ``charAt(0).toUpperCase()`` upper-cases one UTF-16 unit: a character beyond the Basic Multilingual Plane is half a surrogate pair and stays as it is.
    if ord(first) > 0xFFFF:
        return display
    return first.upper() + display[1:]


def _format_key_text(darwin: bool, key: str, capitalize: bool) -> str:
    return "/".join("+".join(_format_key_part(darwin, part, capitalize) for part in alternative.split("+")) for alternative in key.split("/"))


def _format_keys(keys: list[str], capitalize: bool) -> str:
    if not keys:
        return ""
    return _format_key_text(sys.platform == "darwin", "/".join(keys), capitalize)


def format_key_text(key: str, capitalize: bool = False) -> str:
    """pi-coding-agent's ``formatKeyText``: a key identifier as the interface shows it, "alt" as "option" on macOS."""
    return _format_key_text(sys.platform == "darwin", key, capitalize)


def _keys(keybinding: str) -> list[str]:
    from .tui import get_keybindings

    return get_keybindings().get_keys(keybinding)


def key_text(keybinding: str) -> str:
    """pi-coding-agent's ``keyText``: the keys of a keybinding as the interface shows them."""
    return _format_keys(_keys(keybinding), False)


def key_display_text(keybinding: str) -> str:
    """pi-coding-agent's ``keyDisplayText``: :func:`key_text` with each part capitalized."""
    return _format_keys(_keys(keybinding), True)


def key_hint(keybinding: str, description: str) -> str:
    """pi-coding-agent's ``keyHint``: the keys of a keybinding in the dim color and ``description`` in the muted color of the host's theme."""
    theme = _tui_state.current_host_theme()
    return theme.fg("dim", key_text(keybinding)) + theme.fg("muted", f" {description}")


def raw_key_hint(key: str, description: str) -> str:
    """pi-coding-agent's ``rawKeyHint``: a hint for a key identifier that is not a keybinding."""
    theme = _tui_state.current_host_theme()
    return theme.fg("dim", format_key_text(key)) + theme.fg("muted", f" {description}")
