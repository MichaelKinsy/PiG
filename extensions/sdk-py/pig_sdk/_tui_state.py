"""The host's replicated pi-tui state, held until an extension uses the ``pig_sdk.tui`` helpers.

The host publishes its keybinding table and terminal capabilities with every state snapshot. Holding them here, instead of building the
pi-tui managers when the snapshot arrives, keeps the width tables and key parsers out of an extension that never imports them: the
``pig_sdk.tui`` helpers (``get_keybindings``, ``get_capabilities``) take what is pending when first used and on every change.
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
