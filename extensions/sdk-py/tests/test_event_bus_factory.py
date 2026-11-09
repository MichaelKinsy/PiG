"""A listener a factory subscribes joins Pi's shared emitter at once (event-bus.ts:13-34), so it hears an emit another extension makes while the factory
runs, and a factory's emit is delivered by the host to every listener in order. A native SDK has no connection while its factory runs, so the first
factory-time bus call dials the host (as ``parse_frontmatter`` does), ``run`` takes that connection, and a factory-time call answers the host's
``events.dispatch`` requests on it while it waits.

The Go SDK's tests for the same contract are extensions/sdk/event_bus_factory_test.go."""

from __future__ import annotations

import os
import socket
import tempfile
import threading
from collections import defaultdict
from types import SimpleNamespace
from typing import Any

import pytest
from test_sdk import _read_frame_raw, _start_fake_host, _write_frame

import pig_sdk
from pig_sdk import host_helpers


class _FactoryHost:
    """A host that accepts one connection and answers every call on it with the next scripted result, recording what it was asked."""

    def __init__(self, monkeypatch: pytest.MonkeyPatch, results: list[Any]) -> None:
        self.dir = tempfile.mkdtemp()
        self.path = os.path.join(self.dir, "ext.sock")
        self.listener = _start_fake_host(self.path)
        self.calls: list[dict[str, Any]] = []
        self.results = list(results)
        self.accepted = 0
        self.conn: socket.socket | None = None
        monkeypatch.setenv("PIG_EXT_SOCKET", self.path)
        self.thread = threading.Thread(target=self._accept, daemon=True)
        self.thread.start()

    def _accept(self) -> None:
        try:
            self.conn, _ = self.listener.accept()
        except OSError:
            return
        self.accepted += 1
        self.conn.settimeout(15)

    def answer(self) -> dict[str, Any]:
        """Read the next call and answer it with the next scripted result."""
        self.thread.join(timeout=15)
        assert self.conn is not None, "the factory never connected"
        env = _read_frame_raw(self.conn)
        assert env["type"] == "call", env
        self.calls.append(env["call"])
        result = self.results.pop(0) if self.results else None
        _write_frame(self.conn, {"type": "call_result", "id": env["id"], "call_result": {"result": result}})
        return env["call"]

    def serve(self, count: int) -> threading.Thread:
        """Answer the next ``count`` calls on a thread, so the factory under test can block on them."""
        worker = threading.Thread(target=lambda: [self.answer() for _ in range(count)], daemon=True)
        worker.start()
        return worker

    def close(self) -> None:
        if self.conn is not None:
            self.conn.close()
        self.listener.close()
        host_helpers._State.dialled.clear()


@pytest.fixture
def host(monkeypatch: pytest.MonkeyPatch):
    made: list[_FactoryHost] = []

    def make(*results: Any) -> _FactoryHost:
        made.append(_FactoryHost(monkeypatch, list(results)))
        return made[-1]

    yield make
    for each in made:
        each.close()
    host_helpers._State.dialled.clear()


def _dispatch(fake: _FactoryHost, request_id: str, handler_id: str, channel: str, payload: Any) -> dict[str, Any]:
    """Send the host's events.dispatch request and return the response the extension answers with, serving a bus call the listener makes meanwhile."""
    assert fake.conn is not None
    _write_frame(fake.conn, {"type": "request", "id": request_id, "request": {"method": "events.dispatch", "args": {"handlerId": handler_id, "channel": channel, "json": payload}}})
    while True:
        env = _read_frame_raw(fake.conn)
        if env["type"] == "response" and env["id"] == request_id:
            return env["response"]
        if env["type"] == "call":
            fake.calls.append(env["call"])
            _write_frame(fake.conn, {"type": "call_result", "id": env["id"], "call_result": {"result": None}})


def test_on_registers_with_the_host_at_once(host) -> None:
    ext = pig_sdk.Extension("bus")
    fake = host(None)
    worker = fake.serve(1)
    ext.events.on("ch", lambda ctx, data: None)
    worker.join(timeout=5)
    call = fake.calls[0]
    assert call["method"] == "events.on"
    assert call["args"]["channel"] == "ch" and call["args"]["value"] is True and call["args"]["handlerId"]
    assert "parent_request_id" not in call


def test_the_host_dispatches_to_a_factory_listener_while_a_call_waits_and_run_does_not_register_it_again(host) -> None:
    heard: list[Any] = []
    ext = pig_sdk.Extension("bus")
    fake = host(None, None)
    worker = fake.serve(1)
    ext.events.on("ack", lambda ctx, data: heard.append(data))
    worker.join(timeout=5)
    handler_id = fake.calls[0]["args"]["handlerId"]

    # An earlier extension's listener answers the factory's emit on cross with an emit on ack: the host dispatches ack to the factory's listener before it answers cross.
    def emit_cross() -> None:
        ext.events.emit("cross", "ping")

    emitter = threading.Thread(target=emit_cross, daemon=True)
    emitter.start()
    call = _read_frame_raw(fake.conn)
    assert call["type"] == "call" and call["call"]["method"] == "events.emit"
    response = _dispatch(fake, "d1", handler_id, "ack", "pong")
    assert response["error"] is None
    assert heard == ["pong"]
    _write_frame(fake.conn, {"type": "call_result", "id": call["id"], "call_result": {"result": None}})
    emitter.join(timeout=5)
    assert not emitter.is_alive()

    runner = threading.Thread(target=ext.run_with_socket, args=(fake.path,), daemon=True)
    runner.start()
    # The first frame Run sends is the register frame: the listener is not registered a second time.
    assert _read_frame_raw(fake.conn)["type"] == "register"
    _write_frame(fake.conn, {"type": "ready", "ready": {"cwd": fake.dir, "width": 80}})
    _write_frame(fake.conn, {"type": "shutdown", "shutdown": {"reason": "test"}})
    runner.join(timeout=5)
    assert fake.accepted == 1


@pytest.mark.parametrize("package_attribute", [True, False], ids=["package-attribute-bound", "package-attribute-unbound"])
def test_a_shutdown_read_during_a_factory_call_ends_the_run_after_it_registers(host, monkeypatch: pytest.MonkeyPatch, package_attribute: bool) -> None:
    """A shutdown the host sends while a factory-time call waits does not fail the call: it returns the host's result, and the run that takes the connection registers and then ends on the shutdown.

    The run reads the shutdown through ``host_helpers.take_shutdown``, so the run's registering step imports ``host_helpers`` itself instead of relying on the import that bound it as an attribute of the package; the unbound case removes that attribute."""
    if not package_attribute:
        monkeypatch.delattr(pig_sdk, "host_helpers")
    ext = pig_sdk.Extension("bus")
    fake = host(None)

    def script() -> None:
        fake.thread.join(timeout=15)
        assert fake.conn is not None
        env = _read_frame_raw(fake.conn)
        assert env["type"] == "call"
        _write_frame(fake.conn, {"type": "shutdown", "shutdown": {"reason": "test"}})
        _write_frame(fake.conn, {"type": "call_result", "id": env["id"], "call_result": {"result": None}})

    worker = threading.Thread(target=script, daemon=True)
    worker.start()
    ext.events.on("ch", lambda ctx, data: None)
    worker.join(timeout=5)
    ext.run_with_socket(fake.path)
    assert fake.conn is not None
    assert _read_frame_raw(fake.conn)["type"] == "register"
    assert fake.conn.recv(1) == b""


def test_a_factory_call_waiting_for_the_host_does_not_hold_up_another_members_register(monkeypatch: pytest.MonkeyPatch) -> None:
    """A packed cell's members build one after another, and the host accepts their connections in that order, serving a connection only after the members before it have
    registered. The second member's factory-time call therefore waits for the first member's register, so the first member's run, which takes its own connection to
    register, must not wait for that call: a lock held across the wait deadlocks the two, and the host never sees the first register."""
    directory = tempfile.mkdtemp()
    first_path, second_path = os.path.join(directory, "first.sock"), os.path.join(directory, "second.sock")
    first_listener, second_listener = _start_fake_host(first_path), _start_fake_host(second_path)
    connections: list[socket.socket] = []
    threads: list[threading.Thread] = []
    second_call: dict[str, Any] = {}

    def factory_call() -> threading.Thread:
        thread = threading.Thread(target=lambda: host_helpers.loading_call("factory-time", {}), daemon=True)
        thread.start()
        threads.append(thread)
        return thread

    def accept(listener: socket.socket) -> socket.socket:
        conn, _ = listener.accept()
        conn.settimeout(15)
        connections.append(conn)
        return conn

    try:
        # the first member's factory made its one host call and returned: its connection is dialled and waits for its run to take it
        monkeypatch.setenv("PIG_EXT_SOCKET", first_path)
        first_caller = factory_call()
        first_conn = accept(first_listener)
        call = _read_frame_raw(first_conn)
        _write_frame(first_conn, {"type": "call_result", "id": call["id"], "call_result": {"result": None}})
        first_caller.join(timeout=10)
        # the second member's factory calls the host, which has not accepted its connection yet: the call waits
        monkeypatch.setenv("PIG_EXT_SOCKET", second_path)
        factory_call()
        second_conn = accept(second_listener)
        second_call.update(_read_frame_raw(second_conn))
        assert second_call["type"] == "call"
        # the first member's run registers meanwhile; a bounded wait, so a run held up by the second member's call fails the test instead of hanging it
        first = pig_sdk.Extension("first")
        runner = threading.Thread(target=lambda: first.run_with_socket(first_path), daemon=True)
        runner.start()
        threads.append(runner)
        registered: list[str] = []
        reader = threading.Thread(target=lambda: registered.append(_read_frame_raw(first_conn)["type"]), daemon=True)
        reader.start()
        reader.join(timeout=10)
        assert registered == ["register"], "the first member did not register while the second member's call waited for the host"
    finally:
        # answer the waiting call so a held-up run finishes, then end both threads
        if second_call:
            _write_frame(connections[1], {"type": "call_result", "id": second_call["id"], "call_result": {"result": None}})
        if connections:
            _write_frame(connections[0], {"type": "shutdown", "shutdown": {"reason": "test"}})
        for thread in threads:
            thread.join(timeout=10)
        for conn in connections:
            conn.close()
        first_listener.close()
        second_listener.close()
        host_helpers._State.dialled.clear()


class _SpyLock:
    """An ``RLock`` that records which lock object each thread went to take, and tells the test (``attempted``) when a thread is about to wait for it."""

    def __init__(self, seen: dict[str, "_SpyLock"], attempted: dict[str, threading.Event]) -> None:
        self._lock = threading.RLock()
        self._seen = seen
        self._attempted = attempted

    def __enter__(self) -> "_SpyLock":
        name = threading.current_thread().name
        self._seen[name] = self
        self._attempted[name].set()
        self._lock.acquire()
        return self

    def __exit__(self, *exc: Any) -> None:
        self._lock.release()


class _PausingDialled(dict):
    """The table of dialled connections, whose ``pop`` stops until the test lets it go. ``take_helper_socket`` makes that call while it holds its path's lock and
    ``_State.lock``; the pause gives ``_State.lock`` up, as any wait for the host would, so other threads can look the path's lock up while the take holds it."""

    def __init__(self) -> None:
        super().__init__()
        self.in_pop = threading.Event()
        self.go = threading.Event()

    def pop(self, key: Any, *default: Any) -> Any:
        self.in_pop.set()
        host_helpers._State.lock.release()
        try:
            self.go.wait(timeout=10)
        finally:
            host_helpers._State.lock.acquire()
        return super().pop(key, *default)


def test_a_path_lock_is_not_dropped_while_a_thread_holds_or_waits_for_it(monkeypatch: pytest.MonkeyPatch) -> None:
    """Every call on a socket path takes that path's lock, and two threads on one path must never hold two different locks for it. ``take_helper_socket`` holds
    the lock while it takes the connection; thread B comes to the same path and waits for that lock; the take ends, B takes the lock, and a third caller C then
    comes to the path. C must wait for B: a lock the take dropped from the table while B waited for it gives C a new one, and B and C run at once."""
    path = "/nonexistent/pig-path-lock.sock"
    seen: dict[str, _SpyLock] = {}
    attempted: dict[str, threading.Event] = defaultdict(threading.Event)
    dialled = _PausingDialled()
    monkeypatch.setattr(host_helpers, "threading", SimpleNamespace(**{**vars(threading), "RLock": lambda: _SpyLock(seen, attempted)}))
    monkeypatch.setattr(host_helpers._State, "path_locks", {})
    monkeypatch.setattr(host_helpers._State, "dialled", dialled)
    holding = {"B": threading.Event(), "C": threading.Event()}
    release = threading.Event()

    def call_on_the_path(name: str) -> threading.Thread:
        def run() -> None:
            with host_helpers._path_lock(path):
                holding[name].set()
                release.wait(timeout=10)

        thread = threading.Thread(target=run, name=name, daemon=True)
        thread.start()
        return thread

    taker = threading.Thread(target=lambda: host_helpers.take_helper_socket(path), name="taker", daemon=True)
    threads = [taker]
    try:
        taker.start()
        assert dialled.in_pop.wait(timeout=10), "take_helper_socket never reached the connection table"
        threads.append(call_on_the_path("B"))
        assert attempted["B"].wait(timeout=10), "B never came to the path's lock"
        dialled.go.set()
        taker.join(timeout=10)
        assert not taker.is_alive(), "take_helper_socket did not end"
        assert holding["B"].wait(timeout=10), "B never took the path's lock after the take ended"
        threads.append(call_on_the_path("C"))
        assert attempted["C"].wait(timeout=10), "C never came to the path's lock"
        assert seen["C"] is seen["B"], "B holds one lock for the path and C went for another: both run at once"
    finally:
        dialled.go.set()
        release.set()
        for thread in threads:
            thread.join(timeout=10)
    assert host_helpers._State.path_locks == {}, "a path's lock stays in the table after its last user left"


def test_a_listener_failure_fails_only_its_dispatch(host) -> None:
    ext = pig_sdk.Extension("bus")
    fake = host(None, None, None)
    worker = fake.serve(2)

    def boom(ctx, data):
        raise ValueError("boom")

    ext.events.on("fail", boom)
    ext.events.on("ok", lambda ctx, data: None)
    worker.join(timeout=5)
    failing, fine = (call["args"]["handlerId"] for call in fake.calls)
    emitter = threading.Thread(target=lambda: ext.events.emit("x", None), daemon=True)
    emitter.start()
    call = _read_frame_raw(fake.conn)
    assert _dispatch(fake, "d1", failing, "fail", None)["error"] == {"message": "boom"}
    assert _dispatch(fake, "d2", fine, "ok", None)["error"] is None
    _write_frame(fake.conn, {"type": "call_result", "id": call["id"], "call_result": {"result": None}})
    emitter.join(timeout=5)


def test_a_request_the_extension_cannot_serve_yet_is_refused(host) -> None:
    ext = pig_sdk.Extension("bus")
    fake = host(None, None)
    worker = fake.serve(1)
    ext.events.on("ch", lambda ctx, data: None)
    worker.join(timeout=5)
    emitter = threading.Thread(target=lambda: ext.events.emit("x", None), daemon=True)
    emitter.start()
    call = _read_frame_raw(fake.conn)
    _write_frame(fake.conn, {"type": "request", "id": "t1", "request": {"method": "tool_call", "args": {}}})
    refused = _read_frame_raw(fake.conn)
    assert refused["type"] == "response" and "not registered" in refused["response"]["error"]["message"]
    _write_frame(fake.conn, {"type": "call_result", "id": call["id"], "call_result": {"result": None}})
    emitter.join(timeout=5)


def test_unsubscribe_reaches_the_host_over_the_factory_connection(host) -> None:
    ext = pig_sdk.Extension("bus")
    fake = host(None, None)
    worker = fake.serve(2)
    off = ext.events.on("ch", lambda ctx, data: None)
    off()
    worker.join(timeout=5)
    assert [call["method"] for call in fake.calls] == ["events.on", "events.off"]
    assert fake.calls[1]["args"]["handlerId"] == fake.calls[0]["args"]["handlerId"]


def test_an_error_emit_raises_when_the_host_reports_nobody_heard(host) -> None:
    ext = pig_sdk.Extension("bus")
    fake = host({"unhandledError": True})
    worker = fake.serve(1)
    with pytest.raises(RuntimeError, match="unhandled error event"):
        ext.events.emit("error", {"x": 1})
    worker.join(timeout=5)


def test_without_a_host_a_factory_emit_fails_and_a_listener_waits_for_run(monkeypatch) -> None:
    monkeypatch.delenv("PIG_EXT_SOCKET", raising=False)
    host_helpers._State.running[:] = []
    ext = pig_sdk.Extension("bus")
    with pytest.raises(RuntimeError, match="PIG_EXT_SOCKET"):
        ext.events.emit("ch", None)
    ext.events.on("ch", lambda ctx, data: None)
    assert len(ext._bus._pending) == 1


def test_a_factory_listener_context_serves_only_the_bus(host) -> None:
    seen: dict[str, Any] = {}
    ext = pig_sdk.Extension("bus")
    fake = host(None, None)
    worker = fake.serve(1)

    def outer(ctx, data):
        ctx.events.emit("inner", None)
        try:
            ctx.call_host("ui.notify", {"message": "x"})
        except RuntimeError as exc:
            seen["error"] = str(exc)

    ext.events.on("outer", outer)
    worker.join(timeout=5)
    handler_id = fake.calls[0]["args"]["handlerId"]
    emitter = threading.Thread(target=lambda: ext.events.emit("x", None), daemon=True)
    emitter.start()
    call = _read_frame_raw(fake.conn)
    assert _dispatch(fake, "d1", handler_id, "outer", None)["error"] is None
    _write_frame(fake.conn, {"type": "call_result", "id": call["id"], "call_result": {"result": None}})
    emitter.join(timeout=5)
    assert [call["method"] + ":" + str(call["args"].get("channel")) for call in fake.calls[1:]] == ["events.emit:inner"]
    assert "not registered" in seen["error"]
