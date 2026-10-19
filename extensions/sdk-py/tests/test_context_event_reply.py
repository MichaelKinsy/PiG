"""Pi core/extensions/runner.ts emitContext takes ``handlerResult?.messages``, or ``event.messages`` when the handler edited that
list in place, and ``sameMessages`` compares the lists by message identity.

The host holds the list it sent, so a reply for a list the handler left as received, with no message edited, is the
``_pigContextUnchanged`` report alone. An edit in place keeps identity and the reply carries the edited list; a replacement
carries its list and reports a change.
"""

import pig_sdk
from test_request_lifetime import connected_extension

MESSAGES = [{"role": "user", "content": "one", "timestamp": 0}, {"role": "user", "content": "two", "timestamp": 0}]


def edit_in_place(ctx, data):
    data["messages"][0]["content"] = "edited"


def same_list(ctx, data):
    return {"messages": list(data["messages"])}


def replacement(ctx, data):
    return {"messages": [dict(data["messages"][0]), data["messages"][1]]}


def test_the_reply_omits_a_list_left_as_received_and_carries_any_other():
    ext = pig_sdk.Extension("context-reply")
    for name, handler in [("noop", lambda ctx, data: None), ("same", same_list), ("edit", edit_in_place), ("replace", replacement)]:
        ext.on_event("context", handler)
    ext.on_event("context_with_system", lambda ctx, data: None)
    replies = {}
    with connected_extension(ext) as (send, receive):
        for handler_id, name in enumerate(["noop", "same", "edit", "replace"], start=1):
            send({"type": "request", "id": name, "request": {"method": "event", "event": "context", "handler_id": handler_id, "args": {"type": "context", "messages": MESSAGES}}})
            replies[name] = receive("response")["response"]["result"]
        send({"type": "request", "id": "full", "request": {"method": "event", "event": "context_with_system", "handler_id": 5, "args": {"type": "context_with_system", "messages": MESSAGES}}})
        replies["full"] = receive("response")["response"]["result"]
    assert replies["noop"] == {"_pigContextUnchanged": True}
    assert replies["same"] == {"_pigContextUnchanged": True}
    assert replies["full"] == {"_pigContextUnchanged": True}
    assert replies["edit"] == {"messages": [{**MESSAGES[0], "content": "edited"}, MESSAGES[1]], "_pigContextUnchanged": True}
    assert replies["replace"] == {"messages": MESSAGES, "_pigContextUnchanged": False}
