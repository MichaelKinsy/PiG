"""Header and footer rows carry the width they were laid out for (issue #104).

Pi 0.87.1 installs a footer or header as a component factory and the TUI calls
render(width) every frame (interactive-mode.ts:2418-2480). A subprocess cannot
be called every frame, so the SDK sends the width each set of rows was laid out
for and the host never paints rows for another width. A string list widget is
content the host lays out with Text(line, 1, 0) at the current width
(interactive-mode.ts:2321-2336), so it is sent as a widget_push with no width,
which the host lays out instead of painting it as a pre-rendered frame.
"""

from __future__ import annotations

import os
import queue
import tempfile
import threading

import pytest

import pig_sdk

from test_sdk import _read_frame, _start_fake_host, _write_frame


class Host:
    """A fake host that answers every call and queues the UI calls in order."""

    def __init__(self, register) -> None:
        tmp = tempfile.mkdtemp()
        sock_path = os.path.join(tmp, "ext.sock")
        self.listener = _start_fake_host(sock_path)
        self.ext = pig_sdk.Extension("py-surface")
        register(self.ext)
        self.thread = threading.Thread(target=self.ext.run_with_socket, args=(sock_path,), daemon=True)
        self.thread.start()
        self.conn, _ = self.listener.accept()
        self.calls: queue.Queue[dict] = queue.Queue()
        self.write_lock = threading.Lock()
        # Commands whose response has not arrived. close() waits for them so a
        # handler is never writing its response to a closed socket.
        self.outstanding = 0
        self.responded = threading.Condition()
        assert _read_frame(self.conn)["type"] == "register"
        self.send({"type": "ready", "ready": {"cwd": tmp, "width": 80}})
        threading.Thread(target=self._pump, daemon=True).start()

    def send(self, env: dict) -> None:
        with self.write_lock:
            _write_frame(self.conn, env)

    def _pump(self) -> None:
        try:
            while True:
                env = _read_frame(self.conn)
                if env.get("type") == "response":
                    with self.responded:
                        self.outstanding -= 1
                        self.responded.notify_all()
                if env.get("type") == "call":
                    self.calls.put(env["call"])
                    self.send({"type": "call_result", "id": env["id"], "call_result": {}})
                # A widget_push has no reply; it is recorded as a call named
                # widget_push whose arguments are the payload.
                if env.get("type") == "widget_push":
                    self.calls.put({"method": "widget_push", "args": env["widget_push"]})
        except (EOFError, OSError):
            return

    def command(self, name: str) -> None:
        with self.responded:
            self.outstanding += 1
        self.send({"type": "request", "id": f"req-{name}", "request": {"method": "command", "tool": name, "args": {}}})

    def width(self, width: int) -> None:
        self.send({"type": "notify", "notify": {"method": "width_change", "args": {"width": width}}})

    def next(self, method: str) -> dict:
        while True:
            call = self.calls.get(timeout=5)
            if call["method"] == method:
                return call["args"]

    def quiet(self, method: str) -> None:
        try:
            call = self.calls.get(timeout=0.3)
        except queue.Empty:
            return
        assert call["method"] != method, f"unexpected {method} call: {call['args']}"

    def close(self) -> None:
        with self.responded:
            self.responded.wait_for(lambda: self.outstanding <= 0, timeout=5)
        try:
            self.send({"type": "shutdown", "shutdown": {"reason": "test"}})
        except OSError:
            pass
        self.conn.close()
        self.listener.close()
        self.thread.join(timeout=2)


@pytest.fixture
def host_factory():
    hosts: list[Host] = []

    def make(register) -> Host:
        host = Host(register)
        hosts.append(host)
        return host

    yield make
    for host in hosts:
        host.close()


@pytest.mark.parametrize("surface,method", [("footer", "ui.setFooter"), ("header", "ui.setHeader")])
def test_static_rows_are_tagged_with_the_sdk_width(host_factory, surface, method):
    def register(ext):
        def push(ctx, _args):
            getattr(ctx, f"set_{surface}")([f"rows@{ctx.width}"])

        ext.command("go", "push rows", push)

    host = host_factory(register)
    host.command("go")
    assert host.next(method) == {"lines": ["rows@80"], "width": 80}
    host.width(60)
    host.command("go")
    assert host.next(method) == {"lines": ["rows@60"], "width": 60}


@pytest.mark.parametrize("surface,method", [("footer", "ui.setFooter"), ("header", "ui.setHeader")])
def test_renderers_render_at_host_width_and_follow_resize(host_factory, surface, method):
    def register(ext):
        def install(ctx, _args):
            getattr(ctx, f"set_{surface}_renderer")(lambda width: [f"row@{width}"])

        def clear(ctx, _args):
            getattr(ctx, f"set_{surface}_renderer")(None)

        ext.command("go", "install renderer", install)
        ext.command("clear", "clear renderer", clear)

    host = host_factory(register)
    host.command("go")
    assert host.next(method) == {"lines": ["row@80"], "width": 80}
    # No extension action: the SDK re-renders at the host's new width.
    host.width(60)
    assert host.next(method) == {"lines": ["row@60"], "width": 60}
    host.width(114)
    assert host.next(method) == {"lines": ["row@114"], "width": 114}
    host.command("clear")
    assert host.next(method) == {"clear": True}
    host.width(90)
    host.quiet(method)


def test_static_footer_replaces_renderer(host_factory):
    def register(ext):
        ext.command("go", "install", lambda ctx, _a: ctx.set_footer_renderer(lambda w: [f"dyn@{w}"]))
        ext.command("static", "static", lambda ctx, _a: ctx.set_footer(["static"]))

    host = host_factory(register)
    host.command("go")
    host.next("ui.setFooter")
    host.command("static")
    assert host.next("ui.setFooter") == {"lines": ["static"], "width": 80}
    host.width(60)
    host.quiet("ui.setFooter")


def test_renderer_panic_is_reported_not_fatal(host_factory):
    def register(ext):
        def render(width):
            if width != 80:
                raise RuntimeError("boom")
            return ["ok"]

        ext.command("go", "install", lambda ctx, _a: ctx.set_footer_renderer(render))

    host = host_factory(register)
    host.command("go")
    host.next("ui.setFooter")
    host.width(60)
    assert host.next("ui.notify") == {"message": "footer render failed: boom", "level": "error"}


def test_set_widget_string_list_is_content_without_width(host_factory):
    host = host_factory(lambda ext: ext.command("go", "widget", lambda ctx, _a: ctx.set_widget("status", ["● 3 agents running"])))
    host.command("go")
    # No width: the host treats a width-less widget_push as string list content.
    assert host.next("widget_push") == {"key": "status", "lines": ["● 3 agents running"]}


def test_set_widget_from_a_width_handler_does_not_block_the_message_loop(host_factory):
    # Pi's ctx.ui.setWidget returns void without a host round trip
    # (interactive-mode.ts:2300-2340). on_width_change handlers run on the loop
    # that reads host replies, so a set_widget that waited for a reply there
    # would never return and the extension would stop reading the socket.
    def register(ext):
        def go(ctx, _args):
            ctx.on_width_change(lambda width: ctx.set_widget("fit", [f"w@{width}"]))
            ctx.set_widget("fit", [f"w@{ctx.width}"])

        ext.command("go", "subscribe", go)

    host = host_factory(register)
    host.command("go")
    assert host.next("widget_push")["lines"] == ["w@80"]
    host.width(100)
    assert host.next("widget_push")["lines"] == ["w@100"]
    # The second resize is read only if the first handler returned.
    host.width(90)
    assert host.next("widget_push")["lines"] == ["w@90"]


def test_renderer_returning_a_non_list_sends_empty_rows(host_factory):
    # The Node runtime sends [] when a component's render returns anything but
    # an array (runtime.mjs renderSpecialSurface), as the Go SDK does for nil.
    host = host_factory(lambda ext: ext.command("go", "install", lambda ctx, _a: ctx.set_footer_renderer(lambda width: None)))
    host.command("go")
    assert host.next("ui.setFooter") == {"lines": [], "width": 80}
