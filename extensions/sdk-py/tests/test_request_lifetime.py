import contextlib
import json
import socket
import struct
import tempfile
import threading

import pig_sdk
import pytest


@contextlib.contextmanager
def connected_extension(ext, ready=None):
    with tempfile.TemporaryDirectory() as directory:
        listener = socket.socket(socket.AF_UNIX)
        listener.bind(directory + "/sdk.sock")
        listener.listen(1)
        runtime = threading.Thread(target=ext.run_with_socket, args=(directory + "/sdk.sock",), daemon=True)
        runtime.start()
        host, _ = listener.accept()
        host.settimeout(5)

        def exact(size):
            chunks = bytearray()
            while len(chunks) < size:
                chunk = host.recv(size - len(chunks))
                assert chunk, "SDK disconnected"
                chunks.extend(chunk)
            return bytes(chunks)

        def receive(kind):
            while True:
                size = struct.unpack(">I", exact(4))[0]
                frame = json.loads(exact(size))
                if kind is None or frame["type"] == kind:
                    return frame

        def send(frame):
            data = json.dumps(frame).encode()
            host.sendall(struct.pack(">I", len(data)) + data)

        try:
            receive("register")
            send({"type": "ready", "ready": ready or {}})
            yield send, receive
        finally:
            host.close()
            listener.close()
            runtime.join(5)
            assert not runtime.is_alive()
            ext._sock.close()


def test_retained_context_uses_same_live_runtime_after_normal_response():
    # Pi runner.ts:809-886 permits retained Context calls until runtime invalidation.
    ext = pig_sdk.Extension("retained-context")
    contexts = []
    ext.command("capture", "Capture original context", lambda ctx, _: contexts.append(ctx))
    with connected_extension(ext) as (send, receive):
        send({"type": "request", "id": "origin", "request": {"method": "command", "tool": "capture"}})
        assert receive("response")["id"] == "origin"
        ctx = contexts[0]
        assert not ctx.is_cancelled()
        for expected in ["nondefault editor", "", "replacement editor"]:
            result = []
            getter = threading.Thread(target=lambda: result.append(ctx.get_editor_text()), daemon=True)
            getter.start()
            call = receive("call")
            parent = call["call"].get("parent_request_id", "")
            send({"type": "call_result", "id": call["id"], "call_result": {"result": {"text": expected}}})
            getter.join(5)
            assert not getter.is_alive()
            assert parent == "", f"completed parent {parent} was sent again"
            assert result == [expected]
        assert ext._request_parents == {}
        assert ext._pending == {}
    with pytest.raises(RuntimeError):
        ctx.call_host("ui.getEditorText")


def test_cancelled_context_is_not_promoted_after_response():
    ext = pig_sdk.Extension("cancelled-context")
    contexts = []
    entered, release = threading.Event(), threading.Event()

    def command(ctx, _):
        contexts.append(ctx)
        entered.set()
        assert release.wait(5)

    ext.command("capture", "Capture original context", command)
    with connected_extension(ext) as (send, receive):
        send({"type": "request", "id": "cancelled", "request": {"method": "command", "tool": "capture"}})
        assert entered.wait(5)
        try:
            send({"type": "cancel", "cancel": {"request_id": "cancelled"}})
            send({"type": "ping", "ping": {"nonce": "cancel-observed"}})
            assert receive("pong")["pong"]["nonce"] == "cancel-observed"
            with pytest.raises(RuntimeError):
                contexts[0].call_host("ui.getEditorText")
        finally:
            release.set()
        assert receive("response")["id"] == "cancelled"
        with pytest.raises(RuntimeError):
            contexts[0].call_host("ui.getEditorText")
        assert contexts[0].is_cancelled()


def test_failed_worker_start_releases_armed_parent(monkeypatch):
    ext = pig_sdk.Extension("failed-worker")
    ext.command("capture", "Must not enter", lambda *_: pytest.fail("failed worker ran"))
    with connected_extension(ext) as (send, receive):
        def fail_start(_thread):
            raise RuntimeError("owned worker creation failed")

        with monkeypatch.context() as patch:
            patch.setattr(threading.Thread, "start", fail_start)
            send({"type": "request", "id": "failed", "request": {"method": "command", "tool": "capture"}})
            response = receive("response")
            assert response["response"]["error"]["message"] == "start extension handler: owned worker creation failed"
            assert ext._active == {}
            assert ext._request_parents == {}


def test_parent_selection_races_response_publication():
    ext = pig_sdk.Extension("racing-parent")
    entered, release = threading.Event(), threading.Event()
    contexts = []

    def command(ctx, _):
        contexts.append(ctx)
        entered.set()
        assert release.wait(5)

    ext.command("capture", "Capture original context", command)
    with connected_extension(ext) as (send, receive):
        for i in range(50):
            entered.clear()
            release.clear()
            request_id = f"origin-{i}"
            send({"type": "request", "id": request_id, "request": {"method": "command", "tool": "capture"}})
            assert entered.wait(5)
            ctx = contexts[-1]
            outcomes = []

            def query():
                try:
                    outcomes.append(ctx.get_editor_text())
                except RuntimeError:
                    outcomes.append("cancelled")

            getter = threading.Thread(target=query, daemon=True)
            getter.start()
            release.set()
            # The wire order decides whether the getter belongs to the active request or the live runtime.
            response_seen = call_seen = False
            while not (response_seen and call_seen):
                frame = receive(None)
                if frame["type"] == "response" and frame["id"] == request_id:
                    response_seen = True
                    assert ctx._parent.state == "completed"
                elif frame["type"] == "call":
                    parent = frame["call"].get("parent_request_id", "")
                    assert parent in ("", request_id)
                    assert not (response_seen and parent), "completed parent sent after its response"
                    send({"type": "call_result", "id": frame["id"], "call_result": {"result": {"text": "current"}}})
                    call_seen = True
            getter.join(5)
            assert not getter.is_alive()
            assert outcomes in (["current"], ["cancelled"])
            # Wait for the request's registered worker, not a sleep or a guessed response delay.
            with ext._request_threads_lock:
                workers = list(ext._request_threads)
            for worker in workers:
                worker.join(5)
                assert not worker.is_alive()
            assert ctx._parent.state == "completed"
            assert ext._request_parents == {}


def test_run_with_socket_closes_its_connection_when_the_host_shuts_it_down():
    # A runner that keeps its process for later factory generations ends only this generation's connection; the host's Retire waits for that close. Go's RunWithConn closes its connection on return (sdk/extension.go:704).
    ext = pig_sdk.Extension("closing")
    with tempfile.TemporaryDirectory() as directory:
        listener = socket.socket(socket.AF_UNIX)
        listener.bind(directory + "/sdk.sock")
        listener.listen(1)
        runtime = threading.Thread(target=ext.run_with_socket, args=(directory + "/sdk.sock",), daemon=True)
        runtime.start()
        host, _ = listener.accept()
        host.settimeout(5)

        def send(frame):
            data = json.dumps(frame).encode()
            host.sendall(struct.pack(">I", len(data)) + data)

        try:
            assert host.recv(4)
            while True:
                try:
                    host.settimeout(0.2)
                    if not host.recv(65536):
                        break
                except TimeoutError:
                    break
            host.settimeout(5)
            send({"type": "ready", "ready": {}})
            send({"type": "shutdown", "shutdown": {"reason": "reload"}})
            runtime.join(5)
            assert not runtime.is_alive()
            assert host.recv(1) == b"", "the SDK left its socket open after shutdown"
        finally:
            host.close()
            listener.close()
            ext._sock.close()


STALE = "This extension ctx is stale after session replacement or reload."


def _invalidate(send, receive, message=STALE):
    send({"type": "notify", "notify": {"method": "invalidate", "args": {"message": message}}})
    send({"type": "ping", "ping": {"nonce": "invalidated"}})
    assert receive("pong")["pong"]["nonce"] == "invalidated"


def test_local_context_members_raise_the_stale_message_after_invalidate():
    # runner.ts:571-600 throws the stale message from cwd, mode, hasUI and model once the runtime is invalidated.
    ext = pig_sdk.Extension("stale-context")
    contexts = []
    ext.command("capture", "Capture", lambda ctx, _: contexts.append(ctx))
    ready = {"cwd": "/work", "mode": "tui", "model": "m", "state": {"hasUI": True}}
    with connected_extension(ext, ready) as (send, receive):
        send({"type": "request", "id": "origin", "request": {"method": "command", "tool": "capture"}})
        assert receive("response")["id"] == "origin"
        ctx = contexts[0]
        assert (ctx.cwd, ctx.mode, ctx.model, ctx.has_ui()) == ("/work", "tui", "m", True)
        _invalidate(send, receive)
        for name, read in {
            "cwd": lambda: ctx.cwd,
            "mode": lambda: ctx.mode,
            "model": lambda: ctx.model,
            "has_ui": ctx.has_ui,
        }.items():
            with pytest.raises(RuntimeError, match="stale after session replacement or reload"):
                read()
            assert name


def test_first_invalidate_message_wins():
    # runner.ts:725-727 keeps the first staleMessage.
    ext = pig_sdk.Extension("stale-first")
    contexts = []
    ext.command("capture", "Capture", lambda ctx, _: contexts.append(ctx))
    with connected_extension(ext) as (send, receive):
        send({"type": "request", "id": "origin", "request": {"method": "command", "tool": "capture"}})
        assert receive("response")["id"] == "origin"
        _invalidate(send, receive, "first")
        _invalidate(send, receive, "second")
        with pytest.raises(RuntimeError, match="^first$"):
            contexts[0].cwd


def test_loop_turned_notify_changes_nothing():
    # protocol.go NotifyLoopTurned is the host step a Node runtime waits for after a print prompt's last call. A Python
    # extension has no such wait, so the notify, which has no arguments, changes nothing: the next request runs with the
    # state the extension held before it.
    ext = pig_sdk.Extension("loop-turned")
    seen = []
    ext.command("read", "Read", lambda ctx, _: seen.append((ctx.cwd, ctx.has_ui())))
    with connected_extension(ext, {"cwd": "/work", "mode": "print", "state": {"hasUI": True}}) as (send, receive):
        send({"type": "notify", "notify": {"method": "loop_turned"}})
        send({"type": "request", "id": "after", "request": {"method": "command", "tool": "read"}})
        response = receive("response")
        assert response["id"] == "after" and response["response"].get("error") is None, response
        assert seen == [("/work", True)]
