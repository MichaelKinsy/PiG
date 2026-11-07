"""Host values the SDK compares, reads and writes with the interpreter (a palette, a keybinding table, a theme_change string, the options of a system prompt, a command's arguments, a session log line) are deep-safe.

CPython's C readers, writers and ``==`` stop at the interpreter's limit: about 990 levels on 3.11, 3000 on 3.12 on Windows (``C_RECURSION_LIMIT``), 10000 on 3.12 elsewhere. The host sends values up to 10000 levels,
so each row runs at 10000 and 12000 levels (past the limit everywhere but a 3.12 that allows more) and again under a recursion limit a few hundred frames above the test's own (where 3.11 stops at once).
"""

import contextlib
import inspect
import json
import sys
import tempfile
import time

import pig_sdk
import pytest
from pig_sdk import _json_loads, _json_text, _tui_state

DEPTHS = (2000, 10000, 12000)


def deep(depth, leaf=1):
    value = leaf
    for _ in range(depth):
        value = {"a": value}
    return value


def deep_text(depth):
    return '{"a":' * depth + "1" + "}" * depth


def deepest(value):
    """The dict that holds the leaf of ``deep``."""
    while isinstance(value["a"], dict):
        value = value["a"]
    return value


@contextlib.contextmanager
def low_recursion_limit():
    """A recursion limit 150 frames above the caller: 3.11's JSON reader, writer and ``==`` stop here, and any Python-level walk over a deep value does too."""
    frames = len(inspect.stack(0))
    old = sys.getrecursionlimit()
    sys.setrecursionlimit(frames + 150)
    try:
        yield
    finally:
        sys.setrecursionlimit(old)


@pytest.fixture(autouse=True)
def fresh_host_theme():
    _tui_state._host_palette = None
    _tui_state._installed_palette = None
    _tui_state._keybindings = None
    yield
    _tui_state._host_palette = None
    _tui_state._installed_palette = None
    _tui_state._keybindings = None


def test_json_equal_is_equality_of_decoded_json():
    cases = [
        ({}, {}, True), ({"a": 1}, {"a": 1.0}, True), ({"a": 1}, {"a": True}, True), ({"a": 1}, {"a": 2}, False), ({"a": 1}, {"b": 1}, False), ({"a": 1}, {"a": 1, "b": 2}, False),
        ([1, [2, {"x": None}]], [1, [2, {"x": None}]], True), ([1, 2], [1, 2, 3], False), ([1], {"0": 1}, False), ({"a": []}, {"a": {}}, False),
        ("s", "s", True), ("s", "t", False), (None, None, True), (None, 0, False), ({"a": {"b": [1, "x"]}}, {"a": {"b": [1, "y"]}}, False),
    ]
    for left, right, want in cases:
        assert _tui_state.json_equal(left, right) is want, (left, right)
        assert (left == right) is want, "the table states what == says"


@pytest.mark.parametrize("depth", DEPTHS)
def test_json_equal_compares_values_nested_past_the_interpreters_limit(depth):
    for limited in (False, True):
        with low_recursion_limit() if limited else contextlib.nullcontext():
            a, b, c = deep(depth), deep(depth), deep(depth, leaf=2)
            assert _tui_state.json_equal(a, b)
            assert not _tui_state.json_equal(a, c)
            assert not _tui_state.json_equal(a, deep(depth - 1))
            deepest(b)["a"] = [1]
            assert not _tui_state.json_equal(a, b)


@pytest.mark.parametrize("depth", DEPTHS)
def test_the_host_theme_and_keybindings_compare_at_any_depth(depth):
    with low_recursion_limit():
        _tui_state.set_host_theme(deep(depth))
        first = _tui_state._host_palette
        _tui_state._installed_palette = {"installed": True}
        _tui_state.set_host_theme(deep(depth))  # the same palette again, as every snapshot repeats it
        assert _tui_state._installed_palette == {"installed": True} and _tui_state._host_palette is first
        _tui_state.set_host_theme(deep(depth, leaf=2))
        assert _tui_state._installed_palette is None
        table = {"definitions": deep(depth)}
        _tui_state.apply_host_state({"keybindings": table})
        assert _tui_state.take_keybindings() is table
        _tui_state.apply_host_state({"keybindings": {"definitions": deep(depth)}})
        assert _tui_state.take_keybindings() is None, "the same table is not a change"
        _tui_state.apply_host_state({"keybindings": {"definitions": deep(depth, leaf=2)}})
        assert _tui_state.take_keybindings() is not None


@pytest.mark.parametrize("depth", DEPTHS)
def test_a_theme_palette_nested_deep_is_a_change_only_when_it_differs(depth):
    ext = pig_sdk.Extension("deep-palette")
    ctx = pig_sdk.Context(ext)
    seen = []
    ctx.on_theme_change(lambda theme: seen.append(theme.name))

    def palette(name, leaf=1):
        return {"name": name, "foregrounds": {}, "backgrounds": {}, "extra": deep(depth, leaf)}

    def apply(value):
        with ext._state_lock:
            ext._apply_ui_state({"theme": value})

    with low_recursion_limit():
        apply(palette("one"))
        apply(palette("one"))
        apply(palette("one", leaf=2))
        apply(palette("two"))
    deadline = time.time() + 10
    while len(seen) < 2 and time.time() < deadline:
        time.sleep(0.01)
    assert seen == ["one", "two"], "an equal palette is not a change; a palette that differs only at the bottom is"


@pytest.mark.parametrize("depth", (2000, 10000))
def test_a_theme_change_notification_carries_a_palette_the_interpreter_cannot_read(depth):
    ext = pig_sdk.Extension("deep-theme-change")
    palette = '{"name":"deep","foregrounds":{},"backgrounds":{},"extra":' + deep_text(depth) + "}"
    with low_recursion_limit():
        ext._handle_notify({"type": "notify", "notify": {"method": "theme_change", "args": palette}})
    assert ext._theme.name == "deep"
    assert _tui_state._host_palette["name"] == "deep"
    # A string nested past what the host can send is no palette: the empty one, and the extension goes on.
    ext._handle_notify({"type": "notify", "notify": {"method": "theme_change", "args": '{"extra":' + deep_text(10200) + "}"}})
    assert _tui_state._host_palette == {}


class _Ctx:
    def __init__(self):
        self.renders = []

    def _reply_field(self, method, field, args=None):
        self.renders.append(args)
        return "rendered %d" % len(self.renders)


@pytest.mark.parametrize("depth", DEPTHS[:2])
def test_a_system_prompt_with_deeply_nested_options_renders_again_only_when_they_change(depth):
    ctx = _Ctx()
    options = {"sections": deep(depth)}
    with low_recursion_limit():
        prompt = pig_sdk._SystemPrompt(ctx, options, "received")
        assert prompt.get() == "received" and ctx.renders == []
        deepest(options["sections"])["a"] = 2
        assert prompt.get() == "rendered 1"
        assert prompt.get() == "rendered 1" and len(ctx.renders) == 1


@pytest.mark.parametrize("depth", DEPTHS[:2])
def test_json_text_and_loads_take_what_the_interpreters_cannot(depth):
    with low_recursion_limit():
        text = _json_text(deep(depth))
        assert text.replace(" ", "").startswith('{"a":{"a":') and text.count("{") == depth
        value = _json_loads(deep_text(depth))
        assert _tui_state.json_equal(value, deep(depth))
    with pytest.raises(ValueError):
        _json_loads('{"a":')
    assert _json_loads('{"a": [1, "\\ud800"]}') == {"a": [1, "\ud800"]}


def test_a_session_log_line_nested_deep_is_read_and_one_too_deep_is_not_a_log():
    with tempfile.TemporaryDirectory() as directory:
        path = directory + "/s.jsonl"
        with open(path, "w", encoding="utf-8") as log:
            log.write(json.dumps({"type": "session", "id": "s"}) + "\n")
            log.write('{"type":"message","id":"1","message":' + deep_text(10000) + "}\n")
        with low_recursion_limit():
            entries = pig_sdk.Extension._read_session_entries(path)
        assert len(entries) == 1 and entries[0]["id"] == "1"
        with open(path, "a", encoding="utf-8") as log:
            log.write('{"type":"message","id":"2","message":' + deep_text(10200) + "}\n")
        assert pig_sdk.Extension._read_session_entries(path) == []



def deep_array(depth, leaf=1):
    value = leaf
    for _ in range(depth):
        value = [value]
    return value


def _reference_stringify_length(value):
    """The recursive walk ``_json_stringify_length`` replaced, as the reference for values it can take."""
    from collections.abc import Mapping

    from pig_sdk.compaction_estimate import _js_number_string, _json_quoted_length

    if value is None:
        return 4
    if isinstance(value, bool):
        return 4 if value else 5
    if isinstance(value, (int, float)):
        return len(_js_number_string(value))
    if isinstance(value, str):
        return _json_quoted_length(value)
    if isinstance(value, (list, tuple)):
        return 2 + sum(_reference_stringify_length(element) for element in value) + max(len(value) - 1, 0)
    if isinstance(value, Mapping):
        return 2 + sum(_json_quoted_length(str(key)) + 1 + _reference_stringify_length(element) for key, element in value.items()) + max(len(value) - 1, 0)
    return 0


def _reference_js_value_string(value):
    """The recursive walk ``_js_value_string`` replaced."""
    from pig_sdk.llm_convert import _js_number_to_string

    if value is None:
        return "null"
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, str):
        return value
    if isinstance(value, (int, float)):
        return _js_number_to_string(value)
    if isinstance(value, (list, tuple)):
        return ",".join("" if element is None else _reference_js_value_string(element) for element in value)
    return "[object Object]"


def _samples():
    import random

    rng = random.Random(7)
    scalars = [None, True, False, 0, 1, -2, 1.5, 1e21, float("nan"), float("inf"), "", "a", 'q"\n\u2028', "\ud800", "\U0001F600", [], {}]

    def build(depth):
        kind = rng.randrange(4) if depth < 5 else 0
        if kind == 0:
            return rng.choice(scalars)
        if kind == 1:
            return [build(depth + 1) for _ in range(rng.randrange(4))]
        if kind == 2:
            return {rng.choice(["a", "b", "0", "é", '"']): build(depth + 1) for _ in range(rng.randrange(4))}
        return (build(depth + 1), build(depth + 1))

    return [build(0) for _ in range(3000)]


def test_the_iterative_walks_equal_the_recursive_ones_they_replaced():
    from pig_sdk.compaction_estimate import _json_stringify_length
    from pig_sdk.llm_convert import _js_value_string

    for value in _samples():
        assert _json_stringify_length(value) == _reference_stringify_length(value), value
        assert _js_value_string(value) == _reference_js_value_string(value), value
    assert _js_value_string([1, [2, None, [3, []]], None]) == "1,2,,3,,"


@pytest.mark.parametrize("depth", (2000, 10000, 12000))
def test_a_tool_calls_arguments_nested_deep_are_measured_for_compaction(depth):
    from pig_sdk.compaction_cutpoint import find_cut_point
    from pig_sdk.compaction_estimate import _json_stringify_length

    with low_recursion_limit():
        assert _json_stringify_length(deep_array(depth)) == 2 * depth + 1
        assert _json_stringify_length(deep(depth)) == 6 * depth + 1  # {"a":...}
        message = {"role": "assistant", "content": [{"type": "toolCall", "name": "read", "arguments": {"meta": deep_array(depth)}}]}
        chars = len("read") + len('{"meta":') + 2 * depth + 1 + 1
        assert pig_sdk.estimate_tokens(message) == -(-chars // 4)
        entries = [{"type": "message", "id": str(index), "parentId": str(index - 1) if index else None, "message": message if index % 2 else {"role": "user", "content": "x"}} for index in range(6)]
        assert find_cut_point(entries, 0, len(entries), 1)  # estimates every message, the deep ones too


@pytest.mark.parametrize("depth", (2000, 10000, 12000))
def test_the_string_of_a_nested_array_is_taken_at_any_depth(depth):
    from pig_sdk.llm_convert import _js_value_string
    from pig_sdk.serialize_conversation import serialize_conversation

    with low_recursion_limit():
        assert _js_value_string(deep_array(depth)) == "1"
        assert _js_value_string(deep_array(depth, leaf=[1, None, [2]])) == "1,,2"
        assert _js_value_string(deep(depth)) == "[object Object]"
        text = serialize_conversation([{"role": "assistant", "content": [{"type": "toolCall", "id": "c", "name": deep_array(depth, "tool"), "arguments": {}}]}])
        assert "tool" in text
