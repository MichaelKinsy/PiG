"""Pi's ``tui`` handle (tui.ts:366 ``TUI.invalidate``, tui.ts:990 ``TUI.requestRender``) and D193's OSC 8 link claims."""

from __future__ import annotations

import threading
from typing import Any

from test_sdk import _write_frame
from test_sdk_surface import _Host

import pig_sdk

_UI = {"state": {"hasUI": True}}
_NO_UI = {"state": {"hasUI": False}}


def _command(host: _Host, req_id: str, name: str) -> None:
    host.request(req_id, {"method": "command", "tool": name})


def _serve(host: _Host, answers: dict[str, Any] | None = None) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    """Answer each host call until the response frame; return the calls and the response."""
    calls: list[dict[str, Any]] = []
    while True:
        env = host.read()
        if env["type"] == "response":
            return calls, env
        if env["type"] == "call":
            calls.append(env)
            answer = (answers or {}).get(env["call"]["method"])
            if isinstance(answer, dict) and "error" in answer:
                host.answer(env, error=answer["error"])
            else:
                host.answer(env, answer)


def _click(host: _Host, scheme: str, url: str) -> None:
    _write_frame(host.conn, {"type": "notify", "notify": {"method": "link_click", "args": {"scheme": scheme, "url": url}}})


def test_tui_handle_sends_invalidate_and_request_render_with_the_force_given() -> None:
    ext = pig_sdk.Extension("py-tui")

    def go(ctx: pig_sdk.Context, _args: str) -> None:
        ctx.tui.invalidate()
        ctx.tui.request_render()
        ctx.tui.request_render(True)

    ext.command("go", "run", go)
    host = _Host(ext, _UI)
    try:
        _command(host, "r1", "go")
        calls, _ = _serve(host)
        assert [(c["call"]["method"], c["call"]["args"]) for c in calls] == [
            ("ui.tui.invalidate", {}),
            ("ui.tui.requestRender", {"force": False}),
            ("ui.tui.requestRender", {"force": True}),
        ]
    finally:
        host.close()


def test_tui_handle_with_no_ui_sends_nothing() -> None:
    ext = pig_sdk.Extension("py-tui-noui")

    def go(ctx: pig_sdk.Context, _args: str) -> None:
        ctx.tui.invalidate()
        ctx.tui.request_render(True)

    ext.command("go", "run", go)
    host = _Host(ext, _NO_UI)
    try:
        _command(host, "r1", "go")
        calls, response = _serve(host)
        assert calls == []
        assert response["type"] == "response"
    finally:
        host.close()


def test_on_link_click_claims_the_scheme_and_delivers_clicks_in_order_until_unsubscribed() -> None:
    ext = pig_sdk.Extension("py-link")
    seen: list[str] = []
    three = threading.Event()
    box: dict[str, Any] = {}

    def on_click(url: str) -> None:
        seen.append(url)
        if len(seen) == 3:
            three.set()

    def claim(ctx: pig_sdk.Context, _args: str) -> None:
        box["off"] = ctx.on_link_click("probe", on_click)

    def release(_ctx: pig_sdk.Context, _args: str) -> None:
        box["off"]()
        box["off"]()  # idempotent

    ext.command("claim", "claim", claim)
    ext.command("release", "release", release)
    host = _Host(ext, _UI)
    try:
        _command(host, "r1", "claim")
        calls, _ = _serve(host)
        assert [(c["call"]["method"], c["call"]["args"]) for c in calls] == [("ui.onLinkClick", {"scheme": "probe"})]
        for url in ("probe:1", "probe:2"):
            _click(host, "probe", url)
        _click(host, "other", "other:1")  # another scheme: nobody's click
        _click(host, "probe", "probe:3")
        assert three.wait(5), f"clicks delivered = {seen}"
        assert seen == ["probe:1", "probe:2", "probe:3"]

        _command(host, "r2", "release")
        calls, _ = _serve(host)
        assert [(c["call"]["method"], c["call"]["args"]) for c in calls] == [("ui.offLinkClick", {"scheme": "probe"})]
        _click(host, "probe", "probe:4")
        # A request after the click is answered after the reader handled the click, so the handler would have run by now.
        _command(host, "r3", "release")
        _serve(host)
        assert seen == ["probe:1", "probe:2", "probe:3"]
    finally:
        host.close()


def test_a_refused_claim_raises_and_keeps_the_earlier_claim() -> None:
    ext = pig_sdk.Extension("py-link-refused")
    seen: list[str] = []
    got = threading.Event()
    errors: list[BaseException] = []

    def on_first(url: str) -> None:
        seen.append("first:" + url)
        got.set()

    def first(ctx: pig_sdk.Context, _args: str) -> None:
        ctx.on_link_click("probe", on_first)

    def second(ctx: pig_sdk.Context, _args: str) -> None:
        try:
            ctx.on_link_click("probe", lambda url: seen.append("second:" + url))
        except pig_sdk.HostCallError as err:
            errors.append(err)

    ext.command("first", "first", first)
    ext.command("second", "second", second)
    host = _Host(ext, _UI)
    try:
        _command(host, "r1", "first")
        _serve(host)
        _command(host, "r2", "second")
        _serve(host, {"ui.onLinkClick": {"error": {"code": "scheme_in_use", "message": "held"}}})
        assert len(errors) == 1 and "held" in str(errors[0])
        _click(host, "probe", "probe:9")
        assert got.wait(5)
        assert seen == ["first:probe:9"]
    finally:
        host.close()


def test_on_link_click_with_no_ui_claims_nothing_and_rejects_a_bad_handler() -> None:
    ext = pig_sdk.Extension("py-link-noui")
    outcome: dict[str, Any] = {}

    def go(ctx: pig_sdk.Context, _args: str) -> None:
        outcome["off"] = ctx.on_link_click("probe", lambda url: None)
        try:
            ctx.on_link_click("probe", None)  # type: ignore[arg-type]
        except TypeError as err:
            outcome["error"] = str(err)

    ext.command("go", "run", go)
    host = _Host(ext, _NO_UI)
    try:
        _command(host, "r1", "go")
        calls, _ = _serve(host)
        assert calls == []
        outcome["off"]()
        assert "callable handler" in outcome["error"]
    finally:
        host.close()
