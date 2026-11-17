"""tool_renderers registers the tool again with its renderers: the tool keeps its prepare_loadout and reaches_tools hooks, which the host asks the extension for (Pi's registerTool takes renderCall, prepareLoadout and reachesTools in one definition)."""

import pig_sdk
from test_request_lifetime import connected_extension
from test_tool_dispatch import request


def test_tool_renderers_keeps_the_loadout_and_reach_hooks_of_the_tool():
    ext = pig_sdk.Extension("py-renderers-hooks")
    ext.register_tool(pig_sdk.ToolDefinition(
        name="finder", label="Finder", description="Finds", parameters={"type": "object"}, execute=lambda ctx, args: "ok",
        prepare_loadout=lambda loadout: {"descriptions": {"finder": "Finds, as the hook says"}},
        reaches_tools=lambda args: ("docs",),
    ))
    ext.tool_renderers("finder", render_call=lambda ctx, args, render, width: ["finder"])
    declaration = next(tool for tool in ext._tools if tool["name"] == "finder")
    assert declaration["prepares_loadout"] is True and declaration["reaches_tools"] is True and declaration["renders_call"] is True
    with connected_extension(ext) as (send, receive):
        send(request("r1", "tool_prepare_loadout", "finder", {"declared": [], "callable": [], "registered": []}))
        assert receive("response")["response"] == {"result": {"descriptions": {"finder": "Finds, as the hook says"}}, "error": None}
        send(request("r2", "tool_reaches_tools", "finder", {}))
        assert receive("response")["response"] == {"result": ["docs"], "error": None}
