"""Pi core/extensions/types.ts:1372-1374: ``isToolCallEventType(toolName, event)`` is ``event.toolName === toolName``."""

from __future__ import annotations

import pig_sdk

NAMES = ["bash", "read", "my_tool", "bashful", "Bash", ""]


def test_the_guard_accepts_exactly_the_events_own_tool_name() -> None:
    for tool in NAMES:
        event = {"type": "tool_call", "toolCallId": "c1", "toolName": tool, "input": {}}
        for name in NAMES:
            assert pig_sdk.is_tool_call_event_type(name, event) is (name == tool), (name, tool)


def test_a_payload_without_a_string_tool_name_is_a_call_of_no_tool() -> None:
    for name in NAMES:
        for payload in ({"type": "tool_call"}, {"toolName": 7}, {"toolName": None}, None, [name]):
            assert pig_sdk.is_tool_call_event_type(name, payload) is False, (name, payload)
