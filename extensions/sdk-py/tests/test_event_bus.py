"""The wire the Python SDK speaks for upstream's pi.events (packages/coding-agent/src/core/event-bus.ts:12-33): events.on and events.emit calls flagged by value, events.off, and the Host's events.dispatch request. The Go SDK's tests state the same rows."""

from __future__ import annotations

import os
import tempfile
import threading

import pig_sdk

from test_sdk import _read_frame, _start_fake_host, _write_frame


class _Host:
    def __init__(self, ext: pig_sdk.Extension):
        self.tmp = tempfile.mkdtemp()
        self.listener = _start_fake_host(os.path.join(self.tmp, "ext.sock"))
        self.thread = threading.Thread(target=ext.run_with_socket, args=(os.path.join(self.tmp, "ext.sock"),), daemon=True)
        self.thread.start()
        self.conn, _ = self.listener.accept()
        self.conn.settimeout(5)

    def call(self, method: str, result=None) -> tuple[dict, str]:
        """Read the extension's next host call and answer it."""
        while True:
            env = _read_frame(self.conn)
            if env["type"] != "call":
                continue
            assert env["call"]["method"] == method, env["call"]["method"]
            _write_frame(self.conn, {"type": "call_result", "id": env["id"], "call_result": {"result": result}})
            return env["call"]["args"], env["call"].get("parent_request_id", "")

    def ready(self) -> None:
        while _read_frame(self.conn)["type"] != "register":
            pass
        _write_frame(self.conn, {"type": "ready", "ready": {"cwd": self.tmp, "width": 80}})

    def response(self) -> dict:
        while True:
            env = _read_frame(self.conn)
            if env["type"] == "response":
                return env

    def stop(self) -> None:
        _write_frame(self.conn, {"type": "shutdown", "shutdown": {"reason": "test"}})
        self.conn.close()
        self.listener.close()
        self.thread.join(timeout=5)


# A listener declared while the factory runs is registered before the register message, as a node factory's pi.events.on is (loader.ts runs the factory before the runtime registers), and carries the by-value flag.
def test_factory_listener_is_registered_before_register() -> None:
    ext = pig_sdk.Extension("bus")
    ext.events.on("ch", lambda ctx, data: None)
    host = _Host(ext)
    try:
        on, parent = host.call("events.on")
        assert on["channel"] == "ch" and on["handlerId"] and on["value"] is True and parent == ""
        host.ready()
    finally:
        host.stop()


# Dispatch runs the handler with the payload decoded from the JSON the Host sends and answers after the handler returns (event-bus.ts:19-25).
def test_dispatch_runs_handler_then_answers() -> None:
    got: list = []
    ext = pig_sdk.Extension("bus")
    ext.events.on("ch", lambda ctx, data: got.append(data))
    host = _Host(ext)
    try:
        on, _ = host.call("events.on")
        host.ready()
        _write_frame(host.conn, {"type": "request", "id": "d1", "request": {"method": "events.dispatch", "args": {"handlerId": on["handlerId"], "channel": "ch", "json": {"a": [1, 2]}}}})
        response = host.response()
        assert response["id"] == "d1" and not response["response"].get("error")
        assert got == [{"a": [1, 2]}], "the response arrived before the handler ran"
    finally:
        host.stop()


# A handler's error is the dispatch's failure and nothing else: the extension keeps serving (event-bus.ts:19-25 catches and prints, and never rethrows).
def test_handler_error_fails_only_its_dispatch() -> None:
    def fail(ctx, data):
        raise RuntimeError("listener-failed")

    ext = pig_sdk.Extension("bus")
    ext.events.on("fail", fail)
    ext.events.on("ok", lambda ctx, data: None)
    host = _Host(ext)
    try:
        ids = {}
        for _ in range(2):
            on, _ = host.call("events.on")
            ids[on["channel"]] = on["handlerId"]
        host.ready()
        for i, (channel, want) in enumerate([("fail", "listener-failed"), ("ok", "")]):
            _write_frame(host.conn, {"type": "request", "id": f"d{i}", "request": {"method": "events.dispatch", "args": {"handlerId": ids[channel], "channel": channel, "json": None}}})
            error = (host.response()["response"].get("error") or {}).get("message", "")
            assert (want == "") == (error == "") and want in error, (channel, error)
    finally:
        host.stop()


# Emit sends the payload as JSON under the by-value flag and takes the calling request as the call's parent, so a handler's nested emit is ordered in its own lane and cancelled with it. The unhandled "error" rule surfaces as the emit's error (event-bus.ts:15-17, EventEmitter throws).
def test_emit_carries_payload_and_parent() -> None:
    outcomes: list = []

    def go(ctx: pig_sdk.Context, args: str) -> None:
        ctx.events.emit("ch", {"a": 1})
        try:
            ctx.events.emit("error", "boom")
        except RuntimeError as exc:
            outcomes.append(str(exc))

    ext = pig_sdk.Extension("bus")
    ext.command("go", "", go)
    host = _Host(ext)
    try:
        host.ready()
        _write_frame(host.conn, {"type": "request", "id": "cmd1", "request": {"method": "command", "tool": "go"}})
        args, parent = host.call("events.emit")
        assert args == {"channel": "ch", "value": True, "json": {"a": 1}} and parent == "cmd1"
        host.call("events.emit", {"unhandledError": True})
        assert host.response()["id"] == "cmd1"
        assert len(outcomes) == 1 and "unhandled error" in outcomes[0]
    finally:
        host.stop()


# Unsubscribe removes the listener with one events.off call and is idempotent (event-bus.ts:27, EventEmitter.off).
def test_unsubscribe_sends_one_off() -> None:
    done = threading.Event()

    def off(ctx: pig_sdk.Context, args: str) -> None:
        unsubscribe = ctx.events.on("ch", lambda c, d: None)
        unsubscribe()
        unsubscribe()
        done.set()

    ext = pig_sdk.Extension("bus")
    ext.command("off", "", off)
    host = _Host(ext)
    try:
        host.ready()
        _write_frame(host.conn, {"type": "request", "id": "cmd1", "request": {"method": "command", "tool": "off"}})
        on, parent = host.call("events.on")
        assert parent == "cmd1"
        off_args, _ = host.call("events.off")
        assert off_args["handlerId"] == on["handlerId"]
        assert done.wait(5), "the second unsubscribe waited for a host call it should not send"
        assert host.response()["id"] == "cmd1"
    finally:
        host.stop()


# A listener the Host dispatches to while the extension is still loading (after register, before ready) is served, as a node runtime serves it during its factory.
def test_dispatch_during_load_is_served() -> None:
    got: list = []
    ext = pig_sdk.Extension("bus")
    ext.events.on("ch", lambda ctx, data: got.append(data))
    host = _Host(ext)
    try:
        on, _ = host.call("events.on")
        while _read_frame(host.conn)["type"] != "register":
            pass
        _write_frame(host.conn, {"type": "request", "id": "d1", "request": {"method": "events.dispatch", "args": {"handlerId": on["handlerId"], "channel": "ch", "json": "early"}}})
        response = host.response()
        assert response["id"] == "d1" and not response["response"].get("error")
        assert got == ["early"]
        _write_frame(host.conn, {"type": "ready", "ready": {"cwd": host.tmp, "width": 80}})
    finally:
        host.stop()
