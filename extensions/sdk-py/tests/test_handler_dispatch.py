"""A handler that makes a blocking host call completes, whatever the host sends meanwhile.

Pi runs every extension handler in one process, and a handler can always await a
host call (packages/coding-agent/src/core/extensions/types.ts: the ``ctx.ui``
dialogs ``select``, ``confirm`` and ``input``, ``exec`` and ``executeTool`` return
promises; ``notify`` and ``setFooter`` are synchronous in-process calls). A pig handler waits on a
socket reply, so no handler may run on the thread that reads those replies.
"""

import threading

import pig_sdk
import pytest
from test_request_lifetime import connected_extension

WIDTH = {"type": "notify", "notify": {"method": "width_change", "args": {"width": 100}}}


def width(value):
    return {"type": "notify", "notify": {"method": "width_change", "args": {"width": value}}}


def command(name):
    return {"type": "request", "id": f"req-{name}", "request": {"method": "command", "tool": name}}


def answer(send, frame):
    send({"type": "call_result", "id": frame["id"], "call_result": {"result": None}})


def test_width_handler_blocking_host_call_completes():
    ext = pig_sdk.Extension("width-call")
    done = threading.Event()

    def arm(ctx, _):
        def on_width(_w):
            ctx.set_footer(["footer"])
            done.set()

        ctx.on_width_change(on_width)

    ext.command("arm", "", arm)
    with connected_extension(ext) as (send, receive):
        send(command("arm"))
        receive("response")
        send(WIDTH)
        call = receive("call")
        assert call["call"]["method"] == "ui.setFooter"
        answer(send, call)
        assert done.wait(5), "the width handler's host call never returned: the extension stopped reading replies"


def test_blocked_width_handler_does_not_stop_the_reader():
    ext = pig_sdk.Extension("width-reader")
    ext.command("ping", "", lambda ctx, _: None)

    def arm(ctx, _):
        ctx.on_width_change(lambda _w: ctx.notify("from the width handler"))

    ext.command("arm", "", arm)
    with connected_extension(ext) as (send, receive):
        send(command("arm"))
        receive("response")
        send(WIDTH)
        held = receive("call")
        # The handler's call has no reply. Pings, requests and the reply itself are still read.
        send({"type": "ping", "ping": {"nonce": "n1"}})
        assert receive("pong")["pong"]["nonce"] == "n1"
        send(command("ping"))
        assert receive("response")["id"] == "req-ping"
        answer(send, held)


def test_width_handlers_run_in_order_while_one_blocks():
    ext = pig_sdk.Extension("width-order")
    seen = []
    finished = threading.Event()

    def arm(ctx, _):
        def on_width(w):
            ctx.notify(f"w{w}")
            seen.append(w)
            if len(seen) == 3:
                finished.set()

        ctx.on_width_change(on_width)

    ext.command("arm", "", arm)
    with connected_extension(ext) as (send, receive):
        send(command("arm"))
        receive("response")
        for value in (100, 90, 80):
            send(width(value))
        for _ in range(3):
            answer(send, receive("call"))
        assert finished.wait(5)
        assert seen == [100, 90, 80]


def test_raising_width_handler_does_not_stop_the_extension():
    ext = pig_sdk.Extension("width-raise")
    seen = []
    second = threading.Event()

    def arm(ctx, _):
        def boom(_w):
            raise RuntimeError("handler failed")

        def record(w):
            seen.append(w)
            second.set()

        ctx.on_width_change(boom)
        ctx.on_width_change(record)

    ext.command("arm", "", arm)
    ext.command("ping", "", lambda ctx, _: None)
    with connected_extension(ext) as (send, receive):
        send(command("arm"))
        receive("response")
        send(width(100))
        assert second.wait(5), "a raising handler stopped the other handlers"
        send(command("ping"))
        assert receive("response")["id"] == "req-ping"
        assert seen == [100]


def test_shutdown_ends_a_width_handler_blocked_on_a_host_call():
    ext = pig_sdk.Extension("width-shutdown")
    ended = threading.Event()

    def arm(ctx, _):
        def on_width(_w):
            try:
                ctx.notify("never answered")
            finally:
                ended.set()

        ctx.on_width_change(on_width)

    ext.command("arm", "", arm)
    with connected_extension(ext) as (send, receive):
        send(command("arm"))
        receive("response")
        send(WIDTH)
        receive("call")
        send({"type": "shutdown", "shutdown": {"reason": "test"}})
        assert ended.wait(5), "shutdown left the width handler waiting"


@pytest.mark.parametrize("kind", ["event", "command", "tool"])
def test_request_handlers_complete_blocking_host_calls(kind):
    ext = pig_sdk.Extension(f"call-{kind}")
    done = threading.Event()

    def handler(ctx, *_):
        ctx.set_footer(["footer"])
        done.set()
        return {"content": "ok"}

    if kind == "event":
        ext.on_event("agent_start", handler)
        request = {"type": "request", "id": "req-1", "request": {"method": "event", "event": "agent_start", "handler_id": 1, "args": {}}}
    elif kind == "command":
        ext.command("go", "", handler)
        request = command("go")
    else:
        ext.tool("go", "go", {"type": "object"}, handler)
        request = {"type": "request", "id": "req-1", "request": {"method": "tool_call", "tool": "go", "tool_call_id": "tc", "args": {}}}
    with connected_extension(ext) as (send, receive):
        send(request)
        call = receive("call")
        assert call["call"]["method"] == "ui.setFooter"
        answer(send, call)
        assert done.wait(5)
        receive("response")


def _width_notify(value):
    return {"type": "notify", "notify": {"method": "width_change", "args": {"width": value}}}


def _drain_width_workers(ext):
    while True:
        with ext._request_threads_lock:
            workers = [t for t in ext._request_threads if t.name == "pig-width-change"]
        if not workers:
            return
        for worker in workers:
            worker.join(timeout=5)
            assert not worker.is_alive(), "a width handler did not finish"


def test_width_worker_that_cannot_start_does_not_stop_the_reader(monkeypatch):
    """As the Rust SDK's WidthDeliveries::submit: a worker thread that cannot start drops the queued widths, the reader keeps serving and the next width starts a worker."""
    ext = pig_sdk.Extension("width-start")
    seen = []
    pig_sdk.Context(ext, None, None).on_width_change(seen.append)
    real_start = threading.Thread.start

    def failing_start(thread):
        if thread.name == "pig-width-change":
            raise RuntimeError("can't start new thread")
        real_start(thread)

    monkeypatch.setattr(threading.Thread, "start", failing_start)
    ext._handle_notify(_width_notify(100))
    monkeypatch.setattr(threading.Thread, "start", real_start)
    with ext._request_threads_lock:
        assert not ext._request_threads, "an unstarted worker stays tracked and shutdown cannot join it"
    ext._handle_notify(_width_notify(90))
    _drain_width_workers(ext)
    assert seen == [90]


def test_width_handler_system_exit_does_not_stop_later_deliveries(monkeypatch):
    """A handler that raises SystemExit ends its worker thread only; the next width still reaches the handlers."""
    ext = pig_sdk.Extension("width-exit")
    ended = []
    monkeypatch.setattr(threading, "excepthook", lambda args: ended.append(args.exc_type))
    seen = []
    first = [True]

    def handler(w):
        if first[0]:
            first[0] = False
            raise SystemExit
        seen.append(w)

    pig_sdk.Context(ext, None, None).on_width_change(handler)
    ext._handle_notify(_width_notify(100))
    _drain_width_workers(ext)
    assert ended == [SystemExit]
    ext._handle_notify(_width_notify(90))
    _drain_width_workers(ext)
    assert seen == [90]
