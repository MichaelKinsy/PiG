"""Pi's ``DefaultPackageManager`` class (core/package-manager.ts): it installs, removes, resolves and lists the packages named in the user and project settings of the SettingsManager it was given. The host owns the manager. Results are Pi's JSON shapes as dicts and lists."""
from __future__ import annotations

from typing import Any, Callable

from ._graph import _GraphObject, _callback_id


class DefaultPackageManager(_GraphObject):
    def __init__(self, context: Any, *, cwd: str, agent_dir: str, settings_manager: Any, builtin_extensions: list[str] | None = None) -> None:
        """``new DefaultPackageManager(options)``; ``builtin_extensions`` are the names of built-in extensions resolved as ``builtin:<name>``."""
        super().__init__(context, "DefaultPackageManager", "new", {"cwd": cwd, "agentDir": agent_dir, "settingsManager": settings_manager.handle, "builtinExtensions": builtin_extensions})

    def set_progress_callback(self, callback: Callable[[dict[str, Any]], None] | None) -> None:
        """The function every later install, remove and update reports ``{"type", "action", "source", "message"?}`` events to; ``None`` clears it."""
        self._invoke("setProgressCallback", _callback_id(self._context, None if callback is None else (lambda event: callback(event))))

    def add_source_to_settings(self, source: str, local: bool = False) -> bool:
        return self._invoke("addSourceToSettings", source, {"local": local})

    def remove_source_from_settings(self, source: str, local: bool = False) -> bool:
        return self._invoke("removeSourceFromSettings", source, {"local": local})

    def get_installed_path(self, source: str, scope: str) -> str | None:
        """Where the package ``source`` is installed in ``scope`` (``"user"`` or ``"project"``), ``None`` when it is not."""
        return self._invoke("getInstalledPath", source, scope)

    def resolve(self, on_missing: Callable[[str], str] | None = None) -> dict[str, Any]:
        """Resolve every configured package into ``{"extensions", "skills", "prompts", "themes"}``; ``on_missing`` answers ``"install"``, ``"skip"`` or ``"error"`` for a package that is not installed."""
        return self._invoke_async("resolve", _callback_id(self._context, on_missing or (lambda source: "install")))

    def resolve_extension_sources(self, sources: list[str], local: bool = False, temporary: bool = False) -> dict[str, Any]:
        return self._invoke_async("resolveExtensionSources", sources, {"local": local, "temporary": temporary})

    def list_configured_packages(self) -> list[dict[str, Any]]:
        return self._invoke("listConfiguredPackages")

    def install(self, source: str, local: bool = False) -> None:
        self._invoke_async("install", source, {"local": local})

    def install_and_persist(self, source: str, local: bool = False) -> None:
        self._invoke_async("installAndPersist", source, {"local": local})

    def remove(self, source: str, local: bool = False) -> None:
        self._invoke_async("remove", source, {"local": local})

    def remove_and_persist(self, source: str, local: bool = False) -> bool:
        return self._invoke_async("removeAndPersist", source, {"local": local})

    def update(self, source: str | None = None) -> None:
        self._invoke_async("update", source)

    def check_for_available_updates(self) -> list[dict[str, Any]]:
        return self._invoke_async("checkForAvailableUpdates")
