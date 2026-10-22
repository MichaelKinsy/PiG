"""pi-tui's terminal-image.ts capability detection and ``hyperlink``."""

from __future__ import annotations

import os
import subprocess  # nosec B404 - only runs the fixed tmux capability probe below
import sys
from collections.abc import Callable, Mapping
from dataclasses import dataclass, replace
from typing import Literal, Optional

from .. import _tui_state
from ._colors import TerminalColorMode
from ._fuzzy import js_lower

ImageProtocol = Optional[Literal["kitty", "iterm2"]]


@dataclass(frozen=True)
class TerminalCapabilities:
    """pi-tui's ``TerminalCapabilities``."""

    images: ImageProtocol
    true_color: bool
    hyperlinks: bool


@dataclass(frozen=True)
class CapabilityOverrides:
    """pi-tui's ``Partial<TerminalCapabilities>``; ``None`` is absent. ``images="none"`` is upstream's ``null``."""

    images: Literal["kitty", "iterm2", "none"] | None = None
    true_color: bool | None = None
    hyperlinks: bool | None = None


def hyperlink(text: str, url: str) -> str:
    """``text`` wrapped in an OSC 8 link to ``url`` (pi-tui's ``hyperlink``)."""
    return f"\x1b]8;;{url}\x1b\\{text}\x1b]8;;\x1b\\"


def _probe_tmux_hyperlinks() -> bool:
    try:
        out = subprocess.run(  # nosec B603 B607 - fixed argv, no input; pi-tui runs tmux from PATH the same way
            ["tmux", "display-message", "-p", "#{client_termfeatures}"],
            capture_output=True, text=True, timeout=0.25, stdin=subprocess.DEVNULL, check=True,
        ).stdout  # fmt: skip
    except (OSError, subprocess.SubprocessError):
        return False
    return "hyperlinks" in [f.strip() for f in out.split(",")]


def _detect_from_environment(env: Mapping[str, str], platform: str, tmux_forwards_hyperlink: Callable[[], bool]) -> TerminalCapabilities:
    def get(key: str) -> str:
        return env.get(key) or ""

    term_program = js_lower(get("TERM_PROGRAM"))
    terminal_emulator = js_lower(get("TERMINAL_EMULATOR"))
    term = js_lower(get("TERM"))
    color_term = js_lower(get("COLORTERM"))
    true_color_hint = color_term in ("truecolor", "24bit") or term.endswith("-direct")
    if get("TMUX") or term.startswith("tmux"):
        return TerminalCapabilities(None, true_color_hint, tmux_forwards_hyperlink())
    if term.startswith("screen"):
        return TerminalCapabilities(None, true_color_hint, False)
    if get("KITTY_WINDOW_ID") or term_program == "kitty":
        return TerminalCapabilities("kitty", True, True)
    if term_program == "ghostty" or "ghostty" in term or get("GHOSTTY_RESOURCES_DIR"):
        return TerminalCapabilities("kitty", True, True)
    if get("WEZTERM_PANE") or term_program == "wezterm":
        return TerminalCapabilities("kitty", True, True)
    if term_program == "warpterminal" or get("WARP_SESSION_ID") or get("WARP_TERMINAL_SESSION_UUID"):
        return TerminalCapabilities("kitty", True, True)
    if get("ITERM_SESSION_ID") or term_program == "iterm.app":
        return TerminalCapabilities("iterm2", True, True)
    if get("WT_SESSION"):
        return TerminalCapabilities(None, True, True)
    if term_program in ("alacritty", "vscode", "zed"):
        return TerminalCapabilities(None, True, True)
    if terminal_emulator == "jetbrains-jediterm":
        return TerminalCapabilities(None, True, False)
    if platform == "win32":
        return TerminalCapabilities(None, True, False)
    return TerminalCapabilities(None, true_color_hint, False)


def _boolean_override(value: str | None) -> bool | None:
    return True if value == "1" else False if value == "0" else None


def _detect(env: Mapping[str, str], platform: str, tmux_forwards_hyperlink: Callable[[], bool] | None) -> TerminalCapabilities:
    probe = tmux_forwards_hyperlink or _probe_tmux_hyperlinks
    hyperlinks = _boolean_override(env.get("PI_HYPERLINKS"))
    detected = _detect_from_environment(env, platform, probe if hyperlinks is None else (lambda: hyperlinks))
    protocol = js_lower(env["PI_IMAGE_PROTOCOL"]) if env.get("PI_IMAGE_PROTOCOL") is not None else None
    if protocol in ("kitty", "iterm2"):
        detected = replace(detected, images=protocol)
    elif protocol in ("none", "0"):
        detected = replace(detected, images=None)
    true_color = _boolean_override(env.get("PI_TRUE_COLOR"))
    if true_color is not None:
        detected = replace(detected, true_color=true_color)
    if hyperlinks is not None:
        detected = replace(detected, hyperlinks=hyperlinks)
    return detected


def detect_capabilities(tmux_forwards_hyperlink: Callable[[], bool] | None = None) -> TerminalCapabilities:
    """The terminal's capabilities from the environment (pi-tui's ``detectCapabilities``).

    ``PI_HYPERLINKS``, ``PI_IMAGE_PROTOCOL`` and ``PI_TRUE_COLOR`` override detection; without ``tmux_forwards_hyperlink`` a tmux session is probed.
    """
    return _detect(os.environ, sys.platform, tmux_forwards_hyperlink)


_cached: TerminalCapabilities | None = None
_overrides = CapabilityOverrides()


def _apply_overrides(caps: TerminalCapabilities, overrides: CapabilityOverrides) -> TerminalCapabilities:
    if overrides.images is not None:
        caps = replace(caps, images=None if overrides.images == "none" else overrides.images)
    if overrides.true_color is not None:
        caps = replace(caps, true_color=overrides.true_color)
    if overrides.hyperlinks is not None:
        caps = replace(caps, hyperlinks=overrides.hyperlinks)
    return caps


def get_capabilities() -> TerminalCapabilities:
    """The detected capabilities with the overrides on top, cached (pi-tui's ``getCapabilities``)."""
    global _cached
    published = _tui_state.take_capabilities()
    if published is not None:
        _cached = TerminalCapabilities(published.get("images") or None, published.get("trueColor") is True, published.get("hyperlinks") is True)
    if _cached is None:
        hyperlinks = _overrides.hyperlinks
        _cached = _apply_overrides(detect_capabilities(None if hyperlinks is None else (lambda: hyperlinks)), _overrides)
    return _cached


def get_terminal_color_mode(capabilities: TerminalCapabilities | None = None) -> TerminalColorMode:
    """``"truecolor"`` when the capabilities report it, else ``"256color"`` (pi-tui's ``getTerminalColorMode``)."""
    caps = capabilities if capabilities is not None else get_capabilities()
    return "truecolor" if caps.true_color else "256color"


def reset_capabilities_cache() -> None:
    global _cached
    _cached = None


def set_capability_overrides(overrides: CapabilityOverrides) -> None:
    """Replace the overrides, dropping the cache only when one changed (pi-tui's ``setCapabilityOverrides``)."""
    global _overrides, _cached
    if overrides == _overrides:
        return
    _overrides = overrides
    _cached = None


def set_capabilities(capabilities: TerminalCapabilities) -> None:
    """Replace the cached capabilities (pi-tui's ``setCapabilities``)."""
    global _cached
    _cached = capabilities
