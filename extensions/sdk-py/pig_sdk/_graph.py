"""The session graph: the classes Pi extensions construct around a session (SessionManager, SettingsManager, ModelRuntime, DefaultResourceLoader, DefaultPackageManager, ProjectTrustStore). The host owns each object for the extension's connection; the Python object holds the host's name for it. Every method is Pi's method under its snake_case name with Pi's positional parameters, and returns Pi's result in the JSON shape Pi's types have (an undefined result is None)."""
from __future__ import annotations

import threading
from typing import Any


def _callback_id(context: Any, callback: Any) -> str | None:
    """Name ``callback`` for the host, which runs it with the JSON arguments of Pi's function. A function stays until the connection ends: the host may run it again on a later reload of the object it was passed to, including after the extension dropped the object. ``None`` is Pi's undefined option."""
    if callback is None:
        return None
    extension = context.extension
    with extension._state_lock:
        callbacks = extension.__dict__.setdefault("_graph_callbacks", {})
        callback_id = f"cb{len(callbacks) + 1}"
        callbacks[callback_id] = callback
    return callback_id


class _GraphObject:
    def __init__(self, context: Any, kind: str, op: str, *args: Any) -> None:
        reply = context._call("sessionGraph.new", {"kind": kind, "op": op, "args": list(args)}).get("result") or {}
        if not reply.get("handle"):
            raise RuntimeError("host returned no session graph handle")
        self._context = context
        self.handle: str = reply["handle"]
        self._released = False

    @staticmethod
    def _static(context: Any, kind: str, method: str, *args: Any) -> Any:
        return context._call("sessionGraph.call", {"kind": kind, "method": method, "args": list(args)}).get("result")

    @staticmethod
    def _static_async(context: Any, kind: str, method: str, *args: Any) -> Any:
        return context._call("sessionGraph.async", {"kind": kind, "method": method, "args": list(args)}).get("result")

    def _invoke(self, method: str, *args: Any) -> Any:
        return self._context._call("sessionGraph.call", {"handle": self.handle, "method": method, "args": list(args)}).get("result")

    def _invoke_async(self, method: str, *args: Any) -> Any:
        return self._context._call("sessionGraph.async", {"handle": self.handle, "method": method, "args": list(args)}).get("result")

    def __del__(self) -> None:
        # Pi frees an object when it is unreachable; the host keeps its reference until the connection closes or the object is released.
        context, handle = getattr(self, "_context", None), getattr(self, "handle", None)
        if context is None or handle is None or self._released:
            return

        def release() -> None:
            try:
                context._call("sessionGraph.release", {"handle": handle})
            except Exception:  # noqa: BLE001 - a release after the connection closed has nothing left to free  # nosec B110
                pass

        try:
            threading.Thread(target=release, daemon=True).start()
        except Exception:  # noqa: BLE001 - a finalizer at interpreter shutdown cannot start a thread  # nosec B110
            pass
