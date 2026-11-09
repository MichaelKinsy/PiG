import pytest

from pig_sdk import HostCallError
from pig_sdk.child_session import AgentSession


class Host:
    """The extension side of one child session: records each host call and answers it from ``replies`` by member name."""

    def __init__(self, replies=None):
        self.calls = []
        self.replies = replies or {}

    def _call(self, method, args):
        self.calls.append((method, args))
        reply = self.replies.get(args.get("op") or method)
        if isinstance(reply, Exception):
            raise reply
        return {"result": reply}


def session_with(replies=None):
    host = Host(replies)
    return AgentSession(host, "c1"), host


def test_getters_are_live_host_reads():
    session, host = session_with({"sessionId": "s-1", "sessionFile": None, "model": {"id": "echo"}, "thinkingLevel": "high", "isIdle": True,
                                  "sessionName": None, "getActiveToolNames": ["a"], "systemPrompt": "p"})
    assert session.session_id == "s-1"
    assert session.session_file is None
    assert session.model == {"id": "echo"}
    assert session.thinking_level == "high"
    assert session.is_idle is True
    assert session.session_name is None
    assert session.active_tools == ["a"]
    assert session.system_prompt == "p"
    assert host.calls[0] == ("childSession.op", {"id": "c1", "op": "sessionId"})
    host.replies["sessionId"] = "s-2"
    assert session.session_id == "s-2"


def test_changes_send_pis_member_arguments():
    session, host = session_with({"clearQueue": {"steering": ["x"], "followUp": None}})
    session.set_steering_mode("all")
    session.set_model({"provider": "p", "id": "m"}, persist=True)
    session.cycle_model("backward")
    assert session.clear_queue() == {"steering": ["x"], "followUp": []}
    session.compact()
    session.navigate_tree("e1", summarize=True)
    session.execute_bash("echo hi", exclude_from_context=True)
    session.send_user_message("hello")
    session.follow_up("later")
    assert host.calls[0] == ("childSession.op", {"id": "c1", "op": "setSteeringMode", "args": {"mode": "all"}})
    assert host.calls[1] == ("childSession.opAsync", {"id": "c1", "op": "setModel", "args": {"model": {"provider": "p", "id": "m"}, "persist": True}})
    assert host.calls[2] == ("childSession.op", {"id": "c1", "op": "cycleModel", "args": {"direction": "backward", "persist": False}})
    assert host.calls[4] == ("childSession.opAsync", {"id": "c1", "op": "compact", "args": {}})
    assert host.calls[5] == ("childSession.opAsync", {"id": "c1", "op": "navigateTree", "args": {"targetId": "e1", "summarize": True}})
    assert host.calls[6] == ("childSession.opAsync", {"id": "c1", "op": "executeBash", "args": {"command": "echo hi", "excludeFromContext": True}})
    assert host.calls[7] == ("childSession.opAsync", {"id": "c1", "op": "sendUserMessage", "args": {"content": "hello", "options": {}}})
    assert host.calls[8] == ("childSession.queue", {"id": "c1", "behavior": "followUp", "text": "later"})


def test_host_errors_propagate():
    session, _ = session_with({"reload": HostCallError("busy")})
    with pytest.raises(HostCallError):
        session.reload()
