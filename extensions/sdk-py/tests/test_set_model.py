"""``Context.set_model``: Pi's ``pi.setModel(model)`` takes the model object; the string form resolves the name first (``resolveModel``) and sets the object."""

from pig_sdk import Context


class Host:
    """The context side of ``set_model``: records each host call and answers it from ``replies`` by method."""

    def __init__(self, replies):
        self.calls = []
        self.replies = replies

    def _call(self, method, args):
        self.calls.append((method, args))
        reply = self.replies[method]
        if isinstance(reply, Exception):
            raise reply
        return {"result": reply}


def test_object_is_sent_as_given():
    model = {"id": "chat-1", "provider": "acme", "api": "openai-completions", "baseUrl": "http://gateway/v1", "contextWindow": 4321, "extra": {"kept": True}}
    host = Host({"setModel": {"success": True}})
    assert Context.set_model(host, model) == (True, "")
    assert host.calls == [("setModel", {"model": model})]
    assert host.calls[0][1]["model"] is model


def test_object_refused_or_failed():
    host = Host({"setModel": {"success": False}})
    assert Context.set_model(host, {"id": "m", "provider": "nokey"}) == (False, "")
    host = Host({"setModel": {"success": False, "error": "No API key for nokey/m"}})
    assert Context.set_model(host, {"id": "m", "provider": "nokey"}) == (False, "No API key for nokey/m")


def test_string_resolves_the_name_and_sets_the_object_it_got():
    resolved = {"id": "chat-1", "provider": "acme", "contextWindow": 4321}
    host = Host({"resolveModel": {"model": resolved}, "setModel": {"success": True}})
    assert Context.set_model(host, "acme/chat-1") == (True, "")
    assert host.calls == [("resolveModel", {"spec": "acme/chat-1"}), ("setModel", {"model": resolved})]


def test_string_resolve_error_is_the_answer_and_nothing_is_set():
    host = Host({"resolveModel": {"error": "model switching not configured"}, "setModel": {"success": True}})
    assert Context.set_model(host, "acme/chat-1") == (False, "model switching not configured")
    assert host.calls == [("resolveModel", {"spec": "acme/chat-1"})]


def test_string_resolve_failure_raises_and_nothing_is_set():
    host = Host({"resolveModel": RuntimeError("connection closed"), "setModel": {"success": True}})
    try:
        Context.set_model(host, "acme/chat-1")
    except RuntimeError as exc:
        assert str(exc) == "connection closed"
    else:
        raise AssertionError("set_model returned after the resolve call failed")
    assert [method for method, _ in host.calls] == ["resolveModel"]
