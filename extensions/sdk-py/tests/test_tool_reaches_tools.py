"""Pi 24382bed types.ts reachesTools: the host asks the extension that defines the hook with tool_reaches_tools; the answer is "all" or the words."""

import pig_sdk
from test_request_lifetime import connected_extension
from test_tool_dispatch import request


def test_reaches_tools_runs_the_hook_for_the_host():
    seen = []

    def reaches(args):
        seen.append(args)
        if args.get("fail"):
            raise RuntimeError("reach exploded")
        if args.get("all"):
            return "all"
        if args.get("none"):
            return ()
        if args.get("bad"):
            return 7
        return ("docs", "search")

    ext = pig_sdk.Extension("py-reach")
    ext.register_tool(pig_sdk.ToolDefinition(
        name="finder", label="Finder", description="Finds", parameters={"type": "object"}, execute=lambda ctx, args: "ok",
        namespace={"name": "mcp__docs", "aliases": ["docs"]}, reaches_tools=reaches,
    ))
    ext.register_tool(pig_sdk.ToolDefinition(name="plain", label="Plain", description="Plain", parameters={"type": "object"}, execute=lambda ctx, args: "ok"))
    declarations = {tool["name"]: tool for tool in ext._tools}
    assert declarations["finder"]["reaches_tools"] is True
    assert declarations["finder"]["namespace"] == {"name": "mcp__docs", "aliases": ["docs"]}
    assert "reaches_tools" not in declarations["plain"]
    with connected_extension(ext) as (send, receive):
        for req_id, args, want in (("r1", {"q": "x"}, ["docs", "search"]), ("r2", {"all": True}, "all"), ("r3", {"none": True}, [])):
            send(request(req_id, "tool_reaches_tools", "finder", args))
            assert receive("response")["response"] == {"result": want, "error": None}
        send(request("r4", "tool_reaches_tools", "finder", {"fail": True}))
        assert receive("response")["response"]["error"] == {"message": "reach exploded"}
        send(request("r5", "tool_reaches_tools", "finder", {"bad": True}))
        assert receive("response")["response"]["error"] is not None
        send(request("r6", "tool_reaches_tools", "plain", {}))
        assert receive("response")["response"]["error"] is not None
        send(request("r7", "tool_reaches_tools", "missing", {}))
        assert receive("response")["response"]["error"] is not None
    assert seen == [{"q": "x"}, {"all": True}, {"none": True}, {"fail": True}, {"bad": True}]


def test_get_all_tools_reaches_tools_runs_the_hook_through_the_host():
    """Pi 1.2.0 types.ts:2122-2130 picks reachesTools into ToolInfo and agent-session.ts:1507 copies the definition's hook. The host
    marks such a tool ``"reachesTools": True``; get_all_tools turns the mark into a function that runs the hook with the
    tool.reachesTools call, a tool without the mark has no key, and the host's error is the hook's raise."""
    tools = [
        {"name": "finder", "description": "d", "parameters": {}, "exposure": "direct", "reachesTools": True, "sourceInfo": {"path": "/f"}},
        {"name": "plain", "description": "d", "parameters": {}, "exposure": "direct", "sourceInfo": {"path": "/p"}},
    ]
    calls = []

    def call(method, args=None, request_id=""):
        calls.append((method, args))
        if method == "getAllTools":
            return {"result": {"tools": [dict(tool) for tool in tools]}}
        if args["input"].get("fail"):
            raise pig_sdk.HostCallError("reach exploded", "")
        return {"result": "all" if args["input"].get("all") else ["docs", "search"]}

    ext = pig_sdk.Extension("py-info-reach")
    ext._call = call  # type: ignore[method-assign]
    finder, plain = pig_sdk.Context(extension=ext).get_all_tools()
    assert list(finder) == ["name", "description", "parameters", "exposure", "reachesTools", "sourceInfo"]
    assert callable(finder["reachesTools"])
    assert "reachesTools" not in plain
    assert finder["reachesTools"]({"q": 1}) == ["docs", "search"]
    assert finder["reachesTools"]({"all": True}) == "all"
    assert calls[1:] == [("tool.reachesTools", {"tool": "finder", "input": {"q": 1}}), ("tool.reachesTools", {"tool": "finder", "input": {"all": True}})]
    try:
        finder["reachesTools"]({"fail": True})
    except pig_sdk.HostCallError as error:
        assert "reach exploded" in str(error)
    else:
        raise AssertionError("a failed tool.reachesTools call returned")
