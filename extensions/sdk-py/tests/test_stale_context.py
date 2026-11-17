"""A context kept past its Session fails every member with the stale message (Pi guards every context member: runner.ts:883-1037).

The getters raise it as RuntimeError, as ``cwd`` and ``model`` do. The calls raise it as the HostCallError the host's answer to a call of a replaced runtime raises, and fail before they change anything.
The SDK's own renders read the width without the check, so a refresh of a footer, header or widget after the replacement does not end on the stale width read.
"""

from __future__ import annotations

import threading
from typing import Any

import pytest

import pig_sdk
from pig_sdk import kit

STALE = "This extension ctx is stale after session replacement or reload."


class Host:
    """Answers every call: the test then sees which calls the context sent."""

    def __init__(self) -> None:
        self.calls: list[tuple[str, Any]] = []

    def __call__(self, method: str, args: Any = None, *_: Any, **__: Any) -> dict[str, Any]:
        self.calls.append((method, args))
        return {"result": {}}


def make() -> tuple[pig_sdk.Extension, pig_sdk.Context, Host]:
    ext = pig_sdk.Extension("stale-members")
    host = Host()
    ext._call = host  # type: ignore[method-assign]
    ext._width = 90
    ext._height = 30
    ext._settings = {"a": 1}
    sent: list[dict[str, Any]] = []
    ext._send = sent.append  # type: ignore[method-assign]
    ext._notify = lambda method, args: sent.append({"notify": method, "args": args})  # type: ignore[method-assign]
    return ext, pig_sdk.Context(ext, request_id="request", tool_call_id="tool"), host


def test_the_getters_raise_the_stale_message_and_answer_while_active():
    ext, ctx, _ = make()
    assert (ctx.width, ctx.height, ctx.session_name) == (90, 30, "")
    assert ctx.theme is not None
    ext._stale_message = STALE
    for name in ("theme", "width", "height", "session_name"):
        with pytest.raises(RuntimeError, match="stale after session replacement or reload"):
            getattr(ctx, name)


def test_the_calls_raise_the_stale_message_and_send_nothing():
    ext, ctx, host = make()
    surface = pig_sdk._SurfaceRenderer("ui.setWidget", lambda width: ["row"], "kept", None)
    ext._replace_surface_id("ui.setWidget:kept", surface)
    sent: list[dict[str, Any]] = []
    ext._send = sent.append  # type: ignore[method-assign]
    ext._notify = lambda method, args: sent.append({"notify": method, "args": args})  # type: ignore[method-assign]
    ext._stale_message = STALE
    calls = {
        "set_widget lines": lambda: ctx.set_widget("kept", ["x"]),
        "set_widget clear": lambda: ctx.set_widget("kept", None),
        "set_widget options": lambda: ctx.set_widget("kept", ["x"], {"placement": "belowEditor"}),
        "set_widget view": lambda: ctx.set_widget("kept", kit.View(kit.Text("x", 0, 0))),
        "set_widget_renderer": lambda: ctx.set_widget_renderer("kept", lambda width: ["y"]),
        "set_widget_renderer clear": lambda: ctx.set_widget_renderer("kept", None),
        "on_update": lambda: ctx.on_update("x"),
        "get_settings": lambda: ctx.get_settings(),
    }
    for name, call in calls.items():
        with pytest.raises(pig_sdk.HostCallError) as raised:
            call()
        assert raised.value.message == STALE, name
    assert sent == []
    assert host.calls == []
    assert ext._surfaces.get("ui.setWidget:kept") is surface


def test_a_stale_surface_push_reads_the_width_without_the_stale_check():
    ext, _, host = make()
    ctx = pig_sdk.Context(ext)
    widths: "list[int]" = []
    rendered = threading.Event()

    def render(width: int) -> "list[str]":
        widths.append(width)
        rendered.set()
        return ["row"]

    def refuse(method: str, args: Any = None, *_: Any, **__: Any) -> dict[str, Any]:
        raise pig_sdk.HostCallError(STALE)

    ext._call = refuse  # type: ignore[method-assign]
    surface = pig_sdk._SurfaceRenderer("ui.setFooter", render)
    ext._stale_message = STALE
    surface.refresh(ctx)
    assert rendered.wait(10)
    with pytest.raises(pig_sdk.HostCallError):
        surface.push(ctx)
    with pytest.raises(pig_sdk.HostCallError):
        ctx.set_footer(["x"])
    assert set(widths) == {90}
