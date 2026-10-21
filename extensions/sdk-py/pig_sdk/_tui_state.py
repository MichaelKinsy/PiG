"""The host's replicated pi-tui state, held until an extension uses the ``pig_sdk.tui`` helpers.

The host publishes its keybinding table and terminal capabilities with every state snapshot. Holding them here, instead of building the
pi-tui managers when the snapshot arrives, keeps the managers from being built for an extension that never uses them: the
``pig_sdk.tui`` helpers (``get_keybindings``, ``get_capabilities``) take what is pending when first used and on every change. The
modules of ``pig_sdk.tui`` are loaded by ``import pig_sdk`` like every other module of the package, so a staged directory evicted
under a running extension cannot break a later import of them.
"""

from __future__ import annotations

import threading
from typing import Any

_lock = threading.Lock()
_keybindings: dict[str, Any] | None = None
_capabilities: dict[str, Any] | None = None
_keybindings_dirty = False
_capabilities_dirty = False


def apply_host_state(state: dict[str, Any]) -> None:
    """Record the snapshot's keybinding table and terminal capabilities (the Node runtime's ``applyKeybindings`` and
    ``applyTerminalCapabilities``)."""
    global _keybindings, _capabilities, _keybindings_dirty, _capabilities_dirty
    table = state.get("keybindings")
    caps = state.get("terminalCapabilities")
    palette = state.get("theme")
    if isinstance(palette, dict):
        set_host_theme(palette)
    with _lock:
        if isinstance(table, dict) and isinstance(table.get("definitions"), dict) and table != _keybindings:
            _keybindings = table
            _keybindings_dirty = True
        if isinstance(caps, dict):
            _capabilities = caps
            _capabilities_dirty = True


def take_keybindings() -> dict[str, Any] | None:
    """The table published since the last call, or ``None``."""
    global _keybindings_dirty
    with _lock:
        if not _keybindings_dirty:
            return None
        _keybindings_dirty = False
        return _keybindings


def take_capabilities() -> dict[str, Any] | None:
    """The capabilities published since the last call, or ``None``."""
    global _capabilities_dirty
    with _lock:
        if not _capabilities_dirty:
            return None
        _capabilities_dirty = False
        return _capabilities


# The global theme. The host's active theme stands as Pi's own process-global theme: the pi-coding-agent helpers (key_hint, get_markdown_theme, ...) color with it.
# The host has one theme, so extensions sharing a process share it. ``init_theme`` installs another, which stands until the host's theme changes, as Pi's own
# theme change replaces it. Theme objects are built when first used, so an extension that never uses the helpers pays nothing for them.
_host_palette: dict[str, Any] | None = None
_installed_palette: dict[str, Any] | None = None
_theme_cache: tuple[dict[str, Any] | None, Any] | None = None


def set_host_theme(palette: Any) -> None:
    """Record the palette the host published. A palette that differs from the last one the host published ends what ``init_theme`` installed; the same
    palette, which every state snapshot repeats, leaves it. A value that is not a dict is the empty palette (the Node runtime's ``ThemeShim.setPalette``)."""
    global _host_palette, _installed_palette
    palette = palette if isinstance(palette, dict) else {}
    with _lock:
        if _host_palette is None or _host_palette != palette:
            if _host_palette is not None:
                _installed_palette = None
            _host_palette = palette


def install_theme(palette: dict[str, Any]) -> None:
    """Make the palette the global theme until the host's theme changes."""
    global _installed_palette
    with _lock:
        _installed_palette = palette


def installed_theme_palette() -> dict[str, Any] | None:
    """The palette of the theme ``init_theme`` installed, None while the global theme is the host's."""
    with _lock:
        return _installed_palette


def current_host_theme() -> Any:
    """The global theme: the one ``init_theme`` installed, else the theme the host last published, or the empty palette (modifiers only) before one arrives."""
    global _theme_cache
    with _lock:
        palette = _installed_palette if _installed_palette is not None else _host_palette
        if _theme_cache is not None and _theme_cache[0] is palette:
            return _theme_cache[1]
    from . import Theme

    theme = Theme()
    theme._set_palette(palette or {})
    with _lock:
        _theme_cache = (palette, theme)
    return theme
