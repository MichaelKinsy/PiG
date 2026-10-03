"""The pi.events bridge in the Python SDK.

Mirrors the wire that the Go SDK's tests pin (extensions/sdk/event_bus_test.go on lane port-99-f6f-events):
events.on and events.emit calls flagged by value, events.off, and the Host's events.dispatch request.
Upstream: .upstream/v0.99.2/packages/coding-agent/src/core/event-bus.ts:12-33, extensions/loader.ts:501-513.
"""

from __future__ import annotations

import os
import tempfile
import threading
from typing import Any

import pytest
from test_sdk import _read_frame_raw, _start_fake_host, _write_frame

import pig_sdk


class _BusHost:
    """A fake host that serves calls the extension makes before and after its register frame."""

    def __init__(self, ext: pig_sdk.Extension) -> None:
        tmp = tempfile.mkdtemp()
        sock_path = os.path.join(tmp, "ext.sock")
        self.listener = _start_fake_host(sock_path)
        self.thread = threading.Thread(target=ext.run_with_socket, args=(sock_path,), daemon=True)
        self.thread.start()
        self.conn, _ = self.listener.accept()
        # A missing frame fails the test after 15 s instead of hanging it.
        self.conn.settimeout(15)
        self.pre_register: list[dict[str, Any]] = []
        while True:
            env = self.read()
            if env["type"] == "register":
                self.register = env["register"]
                break
            assert env["type"] == "call", env
            self.pre_register.append(env)
            self.answer(env, None)
        _write_frame(self.conn, {"type": "ready", "ready": {"cwd": tmp, "width": 80}})

    def read(self) -> dict[str, Any]:
        while True:
            env = _read_frame_raw(self.conn)
            if env.get("type") != "request_state":
                return env

    def answer(self, call: dict[str, Any], result: Any = None, error: dict[str, Any] | None = None) -> None:
        payload = {"error": error} if error is not None else {"result": result}
        _write_frame(self.conn, {"type": "call_result", "id": call["id"], "call_result": payload})

    def request(self, req_id: str, request: dict[str, Any]) -> None:
        _write_frame(self.conn, {"type": "request", "id": req_id, "request": request})

    def dispatch(self, req_id: str, handler_id: str, channel: str, data: Any) -> dict[str, Any]:
        """Send events.dispatch and return the response, failing when the extension answers a call instead."""
        self.request(req_id, {"method": "events.dispatch", "args": {"handlerId": handler_id, "channel": channel, "json": data}})
        env = self.read()
        assert env["type"] == "response" and env["id"] == req_id, env
        return env["response"]

    def close(self) -> None:
        _write_frame(self.conn, {"type": "shutdown", "shutdown": {"reason": "test"}})
        self.conn.close()
        self.listener.close()
        self.thread.join(timeout=2)


def test_factory_listener_is_registered_before_the_register_frame_by_value() -> None:
    """loader.ts:501-509: the factory's pi.events.on runs while the extension loads, so the Host has the listener before it registers the extension; a native listener asks for its payload by value."""
    ext = pig_sdk.Extension("bus")
    ext.events.on("ch", lambda ctx, data: None)
    host = _BusHost(ext)
    try:
        assert len(host.pre_register) == 1
        call = host.pre_register[0]["call"]
        assert call["method"] == "events.on"
        assert call["args"]["channel"] == "ch" and call["args"]["handlerId"] and call["args"]["value"] is True
        assert "parent_request_id" not in call
    finally:
        host.close()


def test_a_listener_unsubscribed_while_the_factory_runs_is_never_sent() -> None:
    ext = pig_sdk.Extension("bus")
    ext.events.on("gone", lambda ctx, data: None)()
    ext.events.on("kept", lambda ctx, data: None)
    host = _BusHost(ext)
    try:
        assert [c["call"]["args"]["channel"] for c in host.pre_register] == ["kept"]
    finally:
        host.close()


def test_dispatch_runs_the_handler_then_answers() -> None:
    """event-bus.ts:24-30: the listener's work is one unit, and the emitter is released after it."""
    got: list[Any] = []
    ext = pig_sdk.Extension("bus")
    ext.events.on("ch", lambda ctx, data: got.append(data))
    host = _BusHost(ext)
    try:
        handler_id = host.pre_register[0]["call"]["args"]["handlerId"]
        response = host.dispatch("d1", handler_id, "ch", {"a": [1, 2], "s": "é"})
        assert response == {"result": None, "error": None}
        assert got == [{"a": [1, 2], "s": "é"}]
    finally:
        host.close()


def test_a_handler_error_fails_only_its_dispatch() -> None:
    """event-bus.ts:24-30: safeHandler catches and reports; the SDK keeps serving and the error is that dispatch's."""
    def fail(ctx: pig_sdk.Context, data: Any) -> None:
        raise RuntimeError("listener-failed")

    seen: list[Any] = []
    ext = pig_sdk.Extension("bus")
    ext.events.on("fail", fail)
    ext.events.on("ok", lambda ctx, data: seen.append(data))
    host = _BusHost(ext)
    try:
        ids = {c["call"]["args"]["channel"]: c["call"]["args"]["handlerId"] for c in host.pre_register}
        assert host.dispatch("d1", ids["fail"], "fail", None)["error"] == {"message": "listener-failed"}
        assert host.dispatch("d2", ids["ok"], "ok", 7) == {"result": None, "error": None}
        assert seen == [7]
    finally:
        host.close()


def test_an_unknown_handler_id_is_an_error_not_a_hang() -> None:
    ext = pig_sdk.Extension("bus")
    host = _BusHost(ext)
    try:
        assert "handler" in host.dispatch("d1", "nope", "ch", None)["error"]["message"]
    finally:
        host.close()


def test_emit_sends_json_by_value_with_the_calling_request_as_parent() -> None:
    """event-bus.ts:15-22: emit is synchronous and an unhandled `error` channel throws to the emitter."""
    outcome: list[Any] = []
    ext = pig_sdk.Extension("bus")

    def command(ctx: pig_sdk.Context, _args: str) -> None:
        outcome.append(ctx.events.emit("ch", {"a": 1}))
        try:
            ctx.events.emit("error", "boom")
        except RuntimeError as exc:
            outcome.append(str(exc))

    ext.command("go", "run", command)
    host = _BusHost(ext)
    try:
        host.request("cmd1", {"method": "command", "tool": "go"})
        first = host.read()
        assert first["call"]["method"] == "events.emit" and first["call"]["parent_request_id"] == "cmd1"
        assert first["call"]["args"] == {"channel": "ch", "json": {"a": 1}, "value": True}
        host.answer(first, None)
        second = host.read()
        assert second["call"]["args"]["channel"] == "error"
        host.answer(second, {"unhandledError": True})
        assert host.read()["type"] == "response"
        assert outcome[0] is None and "unhandled error" in outcome[1] and "boom" in outcome[1]
    finally:
        host.close()


def test_emit_from_the_extension_has_no_parent_and_rejects_a_payload_without_a_json_form() -> None:
    ext = pig_sdk.Extension("bus")
    host = _BusHost(ext)
    try:
        errors: list[Exception] = []

        def emit() -> None:
            try:
                ext.events.emit("ch", {"cycle": object()})
            except TypeError as exc:
                errors.append(exc)
            ext.events.emit("ch", None)

        thread = threading.Thread(target=emit)
        thread.start()
        call = host.read()
        assert call["call"]["args"] == {"channel": "ch", "json": None, "value": True}
        assert "parent_request_id" not in call["call"]
        host.answer(call, None)
        thread.join(timeout=5)
        assert len(errors) == 1 and "JSON" in str(errors[0])
    finally:
        host.close()


def test_unsubscribe_sends_one_off_and_is_idempotent() -> None:
    """event-bus.ts:32 (EventEmitter.off): removing a listener twice removes it once."""
    ext = pig_sdk.Extension("bus")
    unsubscribed = threading.Event()

    def command(ctx: pig_sdk.Context, _args: str) -> None:
        unsubscribe = ctx.events.on("ch", lambda c, d: None)
        unsubscribe()
        unsubscribe()
        unsubscribed.set()

    ext.command("off", "", command)
    host = _BusHost(ext)
    try:
        host.request("cmd1", {"method": "command", "tool": "off"})
        on = host.read()
        assert on["call"]["method"] == "events.on" and on["call"]["parent_request_id"] == "cmd1" and on["call"]["args"]["value"] is True
        host.answer(on, None)
        off = host.read()
        assert off["call"]["method"] == "events.off" and off["call"]["args"]["handlerId"] == on["call"]["args"]["handlerId"]
        host.answer(off, None)
        assert host.read()["type"] == "response"
        assert unsubscribed.is_set()
    finally:
        host.close()


def test_a_listener_removed_during_a_dispatch_still_runs_for_that_dispatch() -> None:
    """EventEmitter clones its listener array for each emit: the Host dispatches the snapshot, so a listener that was unsubscribed after the snapshot still receives it."""
    ext = pig_sdk.Extension("bus")
    ran: list[Any] = []
    unsubscribe = ext.events.on("ch", lambda ctx, data: ran.append(data))
    host = _BusHost(ext)
    try:
        handler_id = host.pre_register[0]["call"]["args"]["handlerId"]
        remover = threading.Thread(target=unsubscribe)
        remover.start()
        off = host.read()
        assert off["call"]["method"] == "events.off"
        host.answer(off, None)
        remover.join(timeout=5)
        assert host.dispatch("d1", handler_id, "ch", "snapshot") == {"result": None, "error": None}
        assert ran == ["snapshot"]
    finally:
        host.close()


def test_a_handler_can_emit_while_the_host_waits_for_its_answer() -> None:
    """event-bus.ts:15-27 (EventEmitter is synchronous and reentrant): the SDK serves host calls while a handler runs, so a nested emit does not deadlock."""
    ext = pig_sdk.Extension("bus")
    ext.events.on("a", lambda ctx, data: ctx.events.emit("b", {"by": "py", "got": data}))
    host = _BusHost(ext)
    try:
        handler_id = host.pre_register[0]["call"]["args"]["handlerId"]
        host.request("d1", {"method": "events.dispatch", "args": {"handlerId": handler_id, "channel": "a", "json": {"v": 1}}})
        nested = host.read()
        assert nested["call"]["method"] == "events.emit" and nested["call"]["parent_request_id"] == "d1"
        assert nested["call"]["args"] == {"channel": "b", "json": {"by": "py", "got": {"v": 1}}, "value": True}
        host.answer(nested, None)
        response = host.read()
        assert response["type"] == "response" and response["id"] == "d1" and response["response"]["error"] is None
    finally:
        host.close()


def test_registration_order_and_distinct_handler_ids() -> None:
    """EventEmitter keeps registration order; the Host orders listeners by their events.on calls, so each on() must complete its call before returning."""
    ext = pig_sdk.Extension("bus")
    for _ in range(3):
        ext.events.on("ch", lambda ctx, data: None)
    host = _BusHost(ext)
    try:
        ids = [c["call"]["args"]["handlerId"] for c in host.pre_register]
        assert len(set(ids)) == 3
    finally:
        host.close()


def test_on_rejects_a_handler_that_is_not_callable() -> None:
    ext = pig_sdk.Extension("bus")
    with pytest.raises(TypeError, match="callable"):
        ext.events.on("ch", "nope")  # type: ignore[arg-type]


def test_a_released_listener_is_dropped_and_a_live_one_ignores_release() -> None:
    """The host sends events.release once no snapshot can still name an unsubscribed listener; until then the listener stays callable. A release of a listener that is still subscribed changes nothing."""
    ext = pig_sdk.Extension("bus")
    ran: list[Any] = []
    kept_off = ext.events.on("gone", lambda ctx, data: ran.append(("gone", data)))
    ext.events.on("kept", lambda ctx, data: ran.append(("kept", data)))
    host = _BusHost(ext)
    try:
        ids = {c["call"]["args"]["channel"]: c["call"]["args"]["handlerId"] for c in host.pre_register}
        remover = threading.Thread(target=kept_off)
        remover.start()
        off = host.read()
        assert off["call"]["method"] == "events.off" and off["call"]["args"]["handlerId"] == ids["gone"]
        host.answer(off, None)
        remover.join(timeout=5)

        def release(handler_id: str) -> None:
            _write_frame(host.conn, {"type": "notify", "notify": {"method": "events.release", "args": {"handlerId": handler_id}}})

        release(ids["kept"])
        assert host.dispatch("d1", ids["kept"], "kept", 1) == {"result": None, "error": None}
        assert host.dispatch("d2", ids["gone"], "gone", 2) == {"result": None, "error": None}
        release(ids["gone"])
        # The notify and the next request travel the same socket in order, so the release is applied before this dispatch is served.
        assert "handler" in host.dispatch("d3", ids["gone"], "gone", 3)["error"]["message"]
        assert ran == [("kept", 1), ("gone", 2)]
    finally:
        host.close()


def test_a_dispatch_for_another_channel_is_an_unknown_handler() -> None:
    ext = pig_sdk.Extension("bus")
    ext.events.on("ch", lambda ctx, data: None)
    host = _BusHost(ext)
    try:
        handler_id = host.pre_register[0]["call"]["args"]["handlerId"]
        assert "handler" in host.dispatch("d1", handler_id, "other", None)["error"]["message"]
    finally:
        host.close()


def test_handler_ids_are_unique_across_the_extensions_of_one_process() -> None:
    """The Host keys a listener by realm (the OS process) and handler ID (coding/extension/host/subprocess/event_bus.go busHandlerKey), and a packed cell runs several extensions in one process. Two extensions that each subscribe must send different IDs, or one extension's unsubscribe removes the other's listener."""
    first = pig_sdk.Extension("bus-a")
    second = pig_sdk.Extension("bus-b")
    first.events.on("ch", lambda ctx, data: None)
    second.events.on("ch", lambda ctx, data: None)
    hosts = [_BusHost(first), _BusHost(second)]
    try:
        ids = [host.pre_register[0]["call"]["args"]["handlerId"] for host in hosts]
        assert ids[0] != ids[1], ids
    finally:
        for host in hosts:
            host.close()


def test_a_dispatch_that_arrives_before_the_factory_listener_is_confirmed_is_served() -> None:
    """The Host adds a listener when it receives events.on, so another realm's emit can dispatch to it before the call's result, while the extension still loads. A node runtime serves it during its factory; the SDK serves it and keeps loading."""
    got: list[Any] = []
    ext = pig_sdk.Extension("bus")
    ext.events.on("ch", lambda ctx, data: got.append(data))
    tmp = tempfile.mkdtemp()
    sock_path = os.path.join(tmp, "ext.sock")
    listener = _start_fake_host(sock_path)
    thread = threading.Thread(target=ext.run_with_socket, args=(sock_path,), daemon=True)
    thread.start()
    conn, _ = listener.accept()
    conn.settimeout(15)
    try:
        on = _read_frame_raw(conn)
        assert on["type"] == "call" and on["call"]["method"] == "events.on"
        handler_id = on["call"]["args"]["handlerId"]
        _write_frame(conn, {"type": "request", "id": "d1", "request": {"method": "events.dispatch", "args": {"handlerId": handler_id, "channel": "ch", "json": "early"}}})
        while (env := _read_frame_raw(conn))["type"] == "request_state":
            pass
        assert env["type"] == "response" and env["id"] == "d1" and env["response"]["error"] is None, env
        assert got == ["early"]
        _write_frame(conn, {"type": "call_result", "id": on["id"], "call_result": {"result": None}})
        assert _read_frame_raw(conn)["type"] == "register"
        _write_frame(conn, {"type": "ready", "ready": {"cwd": tmp, "width": 80}})
    finally:
        _write_frame(conn, {"type": "shutdown", "shutdown": {"reason": "test"}})
        conn.close()
        listener.close()
        thread.join(timeout=2)


def test_a_failed_unsubscribe_is_reported_and_does_not_raise(capsys: pytest.CaptureFixture[str]) -> None:
    """event-bus.ts:27: the unsubscribe function is EventEmitter.off, which never throws. A Host refusal is reported on stderr, as the Go SDK reports it."""
    ext = pig_sdk.Extension("bus")
    outcome: list[Any] = []

    def command(ctx: pig_sdk.Context, _args: str) -> None:
        unsubscribe = ctx.events.on("ch", lambda c, d: None)
        try:
            unsubscribe()
            outcome.append("returned")
        except Exception as exc:  # noqa: BLE001 - the test records what escaped
            outcome.append(exc)

    ext.command("off", "", command)
    host = _BusHost(ext)
    try:
        host.request("cmd1", {"method": "command", "tool": "off"})
        on = host.read()
        host.answer(on, None)
        off = host.read()
        assert off["call"]["method"] == "events.off"
        host.answer(off, error={"message": "connection replaced"})
        assert host.read()["type"] == "response"
        assert outcome == ["returned"]
        assert "pi.events unsubscribe 'ch': connection replaced" in capsys.readouterr().err
    finally:
        host.close()
