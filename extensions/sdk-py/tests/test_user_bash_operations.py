"""A user_bash handler's ``{operations}`` stays in the extension (types.ts UserBashEventResult, core/tools/bash.ts BashOperations).

The reply names the object by handle; the host's ``user_bash_exec`` runs its ``exec`` with the command, cwd, timeout and env while every
``on_data`` chunk travels as a ``tool_update`` before the answer; the host's release drops the object.
"""

import base64

import pig_sdk
from test_request_lifetime import connected_extension


class Operations:
    def __init__(self):
        self.options = None

    def exec(self, command, cwd, options):
        self.options = options
        options.on_data(b"one")
        options.on_data(bytes([0xFF, 0x00]))
        if command == "reject":
            raise RuntimeError("rejected: " + cwd)
        return 5 if command != "none" else None


def exec_request(request_id, command):
    return {"type": "request", "id": request_id, "request": {"method": "user_bash_exec", "tool": "bash-1", "args": {"command": command, "cwd": "/w", "timeout": 1.5, "env": {"K": "V"}}}}


def run_exec(send, receive, request_id, command):
    send(exec_request(request_id, command))
    updates = []
    while True:
        frame = receive(None)
        if frame["type"] == "notify" and frame["notify"]["method"] == "tool_update":
            updates.append(frame["notify"]["args"])
        elif frame["type"] == "response" and frame["id"] == request_id:
            return updates, frame["response"]


def test_operations_stay_in_the_extension_until_the_host_releases_them():
    ext = pig_sdk.Extension("user-bash")
    operations = Operations()
    ext.on_event("user_bash", lambda ctx, data: {"operations": operations})
    with connected_extension(ext) as (send, receive):
        send({"type": "request", "id": "u1", "request": {"method": "event", "event": "user_bash", "handler_id": 1, "args": {"type": "user_bash", "command": "x", "excludeFromContext": False, "cwd": "/w"}}})
        reply = receive("response")["response"]
        assert reply.get("error") is None and reply["result"] == {"operations": {"handle": "bash-1"}}

        updates, answer = run_exec(send, receive, "e1", "run")
        assert answer["result"] == {"exitCode": 5}
        assert [update["result"]["data"] for update in updates] == [base64.b64encode(b"one").decode(), base64.b64encode(bytes([0xFF, 0x00])).decode()]
        assert all(update["request_id"] == "e1" for update in updates)
        assert operations.options.timeout == 1.5 and operations.options.env == {"K": "V"} and operations.options.signal is not None

        _, answer = run_exec(send, receive, "e2", "none")
        assert answer["result"] == {"exitCode": None}
        _, answer = run_exec(send, receive, "e3", "reject")
        assert answer["error"]["message"] == "rejected: /w"

        send({"type": "notify", "notify": {"method": "bash_operations_release", "args": {"handle": "bash-1"}}})
        _, answer = run_exec(send, receive, "e4", "run")
        assert answer["error"]["message"] == "unknown bash operations: bash-1"


def test_replies_without_an_exec_function_name_no_handle():
    # runner.ts isUserBashEventResult: operations needs a function exec, and exactly one of operations and result is valid.
    ext = pig_sdk.Extension("user-bash")
    for value in ({"operations": None}, {"operations": {}}, {"operations": {"exec": 1}}, {"result": {"output": "x"}}):
        assert pig_sdk.user_bash._user_bash_operations_reply(ext._bash_operations, value) is None
    assert ext._bash_operations._objects == {}
    both = {"operations": Operations(), "result": {"output": "x"}}
    assert pig_sdk.user_bash._user_bash_operations_reply(ext._bash_operations, both) is None
    assert ext._bash_operations._objects == {}
    mapping = {"operations": {"exec": lambda command, cwd, options: 0}}
    assert pig_sdk.user_bash._user_bash_operations_reply(ext._bash_operations, mapping) == {"operations": {"handle": "bash-1"}}
