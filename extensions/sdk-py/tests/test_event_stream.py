import pytest

from pig_sdk import Extension, _apply_stream_ops, _clone_stream_value


def request(seq, base=0, args=None, ops=None):
    req = {"method": "event", "event": "message_update", "stream": {"seq": seq, "base": base}}
    if ops is not None:
        req["stream"]["ops"] = ops
    if args is not None:
        req["args"] = args
    return req


def extension():
    return Extension.__new__(Extension)


def prepare(ext):
    ext._event_replicas = {}
    return ext


def test_a_delta_applies_to_the_last_payload_and_the_handler_gets_its_own_copy():
    ext = prepare(extension())
    first = request(1, args={"text": "ab", "list": [1], "nested": {"k": "v"}})
    ext._apply_event_stream(first)
    assert "_stream_error" not in first
    first["args"]["text"] = "edited"
    first["args"]["nested"]["k"] = "edited"
    ops = [
        {"op": "append", "path": ["text"], "value": "cd"},
        {"op": "set", "path": ["list", 1], "value": 2},
        {"op": "set", "path": ["nested", "new"], "value": None},
        {"op": "set", "path": ["added"], "value": {"x": [True]}},
    ]
    second = request(2, base=1, ops=ops)
    ext._apply_event_stream(second)
    assert "_stream_error" not in second
    assert second["args"] == {"text": "abcd", "list": [1, 2], "nested": {"k": "v", "new": None}, "added": {"x": [True]}}
    assert list(second["args"]) == ["text", "list", "nested", "added"]


@pytest.mark.parametrize(
    "name, base, ops, want",
    [
        ("wrong base", 7, [], "delta applies to payload 7"),
        ("append to a number", 1, [{"op": "append", "path": ["list", 0], "value": "x"}], "append to a non-string"),
        ("missing member", 1, [{"op": "set", "path": ["no", "where"], "value": 1}], "bad path"),
        ("array past the end", 1, [{"op": "set", "path": ["list", 9], "value": 1}], "bad path"),
        ("unknown operation", 1, [{"op": "del", "path": ["text"], "value": 1}], "unknown operation"),
        ("ops not a list", 1, {}, "ops are not a list"),
    ],
)
def test_a_delta_for_another_payload_is_an_error_and_drops_the_replica(name, base, ops, want):
    ext = prepare(extension())
    ext._apply_event_stream(request(1, args={"text": "x", "list": [1]}))
    bad = request(2, base=base, ops=ops)
    ext._apply_event_stream(bad)
    assert want in bad.get("_stream_error", ""), name
    after = request(3, base=1, ops=[])
    ext._apply_event_stream(after)
    assert "_stream_error" in after, name + ": the replica was kept after a failure"


def test_event_names_have_separate_payloads():
    ext = prepare(extension())
    ext._apply_event_stream(request(1, args={"a": "x"}))
    other = request(9, args={"b": "y"})
    other["event"] = "tool_execution_update"
    ext._apply_event_stream(other)
    patch = request(2, base=1, ops=[{"op": "append", "path": ["a"], "value": "z"}])
    ext._apply_event_stream(patch)
    assert patch["args"] == {"a": "xz"}


def test_helpers_share_strings_and_copy_containers():
    value = {"a": [1, {"b": "c"}]}
    copy = _clone_stream_value(value)
    assert copy == value and copy is not value and copy["a"] is not value["a"]
    assert _apply_stream_ops(value, [{"op": "set", "path": [], "value": 5}]) == 5
