"""pi-tui's keybindings.ts: the effective keys of every keybinding.

``KeybindingsManager`` resolves each keybinding id to the user's override or the definition's default keys, matches raw terminal input
against them and reports conflicts between user overrides. The SDK installs the host's effective table (every ``tui.*`` and ``app.*``
definition with the user's ``keybindings.json`` overrides) as the global manager when the host publishes it, as Pi's startup does.
"""

from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass, field
from typing import Any, Union

from .. import _tui_state
from ._keys import matches_key
from ._tui_keybindings import TUI_KEYBINDING_DEFINITIONS

Keybinding = str
KeyIds = Union[str, "list[str]"]


@dataclass(frozen=True)
class KeybindingDefinition:
    """pi-tui's ``KeybindingDefinition``: the default keys (one key or a list) and the description."""

    default_keys: KeyIds
    description: str | None = None


@dataclass(frozen=True)
class KeybindingConflict:
    """pi-tui's ``KeybindingConflict``: a key claimed by more than one keybinding."""

    key: str
    keybindings: list[str] = field(default_factory=list)


TUI_KEYBINDINGS: dict[str, KeybindingDefinition] = {
    id: KeybindingDefinition(list(keys), description) for id, (keys, description) in TUI_KEYBINDING_DEFINITIONS.items()
}


def _normalize_keys(keys: KeyIds | None) -> list[str]:
    if keys is None:
        return []
    key_list = [keys] if isinstance(keys, str) else keys
    seen: set[str] = set()
    result: list[str] = []
    for key in key_list:
        if key not in seen:
            seen.add(key)
            result.append(key)
    return result


def _definition(value: Any) -> KeybindingDefinition:
    if isinstance(value, KeybindingDefinition):
        return value
    return KeybindingDefinition(value.get("defaultKeys", []), value.get("description"))


class KeybindingsManager:
    """pi-tui's ``KeybindingsManager``."""

    def __init__(self, definitions: Mapping[str, Any], user_bindings: Mapping[str, KeyIds | None] | None = None) -> None:
        self._definitions = {id: _definition(d) for id, d in definitions.items()}
        self._user_bindings: dict[str, KeyIds | None] = dict(user_bindings or {})
        self._keys_by_id: dict[str, list[str]] = {}
        self._conflicts: list[KeybindingConflict] = []
        self._rebuild()

    @classmethod
    def from_wire(cls, table: Mapping[str, Any]) -> KeybindingsManager:
        """The manager of the host's published table: ``{"definitions": {...}, "userBindings": {...}}``."""
        return cls(table.get("definitions") or {}, table.get("userBindings") or {})

    def _rebuild(self) -> None:
        self._keys_by_id = {}
        self._conflicts = []
        claims: dict[str, list[str]] = {}
        for keybinding, keys in self._user_bindings.items():
            if keybinding not in self._definitions:
                continue
            for key in _normalize_keys(keys):
                claimants = claims.setdefault(key, [])
                if keybinding not in claimants:
                    claimants.append(keybinding)
        for key, keybindings in claims.items():
            if len(keybindings) > 1:
                self._conflicts.append(KeybindingConflict(key, list(keybindings)))
        for id, definition in self._definitions.items():
            user_keys = self._user_bindings.get(id)
            self._keys_by_id[id] = _normalize_keys(definition.default_keys if user_keys is None else user_keys)

    def matches(self, data: str, keybinding: Keybinding) -> bool:
        return any(matches_key(data, key) for key in self._keys_by_id.get(keybinding, []))

    def get_keys(self, keybinding: Keybinding) -> list[str]:
        return list(self._keys_by_id.get(keybinding, []))

    def get_definition(self, keybinding: Keybinding) -> KeybindingDefinition | None:
        return self._definitions.get(keybinding)

    def get_conflicts(self) -> list[KeybindingConflict]:
        return [KeybindingConflict(c.key, list(c.keybindings)) for c in self._conflicts]

    def set_user_bindings(self, user_bindings: Mapping[str, KeyIds | None]) -> None:
        self._user_bindings = dict(user_bindings)
        self._rebuild()

    def get_user_bindings(self) -> dict[str, KeyIds | None]:
        return dict(self._user_bindings)

    def get_resolved_bindings(self) -> dict[str, KeyIds]:
        """Every id to its resolved keys: a lone key as a string, several (or none) as a list, as Pi gives them."""
        resolved: dict[str, KeyIds] = {}
        for id in self._definitions:
            keys = self._keys_by_id.get(id, [])
            resolved[id] = keys[0] if len(keys) == 1 else list(keys)
        return resolved


_global_keybindings: KeybindingsManager | None = None


def set_keybindings(keybindings: KeybindingsManager) -> None:
    """Install the global manager (pi-tui's ``setKeybindings``)."""
    global _global_keybindings
    _tui_state.take_keybindings()  # an explicit install supersedes what the host published before it
    _global_keybindings = keybindings


def get_keybindings() -> KeybindingsManager:
    """The global manager, over :data:`TUI_KEYBINDINGS` until one is installed (pi-tui's ``getKeybindings``).

    The host's effective table (every ``tui.*`` and ``app.*`` definition with the user's ``keybindings.json`` overrides) is installed
    as the global manager when the host publishes it, as Pi's startup does with ``setKeybindings``.
    """
    global _global_keybindings
    published = _tui_state.take_keybindings()
    if published is not None:
        _global_keybindings = KeybindingsManager.from_wire(published)
    if _global_keybindings is None:
        _global_keybindings = KeybindingsManager(TUI_KEYBINDINGS)
    return _global_keybindings
