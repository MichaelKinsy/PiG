"""Complete autocomplete provider factories over the shared host contract."""
from __future__ import annotations

import threading
import uuid
import weakref
from typing import Any, Callable, Protocol, TYPE_CHECKING

if TYPE_CHECKING:
    from . import Context


class AutocompleteProvider(Protocol):
    """A retained provider instance. Cursor columns are UTF-16 code units."""
    trigger_characters: list[str]

    def get_suggestions(self, ctx: Context, lines: list[str], cursor_line: int, cursor_col: int, force: bool = False) -> dict[str, Any] | None: ...

    def apply_completion(self, ctx: Context, lines: list[str], cursor_line: int, cursor_col: int, item: dict[str, Any], prefix: str) -> dict[str, Any]: ...


AutocompleteProviderFactory = Callable[["Context", AutocompleteProvider], AutocompleteProvider]


def _release_current(owner, provider_id):
    extension = owner()
    if extension is None or extension._shutdown.is_set():
        return
    try:
        extension._notify("ui.autocomplete.release", {"id": provider_id})
    except (OSError, RuntimeError):
        pass  # Connection close releases all captured providers.


class _CurrentProvider:
    def __init__(self, ctx, descriptor):
        weakref.finalize(self, _release_current, weakref.ref(ctx.extension), descriptor["id"])
        self._id = descriptor["id"]
        self.trigger_characters = descriptor.get("triggerCharacters") or []
        self.should_trigger_file_completion = None
        if descriptor.get("hasFileTrigger"):
            self.should_trigger_file_completion = lambda ctx, lines, line, col: self._invoke(ctx, "shouldTriggerFileCompletion", lines, line, col)

    def _invoke(self, ctx, operation, lines, line, col, **args):
        if ctx.is_cancelled():
            raise RuntimeError("autocomplete cancelled")
        return ctx._call("ui.autocomplete.invoke", {"id": self._id, "operation": operation, "lines": lines, "cursorLine": line, "cursorCol": col, **args}).get("result")

    def get_suggestions(self, ctx, lines, cursor_line, cursor_col, force=False):
        return self._invoke(ctx, "getSuggestions", lines, cursor_line, cursor_col, force=force)

    def apply_completion(self, ctx, lines, cursor_line, cursor_col, item, prefix):
        return self._invoke(ctx, "applyCompletion", lines, cursor_line, cursor_col, item=item, prefix=prefix)


class _AutocompleteRegistry:
    def __init__(self):
        self._lock = threading.RLock()
        self._factories = {}
        self._invoked = set()
        self._providers = {}

    def add(self, ctx, factory):
        if not ctx.has_ui():
            return
        if not callable(factory):
            raise TypeError("autocomplete factory is missing")
        key = uuid.uuid4().hex
        with self._lock:
            self._factories[key] = factory
        try:
            ctx._call("ui.addAutocompleteProvider", {"factoryId": key})
        finally:
            with self._lock:
                if key not in self._invoked:
                    self._factories.pop(key, None)

    def dispatch(self, ctx, args):
        operation = args["operation"]
        if operation == "wrap":
            with self._lock:
                factory = self._factories[args["factoryId"]]
                self._invoked.add(args["factoryId"])
            provider = factory(ctx, _CurrentProvider(ctx, args["current"]))
            if not callable(getattr(provider, "get_suggestions", None)) or not callable(getattr(provider, "apply_completion", None)):
                raise TypeError("autocomplete factory must return a provider")
            key = uuid.uuid4().hex
            with self._lock:
                self._providers[key] = provider
            return {"id": key, "triggerCharacters": getattr(provider, "trigger_characters", []), "hasFileTrigger": callable(getattr(provider, "should_trigger_file_completion", None))}
        with self._lock:
            provider = self._providers[args["id"]]
        values = (ctx, args["lines"], args["cursorLine"], args["cursorCol"])
        if operation == "getSuggestions":
            return provider.get_suggestions(*values, bool(args.get("force")))
        if operation == "applyCompletion":
            return provider.apply_completion(*values, args["item"], args["prefix"])
        if operation == "shouldTriggerFileCompletion":
            trigger = getattr(provider, "should_trigger_file_completion", None)
            return True if trigger is None else trigger(*values)
        raise ValueError(f"unknown autocomplete operation: {operation}")

    def release(self, provider_id):
        with self._lock:
            self._providers.pop(provider_id, None)

    def clear(self):
        with self._lock:
            self._factories.clear()
            self._invoked.clear()
            self._providers.clear()
