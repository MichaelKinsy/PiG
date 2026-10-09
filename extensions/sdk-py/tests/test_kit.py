"""The component kit (D107): node encoding, image refs, the frontend flag,
and the wire of every view carrier against a fake host."""

from __future__ import annotations

import hashlib
import json
import os
import tempfile
import threading
from typing import Any

import pytest
from test_sdk import _read_frame, _start_fake_host, _write_frame

import pig_sdk
from pig_sdk import kit


def _sha(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def _wire(ledger: kit._ViewLedger, view: kit.View) -> dict[str, Any]:
    return ledger.wire(ledger.encode(view))


# ── encoding ─────────────────────────────────────────────────────────────────


def test_every_kind_encodes_its_upstream_fields() -> None:
    tree = kit.Container([
        kit.DynamicBorder("accent"),
        kit.Box(2, 0, bg="selectedBg", children=[kit.Text("in box", 1, 1)]),
        kit.Text("Pick", 2, 0, bg="customMessageBg"),
        kit.TruncatedText("cut", 0, 0),
        kit.Markdown("- **a**", 1, 0, default_text_style=kit.TextStyle(color="text", bg_color="customMessageBg", italic=True), render_latex=False),
        kit.Spacer(2),
        kit.SelectList(
            "tracks",
            [kit.SelectItem("t1", "One", "first"), kit.SelectItem("t2", "Two")],
            3,
            layout=kit.SelectLayout(min_primary_column_width=4),
            selected_index=1,
            filter="o",
        ),
        kit.SettingsList(
            "prefs",
            [
                kit.SettingItem("mode", "Mode", "fast", values=["fast", "slow"]),
                kit.SettingItem("theme", "Theme", "dark", description="colors", submenu=kit.SelectList("themes", [kit.SelectItem("dark", "Dark")], 5)),
            ],
            4,
            enable_search=True,
            selected_index=1,
            filter="th",
        ),
        kit.Loader("Working", spinner_color="accent", message_color="muted", indicator=kit.LoaderIndicator(frames=["a", "b"], interval_ms=120), frame=1),
        kit.HStack(
            [
                kit.TruncatedText("left", 0, 0),
                kit.StackEntry(kit.TruncatedText("right", 0, 0), grow=1, basis=3),
                kit.StackEntry(kit.TruncatedText("plain", 0, 0)),
            ],
            gap=1,
            align="center",
        ),
        kit.VStack([kit.Spacer(1)]),
        kit.Lines(["raw"]),
    ])
    encoded = kit.View(tree, focus="tracks", theme={"accent": "#d75f00"})._encode(False)
    assert encoded.body == {
        "root": {"kind": "container", "children": [
            {"kind": "dynamic-border", "color": "accent"},
            {"kind": "box", "children": [{"kind": "text", "text": "in box", "paddingX": 1, "paddingY": 1}],
             "paddingX": 2, "paddingY": 0, "bg": "selectedBg"},
            {"kind": "text", "text": "Pick", "paddingX": 2, "paddingY": 0, "bg": "customMessageBg"},
            {"kind": "truncated-text", "text": "cut", "paddingX": 0, "paddingY": 0},
            {"kind": "markdown", "text": "- **a**", "paddingX": 1, "paddingY": 0,
             "defaultTextStyle": {"color": "text", "bgColor": "customMessageBg", "italic": True}, "renderLatex": False},
            {"kind": "spacer", "lines": 2},
            {"kind": "select-list", "id": "tracks",
             "items": [{"value": "t1", "label": "One", "description": "first"}, {"value": "t2", "label": "Two"}],
             "maxVisible": 3, "layout": {"minPrimaryColumnWidth": 4}, "selectedIndex": 1, "filter": "o"},
            {"kind": "settings-list", "id": "prefs", "items": [
                {"id": "mode", "label": "Mode", "currentValue": "fast", "values": ["fast", "slow"]},
                {"id": "theme", "label": "Theme", "description": "colors", "currentValue": "dark",
                 "submenu": {"kind": "select-list", "id": "themes", "items": [{"value": "dark", "label": "Dark"}], "maxVisible": 5}}],
             "maxVisible": 4, "enableSearch": True, "selectedIndex": 1, "filter": "th"},
            {"kind": "loader", "message": "Working", "spinnerColor": "accent", "messageColor": "muted",
             "indicator": {"frames": ["a", "b"], "intervalMs": 120}, "frame": 1},
            {"kind": "hstack", "children": [
                {"kind": "truncated-text", "text": "left", "paddingX": 0, "paddingY": 0},
                {"kind": "truncated-text", "text": "right", "paddingX": 0, "paddingY": 0, "stack": {"basis": 3, "grow": 1}},
                {"kind": "truncated-text", "text": "plain", "paddingX": 0, "paddingY": 0}],
             "gap": 1, "align": "center"},
            {"kind": "vstack", "children": [{"kind": "spacer", "lines": 1}]},
            {"kind": "lines", "content": ["raw"]},
        ]},
        "focus": "tracks",
        "theme": {"accent": "#d75f00"},
    }
    assert encoded.images == []


def test_defaults_match_upstream_constructors() -> None:
    body = kit.View(kit.Container([kit.Box(), kit.Spacer(), kit.DynamicBorder(), kit.Loader(), kit.Text()]))._encode(False).body
    assert body["root"]["children"] == [
        {"kind": "box", "paddingX": 1, "paddingY": 1},
        {"kind": "spacer", "lines": 1},
        {"kind": "dynamic-border", "color": "border"},
        {"kind": "loader", "message": "Loading..."},
        {"kind": "text", "text": "", "paddingX": 1, "paddingY": 1},
    ]


def test_conversation_kinds_encode_as_the_go_sdk() -> None:
    image = b"png bytes"
    assistant = kit.AssistantMessage(id="a1")
    assistant.update_content(kit.Message([kit.thinking_block("plan"), kit.text_block("done"), kit.tool_call_block()], stop_reason="toolUse"), True)
    hidden = kit.AssistantMessage(kit.Message([kit.text_block("x")], stop_reason="error", error_message="boom"))
    hidden.set_hide_thinking_block(True)
    hidden.set_hidden_thinking_label("Pondering...")
    hidden.set_output_pad(0)
    tool = kit.ToolExecution("read", "call-1", {"path": "a.txt"}, "/work", id="call-1")
    tool.set_args_complete()
    tool.mark_execution_started()
    tool.update_result(kit.ToolResult([kit.text_content("hi"), kit.image_content(image, "image/png")], is_error=True, details={"n": 1}), False)
    tool.set_expanded(True)
    empty = kit.ToolExecution("kit_tool", tool_definition=kit.TOOL_DEFINITION_EMPTY)
    empty.set_show_images(False)
    empty.set_image_width_cells(30)
    bash = kit.BashExecution("ls", True, id="b")
    bash.append_output("a\n")
    bash.append_output("b")
    bash.set_complete(2, False, True, "/tmp/out")
    bash.set_expanded(True)
    diff = kit.Diff(" 1 a\n-2 b\n+2 c", file_path="x.go")
    view = kit.View(kit.Container([kit.UserMessage("hello"), assistant, hidden, tool, empty, bash, kit.BashExecution("sleep"), diff]))
    encoded = view._encode(False)
    ref = _sha(image)
    want = ('{"root":{"kind":"container","children":['
            '{"kind":"user-message","text":"hello","outputPad":1},'
            '{"kind":"assistant-message","id":"a1","message":{"content":[{"type":"thinking","thinking":"plan"},{"type":"text","text":"done"},{"type":"toolCall"}],"stopReason":"toolUse"},"outputPad":1,"isStreaming":true},'
            '{"kind":"assistant-message","message":{"content":[{"type":"text","text":"x"}],"stopReason":"error","errorMessage":"boom"},"outputPad":0,"hideThinkingBlock":true,"hiddenThinkingLabel":"Pondering..."},'
            '{"kind":"tool-execution","id":"call-1","toolName":"read","toolCallId":"call-1","args":{"path":"a.txt"},"cwd":"/work","executionStarted":true,"argsComplete":true,"expanded":true,"result":{"content":[{"type":"text","text":"hi"},{"type":"image","ref":"' + ref + '","mimeType":"image/png"}],"isError":true,"details":{"n":1}},"isPartial":false},'
            '{"kind":"tool-execution","toolName":"kit_tool","args":{},"toolDefinition":"empty","showImages":false,"imageWidthCells":30},'
            '{"kind":"bash-execution","id":"b","expanded":true,"command":"ls","excludeFromContext":true,"output":"a\\nb","complete":{"exitCode":2,"truncated":true,"fullOutputPath":"/tmp/out"}},'
            '{"kind":"bash-execution","command":"sleep"},'
            '{"kind":"diff","paddingX":0,"paddingY":0,"diff":" 1 a\\n-2 b\\n+2 c","filePath":"x.go"}]}}')
    assert json.dumps(encoded.body, separators=(",", ":")) == want
    assert encoded.refs() == [ref], "the result image goes out once"


def test_image_ref_is_the_sha256_of_its_bytes() -> None:
    image = kit.Image(b"png-bytes", "image/png", max_width_cells=20, max_height_cells=10, filename="cover.png", fallback_color="dim")
    assert image.ref == _sha(b"png-bytes")
    encoded = kit.View(image)._encode(False)
    assert encoded.body["root"] == {"kind": "image", "ref": _sha(b"png-bytes"), "mimeType": "image/png", "maxWidthCells": 20,
                                    "maxHeightCells": 10, "filename": "cover.png", "fallbackColor": "dim"}
    assert encoded.refs() == [_sha(b"png-bytes")]


def test_lines_annotations_travel_only_with_a_frontend() -> None:
    view = kit.View(kit.Lines(["▀▀"]).image(b"cover", "image/jpeg").progress(30, 120))
    without = view._encode(False)
    assert without.body["root"] == {"kind": "lines", "content": ["▀▀"]}
    assert without.images == [], "no image bytes without a frontend"
    with_frontend = view._encode(True)
    assert with_frontend.body["root"] == {"kind": "lines", "content": ["▀▀"], "image": {"ref": _sha(b"cover")},
                                          "progress": {"value": 30.0, "max": 120.0}}
    assert with_frontend.refs() == [_sha(b"cover")]


def test_list_annotation_travels_only_with_a_frontend() -> None:
    tracks = kit.List([kit.ListItem("Blue in Green", "Miles Davis", ["5:37"]), kit.ListItem("So What")], selected=1)
    view = kit.View(kit.Lines(["a", "b"], list=tracks))
    assert view._encode(False).body["root"] == {"kind": "lines", "content": ["a", "b"]}
    assert view._encode(True).body["root"]["list"] == {
        "items": [{"label": "Blue in Green", "detail": "Miles Davis", "columns": ["5:37"]}, {"label": "So What"}],
        "selectedIndex": 1,
    }
    none = kit.View(kit.Lines(["a"], list=kit.List([kit.ListItem("")])))
    assert none._encode(True).body["root"]["list"]["selectedIndex"] == -1, "no selection is -1"


def test_ledger_sends_image_bytes_once_per_connection_until_evicted() -> None:
    ledger = kit._ViewLedger()
    view = kit.View(kit.Container([kit.Image(b"abc", "image/png"), kit.Image(b"abc", "image/png")]))
    assert _wire(ledger, view)["images"] == [{"ref": _sha(b"abc"), "mimeType": "image/png", "data": "YWJj"}], "one entry per ref"
    encoded = ledger.encode(view)
    assert not ledger.has_unsent(encoded)
    assert "images" not in ledger.wire(encoded), "the second frame names the ref only"
    assert encoded.references({_sha(b"abc")})
    ledger.forget({_sha(b"abc")})
    assert ledger.has_unsent(encoded)
    assert _wire(ledger, view)["images"][0]["data"] == "YWJj", "evicted bytes are sent again"


def test_a_view_deeper_than_the_host_bound_is_an_error() -> None:
    node: kit.Node = kit.Spacer(1)
    for _ in range(63):
        node = kit.Container([node])
    kit.View(node)._encode(False)
    with pytest.raises(ValueError, match="kit: view deeper than 64 nodes"):
        kit.View(kit.Container([node]))._encode(False)
    loop = kit.Container()
    loop.add_child(loop)
    with pytest.raises(ValueError, match="deeper than 64"):
        kit.View(loop)._encode(False)


def test_ledger_follows_the_frontend_flag_of_each_snapshot() -> None:
    ledger = kit._ViewLedger()
    view = kit.View(kit.Lines([]).progress(1, 2))
    assert "progress" not in _wire(ledger, view)["root"]
    ledger.apply_state({"frontend": True})
    assert _wire(ledger, view)["root"]["progress"] == {"value": 1.0, "max": 2.0}
    # state_update snapshots omit a false flag.
    ledger.apply_state({"hasUI": True})
    assert "progress" not in _wire(ledger, view)["root"]


def test_events_decode_each_upstream_callback() -> None:
    key, select = kit.Event._from_wire({"key": "custom-1", "node": "tracks", "type": "select", "index": 2,
                                        "item": {"value": "k2", "label": "Two", "description": "d"}})
    assert key == "custom-1"
    assert select == kit.Event(node="tracks", type=kit.SELECT, index=2, item=kit.SelectItem("k2", "Two", "d"))
    _, change = kit.Event._from_wire({"key": "k", "node": "prefs", "type": "change", "index": 0, "id": "mode", "value": "slow"})
    assert (change.type, change.id, change.value, change.item) == (kit.CHANGE, "mode", "slow", None)
    for wire in (kit.CANCEL, kit.SELECTION_CHANGE):
        assert kit.Event._from_wire({"key": "k", "node": "n", "type": wire})[1].type == wire
    assert kit.Event._from_wire({"key": "k", "node": "n", "type": "hover"}) is None


def test_rendered_results_carry_lines_or_a_view_never_both() -> None:
    ledger = kit._ViewLedger()
    assert ledger.result(["a"], False) == {"lines": ["a"]}
    assert ledger.result(kit.View(kit.Spacer(1)), True) == {"view": {"root": {"kind": "spacer", "lines": 1}}}


# ── wire ─────────────────────────────────────────────────────────────────────


class _Host:
    def __init__(self, name: str, ext: pig_sdk.Extension, state: dict[str, Any]) -> None:
        tmp = tempfile.mkdtemp()
        self.sock_path = os.path.join(tmp, f"{name}.sock")
        self.listener = _start_fake_host(self.sock_path)
        self.thread = threading.Thread(target=ext.run_with_socket, args=(self.sock_path,), daemon=True)
        self.thread.start()
        self.conn, _ = self.listener.accept()
        self.conn.settimeout(10)
        self.register = _read_frame(self.conn)
        assert self.register["type"] == "register"
        _write_frame(self.conn, {"type": "ready", "ready": {"cwd": tmp, "width": 80, "state": state}})

    def next(self) -> dict[str, Any]:
        return _read_frame(self.conn)

    def notify(self, method: str, args: dict[str, Any]) -> None:
        _write_frame(self.conn, {"type": "notify", "notify": {"method": method, "args": args}})

    def request(self, req_id: str, method: str, tool: str, args: dict[str, Any]) -> None:
        _write_frame(self.conn, {"type": "request", "id": req_id, "request": {"method": method, "tool": tool, "args": args}})

    def answer(self, call: dict[str, Any], result: dict[str, Any]) -> None:
        _write_frame(self.conn, {"type": "call_result", "id": call["id"], "call_result": {"result": result}})

    def close(self) -> None:
        try:
            _write_frame(self.conn, {"type": "shutdown", "shutdown": {"reason": "done"}})
        except OSError:
            pass
        self.thread.join(timeout=5)
        self.conn.close()
        self.listener.close()


def _tracks() -> list[kit.SelectItem]:
    return [kit.SelectItem(f"k{i}", f"Track {i}", f"d{i}") for i in range(5)]


class _Picker:
    """The conformance fixture's picker: logs every event and input it acts on,
    and closes on select with the log."""

    def __init__(self) -> None:
        self.log: list[str] = []

    def view(self, width: int) -> kit.View:
        return kit.View(
            kit.Container([kit.Text(f"{len(self.log)} logged", 1, 0), kit.SelectList("kit-tracks", _tracks(), 3, selected_index=2)]),
            focus="kit-tracks",
            theme={"accent": "#d75f00"},
        )

    def handle_input(self, data: str) -> pig_sdk.RemoteComponentResult:
        if data != "noop":
            self.log.append(f"input:{data}")
        return pig_sdk.RemoteComponentResult()

    def handle_view_event(self, event: kit.Event) -> pig_sdk.RemoteComponentResult:
        value = event.item.value if event.item else ""
        if event.type == kit.SELECTION_CHANGE:
            self.log.append(f"selectionChange:{event.index}:{value}")
        elif event.type == kit.SELECT:
            self.log.append(f"select:{event.index}:{value}")
            return pig_sdk.RemoteComponentResult(done=True, value=",".join(self.log))
        elif event.type == kit.CANCEL:
            raise RuntimeError("cancelled")
        return pig_sdk.RemoteComponentResult()


def _custom_ext(name: str, component_factory: Any) -> pig_sdk.Extension:
    ext = pig_sdk.Extension(name)
    ext.tool("pick", "Pick", {"type": "object"}, lambda ctx, args: {"content": str(ctx.custom(component_factory()))})
    return ext


def test_custom_view_sends_authoritative_frames_and_receives_events_in_order() -> None:
    host = _Host("custom", _custom_ext("kit-custom", _Picker), {"hasUI": True})
    try:
        host.request("pick", "tool_call", "pick", {})
        call = host.next()
        assert call["call"]["method"] == "ui.custom"
        key = call["call"]["args"]["key"]
        first = host.next()["notify"]
        assert first["method"] == "ui.custom.render"
        frame = first["args"]
        assert "lines" not in frame, "an authoritative view frame has no lines"
        assert (frame["key"], frame["width"], frame["seq"]) == (key, 80, 1)
        assert frame["view"]["focus"] == "kit-tracks"
        assert frame["view"]["theme"] == {"accent": "#d75f00"}
        assert frame["view"]["root"]["children"][1]["selectedIndex"] == 2
        assert frame["view"]["root"]["children"][1]["items"][3] == {"value": "k3", "label": "Track 3", "description": "d3"}

        def event(kind: str, index: int, value: str) -> dict[str, Any]:
            return {"key": key, "node": "kit-tracks", "type": kind, "index": index, "item": {"value": value, "label": "x", "description": "y"}}

        # An unchanged view after input is not sent again.
        host.notify("ui.custom.input", {"key": key, "data": "noop"})
        host.notify("ui.view.event", event("selectionChange", 3, "k3"))
        # An event of a surface that is not open is dropped.
        host.notify("ui.view.event", {"key": "custom-999", "node": "kit-tracks", "type": "select", "index": 0})
        host.notify("ui.view.event", event("selectionChange", 4, "k4"))
        host.notify("ui.custom.input", {"key": key, "data": "x"})
        host.notify("ui.view.event", event("select", 0, "k0"))
        seqs = []
        while True:
            frame = host.next()
            assert frame["type"] == "notify", frame
            if frame["notify"]["method"] == "ui.custom.close":
                break
            assert frame["notify"]["method"] == "ui.custom.render"
            assert "lines" not in frame["notify"]["args"]
            seqs.append(frame["notify"]["args"]["seq"])
        assert seqs == [2, 3, 4], "one frame per changed view, none for the no-op input"
        result = "selectionChange:3:k3,selectionChange:4:k4,input:x,select:0:k0"
        assert frame["notify"]["args"]["result"] == result
        host.answer(call, {"ok": True, "result": result})
        assert host.next()["type"] == "response"
    finally:
        host.close()


def test_view_event_error_closes_the_overlay_with_it() -> None:
    host = _Host("cancel", _custom_ext("kit-cancel", _Picker), {"hasUI": True})
    try:
        host.request("pick", "tool_call", "pick", {})
        call = host.next()
        key = call["call"]["args"]["key"]
        assert host.next()["notify"]["method"] == "ui.custom.render"
        host.notify("ui.view.event", {"key": key, "node": "kit-tracks", "type": "cancel", "index": 0})
        close = host.next()["notify"]
        assert close["method"] == "ui.custom.close"
        assert close["args"]["error"] == "cancelled"
        host.answer(call, {"ok": False})
        assert host.next()["type"] == "response"
    finally:
        host.close()


class _Cover:
    def view(self, width: int) -> kit.View:
        return kit.View(kit.Image(b"cover", "image/png"))

    def handle_input(self, data: str) -> pig_sdk.RemoteComponentResult:
        return pig_sdk.RemoteComponentResult(done=True)


def test_custom_view_sends_an_evicted_image_again_in_an_unchanged_frame() -> None:
    host = _Host("cover", _custom_ext("kit-cover", _Cover), {"hasUI": True})
    cover = _sha(b"cover")
    try:
        host.request("c", "tool_call", "pick", {})
        call = host.next()
        key = call["call"]["args"]["key"]
        assert host.next()["notify"]["args"]["view"]["images"][0]["ref"] == cover
        # A new width sends the unchanged view again, naming the ref only.
        host.notify("width_change", {"width": 90})
        resized = host.next()["notify"]["args"]
        assert (resized["seq"], resized["width"]) == (2, 90)
        assert "images" not in resized["view"]
        # An eviction of another ref leaves the overlay alone; its own ref sends the unchanged frame again with the data.
        host.notify("ui.view.evicted", {"refs": ["0000"]})
        host.notify("ui.view.evicted", {"refs": [cover]})
        again = host.next()["notify"]
        assert again["method"] == "ui.custom.render", again
        assert again["args"]["seq"] == 3
        assert again["args"]["view"]["images"][0]["data"] == "Y292ZXI="
        host.notify("ui.custom.input", {"key": key, "data": "q"})
        assert host.next()["notify"]["method"] == "ui.custom.close"
        host.answer(call, {"ok": True})
        assert host.next()["type"] == "response"
    finally:
        host.close()


def _cover_view() -> kit.View:
    return kit.View(kit.Container([kit.Image(b"cover", "image/png"), kit.Lines(["▀▀"]).progress(1, 4)]))


def test_widget_header_and_footer_views_send_images_once_and_again_after_eviction() -> None:
    ext = pig_sdk.Extension("kit-surfaces")

    def show(ctx: pig_sdk.Context, args: dict[str, Any]) -> dict[str, Any]:
        ctx.set_widget("w", _cover_view())
        ctx.set_widget("w", _cover_view())
        ctx.set_header_view(_cover_view())
        ctx.set_footer_view(kit.View(kit.Spacer(1)))
        ctx.set_widget("w2", kit.View(kit.Spacer(2)), {"placement": "belowEditor"})
        return {"content": "ok"}

    def plain(ctx: pig_sdk.Context, args: dict[str, Any]) -> dict[str, Any]:
        ctx.set_widget("w", _cover_view())
        return {"content": "ok"}

    ext.tool("show", "Show", {"type": "object"}, show)
    ext.tool("plain", "Plain", {"type": "object"}, plain)
    host = _Host("surfaces", ext, {"hasUI": True, "frontend": True})
    cover = _sha(b"cover")
    try:
        host.request("show", "tool_call", "show", {})
        push = host.next()
        assert push["type"] == "widget_push"
        assert "lines" not in push["widget_push"]
        view = push["widget_push"]["view"]
        assert view["images"] == [{"ref": cover, "mimeType": "image/png", "data": "Y292ZXI="}]
        assert view["root"]["children"][1]["progress"] == {"value": 1.0, "max": 4.0}, "a frontend draws"
        again = host.next()
        assert again["type"] == "widget_push"
        assert "images" not in again["widget_push"]["view"], "the ref is sent once"

        header = host.next()
        assert header["call"]["method"] == "ui.setHeader"
        assert header["call"]["args"]["view"]["root"]["children"][0]["ref"] == cover
        assert "images" not in header["call"]["args"]["view"]
        assert "lines" not in header["call"]["args"] and "width" not in header["call"]["args"]
        host.answer(header, {})
        footer = host.next()
        assert footer["call"]["method"] == "ui.setFooter"
        assert footer["call"]["args"] == {"view": {"root": {"kind": "spacer", "lines": 1}}}
        host.answer(footer, {})
        widget = host.next()
        assert widget["call"]["method"] == "ui.setWidget"
        assert widget["call"]["args"] == {"key": "w2", "view": {"root": {"kind": "spacer", "lines": 2}}, "options": {"placement": "belowEditor"}}
        host.answer(widget, {})
        assert host.next()["type"] == "response"

        # The host dropped the cover: the widget and the header that name it go out again; the footer and w2 do not.
        # The data travels once, with the first frame that names the ref again.
        host.notify("ui.view.evicted", {"refs": [cover]})
        resent = host.next()
        assert resent["type"] == "widget_push"
        assert resent["widget_push"]["key"] == "w"
        assert resent["widget_push"]["view"]["images"] == [{"ref": cover, "mimeType": "image/png", "data": "Y292ZXI="}]
        header = host.next()
        assert header["call"]["method"] == "ui.setHeader"
        assert header["call"]["args"]["view"]["root"]["children"][0]["ref"] == cover
        assert "images" not in header["call"]["args"]["view"]
        host.answer(header, {})

        # Without a frontend the progress annotation is not sent.
        host.notify("state_update", {"state": {"hasUI": True}})
        host.request("plain", "tool_call", "plain", {})
        plain_push = host.next()
        assert plain_push["type"] == "widget_push"
        assert plain_push["widget_push"]["view"]["root"]["children"][1] == {"kind": "lines", "content": ["▀▀"]}
        assert host.next()["type"] == "response"
    finally:
        host.close()


def test_view_renderers_answer_with_a_view_and_win_over_lines() -> None:
    ext = pig_sdk.Extension("kit-renderers")

    def result_view(ctx: Any, result: dict[str, Any], options: dict[str, Any], render: pig_sdk.ToolRenderContext, width: int) -> kit.View:
        render.state["seen"] = width
        return kit.View(kit.Text(f"{len(result['content'])} blocks at {width}", 0, 0))

    ext.register_tool(pig_sdk.ToolDefinition(
        name="t", label="T", description="tool", parameters={"type": "object"}, execute=lambda ctx, args: {"content": "ok"},
        render_result=lambda *_: ["lines"], result_view=result_view,
    ))
    ext.tool("u", "U", {"type": "object"}, lambda ctx, args: {"content": "ok"})
    ext.tool_renderers("u", call_view=lambda ctx, args, render, width: kit.View(kit.Text(str(args["q"]), 0, 0)))
    ext.tool_renderer(lambda tool, next_: pig_sdk.ToolRenderers(call_view=lambda ctx, args, render, width: kit.View(kit.Spacer(width // 10))) if tool == "r" else None)
    ext.message_view_renderer("note", lambda ctx, message, options, width: kit.View(kit.Text(message["content"], 1, 1)))
    ext.entry_view_renderer("mark", lambda ctx, entry, options, width: kit.View(kit.Text(entry["data"], 1, 0)))
    host = _Host("renderers", ext, {"hasUI": True})
    try:
        tools = {tool["name"]: tool for tool in host.register["register"]["tools"]}
        assert tools["t"]["renders_result"] is True
        assert tools["u"]["renders_call"] is True, "a view form declares the phase rendered"

        def text(frame: dict[str, Any]) -> Any:
            return frame["response"]["result"]["view"]["root"]["text"]

        host.request("1", "render_tool", "t", {"card": "c", "phase": "result", "result": {"content": [{"type": "text", "text": "x"}]}, "width": 40})
        result = host.next()
        assert "lines" not in result["response"]["result"], result
        assert text(result) == "1 blocks at 40"
        host.request("2", "render_tool", "u", {"card": "d", "phase": "call", "args": {"q": "hi"}, "width": 40})
        assert text(host.next()) == "hi"
        host.request("3", "resolve_tool_renderers", "", {"tool": "r"})
        resolved = host.next()["response"]["result"]
        assert resolved["renders_call"] is True
        host.request("4", "render_tool", "r", {"card": "e", "phase": "call", "renderers": resolved["renderers"], "width": 30})
        assert host.next()["response"]["result"]["view"]["root"] == {"kind": "spacer", "lines": 3}
        host.request("5", "render_message", "note", {"message": {"content": "hello"}, "options": {}, "width": 50})
        assert text(host.next()) == "hello"
        host.request("6", "render_entry", "mark", {"entry": {"data": "entry"}, "options": {}, "width": 50})
        assert text(host.next()) == "entry"
    finally:
        host.close()


def test_a_view_renderer_that_cannot_be_encoded_answers_an_error() -> None:
    loop = kit.Container()
    loop.add_child(loop)
    ext = pig_sdk.Extension("kit-deep")
    ext.message_view_renderer("deep", lambda ctx, message, options, width: kit.View(loop))
    host = _Host("deep", ext, {"hasUI": True})
    try:
        host.request("1", "render_message", "deep", {"message": {}, "options": {}, "width": 50})
        answer = host.next()
        assert answer["response"]["error"]["message"] == "kit: view deeper than 64 nodes"
    finally:
        host.close()


def test_kit_is_reachable_from_the_package() -> None:
    assert pig_sdk.kit is kit
    assert pig_sdk.ViewComponent is kit.ViewComponent
