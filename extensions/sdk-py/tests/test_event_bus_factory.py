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
