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


# pig divergence (D213): how long a provider that blocks its thread may run, outside a call to the host, before the host is told that it awaits real I/O
# (the ui.autocomplete.pending notice, D213). Pi's microtask checkpoint has no duration; a blocking provider has no event loop to end it.
AUTOCOMPLETE_CHECKPOINT = 0.05


class _Settle:
    """One getSuggestions request being served: it awaits real I/O when it made no progress for a checkpoint, unfinished and outside a host call."""

    def __init__(self, announce):
        self._announce = announce
        self._lock = threading.Lock()
        self._host_calls = 0
        self._epoch = 0
        self._done = False
        self._announced = False
        self._timer = None

    def arm(self):
        with self._lock:
            if self._done or self._announced:
                return
            self._epoch += 1
            epoch = self._epoch
            if self._timer is not None:
                self._timer.cancel()
            self._timer = threading.Timer(AUTOCOMPLETE_CHECKPOINT, self._fire, args=(epoch,))
            self._timer.daemon = True
            self._timer.start()

    def _fire(self, epoch):
        with self._lock:
            announce = not self._done and not self._announced and self._host_calls == 0 and self._epoch == epoch
            if announce:
                self._announced = True
        if announce:
            self._announce()

    def host_call_started(self):
        with self._lock:
            self._host_calls += 1

    def host_call_ended(self):
        with self._lock:
            self._host_calls -= 1
        self.arm()

    def finish(self):
        with self._lock:
            self._done = True
            if self._timer is not None:
                self._timer.cancel()


AutocompleteProviderFactory = Callable[["Context", AutocompleteProvider], AutocompleteProvider]


def _release_current(owner, provider_id):
    extension = owner()
    if extension is None or extension._shutdown.is_set():
        return
    # A finalizer may run on a thread that holds the write lock; queue the release instead of sending it.
    extension._send_from_finalizer({"type": "notify", "notify": {"method": "ui.autocomplete.release", "args": {"id": provider_id}}})


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
        settle = ctx.extension._autocomplete.settle_of(ctx.request_id) if operation == "getSuggestions" else None
        if settle is not None:
            settle.host_call_started()
        try:
            return ctx._call("ui.autocomplete.invoke", {"id": self._id, "operation": operation, "lines": lines, "cursorLine": line, "cursorCol": col, **args}).get("result")
        finally:
            if settle is not None:
                settle.host_call_ended()

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
        self._suggests = {}

    def settle_of(self, request_id):
        if not request_id:
            return None
        with self._lock:
            return self._suggests.get(request_id)

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
            request_id = ctx.request_id
            if not request_id:
                return provider.get_suggestions(*values, bool(args.get("force")))
            settle = _Settle(lambda: ctx.extension._notify("ui.autocomplete.pending", {"requestId": request_id}))
            with self._lock:
                self._suggests[request_id] = settle
            settle.arm()
            try:
                return provider.get_suggestions(*values, bool(args.get("force")))
            finally:
                settle.finish()
                with self._lock:
                    self._suggests.pop(request_id, None)
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
