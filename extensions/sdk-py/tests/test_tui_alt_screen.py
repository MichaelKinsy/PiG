"""TuiAltScreen's wire encoding: the ``tui.alt`` calls it sends and the ``tui.alt.cb`` requests it answers.

The host is a fake: it records each ``tui.alt`` call and answers from a table, and the tests send the host's callbacks through the connection's dispatcher.
"""

from __future__ import annotations

import threading
from dataclasses import dataclass
from typing import Any

import pytest

import pig_sdk


class _Terminal:
    columns = 20
    rows = 6
    kitty_protocol_active = False

    def __init__(self) -> None:
        self.calls: list[tuple[Any, ...]] = []

    def start(self, on_input, on_resize) -> None:
        self.on_input, self.on_resize = on_input, on_resize
        self.calls.append(("start",))

    def stop(self) -> None:
        self.calls.append(("stop",))

    def drain_input(self, max_ms=None, idle_ms=None) -> None:
        self.calls.append(("drain_input", max_ms, idle_ms))

    def write(self, data: str) -> None:
        self.calls.append(("write", data))

    def move_by(self, lines: int) -> None:
        self.calls.append(("move_by", lines))

    def hide_cursor(self) -> None:
        self.calls.append(("hide_cursor",))

    def show_cursor(self) -> None:
        self.calls.append(("show_cursor",))

    def clear_line(self) -> None:
        self.calls.append(("clear_line",))

    def clear_from_cursor(self) -> None:
        self.calls.append(("clear_from_cursor",))

    def clear_screen(self) -> None:
        self.calls.append(("clear_screen",))

    def set_title(self, title: str) -> None:
        self.calls.append(("set_title", title))

    def set_progress(self, active: bool) -> None:
        self.calls.append(("set_progress", active))

    def set_program_status(self, status) -> None:
        self.calls.append(("set_program_status", status))

    def restore_on_exit(self, before, after) -> None:
        self.calls.append(("restore_on_exit", before, after))


class _Lines:
    def __init__(self, *lines: str) -> None:
        self.lines = list(lines)

    def render(self, width: int) -> list[str]:
        return [f"{line}@{width}" for line in self.lines]


class _Full(_Lines):
    wants_key_release = True

    def __init__(self) -> None:
        super().__init__("full")
        self.focused = False
        self.inputs: list[str] = []
        self.invalidated = 0

    def handle_input(self, data: str) -> None:
        self.inputs.append(data)

    def handle_mouse(self, event):
        return {"handled": True, "x": event.x} if event.type == "click" else None

    def invalidate(self) -> None:
        self.invalidated += 1


class _Host:
    """Records the tui.alt calls and answers them from ``results`` (op -> result)."""

    def __init__(self) -> None:
        self.calls: list[dict[str, Any]] = []
        self.results: dict[str, Any] = {}
        self.rests: list[tuple[Any, ...]] = []

    def call(self, method: str, args: Any = None, *rest: Any) -> dict[str, Any]:
        assert method == "tui.alt"
        self.calls.append(args)
        self.rests.append(rest)
        return {"result": self.results.get(args["op"], {})}

    def last(self) -> dict[str, Any]:
        return self.calls[-1]


def _screen(**options: Any) -> tuple[pig_sdk.Extension, _Host, _Terminal, pig_sdk.TuiAltScreen]:
    ext = pig_sdk.Extension("tui-alt-test")
    host = _Host()
    ext._call = host.call  # type: ignore[method-assign]
    terminal = _Terminal()
    screen = pig_sdk.TuiAltScreen(pig_sdk.Context(ext), terminal, False, None, pig_sdk.TuiAltScreenOptions(**options) if options else None)
    return ext, host, terminal, screen


def _cb(ext: pig_sdk.Extension, screen: pig_sdk.TuiAltScreen, **args: Any) -> Any:
    return ext._tui_alt.dispatch(pig_sdk.Context(ext), {"id": screen._id, **args})


def test_create_carries_the_options_and_the_terminal_block() -> None:
    _, host, _, _ = _screen(mouse=False, wheel_scroll_lines="auto", copy_on_select=True, copy_selection=lambda text: True, open_url=lambda url: None)
    assert host.calls == [{
        "id": "t1", "op": "create", "showHardwareCursor": False,
        "options": {"wheelScrollLines": "auto", "mouse": False, "copyOnSelect": True, "callbacks": ["copySelection", "openUrl"]},
        "terminal": {"columns": 20, "rows": 6, "kitty": False},
    }]


def test_screen_ids_are_unique_per_connection() -> None:
    ext, _, terminal, first = _screen()
    second = pig_sdk.TuiAltScreen(pig_sdk.Context(ext), terminal)
    assert (first._id, second._id) == ("t1", "t2")


def test_every_call_reads_the_terminal_block_afresh() -> None:
    _, host, terminal, screen = _screen()
    terminal.columns, terminal.rows, terminal.kitty_protocol_active = 80, 24, True
    screen.invalidate()
    assert host.last() == {"id": "t1", "op": "invalidate", "terminal": {"columns": 80, "rows": 24, "kitty": True}}


def test_simple_ops_and_their_fields() -> None:
    _, host, _, screen = _screen()
    screen.scroll_by(-2)
    assert host.last()["op"] == "scrollBy" and host.last()["lines"] == -2
    screen.flash("hi")
    assert "durationMs" not in host.last()
    screen.flash("hi", 500)
    assert host.last()["durationMs"] == 500
    screen.render_now()
    assert "force" not in host.last()
    screen.request_render(True)
    assert host.last()["op"] == "requestRender" and host.last()["force"] is True
    screen.stop()
    assert "preserveScreen" not in host.last()
    screen.stop(preserve_screen=True)
    assert host.last()["preserveScreen"] is True
    screen.set_wheel_scroll_lines("auto")
    assert host.last()["lines"] == "auto"
    for method, op in ((screen.set_copy_on_select, "setCopyOnSelect"), (screen.set_show_hardware_cursor, "setShowHardwareCursor"), (screen.set_clear_on_shrink, "setClearOnShrink")):
        method(True)
        assert host.last()["op"] == op and host.last()["enabled"] is True


def test_reads_come_from_one_state_call() -> None:
    _, host, _, screen = _screen()
    host.results["state"] = {
        "viewportTop": 4, "isFollowingOutput": True, "fullRedraws": 3, "hasOverlay": True, "hasOverlayEntries": False, "hasActiveSelection": True,
        "copyOnSelect": True, "showHardwareCursor": True, "clearOnShrink": False, "focusedKey": None,
    }
    assert (screen.viewport_top, screen.is_following_output, screen.full_redraws, screen.has_overlay(), screen.has_overlay_entries) == (4, True, 3, True, False)
    assert (screen.has_active_selection(), screen.get_copy_on_select(), screen.get_show_hardware_cursor(), screen.get_clear_on_shrink()) == (True, True, True, False)
    assert screen.get_focused_component() is None
    assert all(call["op"] == "state" for call in host.calls[1:])
    assert screen.mode == "fullscreen" and screen.viewport_tui is True


def test_children_keys_and_caps() -> None:
    _, host, _, screen = _screen()
    plain, full = _Lines("a"), _Full()
    screen.add_child(plain)
    assert host.last() == {"id": "t1", "op": "addChild", "key": "c1", "caps": {"input": False, "mouse": False, "focusable": False, "wantsKeyRelease": False}, "terminal": host.last()["terminal"]}
    screen.add_child(full)
    assert host.last()["caps"] == {"input": True, "mouse": True, "focusable": True, "wantsKeyRelease": True}
    assert screen.children == [plain, full]
    screen.set_focus(full)
    assert host.last()["key"] == "c2"  # the same object, the same key
    host.results["state"] = {"focusedKey": "c2"}
    assert screen.get_focused_component() is full
    screen.set_focus(None)
    assert host.last()["key"] is None and "caps" not in host.last()
    screen.set_layout_root(plain)
    assert host.last()["op"] == "setLayoutRoot" and host.last()["key"] == "c1"
    screen.set_layout_root(None)
    assert host.last()["key"] is None
    screen.remove_child(plain)
    assert host.last() == {"id": "t1", "op": "removeChild", "key": "c1", "terminal": host.last()["terminal"]}
    assert screen.children == [full]
    before = len(host.calls)
    screen.remove_child(plain)  # not a child: Pi's no-op, no host call
    assert len(host.calls) == before
    screen.clear()
    assert screen.children == []


def test_overlay_and_its_handle() -> None:
    _, host, _, screen = _screen()
    over = _Lines("o")
    handle = screen.show_overlay(over, {"width": 8, "anchor": "center"})
    assert host.last()["op"] == "showOverlay" and host.last()["options"] == {"width": 8, "anchor": "center"}
    host.results["overlay"] = {"hidden": True, "focused": False, "visible": False, "bounds": {"row": 1, "col": 2, "width": 8, "height": 1}}
    handle.set_hidden(True)
    assert host.last()["action"] == "setHidden" and host.last()["hidden"] is True and host.last()["key"] == "c1"
    assert handle.is_hidden() is True and host.last()["action"] == "state"
    assert handle.get_bounds() == {"row": 1, "col": 2, "width": 8, "height": 1}
    handle.unfocus()
    assert "target" not in host.last()
    handle.unfocus(None)
    assert host.last()["target"] is None
    handle.unfocus(over)
    assert host.last()["target"] == {"key": "c1"}
    with pytest.raises(ValueError):
        handle.unfocus(_Lines("stranger"))
    handle.focus()
    handle.hide()
    assert host.last()["action"] == "hide"
    screen.hide_overlay()
    assert host.last()["op"] == "hideOverlay"
    with pytest.raises(ValueError):
        screen.show_overlay(over, {"bogus": 1})
    with pytest.raises(TypeError):
        screen.show_overlay(over, {"visible": True})


def test_overlay_visible_callback() -> None:
    ext, host, _, screen = _screen()
    over = _Lines("o")
    asked: list[tuple[int, int]] = []

    def visible(width: int, height: int) -> bool:
        asked.append((width, height))
        return width >= 20

    screen.show_overlay(over, {"width": 8, "visible": visible})
    assert host.last()["options"] == {"width": 8} and host.last()["visible"] is True  # the callback itself stays here
    assert _cb(ext, screen, kind="visible", key="c1", termWidth=20, termHeight=6) == {"value": True}
    assert _cb(ext, screen, kind="visible", key="c1", termWidth=10, termHeight=6) == {"value": False}
    assert asked == [(20, 6), (10, 6)]
    screen.show_overlay(over, {"width": 8})  # the latest show of the component names its callback
    assert host.last()["options"] == {"width": 8}
    with pytest.raises(RuntimeError):
        _cb(ext, screen, kind="visible", key="c1", termWidth=20, termHeight=6)


def test_input_listeners() -> None:
    ext, host, _, screen = _screen()
    seen: list[str] = []

    def listener(data: str):
        seen.append(data)
        return {"consume": True, "data": "x"} if data == "c" else None

    remove = screen.add_input_listener(listener)
    assert host.last()["op"] == "addInputListener" and host.last()["listenerId"] == "l1"
    count = len(host.calls)
    screen.add_input_listener(listener)  # a Set: no second registration
    assert len(host.calls) == count
    assert _cb(ext, screen, kind="listener", listenerId="l1", data="c") == {"consume": True, "data": "x"}
    assert _cb(ext, screen, kind="listener", listenerId="l1", data="d") == {}
    remove()
    assert host.last()["op"] == "removeInputListener" and host.last()["listenerId"] == "l1"
    assert _cb(ext, screen, kind="listener", listenerId="l1", data="c") == {}
    assert seen == ["c", "d"]


def test_on_debug_and_color_scheme_and_late_colors() -> None:
    ext, host, _, screen = _screen()
    hits: list[str] = []
    screen.on_debug = lambda: hits.append("debug")
    assert host.last()["op"] == "setOnDebug" and host.last()["enabled"] is True
    _cb(ext, screen, kind="debug")
    screen.on_debug = None
    assert host.last()["enabled"] is False
    _cb(ext, screen, kind="debug")
    assert hits == ["debug"]
    schemes: list[str] = []
    unsubscribe = screen.on_terminal_color_scheme_change(schemes.append)
    screen.set_terminal_color_scheme_notifications(True)
    assert host.last()["op"] == "setTerminalColorSchemeNotifications" and host.last()["enabled"] is True
    _cb(ext, screen, kind="colorScheme", scheme="dark")
    unsubscribe()
    _cb(ext, screen, kind="colorScheme", scheme="light")
    assert schemes == ["dark"]
    late: list[Any] = []
    host.results["queryTerminalColors"] = {"colors": {"foreground": {"r": 1, "g": 2, "b": 3}}}
    assert screen.query_terminal_colors(20) == {"foreground": {"r": 1, "g": 2, "b": 3}}
    assert host.last()["onLateReply"] is False and "queryId" not in host.last()
    screen.query_terminal_colors(30, late.append)
    assert host.last()["timeoutMs"] == 30 and host.last()["onLateReply"] is True and host.last()["queryId"] == "q1"
    _cb(ext, screen, kind="lateColors", queryId="q1", colors={"background": {"r": 0, "g": 0, "b": 0}})
    assert late == [{"background": {"r": 0, "g": 0, "b": 0}}]


def test_mouse_dispatch_and_results() -> None:
    _, host, _, screen = _screen()
    host.results["handleMouse"] = {"result": {"handled": True}}
    event = pig_sdk.MouseEvent(type="wheel", button="none", width=20, height=6, wheel_delta=1)
    assert screen.handle_mouse(event) == {"handled": True}
    assert host.last()["event"] == {
        "type": "wheel", "button": "none", "x": 0, "y": 0, "screenX": 0, "screenY": 0, "width": 20, "height": 6, "shift": False, "alt": False, "ctrl": False, "wheelDelta": 1,
    }
    host.results["handleMouse"] = {"result": None}
    assert screen.handle_mouse({"type": "move"}) is None
    assert host.last()["event"] == {"type": "move"}


def test_copy_active_selection_and_screen_lines() -> None:
    _, host, _, screen = _screen()
    host.results["copyActiveSelectionToClipboard"] = {"value": True}
    host.results["getScreenLines"] = {"lines": ["a", "b"]}
    host.results["render"] = {"lines": ["r"]}
    assert screen.copy_active_selection_to_clipboard() is True
    assert screen.get_screen_lines() == ["a", "b"]
    assert screen.render(10) == ["r"] and host.last()["width"] == 10


def test_terminal_callbacks() -> None:
    ext, host, terminal, screen = _screen()
    _cb(ext, screen, kind="terminal", method="start")
    terminal.on_input("k")
    assert host.last()["op"] == "input" and host.last()["data"] == "k"
    terminal.on_resize()
    assert host.last()["op"] == "resize"
    for method, args, expected in (
        ("write", {"data": "abc"}, ("write", "abc")), ("write", {"data": ""}, ("write", "")), ("moveBy", {"lines": -3}, ("move_by", -3)), ("hideCursor", {}, ("hide_cursor",)),
        ("showCursor", {}, ("show_cursor",)), ("clearLine", {}, ("clear_line",)), ("clearFromCursor", {}, ("clear_from_cursor",)), ("clearScreen", {}, ("clear_screen",)),
        ("setTitle", {"title": "T"}, ("set_title", "T")), ("setProgress", {"active": True}, ("set_progress", True)),
        ("setProgramStatus", {"status": {"state": "working", "app": "pi"}}, ("set_program_status", {"state": "working", "app": "pi"})),
        ("restoreOnExit", {"before": "b", "after": "a"}, ("restore_on_exit", "b", "a")), ("drainInput", {"maxMs": 1000, "idleMs": 50}, ("drain_input", 1000, 50)), ("stop", {}, ("stop",)),
    ):
        assert _cb(ext, screen, kind="terminal", method=method, **args) == {}
        assert terminal.calls[-1] == expected
    with pytest.raises(RuntimeError):
        _cb(ext, screen, kind="terminal", method="bogus")


def test_component_callbacks() -> None:
    ext, host, _, screen = _screen()
    full, plain = _Full(), _Lines("p")
    screen.add_child(full)
    screen.add_child(plain)
    assert _cb(ext, screen, kind="render", key="c1", width=7) == {"lines": ["full@7"]}
    _cb(ext, screen, kind="input", key="c1", data="x")
    _cb(ext, screen, kind="input", key="c2", data="ignored")
    assert full.inputs == ["x"]
    _cb(ext, screen, kind="focus", key="c1", focused=True)
    assert full.focused is True
    _cb(ext, screen, kind="invalidate", key="c1")
    _cb(ext, screen, kind="invalidate", key="c2")
    assert full.invalidated == 1
    assert _cb(ext, screen, kind="mouse", key="c1", event={"type": "click", "x": 3}) == {"result": {"handled": True, "x": 3}}
    assert _cb(ext, screen, kind="mouse", key="c1", event={"type": "move"}) == {"result": None}
    with pytest.raises(RuntimeError):
        _cb(ext, screen, kind="render", key="c9", width=1)
    with pytest.raises(RuntimeError):
        ext._tui_alt.dispatch(pig_sdk.Context(ext), {"id": "nope", "kind": "debug"})


def test_option_callbacks() -> None:
    ext, _, _, screen = _screen(
        copy_selection=lambda text: "no clipboard" if text == "bad" else True, open_url=lambda url: opened.append(url), search_match_style=lambda text: f"<{text}>",
        search_current_match_style=lambda text: f"[{text}]", search_navigation_button_style=lambda text, hovered: f"{text}:{hovered}", scroll_to_end_indicator=lambda: "v",
        on_right_click_paste=lambda: opened.append("paste"),
    )
    opened: list[str] = []

    def option(name: str, *args: Any) -> Any:
        return _cb(ext, screen, kind="option", name=name, args=list(args))

    assert option("copySelection", "ok") == {"value": True}
    assert option("copySelection", "bad") == {"value": "no clipboard"}
    assert option("openUrl", "https://x") == {"value": None}
    assert option("onRightClickPaste") == {"value": None}
    assert opened == ["https://x", "paste"]
    assert option("searchMatchStyle", "m") == {"value": "<m>"}
    assert option("searchCurrentMatchStyle", "m") == {"value": "[m]"}
    assert option("searchNavigationButtonStyle", "b", True) == {"value": "b:True"}
    assert option("scrollToEndIndicator") == {"value": "v"}
    bare_ext, _, _, bare = _screen()
    with pytest.raises(RuntimeError):
        _cb(bare_ext, bare, kind="option", name="copySelection", args=["x"])


def test_connection_close_ends_the_screens() -> None:
    ext, _, _, screen = _screen()
    ext._tui_alt.close_all()
    with pytest.raises(RuntimeError):
        _cb(ext, screen, kind="debug")
    with pytest.raises(RuntimeError):
        pig_sdk.TuiAltScreen(pig_sdk.Context(ext), _Terminal())


def test_calls_inside_a_callback_carry_no_in_frame_key() -> None:
    ext, host, _, screen = _screen(search_match_style=lambda text: str(screen.has_overlay()))
    ext._request_state = lambda *args, **kwargs: None  # type: ignore[method-assign]
    parent = pig_sdk._RequestParent("r7", ext._sock)
    cb_ctx = pig_sdk.Context(ext, None, "r7", threading.Event(), _parent=parent)
    inside = _Lines("x")
    inside.render = lambda width: [str(screen.viewport_top)]  # type: ignore[method-assign]
    screen.add_child(inside)
    assert "inFrame" not in host.last()
    for args in (
        {"kind": "render", "key": "c1", "width": 5}, {"kind": "option", "name": "searchMatchStyle", "args": ["m"]},
    ):
        start, rests = len(host.calls), len(host.rests)
        ext._tui_alt.dispatch(cb_ctx, {"id": screen._id, **args})
        assert host.rests[rests:] == [("r7", parent)], args
        assert len(host.calls) == start + 1 and "inFrame" not in host.calls[-1], args


def test_equal_components_stay_distinct() -> None:
    @dataclass
    class Box:
        text: str

        def render(self, width: int) -> list[str]:
            return [self.text]

    _, host, _, screen = _screen()
    a, b = Box("same"), Box("same")
    assert a == b
    screen.add_child(a)
    screen.add_child(b)
    key_b = screen._key_of(b)
    assert screen._key_of(a) != key_b
    screen.remove_child(b)
    assert len(screen.children) == 1 and screen.children[0] is a
    assert host.last()["op"] == "removeChild" and host.last()["key"] == key_b



def test_calls_inside_a_callback_are_children_of_its_request() -> None:
    ext, host, _, screen = _screen(search_match_style=lambda text: str(screen.has_overlay()))
    ext._request_state = lambda *args, **kwargs: None  # type: ignore[method-assign]
    parent = pig_sdk._RequestParent("r7", ext._sock)
    cb_ctx = pig_sdk.Context(ext, None, "r7", threading.Event(), _parent=parent)
    inside = _Lines("x")
    inside.render = lambda width: [str(screen.viewport_top)]  # type: ignore[method-assign]
    screen.add_child(inside)
    screen.set_focus(inside)
    assert ("r7", parent) not in host.rests
    for args in (
        {"kind": "render", "key": "c1", "width": 5}, {"kind": "option", "name": "searchMatchStyle", "args": ["m"]},
        {"kind": "invalidate", "key": "c1"},
    ):
        start = len(host.rests)
        ext._tui_alt.dispatch(cb_ctx, {"id": screen._id, **args})
        assert host.rests[start:] == ([("r7", parent)] if args["kind"] != "invalidate" else []), args
    # A callback that calls the host from its own code: the focus and listener callbacks of the user are the same path.
    seen: list[Any] = []
    inside.handle_input = lambda data: (screen.invalidate(), seen.append(host.rests[-1]))  # type: ignore[attr-defined]
    ext._tui_alt.dispatch(cb_ctx, {"id": screen._id, "kind": "input", "key": "c1", "data": "k"})
    assert seen == [("r7", parent)]
    # Once the callback is answered, the thread's calls have no parent again.
    screen.invalidate()
    assert host.rests[-1] == ()
