"""Upstream's ``pi.events`` (packages/coding-agent/src/core/event-bus.ts:12-33), bridged by value.

Every realm of a session shares one ordered listener registry, held by the
host. A Python listener sits in it in registration order between the listeners
of the Node realms, and a payload crosses as JSON.
"""

from __future__ import annotations

import itertools
import json
import sys
import threading
from collections.abc import Callable
from dataclasses import dataclass
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from . import Context, Extension

EventBusHandler = Callable[["Context", Any], None]


class EventBus:
    """``pi.events``: ``on(channel, handler)`` and ``emit(channel, data)``.

    ``on`` returns an idempotent unsubscribe function. A handler runs in a
    request of its own, so it may call ``emit`` while the host waits for its
    answer (EventEmitter is synchronous and reentrant). A handler's error fails
    only that delivery: the host reports it as ``Event handler error`` and the
    emitter and the other listeners are unaffected (event-bus.ts:24-30).
    """

    def __init__(self, extension: Extension, ctx: Context | None = None):
        self._extension = extension
        self._ctx = ctx

    def on(self, channel: str, handler: EventBusHandler) -> Callable[[], None]:
        """Subscribe ``handler(ctx, data)`` to ``channel`` and return the unsubscribe function.

        A subscription made while the factory runs is registered with the host
        before the extension is, as a Node factory's ``pi.events.on`` is
        (loader.ts:501-509).
        """
        return self._extension._bus.on(channel, handler, self._ctx)

    def emit(self, channel: str, data: Any = None) -> None:
        """Send ``data`` to every listener of ``channel`` and return after each listener's work.

        Raises ``TypeError`` for a payload without a JSON form, and
        ``RuntimeError`` for an emit on ``"error"`` that no listener handles,
        as EventEmitter throws (event-bus.ts:15-22).

        Called while the factory runs (through ``Extension.events``, before the
        extension serves), the emit is queued and delivered once the extension is loaded,
        before ``on_loaded`` handlers and any request; Pi delivers it at once
        to the listeners of the extensions loaded before this one and to the
        factory's own, and the host does the same at that point. It returns
        after queueing, and a failure of the delivery is reported on stderr. Use ``Extension.on_loaded`` for work that needs the live
        session.
        """
        self._extension._bus.emit(channel, data, self._ctx)


# Process-wide, like the Host's realm: the Host keys a listener by (OS process, handler ID), and a packed cell runs several extensions in one process (extensions/sdk/event_bus.go busHandlerSeq).
_handler_seq = itertools.count(1)
_handler_seq_lock = threading.Lock()


def _next_handler_id() -> str:
    with _handler_seq_lock:
        return f"py-{next(_handler_seq)}"


@dataclass(eq=False)
class _Subscription:
    id: str
    channel: str
    handler: EventBusHandler
    # sent: the events.on call is made or under way; off: unsubscribed. Both are guarded by _EventBusHub._lock.
    sent: bool = False
    off: bool = False


class _EventBusHub:
    """One extension's listeners and the host calls that keep them in the host's registry."""

    def __init__(self, extension: Extension) -> None:
        self._extension = extension
        self._lock = threading.Lock()
        # A listener stays here after unsubscribe until the host releases it: the host dispatches the snapshot it took before the off call, as EventEmitter clones its listener array for each emit.
        self._subs: dict[str, _Subscription] = {}
        # What the factory did before the extension connected, in the order it did it: a subscription (a _Subscription) is registered with the host and an emit (a tuple of channel and data) is delivered, each in turn, so a listener subscribed before an emit hears it, as with Pi's synchronous EventEmitter.
        self._pending: list[_Subscription | tuple[str, Any]] = []
        self._live = False
        self._factory_emits: list[tuple[str, Any]] = []

    def on(self, channel: str, handler: EventBusHandler, ctx: Context | None) -> Callable[[], None]:
        if not isinstance(channel, str):
            raise TypeError("event channel must be a string")
        if not callable(handler):
            raise TypeError("event handler must be callable")
        sub = _Subscription(_next_handler_id(), channel, handler)
        with self._lock:
            self._subs[sub.id] = sub
            if not self._live:
                self._pending.append(sub)
                return self._unsubscribe(sub)
            sub.sent = True
        try:
            self._call("events.on", {"channel": channel, "handlerId": sub.id, "value": True}, ctx)
        except BaseException:
            with self._lock:
                self._subs.pop(sub.id, None)
            raise
        return self._unsubscribe(sub)

    def _unsubscribe(self, sub: _Subscription) -> Callable[[], None]:
        removed = threading.Lock()

        def unsubscribe() -> None:
            if not removed.acquire(blocking=False):
                return
            with self._lock:
                sub.off = True
                sent = sub.sent
                if not sent:
                    self._subs.pop(sub.id, None)
                    if any(op is sub for op in self._pending):
                        self._pending = [op for op in self._pending if op is not sub]
            if not sent:
                return
            # EventEmitter.off never throws (event-bus.ts:27); a failed off is reported, as the Go SDK reports it.
            try:
                self._extension._call("events.off", {"handlerId": sub.id})
            except Exception as exc:  # noqa: BLE001 - upstream's unsubscribe cannot fail; report it
                print(f"extension: pi.events unsubscribe {sub.channel!r}: {exc}", file=sys.stderr)

        return unsubscribe

    def emit(self, channel: str, data: Any, ctx: Context | None) -> None:
        if not isinstance(channel, str):
            raise TypeError("event channel must be a string")
        try:
            json.dumps(data, allow_nan=False)
        except (TypeError, ValueError) as exc:
            raise TypeError("event payload must be JSON-serializable") from exc
        with self._lock:
            if not self._live:
                # Called while the factory runs: delivered when the extension connects, in order with the subscriptions made before it and before the extension registers, as a node factory's pi.events.emit reaches the listeners of the extensions loaded before it. A failure of the delivery fails the load.
                self._pending.append((channel, data))
                return
        reply = self._call("events.emit", {"channel": channel, "json": data, "value": True}, ctx)
        if (reply.get("result") or {}).get("unhandledError"):
            raise RuntimeError(f"pi.events.emit {channel!r}: unhandled error event: {json.dumps(data)}")

    def dispatch(self, ctx: Context, args: dict[str, Any]) -> None:
        """Run one listener for the host's events.dispatch request. The caller answers after this returns."""
        handler_id = str(args.get("handlerId") or "")
        with self._lock:
            sub = self._subs.get(handler_id)
        if sub is None or sub.channel != args.get("channel"):
            raise RuntimeError(f"Unknown event bus handler: {handler_id}")
        sub.handler(ctx, args.get("json"))

    def release(self, handler_id: str) -> None:
        """Drop an unsubscribed listener once the host says no snapshot can name it (the events.release notify). A listener that is still subscribed stays."""
        with self._lock:
            sub = self._subs.get(handler_id)
            if sub is not None and sub.off:
                del self._subs[handler_id]

    def register_pending(self) -> None:
        """Send the factory's subscriptions, in subscription order, before the register frame, so the host has them when it registers the extension. Later subscriptions register at once."""
        with self._lock:
            pending, self._pending = self._pending, []
            self._live = True
            for op in pending:
                if isinstance(op, _Subscription):
                    op.sent = True
        for op in pending:
            if not isinstance(op, _Subscription):
                # The host dispatches an emit to this extension's own listeners too, and the extension serves a dispatch only once it has registered: flush_factory_emits sends it then.
                self._factory_emits.append(op)
                continue
            with self._lock:
                off = op.off
            if off:
                continue
            self._extension._load_call("events.on", {"channel": op.channel, "handlerId": op.id, "value": True})

    def has_factory_emits(self) -> bool:
        return bool(self._factory_emits)

    def flush_factory_emits(self) -> None:
        """Deliver the emits the factory made before the extension connected, in the order it made them, once the extension serves requests. A failed delivery, such as an emit on ``"error"`` that no listener handles, is reported on stderr: the extension is already loaded."""
        emits, self._factory_emits = self._factory_emits, []
        for channel, data in emits:
            try:
                reply = self._extension._call("events.emit", {"channel": channel, "json": data, "value": True})
                if (reply.get("result") or {}).get("unhandledError"):
                    raise RuntimeError(f"unhandled error event: {json.dumps(data)}")
            except Exception as exc:  # noqa: BLE001 - reported, as Go's flush reports it
                print(f"extension: pi.events.emit {channel!r} from the factory: {exc}", file=sys.stderr)

    def _call(self, method: str, args: dict[str, Any], ctx: Context | None) -> dict[str, Any]:
        return ctx._call(method, args) if ctx is not None else self._extension._call(method, args)
