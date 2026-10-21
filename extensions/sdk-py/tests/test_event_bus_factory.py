"""A factory's ``pi.events.emit`` is Pi's synchronous EventEmitter emit (event-bus.ts:13-34): the listeners of the extensions loaded before hear it at
once, then the factory's own, each before ``emit`` returns, and an emit on ``"error"`` that nobody hears raises. A native SDK has no connection while its
factory runs, so the first factory-time bus call dials the host (as ``parse_frontmatter`` does) and ``run`` takes that connection.

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


def test_emit_reaches_the_host_and_own_listeners_before_it_returns(host) -> None:
    heard: list[Any] = []
    ext = pig_sdk.Extension("bus")
    ext.events.on("ch", lambda ctx, data: heard.append(data))
    fake = host(None)
    worker = fake.serve(1)
    assert ext.events.emit("ch", {"n": 1}) is None
    worker.join(timeout=5)
    call = fake.calls[0]
    assert call["method"] == "events.emit"
    assert call["args"] == {"channel": "ch", "json": {"n": 1}, "value": True}
    assert "parent_request_id" not in call
    # The host does not hold this extension's listener yet: it ran inline, and has finished when emit returned.
    assert heard == [{"n": 1}]


def test_listeners_run_in_order_and_a_failure_reaches_neither_the_emitter_nor_the_rest(host, capsys) -> None:
    order: list[str] = []
    ext = pig_sdk.Extension("bus")
    bus = ext.events
    bus.on("ch", lambda ctx, data: order.append("first"))

    def boom(ctx, data):
        raise ValueError("boom")

    bus.on("ch", boom)
    off = bus.on("ch", lambda ctx, data: order.append("unsubscribed"))
    off()
    bus.on("ch", lambda ctx, data: order.append("last"))
    fake = host(None)
    worker = fake.serve(1)
    bus.emit("ch", None)
    bus.on("ch", lambda ctx, data: order.append("late"))
    worker.join(timeout=5)
    assert order == ["first", "last"]
    assert "Event handler error (ch): boom" in capsys.readouterr().err


def test_an_error_emit_nobody_hears_raises_and_an_own_listener_hears_it(host) -> None:
    ext = pig_sdk.Extension("bus")
    fake = host({"unhandledError": True}, {"unhandledError": True})
    worker = fake.serve(2)
    with pytest.raises(RuntimeError, match="unhandled error event"):
        ext.events.emit("error", {"x": 1})
    heard: list[Any] = []
    own = pig_sdk.Extension("own")
    own.events.on("error", lambda ctx, data: heard.append(data))
    own.events.emit("error", "bad")
    worker.join(timeout=5)
    assert heard == ["bad"]


def test_without_a_host_a_factory_emit_fails(monkeypatch) -> None:
    monkeypatch.delenv("PIG_EXT_SOCKET", raising=False)
    host_helpers._State.running[:] = []
    with pytest.raises(RuntimeError, match="PIG_EXT_SOCKET"):
        pig_sdk.Extension("bus").events.emit("ch", None)


def test_a_factory_listener_context_serves_only_the_bus(host) -> None:
    heard: list[str] = []
    seen: dict[str, Any] = {}
    ext = pig_sdk.Extension("bus")
    ext.events.on("inner", lambda ctx, data: heard.append("inner"))

    def outer(ctx, data):
        ctx.events.emit("inner", None)
        try:
            ctx.call_host("ui.notify", {"message": "x"})
        except RuntimeError as exc:
            seen["error"] = str(exc)

    ext.events.on("outer", outer)
    fake = host(None, None)
    worker = fake.serve(2)
    ext.events.emit("outer", None)
    worker.join(timeout=5)
    assert [call["args"]["channel"] for call in fake.calls] == ["outer", "inner"]
    assert heard == ["inner"]
    assert "not registered" in seen["error"]


def test_run_takes_the_connection_the_factory_dialled_and_registers_the_listener(host) -> None:
    heard: list[Any] = []
    ext = pig_sdk.Extension("bus")
    ext.events.on("ch", lambda ctx, data: heard.append(data))
    fake = host(None)
    worker = fake.serve(1)
    ext.events.emit("ch", 1)
    worker.join(timeout=5)
    runner = threading.Thread(target=ext.run_with_socket, args=(fake.path,), daemon=True)
    runner.start()
    on = _read_frame_raw(fake.conn)
    assert on["call"]["method"] == "events.on" and on["call"]["args"]["channel"] == "ch"
    _write_frame(fake.conn, {"type": "call_result", "id": on["id"], "call_result": {"result": None}})
    assert _read_frame_raw(fake.conn)["type"] == "register"
    _write_frame(fake.conn, {"type": "ready", "ready": {"cwd": fake.dir, "width": 80}})
    _write_frame(fake.conn, {"type": "shutdown", "shutdown": {"reason": "test"}})
    runner.join(timeout=5)
    # One connection served the emit and the run, and the emit was not delivered a second time.
    assert fake.accepted == 1
    assert heard == [1]
