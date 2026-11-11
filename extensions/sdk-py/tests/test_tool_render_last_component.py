"""ToolRenderContext.last_component: the previous run of the card's renderer of the same phase, nil after a failed run."""
from __future__ import annotations

import pytest

import pig_sdk
from pig_sdk import kit


def _extension(kinds: list[str] | None = None) -> pig_sdk.Extension:
    ext = pig_sdk.Extension("probe")
    ext.tool("probe", "probe", {"type": "object"}, lambda _ctx, _params: "ok")

    def call(_ctx, args, render, _width):
        last = "none" if render.last_component is None else "|".join(render.last_component)
        if args.get("fail"):
            raise RuntimeError("boom")
        return ["run%s:last=%s" % (args.get("n"), last)]

    def result(_ctx, _result, _options, render, _width):
        last = render.last_component
        kind = "none" if last is None else ("view" if isinstance(last, kit.View) else "lines")
        if kinds is not None:
            kinds.append(kind)
        return kit.View(kit.Container([kit.Text("result:last=" + kind, 0, 0)]))

    ext.tool_renderers("probe", render_call=call, result_view=result)
    return ext


def _call(ext: pig_sdk.Extension, card: str, args: dict) -> list[str]:
    lines, is_view = ext._render_tool(None, "probe", {"card": card, "phase": "call", "width": 40, "args": args})
    assert not is_view
    return lines


def test_last_component_is_the_previous_run_and_none_after_a_failed_run():
    ext = _extension()
    assert _call(ext, "c1", {"n": 1}) == ["run1:last=none"]
    assert _call(ext, "c1", {"n": 2}) == ["run2:last=run1:last=none"]
    with pytest.raises(RuntimeError):
        _call(ext, "c1", {"n": 3, "fail": True})
    # the failed run left no component: the next run starts from nothing, not from run 2's lines
    assert _call(ext, "c1", {"n": 4}) == ["run4:last=none"]
    assert _call(ext, "c1", {"n": 5}) == ["run5:last=run4:last=none"]
    # a card of its own starts from nothing
    assert _call(ext, "c2", {"n": 1}) == ["run1:last=none"]


def test_the_result_phase_has_a_slot_of_its_own_and_a_view_stands_in_for_the_component():
    kinds: list[str] = []
    ext = _extension(kinds)
    for _ in range(2):
        _view, is_view = ext._render_tool(None, "probe", {"card": "c1", "phase": "result", "width": 40, "args": {}})
        assert is_view
    assert kinds == ["none", "view"]
