"""Pi's BashOperations for a user_bash handler (core/tools/bash.ts BashOperations, types.ts UserBashEventResult)."""
from __future__ import annotations

import base64
import threading
from dataclasses import dataclass
from typing import Any, Callable

from .provider import ProviderSignal


@dataclass
class BashExecOptions:
    """What operations.exec receives (core/tools/bash.ts): on_data gets the command's output as it arrives, in order; signal is set when the host aborts the command; timeout is seconds; env, when not None, replaces the inherited environment."""
    on_data: Callable[[bytes], None]
    signal: ProviderSignal | None = None
    timeout: float | None = None
    env: dict[str, str] | None = None


class _BashOperationsTable:
    """The BashOperations objects a user_bash reply named by handle, until the host releases them."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._objects: dict[str, Any] = {}
        self._seq = 0

    def register(self, operations: Any) -> str:
        with self._lock:
            self._seq += 1
            handle = f"bash-{self._seq}"
            self._objects[handle] = operations
            return handle

    def get(self, handle: str) -> Any:
        with self._lock:
            return self._objects.get(handle)

    def release(self, handle: str) -> None:
        with self._lock:
            self._objects.pop(handle, None)


def _exec_of(operations: Any) -> Callable[..., Any] | None:
    """A handler returns Pi's `{ operations }`: an object with an exec method, or a mapping with an "exec" callable."""
    candidate = operations.get("exec") if isinstance(operations, dict) else getattr(operations, "exec", None)
    return candidate if callable(candidate) else None


def _user_bash_operations_reply(table: _BashOperationsTable, value: Any) -> Any:
    """Keep a user_bash handler's operations object in the extension: the reply names it by handle and the host calls it with user_bash_exec."""
    if isinstance(value, dict) and value.get("result") is None and "operations" in value and _exec_of(value["operations"]) is not None:
        return {"operations": {"handle": table.register(value["operations"])}}
    return None


def _run_user_bash_exec(table: _BashOperationsTable, req: dict[str, Any], signal: ProviderSignal, notify: Callable[[bytes], None]) -> dict[str, Any]:
    """Run operations.exec for a user_bash_exec request and return Pi's `{ exitCode }`."""
    operations = table.get(str(req.get("tool") or ""))
    exec_ = _exec_of(operations) if operations is not None else None
    if exec_ is None:
        raise RuntimeError(f"unknown bash operations: {req.get('tool', '')}")
    args = req.get("args") or {}
    finished = threading.Event()

    def on_data(data: bytes | str) -> None:
        if not finished.is_set():
            notify(data.encode() if isinstance(data, str) else bytes(data))

    options = BashExecOptions(on_data=on_data, signal=signal, timeout=args.get("timeout"), env=args.get("env"))
    try:
        result = exec_(args.get("command", ""), args.get("cwd", ""), options)
    finally:
        finished.set()
    exit_code = result.get("exitCode") if isinstance(result, dict) else result
    return {"exitCode": exit_code}


def _data_update(data: bytes) -> dict[str, str]:
    return {"data": base64.b64encode(data).decode("ascii")}
