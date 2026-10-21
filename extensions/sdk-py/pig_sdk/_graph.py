"""The session graph: the classes Pi extensions construct around a session (SessionManager, SettingsManager, ModelRuntime, DefaultResourceLoader, DefaultPackageManager, ProjectTrustStore). The host owns each object for the extension's connection; the Python object holds the host's name for it. Every method is Pi's method under its snake_case name with Pi's positional parameters, and returns Pi's result in the JSON shape Pi's types have (an undefined result is None)."""
import threading
from typing import Any


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
            except Exception:
                pass

        try:
            threading.Thread(target=release, daemon=True).start()
        except Exception:
            pass
