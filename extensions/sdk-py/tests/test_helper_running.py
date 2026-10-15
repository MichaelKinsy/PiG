import pytest

from pig_sdk import host_helpers


class _Running:
    def __init__(self, name, served=""):
        self.name = name
        self._served_socket = served

    def _call(self, method, args):
        return {"result": {"from": self.name}}


# A packed cell runs several extensions in one process. A helper called with no Context and no socket of its own uses a running extension's connection, and
# must keep working when the first one to start has stopped.
def test_helper_call_survives_the_first_running_extension_stopping(monkeypatch):
    monkeypatch.delenv("PIG_EXT_SOCKET", raising=False)
    first, second = _Running("first", "/s/e-0.sock"), _Running("second", "/s/e-1.sock")
    host_helpers._State.running[:] = [first, second]
    try:
        assert host_helpers._helper_call("x", {}) == {"from": "first"}
        host_helpers._State.running.remove(first)
        assert host_helpers._helper_call("x", {}) == {"from": "second"}
    finally:
        host_helpers._State.running[:] = []


# The runner names a member's socket (PIG_EXT_SOCKET) while that member's factory runs. The host accepts a packed cell's members in order and serves a
# connection only after the members before it: a helper called by the first member's factory that rode the second member's registered connection waited for
# an answer the host would not send until the first member had connected (the conformance rust-packed hang).
def test_helper_named_for_a_socket_does_not_use_another_members_connection(monkeypatch, tmp_path):
    second = _Running("second", "/s/e-1.sock")
    host_helpers._State.running[:] = [second]
    try:
        monkeypatch.setenv("PIG_EXT_SOCKET", "/s/e-1.sock")
        assert host_helpers._helper_call("x", {}) == {"from": "second"}
        # Nothing listens on the first member's socket: a helper that dials it fails, where one that took the second member's connection would wait for the host.
        monkeypatch.setenv("PIG_EXT_SOCKET", str(tmp_path / "e-0.sock"))
        with pytest.raises(OSError):
            host_helpers._helper_call("x", {})
        # An extension with no socket of its own answers whichever socket is named.
        host_helpers._State.running[:] = [_Running("fused")]
        assert host_helpers._helper_call("x", {}) == {"from": "fused"}
    finally:
        host_helpers._State.running[:] = []
        host_helpers._State.dialled.clear()
