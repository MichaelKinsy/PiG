"""``Value.check`` and ``Value.errors`` run the refinements of a schema by answering the host's ``call`` replies (see ``pig_sdk/type_engine.py`` and
``internal/typebox/engine/value_check.go``). The host is replaced by a scripted transport: the replies are fixed, the requests are recorded, and the tests assert what the SDK ran and sent. The
real walk, and its agreement with TypeBox's, is ``go test ./test/extension-conformance -run TestConformance_RefinedValueRunsRefinements``."""
import pytest

import pig_sdk
from pig_sdk import host_helpers


class Transport:
    """Answers each ``type.engine`` request with the next scripted reply and keeps the requests (the answers list is copied by the SDK, so each request shows the list it was sent with)."""

    def __init__(self, replies):
        self.replies = list(replies)
        self.requests = []

    def __call__(self, method, args):
        assert method == "type.engine"
        self.requests.append(args)
        return self.replies.pop(0)


def install(monkeypatch, replies):
    transport = Transport(replies)
    monkeypatch.setattr(host_helpers, "_helper_call", transport)
    return transport


def call(id, message, value):
    return {"call": {"id": id, "message": message, "value": value}}


def probe(log, error=lambda value: f"bad {value}"):
    """Two refinements: id 0 on the "a" member, id 1 on the "b" member (the order the wire encoder lists them in)."""

    def check_a(value):
        log.append(("check", "a", value))
        return True

    def check_b(value):
        log.append(("check", "b", value))
        return False

    def error_b(value):
        log.append(("error", "b", value))
        return error(value)

    return pig_sdk.Type.object({"a": pig_sdk.Type.refine(pig_sdk.Type.string(), check_a), "b": pig_sdk.Type.refine(pig_sdk.Type.integer(), check_b, error_b)})


def refinement_ids(request):
    found = []

    def walk(node):
        if isinstance(node, dict):
            if node.get("\x00") == "f":
                found.append(node["i"])
            for member in node.values():
                walk(member)
        elif isinstance(node, list):
            for member in node:
                walk(member)

    walk(request["args"][0])
    return found


def test_check_answers_every_call_once_in_order_and_replays_the_answers(monkeypatch):
    log = []
    schema = probe(log)
    transport = install(monkeypatch, [call(0, False, "x"), call(1, False, 5), call(1, True, 5), {"value": False}])

    assert pig_sdk.Value.check(schema, {"a": "x", "b": 5}) is False

    assert log == [("check", "a", "x"), ("check", "b", 5), ("error", "b", 5)]
    assert [request["member"] for request in transport.requests] == ["Value.Check"] * 4
    assert [request["args"][2] for request in transport.requests] == [[], [True], [True, False], [True, False, "bad 5"]]
    assert len({repr(request["args"][:2]) for request in transport.requests}) == 1
    assert request_value(transport.requests[0]) == {"a": "x", "b": 5}
    assert refinement_ids(transport.requests[0]) == [0, 1]
    assert transport.replies == []


def request_value(request):
    return request["args"][1]


def test_check_returns_the_host_result_as_a_bool(monkeypatch):
    schema = probe([])
    install(monkeypatch, [{"value": True}])
    assert pig_sdk.Value.check(schema, 1) is True


def test_a_check_answers_by_truthiness_and_a_missing_error_function_is_refine_error(monkeypatch):
    refined = pig_sdk.Type.refine(pig_sdk.Type.array(pig_sdk.Type.integer()), lambda value: value)
    transport = install(monkeypatch, [call(0, False, []), call(0, False, [1]), call(0, True, []), {"value": True}])

    assert pig_sdk.Value.check(refined, []) is True

    assert [request["args"][2] for request in transport.requests] == [[], [False], [False, True], [False, True, "Refine Error"]]


def test_the_value_of_a_call_is_decoded_as_plain_json(monkeypatch):
    seen = []
    refined = pig_sdk.Type.refine(pig_sdk.Type.object({}), lambda value: seen.append(value) is None)
    install(monkeypatch, [call(0, False, {"z": [1, {"y": None}], "a": 1.5}), {"value": True}])

    pig_sdk.Value.check(refined, {})

    assert seen == [{"z": [1, {"y": None}], "a": 1.5}]
    assert list(seen[0]) == ["z", "a"]


def test_errors_builds_the_error_list_in_the_hosts_order(monkeypatch):
    log = []
    schema = probe(log)
    failures = [
        {"keyword": "~refine", "schemaPath": "#/properties/b", "instancePath": "/b", "params": {"z": 1, "a": [2]}, "message": "bad 5"},
        {"keyword": "type", "schemaPath": "#", "instancePath": "", "params": {}, "message": "must be object"},
    ]
    transport = install(monkeypatch, [call(1, False, 5), call(1, True, 5), {"value": failures}])

    errors = pig_sdk.Value.errors(schema, {"a": "x", "b": 5})

    assert [request["member"] for request in transport.requests] == ["Value.Errors"] * 3
    assert [request["args"][2] for request in transport.requests] == [[], [False], [False, "bad 5"]]
    assert errors == [
        pig_sdk.ValueCheckError("~refine", "#/properties/b", "/b", {"z": 1, "a": [2]}, "bad 5"),
        pig_sdk.ValueCheckError("type", "#", "", {}, "must be object"),
    ]
    assert errors[0].keyword == "~refine" and errors[0].schema_path == "#/properties/b" and errors[0].instance_path == "/b" and errors[0].message == "bad 5"
    assert list(errors[0].params) == ["z", "a"]
    assert log == [("check", "b", 5), ("error", "b", 5)]


def test_errors_of_a_passing_value_is_an_empty_list(monkeypatch):
    install(monkeypatch, [{"value": []}])
    assert pig_sdk.Value.errors(probe([]), {}) == []


def test_a_raising_check_aborts_the_call_with_the_original_exception(monkeypatch):
    boom = RuntimeError("kaboom")
    ran = []

    def check(value):
        ran.append(value)
        raise boom

    refined = pig_sdk.Type.refine(pig_sdk.Type.string(), check)
    transport = install(monkeypatch, [call(0, False, "x"), {"value": True}])

    with pytest.raises(RuntimeError) as failure:
        pig_sdk.Value.check(refined, "x")

    assert failure.value is boom
    assert ran == ["x"]
    assert len(transport.requests) == 1
    assert len(transport.replies) == 1


def test_a_raising_error_function_aborts_after_the_calls_made_so_far(monkeypatch):
    log = []
    boom = KeyError("kaboom")

    def refuse(value):
        raise boom

    schema = probe(log, error=refuse)
    transport = install(monkeypatch, [call(0, False, "x"), call(1, False, 5), call(1, True, 5), {"value": []}])

    with pytest.raises(KeyError) as failure:
        pig_sdk.Value.errors(schema, {"a": "x", "b": 5})

    assert failure.value is boom
    assert log == [("check", "a", "x"), ("check", "b", 5), ("error", "b", 5)]
    assert [request["args"][2] for request in transport.requests] == [[], [True], [True, False]]
    assert len(transport.replies) == 1


def test_an_error_reply_fails_the_call(monkeypatch):
    transport = install(monkeypatch, [call(0, False, "x"), {"error": {"name": "TypeError", "message": "the value cannot be written as JSON: boom"}}, {"value": True}])
    refined = pig_sdk.Type.refine(pig_sdk.Type.string(), lambda value: True)

    with pytest.raises(ValueError) as failure:
        pig_sdk.Value.check(refined, "x")

    assert str(failure.value) == "the value cannot be written as JSON: boom"
    assert len(transport.requests) == 2
    assert len(transport.replies) == 1


def test_an_error_function_must_return_a_string(monkeypatch):
    refined = pig_sdk.Type.refine(pig_sdk.Type.string(), lambda value: False, lambda value: 7)
    transport = install(monkeypatch, [call(0, False, "x"), call(0, True, "x"), {"value": []}])

    with pytest.raises(TypeError):
        pig_sdk.Value.errors(refined, "x")

    assert len(transport.requests) == 2


def test_a_schema_without_refinements_is_one_request(monkeypatch):
    transport = install(monkeypatch, [{"value": True}])
    assert pig_sdk.Value.check(pig_sdk.Type.string(), "x") is True
    assert transport.requests == [{"member": "Value.Check", "args": [{"\x00": "o", "h": {"~kind": "String"}, "e": {"type": "string"}}, "x", []]}]
