"""The function options of Pi's ``createMcpExtension`` (extensions/mcp/index.ts ``McpExtensionOptions``): ``loadConfig``, ``createTransport``, ``credentials``, ``openUrl`` and ``updateConfig``.

They stay in this process. The reference :func:`create_mcp_extension` returns names them to the host as session graph callbacks when :class:`DefaultResourceLoader` is built, and the host runs them with JSON arguments over the connection (the wire of the loader's override options; no request kind is added). Entries, configurations, tokens and messages are Pi's JSON shapes as dicts.
"""
from __future__ import annotations

import threading
from typing import Any, Callable, Protocol

from ._graph import _callback_id, _drop_callback


class McpAuthProvider:
    """Pi's ``AuthProvider`` of an HTTP server, held by the host for a transport: it supplies bearer tokens and refreshes them after a rejection. Its methods call the host."""

    def __init__(self, context: Any, handle: str) -> None:
        self._context = context
        self._handle = handle

    def token(self) -> str | None:
        """``token()``: the current access token, ``None`` for none."""
        return self._context._call("sessionGraph.call", {"handle": self._handle, "method": "token", "args": []}).get("result")

    def on_unauthorized(self, status: int, headers: list[tuple[str, str]], server_url: str, token: str | None = None) -> None:
        """``onUnauthorized(context)``: the host refreshes the credentials after the response (its ``status`` and ordered ``headers``; Pi's ``fetch`` and the body stay in the host) a request of the transport got; the transport retries the request once with what the provider holds then."""
        wire: dict[str, Any] = {"status": status, "headers": [list(header) for header in headers], "serverUrl": server_url}
        if token:
            wire["token"] = token
        self._context._call("sessionGraph.call", {"handle": self._handle, "method": "onUnauthorized", "args": [wire]})


class McpTransportEvents:
    """Pi's ``TransportEvents``: the listener bookkeeping a transport inherits. ``emit_*`` call the listeners in registration order on the caller's thread; ``emit_close`` fires at most once."""

    def __init__(self) -> None:
        self._listener_lock = threading.Lock()
        self._listeners: dict[str, dict[int, Callable[..., None]]] = {"message": {}, "error": {}, "close": {}}
        self._listener_next = 0
        self._close_emitted = False

    def _add(self, kind: str, listener: Callable[..., None]) -> Callable[[], None]:
        with self._listener_lock:
            self._listener_next += 1
            key = self._listener_next
            self._listeners[kind][key] = listener

        def dispose() -> None:
            with self._listener_lock:
                self._listeners[kind].pop(key, None)

        return dispose

    def _snapshot(self, kind: str) -> list[Callable[..., None]]:
        with self._listener_lock:
            return [self._listeners[kind][key] for key in sorted(self._listeners[kind])]

    def on_message(self, listener: Callable[[dict[str, Any]], None]) -> Callable[[], None]:
        """Registers a listener for each JSON-RPC message the transport receives; returns the function that removes it."""
        return self._add("message", listener)

    def on_error(self, listener: Callable[[Exception], None]) -> Callable[[], None]:
        """Registers a listener for errors that do not close the transport."""
        return self._add("error", listener)

    def on_close(self, listener: Callable[[], None]) -> Callable[[], None]:
        """Registers a listener for the close, which the transport reports once."""
        return self._add("close", listener)

    def emit_message(self, message: dict[str, Any]) -> None:
        for listener in self._snapshot("message"):
            listener(message)

    def emit_error(self, error: Exception) -> None:
        for listener in self._snapshot("error"):
            listener(error)

    def emit_close(self) -> None:
        with self._listener_lock:
            first = not self._close_emitted
            self._close_emitted = True
        if first:
            for listener in self._snapshot("close"):
                listener()


class McpTransport(Protocol):
    """Pi's ``McpTransport`` (packages/mcp/src/transports/transport.ts): the duplex message stream of one MCP connection. The host's client calls ``start``, ``send`` and ``close`` from the host; the SDK delivers them to the transport in this process, registers its listeners, and delivers what the transport emits back, in order.

    ``send(message, signal)`` writes one JSON-RPC message (a dict). It may wait for the peer's HTTP reply, so calls run concurrently; ``signal`` is a :class:`threading.Event` set once the sender no longer waits for a reply. A transport that also has ``send_ordered(message, signal, placed)`` writes in the order the writes took their place, as a stdio pipe does: it calls ``placed()`` once every later send is written after ``message``; one without it gets ``placed`` called just before its ``send``. A transport with ``set_protocol_version(version)`` (Pi's optional ``setProtocolVersion``) is told the negotiated version."""

    def start(self) -> None: ...
    def send(self, message: dict[str, Any], signal: threading.Event) -> None: ...
    def close(self) -> None: ...
    def on_message(self, listener: Callable[[dict[str, Any]], None]) -> Callable[[], None]: ...
    def on_error(self, listener: Callable[[Exception], None]) -> Callable[[], None]: ...
    def on_close(self, listener: Callable[[], None]) -> Callable[[], None]: ...


class McpOAuthServerStore(Protocol):
    """Pi's ``McpOAuthStateStore`` plus the refresh lock of one server: ``load()`` returns the stored state dict or ``None``, ``save(state)`` stores it, ``with_refresh_lock(fn)`` runs ``fn`` while no other process refreshes the server's tokens and returns its result."""

    def load(self) -> dict[str, Any] | None: ...
    def save(self, state: dict[str, Any]) -> None: ...
    def with_refresh_lock(self, fn: Callable[[], Any]) -> Any: ...


class McpOAuthCredentialStore(Protocol):
    """What the MCP extension asks of its ``credentials`` option (Pi's ``McpOAuthCredentialStore``: per-server OAuth state in ``mcp-auth.json`` by default): ``for_server(name, server_url)`` returns the server's store, ``tokens(name, server_url)`` its stored tokens dict or ``None``, ``remove(name, server_url)`` deletes the stored state and returns whether credentials were stored."""

    def for_server(self, name: str, server_url: str) -> McpOAuthServerStore: ...
    def tokens(self, name: str, server_url: str) -> dict[str, Any] | None: ...
    def remove(self, name: str, server_url: str) -> bool: ...


class _TransportShim:
    """Serves one transport of the caller to the host: the host's ops arrive as one session graph callback, and what the transport produces is queued for the host's long poll (``receive``)."""

    def __init__(self, context: Any, transport: Any) -> None:
        self._context = context
        self._transport = transport
        self._cond = threading.Condition()
        self._events: list[dict[str, Any]] = []
        self._closed = False
        self._aborts: dict[int, threading.Event] = {}
        self.id = _callback_id(context, self.serve)
        transport.on_message(lambda message: self._push({"type": "message", "message": message}))
        transport.on_error(lambda error: self._push({"type": "error", "error": str(error)}))
        transport.on_close(self._push_close)

    def _push(self, event: dict[str, Any]) -> None:
        with self._cond:
            if not self._closed:
                self._events.append(event)
            self._cond.notify_all()

    def _push_close(self) -> None:
        with self._cond:
            if not self._closed:
                self._events.append({"type": "close"})
                self._closed = True
            self._cond.notify_all()

    def serve(self, op: str, *args: Any) -> Any:
        if op == "start":
            self._transport.start()
            return None
        if op == "setProtocolVersion":
            setter = getattr(self._transport, "set_protocol_version", None)
            if setter is not None:
                setter(args[0])
            return None
        if op == "receive":
            return self._receive()
        if op == "send":
            return self._send(int(args[0]), args[1], bool(args[2]))
        if op == "abort":
            with self._cond:
                signal = self._aborts.get(int(args[0]))
            if signal is not None:
                signal.set()
            return None
        if op == "close":
            try:
                self._transport.close()
            finally:
                self._push_close()
                # The host closed the transport and waits for its poll to see the close; the id is not used again.
                _drop_callback(self._context, self.id)
            return None
        raise RuntimeError(f"an MCP transport has no operation {op}")

    def _receive(self) -> dict[str, Any]:
        with self._cond:
            while not self._events:
                if self._closed:
                    return {"events": [{"type": "close"}]}
                self._cond.wait()
            events, self._events = self._events, []
        return {"events": events}

    def _send(self, seq: int, message: dict[str, Any], ordered: bool) -> None:
        signal = threading.Event()
        with self._cond:
            self._aborts[seq] = signal
        once = threading.Lock()
        placed_done = [False]

        def placed() -> None:
            with once:
                if placed_done[0]:
                    return
                placed_done[0] = True
            if ordered:
                self._push({"type": "placed", "seq": seq})

        try:
            send_ordered = getattr(self._transport, "send_ordered", None)
            if send_ordered is not None and ordered:
                send_ordered(message, signal, placed)
            else:
                placed()
                self._transport.send(message, signal)
        finally:
            # A transport that never calls placed must not hold up the sends after this one.
            placed()
            with self._cond:
                self._aborts.pop(seq, None)
            signal.set()
        return None


class _CredentialsShim:
    """Serves the caller's credential store: the host's ops arrive as one callback."""

    def __init__(self, store: Any) -> None:
        self._store = store
        self._lock = threading.Lock()
        self._next = 0
        self._stores: dict[str, Any] = {}
        self._locks: dict[str, tuple[threading.Event, dict[str, Any], threading.Event, threading.Event]] = {}

    def _id(self, prefix: str) -> str:
        with self._lock:
            self._next += 1
            return f"{prefix}{self._next}"

    def serve(self, op: str, *args: Any) -> Any:
        if op == "forServer":
            store = self._store.for_server(args[0], args[1])
            if store is None:
                raise RuntimeError("for_server returned no store")
            store_id = self._id("s")
            with self._lock:
                self._stores[store_id] = store
            return {"store": store_id}
        if op == "tokens":
            return self._store.tokens(args[0], args[1])
        if op == "remove":
            return bool(self._store.remove(args[0], args[1]))
        with self._lock:
            store = self._stores.get(args[0])
            if op == "release":
                self._stores.pop(args[0], None)
        if op == "release":
            return None
        if store is None:
            raise RuntimeError(f"unknown credential store {args[0]}")
        if op == "load":
            return store.load()
        if op == "save":
            store.save(args[1])
            return None
        if op == "lock":
            return self._acquire(args[0], store)
        if op == "unlock":
            return self._release(args[1], args[2] if len(args) > 2 else "", bool(args[3]) if len(args) > 3 else False)
        raise RuntimeError(f"a credential store has no operation {op}")

    def _acquire(self, store_id: str, store: Any) -> dict[str, Any]:
        entered, unlock, done = threading.Event(), threading.Event(), threading.Event()
        outcome: dict[str, Any] = {"failure": None, "error": None}

        def held() -> Any:
            entered.set()
            unlock.wait()
            if outcome["failure"] is not None:
                raise RuntimeError(outcome["failure"])
            return None

        def run() -> None:
            try:
                store.with_refresh_lock(held)
            except BaseException as error:  # noqa: BLE001 - the error is the answer of the host's unlock, or of its lock when the store failed first
                outcome["error"] = error
            finally:
                done.set()

        threading.Thread(target=run, daemon=True).start()
        while not entered.is_set() and not done.is_set():
            entered.wait(0.005)
        if not entered.is_set():
            # The store's with_refresh_lock returned without running the function: it could not lock.
            raise outcome["error"] or RuntimeError("with_refresh_lock returned without running its function")
        lock_id = self._id(f"{store_id}.")
        with self._lock:
            self._locks[lock_id] = (entered, outcome, unlock, done)
        return {"lock": lock_id}

    def _release(self, lock_id: str, failure: str, failed: bool) -> None:
        with self._lock:
            held = self._locks.pop(lock_id, None)
        if held is None:
            raise RuntimeError(f"unknown refresh lock {lock_id}")
        _, outcome, unlock, done = held
        outcome["failure"] = failure if failed else None
        unlock.set()
        done.wait()
        if outcome["error"] is not None:
            raise outcome["error"]
        return None


def _register(context: Any, functions: dict[str, Any], wire: dict[str, Any]) -> None:
    """Names each function to the host in ``wire``, the option object of the reference."""
    load_config = functions.get("load_config")
    if load_config is not None:
        wire["loadConfig"] = _callback_id(context, lambda ctx: load_config(ctx))
    open_url = functions.get("open_url")
    if open_url is not None:
        wire["openUrl"] = _callback_id(context, lambda url: open_url(url))
    update_config = functions.get("update_config")
    if update_config is not None:
        wire["updateConfig"] = _callback_id(context, lambda entry, patch: update_config(entry, patch))
    create_transport = functions.get("create_transport")
    if create_transport is not None:

        def create(entry: dict[str, Any], cwd: str, auth: str) -> dict[str, Any]:
            provider = McpAuthProvider(context, auth) if auth else None
            transport = create_transport(entry, cwd, provider)
            if transport is None:
                raise RuntimeError("create_transport returned no transport")
            shim = _TransportShim(context, transport)
            return {"transport": shim.id, "setsProtocolVersion": hasattr(transport, "set_protocol_version")}

        wire["createTransport"] = _callback_id(context, create)
    credentials = functions.get("credentials")
    if credentials is not None:
        wire["credentials"] = _callback_id(context, _CredentialsShim(credentials).serve)
