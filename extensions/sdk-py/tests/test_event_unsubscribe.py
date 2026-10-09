"""``pi.on`` returns an unsubscribe function (packages/coding-agent/src/core/extensions/types.ts:1558 ``on(...): () => void``).

Before the extension connects the handler is a declaration of the register frame and unsubscribing removes it; after it
connects the SDK tells the host with ``event.subscribe`` / ``event.unsubscribe``, as the Go SDK's ``OnEvent`` does
(extensions/sdk/extension.go OnEvent).
"""

from __future__ import annotations

import threading
from typing import Any

from test_extension_api_099 import _drive, _host

import pig_sdk


def test_unsubscribe_before_connect_removes_the_declaration() -> None:
    ext = pig_sdk.Extension("py-unsub-pre")
    keep = ext.on_event("turn_start", lambda ctx, event: None)
    drop = ext.on_event("turn_end", lambda ctx, event: None)
    drop()
    drop()  # idempotent
    host = _host(ext)
    try:
        assert callable(keep)
        assert [(h["event"], h["handler_id"]) for h in host.register["handlers"]] == [("turn_start", 1)]
        # A removed declaration's id is not reused by a later subscription.
        late: dict[str, Any] = {}

        def subscribe() -> None:
            late["unsubscribe"] = ext.on_event("agent_end", lambda ctx, event: None)

        thread = threading.Thread(target=subscribe)
        thread.start()
        call = host.read()
        assert call["type"] == "call" and call["call"]["method"] == "event.subscribe"
        assert call["call"]["args"] == {"event": "agent_end", "handlerId": 3}
        host.answer(call, {})
        thread.join(timeout=5)
    finally:
        host.close()


def test_subscribe_and_unsubscribe_after_connect_are_host_calls() -> None:
    ext = pig_sdk.Extension("py-unsub-live")
    host = _host(ext)
    try:
        box: dict[str, Any] = {}

        def subscribe() -> None:
            box["unsubscribe"] = ext.on_event("turn_start", lambda ctx, event: None)

        thread = threading.Thread(target=subscribe)
        thread.start()
        subscribed = host.read()
        assert subscribed["call"]["method"] == "event.subscribe"
        assert subscribed["call"]["args"] == {"event": "turn_start", "handlerId": 1}
        host.answer(subscribed, {})
        thread.join(timeout=5)

        def unsubscribe_twice() -> None:
            box["unsubscribe"]()
            box["unsubscribe"]()

        thread = threading.Thread(target=unsubscribe_twice)
        thread.start()
        unsubscribed = host.read()
        assert unsubscribed["call"]["method"] == "event.unsubscribe"
        assert unsubscribed["call"]["args"] == {"event": "turn_start", "handlerId": 1}
        host.answer(unsubscribed, {})
        thread.join(timeout=5)
        assert not thread.is_alive()  # the second unsubscribe sent nothing: it would block on a call nobody answers
    finally:
        host.close()


def test_project_trust_registration_returns_its_unsubscribe() -> None:
    ext = pig_sdk.Extension("py-unsub-trust")
    unsubscribe = ext.on_project_trust(lambda ctx, event: None)
    unsubscribe()
    host = _host(ext)
    try:
        assert host.register["handlers"] == []
    finally:
        host.close()
