#!/usr/bin/env python3
from __future__ import annotations

import dataclasses
import json
import pathlib
import struct
import sys
import threading
import time
import zlib
from typing import Any

# kit-kinds' image, a 1×1 PNG every SDK fixture embeds byte for byte.
KIT_KINDS_PNG = "89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c48900000010494441547801010500faff002a005fff026a01892888e8cd0000000049454e44ae426082"

ROOT = pathlib.Path(__file__).resolve().parents[4]
sys.path.insert(0, str(ROOT / "extensions" / "sdk-py"))

import pig_sdk  # noqa: E402
from pig_sdk import kit  # noqa: E402


def kit_probe_view() -> kit.View:
    """The component kit's conformance view (D107,
    docs/plan/extension-component-kit.md §10)."""
    items = [kit.SelectItem(f"k{i}", f"Track {i}", f"Artist {i}") for i in range(5)]
    return kit.View(
        kit.Container([
            kit.DynamicBorder("accent"),
            kit.Text("Kit probe", 2, 0, bg="customMessageBg"),
            kit.Markdown("- one\n- **two**", 1, 0),
            kit.HStack(
                [kit.StackEntry(kit.TruncatedText("left side", 0, 0), grow=1), kit.StackEntry(kit.TruncatedText("right", 0, 0), grow=1)],
                gap=1,
            ),
            kit.Spacer(1),
            kit.SelectList("kit-tracks", items, 3, selected_index=2),
        ]),
        focus="kit-tracks",
        theme={"accent": "#d75f00"},
    )


class KitProbe:
    """kit-probe's component: logs every input and view event and closes on
    select with the log joined by ","."""

    def __init__(self) -> None:
        self.log: list[str] = []

    def view(self, _width: int) -> kit.View:
        return kit_probe_view()

    def handle_input(self, data: str) -> pig_sdk.RemoteComponentResult:
        self.log.append("input:" + data)
        return pig_sdk.RemoteComponentResult()

    def handle_view_event(self, event: kit.Event) -> pig_sdk.RemoteComponentResult:
        self.log.append(f"{event.type}:{event.index}:{event.item.value if event.item else ''}")
        if event.type == kit.SELECT:
            return pig_sdk.RemoteComponentResult(done=True, value=",".join(self.log))
        return pig_sdk.RemoteComponentResult()


class MouseProbe:
    """The extension mouse row's component: logs every mouse event it
    receives, all of its fields, and closes on a click with the log joined by
    ","."""

    def __init__(self) -> None:
        self.log: list[str] = []

    def render(self, _width: int) -> list[str]:
        return ["mouse probe", "row 1", "row 2", "row 3"]

    def handle_input(self, _data: str) -> pig_sdk.RemoteComponentResult:
        return pig_sdk.RemoteComponentResult()

    def handle_mouse(self, event: pig_sdk.MouseEvent) -> pig_sdk.RemoteComponentResult:
        mods = "".join(name for on, name in ((event.shift, "S"), (event.alt, "A"), (event.ctrl, "C")) if on)
        self.log.append(
            f"{event.type}/{event.button}/{event.x},{event.y}/{event.screen_x},{event.screen_y}/"
            f"{event.width}x{event.height}/w{event.wheel_delta}/c{event.click_count}/{mods}"
        )
        if event.type == "click":
            return pig_sdk.RemoteComponentResult(done=True, value=",".join(self.log))
        return pig_sdk.RemoteComponentResult()


KIT_WIDGET_CASES: dict[str, Any] = {}
"""The widget kinds' Pi vector cases (D107, docs/plan/extension-component-kit.md
§10), built with the SDK's own constructors; the key is
"<module>/<export>/<case name>" and the case's wire node is its vector's
``root``."""

KIT_BRANCH_SUMMARY = "The branch explored **two** options:\n\n- keep the cache\n- drop the cache\n\nIt chose `drop`."


def _kit_branch_view(expanded: bool = False, output_pad: int = 1) -> kit.View:
    node = kit.BranchSummary(KIT_BRANCH_SUMMARY, output_pad=output_pad, id="b")
    node.set_expanded(expanded)
    return kit.View(kit.Container([node]))


def _kit_branch_empty() -> kit.View:
    node = kit.BranchSummary("")
    node.set_expanded(True)
    return kit.View(node)


_BRANCH = "pi-coding-agent/BranchSummaryMessageComponent/"
KIT_WIDGET_CASES[_BRANCH + "collapsed"] = lambda: _kit_branch_view()
KIT_WIDGET_CASES[_BRANCH + "click toggles"] = lambda: _kit_branch_view()
KIT_WIDGET_CASES[_BRANCH + "right click ignored"] = lambda: _kit_branch_view()
KIT_WIDGET_CASES[_BRANCH + "expanded"] = lambda: _kit_branch_view(expanded=True)
KIT_WIDGET_CASES[_BRANCH + "outputPad 0 collapsed"] = lambda: _kit_branch_view(output_pad=0)
KIT_WIDGET_CASES[_BRANCH + "outputPad 0 expanded"] = lambda: _kit_branch_view(expanded=True, output_pad=0)
KIT_WIDGET_CASES[_BRANCH + "empty summary expanded"] = _kit_branch_empty

KIT_COMPACTION_SUMMARY = "Compacted the first **forty** turns:\n\n1. the build setup\n2. the failing test\n\nThe fix is pending."


def _kit_compaction_view(tokens: int = 123456, expanded: bool = False, output_pad: int = 1) -> kit.View:
    node = kit.CompactionSummary(KIT_COMPACTION_SUMMARY, tokens, output_pad=output_pad, id="c")
    node.set_expanded(expanded)
    return kit.View(kit.Container([node]))


def _kit_compaction_empty() -> kit.View:
    node = kit.CompactionSummary("", 0)
    node.set_expanded(True)
    return kit.View(node)


_COMPACTION = "pi-coding-agent/CompactionSummaryMessageComponent/"
KIT_WIDGET_CASES[_COMPACTION + "collapsed"] = lambda: _kit_compaction_view()
KIT_WIDGET_CASES[_COMPACTION + "click toggles"] = lambda: _kit_compaction_view()
KIT_WIDGET_CASES[_COMPACTION + "right click ignored"] = lambda: _kit_compaction_view()
KIT_WIDGET_CASES[_COMPACTION + "expanded"] = lambda: _kit_compaction_view(expanded=True)
KIT_WIDGET_CASES[_COMPACTION + "outputPad 0 collapsed"] = lambda: _kit_compaction_view(output_pad=0)
KIT_WIDGET_CASES[_COMPACTION + "outputPad 0 expanded"] = lambda: _kit_compaction_view(expanded=True, output_pad=0)
KIT_WIDGET_CASES[_COMPACTION + "tokens 999"] = lambda: _kit_compaction_view(tokens=999)
KIT_WIDGET_CASES[_COMPACTION + "tokens 1234567 expanded"] = lambda: _kit_compaction_view(tokens=1234567, expanded=True)
KIT_WIDGET_CASES[_COMPACTION + "empty summary tokens 0 expanded"] = _kit_compaction_empty

KIT_SKILL_CONTENT = "Review the diff for **bugs** first.\n\n- correctness\n- naming\n\nReport findings as a list."


def _kit_skill_view(expanded: bool = False, output_pad: int = 1) -> kit.View:
    node = kit.SkillInvocation("code-review", KIT_SKILL_CONTENT, output_pad=output_pad, id="s")
    node.set_expanded(expanded)
    return kit.View(kit.Container([node]))


def _kit_skill_empty() -> kit.View:
    node = kit.SkillInvocation("", "")
    node.set_expanded(True)
    return kit.View(node)


_SKILL = "pi-coding-agent/SkillInvocationMessageComponent/"
KIT_WIDGET_CASES[_SKILL + "collapsed"] = lambda: _kit_skill_view()
KIT_WIDGET_CASES[_SKILL + "click toggles"] = lambda: _kit_skill_view()
KIT_WIDGET_CASES[_SKILL + "right click ignored"] = lambda: _kit_skill_view()
KIT_WIDGET_CASES[_SKILL + "expanded"] = lambda: _kit_skill_view(expanded=True)
KIT_WIDGET_CASES[_SKILL + "outputPad 0 collapsed"] = lambda: _kit_skill_view(output_pad=0)
KIT_WIDGET_CASES[_SKILL + "outputPad 0 expanded"] = lambda: _kit_skill_view(expanded=True, output_pad=0)
KIT_WIDGET_CASES[_SKILL + "empty skill expanded"] = _kit_skill_empty

KIT_CUSTOM_TEXT = "Build **passed** in 12s\n\n- 214 tests\n- 0 failures"


def _kit_custom_view(text: str = KIT_CUSTOM_TEXT, expanded: bool = False, output_pad: int = 1) -> kit.View:
    node = kit.CustomMessage("ci-status", text, output_pad=output_pad, id="m")
    node.set_expanded(expanded)
    return kit.View(kit.Container([node]))


_CUSTOM = "pi-coding-agent/CustomMessageComponent/"
KIT_WIDGET_CASES[_CUSTOM + "default"] = lambda: _kit_custom_view()
KIT_WIDGET_CASES[_CUSTOM + "outputPad 0"] = lambda: _kit_custom_view(output_pad=0)
KIT_WIDGET_CASES[_CUSTOM + "expanded changes nothing"] = lambda: _kit_custom_view(expanded=True)
KIT_WIDGET_CASES[_CUSTOM + "empty text"] = lambda: _kit_custom_view(text="")
KIT_WIDGET_CASES[_CUSTOM + "no id"] = lambda: kit.View(kit.CustomMessage("note", "plain"))

KIT_WIDE_MESSAGE = "A very long message that wraps around the narrow widths of the table"


def _kit_cancellable(message: str = "Working", **options: Any) -> kit.View:
    return kit.View(kit.Container([kit.CancellableLoader("c", message, **options)]), focus="c")


def _kit_bordered(message: str = "Fetching models", **options: Any) -> kit.View:
    return kit.View(kit.Container([kit.BorderedLoader("b", message, **options)]), focus="b")


_CANCELLABLE = "pi-tui/CancellableLoader/"
for _name in ("default", "escape aborts", "escape twice aborts twice", "other keys are ignored"):
    KIT_WIDGET_CASES[_CANCELLABLE + _name] = lambda: _kit_cancellable()
KIT_WIDGET_CASES[_CANCELLABLE + "default message"] = lambda: _kit_cancellable("Loading...")
KIT_WIDGET_CASES[_CANCELLABLE + "colors"] = lambda: _kit_cancellable(spinner_color="accent", message_color="muted")
KIT_WIDGET_CASES[_CANCELLABLE + "frame 3"] = lambda: _kit_cancellable(frame=3)
KIT_WIDGET_CASES[_CANCELLABLE + "indicator frames"] = lambda: _kit_cancellable(indicator=kit.LoaderIndicator(frames=["*", "+", "x"], interval_ms=50), frame=1)
KIT_WIDGET_CASES[_CANCELLABLE + "hidden indicator"] = lambda: _kit_cancellable(indicator=kit.LoaderIndicator(frames=[]))
KIT_WIDGET_CASES[_CANCELLABLE + "wide message"] = lambda: _kit_cancellable(KIT_WIDE_MESSAGE, spinner_color="accent")
_BORDERED = "pi-coding-agent/BorderedLoader/"
for _name in ("cancellable", "escape aborts", "escape twice aborts twice", "other keys are ignored"):
    KIT_WIDGET_CASES[_BORDERED + _name] = lambda: _kit_bordered()
for _name in ("not cancellable", "escape ignored when not cancellable"):
    KIT_WIDGET_CASES[_BORDERED + _name] = lambda: _kit_bordered(cancellable=False)
KIT_WIDGET_CASES[_BORDERED + "default message"] = lambda: _kit_bordered("Loading...")
KIT_WIDGET_CASES[_BORDERED + "frame 4"] = lambda: _kit_bordered(frame=4)
KIT_WIDGET_CASES[_BORDERED + "wide message"] = lambda: _kit_bordered("Fetching the models of every configured provider, one after another")

def _kit_input(focus: bool = True, **options: Any) -> kit.View:
    return kit.View(kit.Container([kit.Input("i", **options)]), focus="i" if focus else None)


_INPUT = "pi-tui/Input/"
KIT_WIDGET_CASES[_INPUT + "empty"] = lambda: _kit_input(False)
for _name in ("empty focused", "typing", "left then insert", "backspace and delete", "home and end", "kill word and yank", "kill to end", "paste scrolls", "submit"):
    KIT_WIDGET_CASES[_INPUT + _name] = lambda: _kit_input()
KIT_WIDGET_CASES[_INPUT + "placeholder"] = lambda: _kit_input(False, placeholder="type here")
KIT_WIDGET_CASES[_INPUT + "placeholder focused colored"] = lambda: _kit_input(placeholder="type here", placeholder_color="dim")
KIT_WIDGET_CASES[_INPUT + "custom prompt"] = lambda: _kit_input(prompt="$ ", value="ls -la")
KIT_WIDGET_CASES[_INPUT + "value"] = lambda: _kit_input(False, value="hello world")
for _name in ("value focused", "click moves the cursor", "right click ignored"):
    KIT_WIDGET_CASES[_INPUT + _name] = lambda: _kit_input(value="hello world")
KIT_WIDGET_CASES[_INPUT + "wide characters"] = lambda: _kit_input(value="漢字 and 😀 text")
KIT_WIDGET_CASES[_INPUT + "submit newline key"] = lambda: _kit_input(value="preset")
KIT_WIDGET_CASES[_INPUT + "escape"] = lambda: _kit_input(value="keep")

_EDITOR_COMMANDS = kit.Autocomplete([
    kit.SlashCommand("help", "Show help"),
    kit.SlashCommand("model", "Switch the model", argument_hint="<name>"),
    kit.SlashCommand("quit", "Exit"),
    kit.SlashCommand("session", "Session info"),
    kit.SlashCommand("settings", "Open settings"),
    kit.SlashCommand("share"),
    kit.SlashCommand("skills", "List skills"),
    kit.SlashCommand("compact", "Compact the context"),
])


def _kit_editor(focus: bool = True, **options: Any) -> kit.View:
    return kit.View(kit.Container([kit.Editor("e", **options)]), focus="e" if focus else None)


_EDITOR = "pi-tui/Editor/"
KIT_WIDGET_CASES[_EDITOR + "empty"] = lambda: _kit_editor(False)
KIT_WIDGET_CASES[_EDITOR + "empty focused"] = lambda: _kit_editor()
KIT_WIDGET_CASES[_EDITOR + "value"] = lambda: _kit_editor(False, value="hello world")
KIT_WIDGET_CASES[_EDITOR + "multiline value focused"] = lambda: _kit_editor(value="one\ntwo\nthree")
KIT_WIDGET_CASES[_EDITOR + "wrapped line"] = lambda: _kit_editor(value="The quick brown fox jumps over the lazy dog and keeps running through the field")
KIT_WIDGET_CASES[_EDITOR + "wide characters"] = lambda: _kit_editor(value="漢字 and 😀 text")
KIT_WIDGET_CASES[_EDITOR + "typing"] = lambda: _kit_editor()
KIT_WIDGET_CASES[_EDITOR + "newline"] = lambda: _kit_editor()
KIT_WIDGET_CASES[_EDITOR + "backspace and delete"] = lambda: _kit_editor()
KIT_WIDGET_CASES[_EDITOR + "kill word and yank"] = lambda: _kit_editor()
KIT_WIDGET_CASES[_EDITOR + "undo"] = lambda: _kit_editor()
KIT_WIDGET_CASES[_EDITOR + "submit"] = lambda: _kit_editor()
KIT_WIDGET_CASES[_EDITOR + "paste"] = lambda: _kit_editor()
KIT_WIDGET_CASES[_EDITOR + "paste large"] = lambda: _kit_editor()
KIT_WIDGET_CASES[_EDITOR + "cursor movement"] = lambda: _kit_editor(value="one\ntwo\nthree")
KIT_WIDGET_CASES[_EDITOR + "submit multiline"] = lambda: _kit_editor(value="one\ntwo")
KIT_WIDGET_CASES[_EDITOR + "submit disabled"] = lambda: _kit_editor(value="keep", disable_submit=True)
KIT_WIDGET_CASES[_EDITOR + "padding"] = lambda: _kit_editor(value="padded text", padding_x=2)
KIT_WIDGET_CASES[_EDITOR + "padding wrapped"] = lambda: _kit_editor(value="The quick brown fox jumps over the lazy dog and keeps running", padding_x=3)
KIT_WIDGET_CASES[_EDITOR + "border color"] = lambda: _kit_editor(value="colored", border_color="accent")
KIT_WIDGET_CASES[_EDITOR + "history"] = lambda: _kit_editor(history=["first", "second", "third"])
KIT_WIDGET_CASES[_EDITOR + "history then edit"] = lambda: _kit_editor(history=["first", "second"])
KIT_WIDGET_CASES[_EDITOR + "scrolls past the visible lines"] = lambda: _kit_editor(value="line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\nline 11\nline 12")
KIT_WIDGET_CASES[_EDITOR + "scroll indicator below"] = lambda: _kit_editor(value="line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\nline 11\nline 12")
KIT_WIDGET_CASES[_EDITOR + "more rows show more lines"] = lambda: _kit_editor(value="line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\nline 11\nline 12")
KIT_WIDGET_CASES[_EDITOR + "few rows show five lines"] = lambda: _kit_editor(value="line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\nline 11\nline 12")
KIT_WIDGET_CASES[_EDITOR + "page up and down"] = lambda: _kit_editor(value="line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\nline 11\nline 12\nline 13\nline 14\nline 15\nline 16\nline 17\nline 18\nline 19\nline 20\nline 21\nline 22\nline 23\nline 24\nline 25\nline 26\nline 27\nline 28\nline 29\nline 30")
KIT_WIDGET_CASES[_EDITOR + "click moves the cursor"] = lambda: _kit_editor(value="hello\nworld")
KIT_WIDGET_CASES[_EDITOR + "click on the border"] = lambda: _kit_editor(value="hello\nworld")
KIT_WIDGET_CASES[_EDITOR + "right click ignored"] = lambda: _kit_editor(value="hello")
KIT_WIDGET_CASES[_EDITOR + "slash opens the menu"] = lambda: _kit_editor(autocomplete=_EDITOR_COMMANDS)
KIT_WIDGET_CASES[_EDITOR + "slash narrows"] = lambda: _kit_editor(autocomplete=_EDITOR_COMMANDS)
KIT_WIDGET_CASES[_EDITOR + "slash no match"] = lambda: _kit_editor(autocomplete=_EDITOR_COMMANDS)
KIT_WIDGET_CASES[_EDITOR + "slash tab accepts"] = lambda: _kit_editor(autocomplete=_EDITOR_COMMANDS)
KIT_WIDGET_CASES[_EDITOR + "slash arrow then tab"] = lambda: _kit_editor(autocomplete=_EDITOR_COMMANDS)
KIT_WIDGET_CASES[_EDITOR + "slash enter accepts"] = lambda: _kit_editor(autocomplete=_EDITOR_COMMANDS)
KIT_WIDGET_CASES[_EDITOR + "slash escape closes"] = lambda: _kit_editor(autocomplete=_EDITOR_COMMANDS)
KIT_WIDGET_CASES[_EDITOR + "slash click selects"] = lambda: _kit_editor(autocomplete=_EDITOR_COMMANDS)
KIT_WIDGET_CASES[_EDITOR + "text after the slash opens nothing"] = lambda: _kit_editor(autocomplete=_EDITOR_COMMANDS)
KIT_WIDGET_CASES[_EDITOR + "slash max visible 3"] = lambda: _kit_editor(autocomplete_max_visible=3, autocomplete=_EDITOR_COMMANDS)
KIT_WIDGET_CASES[_EDITOR + "slash with padding"] = lambda: _kit_editor(padding_x=2, autocomplete=_EDITOR_COMMANDS)
KIT_WIDGET_CASES[_EDITOR + "autocomplete menu does not follow disabled submit"] = lambda: _kit_editor(disable_submit=True, autocomplete=_EDITOR_COMMANDS)

def _kit_scroll_child(kind: str) -> kit.Node:
    if kind == "md":
        return kit.Markdown("# Title\n\n- one\n- two with a long line that wraps at narrow widths\n\n`code`", 1, 0)
    if kind == "region":
        return kit.Text("Region content\nsecond row", 1, 0)
    if kind == "empty":
        return kit.Container([])
    if kind == "branch":
        return kit.BranchSummary("The branch explored **two** options:\n\n- keep the cache\n- drop the cache\n\nIt chose `drop`.", id="b")
    return kit.Text("Alpha line of the log\nBeta line of the log\nGamma line of the log that is long enough to wrap at the narrow widths\nDelta", 1, 0)


_SCROLL = "pi-tui/ScrollView/"
_MOUSE_REGION = "pi-tui/MouseRegion/"
KIT_WIDGET_CASES[_SCROLL + "default"] = lambda: kit.View(kit.Container([kit.ScrollView("s", _kit_scroll_child("text"))]))
KIT_WIDGET_CASES[_SCROLL + "scrollbar always"] = lambda: kit.View(kit.Container([kit.ScrollView("s", _kit_scroll_child("text"), scrollbar="always")]))
KIT_WIDGET_CASES[_SCROLL + "scrollbar auto"] = lambda: kit.View(kit.Container([kit.ScrollView("s", _kit_scroll_child("text"), scrollbar="auto")]))
KIT_WIDGET_CASES[_SCROLL + "scrollbar colors"] = lambda: kit.View(kit.Container([kit.ScrollView("s", _kit_scroll_child("text"), scrollbar="always", scrollbar_track_color="dim", scrollbar_thumb_color="accent")]))
KIT_WIDGET_CASES[_SCROLL + "follow end"] = lambda: kit.View(kit.Container([kit.ScrollView("s", _kit_scroll_child("text"), follow="end")]))
KIT_WIDGET_CASES[_SCROLL + "primary and contain"] = lambda: kit.View(kit.Container([kit.ScrollView("s", _kit_scroll_child("text"), primary=True, overscroll="contain")]))
KIT_WIDGET_CASES[_SCROLL + "hide delay"] = lambda: kit.View(kit.Container([kit.ScrollView("s", _kit_scroll_child("text"), scrollbar="auto", scrollbar_hide_delay_ms=50)]))
KIT_WIDGET_CASES[_SCROLL + "scroll top clamps"] = lambda: kit.View(kit.Container([kit.ScrollView("s", _kit_scroll_child("text"), scroll_top=3)]))
KIT_WIDGET_CASES[_SCROLL + "markdown child"] = lambda: kit.View(kit.Container([kit.ScrollView("s", _kit_scroll_child("md"), scrollbar="always")]))
KIT_WIDGET_CASES[_SCROLL + "after other rows"] = lambda: kit.View(kit.Container([kit.Text("Header", 1, 0), kit.ScrollView("s", _kit_scroll_child("text"), scrollbar="always"), kit.Text("Footer", 1, 0)]))
KIT_WIDGET_CASES[_SCROLL + "empty child"] = lambda: kit.View(kit.Container([kit.ScrollView("s", _kit_scroll_child("empty"), scrollbar="always")]))
KIT_WIDGET_CASES[_SCROLL + "wheel and click pass"] = lambda: kit.View(kit.Container([kit.ScrollView("s", _kit_scroll_child("text")), kit.Text("after", 1, 0)]))
KIT_WIDGET_CASES[_MOUSE_REGION + "renders its child"] = lambda: kit.View(kit.Container([kit.MouseRegion("m", _kit_scroll_child("region"))]))
KIT_WIDGET_CASES[_MOUSE_REGION + "every type by default"] = lambda: kit.View(kit.Container([kit.MouseRegion("m", _kit_scroll_child("region"))]))
KIT_WIDGET_CASES[_MOUSE_REGION + "types filter"] = lambda: kit.View(kit.Container([kit.MouseRegion("m", _kit_scroll_child("region"), types=["click"])]))
KIT_WIDGET_CASES[_MOUSE_REGION + "not handled"] = lambda: kit.View(kit.Container([kit.MouseRegion("m", _kit_scroll_child("region"), handled=False)]))
KIT_WIDGET_CASES[_MOUSE_REGION + "capture"] = lambda: kit.View(kit.Container([kit.MouseRegion("m", _kit_scroll_child("region"), capture=True)]))
KIT_WIDGET_CASES[_MOUSE_REGION + "focus and render"] = lambda: kit.View(kit.Container([kit.MouseRegion("m", _kit_scroll_child("region"), focus=True, render=False)]))
KIT_WIDGET_CASES[_MOUSE_REGION + "local coordinates"] = lambda: kit.View(kit.Container([kit.Text("Above the region", 1, 0), kit.MouseRegion("m", _kit_scroll_child("region")), kit.Text("Below", 1, 0)]))
KIT_WIDGET_CASES[_MOUSE_REGION + "the child takes the event first"] = lambda: kit.View(kit.Container([kit.MouseRegion("m", _kit_scroll_child("branch"))]))
KIT_WIDGET_CASES[_MOUSE_REGION + "right button"] = lambda: kit.View(kit.Container([kit.MouseRegion("m", _kit_scroll_child("region"))]))

# kit-widget-cases


class KitWidgetProbe:
    """kit-widget's component: takes the keys and mouse events the view leaves
    to the extension, and notifies each callback its nodes fire as
    ``kit-widget-event=<type>:<value>`` (a change report is not one). It never
    closes itself: the harness delivers the case's steps and then reads the
    callbacks, and a probe that closed after the callbacks Pi fired would drop
    the steps still to come."""

    def __init__(self, ctx: pig_sdk.Context, view: kit.View) -> None:
        self.ctx = ctx
        self.view_ = view

    def view(self, _width: int) -> kit.View:
        return self.view_

    def handle_input(self, _data: str) -> pig_sdk.RemoteComponentResult:
        return pig_sdk.RemoteComponentResult()

    def handle_mouse(self, _event: pig_sdk.MouseEvent) -> pig_sdk.RemoteComponentResult:
        return pig_sdk.RemoteComponentResult()

    def handle_view_event(self, event: kit.Event) -> pig_sdk.RemoteComponentResult:
        if event.type == kit.MOUSE and event.mouse is not None:
            m = event.mouse
            self.ctx.notify(f"kit-widget-event=mouse:{m['type']}/{m['button']}/{m['x']}/{m['y']}", "info")
        elif event.type != kit.CHANGE:
            self.ctx.notify(f"kit-widget-event={event.type}:{event.value}", "info")
        return pig_sdk.RemoteComponentResult()


def kit_image_png(n: int) -> bytes:
    """Image n of kit-images: a 1×1 RGBA PNG whose pixel encodes n, so each n
    has its own bytes and ref."""

    def chunk(kind: bytes, data: bytes) -> bytes:
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))

    pixel = bytes([0, n & 0xFF, (n >> 8) & 0xFF, 0x5F, 0xFF])
    header = struct.pack(">IIBBBBB", 1, 1, 8, 6, 0, 0, 0)
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", header) + chunk(b"IDAT", zlib.compress(pixel)) + chunk(b"IEND", b"")


class FocusedList:
    def __init__(self) -> None:
        self.items = ["alpha", "beta", "gamma"]
        self.selected = 0
        self.disposed = False

    def render(self, width: int) -> list[str]:
        lines = [f"focused width={width}"]
        lines.extend(("> " if index == self.selected else "  ") + item for index, item in enumerate(self.items))
        return lines

    def handle_input(self, data: str) -> pig_sdk.RemoteComponentResult:
        if data == "\x1b[A":
            self.selected = (self.selected - 1) % len(self.items)
        elif data == "\x1b[B":
            self.selected = (self.selected + 1) % len(self.items)
        elif data == "\x1b[6~":
            self.selected = min(self.selected + 2, len(self.items) - 1)
        elif data in {"\r", "\n"}:
            return pig_sdk.RemoteComponentResult(done=True, value=self.items[self.selected])
        elif data == "\x1b":
            return pig_sdk.RemoteComponentResult(done=True)
        return pig_sdk.RemoteComponentResult()

    def dispose(self) -> None:
        self.disposed = True


class TimerFocused:
    def __init__(self) -> None:
        self.frame = 0
        self.disposed = False
        self.detached = False
        self._invalidate = None
        self._lock = threading.Lock()
        self._stop = threading.Event()
        self._worker = threading.Thread(target=self._tick, daemon=True)
        self._worker.start()

    def _tick(self) -> None:
        while not self._stop.wait(0.02):
            with self._lock:
                self.frame += 1
                invalidate = self._invalidate
            if invalidate is not None:
                invalidate()

    def render(self, width: int) -> list[str]:
        with self._lock:
            return [f"timer frame={self.frame} width={width}"]

    def handle_input(self, data: str) -> pig_sdk.RemoteComponentResult:
        with self._lock:
            if data == "\r":
                return pig_sdk.RemoteComponentResult(done=True, value=self.frame)
        return pig_sdk.RemoteComponentResult()

    def set_invalidate(self, invalidate) -> None:
        with self._lock:
            self._invalidate = invalidate
            self.detached = invalidate is None

    def dispose(self) -> None:
        self._stop.set()
        self._worker.join()
        self.disposed = True


class ConformanceBashOperations:
    """The BashOperations every SDK fixture returns for the user_bash command "operations" (TestConformance_UserBashOperationsRunInTheExtension)."""

    def exec(self, command: str, cwd: str, options: pig_sdk.BashExecOptions) -> int | None:
        if command == "echo":
            options.on_data(f"cmd:{command}\n".encode())
            options.on_data(f"cwd:{cwd}\n".encode())
            if options.env is not None and not options.env:
                options.on_data(b"env-empty\n")
            for name in sorted(options.env or {}):
                options.on_data(f"env:{name}={options.env[name]}\n".encode())
            if options.timeout is not None:
                options.on_data(f"timeout:{options.timeout:g}\n".encode())
            return 3
        if command == "chunks":
            for chunk in (b"a", b"b", b"c"):
                options.on_data(chunk)
            return None
        if command == "binary":
            options.on_data(bytes([0xFF, 0x00, 0x80]))
            return 0
        if command == "wait":
            options.on_data(b"waiting")
            options.signal.wait()
            options.on_data(b"stopped")
            raise RuntimeError("aborted")
        raise RuntimeError(f"exec failed: {command}")


def conformance_login_definition() -> pig_sdk.LoginDefinition:
    return pig_sdk.LoginDefinition(
        brand=["A" * 41 for _ in range(5)],
        hero=["A" * 32 for _ in range(14)],
        mascot=["A" * 16 for _ in range(14)],
        palette={"A": "#123ABC"},
        name="Conformance Pig",
        description="Cross-language login fixture",
        tagline="One canonical definition across every SDK",
    )


def conformance_sprite_definition() -> pig_sdk.SpriteDefinition:
    return pig_sdk.SpriteDefinition(
        id="conformance-pig",
        name="Conformance Pig",
        tagline="One canonical sprite across every SDK",
        mascot=["A" * 16 for _ in range(14)],
        palette={"A": "#123ABC"},
    )


def dialog_text(value) -> str:
    """A dialog's string answer: select, input and editor results are whatever JSON the client sent, and undefined is None."""
    return value if isinstance(value, str) else ""


def new_extension() -> pig_sdk.Extension:
    # factory_agent_dir is get_agent_dir() while the factory runs, where Pi's extensions call it (TestConformance_HostConfigurationAgreesAcrossSDKs).
    try:
        factory_agent_dir = pig_sdk.get_agent_dir()
    except pig_sdk.HostInfoUnavailable as error:
        factory_agent_dir = "error: %s" % error
    prompt_lock = threading.Lock()
    prompt_sequence = 0

    def ui_prompt_event(ctx, data):
        nonlocal prompt_sequence
        with prompt_lock:
            sequence = prompt_sequence
            prompt_sequence += 1
        title = data["title"] if "title" in data else "(none)"
        if title.startswith("fifo:"):
            ctx.notify("fifo:%d:%s:%s" % (sequence, data["type"], title), "info")
            return
        ctx.notify("ui_prompt:%s:%s:%s:%s" % (data.get("type"), data.get("reason"), data.get("kind"), title), "info")

    ext = pig_sdk.Extension("python-sdk-fixture")
    schema_rejected = False
    try:
        ext.tool("schema-invalid", "Must not register", None, lambda ctx, args: {"content": "bad"})
    except ValueError as error:
        schema_rejected = str(error) == 'Tool "schema-invalid" registered by extension "python-sdk-fixture" must define an object parameter schema.'
    class ConformanceEditor(pig_sdk.EditorComponent):
        """Swallows "q", rewrites "a" to "A", upper-cases the host's setText, and frames super.render."""

        def handle_input(self, data):
            if data == "q":
                return
            if data == "L":
                super().set_text("raw:lines=" + "|".join(self.base.get_lines()))
                return
            super().handle_input("A" if data == "a" else data)

        def set_text(self, text):
            super().set_text(text if text.startswith("raw:") else text.upper())

        # insert_text_at_cursor wraps the host's text in angle brackets and add_to_history prefixes it, unless it starts with "raw:" (TestEditorComponentOverridesReachSuper).
        def insert_text_at_cursor(self, text):
            super().insert_text_at_cursor(text if text.startswith("raw:") else "<" + text + ">")

        def add_to_history(self, text):
            super().add_to_history(text if text.startswith("raw:") else "history:" + text)

        def render(self, width):
            return ["[custom editor]"] + super().render(width)

    class EmbeddingEditor(ConformanceEditor):
        embed_working_status = True

    class HooksEditor(ConformanceEditor):
        """A CustomEditor subclass that sets the handlers Pi's CustomEditor keeps as public fields (on_escape, on_ctrl_d,
        on_paste_image, action_handlers through on_action, on_extension_shortcut), reads and sets the members it inherits
        from Pi's Editor on its base ("M"), and hears the host's working status and invalidate. Each handler writes what ran
        into the host's text (TestEditorComponentAppHandlersRunInTheExtension, TestEditorComponentInheritedMembersReachTheHost,
        TestEditorComponentHearsTheWorkingStatusAndInvalidate)."""

        def __init__(self, base):
            super().__init__(base)

            def say(what):
                return lambda: base.set_text("raw:hook=" + what)

            base.on_escape = say("escape")
            base.on_ctrl_d = say("ctrl-d")
            base.on_paste_image = say("paste-image")
            base.on_action("app.clear", say("clear"))
            # action_handlers is the live dict: an entry deleted from it is no longer the component's.
            base.on_action("app.unused", say("unused"))
            del base.action_handlers["app.unused"]

            def shortcut(data):
                if data != "S":
                    return False
                base.set_text("raw:hook=shortcut")
                return True

            base.on_extension_shortcut = shortcut

        def handle_input(self, data):
            if data != "M":
                return super().handle_input(data)
            base = self.base
            padding = base.get_padding_x()
            visible = base.get_autocomplete_max_visible()
            border = base.border_color("x")
            base.disable_submit = True
            disabled = base.disable_submit
            base.focused = True
            focused = base.focused
            base.set_autocomplete_provider(kit.Autocomplete([kit.SlashCommand("help", "Show help")], "/work"))
            base.on_submit("sub")
            base.on_change("chg")
            base.set_text("raw:members=padding=%d,max=%d,border=%s,disable=%s,focused=%s" % (padding, visible, border, str(disabled).lower(), str(focused).lower()))

        def set_working_status_indicator(self, status):
            self.base.set_text("raw:status=none" if status is None else "raw:status=%s/%s/%d" % (status.kind, status.message, status.frame))

        def invalidate(self):
            self.base.set_text("raw:invalidated")

    ext.command("editor-install-hooks", "Install a custom editor that sets CustomEditor's handlers", lambda ctx, _args: ctx.set_editor_component(HooksEditor))

    def eager_editor(ctx, _args):
        def factory(base: pig_sdk.EditorBase):
            try:
                base.set_text("eager")
                ctx.notify("eager:" + base.get_text(), "info")
                # The stock editor's other queries and setters, at the values it already holds.
                base.get_cursor(), base.get_expanded_text(), base.is_showing_autocomplete()
                base.set_padding_x(0)
                base.set_autocomplete_max_visible(5)
            except Exception as exc:  # noqa: BLE001
                ctx.notify("eager-error:" + str(exc), "info")
            return ConformanceEditor(base)

        ctx.set_editor_component(factory)

    ext.command("editor-install-eager", "Install a custom editor that calls super in its factory", eager_editor)
    ext.command("editor-install-embed", "Install a custom editor that embeds the working status", lambda ctx, _args: ctx.set_editor_component(EmbeddingEditor))
    ext.command("editor-install", "Install a custom editor component", lambda ctx, _args: ctx.set_editor_component(ConformanceEditor))

    # headless-ui-probe hands the SDK footer, header and custom component closures; its argument is a file every closure appends a marker to when it runs (TestConformance_HeadlessModesNeverRunUIClosures).
    def headless_ui_probe(ctx, path):
        def mark(line):
            with open(path, "a", encoding="utf-8") as handle:
                handle.write(line + "\n")

        def footer(_width):
            mark("python:footer")
            return ["python:footer"]

        def header(_width):
            mark("python:header")
            return ["python:header"]

        class HeadlessComponent:
            def render(self, _width):
                mark("python:custom")
                return ["python:custom"]

            def handle_input(self, _data):
                mark("python:custom")
                return pig_sdk.RemoteComponentResult(done=True, value="closed")

        ctx.set_footer_renderer(footer)
        ctx.set_header_renderer(header)
        ctx.custom(HeadlessComponent())
        # Options only, no component: Pi's RPC and no-op UIs resolve undefined, the TUI SDK reports it unsupported.
        try:
            value = ctx.custom({})
            mark("python:custom-opaque:" + ("undefined" if value is None else "value"))
        except Exception:  # noqa: BLE001
            mark("python:custom-opaque:error")

    ext.command("headless-ui-probe", "Hand the SDK footer, header and custom component closures", headless_ui_probe)

    def editor_get_probe(ctx, _args):
        got = ctx.get_editor_component()
        state = "absent" if got is None else ("installed" if got is ConformanceEditor else "other")
        ctx.notify("editor_get:%s:end" % state, "info")

    ext.command("editor-get-probe", "Report the factory get_editor_component returns", editor_get_probe)
    # truncate_probe runs one SDK truncation helper on the JSON case in its argument and reports the result in Pi's shape (TestConformance_TruncationHelpersAgreeAcrossSDKs).
    def truncate_probe(ctx, args):
        case = json.loads(args)
        fn = case["fn"]
        if fn in ("head", "tail"):
            run = pig_sdk.truncate_head if fn == "head" else pig_sdk.truncate_tail
            r = run(case["content"], max_lines=case.get("maxLines"), max_bytes=case.get("maxBytes"))
            result = {"content": r.content, "truncated": r.truncated, "truncatedBy": r.truncated_by, "totalLines": r.total_lines, "totalBytes": r.total_bytes,
                      "outputLines": r.output_lines, "outputBytes": r.output_bytes, "lastLinePartial": r.last_line_partial,
                      "firstLineExceedsLimit": r.first_line_exceeds_limit, "maxLines": r.max_lines, "maxBytes": r.max_bytes}
        elif fn == "line":
            text, was = pig_sdk.truncate_line(case["content"], case.get("maxChars", pig_sdk.GREP_MAX_LINE_LENGTH))
            result = {"text": text, "wasTruncated": was}
        elif fn == "limits":
            result = {"maxLines": pig_sdk.DEFAULT_MAX_LINES, "maxBytes": pig_sdk.DEFAULT_MAX_BYTES}
        else:
            result = {"text": pig_sdk.format_size(case["bytes"])}
        ctx.notify("truncate:" + json.dumps(result) + ":end", "info")

    ext.command("truncate_probe", "Run a truncation helper on a JSON case and report the result", truncate_probe)
    # builtin_probe: see the Go fixture (testfixture/builtin_tool.go).
    def builtin_report(result):
        blocks = [{"type": "text", "text": b.get("text", "")} if b.get("type") == "text" else {"type": b.get("type"), "mimeType": b.get("mimeType")} for b in result.get("content", [])]
        report = {"content": blocks, "isError": bool(result.get("is_error"))}
        if result.get("details") is not None:
            report["details"] = result["details"]
            report["detailsJSON"] = json.dumps(result["details"], separators=(",", ":"), ensure_ascii=False)
        structured = result.get("structured_content")
        if isinstance(structured, dict):
            report["structuredKeys"] = list(structured.keys())
            report["exitCode"] = structured.get("exit_code")
        return report

    def builtin_options(case, log):
        custom = case.get("ops") == "custom"
        tool = case.get("tool")
        if tool == "read":
            ops = None
            if custom:
                def read_file(path):
                    log.append("readFile:" + path)
                    return ("OPS:" + path).encode()

                def access(path):
                    log.append("access:" + path)
                ops = pig_sdk.ReadOperations(read_file=read_file, access=access)
            return pig_sdk.ReadToolOptions(auto_resize_images=case.get("autoResizeImages"), operations=ops)
        if tool == "write":
            ops = None
            if custom:
                ops = pig_sdk.WriteOperations(write_file=lambda path, content: log.append("writeFile:%s:%s" % (path, content)), mkdir=lambda d: log.append("mkdir:" + d))
            return pig_sdk.WriteToolOptions(operations=ops)
        if tool == "edit":
            ops = None
            if custom:
                def edit_read(path):
                    log.append("readFile:" + path)
                    return b"hello world\n"
                ops = pig_sdk.EditOperations(read_file=edit_read, write_file=lambda path, content: log.append("writeFile:%s:%s" % (path, content)), access=lambda path: log.append("access:" + path))
            return pig_sdk.EditToolOptions(operations=ops)
        if tool == "ls":
            ops = None
            if custom:
                def exists(path):
                    log.append("exists:" + path)
                    return True

                def stat(path):
                    log.append("stat:" + path)
                    return pig_sdk.LsStat(is_directory="." not in path)

                def readdir(path):
                    log.append("readdir:" + path)
                    return ["b.txt", "a.txt", "sub"]
                ops = pig_sdk.LsOperations(exists=exists, stat=stat, readdir=readdir)
            return pig_sdk.LsToolOptions(operations=ops)
        if tool == "grep":
            ops = None
            if custom:
                def is_directory(path):
                    log.append("isDirectory:" + path)
                    raise RuntimeError("no such path for the probe")
                ops = pig_sdk.GrepOperations(is_directory=is_directory)
            return pig_sdk.GrepToolOptions(operations=ops)
        if tool == "find":
            ops = None
            if custom:
                def find_exists(path):
                    log.append("exists:" + path)
                    return False

                def glob(pattern, cwd, options):
                    log.append("glob:" + pattern)
                    return []
                ops = pig_sdk.FindOperations(exists=find_exists, glob=glob)
            return pig_sdk.FindToolOptions(operations=ops)
        if tool == "powershell":
            return pig_sdk.PowerShellToolOptions(expose_session_environment=case.get("exposeSessionEnvironment"))
        hook = None
        if case.get("hook"):
            def hook(spawn):
                log.append("spawnHook:" + spawn["command"])
                return {"command": "printf '%s|%s' \"$HOOK_VAR\" \"$PI_SESSION_ID\"", "cwd": spawn["cwd"], "env": {**spawn["env"], "HOOK_VAR": "hook-env"}}
        ops = None
        if custom:
            def exec_(command, cwd, options):
                log.append("exec:%s|cwd=%s" % (command, cwd))
                options.on_data(("custom:" + command).encode())
                return {"exitCode": 0}
            ops = pig_sdk.BashOperations(exec=exec_)
        return pig_sdk.BashToolOptions(command_prefix=case.get("commandPrefix") or None, expose_session_environment=case.get("exposeSessionEnvironment"), spawn_hook=hook, operations=ops)

    factories = {
        "read": (pig_sdk.create_read_tool_definition, pig_sdk.create_read_tool), "write": (pig_sdk.create_write_tool_definition, pig_sdk.create_write_tool),
        "edit": (pig_sdk.create_edit_tool_definition, pig_sdk.create_edit_tool), "bash": (pig_sdk.create_bash_tool_definition, pig_sdk.create_bash_tool),
        "grep": (pig_sdk.create_grep_tool_definition, pig_sdk.create_grep_tool), "find": (pig_sdk.create_find_tool_definition, pig_sdk.create_find_tool),
        "ls": (pig_sdk.create_ls_tool_definition, pig_sdk.create_ls_tool), "powershell": (pig_sdk.create_powershell_tool_definition, pig_sdk.create_powershell_tool),
    }

    def register_overrides(ctx, factory_cwd):
        read = pig_sdk.create_read_tool_definition(factory_cwd)
        run_read = read.execute

        def read_execute(ctx, params):
            result = run_read(ctx, params)
            blocks = [{**b, "text": "«delegated:" + b["text"] + "»"} if i == 0 else b for i, b in enumerate(result["content"])]
            return {**result, "content": blocks, "details": {"delegated": True}}

        ctx.register_tool(dataclasses.replace(read, execute=read_execute))
        bash = pig_sdk.create_bash_tool_definition(factory_cwd)
        run_bash = bash.execute

        def bash_execute(ctx, params):
            result = run_bash(ctx, params)
            blocks = [{**b, "text": "«" + b["text"] + "»"} if i == 0 else b for i, b in enumerate(result["content"])]
            return {**result, "content": blocks}

        ctx.register_tool(dataclasses.replace(bash, execute=bash_execute))
        return [read.name, bash.name]

    def local_probe(case):
        operations = pig_sdk.create_local_powershell_operations() if case.get("shell") == "powershell" else pig_sdk.create_local_bash_operations()
        signal = pig_sdk.ProviderSignal()
        chunks = []

        def on_data(data):
            chunks.append(bytes(data).decode("utf-8"))
            if case.get("abortOnOutput"):
                signal.set()

        options = pig_sdk.BashExecOptions(on_data=on_data, signal=signal, timeout=case.get("timeout"), env=case.get("env"))
        report = {}
        try:
            result = operations.exec(case["command"], case["factoryCwd"], options)
            report["exitCode"] = result["exitCode"]
        except Exception as error:  # noqa: BLE001
            report["error"] = str(error)
        report["output"] = "".join(chunks)
        return report

    def builtin_probe(ctx, args):
        case = json.loads(args)
        kind = case["kind"]
        if kind == "cwd":
            report = {"cwd": ctx.cwd}
        elif kind == "register":
            report = {"registered": register_overrides(ctx, case["factoryCwd"])}
        elif kind == "local":
            report = local_probe(case)
        elif kind == "sets":
            report = {"coding": [t.name for t in pig_sdk.create_coding_tools(case["factoryCwd"])], "readOnly": [t.name for t in pig_sdk.create_read_only_tools(case["factoryCwd"])]}
        else:
            log = []
            create_definition, create_tool = factories[case["tool"]]
            options = builtin_options(case, log)
            if kind == "definition":
                d = create_definition(case["factoryCwd"], options)
                report = {"definition": {"name": d.name, "label": d.label, "description": d.description, "promptSnippet": d.prompt_snippet or "", "promptGuidelines": d.prompt_guidelines,
                                         "parameters": d.parameters, "outputSchema": d.output_schema, "constrainedSampling": d.constrained_sampling, "executionMode": d.execution_mode or "",
                                         "renderShell": "self" if d.render_shell == "self" else ""}}
            else:
                signal = pig_sdk.ProviderSignal()
                if case.get("preAborted"):
                    signal.set()
                updates = []

                def collect(partial):
                    text = "\n".join(b.get("text", "") for b in partial.get("content", []) if b.get("type") == "text")
                    updates.append(text)
                    if case.get("abortOnOutput") and text != "":
                        signal.set()

                on_update = collect if case.get("updates") or case.get("abortOnOutput") else None
                report = {}
                try:
                    if case.get("noContext"):
                        result = create_tool(case["factoryCwd"], options).execute("probe-call", case.get("params") or {}, signal, on_update)
                    else:
                        result = create_definition(case["factoryCwd"], options).execute_with(ctx, "probe-call", case.get("params") or {}, signal, on_update)
                    report["result"] = builtin_report(result)
                except Exception as error:  # noqa: BLE001
                    report["error"] = str(error)
                if case.get("updates"):
                    report["updates"] = updates
                report["calls"] = log
        ctx.notify("builtin:" + json.dumps(report) + ":end", "info")

    ext.command("builtin_probe", "Delegate to a built-in tool through the SDK and report what it returned", builtin_probe)
    # message_probe reads the event payload in its JSON argument with the SDK's message_role and message_text helpers (TestConformance_MessageHelpersAgreeAcrossSDKs).
    def message_probe(ctx, args):
        data = json.loads(args)
        ctx.notify("message:" + json.dumps({"role": pig_sdk.message_role(data), "text": pig_sdk.message_text(data)}) + ":end", "info")

    ext.command("message_probe", "Report message_role and message_text of a JSON event payload", message_probe)
    # file_queue_probe runs the tasks in its JSON argument concurrently through the SDK's file mutation queue and reports the start/end log and the tasks that failed (TestConformance_FileMutationQueueAgreesAcrossSDKs).
    # Tasks are ordered by waiting on each other's progress, never by sleeping through a race: waitStarted/waitCalled delay the call into the queue, holdEnded/holdCalled keep a task inside it.
    # After a "called" signal a fixed settle time lets a task that wrongly skipped the queue show itself.
    def file_queue_probe(ctx, args):
        import threading
        import time

        case = json.loads(args)
        lock = threading.Lock()
        log, failed, progress = [], [], set()

        def mark(kind, task_id):
            with lock:
                progress.add((kind, task_id))

        def await_stage(kind, ids, who):
            for other in ids:
                deadline = time.monotonic() + 20
                while True:
                    with lock:
                        if (kind, other) in progress:
                            break
                    if time.monotonic() > deadline:
                        with lock:
                            log.append("timeout:%d:%s:%d" % (who, kind, other))
                        return
                    time.sleep(0.001)

        def settle(ids):
            if ids:
                time.sleep(case["settleMs"] / 1000)

        def run(task):
            tid = task["id"]
            await_stage("started", task.get("waitStarted", []), tid)
            await_stage("called", task.get("waitCalled", []), tid)
            settle(task.get("waitCalled", []))
            mark("called", tid)

            def body():
                with lock:
                    log.append("start:%d" % tid)
                mark("started", tid)
                await_stage("ended", task.get("holdEnded", []), tid)
                await_stage("called", task.get("holdCalled", []), tid)
                settle(task.get("holdCalled", []))
                with lock:
                    log.append("end:%d" % tid)
                mark("ended", tid)
                if task.get("fail"):
                    raise RuntimeError("task failed")

            try:
                pig_sdk.with_file_mutation_queue(task["path"], body)
            except Exception:
                mark("ended", tid)
                with lock:
                    failed.append(tid)

        threads = [threading.Thread(target=run, args=(task,)) for task in case["tasks"]]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join()
        ctx.notify("file_queue:" + json.dumps({"log": log, "errors": sorted(failed)}) + ":end", "info")

    ext.command("file_queue_probe", "Run tasks through the file mutation queue and report the log", file_queue_probe)
    # host_paths_probe reports getPackageDir() and getShellConfig() as this SDK answers them, for the default shell and for the existing and the missing custom shell path
    # in args ("<existing>|<missing>") (TestConformance_HostPathsAgreeAcrossSDKs).
    def host_paths_probe(ctx, args):
        existing, _, missing = args.partition("|")

        def shell(*custom):
            try:
                config = pig_sdk.get_shell_config(*custom)
            except OSError as error:
                return {"error": str(error)}
            report = {"shell": config.shell, "args": list(config.args)}
            if config.command_transport:
                report["commandTransport"] = config.command_transport
            return report

        ctx.notify("host_paths:" + json.dumps({"packageDir": pig_sdk.get_package_dir(), "default": shell(), "custom": shell(existing), "missing": shell(missing)}) + ":end", "info")

    ext.command("host_paths_probe", "Report getPackageDir and getShellConfig", host_paths_probe)
    # host_helpers_probe runs the host-served pi helpers over the golden file's inputs (TestConformance_HostHelpersAgreeAcrossSDKs). args is
    # "<golden>|<auth.json path>|<text to copy>".
    def host_helpers_probe(ctx, args):
        import base64

        golden_path, auth_path, text = args.split("|", 2)
        golden = json.loads(pathlib.Path(golden_path).read_text(encoding="utf-8"))
        diffs = []
        for case in golden["diffs"]:
            lines = case.get("contextLines")
            report = {"patch": ctx.generate_unified_patch(case.get("path", "src/a.ts"), case["oldContent"], case["newContent"], lines)}
            if not case.get("patchOnly"):
                diff = ctx.generate_diff_string(case["oldContent"], case["newContent"], lines)
                report["diff"], report["firstChangedLine"] = diff.diff, diff.first_changed_line
            diffs.append(report)
        entries = [pig_sdk.session_entry_to_context_messages(case["entry"]) for case in golden["entries"]]
        images = []
        for case in golden["images"]:
            if case["op"] == "convert":
                converted = ctx.convert_to_png(case["data"], case["mimeType"])
                images.append(None if converted is None else {"mimeType": converted.mime_type, "decodes": converted.data != ""})
                continue
            options = case.get("options")
            options = None if options is None else pig_sdk.ImageResizeOptions(options.get("maxWidth"), options.get("maxHeight"), options.get("maxBytes"), options.get("jpegQuality"))
            resized = ctx.resize_image(base64.b64decode(case["data"]), case["mimeType"], options)
            images.append(None if resized is None else {"mimeType": resized.mime_type, "originalWidth": resized.original_width, "originalHeight": resized.original_height, "width": resized.width, "height": resized.height, "wasResized": resized.was_resized, "decodes": resized.data != ""})
        creds = {
            "explicit": pig_sdk.read_stored_credential("anthropic", auth_path), "default": pig_sdk.read_stored_credential("anthropic"),
            "absentProvider": pig_sdk.read_stored_credential("nobody", auth_path), "missingFile": pig_sdk.read_stored_credential("anthropic", auth_path + ".missing"),
            "notJSON": pig_sdk.read_stored_credential("anthropic", auth_path + ".bad"),
        }
        ctx.copy_to_clipboard(text)
        ctx.notify("host_helpers:" + json.dumps({"diffs": diffs, "entries": entries, "images": images, "creds": creds}) + ":end", "info")

    ext.command("host_helpers_probe", "Run the host-served pi helpers over the golden inputs", host_helpers_probe)
    # pi_ai_helpers_probe runs every call of the vector calls (test/parity/sdk-vectors, Pi's own answers at the pin, listed in one file by the test) through this SDK's pi-ai
    # helpers and reports each answer as JSON (TestConformance_PiAiHelpersAgreeAcrossSDKs).
    def pi_ai_helpers_probe(ctx, args):
        golden = json.loads(pathlib.Path(args).read_text(encoding="utf-8"))
        calls = {
            "StringEnum": pig_sdk.string_enum, "getSupportedThinkingLevels": pig_sdk.get_supported_thinking_levels, "clampThinkingLevel": pig_sdk.clamp_thinking_level,
            "calculateCost": pig_sdk.calculate_cost, "contentText": pig_sdk.content_text, "getCurrentTools": pig_sdk.get_current_tools,
            "getCurrentSystemMessage": pig_sdk.get_current_system_message, "getCurrentSystemPrompt": pig_sdk.get_current_system_prompt,
            "getInitialSystemMessage": pig_sdk.get_initial_system_message, "getDeclaredTools": pig_sdk.get_declared_tools,
            "hasNonAdditiveToolChanges": pig_sdk.has_non_additive_tool_changes, "resolveTranscriptTools": pig_sdk.resolve_transcript_tools,
            "collapseSystemMessages": pig_sdk.collapse_system_messages, "resolveTranscript": pig_sdk.resolve_transcript,
            "withoutInitialSystemMessage": pig_sdk.without_initial_system_message, "normalizeContext": pig_sdk.normalize_context,
            "createInitialSystemMessage": pig_sdk.create_initial_system_message, "getSystemMessageText": pig_sdk.get_system_message_text,
            "renderSystemMessageUpdate": pig_sdk.render_system_message_update, "isRetryableAssistantError": pig_sdk.is_retryable_assistant_error,
            "isContextOverflow": pig_sdk.is_context_overflow, "isRecoverableLength": pig_sdk.is_recoverable_length, "retryDelayMs": pig_sdk.retry_delay_ms,
            "getModelType": pig_sdk.get_model_type, "isModelType": pig_sdk.is_model_type, "hasApi": pig_sdk.has_api, "modelsAreEqual": pig_sdk.models_are_equal,
        }
        cases = []
        for case in golden["cases"]:
            if case["fn"] == "DEFAULT_MAX_AGENT_RETRY_DELAY_MS":
                cases.append([pig_sdk.DEFAULT_MAX_AGENT_RETRY_DELAY_MS])
                continue
            value = calls[case["fn"]](*case["args"])
            cases.append([] if value is None else [value])
        auth = []
        for case in golden["auth"]["cases"]:
            seen = []

            def env(name, case=case, seen=seen):
                seen.append(name)
                return case["env"].get(name)

            result = pig_sdk.env_api_key_auth(case["name"], case["envVars"]).resolve(
                pig_sdk.APIKeyAuthInput(pig_sdk.AuthContext(env, lambda path: False), case.get("credential"), pig_sdk.ProviderSignal())
            )
            auth.append({"result": None if result is None else {key: value for key, value in dataclasses.asdict(result).items() if value is not None}, "seen": seen})
        prompts = []
        credential = pig_sdk.env_api_key_auth("Name", ["FIRST", "SECOND"]).login(
            pig_sdk.AuthInteraction(lambda request: prompts.append(request) or "typed-key", lambda request: None, pig_sdk.ProviderSignal())
        )
        timing = []
        for case in golden["timing"]:
            stream = pig_sdk.create_assistant_message_event_stream()

            def message(now, case=case):
                result = {"role": "assistant", "content": [], "stopReason": "error" if case["kind"] == "error" else "stop"}
                if now or case["timestamp"] == "now":
                    result["timestamp"] = int(time.time() * 1000)
                elif case["timestamp"] == "old":
                    result["timestamp"] = 1
                if case["preset"] is not None and not now:
                    result["durationMs"] = case["preset"]
                return result

            final, later = message(False), message(True)
            if case["kind"] == "done":
                stream.push({"type": "done", "reason": "stop", "message": final})
            elif case["kind"] == "error":
                stream.push({"type": "error", "reason": "error", "error": final})
            elif case["kind"] == "end":
                stream.end(final)
            else:
                stream.push({"type": "done", "reason": "stop", "message": final})
                stream.end(later)
            value = final.get("durationMs")
            timing.append({"present": "durationMs" in final, "value": case["preset"] if case["preset"] is not None and value == case["preset"] else None, "laterTimed": "durationMs" in later})
        ctx.notify("pi_ai_helpers:" + json.dumps({"cases": cases, "timing": timing, "auth": auth, "login": {"prompts": prompts, "result": credential}}) + ":end", "info")

    ext.command("pi_ai_helpers_probe", "Run the golden pi-ai helper calls", pi_ai_helpers_probe)
    # host_info_probe reports Pi's getAgentDir(), CONFIG_DIR_NAME and VERSION as this SDK answers them (TestConformance_HostConfigurationAgreesAcrossSDKs).
    ext.command(
        "host_info_probe",
        "Report getAgentDir, CONFIG_DIR_NAME and VERSION",
        lambda ctx, _args: ctx.notify(
            "host_info:" + json.dumps({"agentDir": pig_sdk.get_agent_dir(), "configDirName": pig_sdk.get_config_dir_name(), "version": pig_sdk.get_version(), "factoryAgentDir": factory_agent_dir}) + ":end", "info"
        ),
    )
    # ui_dialog_values_probe reports the value each dialog resolves with (TestConformance_UIDialogValuesAgreeAcrossSDKs): `<undefined>` for Pi's undefined, else the JSON of the value.
    def ui_dialog_values_probe(ctx, _args):
        def report(name, value, ok):
            text = json.dumps(value, separators=(",", ":")) if ok else "<undefined>"
            ctx.notify(f"{name}={text}", "info")
        for title in ("S1", "S2", "S3", "S4"):
            value, ok = ctx.select(title, ["a", "b"])
            report(title, value, ok)
        for title in ("C1", "C2", "C3", "C4"):
            report(title, ctx.confirm(title, "m"), True)
        for title in ("I1", "I2"):
            value, ok = ctx.input(title, "p")
            report(title, value, ok)
        for title in ("E1", "E2"):
            value, ok = ctx.editor(title, "p")
            report(title, value, ok)

    ext.command("ui_dialog_values_probe", "Report the typed result of each dialog", ui_dialog_values_probe)
    # ui_forward_probe calls the UI members whose host effect a conformance row records (TestConformance_UIForwardingAgreesAcrossSDKs).
    def ui_forward_probe(ctx, _args):
        ctx.set_working_message("Thinking hard")
        ctx.set_working_message("")
        ctx.set_working_message(None)
        ctx.set_hidden_thinking_label("Pondering")
        ctx.set_hidden_thinking_label("")
        ctx.set_hidden_thinking_label(None)
        ctx.set_working_visible(False)
        ctx.set_working_indicator({"frames": ["-", "+"], "intervalMs": 120})
        ctx.paste_to_editor("pasted \U0001F600")
        ctx.set_editor_text("draft")
        ctx.set_tools_expanded(True)

    ext.command("ui_forward_probe", "Call the working, paste, editor-text and tools-expanded UI members", ui_forward_probe)

    # dialog-stack-probe opens its dialogs at the same time, each from its own thread, and reports each one's answer as it settles (TestConformance_ConcurrentDialogsAgreeAcrossSDKs). "pair" is a select A and a confirm B; "mix" is a select S, a confirm C, an input I and an editor E.
    def dialog_stack_probe(ctx, scenario):
        def chosen(result):
            value, ok = result
            return value if ok else "cancelled"

        dialogs = {
            "A": lambda: chosen(ctx.select("A", ["a1", "a2"])),
            "B": lambda: str(ctx.confirm("B", "message")).lower(),
            "S": lambda: chosen(ctx.select("S", ["s1", "s2"])),
            "C": lambda: str(ctx.confirm("C", "message")).lower(),
            "I": lambda: chosen(ctx.input("I", "placeholder")),
            "E": lambda: chosen(ctx.editor("E", "prefill")),
        }
        failures = []

        def run(name):
            try:
                ctx.notify("stack settled %s=%s" % (name, dialogs[name]()), "info")
            except Exception as err:  # reported through the command's error below
                failures.append(err)

        threads = [threading.Thread(target=run, args=(name,)) for name in (["S", "C", "I", "E"] if scenario == "mix" else ["A", "B"])]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join()
        if failures:
            raise failures[0]
        ctx.notify("stack done", "info")

    ext.command("dialog-stack-probe", "Open concurrent dialogs from one command", dialog_stack_probe)

    # ui_wire_empties_probe calls the UI members Pi's RPC mode writes with empty strings and lists, and the optional members it omits, in this SDK's own spelling of each (TestConformance_UIEmptyMembersAgreeAcrossSDKs).
    def ui_wire_empties_probe(ctx, _args):
        ctx.set_status("k", "")
        ctx.clear_status("k")
        ctx.notify("", "")
        ctx.notify("typed", "warning")
        ctx.set_title("")
        ctx.set_editor_text("")
        ctx.select("T", [])
        ctx.confirm("", "")
        ctx.input("T1")
        ctx.input("T2", "")
        ctx.input("T3", "ph")
        ctx.editor("E1")
        ctx.editor("E2", "")
        ctx.editor("E3", "pre")

    ext.command("ui_wire_empties_probe", "Call the UI members with empty and unset arguments", ui_wire_empties_probe)
    # tool_call_guard_probe reports which tool names isToolCallEventType accepts for each tool_call event it receives (TestConformance_ToolCallGuardAgreesAcrossSDKs).
    def tool_call_guard_probe(ctx, args):
        names = ["bash", "read", "probe", "other"]
        ext.on_event("tool_call", lambda hctx, data: hctx.notify("tool_call_guard:%s:end" % ",".join(n for n in names if pig_sdk.is_tool_call_event_type(n, data)), "info"))

    ext.command("tool_call_guard_probe", "Report the tool names the tool_call guard accepts", tool_call_guard_probe)
    ext.command("editor-clear", "Restore the host's editor", lambda ctx, _args: ctx.set_editor_component(None))
    ext.command("schema-probe", "Report schema rejection", lambda ctx, _args: ctx.notify("schema-rejected:" + str(schema_rejected).lower(), "info"))
    for name, kind, value in [("flag-true", "boolean", True), ("flag-false", "boolean", False), ("flag-string", "string", "default"), ("flag-empty", "string", ""), ("flag-unset", "string", None)]:
        ext.flag(name, flag_type=kind, default=value)
    ext.command("flag-probe", "Report registered flag values", lambda ctx, _args: ctx.notify(json.dumps([ctx.get_flag(name) for name in ["flag-true", "flag-false", "flag-string", "flag-empty", "flag-unset", "unregistered"]]), "info"))
    def registry_session(ctx, _args):
        s, r = ctx.session_manager, ctx.model_registry
        out = {
            "cwd": s.get_cwd(), "dir": s.get_session_dir(), "id": s.get_session_id(),
            "name": s.get_session_name(), "leaf": s.get_leaf_id(),
            "entry": s.get_entry("one"), "missing": s.get_entry("missing"), "label": s.get_label("one"),
            "entries": s.get_entries(), "branch": s.get_branch("one"), "tree": s.get_tree(),
            "contextEntries": s.build_context_entries(), "projection": s.build_session_projection(),
            "persisted": s.is_persisted(), "usesDefaultDir": s.uses_default_session_dir(), "children": s.get_children("one"), "sessionContext": s.build_session_context(),
            "models": r.get_all(), "available": r.get_available(), "status": r.get_provider_auth_status("registry-probe"),
            "display": r.get_provider_display_name("registry-probe"), "error": r.get_error(),
            "config": r.get_registered_provider_config("registry-probe"), "ids": r.get_registered_provider_ids(),
            "auth": r.get_provider_auth("registry-probe"), "apiKey": r.get_api_key_for_provider("registry-probe"),
            "missingKey": r.get_api_key_for_provider("missing"), "refresh": r.refresh({"allowNetwork": False}),
        }
        ctx.notify(json.dumps(out), "info")
    def session_order(ctx, _args):
        s = ctx.session_manager
        out = {
            "getEntries": s.get_entries(), "getEntry": s.get_entry("a4"), "getLeafEntry": s.get_leaf_entry(),
            "getBranch": s.get_branch("a4"), "getChildren": s.get_children("a1"), "getTree": s.get_tree(),
            "buildContextEntries": s.build_context_entries(), "buildSessionProjection": s.build_session_projection(),
            "buildSessionContext": s.build_session_context(),
        }
        ctx.notify(json.dumps(out, separators=(",", ":"), ensure_ascii=False), "info")
    ext.command("session-order", "Read the session as Pi returns it", session_order)
    def timeout_probe(ctx, _args):
        for timeout in (0.5, 4294967296.5, 1e21):
            ctx.exec("timeout-command", [], timeout=timeout)
        ctx.select("timeout", ["a"], timeout=1500.5)

    ext.command("timeout-probe", "Send JavaScript-number timeouts", timeout_probe)

    def exec_reject_probe(ctx, args):
        for command in json.loads(args):
            try:
                ctx.notify(f"resolved:{ctx.exec(command, []).exit_code}", "info")
            except RuntimeError as error:
                ctx.notify(f"rejected:{error}", "info")

    def exec_abort_probe(ctx, args):
        probe = json.loads(args)
        signal = pig_sdk.ProviderSignal()
        if probe.get("pre"):
            signal.set()
        else:
            def abort_when_started():
                deadline = time.monotonic() + 15
                while time.monotonic() < deadline and not pathlib.Path(probe["pidFile"]).exists():
                    time.sleep(0.01)
                signal.set()

            threading.Thread(target=abort_when_started, daemon=True).start()
        try:
            result = ctx.exec(probe["command"], probe["args"], signal=signal)
            ctx.notify(f"exec-abort:killed={str(result.killed).lower()};code={result.exit_code};stdout={result.stdout.strip()}", "info")
        except RuntimeError as error:
            ctx.notify(f"rejected:{error}", "info")

    ext.command("exec-abort-probe", "Abort a running exec through its signal", exec_abort_probe)

    def language_probe(ctx, args):
        ctx.notify("language:%s:end" % json.dumps({"lang": pig_sdk.get_language_from_path(json.loads(args)["path"])}, separators=(",", ":")), "info")

    ext.command("language_probe", "Report the highlight language of a path", language_probe)
    def key_lists(value, out=None):
        out = [] if out is None else out
        if isinstance(value, list):
            for element in value:
                key_lists(element, out)
        elif isinstance(value, dict):
            out.append(",".join(value.keys()))
            for element in value.values():
                key_lists(element, out)
        return out

    def helper_report(fn, text, call=None):
        try:
            if fn == "date":
                ms = (call or pig_sdk.parse_date)(text)
                return {"ms": ms}
            if fn == "strip":
                return {"body": (call or pig_sdk.strip_frontmatter)(text)}
            parsed = (call or pig_sdk.parse_frontmatter)(text)
            return {"frontmatter": parsed.frontmatter, "body": parsed.body, "keys": key_lists(parsed.frontmatter)}
        except Exception:
            return {"error": True}

    # The helpers run here, while the factory runs, before the connection to the host is ready.
    factory_helpers = {"frontmatter": helper_report("frontmatter", "---\nz: 1\n10: a\n2: b\nnested:\n  y: 1\n  3: x\n  \"q r\": [1, 2]\n---\nbody text\n"), "error": helper_report("frontmatter", "---\na: [\n---\nx"), "date": helper_report("date", "2020-01-02T03:04:05.678Z"), "strip": helper_report("strip", "---\nz: 1\n---\nbody text\n")}

    def helper_probe(ctx, args):
        c = json.loads(args)
        call = {"date": ctx.parse_date, "strip": ctx.strip_frontmatter}.get(c["fn"], ctx.parse_frontmatter)
        ctx.notify("helper:%s:end" % json.dumps(helper_report(c["fn"], c["input"], call), separators=(",", ":")), "info")

    ext.command("helper_probe", "Run parseFrontmatter, stripFrontmatter or Date.parse on its argument", helper_probe)

    def helper_factory_probe(ctx, args):
        ctx.notify("helper-factory:%s:end" % json.dumps(factory_helpers, separators=(",", ":")), "info")

    ext.command("helper_factory_probe", "Report the helpers' results from the factory", helper_factory_probe)
    def convert_probe(ctx, args):
        c = json.loads(args)
        if c["fn"] == "convert":
            result = pig_sdk.convert_to_llm(c["messages"])
        else:
            i = c["image"]
            result = pig_sdk.format_dimension_note(pig_sdk.ResizedImage(i["originalWidth"], i["originalHeight"], i["width"], i["height"], i["wasResized"]))
        ctx.notify("convert:%s:end" % json.dumps({"result": result}, separators=(",", ":")), "info")

    ext.command("convert_probe", "Run convertToLlm or formatDimensionNote on a JSON case and report the result", convert_probe)
    def serialize_probe(ctx, args):
        c = json.loads(args)
        try:
            out = {"result": pig_sdk.serialize_conversation(c["messages"])}
        except TypeError:
            out = {"error": True}
        ctx.notify("serialize:%s:end" % json.dumps(out, separators=(",", ":")), "info")

    ext.command("serialize_probe", "Run serializeConversation on JSON messages and report the text", serialize_probe)
    class _EntrySession:
        def __init__(self, entries):
            self.by_id = {e["id"]: e for e in entries}

        def get_entry(self, entry_id):
            return self.by_id.get(entry_id)

        def get_branch(self, from_id=None):
            path, cur = [], self.by_id.get(from_id)
            while cur:
                path.insert(0, cur)
                cur = self.by_id.get(cur["parentId"]) if cur.get("parentId") else None
            return path

    def _json_nan(value):
        if isinstance(value, float) and value != value:
            return None
        if isinstance(value, list):
            return [_json_nan(v) for v in value]
        if isinstance(value, dict):
            return {k: _json_nan(v) for k, v in value.items()}
        return value

    def branch_probe(ctx, args):
        c = json.loads(args)
        try:
            if c["fn"] == "prepare":
                r = pig_sdk.prepare_branch_entries(c["entries"], c["budget"])
                out = {"messages": r.messages, "fileOps": {"read": r.file_ops.read, "written": r.file_ops.written, "edited": r.file_ops.edited}, "totalTokens": r.total_tokens}
            else:
                r = pig_sdk.collect_entries_for_branch_summary(_EntrySession(c["entries"]), c["old"], c["target"])
                out = {"entries": r.entries, "commonAncestorId": r.common_ancestor_id}
        except TypeError:
            out = {"error": True}
        ctx.notify("branch:%s:end" % json.dumps(_json_nan(out), separators=(",", ":")), "info")

    ext.command("branch_probe", "Run a branch summarization helper on a JSON case and report the result", branch_probe)
    def compact_usage_probe(ctx, args):
        c = json.loads(args)
        fn = c["fn"]
        if fn == "calc":
            result = {"tokens": pig_sdk.calculate_context_tokens(c["usage"])}
        elif fn == "should":
            s = c["settings"]
            result = {"result": pig_sdk.should_compact(c["tokens"], c["window"], pig_sdk.CompactionSettings(s["enabled"], s["reserveTokens"], s["keepRecentTokens"]))}
        elif fn == "last":
            usage = pig_sdk.get_last_assistant_usage(c["entries"])
            result = {"usage": None if usage is None else {key: 0 if usage.get(key) is None else usage[key] for key in ("input", "output", "cacheRead", "cacheWrite", "totalTokens")}}
        elif fn == "cut":
            cut = pig_sdk.find_cut_point(c["entries"], c["start"], c["end"], c["keep"])
            result = {"firstKeptEntryIndex": cut.first_kept_entry_index, "turnStartIndex": cut.turn_start_index, "isSplitTurn": cut.is_split_turn}
        elif fn == "turn":
            result = {"index": pig_sdk.find_turn_start_index(c["entries"], c["index"], c["start"])}
        elif fn == "estimate":
            result = {"tokens": pig_sdk.estimate_tokens(c["message"])}
        else:
            d = pig_sdk.DEFAULT_COMPACTION_SETTINGS
            result = {"enabled": d.enabled, "reserveTokens": d.reserve_tokens, "keepRecentTokens": d.keep_recent_tokens}
        ctx.notify("compact:%s:end" % json.dumps(result, separators=(",", ":")), "info")

    ext.command("compact_usage_probe", "Run a compaction usage helper on a JSON case and report the result", compact_usage_probe)
    def skills_probe(ctx, args):
        c = json.loads(args)
        fn = c["fn"]
        if fn == "latest":
            result = {"entry": pig_sdk.get_latest_compaction_entry(c["entries"])}
        elif fn == "parse":
            block = pig_sdk.parse_skill_block(c["text"])
            result = {"block": None if block is None else {"name": block.name, "location": block.location, "content": block.content, "userMessage": block.user_message}}
        elif fn == "format":
            tool = c.get("tool")
            result = {"text": pig_sdk.format_skills_for_prompt(c["skills"]) if tool is None else pig_sdk.format_skills_for_prompt(c["skills"], tool)}
        else:
            result = {"version": pig_sdk.CURRENT_SESSION_VERSION, "virtual": pig_sdk.VIRTUAL_MODEL_STATE_ENTRY}
        ctx.notify("skills:%s:end" % json.dumps(result, separators=(",", ":"), ensure_ascii=False), "info")

    ext.command("skills_probe", "Run a session or skill helper on a JSON case and report the result", skills_probe)
    ext.command("exec-reject-probe", "Report exec outcomes", exec_reject_probe)
    ext.command("thinking-model", "Read the thinking level and switch the model", lambda ctx, _args: (ctx.set_thinking_level("high"), ctx.notify(json.dumps([ctx.get_thinking_level(), ctx.set_model("probe/model")[0]], separators=(",", ":")), "info"))[-1])
    def replicated_accessors(ctx, _args):
        out = {"name0": ctx.session_name, "model0": ctx.model, "provider0": ctx.model_provider}
        ctx.set_session_name("renamed")
        out["name1"] = ctx.session_name
        ctx.set_label("one", "lab")
        out["entryAfterLabel"] = ctx.get_entries()[-1]["type"]
        ctx.send_message("probe", "hi", True)
        out["entryAfterMessage"] = ctx.get_entries()[-1]["type"]
        ctx.set_model("prov-after/id-after")
        out["model1"], out["provider1"], out["qualified1"] = ctx.model, ctx.model_provider, ctx.model_qualified
        ctx.notify(json.dumps(out), "info")
    ext.command("replicated-accessors", "Read the replicated accessors after each write", replicated_accessors)
    ext.command("active_tools_set", "Set the active tools to the comma-separated names", lambda ctx, args: ctx.set_active_tools(args.strip().split(",")))
    def session_actions_unbound(ctx, _args):
        parts = []
        for name, call in (("new", lambda: ctx.new_session()), ("fork", lambda: ctx.fork("entry")), ("navigate", lambda: ctx.navigate_tree("entry")), ("switch", lambda: ctx.switch_session("/s.jsonl"))):
            try:
                parts.append("%s=%s" % (name, str(call()["cancelled"]).lower()))
            except Exception as error:
                parts.append("%s=error:%s" % (name, error))
        try:
            ctx.reload()
            parts.append("reload=ok")
        except Exception as error:
            parts.append("reload=error:%s" % error)
        ctx.notify("unbound:" + ",".join(parts), "info")

    ext.command("session_actions_unbound", "Report what unbound session actions answer", session_actions_unbound)
    ext.command("active_tools_get", "Report the active tools", lambda ctx, _args: ctx.notify("active_tools:%s" % ",".join(ctx.get_active_tools()), "info"))
    # provider_probe_register and provider_probe_unregister register and unregister a plain provider after the extension connected.
    ext.command("provider_probe_register", "Register a provider with one model", lambda ctx, _args: ext.register_provider("conformance-probe", json.loads(r'''{"baseUrl":"https://probe.invalid/v1","api":"openai-completions","apiKey":"probe-key","models":[{"id":"probe-model","name":"Probe Model","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":1000,"maxTokens":100}]}''')))
    ext.command("provider_probe_unregister", "Unregister the provider", lambda ctx, _args: ext.unregister_provider("conformance-probe"))
    ext.command("commands-probe", "Read the host's slash commands", lambda ctx, _args: ctx.notify(json.dumps([[c["name"], c["source"], c.get("description", "")] for c in ctx.get_commands() if c["name"] == "conformance-listed"], separators=(",", ":")), "info"))
    ext.command("session-identity", "Read context identity accessors", lambda ctx, _args: ctx.notify(json.dumps([ctx.get_session_id(), ctx.get_session_file(), ctx.get_leaf_id(), ctx.get_session_name()]), "info"))
    ext.command("registry-session", "Read registry and session facades", registry_session)

    def registry_edges(ctx, _args):
        r = ctx.model_registry
        models = [*r.get_all(), {}]
        ctx.notify(json.dumps({
            "available": r.get_available(),
            "hasAuth": [r.has_configured_auth(model) for model in models],
            "oauth": [r.is_using_oauth(model) for model in models],
            "found": r.find("registry-probe", "a"), "foundOff": r.find("registry-off", "d"),
            "missing": r.find("registry-probe", "zzz"), "mismatch": r.find("registry-probe", "c"),
        }), "info")
    ext.command("registry-edges", "Read the registry answers that depend on the provider state", registry_edges)

    def registry_unbound_reads(ctx, _args):
        def report(read):
            try:
                return "ok:" + json.dumps(read())
            except Exception as error:  # noqa: BLE001 - the row reports every failure
                return f"error:{error}"
        ctx.notify(json.dumps({"find": report(lambda: ctx.model_registry.find("registry-probe", "a")), "entries": report(ctx.session_manager.get_entries)}), "info")
    ext.command("registry-unbound-reads", "Report find and a session read against a host that cannot answer", registry_unbound_reads)
    ext.message_renderer(
        "conformance-message",
        lambda _ctx, message, options, width: [json.dumps(options)] if message.get("content") == "padding-options" else [
            "renderer:%s:expanded=%s:width=%d"
            % (message.get("content", ""), str(bool(options.get("expanded"))).lower(), width)
        ],
    )
    ext.message_view_renderer("kit-message", lambda _ctx, _message, _options, _width: kit_probe_view())
    ext.entry_view_renderer("kit-entry", lambda _ctx, _entry, _options, _width: kit_probe_view())
    def transform_markdown(markdown, context):
        # "trace:<dir>:<name>" records the body's entry in <dir>/trace; the body named "first" returns only once <dir>/release exists. newline="" keeps "\n" from becoming CRLF on Windows.
        if markdown.startswith("trace:"):
            # The name is the last field: a Windows <dir> contains a drive colon.
            directory, name = markdown[len("trace:"):].rsplit(":", 1)
            with open(directory + "/trace", "a", newline="") as trace:
                trace.write("entered %s\n" % name)
            while name == "first" and not pathlib.Path(directory + "/release").exists():
                time.sleep(0.01)
            if name == "first":
                with open(directory + "/trace", "a", newline="") as trace:
                    trace.write("returned first\n")
        return "md:%s:%s:streaming=%s:width=%d" % (markdown, context.get("messageType", ""), str(bool(context.get("isStreaming"))).lower(), int(context.get("availableWidth") or 0))

    ext.markdown_transformer(transform_markdown)
    def resolved_line(prefix: str, tool: str):
        return lambda _ctx, args, _render, _width: ["%s:%s:%s" % (prefix, tool, args.get("q"))]

    def tool_renderer(tool: str, next_renderers):
        if tool == "conformance_tool_renderer":
            return pig_sdk.ToolRenderers(render_call=resolved_line("resolved", tool))
        if tool == "kit_resolved_tool":
            # The resolver draws a call and its result as the kit probe (D107).
            renderers = pig_sdk.ToolRenderers()
            renderers.call_view = lambda _ctx, _args, _render, _width: kit_probe_view()
            renderers.result_view = lambda _ctx, _result, _options, _render, _width: kit_probe_view()
            return renderers
        if tool == "conformance_no_renderer":
            return None
        if tool == "conformance_fill":
            return next_renderers() or pig_sdk.ToolRenderers(render_call=resolved_line("filled", tool))
        if tool == "conformance_wrap":
            return dataclasses.replace(next_renderers(), render_call=resolved_line("wrapped", tool))
        return next_renderers()

    ext.tool_renderer(tool_renderer)

    def late_tool_renderer(_ctx: pig_sdk.Context, _args: str) -> None:
        ext.tool_renderer(
            lambda tool, next_renderers: pig_sdk.ToolRenderers(render_call=resolved_line("late", tool))
            if tool == "conformance_late"
            else next_renderers()
        )

    ext.command("late_tool_renderer", "Register a tool renderer resolver after loading", late_tool_renderer)
    ext.entry_renderer(
        "conformance-entry",
        lambda _ctx, entry, options, width: [
            "entryrenderer:%s:expanded=%s:width=%d"
            % (entry.get("data", ""), str(bool(options.get("expanded"))).lower(), width)
        ],
    )

    def echo(ctx: pig_sdk.Context, params: dict) -> dict:
        return {"content": "echo: " + str(params.get("text", ""))}

    def throw_probe(ctx: pig_sdk.Context, params: dict) -> dict:
        kind = params.get("kind")
        if kind == "unicode":
            raise RuntimeError("é😀 \n x")
        if kind == "empty":
            raise RuntimeError("")
        if kind == "long":
            raise RuntimeError("x" * 100000)
        return {"content": "ok"}

    ext.tool("throw_probe", "Throw a value of the requested kind", {"type": "object", "properties": {"kind": {"type": "string"}}}, throw_probe)

    def tool_error(ctx: pig_sdk.Context, params: dict) -> dict:
        raise RuntimeError("tool exploded")

    def tool_is_error(ctx: pig_sdk.Context, params: dict) -> dict:
        return {"content": "soft tool error", "is_error": True}

    ext.tool("echo", "Echo input text", {"type": "object", "required": ["text"], "properties": {"text": {"type": "string", "description": "Text to echo"}, "offset": {"type": "number"}}}, echo)
    ext.tool("render_probe", "Render its own tool card", {"type": "object", "properties": {}}, lambda _ctx, _params: "render ok")

    def render_probe_call(_ctx, args, render, width):
        render.state["calls"] = render.state.get("calls", 0) + 1
        return ["toolrender:call:%s:partial=%s:calls=%d:width=%d" % (args.get("topic"), str(render.is_partial).lower(), render.state["calls"], width)]

    def render_probe_result(_ctx, result, options, render, width):
        return [
            "toolrender:result:%s:%s:expanded=%s:calls=%s:width=%d:duration=%s"
            % (result["content"][0]["text"], result["details"]["k"], str(bool(options.get("expanded"))).lower(), render.state.get("calls"), width, "none" if render.duration_ms is None else render.duration_ms)
        ]

    ext.tool_renderers("render_probe", render_call=render_probe_call, render_result=render_probe_result, render_shell="self")
    abort_observed = [False]
    abort_reason = [None]

    def update_tool(ctx, _params):
        ctx.on_update({"content": [{"type": "text", "text": "step 1"}]})
        ctx.on_update({"content": [{"type": "text", "text": "step 2"}]})
        return {"content": [{"type": "text", "text": "done"}]}

    def ordered_details(ctx, _params):
        ctx.on_update({"content": [{"type": "text", "text": "partial"}], "details": {"zeta": 1.0, "alpha": {"yy": 2, "bb": 3}, "mid": [{"qq": 1, "aa": 2}]}})
        return {"content": [{"type": "text", "text": "done"}], "details": {"zeta": 1.0, "alpha": {"yy": 2, "bb": 3}, "mid": [{"qq": 1, "aa": 2}]}}

    def ordered_result(ctx, _params):
        ctx.on_update({"details": {"k": 1}, "content": [{"type": "text", "text": "partial"}]})
        return {"details": {"k": 1}, "is_error": True, "content": [{"type": "text", "text": "done"}]}

    def abort_tool(ctx, _params):
        ctx.on_update({"content": [{"type": "text", "text": "waiting"}]})
        while not ctx.is_cancelled():
            time.sleep(0.01)
        abort_reason[0] = ctx.cancellation_reason()
        abort_observed[0] = True
        return {"content": "aborted"}

    # hang_tool ignores its abort signal for longer than the host's abort grace period (D111).
    def hang_tool(ctx, _params):
        ctx.on_update({"content": [{"type": "text", "text": "waiting"}]})
        time.sleep(8)
        return {"content": "late"}

    ext.tool("big_out", "Return text of the size in the argument", {"type": "object", "properties": {}}, lambda ctx, params: {"content": [{"type": "text", "text": "a" * int(params["n"])}]})
    ext.tool("echo_params", "Return the arguments as the result details", {"type": "object", "properties": {}}, lambda ctx, params: {"content": [{"type": "text", "text": "echoed"}], "details": params})
    ext.tool("nan_out", "Return details holding NaN and Infinity", {"type": "object", "properties": {}}, lambda ctx, params: {"content": [{"type": "text", "text": "nan"}], "details": {"n": float("nan"), "i": float("-inf"), "l": [float("nan"), 2, {"d": float("inf")}], "ok": 1}})
    ext.tool("update_tool", "Stream two partial results", {"type": "object", "properties": {}}, update_tool)
    ext.tool("ordered_details", "Return details whose members are not in alphabetical order", {"type": "object", "properties": {}}, ordered_details)
    ext.tool("ordered_result", "Return a result whose members are not in the declared order", {"type": "object", "properties": {}}, ordered_result)
    ext.tool("abort_tool", "Wait for the abort signal", {"type": "object", "properties": {}}, abort_tool)
    ext.tool("hang_tool", "Ignore the abort signal", {"type": "object", "properties": {}}, hang_tool)
    ext.command(
        "abort_probe",
        "Report whether abort_tool saw its abort signal",
        lambda ctx, args: ctx.notify("abort:" + ("true" if abort_observed[0] else "false"), "info"),
    )
    ext.command("abort_reason", "Report the cancellation reason abort_tool saw", lambda ctx, args: ctx.notify("reason:" + (abort_reason[0] or ""), "info"))
    ext.tool(
        "rich_tool",
        "Return text, image, and terminate",
        {"type": "object", "properties": {}},
        lambda _ctx, _params: {
            "content": [
                {"type": "text", "text": "  padded  "},
                {"type": "image", "data": "aW1n", "mimeType": "image/png"},
                {"type": "text", "text": "tail\n"},
            ],
            "terminate": True,
        },
    )
    ext.tool(
        "prepared_tool",
        "Transform legacy arguments before execution",
        {"type": "object", "required": ["text"], "properties": {"text": {"type": "string"}}},
        lambda _ctx, params: {"content": "prepared:" + str(params.get("text", ""))},
        prepare_arguments=lambda params: {"text": params.get("legacy")},
    )
    # Reports the order in which calls start: the number of calls that started before it, plus its own argument.
    started_calls = [0]
    started_lock = threading.Lock()

    def start_order(_ctx, params):
        with started_lock:
            started_calls[0] += 1
            number = started_calls[0]
        return {"content": f"start#{number} n={params.get('n')}"}

    # A tool that notifies inline and, from a thread released by timer_release, after its handler returned (D189).
    timer_gate = threading.Semaphore(0)

    stale_contexts = []

    def timer_defer(ctx, _params):
        stale_contexts.append(ctx)
        ctx.notify("inline:sync", "info")

        def deferred():
            timer_gate.acquire()
            ctx.notify("deferred:thread", "info")

        threading.Thread(target=deferred, daemon=True).start()
        return {"content": "queued"}

    # timer_stale awaits a host call it makes through the context of the earlier timer_defer request, which Pi-port extensions keep (an activeCtx), and returns once it was applied. The call is not detached work: the handler waits for it, so the host must not hold it for a loop turn that waits for the handler.
    def timer_stale(_ctx, _params):
        stale_contexts[0].notify("stale:awaited", "info")
        return {"content": "applied"}

    def timer_release(_ctx, _params):
        timer_gate.release()
        return {"content": "released"}

    ext.tool("timer_defer", "Notify inline and from deferred work", {"type": "object"}, timer_defer)
    ext.tool("timer_release", "Release the deferred work", {"type": "object"}, timer_release)
    ext.tool("timer_stale", "Await a host call made through an earlier request's context", {"type": "object"}, timer_stale)
    # order_probe makes the host calls and updates whose order the ordering rows pin (ordering_test.go): its first acts are two synchronous host calls, then one update, and the tool_result handler below reports each result as its first act.
    def order_probe(ctx, params):
        ctx.notify(f"order-call={params.get('n')}", "info")
        ctx.notify(f"order-then={params.get('n')}", "info")
        ctx.on_update({"content": [{"type": "text", "text": f"order-update={params.get('n')}"}]})
        return {"content": f"order-done n={params.get('n')}"}

    ext.tool("order_probe", "Make a host call and an update in the first tick", {"type": "object", "properties": {"n": {"type": "number"}}}, order_probe)
    # mutation_order_probe takes the file mutation queue in its first statement and appends its tag to the file (TestFileMutationOrderAcrossSDKs).
    def mutation_order_probe(ctx, params):
        def body():
            # Bytes, as the Go, Rust and Node fixtures write them: text mode turns "\n" into "\r\n" on Windows.
            path = pathlib.Path(params["file"])
            before = path.read_bytes() if path.exists() else b""
            path.write_bytes(before + (params["tag"] + "\n").encode())

        pig_sdk.with_file_mutation_queue(params["file"], body)
        return {"content": "mutated " + params["tag"]}

    ext.tool("mutation_order_probe", "Take the file mutation queue and append a tag to the file", {"type": "object", "properties": {"file": {"type": "string"}, "tag": {"type": "string"}}}, mutation_order_probe)
    ext.tool("start_order", "Report the order in which calls start", {"type": "object", "properties": {"n": {"type": "number"}}}, start_order)

    def dialog_order(ctx, params):
        if params.get("exec"):
            try:
                ctx.exec("dialog-order-exec", [])
            except RuntimeError:
                pass
        selected, _ = ctx.select(str(params.get("title")), ["a", "b"])
        return {"content": f"dialog:{params.get('title')}={selected}"}

    ext.tool("dialog_order", "Open a select dialog titled by the call and report the answer", {"type": "object", "properties": {"title": {"type": "string"}, "exec": {"type": "boolean"}}}, dialog_order)
    ext.tool("tool_error", "Return a thrown tool error", {"type": "object"}, tool_error)
    ext.tool("tool_is_error", "Return a structured tool error result", {"type": "object"}, tool_is_error)
    ext.tool(
        "guided_tool", "Tool with prompt guidelines", {"type": "object"},
        lambda ctx, params: {"content": "guided"},
        prompt_snippet=" \ufeffGuided\r\n tool\t summary ",
        prompt_guidelines=["Use guided_tool when the user asks for guided behavior."],
    )
    ext.tool(
        "sourced_tool", "Tool with explicit source", {"type": "object"},
        lambda ctx, params: {"content": "sourced"},
        prompt_guidelines=["Use sourced_tool to test per-tool source attribution."],
        source="mcp:test-server",
    )
    ext.tool("sampling_disabled", "Disable constrained sampling", {"type":"object"}, lambda ctx, params: "disabled", constrained_sampling=False)
    ext.tool(
        "grammar_tool", "Tool with a grammar constrained sampling request", {"type": "object"},
        lambda ctx, params: {"content": "grammar"},
        constrained_sampling={"type": "grammar", "variants": {"openai_lark": "start: NUMBER"}},
    )

    def complete_probe(prefix: str) -> list[dict[str, str]] | None:
        items = [{"value": "alpha", "label": "alpha — first"}, {"value": "apple", "description": "fruit"}, {"value": "beta"}]
        matched = [item for item in items if item["value"].startswith(prefix.strip())]
        return matched or None

    ext.command("complete_probe", "Complete its arguments", lambda ctx, args: None, get_argument_completions=complete_probe)
    ext.command("ping", "Respond with pong", lambda ctx, args: ctx.notify("pong", "info"))

    def model_stream_probe(ctx: pig_sdk.Context, _args: str) -> None:
        current = ctx.model_registry.find("conformance", "current")
        if current is None or current.get("id") != "current":
            raise RuntimeError(f"find current = {current}")
        found = ctx.model_registry.find("conformance", "declared")
        if found is None or found.get("id") != "declared" or found.get("provider") != "conformance":
            raise RuntimeError(f"find declared = {found}")
        slash = ctx.model_registry.find("conformance", "org/model/name")
        if slash is None or slash.get("id") != "org/model/name":
            raise RuntimeError(f"find slash = {slash}")
        expected_limits = {"maxRequestBytes":12345,"images":{"maxPerMessage":7,"maxPerRequest":11,"resize":{"maxWidth":321,"maxHeight":123,"maxBytes":45678,"jpegQuality":67}}}
        if slash.get("inputLimits") != expected_limits or ctx.get_model_info().get("inputLimits") != expected_limits:
            raise RuntimeError(f"model inputLimits = {slash}")
        required = {"baseUrl", "input", "cost", "thinkingLevelMap", "promptCache", "contextWindow", "maxTokens", "samplingParams", "headers", "compat"}
        if not required.issubset(slash):
            raise RuntimeError(f"find slash missing {required - set(slash)}: {slash}")
        if slash["input"] != [] or slash["cost"]["input"] != 0 or len(slash["cost"]["tiers"]) != 1 or slash["compat"]["supportsStrictMode"] is not False:
            raise RuntimeError(f"find slash shape = {slash}")
        if ctx.model_registry.find("conformance", "missing") is not None:
            raise RuntimeError("find missing returned a model")
        if ctx.model_registry.find("conformance", "override-only") is not None:
            raise RuntimeError("find override-only returned a model")
        auth = ctx.model_registry.get_api_key_and_headers(found)
        expected_auth = {"ok": True, "apiKey": "conformance-key", "headers": {"X-Conformance-Auth": "yes"}, "baseUrl": "https://models.invalid/v1", "env": {"CONFORMANCE_AUTH": "yes"}}
        if auth != expected_auth:
            raise RuntimeError(f"auth = {auth}")
        model = {"provider": "conformance", "modelId": "declared", "api": "openai-responses"}
        request = {
            "systemPrompt": "conformance-system",
            "messages": [
                {"role": "system", "content": [{"type": "text", "text": "signed system", "textSignature": "system-signature"}], "sections": {"zeta": "last-first", "alpha": None, "middle": "middle"}, "timestamp": 41},
                {"role": "user", "content": "hello", "timestamp": 42},
                {"role": "assistant", "content": [{"type": "text", "text": "prior", "textSignature": "signed"}], "api": "openai-responses", "provider": "prior-provider", "model": "prior-model", "usage": {"input": 1, "output": 2, "cacheRead": 3, "cacheWrite": 4, "totalTokens": 10, "cost": {"input": 0.1, "output": 0.2, "cacheRead": 0.3, "cacheWrite": 0.4, "total": 1.0}}, "stopReason": "stop", "timestamp": 43},
            ],
            "tools": [{"name": "lookup", "description": "lookup", "parameters": {"type": "object"}, "constrainedSampling": {"type": "grammar", "variants": {"openai_lark": "start: NUMBER"}}}],
        }
        options = {
            "timeoutMs":0,"websocketConnectTimeoutMs":1234,"maxRetries":2,"maxRetryDelayMs":3000,
            "maxTokens": 321, "temperature": 0.65, "samplingParams": {"topP": 0.8},
            "thinkingBudgets": {"minimal": 11, "low": 22, "medium": 33, "high": 44}, "reasoning": "high", "isReasoning": True,
            "env": {"WIRE_ENV": "request-value", "SECOND_ENV": "distinct-value"}, "headers": {"X-Wire": "yes", "X-Remove": None}, "sessionId": "conformance-session", "transport": "sse",
        }
        stream = ctx.model_registry.stream(model, request, options)
        types = [event["type"] for event in stream.events()]
        if types != ["start", "text_start", "text_delta", "text_end", "done"]:
            raise RuntimeError(f"stream events = {types}")
        result = stream.result()
        if result["content"][0]["text"] != "streamed":
            raise RuntimeError(f"stream result = {result}")
        simple = ctx.model_registry.stream_simple(model, request, options)
        simple_types = [event["type"] for event in simple.events()]
        if simple_types != ["start", "text_start", "text_delta", "text_end", "done"]:
            raise RuntimeError(f"simple events = {simple_types}")
        if simple.result()["stopReason"] != "stop":
            raise RuntimeError("simple did not stop")
        if ctx.model_registry.complete(model, request, options)["stopReason"] != "stop":
            raise RuntimeError("complete did not stop")
        if ctx.model_registry.stream(model, request, options).result()["stopReason"] != "stop":
            raise RuntimeError("result without iteration did not stop")
        unknown = ctx.model_registry.complete({"provider": "conformance", "modelId": "unknown", "api": "openai-responses"}, request, options)
        if unknown["stopReason"] != "error" or "unknown model" not in unknown["errorMessage"]:
            raise RuntimeError(f"unknown result = {unknown}")
        transport_error = ctx.model_registry.complete({"provider": "conformance", "modelId": "protocol-error", "api": "openai-responses"}, request, options)
        timestamp = transport_error.pop("timestamp", None)
        if not isinstance(timestamp, int) or timestamp <= 0:
            raise RuntimeError(f"transport timestamp = {timestamp}")
        expected_transport = {
            "role": "assistant", "content": [], "api": "openai-responses", "provider": "conformance", "model": "protocol-error",
            "usage": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0,
                      "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}},
            "stopReason": "error", "errorMessage": "transport boom",
        }
        if transport_error != expected_transport:
            raise RuntimeError(f"transport result = {transport_error}")
        ctx.notify("model-stream=ok", "info")

    ext.command("model-stream-probe", "Exercise model streaming", model_stream_probe)

    def model_stream_callback_probe(ctx: pig_sdk.Context, _args: str) -> None:
        model = {"provider": "conformance", "id": "declared", "modelId": "declared", "api": "openai-responses"}
        request = {"systemPrompt": "callbacks", "messages": [{"role": "user", "content": "hello", "timestamp": 1}]}
        seen: dict[str, Any] = {}

        def on_payload(payload: Any, callback_model: dict[str, Any]) -> Any:
            seen["payload"] = payload
            seen["model"] = callback_model.get("id")
            return {**payload, "mark": "on-payload"}

        def on_response(response: dict[str, Any], _callback_model: dict[str, Any]) -> None:
            seen["response"] = response

        def transform_headers(headers: dict[str, Any], _callback_model: dict[str, Any]) -> dict[str, Any]:
            return {**headers, "x-transformed": "yes"}

        stream = ctx.model_registry.stream(model, request, {"onPayload": on_payload, "onResponse": on_response, "transformHeaders": transform_headers})
        if stream.result()["stopReason"] != "stop":
            raise RuntimeError("callback stream did not stop")
        if (seen.get("payload") or {}).get("original") is not True or seen.get("model") != "declared":
            raise RuntimeError(f"onPayload saw {seen}")
        response = seen.get("response") or {}
        if response.get("status") != 201 or (response.get("headers") or {}).get("x-upstream") != "seen":
            raise RuntimeError(f"onResponse saw {response}")
        if ctx.model_registry.stream(model, request, {}).result()["stopReason"] != "stop":
            raise RuntimeError("plain stream did not stop")
        ctx.notify("model-callbacks=ok", "info")

    class HandleProbeComponent:
        def render(self, width: int) -> list[str]:
            return ["overlay"]

        def handle_input(self, data: str) -> pig_sdk.RemoteComponentResult:
            return pig_sdk.RemoteComponentResult(done=data == "q", value="closed" if data == "q" else None)

    def overlay_handle_probe(ctx: pig_sdk.Context, _args: str) -> None:
        failures: list[str] = []

        def expect(label: str, actual: Any, expected: Any) -> None:
            if actual != expected:
                failures.append(f"{label}: {actual!r} != {expected!r}")

        def on_handle(handle: pig_sdk.OverlayHandle) -> None:
            expect("initial focused", handle.is_focused(), True)
            expect("initial hidden", handle.is_hidden(), False)
            expect("initial bounds", handle.get_bounds(), {"row": 3, "col": 4, "width": 20, "height": 5})
            handle.focus()
            expect("focus", handle.is_focused(), True)
            handle.set_hidden(True)
            expect("hidden", [handle.is_hidden(), handle.is_focused(), handle.get_bounds()], [True, False, None])
            handle.set_hidden(False)
            expect("shown", [handle.is_hidden(), handle.is_focused()], [False, False])
            handle.focus()
            expect("refocus", handle.is_focused(), True)
            handle.unfocus()
            expect("unfocus", handle.is_focused(), False)
            handle.unfocus(None)

        ctx.custom(HandleProbeComponent(), {"overlay": True, "onHandle": on_handle})
        if failures:
            raise RuntimeError("; ".join(failures))
        ctx.notify("overlay-handle=ok", "info")

    ext.command("overlay-handle-probe", "Exercise the overlay handle ui.custom hands to on_handle", overlay_handle_probe)

    class FocusProbeComponent:
        def __init__(self) -> None:
            self.handle: pig_sdk.OverlayHandle | None = None

        def render(self, width: int) -> list[str]:
            return [f"focused={'pending' if self.handle is None else str(self.handle.is_focused()).lower()}"]

        def handle_input(self, data: str) -> pig_sdk.RemoteComponentResult:
            return pig_sdk.RemoteComponentResult(done=data == "q", value="closed" if data == "q" else None)

    def overlay_focus_probe(ctx: pig_sdk.Context, _args: str) -> None:
        component = FocusProbeComponent()

        def on_handle(handle: pig_sdk.OverlayHandle) -> None:
            component.handle = handle

        ctx.custom(component, {"overlay": True, "onHandle": on_handle})
        ctx.notify("overlay-focus=ok", "info")

    ext.command("overlay-focus-probe", "Render the focus the host publishes to a mounted overlay", overlay_focus_probe)

    def model_stream_fetch_probe(ctx: pig_sdk.Context, _args: str) -> None:
        model = {"provider": "conformance", "id": "declared", "modelId": "declared", "api": "openai-responses"}
        request = {"systemPrompt": "fetch", "messages": [{"role": "user", "content": "hello", "timestamp": 1}]}
        seen: dict[str, Any] = {}

        def fetch(call: dict[str, Any]) -> dict[str, Any]:
            seen["url"] = call["url"]
            seen["method"] = call["method"]
            seen["host"] = (call["headers"].get("X-Host") or [None])[0]
            seen["body"] = call["body"].decode()
            return {"status": 207, "statusText": "Answered", "headers": {"x-sdk-fetch": "answered"}, "body": bytes(index % 251 for index in range(70000))}

        stream = ctx.model_registry.stream(model, request, {"fetch": fetch})
        if stream.result()["stopReason"] != "stop":
            raise RuntimeError("fetch stream did not stop")
        if seen != {"url": "https://fetch.invalid/v1/chat?x=1", "method": "POST", "host": "1", "body": "ping-body"}:
            raise RuntimeError(f"fetch saw {seen}")
        if ctx.model_registry.stream(model, request, {}).result()["stopReason"] != "stop":
            raise RuntimeError("plain stream did not stop")
        ctx.notify("model-fetch=ok", "info")

    ext.command("model-stream-fetch-probe", "Exercise the fetch option of model_registry.stream", model_stream_fetch_probe)

    ext.command("model-stream-callback-probe", "Exercise the provider request callbacks of model_registry.stream", model_stream_callback_probe)

    def command_error(ctx: pig_sdk.Context, args: str) -> None:
        raise RuntimeError("command exploded")

    ext.command("liveness_host_call", "Exercise an awaited host call", lambda ctx, args: ctx.wait_for_idle())
    ext.command("liveness_user_call", "Exercise an interactive host call", lambda ctx, args: ctx.input("Question", "Answer"))
    ext.command("liveness_fire_call", "Exercise a no-result UI host call", lambda ctx, args: ctx.set_title("Conformance title"))

    ext.command("command_error", "Return a command error", command_error)

    def command_awaited_error(ctx: pig_sdk.Context, args: str) -> None:
        time.sleep(0.15)
        raise RuntimeError("awaited command exploded")

    ext.command("command_awaited_error", "Return an error after awaited work", command_awaited_error)
    ext.command("status", "Set a status entry", lambda ctx, args: ctx.set_status("conformance", "ok"))

    def status_burst(ctx, args):
        for i in range(200):
            ctx.set_status("burst", str(i))

    ext.command("status_burst", "Set one status repeatedly without awaiting", status_burst)
    event_probe_off: list = []

    def event_probe_subscribe(ctx, args):
        event_probe_off.append(
            ext.on_event("turn_end", lambda hctx, data: hctx.notify("event_probe:%s" % data.get("messageEntryId", ""), "info"))
        )

    def event_probe_unsubscribe(ctx, args):
        while event_probe_off:
            event_probe_off.pop()()

    ext.command("event_probe_subscribe", "Subscribe to turn_end after connecting", event_probe_subscribe)
    ext.command("event_probe_unsubscribe", "Remove the turn_end handler registered after connecting", event_probe_unsubscribe)
    ext.command(
        "report_geometry",
        "Report observed terminal geometry",
        lambda ctx, args: ctx.notify(f"geometry:{ctx.width}x{ctx.height}", "info"),
    )

    width_probe: list = []

    def arm_width_probe(ctx, args):
        # A width handler makes a host call the way any other handler does; the host's reply must reach it (conformance TestConformance_WidthHandlerHostCall).
        if not width_probe:
            def on_width(width):
                ctx.notify(f"width-probe:{width}", "info")
                ctx.notify(f"width-probe-returned:{width}", "info")

            width_probe.append(ctx.on_width_change(on_width))

    ext.command("arm_width_probe", "Notify from a width handler", arm_width_probe)

    ext.command("surface_footer", "Install a footer renderer", lambda ctx, args: ctx.set_footer_renderer(lambda width: [f"footer@{width}"]))
    ext.command("surface_header", "Install a header renderer", lambda ctx, args: ctx.set_header_renderer(lambda width: [f"header@{width}"]))
    ext.command("surface_static_footer", "Push static footer rows", lambda ctx, args: ctx.set_footer([f"static@{ctx.width}"]))
    ext.command("surface_static_header", "Push static header rows", lambda ctx, args: ctx.set_header([f"static@{ctx.width}"]))
    ext.command("surface_widget", "Set a string list widget wider than the pane", lambda ctx, args: ctx.set_widget("wide", ["A" * 60 + " tail", "short"]))

    term_unsub: list = []

    def term_verdict(ctx: pig_sdk.Context, data: str) -> pig_sdk.TerminalInputResult:
        if data in ("\ud83d", "\ude00", "😀"):
            return pig_sdk.TerminalInputResult(data="seen:" + data)
        if data == "\x1b[96~":
            return pig_sdk.TerminalInputResult(data=json.dumps([ctx.get_editor_text(), ctx.get_tools_expanded()], separators=(",", ":")))
        if data == "\x1b[98~":
            return pig_sdk.TerminalInputResult(data="rewritten")
        if data == "\x1b[97~":
            time.sleep(0.2)
            return pig_sdk.TerminalInputResult(consume=True)
        return pig_sdk.TerminalInputResult(consume=data == "\x1b[99~")

    def term_subscribe(ctx: pig_sdk.Context, args: str) -> None:
        term_unsub.append(ctx.on_terminal_input(lambda data: term_verdict(ctx, data)))

    def term_unsubscribe(ctx: pig_sdk.Context, args: str) -> None:
        while term_unsub:
            term_unsub.pop()()

    ext.command("term_subscribe", "Subscribe to raw terminal input", term_subscribe)
    ext.command("term_unsubscribe", "Release the raw input subscription", term_unsubscribe)
    ext.command("send_message", "Send a custom message", lambda ctx, args: ctx.send_message("notice", "hello-custom", True, True, "steer"))
    ext.command("send_message_default", "Send a custom message with default options", lambda ctx, args: ctx.send_message("notice", "default"))
    ext.command("send_message_no_turn", "Send a custom message that never starts a turn", lambda ctx, args: ctx.send_message("notice", "no-turn", trigger_turn=False))
    ext.command("send_user_message", "Send a user message", lambda ctx, args: ctx.send_user_message(json.loads(args) if args else "hello-user", "followUp"))
    ext.command("settings_probe", "Report the effective settings", lambda ctx, args: ctx.notify("settings_probe:" + json.dumps(ctx.get_settings(), separators=(",", ":")), "info"))

    def model_set(ctx, args):
        ok, _error = ctx.set_model(args.strip())
        ctx.notify("model_set:%s" % str(ok).lower(), "info")

    ext.command("model_set", "Switch the model", model_set)
    ext.command("thinking_set", "Set the thinking level", lambda ctx, args: ctx.set_thinking_level(args.strip()))
    ext.command("thinking_get", "Report the thinking level", lambda ctx, args: ctx.notify("thinking_get:%s" % ctx.get_thinking_level(), "info"))
    ext.command("set_session_name", "Set the session name", lambda ctx, args: ctx.set_session_name("conformance-session"))

    def host_state_probe(ctx: pig_sdk.Context, _args: str) -> None:
        commands = ["|".join([c["name"], c.get("description", ""), c["source"], c["sourceInfo"]["path"], c["sourceInfo"]["scope"]]) for c in ctx.get_commands()]
        ctx.notify(f"getThinkingLevel={ctx.get_thinking_level()};getCommands={','.join(commands)}", "info")

    ext.command("host_state_probe", "Report the thinking level and the session commands", host_state_probe)

    def rw_prompt_state(ctx: pig_sdk.Context, _args: str) -> None:
        def tool(name: str) -> pig_sdk.ToolDefinition:
            return pig_sdk.ToolDefinition(name=name, label=name, description=name, prompt_snippet=name + "-snippet", parameters={"type": "object"}, execute=lambda c, a: "ok")

        ctx.register_tool(tool("rw-alpha"))
        ctx.register_tool(tool("rw-beta"))
        registered = ctx.get_system_prompt_options()
        ctx.set_active_tools(["rw-beta"])
        options = ctx.get_system_prompt_options()
        prompt = ctx.get_system_prompt()
        ctx.notify("rw_prompt_state=registered:%s;selected:%s;beta:%s;alpha:%s" % ((registered.get("toolSnippets") or {}).get("rw-alpha", ""), ",".join(options.get("selectedTools") or []), str("rw-beta-snippet" in prompt).lower(), str("rw-alpha-snippet" in prompt).lower()), "info")

    ext.command("rw_prompt_state", "Read the system prompt state after registering and activating tools", rw_prompt_state)

    def rw_idle_after_trigger(ctx: pig_sdk.Context, _args: str) -> None:
        ctx.send_message("rw-trigger", "rw-trigger", True, True)
        ctx.notify("rw_idle_after_trigger=%s" % str(ctx.is_idle()).lower(), "info")

    ext.command("rw_idle_after_trigger", "Read isIdle after a message that starts a run", rw_idle_after_trigger)
    rw_settled_once = []

    def rw_idle_in_settled(ctx: pig_sdk.Context, _args: str) -> None:
        def settled(hctx, _data):
            if rw_settled_once:
                return
            rw_settled_once.append(True)
            hctx.send_message("rw-trigger", "rw-trigger", True, True)
            hctx.notify("rw_idle_in_settled=%s" % str(hctx.is_idle()).lower(), "info")

        ext.on_event("agent_settled", settled)

    ext.command("rw_idle_in_settled", "Read isIdle after a message that starts a run, sent from an agent_settled handler", rw_idle_in_settled)

    def set_model(ctx: pig_sdk.Context, args: str) -> None:
        ok, error = ctx.set_model(args)
        if error:
            raise RuntimeError(error)
        ctx.notify(f"setModel={'true' if ok else 'false'}", "info")

    ext.command("set_model", "Switch to the model in the arguments", set_model)
    ext.command("append_entry", "Append a custom entry", lambda ctx, args: ctx.append_entry("conformance-entry", "hello-entry"))

    def append_entries_probe(ctx: pig_sdk.Context, args: str) -> None:
        # Pi's appendEntry returns nothing and never fails, so a refusal the SDK reports is dropped here; the row reads the session afterwards.
        for custom_type in args.split(","):
            try:
                ctx.append_entry(custom_type, f"data-of-{custom_type}")
            except pig_sdk.HostCallError:
                pass

    ext.command("append-entries-probe", "Append a custom entry for each comma-separated type", append_entries_probe)

    def event_field_probe(ctx, args):
        name, field = args.split()
        ext.on_event(name, lambda hctx, data: hctx.notify("event_field:%s.%s=%s" % (name, field, json.dumps(data[field], separators=(",", ":")) if field in data else "absent"), "info"))

    ext.command("event_field_probe", "Report a field of the events named by the argument `<event> <field>`", event_field_probe)
    event_probe_on_offs: dict = {}

    event_probe_results: dict = {}

    def event_probe_result(ctx, args):
        name, _, raw = args.strip().partition(" ")
        event_probe_results.setdefault(name, []).append(json.loads(raw))

    def event_probe_on(ctx, args):
        name = args.strip()

        def probe(hctx, data):
            hctx.notify("event_probe_on:%s" % name, "info")
            hctx.notify("event_payload:%s:%s" % (name, json.dumps(data)), "info")
            # The event as the host sent it, for the tests that compare a payload field with the Go reference's.
            hctx.notify("event_probe_data:%s:%s" % (name, json.dumps(data, separators=(",", ":"))), "info")
            # mcp_servers_change carries every registered server (types.ts:699-709): report their names, which only the host's registry knows.
            if isinstance(data, dict) and isinstance(data.get("servers"), list):
                hctx.notify("event_probe_servers:%s" % ",".join(str(s.get("name")) for s in data["servers"]), "info")
            queued = event_probe_results.get(name)
            return queued.pop(0) if queued else None

        off = ext.on_event(name, probe)
        event_probe_on_offs.setdefault(name, []).append(off)

    def event_probe_on_off(ctx, args):
        for off in event_probe_on_offs.pop(args.strip(), []):
            off()

    ext.command("event_probe_result", "Queue the JSON result the next event_probe_on handler call of an event returns", event_probe_result)
    ext.command("event_probe_on", "Subscribe to the event named by the argument", event_probe_on)

    def event_result_probe(ctx, args):
        # Subscribe to the event named first with a handler that returns the JSON that follows: the result an SDK extension gives the host.
        name, _, raw = args.strip().partition(" ")
        result = json.loads(raw)
        ext.on_event(name, lambda hctx, data: result)

    ext.command("event_result_probe", "Subscribe to an event with a handler that returns the given JSON", event_result_probe)
    ext.command("event_probe_off", "Unsubscribe the event_probe_on handlers of the event named by the argument", event_probe_on_off)
    def event_payload_probe(ctx, args):
        name = args.strip()
        ext.on_event(name, lambda hctx, data: hctx.notify("event_payload:%s:%s" % (name, json.dumps(data)), "info"))

    ext.command("event_payload_probe", "Subscribe and report the payload of the event named by the argument", event_payload_probe)
    def event_isolation_probe(ctx, args):
        # Two handlers: the first reports the nested marker it received, writes a sentinel into it and changes it again from a timer thread after returning; the
        # second reports what it received (TestConformance_EventHandlersDoNotShareTheirPayload).
        name = args.strip()

        def source(data):
            entries = (data.get("context") or {}).get("contextEntries") or []
            return entries[0]["sourceEntry"] if entries else None

        def first(hctx, data):
            container = source(data)
            hctx.notify("event_isolation:A:%s:end" % container["id"], "info")
            container["id"] = "SENTINEL-HANDLER-A"
            threading.Timer(0.1, lambda: container.__setitem__("id", "SENTINEL-LATE-WRITER")).start()

        ext.on_event(name, first)
        ext.on_event(name, lambda hctx, data: hctx.notify("event_isolation:B:%s:end" % source(data)["id"], "info"))

    ext.command("event_isolation_probe", "Subscribe two handlers that report the nested marker they received and then write sentinels into it", event_isolation_probe)

    def tool_result_guards_probe(ctx, args):
        guards = {"bash": pig_sdk.is_bash_tool_result, "powershell": pig_sdk.is_powershell_tool_result, "read": pig_sdk.is_read_tool_result, "edit": pig_sdk.is_edit_tool_result, "write": pig_sdk.is_write_tool_result, "grep": pig_sdk.is_grep_tool_result, "find": pig_sdk.is_find_tool_result, "ls": pig_sdk.is_ls_tool_result}
        ext.on_event("tool_result", lambda hctx, data: hctx.notify("tool_result_guards:%s:end" % ",".join(sorted(name for name, guard in guards.items() if guard(data))), "info"))

    ext.command("tool_result_guards_probe", "Report the tool_result guards that accept each tool_result event", tool_result_guards_probe)
    ext.command("set_label", "Set an entry label", lambda ctx, args: ctx.set_label("label-entry", "conformance-label"))
    # label_probe labels the entry named first with the rest of the arguments: an entry the host's Session holds.
    def label_probe(ctx, args):
        parts = args.strip().split(" ", 1)
        ctx.set_label(parts[0], parts[1] if len(parts) > 1 else "")

    ext.command("label_probe", "Label the entry named first", label_probe)
    ext.shortcut("ctrl+alt+y", "Conformance shortcut", lambda ctx: None)

    def ui_availability(ctx: pig_sdk.Context, args: str) -> None:
        selected, _ = ctx.select("Pick", ["first", "second"])
        selected = dialog_text(selected)
        ctx.notify("availability-notify", "info")
        if not ctx.has_ui():
            assert ctx.input("Input", "placeholder") == (None, False)
            assert ctx.editor("Editor", "prefill") == (None, False)
            assert ctx.confirm("Confirm", "message") is False
            class NoUIComponent:
                def render(self, width):
                    raise AssertionError("headless render")
                def handle_input(self, data):
                    raise AssertionError("headless input")
                def set_invalidate(self, callback):
                    raise AssertionError("headless invalidation")
                def dispose(self):
                    raise AssertionError("headless disposal")
            assert ctx.custom(NoUIComponent()) is None
            off = ctx.on_terminal_input(lambda data: None)
            off(); off()
            ctx.add_autocomplete_provider()
            ctx.set_editor_text("ignored")
            ctx.set_tools_expanded(True)
            assert ctx.get_editor_text() == ""
            assert ctx.get_tools_expanded() is False
            assert ctx.get_all_themes() == []
            assert ctx.get_theme("dark") is None
            assert ctx.set_theme("dark") == (False, "UI not available")
        ctx.append_entry("ui-availability", f"hasUI={str(ctx.has_ui()).lower()} selected={selected}")

    ext.command("ui-availability", "Probe bound UI and headless defaults", ui_availability)

    def ui_state_barrier(ctx, args):
        selected, _ = ctx.select("expand", ["chosen"])
        selected = dialog_text(selected)
        named = ctx.get_theme("light")
        missing = ctx.get_theme("missing")
        success, message = ctx.set_theme("missing")
        ctx.notify(f"selected={selected} expanded={str(ctx.get_tools_expanded()).lower()} named={named['name']} missing={str(missing is None).lower()} success={str(success).lower()} error={message}", "info")

    ext.command("ui-state-barrier", "Read UI state after a dialog", ui_state_barrier)
    def autocomplete_register(ctx, args):
        for tag in ["A", "B"]:
            def factory(ctx, current, tag=tag):
                ctx.notify("factory:" + tag, "info")
                class Wrapped:
                    trigger_characters = ["$"] if tag == "A" else ["#", "$"]
                    calls = 0
                    def get_suggestions(self, ctx, lines, line, col, force=False):
                        self.calls += 1
                        result = current.get_suggestions(ctx, lines, line, col, force)
                        if result is None:
                            return None
                        items = ([dict(item, label=f"{item['value']}:{self.calls}") for item in result["items"] if item["value"] != "drop"] if tag == "A" else result["items"] + [{"value":"tail", "label":f"tail:{self.calls}"}])
                        return dict(result, items=items)
                    def apply_completion(self, ctx, lines, line, col, item, prefix):
                        result = current.apply_completion(ctx, lines, line, col, item, prefix)
                        result["lines"][result["cursorLine"]] += "-" + tag
                        result["cursorCol"] += 2
                        return result
                    def should_trigger_file_completion(self, ctx, lines, line, col):
                        return current.should_trigger_file_completion(ctx, lines, line, col)
                return Wrapped()
            ctx.add_autocomplete_provider(factory)
            ctx.notify("registered:" + tag, "info")

    ext.command("autocomplete-register", "Register retained provider wrappers", autocomplete_register)
    def signal_probe(ctx: pig_sdk.Context, args: str) -> None:
        signal = ctx.signal
        ctx.notify("signal:" + ("none" if signal is None else "aborted" if signal.is_set() else "live"), "info")

    def signal_wait(ctx: pig_sdk.Context, args: str) -> None:
        signal = ctx.signal
        if signal is None:
            ctx.notify("wait:none", "info")
            return
        ctx.notify("wait:start", "info")
        ctx.notify("wait:" + ("aborted" if signal.wait(10) else "timeout"), "info")

    def signal_poll(ctx: pig_sdk.Context, args: str) -> None:
        ctx.notify("poll:start", "info")
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            if (ctx.signal is not None) == (args == "live"):
                ctx.notify("poll:" + args, "info")
                return
            time.sleep(0.005)
        ctx.notify("poll:timeout", "info")

    ext.command("signal-poll", "Poll ctx.signal until it is live or none", signal_poll)
    ext.command("signal-probe", "Report ctx.signal", signal_probe)
    # Upstream ctx.cwd, ctx.mode, ctx.hasUI and ctx.model throw the stale message after invalidation (runner.ts:571-600).
    # The members are read before the report, and a failed report is ignored, so only the local members can fail the command: the Host also rejects the stale ui.notify call with the same message.
    def stale_probe(ctx: pig_sdk.Context, args: str) -> None:
        report = "stale:%s|%s|%s|%s" % (ctx.cwd, ctx.mode, str(ctx.has_ui()).lower(), ctx.model)
        try:
            ctx.notify(report, "info")
        except Exception:
            pass

    ext.command("stale-probe", "Report cwd, mode, hasUI and model", stale_probe)
    ext.command("signal-wait", "Wait for ctx.signal to abort", signal_wait)
    ext.command("usage-probe", "Report context usage", lambda ctx, args: ctx.notify(json.dumps(ctx.get_context_usage()), "info"))

    def context_probe(ctx: pig_sdk.Context, args: str) -> None:
        opts = ctx.get_system_prompt_options()

        def shape(value: object) -> str:
            if value is None:
                return "absent"
            if isinstance(value, list):
                return "array:%d" % len(value)
            if isinstance(value, dict):
                return "object:%d" % len(value)
            return "%s:%d" % ("string" if isinstance(value, str) else type(value).__name__, len(str(value)))

        shapes = ",".join(
            "%s:%s" % (key, shape(opts.get(key)))
            for key in ("selectedTools", "toolSnippets", "toolGuidelines", "promptGuidelines", "appendSystemPrompt", "sections", "contextFiles", "skills")
        )
        ctx.notify(
            "mode=%s trusted=%s spo_prompt=%s spo_cwd=%s spo_tools=%s spo_shape=%s spo_guidelines=%s spo_skill_scope=%s spo_force_empty=%s spo_custom_present=%s"
            % (
                ctx.mode,
                "true" if ctx.is_project_trusted() else "false",
                opts.get("customPrompt", ""),
                opts.get("cwd", ""),
                ",".join(opts.get("selectedTools") or []),
                shapes,
                ",".join(opts.get("toolGuidelines", {}).get("read", [])),
                (opts.get("skills") or [{}])[0].get("sourceInfo", {}).get("scope", ""),
                str(opts.get("forceSystemPrompt") == "").lower(),
                str("customPrompt" in opts).lower(),
            ),
            "info",
        )

    def login_probe(ctx: pig_sdk.Context, args: str) -> None:
        definition = conformance_login_definition()
        ctx.set_login(definition)
        definition.brand[0] = definition.brand[0][:-1]
        try:
            ctx.set_login(definition)
        except pig_sdk.HostCallError as err:
            ctx.notify(str(err), "error")
            return
        raise RuntimeError("invalid login definition was accepted")

    ext.command("login-probe", "Exercise semantic login submission and host errors", login_probe)

    def sprite_probe(ctx: pig_sdk.Context, args: str) -> None:
        definition = conformance_sprite_definition()
        ctx.register_sprite(definition)
        definition.mascot[0] = definition.mascot[0][:-1]
        try:
            ctx.register_sprite(definition)
        except pig_sdk.HostCallError as err:
            ctx.notify(str(err), "error")
            return
        raise RuntimeError("invalid sprite definition was accepted")

    ext.command("sprite-probe", "Exercise sprite registration and host errors", sprite_probe)
    ext.command("scoped-models-probe", "Report the model scope", lambda ctx, _args: ctx.notify(json.dumps(ctx.scoped_models()), "info"))
    ext.command("context-probe", "Report ctx.mode + ctx.getSystemPromptOptions()", context_probe)

    def dialog_probe(ctx: pig_sdk.Context, args: str) -> None:
        selected = dialog_text(ctx.select("Pick", ["first", "second"])[0])
        input_value = dialog_text(ctx.input("Input", "placeholder")[0])
        edited = dialog_text(ctx.editor("Editor", "prefill")[0])
        confirmed = ctx.confirm("Confirm", "message") is True
        ctx.notify(
            "select=%s input=%s editor=%s confirm=%s"
            % (selected, input_value, edited, str(confirmed).lower()),
            "info",
        )

    ext.command(
        "session-log-probe",
        "Read a paged session log",
        lambda ctx, args: ctx.notify(
            f"session entries={len(ctx.get_entries())} branch={len(ctx.get_branch())}",
            "info",
        ),
    )

    def session_branch_probe(ctx: pig_sdk.Context, args: str) -> None:
        manager = ctx.session_manager
        leaf, entry, label = manager.get_leaf_entry(), manager.get_entry(args), manager.get_label(args)
        ids = ">".join(item["id"] for item in ctx.get_branch())
        ctx.notify(
            f"branch={ids} leaf={leaf['id'] if leaf else 'none'} entry={entry['id'] if entry else 'none'} label={label if label is not None else 'none'}",
            "info",
        )

    ext.command("session-branch-probe", "Read the branch, an entry and its label", session_branch_probe)
    ext.command("dialog-probe", "Exercise interactive dialog responses", dialog_probe)

    def focused_probe(ctx: pig_sdk.Context, args: str) -> None:
        component = FocusedList()
        selected = ctx.custom(
            component,
            {"title": "Focused", "widthFraction": 0.5, "heightFraction": 0.5},
        )
        ctx.notify(f"focused={selected or ''} disposed={str(component.disposed).lower()}", "info")

    ext.command("focused-probe", "Exercise focused subprocess UI", focused_probe)

    def kit_probe(ctx: pig_sdk.Context, _args: str) -> None:
        result = ctx.custom(KitProbe(), {"title": "Kit"})
        ctx.notify(f"kit={'' if result is None else result}", "info")

    ext.command("kit-probe", "Exercise the component kit (D107)", kit_probe)

    def kit_widget(ctx: pig_sdk.Context, key: str) -> None:
        build = KIT_WIDGET_CASES.get(key)
        if build is None:
            raise RuntimeError(f"kit-widget: unknown case {key!r}")
        ctx.custom(KitWidgetProbe(ctx, build()), {"title": "Kit"})

    ext.command("kit-widget", "Show a widget kind's Pi vector case (D107)", kit_widget)

    # mouse-probe opens the mouse row's component: the host hands it
    # fullscreen mouse events, and it closes on a click with what it got.
    def mouse_probe(ctx: pig_sdk.Context, _args: str) -> None:
        result = ctx.custom(MouseProbe(), {"overlay": True})
        ctx.notify(f"mouse={'' if result is None else result}", "info")

    ext.command("mouse-probe", "Exercise extension mouse input", mouse_probe)

    # kit-surfaces shows the kit probe's tree on every other view surface
    # (D107, spec §10): a pushed widget, the header, the footer and a tool
    # result. The message renderer "kit-message" draws it too.
    def kit_surfaces(ctx: pig_sdk.Context, _args: str) -> None:
        ctx.set_widget("kit-probe", kit_probe_view())
        ctx.set_header_view(kit_probe_view())
        ctx.set_footer_view(kit_probe_view())
        ctx.register_tool(pig_sdk.ToolDefinition(
            name="kit_view_tool",
            label="kit_view_tool",
            description="Render its result as the kit probe",
            parameters={"type": "object"},
            execute=lambda _ctx, _params: "kit",
            result_view=lambda _ctx, _result, _options, _render, _width: kit_probe_view(),
        ))

    ext.command("kit-surfaces", "Show the kit probe on every view surface (D107)", kit_surfaces)

    # kit-images drives the image transport (D107, spec §7): step "a<k>"
    # shows image 0 and step "b<n>" image n (1 ≤ n ≤ 64), each with its step
    # as the text, as one ui.setWidget call on the "kit-img" widget.
    def kit_images(ctx: pig_sdk.Context, args: str) -> None:
        n = int(args[1:]) if args[1:].isdecimal() else 0
        if args[:1] == "a" and n >= 1:
            image = 0
        elif args[:1] == "b" and 1 <= n <= 64:
            image = n
        else:
            raise ValueError(f"kit-images: unknown step {args!r}")
        view = kit.View(kit.Container([kit.Image(kit_image_png(image), "image/png"), kit.Text(args, 0, 0)]))
        ctx.set_widget("kit-img", view, {})

    ext.command("kit-images", "Exercise the component kit's image transport (D107)", kit_images)

    # kit-kinds sets the "kit-kinds" widget to the kinds kit-probe does not
    # draw (D107, spec §10): a box, a settings list, a loader, an image and a
    # lines node whose list annotation goes out only while a frontend draws.
    def kit_kinds(ctx: pig_sdk.Context, _args: str) -> None:
        settings = kit.SettingsList("kit-settings", [
            kit.SettingItem("theme", "Theme", "dark", description="Color theme", values=["dark", "light"]),
            kit.SettingItem("wrap", "Wrap", "on", description="Wrap lines", values=["on", "off"]),
        ], 3)
        loader = kit.Loader("Working", spinner_color="accent", message_color="muted", indicator=kit.LoaderIndicator(frames=["*"]))
        lines = kit.Lines(
            ["track one", "track two"],
            list=kit.List([kit.ListItem("track one", "A"), kit.ListItem("track two", "B")], selected=1),
        )
        stack = kit.VStack([
            kit.Box(1, 0, bg="customMessageBg", children=[kit.Text("boxed", 0, 0)]),
            settings,
            loader,
            kit.Image(bytes.fromhex(KIT_KINDS_PNG), "image/png"),
            lines,
        ], gap=1)
        ctx.set_widget("kit-kinds", kit.View(stack, theme={"accent": "#d75f00"}), {})

    ext.command("kit-kinds", "Show every other component kit kind (D107)", kit_kinds)

    # kit-conversation sets the "kit-conversation" widget to every
    # conversation kind (D107, spec §2.1, §10); "next" updates the nodes
    # with an id as a Pi author updates kept components.
    def kit_conversation(ctx: pig_sdk.Context, args: str) -> None:
        nxt = args == "next"
        user = kit.UserMessage("Fix **the** kit build\n\n- one\n- two")
        streaming = kit.AssistantMessage(id="kit-a1")
        reply = "Done. **Bold** reply\n\n1. a\n2. b"
        if nxt:
            reply += "\n\nThen more kit."
        streaming.update_content(kit.Message([kit.thinking_block("Reading the *kit* file"), kit.text_block(reply)]), not nxt)
        failed = kit.AssistantMessage(kit.Message([kit.thinking_block("secret"), kit.text_block("Visible kit")], stop_reason="error", error_message="kit-boom-7"))
        failed.set_hide_thinking_block(True)
        failed.set_hidden_thinking_label("Pondering kit...")
        failed.set_output_pad(0)

        def card(id: str, name: str, call_id: str, card_args: dict[str, Any], **options: Any) -> kit.ToolExecution:  # noqa: A002
            tool = kit.ToolExecution(name, call_id, card_args, "/work/kit", id=id, **options)
            tool.set_args_complete()
            tool.mark_execution_started()
            return tool

        ls = card("kit-t1", "ls", "call-1", {"path": "src"})
        ls.update_result(kit.ToolResult([kit.text_content("a.go\nb.go")]), False)
        grep = card("kit-t2", "grep", "call-2", {"pattern": "TODO"})
        if nxt:
            grep.update_result(kit.ToolResult([kit.text_content("x.go:1: TODO kit\ny.go:2: TODO kit")]), False)
        else:
            grep.update_result(kit.ToolResult([kit.text_content("x.go:1: TODO kit")]), True)
        read = card("", "read", "call-3", {"path": "missing.txt"})
        read.update_result(kit.ToolResult([kit.text_content("ENOENT: kit")], is_error=True), False)
        read.set_expanded(True)
        custom = card("", "kit_tool", "call-4", {"q": "x"}, tool_definition=kit.TOOL_DEFINITION_EMPTY)
        custom.update_result(kit.ToolResult([kit.text_content("answer 42")]), False)
        exited = kit.BashExecution("ls -la", id="kit-b1")
        exited.append_output("a.txt\n")
        exited.append_output("b.txt")
        exited.set_complete(2)
        exited.set_expanded(nxt)
        seq = kit.BashExecution("seq 25", True)
        seq.append_output("\n".join(str(i + 1) for i in range(25)))
        seq.set_complete(0)
        diff = kit.Diff(" 1 keep\n-2 old kit line\n+2 new kit line\n 3 tail", file_path="kit.go")
        root = kit.Container([user, streaming, failed, ls, grep, read, custom, exited, seq, diff])
        ctx.set_widget("kit-conversation", kit.View(root), {})

    ext.command("kit-conversation", "Show Pi's conversation components (D107)", kit_conversation)

    # kit-output-pad sets the "kit-output-pad" widget to a bash tool card and
    # a bash command with Pi 1.1.0's outputPad 3 and a final result's
    # durationMs 1234 (F7); "next" calls setOutputPad(2) on both and gives
    # the result durationMs 61500.
    def kit_output_pad(ctx: pig_sdk.Context, args: str) -> None:
        nxt = args == "next"
        tool = kit.ToolExecution("bash", "call-p1", {"command": "echo kit"}, "/work/kit", output_pad=3, id="kit-p1")
        tool.set_args_complete()
        tool.mark_execution_started()
        tool.update_result(kit.ToolResult([kit.text_content("kit out")], duration_ms=61500 if nxt else 1234), False)
        command = kit.BashExecution("echo pad", output_pad=3, id="kit-p2")
        command.append_output("padded")
        command.set_complete(0)
        if nxt:
            tool.set_output_pad(2)
            command.set_output_pad(2)
        ctx.set_widget("kit-output-pad", kit.View(kit.Container([tool, command])), {})

    ext.command("kit-output-pad", "Show Pi's outputPad and durationMs (D107)", kit_output_pad)

    def timer_focused_probe(ctx: pig_sdk.Context, args: str) -> None:
        component = TimerFocused()
        frame = ctx.custom(component, {"title": "Timer"})
        ctx.notify(
            "timer=%s disposed=%s detached=%s"
            % (frame or 0, str(component.disposed).lower(), str(component.detached).lower()),
            "info",
        )

    ext.command("timer-focused-probe", "Exercise timer-driven focused UI", timer_focused_probe)

    class OverlayWidthProbe:
        def __init__(self) -> None:
            self.width = 0

        def render(self, width: int) -> list[str]:
            self.width = width
            return [f"overlay width={width}"]

        def handle_input(self, data: str) -> pig_sdk.RemoteComponentResult:
            if data == "\r":
                return pig_sdk.RemoteComponentResult(done=True, value=self.width)
            return pig_sdk.RemoteComponentResult()

    def overlay_width_probe(ctx: pig_sdk.Context, _args: str) -> None:
        default_width = ctx.custom(OverlayWidthProbe(), {"overlay": True})
        percent_width = ctx.custom(OverlayWidthProbe(), {"overlay": True, "overlayOptions": {"width": "50%"}})
        ctx.notify(f"overlay-width default={default_width} percent={percent_width}", "info")

    ext.command("overlay-width-probe", "Report the width overlay components render at", overlay_width_probe)

    def project_trust_error(_ctx: pig_sdk.Context, _data: dict[str, object]) -> dict[str, object]:
        raise RuntimeError("trust-boom")

    def provider_response(ctx, data):
        ctx.notify("provider-response=%s:%s:%s" % (data["type"], data["status"], data["headers"]["x-probe"]), "info")
        return {"cancel": True}

    ext.on_event("after_provider_response", provider_response)
    ext.on_event("after_provider_response", lambda ctx, data: ctx.notify("provider-response=second", "info"))
    ext.on_event(
        "message_update",
        lambda ctx, data: ctx.notify(
            "message-update=%s:%s:%s:%s"
            % (
                data.get("assistantMessageEvent", {}).get("type"),
                data.get("assistantMessageEvent", {}).get("contentIndex"),
                data.get("assistantMessageEvent", {}).get("delta"),
                str("assistantMessageEvent" in data.get("assistantMessageEvent", {})).lower(),
            ),
            "info",
        ),
    )
    def tool_execution_update(ctx: pig_sdk.Context, data: dict[str, Any]) -> None:
        if data.get("toolName") == "production_tool":
            args = data.get("args", {})
            partial = data.get("partialResult", {})
            ctx.notify(
                "tool-update=%s:%s:%s:%s:%s"
                % (data.get("toolName"), args.get("path"), args.get("nested", {}).get("depth"), partial.get("content", [{}])[0].get("text"), partial.get("details", {}).get("progress")),
                "info",
            )
            return
        ctx.notify(
            "tool-update=%s:%s:%s:%s"
            % (data.get("toolName"), json.dumps(data.get("args"), separators=(",", ":")), data.get("partialResult", {}).get("content", [{}])[0].get("text"), data.get("partialResult", {}).get("details", {}).get("progress")),
            "info",
        )

    def tool_execution_end(ctx: pig_sdk.Context, data: dict[str, Any]) -> None:
        result = data.get("result", {})
        content = result.get("content", [])
        if data.get("toolName") == "production_tool":
            ctx.notify(
                "tool-end=%s:%s:%s:%s:%s:%s:%s"
                % (data.get("toolName"), content[0].get("text"), len(content), content[1].get("data"), content[1].get("mimeType"), result.get("details", {}).get("nested", {}).get("value"), str(data.get("isError")).lower()),
                "info",
            )
            return
        ctx.notify(
            "tool-end=%s:%s:%s:%s:%s"
            % (data.get("toolName"), len(content), content[1].get("data"), result.get("details", {}).get("nested", {}).get("value"), str(data.get("isError")).lower()),
            "info",
        )

    # Typed tool events (upstream PowerShellToolCallEvent/BashToolCallEvent and
    # their result variants): record the input and details, block the
    # conformance sentinel command, and replace a result's content.
    def user_bash(_ctx: pig_sdk.Context, data: dict[str, Any]) -> dict[str, Any] | None:
        result: dict[str, Any] = {"output": "handled", "exitCode": 7, "cancelled": False, "truncated": False}
        value: dict[str, Any] = {"result": result}
        command = data.get("command")
        if command == "undefined":
            result["exitCode"] = None
        elif command == "undefined-path":
            result["fullOutputPath"] = None
        elif command == "missing":
            del result["exitCode"]
        elif command == "invalid":
            result["exitCode"] = "invalid"
        elif command == "null-operations":
            value["operations"] = None
        elif command == "operations":
            return {"operations": ConformanceBashOperations()}
        elif command != "valid":
            return None
        return value

    ext.on_event("user_bash", user_bash)

    def tool_call(ctx: pig_sdk.Context, data: dict[str, Any]) -> dict[str, Any] | None:
        name = data.get("toolName")
        # Pi's handler mutates event.input in place and the runner reads it back (runner.ts emitToolCall), so the rewrite is the handler's own edit to the event.
        # Assigning a new dict to the event's input leaves the object the tool runs with (agent-loop.ts prepareToolCall).
        if name == "rewrite_reassign_probe":
            data["input"] = {**data["input"], "command": "git status --short"}
            return None
        # Moving a member to the end is an edit in Pi, and two added members follow in the order the handler added them.
        if name == "rewrite_order_probe":
            order = data["input"]
            if order.get("command") == "reorder":
                order["timeout"] = order.pop("timeout")
            else:
                order["zeta"] = 1
                order["alpha"] = 2
            return None
        if name in ("rewrite_probe", "rewrite_block_probe"):
            rewrite = data["input"]
            if rewrite.get("command") == "git status" or name == "rewrite_block_probe":
                rewrite["command"] = "git status --short"
                rewrite.pop("drop", None)
                rewrite["added"] = True
                rewrite["nested"] = {"depth": 2}
            if name == "rewrite_block_probe":
                return {"block": True, "reason": "blocked after rewrite"}
            return None
        if name not in ("powershell", "bash"):
            return None
        tool_input = data.get("input") or {}
        ctx.notify("tool-call=%s:%s:%s" % (name, tool_input.get("command"), tool_input.get("timeout")), "info")
        if tool_input.get("command") == "blocked-command":
            return {"block": True, "reason": "blocked %s" % name}
        return None

    def tool_result(ctx: pig_sdk.Context, data: dict[str, Any]) -> dict[str, Any] | None:
        if data.get("toolName") == "order_probe":
            ctx.notify("order-result=%s" % data.get("toolCallId"), "info")
            return None
        guards = (
            ("bash", pig_sdk.is_bash_tool_result),
            ("powershell", pig_sdk.is_powershell_tool_result),
            ("read", pig_sdk.is_read_tool_result),
            ("edit", pig_sdk.is_edit_tool_result),
            ("write", pig_sdk.is_write_tool_result),
            ("grep", pig_sdk.is_grep_tool_result),
            ("find", pig_sdk.is_find_tool_result),
            ("ls", pig_sdk.is_ls_tool_result),
        )
        ctx.notify("tool-result-guards=" + ",".join(name for name, is_result in guards if is_result(data)), "info")
        name = data.get("toolName")
        if name not in ("powershell", "bash"):
            return None
        details = data.get("details") or {}
        content = data.get("content") or [{}]
        ctx.notify(
            "tool-result=%s:%s:%s:%s"
            % (name, details.get("fullOutputPath"), (details.get("truncation") or {}).get("totalLines"), content[0].get("text")),
            "info",
        )
        return {"content": [{"type": "text", "text": "%s redacted" % name}]}

    ext.on_event("tool_call", tool_call)
    ext.on_event("tool_result", tool_result)
    ext.on_event("tool_execution_update", tool_execution_update)
    ext.on_event("tool_execution_end", tool_execution_end)
    ext.on_project_trust(project_trust_error)
    ext.on_project_trust(lambda _ctx, _data: {"trusted": "undecided"})
    ext.on_project_trust(lambda _ctx, data: {"trusted": "undecided"} if data.get("cwd") == "/probe" else {"trusted": "yes", "remember": True})
    def cache_warming_decision(_ctx: pig_sdk.Context, data: dict[str, Any]) -> dict[str, Any]:
        if data.get("warmCost") != 0.05 or data.get("missCost") != 0.5 or data.get("continuationProbability") != 0.15 or data.get("action") != "warm":
            raise ValueError("unexpected cache decision")
        return {"action": "stop"}

    ext.on_event("cache_warming_decision", cache_warming_decision)
    def agent_before_settle(ctx: pig_sdk.Context, data: dict[str, Any]) -> None:
        entries = data.get("entries") or []
        preview = data.get("context") or {}
        context_entries = preview.get("contextEntries") or []
        ctx.notify(
            "agent_before_settle:%s:%d:%s:%d:%s"
            % (
                data.get("outcome", ""),
                len(entries),
                str(bool(data.get("continue", False))).lower(),
                len(context_entries),
                str(bool(preview.get("canContinue", False))).lower(),
            ),
            "info",
        )
        data["entries"].append({"type": "custom", "customType": "kept"})

    def boundary_error(_ctx: pig_sdk.Context, data: dict[str, Any]) -> None:
        data["entries"].append({"type": "custom", "customType": "before-error"})
        raise RuntimeError("boundary failed")

    def boundary_result(_ctx: pig_sdk.Context, data: dict[str, Any]) -> dict[str, Any]:
        assert [entry["customType"] for entry in data["entries"]] == ["kept", "before-error"]
        assert len(data["context"]["contextEntries"]) == 2
        return {"entries": [{"type": "custom", "customType": "conformance-boundary"}], "continue": True}

    ext.on_event("agent_before_settle", agent_before_settle)
    ext.on_event("agent_before_settle", boundary_error)
    ext.on_event("agent_before_settle", boundary_result)
    def session_start(ctx, data):
        ctx.notify("session_start:" + str(data.get("reason", "")), "info")
        if data.get("previousSessionFile"):
            ctx.notify("previous:" + data["previousSessionFile"], "info")

    def session_shutdown(ctx, data):
        ctx.notify("session_shutdown:" + str(data.get("reason", "")), "info")
        if data.get("targetSessionFile"):
            ctx.notify("target:" + data["targetSessionFile"], "info")

    ext.on_event("session_start", session_start)
    ext.on_event("session_shutdown", session_shutdown)
    ext.on_event("session_info_changed", lambda ctx, data: ctx.notify("session_info_changed:" + str(data.get("name", "")), "info"))
    for name in ("ui_prompt_start", "ui_prompt_end"):
        ext.on_event(name, ui_prompt_event)
    ext.on_event(
        "session_before_compact",
        lambda ctx, data: ctx.notify(
            "session_before_compact:%s:%s"
            % (data.get("reason", ""), str(data.get("willRetry", False)).lower()),
            "info",
        ),
    )
    ext.on_event(
        "session_compact",
        lambda ctx, data: ctx.notify(
            "session_compact:%s:%s:%s"
            % (
                data.get("reason", ""),
                str(data.get("willRetry", False)).lower(),
                str(data.get("fromExtension", False)).lower(),
            ),
            "info",
        ),
    )
    ext.on_event(
        "session_compact_failed",
        lambda ctx, data: ctx.notify(
            "session_compact_failed:%s:%s:%s:%s:%s"
            % (
                data.get("reason", ""),
                data.get("errorMessage", ""),
                str(data.get("aborted", False)).lower(),
                str(data.get("willRetry", False)).lower(),
                str(data.get("fromExtension", False)).lower(),
            ),
            "info",
        ),
    )
    def mutate_turn_boundary(_ctx, data):
        if data.get("messageEntryId") != "boundary-assistant":
            return
        data["entries"].append({"type": "custom", "customType": "mutated"})
        raise RuntimeError("turn-boundary-failure")

    def snapshot_turn_boundary(_ctx, data):
        if data.get("messageEntryId") == "boundary-assistant":
            return {"entries": [{"type": "custom", "customType": "turn-boundary", "data": data}], "continue": True}

    ext.on_event("turn_end", mutate_turn_boundary)
    ext.on_event("turn_end", snapshot_turn_boundary)
    ext.on_event(
        "turn_end",
        lambda ctx, data: ctx.notify(
            "turn_end:%s:%s" % (data.get("messageEntryId", ""), data.get("toolResultEntryIds", [""])[0]),
            "info",
        ),
    )
    _register_conformance_oauth(ext)
    _register_conformance_oauth_object(ext)
    _register_conformance_oauth_large(ext)
    _register_state_push_probe(ext)
    _register_signal_abort(ext)
    return ext


class _ConformanceStore:
    """Owns credentials for the conformance OAuth provider; values must match the
    Go and Rust fixtures so the recordings compare equal."""

    def credential_status(self) -> pig_sdk.OAuthCredentialStatus:
        return pig_sdk.OAuthCredentialStatus(present=True, auth_type="oauth", source="conformance")

    def store_credentials(self, creds: pig_sdk.OAuthCredentials) -> str:
        if creds.account_id != "account-store" or creds.scope != "scope-store":
            raise RuntimeError("credential metadata lost")
        return "/conf/creds.json"

    def delete_credentials(self) -> bool:
        return True


def _conformance_api_key(creds: pig_sdk.OAuthCredentials) -> str:
    if creds.access == "boom":
        raise RuntimeError("getApiKey exploded")
    return "key:" + creds.access


def _register_conformance_oauth_object(ext: pig_sdk.Extension) -> None:
    """Contribute a provider whose callbacks treat credentials as Pi's complete token object: login
    returns a fractional expiry and provider-owned keys, and refresh spreads its input as
    ``{ ...creds, access, expires }`` does. Behavior must match the Go, Rust and Node fixtures."""

    def login(cb: pig_sdk.OAuthLoginCallbacks) -> pig_sdk.OAuthCredentials:
        return pig_sdk.OAuthCredentials(
            access="object-access", refresh="object-refresh", expires=1700000000000.25, extra={"meta": {"k": [1, None, ""]}, "projectId": ""}
        )

    def refresh(creds: pig_sdk.OAuthCredentials) -> pig_sdk.OAuthCredentials:
        return dataclasses.replace(creds, access="refreshed-" + creds.refresh, expires=creds.expires + 0.5, extra=dict(creds.extra))

    def api_key(creds: pig_sdk.OAuthCredentials) -> str:
        typed = "typed" if creds.extra.get("type") == "oauth" else "untyped"
        meta = "meta" if "meta" in creds.extra else "nometa"
        return f"key:{typed}:{meta}"

    ext.register_oauth_provider(
        "conformance-oauth-object",
        {"name": "Conformance OAuth Object"},
        pig_sdk.OAuthProvider(name="Conformance OAuth Object", login=login, refresh_token=refresh, get_api_key=api_key),
    )


def _register_conformance_oauth_large(ext: pig_sdk.Extension) -> None:
    """Contribute a provider whose expiry is the double 2**60: JSON.parse reads the wire digits 1152921504606847000 as that double, so getApiKey reports a distance of 0 from 2**60. Behavior must match the Go, Rust and Node fixtures."""

    def login(cb: pig_sdk.OAuthLoginCallbacks) -> pig_sdk.OAuthCredentials:
        return pig_sdk.OAuthCredentials(access="large-access", refresh="large-refresh", expires=2**60)

    ext.register_oauth_provider(
        "conformance-oauth-large",
        {"name": "Conformance OAuth Large"},
        pig_sdk.OAuthProvider(name="Conformance OAuth Large", login=login, get_api_key=lambda creds: f"key:{int(creds.expires - 2**60)}:{int(creds.expires) - 2**60}"),
    )


def _register_conformance_oauth(ext: pig_sdk.Extension) -> None:
    """Contribute the canonical OAuth provider the cross-transport conformance
    suite drives. Behavior must match the Go and Rust fixtures byte-for-byte."""

    def login(cb: pig_sdk.OAuthLoginCallbacks) -> pig_sdk.OAuthCredentials:
        cb.on_device_code(
            pig_sdk.OAuthDeviceCodeInfo(user_code="CONF-USER-CODE", verification_uri="https://conf.example/verify")
        )
        cb.on_progress("waiting")
        value = cb.on_prompt(pig_sdk.OAuthPrompt(message="paste the code"))
        return pig_sdk.OAuthCredentials(access="access-" + value, refresh="refresh-tok", expires=4242, account_id="account-login", scope="scope-login")

    ext.register_oauth_provider(
        "conformance-oauth",
        {"name": "Conformance OAuth"},
        pig_sdk.OAuthProvider(
            name="Conformance OAuth",
            is_subscription=True,
            login=login,
            refresh_token=lambda creds: pig_sdk.OAuthCredentials(
                access="refreshed-" + creds.refresh, refresh=creds.refresh, expires=9999, account_id=creds.account_id, scope=creds.scope
            ),
            get_api_key=_conformance_api_key,
            credential_store=_ConformanceStore(),
        ),
    )


# STATE_PUSH_WINDOW is how long a state-push-probe reader waits for the value it armed on to change before it reports the value it still holds.
STATE_PUSH_WINDOW = 5.0


def _state_push_reader(ctx, kind):
    """The read of one replicated accessor, as a string that fails soft: a read that raises reports its error."""
    if kind == "model":
        read = lambda: ctx.model
    elif kind == "session-name":
        read = lambda: ctx.session_name
    elif kind == "theme":
        read = lambda: ctx.theme.name or ""
    elif kind.startswith("settings:"):
        key = kind[len("settings:"):]
        read = lambda: json.dumps(ctx.get_settings().get(key), separators=(",", ":"))
    elif kind == "mcp":
        read = lambda: ",".join(server["name"] for server in ctx.get_mcp_servers())
    else:
        raise ValueError("state-push-probe: unsupported kind %r" % kind)

    def guarded():
        try:
            return read()
        except Exception as error:  # noqa: BLE001 - the report carries the failure
            return "error:%s" % error

    return guarded


def _register_state_push_probe(ext: pig_sdk.Extension) -> None:
    """Register the state-push-probe command (TestHostStateChangeReachesABackgroundReaderAcrossSDKs). The command arms a thread that reads one replicated accessor from outside every request, as an extension's timer does, and returns: the host then changes its own state with no call and no event to this extension, and the thread reports the first value that differs from the one it armed on, or the value it still holds after STATE_PUSH_WINDOW, to a file in the directory the arguments name. The arguments are {"kind": "model" | "session-name" | "theme" | "settings:<key>" | "mcp", "dir": <directory>}; the report is "<armed>=><read>"."""

    def probe(ctx, args):
        request = json.loads(args)
        kind, directory = request["kind"], request["dir"]
        read = _state_push_reader(ctx, kind)
        armed = read()

        def watch():
            current = armed
            deadline = time.monotonic() + STATE_PUSH_WINDOW
            while current == armed and time.monotonic() < deadline:
                time.sleep(0.002)
                current = read()
            report = pathlib.Path(directory) / ("state-push-" + kind.replace(":", "_"))
            temp = report.with_name(report.name + ".tmp")
            temp.write_text(armed + "=>" + current)
            temp.replace(report)

        threading.Thread(target=watch, daemon=True).start()

    ext.command("state-push-probe", "Read one replicated accessor from a thread between requests", probe)


def _register_signal_abort(ext: pig_sdk.Extension) -> None:
    """Contribute the signal-abort row every SDK fixture carries. Pi's ctx.abort() aborts the
    run's signal before it returns (agent.ts:340-342), so a handler that holds the signal and
    reads it again right after the abort sees it aborted, in the same turn."""

    def state(signal: object) -> str:
        return "none" if signal is None else "aborted" if signal.is_set() else "live"

    def signal_abort(ctx: pig_sdk.Context, args: str) -> None:
        held = ctx.signal
        ctx.abort()
        ctx.notify("abort:" + state(held) + ":" + state(ctx.signal), "info")

    ext.command("signal-abort", "Abort the run and report ctx.signal", signal_abort)


if __name__ == "__main__":
    new_extension().run()
