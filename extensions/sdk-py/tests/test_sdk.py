"""Unit tests for the Python pig-sdk.

These tests exercise the SDK in-process against a fake host socket so we can
verify register/ready/request/response/call/cancel/shutdown framing without
spawning a real pig host.
"""

from __future__ import annotations

import json
import os
import socket
import struct
import tempfile
import threading

import pig_sdk


def _read_frame(s: socket.socket) -> dict:
    while True:
        env = _read_frame_raw(s)
        if env.get("type") != "request_state":
            return env


def _read_frame_raw(s: socket.socket) -> dict:
    hdr = b""
    while len(hdr) < 4:
        chunk = s.recv(4 - len(hdr))
        if not chunk:
            raise EOFError
        hdr += chunk
    size = struct.unpack(">I", hdr)[0]
    body = b""
    while len(body) < size:
        chunk = s.recv(size - len(body))
        if not chunk:
            raise EOFError
        body += chunk
    return json.loads(body.decode())


def _write_frame(s: socket.socket, env: dict) -> None:
    data = json.dumps(env, separators=(",", ":")).encode()
    s.sendall(struct.pack(">I", len(data)) + data)


def _start_fake_host(sock_path: str) -> socket.socket:
    listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    listener.bind(sock_path)
    listener.listen(1)
    return listener


def test_register_and_tool_call_roundtrip() -> None:
    tmp = tempfile.mkdtemp()
    sock_path = os.path.join(tmp, "ext.sock")
    listener = _start_fake_host(sock_path)

    def factory() -> pig_sdk.Extension:
        ext = pig_sdk.Extension("py-unit")
        ext.tool("echo", "echo", {"type": "object"}, lambda ctx, args: {"content": args.get("msg", "")})
        return ext

    ext = factory()
    t = threading.Thread(target=ext.run_with_socket, args=(sock_path,), daemon=True)
    t.start()

    conn, _ = listener.accept()
    try:
        reg = _read_frame(conn)
        assert reg["type"] == "register"
        assert reg["register"]["name"] == "py-unit"
        assert [d["name"] for d in reg["register"]["tools"]] == ["echo"]
        assert "shortcuts" in reg["register"]
        assert "flags" in reg["register"]
        assert "providers" in reg["register"]
        assert "message_renderers" in reg["register"]

        _write_frame(conn, {"type": "ready", "ready": {"cwd": tmp, "width": 80}})

        _write_frame(conn, {
            "type": "request",
            "id": "r1",
            "request": {"method": "tool_call", "tool": "echo", "tool_call_id": "tc1", "args": {"msg": "hi"}},
        })
        resp = _read_frame(conn)
        assert resp["type"] == "response"
        assert resp["id"] == "r1"
        assert resp["response"]["result"]["content"] == "hi"
        assert resp["response"].get("error") is None
    finally:
        _write_frame(conn, {"type": "shutdown", "shutdown": {"reason": "test"}})
        conn.close()
        listener.close()
        t.join(timeout=2)


def test_cancel_sets_context_cancelled() -> None:
    tmp = tempfile.mkdtemp()
    sock_path = os.path.join(tmp, "ext.sock")
    listener = _start_fake_host(sock_path)

    started = threading.Event()
    observed_cancel = threading.Event()
    observed_reason: list[str | None] = []

    def slow_tool(ctx: pig_sdk.Context, args: dict) -> dict:
        started.set()
        # Wait for cancellation up to 2s.
        for _ in range(200):
            if ctx.is_cancelled():
                observed_cancel.set()
                observed_reason.append(ctx.cancellation_reason())
                return {"content": "", "is_error": True}
            threading.Event().wait(0.01)
        return {"content": "no cancel"}

    ext = pig_sdk.Extension("py-cancel")
    ext.tool("slow", "slow", {"type": "object"}, slow_tool)
    t = threading.Thread(target=ext.run_with_socket, args=(sock_path,), daemon=True)
    t.start()

    conn, _ = listener.accept()
    try:
        assert _read_frame(conn)["type"] == "register"
        _write_frame(conn, {"type": "ready", "ready": {"cwd": tmp, "width": 80}})
        _write_frame(conn, {
            "type": "request", "id": "r1",
            "request": {"method": "tool_call", "tool": "slow", "args": {}},
        })
        assert started.wait(2)
        _write_frame(conn, {"type": "cancel", "id": "r1", "cancel": {"request_id": "r1", "reason": "user_abort"}})
        assert observed_cancel.wait(2)
        resp = _read_frame(conn)
        assert resp["response"]["result"]["is_error"] is True
        assert observed_reason == ["user_abort"]
    finally:
        _write_frame(conn, {"type": "shutdown", "shutdown": {"reason": "test"}})
        conn.close()
        listener.close()
        t.join(timeout=2)


def test_shortcut_renderer_widget_and_declarations() -> None:
    tmp = tempfile.mkdtemp()
    sock_path = os.path.join(tmp, "ext.sock")
    listener = _start_fake_host(sock_path)

    ext = pig_sdk.Extension("py-full")
    ext.shortcut("ctrl+x", "shortcut", lambda ctx: None)
    ext.flag("dry-run", "dry run", "boolean", False)
    ext.register_provider("fake", {"kind": "test"})
    ext.message_renderer("custom", lambda ctx, message, options, width: [f"{message.get('text')}:{width}"])
    t = threading.Thread(target=ext.run_with_socket, args=(sock_path,), daemon=True)
    t.start()

    conn, _ = listener.accept()
    try:
        reg = _read_frame(conn)["register"]
        assert reg["shortcuts"] == [{"key": "ctrl+x", "description": "shortcut"}]
        assert reg["flags"] == [{"name": "dry-run", "description": "dry run", "type": "boolean", "default": False}]
        assert reg["providers"] == [{"name": "fake", "config": {"kind": "test"}}]
        assert reg["message_renderers"] == [{"custom_type": "custom"}]
        _write_frame(conn, {"type": "ready", "ready": {"cwd": tmp, "width": 80}})

        _write_frame(conn, {"type": "request", "id": "s1", "request": {"method": "shortcut", "tool": "ctrl+x"}})
        assert _read_frame(conn)["response"]["error"] is None

        _write_frame(conn, {"type": "request", "id": "r1", "request": {"method": "render_message", "tool": "custom", "args": {"message": {"text": "hello"}, "options": {}, "width": 42}}})
        resp = _read_frame(conn)
        assert resp["response"]["result"] == {"lines": ["hello:42"]}

        # Widget line updates use the fire-and-forget protocol envelope.
        ext._push_widget("status", ["ok"])
        widget = _read_frame(conn)
        assert widget["type"] == "widget_push"
        assert widget["widget_push"] == {"key": "status", "lines": ["ok"]}
    finally:
        _write_frame(conn, {"type": "shutdown", "shutdown": {"reason": "test"}})
        conn.close()
        listener.close()
        t.join(timeout=2)


def test_host_call_roundtrip() -> None:
    tmp = tempfile.mkdtemp()
    sock_path = os.path.join(tmp, "ext.sock")
    listener = _start_fake_host(sock_path)

    notified: list[dict] = []

    def notify_tool(ctx: pig_sdk.Context, args: dict) -> dict:
        ctx.notify("hi", level="info")
        return {"content": "done"}

    ext = pig_sdk.Extension("py-callback")
    ext.tool("notify", "notify", {"type": "object"}, notify_tool)
    t = threading.Thread(target=ext.run_with_socket, args=(sock_path,), daemon=True)
    t.start()

    conn, _ = listener.accept()
    try:
        assert _read_frame(conn)["type"] == "register"
        _write_frame(conn, {"type": "ready", "ready": {"cwd": tmp, "width": 80}})
        _write_frame(conn, {
            "type": "request", "id": "r1",
            "request": {"method": "tool_call", "tool": "notify", "args": {}},
        })

        call = _read_frame(conn)
        assert call["type"] == "call"
        assert call["call"]["method"] == "ui.notify"
        notified.append(call["call"]["args"])
        _write_frame(conn, {"type": "call_result", "id": call["id"], "call_result": {"result": {}}})

        resp = _read_frame(conn)
        assert resp["response"]["result"]["content"] == "done"
        assert notified[0]["message"] == "hi"
    finally:
        _write_frame(conn, {"type": "shutdown", "shutdown": {"reason": "test"}})
        conn.close()
        listener.close()
        t.join(timeout=2)


class _FakeStore:
    def __init__(self) -> None:
        self._stored = None

    def credential_status(self) -> pig_sdk.OAuthCredentialStatus:
        return pig_sdk.OAuthCredentialStatus(present=self._stored is not None, auth_type="oauth", source="fake")

    def store_credentials(self, creds: pig_sdk.OAuthCredentials) -> str:
        self._stored = creds
        return "/fake/path"

    def delete_credentials(self) -> bool:
        self._stored = None
        return True


def test_oauth_provider_bridge() -> None:
    """The register payload advertises OAuth capability flags (never callables), and
    oauth_* requests dispatch to the provider: login relays callbacks and round-trips a
    value-returning prompt; refresh, get_api_key, and the credential store all resolve."""
    tmp = tempfile.mkdtemp()
    sock_path = os.path.join(tmp, "ext.sock")
    listener = _start_fake_host(sock_path)

    def login(cb: pig_sdk.OAuthLoginCallbacks) -> pig_sdk.OAuthCredentials:
        cb.on_device_code(pig_sdk.OAuthDeviceCodeInfo(user_code="WXYZ", verification_uri="https://verify"))
        value = cb.on_prompt(pig_sdk.OAuthPrompt(message="URL"))
        return pig_sdk.OAuthCredentials(access=f"tok:{value}", expires=999)

    ext = pig_sdk.Extension("py-oauth")
    ext.register_oauth_provider(
        "example-provider",
        {"name": "Example Provider"},
        pig_sdk.OAuthProvider(
            name="Example Provider",
            is_subscription=True,
            login=login,
            refresh_token=lambda creds: pig_sdk.OAuthCredentials(access="fresh", refresh="r2", expires=42),
            get_api_key=lambda creds: f"key-for-{creds.access}",
            credential_store=_FakeStore(),
        ),
    )

    t = threading.Thread(target=ext.run_with_socket, args=(sock_path,), daemon=True)
    t.start()
    conn, _ = listener.accept()
    try:
        reg = _read_frame(conn)
        oauth = reg["register"]["providers"][0]["config"]["oauth"]
        assert oauth["name"] == "Example Provider"
        assert oauth["isSubscription"] is True
        assert oauth["has_login"] is True
        assert oauth["has_refresh"] is True
        assert oauth["has_get_api_key"] is True
        assert oauth["has_credential_store"] is True

        _write_frame(conn, {"type": "ready", "ready": {"cwd": tmp, "width": 80}})

        # oauth_login: extension drives callbacks, then returns creds.
        _write_frame(conn, {"type": "request", "id": "login-1",
                            "request": {"method": "oauth_login", "tool": "example-provider"}})

        dc = _read_frame(conn)
        assert dc["type"] == "call"
        assert dc["call"]["method"] == "oauth.cb.onDeviceCode"
        assert dc["call"]["parent_request_id"] == "login-1"
        assert dc["call"]["args"]["userCode"] == "WXYZ"
        _write_frame(conn, {"type": "call_result", "id": dc["id"], "call_result": {}})

        pr = _read_frame(conn)
        assert pr["call"]["method"] == "oauth.cb.onPrompt"
        _write_frame(conn, {"type": "call_result", "id": pr["id"],
                            "call_result": {"result": {"value": "typed-value"}}})

        login_resp = _read_frame(conn)
        assert login_resp["id"] == "login-1"
        assert login_resp["response"]["result"]["access"] == "tok:typed-value"
        assert login_resp["response"]["result"]["expires"] == 999

        # oauth_get_api_key.
        _write_frame(conn, {"type": "request", "id": "key-1",
                            "request": {"method": "oauth_get_api_key", "tool": "example-provider", "args": {"access": "abc"}}})
        assert _read_frame(conn)["response"]["result"]["apiKey"] == "key-for-abc"

        # oauth_refresh.
        _write_frame(conn, {"type": "request", "id": "ref-1",
                            "request": {"method": "oauth_refresh", "tool": "example-provider", "args": {"access": "abc"}}})
        ref = _read_frame(conn)["response"]["result"]
        assert ref["access"] == "fresh"
        assert ref["refresh"] == "r2"

        # oauth_store_credentials then oauth_credential_status reflect the store.
        _write_frame(conn, {"type": "request", "id": "store-1",
                            "request": {"method": "oauth_store_credentials", "tool": "example-provider", "args": {"access": "abc"}}})
        assert _read_frame(conn)["response"]["result"]["path"] == "/fake/path"

        _write_frame(conn, {"type": "request", "id": "status-1",
                            "request": {"method": "oauth_credential_status", "tool": "example-provider"}})
        status = _read_frame(conn)["response"]["result"]
        assert status["present"] is True
        assert status["source"] == "fake"

        # Unknown provider fails the request.
        _write_frame(conn, {"type": "request", "id": "bad-1",
                            "request": {"method": "oauth_login", "tool": "nope"}})
        assert _read_frame(conn)["response"]["error"] is not None
    finally:
        _write_frame(conn, {"type": "shutdown", "shutdown": {"reason": "test"}})
        conn.close()
        listener.close()
        t.join(timeout=2)


# ── on_width_change ───────────────────────────────────────────────────────────
#
# A header/footer/widget is sent to the host as static lines, so unlike
# upstream's component factories it does not follow a resize. on_width_change is
# the trigger an extension re-pushes from.


def _width_notify(width):
    return {"type": "notify", "notify": {"method": "width_change", "args": {"width": width}}}


def test_on_width_change_delivers_new_width():
    ext = pig_sdk.Extension("py-width")
    ctx = pig_sdk.Context(ext, None, None)

    seen = []
    width_during_call = []
    ctx.on_width_change(lambda w: (seen.append(w), width_during_call.append(ctx.width)))

    ext._handle_notify(_width_notify(100))
    ext._handle_notify(_width_notify(42))

    assert seen == [100, 42]
    # The handler must observe the updated width, or a re-push would rebuild the
    # lines at the width that just became stale.
    assert width_during_call == [100, 42]


def test_on_width_change_unsubscribe_is_idempotent():
    ext = pig_sdk.Extension("py-width-unsub")
    ctx = pig_sdk.Context(ext, None, None)

    calls = []
    unsubscribe = ctx.on_width_change(calls.append)

    ext._handle_notify(_width_notify(80))
    unsubscribe()
    unsubscribe()  # must not raise or remove another subscriber
    ext._handle_notify(_width_notify(90))

    assert calls == [80]


def test_on_width_change_ignores_non_positive_width():
    ext = pig_sdk.Extension("py-width-zero")
    ctx = pig_sdk.Context(ext, None, None)

    calls = []
    ctx.on_width_change(calls.append)
    ext._handle_notify(_width_notify(0))

    assert calls == []


def test_on_width_change_rejects_non_callable():
    ext = pig_sdk.Extension("py-width-bad")
    ctx = pig_sdk.Context(ext, None, None)
    try:
        ctx.on_width_change("not callable")
    except TypeError:
        return
    raise AssertionError("a non-callable handler was accepted; it would fail on the first resize")


def test_heartbeat_and_request_state_bypass_handlers() -> None:
    tmp = tempfile.mkdtemp()
    sock_path = os.path.join(tmp, "ext.sock")
    listener = _start_fake_host(sock_path)
    ext = pig_sdk.Extension("py-liveness")
    ext.command("done", "complete immediately", lambda _ctx, _args: None)
    thread = threading.Thread(target=ext.run_with_socket, args=(sock_path,), daemon=True)
    thread.start()

    conn, _ = listener.accept()
    try:
        assert _read_frame(conn)["type"] == "register"
        _write_frame(conn, {"type": "ready", "ready": {"cwd": tmp, "width": 80}})
        _write_frame(conn, {"type": "ping", "ping": {"nonce": "heartbeat-1"}})
        assert _read_frame_raw(conn) == {"type": "pong", "pong": {"nonce": "heartbeat-1"}}

        _write_frame(conn, {"type": "request", "id": "req-state",
                            "request": {"method": "command", "tool": "done"}})
        started = _read_frame_raw(conn)
        completed = _read_frame_raw(conn)
        response = _read_frame_raw(conn)
        assert started["request_state"] == {"request_id": "req-state", "state": "started"}
        assert completed["request_state"] == {"request_id": "req-state", "state": "completed"}
        assert response["type"] == "response"
        assert response["id"] == "req-state"
        _write_frame(conn, {"type": "shutdown", "shutdown": {"reason": "done"}})
        thread.join(2)
        assert not thread.is_alive()
    finally:
        conn.close()
        listener.close()


def test_user_wait_reports_blocked_and_parents_host_call() -> None:
    tmp = tempfile.mkdtemp()
    sock_path = os.path.join(tmp, "ext.sock")
    listener = _start_fake_host(sock_path)
    ext = pig_sdk.Extension("py-blocked")
    ext.command("ask", "wait for input", lambda ctx, _args: ctx.input("Question", "Answer"))
    thread = threading.Thread(target=ext.run_with_socket, args=(sock_path,), daemon=True)
    thread.start()
    conn, _ = listener.accept()
    try:
        assert _read_frame(conn)["type"] == "register"
        _write_frame(conn, {"type": "ready", "ready": {"cwd": tmp, "width": 80}})
        _write_frame(conn, {"type": "request", "id": "req-user", "request": {"method": "command", "tool": "ask"}})
        assert _read_frame_raw(conn)["request_state"] == {"request_id": "req-user", "state": "started"}
        assert _read_frame_raw(conn)["request_state"] == {"request_id": "req-user", "state": "blocked", "reason": "user"}
        call = _read_frame_raw(conn)
        assert call["call"]["method"] == "ui.input"
        assert call["call"]["parent_request_id"] == "req-user"
        _write_frame(conn, {"type": "ping", "ping": {"nonce": "while-blocked"}})
        assert _read_frame_raw(conn) == {"type": "pong", "pong": {"nonce": "while-blocked"}}
        _write_frame(conn, {"type": "call_result", "id": call["id"], "call_result": {"result": {"text": "ok", "ok": True}}})
        assert _read_frame_raw(conn)["request_state"]["state"] == "progress"
        assert _read_frame_raw(conn)["request_state"]["state"] == "completed"
        assert _read_frame_raw(conn)["type"] == "response"
        _write_frame(conn, {"type": "shutdown", "shutdown": {"reason": "done"}})
        thread.join(2)
        assert not thread.is_alive()
    finally:
        conn.close()
        listener.close()


def test_set_login_sends_canonical_definition_as_call_args() -> None:
    ext = pig_sdk.Extension("py-login")
    ctx = pig_sdk.Context(ext)
    calls: list[tuple[str, object]] = []
    ext._call = lambda method, args=None, request_id="": calls.append((method, args)) or {}  # type: ignore[method-assign]

    definition = pig_sdk.LoginDefinition(
        brand=["A" * 41] * 5,
        hero=["A" * 32] * 14,
        mascot=["A" * 16] * 14,
        palette={"A": "#112233"},
        name="Example Bot",
        description="Custom agent",
        tagline="Build clearly.",
    )

    ctx.set_login(definition)

    assert calls == [(
        "ui.setLogin",
        {
            "brand": ["A" * 41] * 5,
            "hero": ["A" * 32] * 14,
            "mascot": ["A" * 16] * 14,
            "palette": {"A": "#112233"},
            "name": "Example Bot",
            "description": "Custom agent",
            "tagline": "Build clearly.",
        },
    )]


def test_set_login_does_not_wrap_definition_or_replace_clear_header() -> None:
    ext = pig_sdk.Extension("py-login-clear")
    ctx = pig_sdk.Context(ext)
    calls: list[tuple[str, object]] = []
    ext._call = lambda method, args=None, request_id="": calls.append((method, args)) or {}  # type: ignore[method-assign]
    definition = pig_sdk.LoginDefinition(
        brand=["A" * 41] * 5,
        hero=["A" * 32] * 14,
        mascot=["A" * 16] * 14,
        palette={"A": "#112233"},
        name="Pig",
        description="Coding agent",
        tagline="Ready.",
    )

    ctx.set_login(definition)
    ctx.clear_header()

    assert calls[0][1] == definition._to_wire()
    assert "definition" not in calls[0][1]
    assert calls[1] == ("ui.setHeader", {"clear": True})


def test_set_label_raises_host_error() -> None:
    """Upstream setLabel throws when the session cannot record the label."""
    tmp = tempfile.mkdtemp()
    sock_path = os.path.join(tmp, "ext.sock")
    listener = _start_fake_host(sock_path)

    def label(ctx: pig_sdk.Context, _args: str) -> None:
        ctx.set_label("missing-entry", "tag")

    ext = pig_sdk.Extension("py-label")
    ext.command("label", "label an entry", label)
    t = threading.Thread(target=ext.run_with_socket, args=(sock_path,), daemon=True)
    t.start()

    conn, _ = listener.accept()
    try:
        assert _read_frame(conn)["type"] == "register"
        _write_frame(conn, {"type": "ready", "ready": {"cwd": tmp, "width": 80}})
        _write_frame(conn, {"type": "request", "id": "r1", "request": {"method": "command", "tool": "label"}})
        while True:
            env = _read_frame(conn)
            if env["type"] == "call" and env["call"]["method"] == "setLabel":
                _write_frame(conn, {"type": "call_result", "id": env["id"], "call_result": {"error": {"message": "Entry missing-entry not found"}}})
                continue
            if env["type"] == "response":
                break
        error = env["response"].get("error") or {}
        assert "Entry missing-entry not found" in error.get("message", ""), env
    finally:
        _write_frame(conn, {"type": "shutdown", "shutdown": {"reason": "test"}})
        conn.close()
        listener.close()
        t.join(timeout=2)


def test_get_all_tools_and_commands_return_upstream_info() -> None:
    """getAllTools and getCommands answer with upstream's ToolInfo and SlashCommandInfo."""
    tool = {
        "name": "grep",
        "description": "Search file contents for a pattern.",
        "parameters": {"type": "object", "required": ["pattern"], "properties": {"pattern": {"type": "string"}}},
        "sourceInfo": {"path": "<builtin:grep>", "source": "builtin", "scope": "temporary", "origin": "top-level"},
    }
    command = {
        "name": "probe",
        "description": "Probe command",
        "source": "extension",
        "sourceInfo": {"path": "/x/probe.py", "source": "cli", "scope": "temporary", "origin": "top-level"},
    }
    ext = pig_sdk.Extension("py-info")
    calls = []
    results = {"getAllTools": {"tools": [tool]}, "getCommands": {"commands": [command]}}
    ext._call = lambda method, args=None, request_id="": calls.append(method) or {"result": results[method]}  # type: ignore[method-assign]
    ctx = pig_sdk.Context(extension=ext)
    assert ctx.get_all_tools() == [tool]
    assert ctx.get_commands() == [command]
    assert calls == ["getAllTools", "getCommands"]
