"""Upstream 0.99.2 extension API additions in the Python SDK.

Every test runs an extension against a fake host socket and asserts the exact
wire frames of the contract in coding/extension/host/subprocess/
protocol_extension_api.go and how the SDK decodes the host's answers. Each
docstring cites the upstream line it mirrors (.upstream/v0.99.2/packages/
coding-agent/src/core/).
"""

from __future__ import annotations

from typing import Any

import pytest
from test_sdk import _write_frame
from test_sdk_surface import _Host

import pig_sdk


def _host(ext: pig_sdk.Extension, ready: dict[str, Any] | None = None) -> _Host:
    """Start the extension against a fake host whose reads fail after 15 s, so a missing frame fails the test instead of hanging it."""
    host = _Host(ext, ready)
    host.conn.settimeout(15)
    return host


def _drive(host: _Host, req_id: str, request: dict[str, Any], answers: dict[str, Any] | None = None) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    """Send a request, answer each host call from answers, and return the calls and the response frame."""
    calls: list[dict[str, Any]] = []
    host.request(req_id, request)
    while True:
        env = host.read()
        if env["type"] == "response":
            return calls, env
        if env["type"] == "call":
            calls.append(env)
            answer = (answers or {}).get(env["call"]["method"])
            if isinstance(answer, Exception):
                host.answer(env, error={"message": str(answer)})
            else:
                host.answer(env, answer)


def _command_ext(name: str, handler: Any, **ready_state: Any) -> tuple[pig_sdk.Extension, dict[str, Any]]:
    ext = pig_sdk.Extension(name)
    ext.command("go", "run", lambda ctx, _args: handler(ctx))
    return ext, {"state": ready_state} if ready_state else {}


def test_event_constants_of_the_new_events() -> None:
    """types.ts:1562 (mcp_servers_change) and 1580 (provider_stream_event)."""
    assert pig_sdk.EVENT_PROVIDER_STREAM_EVENT == "provider_stream_event"
    assert pig_sdk.EVENT_MCP_SERVERS_CHANGE == "mcp_servers_change"


def test_new_event_handlers_are_declared_and_receive_the_event() -> None:
    """types.ts:699-709, 884-890: the handlers register by event name and get the event dict."""
    seen: list[tuple[str, dict[str, Any]]] = []
    ext = pig_sdk.Extension("py-events")
    ext.on_event(pig_sdk.EVENT_PROVIDER_STREAM_EVENT, lambda ctx, event: seen.append(("stream", event)))
    ext.on_event(pig_sdk.EVENT_MCP_SERVERS_CHANGE, lambda ctx, event: seen.append(("mcp", event)))
    host = _host(ext)
    try:
        assert [(h["event"], h["can_block"]) for h in host.register["handlers"]] == [
            ("provider_stream_event", False),
            ("mcp_servers_change", False),
        ]
        stream = {"type": "provider_stream_event", "provider": "p", "api": "a", "model": "m", "data": {"k": [1]}}
        change = {"type": "mcp_servers_change", "servers": [{"name": "docs", "config": {"url": "u"}, "extensionPath": "/e"}]}
        _drive(host, "r1", {"method": "event", "event": "provider_stream_event", "handler_id": 1, "args": stream})
        _drive(host, "r2", {"method": "event", "event": "mcp_servers_change", "handler_id": 2, "args": change})
        assert seen == [("stream", stream), ("mcp", change)]
    finally:
        host.close()


# ── Tool definition fields ───────────────────────────────────────────────────


def _loadout_hook(loadout: pig_sdk.ToolLoadout) -> dict[str, Any] | None:
    return None


def test_register_tool_declares_exposure_fields() -> None:
    """types.ts:579-607: outputSchema, exposure, namespace, annotations, defaultActive, prepareLoadout."""
    ext = pig_sdk.Extension("py-exposure")
    ext.register_tool(pig_sdk.ToolDefinition(
        name="search", label="Search", description="Search", parameters={"type": "object"},
        output_schema={"type": "object", "properties": {"n": {"type": "number"}}},
        exposure="codemode",
        namespace={"name": "mcp__docs", "description": "Docs server", "instructions": "Search before reading."},
        annotations={"readOnlyHint": True, "openWorldHint": False},
        default_active=False,
        prepare_loadout=_loadout_hook,
        execute=lambda ctx, args: "ok",
    ))
    ext.register_tool(pig_sdk.ToolDefinition(
        name="plain", label="Plain", description="Plain", parameters={"type": "object"}, execute=lambda ctx, args: "ok",
    ))
    host = _host(ext)
    try:
        search, plain = host.register["tools"]
        assert search["output_schema"] == {"type": "object", "properties": {"n": {"type": "number"}}}
        assert search["exposure"] == "codemode"
        assert search["namespace"] == {"name": "mcp__docs", "description": "Docs server", "instructions": "Search before reading."}
        assert search["annotations"] == {"readOnlyHint": True, "openWorldHint": False}
        # A false defaultActive is a value, not an absent field.
        assert search["default_active"] is False
        assert search["prepares_loadout"] is True
        for key in ("output_schema", "exposure", "namespace", "annotations", "default_active", "prepares_loadout"):
            assert key not in plain, key
    finally:
        host.close()


def test_register_tool_default_active_true_is_declared() -> None:
    ext = pig_sdk.Extension("py-active")
    ext.register_tool(pig_sdk.ToolDefinition(
        name="t", label="t", description="t", parameters={"type": "object"}, default_active=True, execute=lambda ctx, args: "ok",
    ))
    host = _host(ext)
    try:
        assert host.register["tools"][0]["default_active"] is True
    finally:
        host.close()


def test_register_tool_rejects_an_unknown_exposure_before_changing_state() -> None:
    ext = pig_sdk.Extension("py-bad-exposure")
    with pytest.raises(ValueError, match="exposure"):
        ext.register_tool(pig_sdk.ToolDefinition(
            name="t", label="t", description="t", parameters={"type": "object"}, exposure="visible", execute=lambda ctx, args: "ok",  # type: ignore[arg-type]
        ))
    host = _host(ext)
    try:
        assert host.register["tools"] == []
    finally:
        host.close()


def test_live_register_tool_sends_the_exposure_fields_to_the_host() -> None:
    """loader.ts:288-296: registerTool after load refreshes the running registry with the same declaration."""
    def handler(ctx: pig_sdk.Context) -> None:
        ctx.register_tool(pig_sdk.ToolDefinition(
            name="late", label="Late", description="d", parameters={"type": "object"}, exposure="deferred",
            namespace={"name": "ns"}, execute=lambda c, a: "ok",
        ))

    ext, ready = _command_ext("py-live-tool", handler)
    host = _host(ext, ready)
    try:
        calls, _ = _drive(host, "r1", {"method": "command", "tool": "go"})
        assert [c["call"]["method"] for c in calls] == ["registerTool"]
        declaration = calls[0]["call"]["args"]
        assert declaration["exposure"] == "deferred" and declaration["namespace"] == {"name": "ns"}
    finally:
        host.close()


def test_prepare_loadout_request_runs_the_hook_with_the_loadout() -> None:
    """types.ts:541-563, 601-607 and agent-session.ts:1480-1482, 1517-1518: the hook sees declared, callable and registered tools; an unregistered name is direct and has no namespace."""
    received: list[pig_sdk.ToolLoadout] = []
    answer: list[Any] = [{"descriptions": {"read": "Read (via codemode)"}, "hiddenDeclarations": ["grep"]}]

    def hook(loadout: pig_sdk.ToolLoadout) -> Any:
        received.append(loadout)
        if isinstance(answer[0], Exception):
            raise answer[0]
        return answer[0]

    ext = pig_sdk.Extension("py-loadout")
    ext.register_tool(pig_sdk.ToolDefinition(
        name="codemode", label="c", description="c", parameters={"type": "object"}, prepare_loadout=hook, execute=lambda ctx, args: "ok",
    ))
    host = _host(ext)
    try:
        read = {"name": "read", "label": "Read", "description": "Read", "parameters": {"type": "object"}}
        grep = {"name": "grep", "label": "Grep", "description": "Grep", "parameters": {"type": "object"}}
        payload = {
            "declared": [read, grep], "callable": [read], "registered": [read, grep],
            "exposures": {"read": "direct", "grep": "deferred"}, "namespaces": {"grep": {"name": "search"}},
            "promptGuidelines": {"grep": ["Use grep for patterns.", "Quote regexes."]},
        }
        request = {"method": "tool_prepare_loadout", "tool": "codemode", "args": payload}
        _, response = _drive(host, "r1", request)
        assert response["response"] == {"result": {"descriptions": {"read": "Read (via codemode)"}, "hiddenDeclarations": ["grep"]}, "error": None}
        loadout = received[0]
        assert loadout.declared == [read, grep] and loadout.callable == [read] and loadout.registered == [read, grep]
        assert loadout.get_exposure("grep") == "deferred" and loadout.get_exposure("read") == "direct"
        assert loadout.get_exposure("missing") == "direct"
        assert loadout.get_namespace("grep") == {"name": "search"}
        assert loadout.get_namespace("read") is None and loadout.get_namespace("missing") is None
        assert loadout.get_prompt_guidelines("grep") == ["Use grep for patterns.", "Quote regexes."]
        assert loadout.get_prompt_guidelines("read") == [] and loadout.get_prompt_guidelines("missing") == []

        answer[0] = None
        _, response = _drive(host, "r2", request)
        assert response["response"] == {"result": None, "error": None}

        answer[0] = RuntimeError("hook exploded")
        _, response = _drive(host, "r3", request)
        assert response["response"]["error"] == {"message": "hook exploded"}
    finally:
        host.close()


def test_tool_result_carries_structured_content_and_is_error_unchanged() -> None:
    """agent/src/types.ts:424-446 (structuredContent, isError): the result reaches the host as returned."""
    ext = pig_sdk.Extension("py-structured")
    result = {
        "content": "text for the model",
        "structured_content": {"rows": [1, 2, 3], "ok": None},
        "is_error": True,
        "details": {"d": 1},
    }
    ext.tool("s", "s", {"type": "object"}, lambda ctx, args: result)
    host = _host(ext)
    try:
        host.request("r1", {"method": "tool_call", "tool": "s", "tool_call_id": "tc", "args": {}})
        assert host.read()["response"]["result"] == result
    finally:
        host.close()


# ── MCP servers ──────────────────────────────────────────────────────────────


def test_mcp_servers_registered_by_the_factory_travel_in_the_register_frame() -> None:
    """loader.ts:456-462, 259-262: registration during load is queued, in order, and applied when the factory succeeds; unregistering a queued server removes it."""
    ext = pig_sdk.Extension("py-mcp-load")
    ext.register_mcp_server("docs", {"url": "https://mcp.example/docs"})
    ext.register_mcp_server("wiki", {"command": "wiki-mcp", "args": ["--stdio"]})
    ext.register_mcp_server("gone", {"url": "https://mcp.example/gone"})
    ext.unregister_mcp_server("gone")
    ext.register_mcp_server("docs", {"url": "https://mcp.example/docs-v2"})
    host = _host(ext)
    try:
        # mis-port fix (red commit expected [wiki, docs-v2]): mcp-servers.ts:212-215 `servers.set` keeps the position of a replaced key, as a JavaScript Map does, so the second `docs` replaces the first in place.
        assert host.register["mcp_servers"] == [
            {"name": "docs", "config": {"url": "https://mcp.example/docs-v2"}},
            {"name": "wiki", "config": {"command": "wiki-mcp", "args": ["--stdio"]}},
        ]
    finally:
        host.close()


def test_register_mcp_server_config_must_be_json() -> None:
    ext = pig_sdk.Extension("py-mcp-json")
    with pytest.raises(TypeError, match="JSON"):
        ext.register_mcp_server("docs", {"url": object()})
    host = _host(ext)
    try:
        assert "mcp_servers" not in host.register
    finally:
        host.close()


def test_live_mcp_registration_calls_the_host_and_replicates_the_list() -> None:
    """loader.ts:456-478: after load the calls apply at once; getMcpServers lists the registry after each call."""
    listed: list[Any] = []
    server = {"name": "jira", "config": {"url": "https://mcp.example/jira"}, "extensionPath": "/ext/py"}

    def handler(ctx: pig_sdk.Context) -> None:
        listed.append(ctx.get_mcp_servers())
        ctx.register_mcp_server("jira", {"url": "https://mcp.example/jira"})
        listed.append(ctx.get_mcp_servers())
        ctx.unregister_mcp_server("jira")
        listed.append(ctx.get_mcp_servers())

    ext, ready = _command_ext("py-mcp-live", handler, mcpServers=[])
    host = _host(ext, ready)
    try:
        calls, response = _drive(host, "r1", {"method": "command", "tool": "go"}, {
            "registerMcpServer": {"servers": [server]},
            "unregisterMcpServer": {"servers": []},
        })
        assert response["response"]["error"] is None
        assert [(c["call"]["method"], c["call"]["args"]) for c in calls] == [
            ("registerMcpServer", {"name": "jira", "config": {"url": "https://mcp.example/jira"}}),
            ("unregisterMcpServer", {"name": "jira"}),
        ]
        assert [c["call"].get("parent_request_id") for c in calls] == ["r1", "r1"]
        assert listed == [[], [server], []]
    finally:
        host.close()


def test_rejected_mcp_registration_raises_and_keeps_the_list() -> None:
    """loader.ts:463-470: an invalid config or a name another extension owns throws to the extension."""
    outcome: list[Any] = []

    def handler(ctx: pig_sdk.Context) -> None:
        try:
            ctx.register_mcp_server("docs", {"url": ""})
        except pig_sdk.HostCallError as exc:
            outcome.append(str(exc))
        outcome.append(ctx.get_mcp_servers())

    kept = [{"name": "other", "config": {"url": "u"}, "extensionPath": "/e"}]
    ext, ready = _command_ext("py-mcp-reject", handler, mcpServers=kept)
    host = _host(ext, ready)
    try:
        message = 'Invalid MCP server registered by extension "/ext/py": url is required'
        _drive(host, "r1", {"method": "command", "tool": "go"}, {"registerMcpServer": RuntimeError(message)})
        assert outcome == [message, kept]
    finally:
        host.close()


def test_get_mcp_servers_reads_the_replicated_state_without_a_host_call() -> None:
    """types.ts:1839-1840: synchronous upstream, so the SDK answers from the state the host pushes."""
    first = [{"name": "a", "config": {"url": "u"}, "extensionPath": "/a"}]
    second = [{"name": "b", "config": {"command": "c"}, "extensionPath": "/b"}]
    seen: list[Any] = []
    ext = pig_sdk.Extension("py-mcp-state")
    ext.command("go", "run", lambda ctx, _args: seen.append(ctx.get_mcp_servers()))
    host = _host(ext, {"state": {"mcpServers": first}})
    try:
        calls, _ = _drive(host, "r1", {"method": "command", "tool": "go"})
        _write_frame(host.conn, {"type": "notify", "notify": {"method": "state_update", "args": {"state": {"mcpServers": second}}}})
        calls2, _ = _drive(host, "r2", {"method": "command", "tool": "go"})
        assert calls == [] and calls2 == []
        assert seen == [first, second]
    finally:
        host.close()


def test_a_registration_reply_never_replaces_a_later_state_push() -> None:
    """mcp-servers.ts:212-220: getMcpServers lists the registry as it is now. The host sends each frame in order, so a state push that follows a registration reply is newer: the reply's list must not replace it however late the calling thread wakes."""
    import time

    older = [{"name": "jira", "config": {"url": "u"}, "extensionPath": "/ext/py"}]
    newer = older + [{"name": "wiki", "config": {"url": "w"}, "extensionPath": "/other"}]
    listed: list[Any] = []
    ext, ready = _command_ext("py-mcp-order", lambda ctx: (ctx.register_mcp_server("jira", {"url": "u"}), listed.append(ctx.get_mcp_servers())), mcpServers=[])
    wait_call = ext._wait_call

    def late_wait(call_id: str, event: Any, method: str) -> dict[str, Any]:
        reply = wait_call(call_id, event, method)
        if method == "registerMcpServer":
            # Wake only after the read loop applied the state push that followed the reply.
            deadline = time.monotonic() + 10
            while ext._get_mcp_servers() != newer and time.monotonic() < deadline:
                time.sleep(0.005)
        return reply

    ext._wait_call = late_wait  # type: ignore[method-assign]
    host = _host(ext, ready)
    try:
        host.request("r1", {"method": "command", "tool": "go"})
        call = host.read()
        assert call["call"]["method"] == "registerMcpServer"
        host.answer(call, {"servers": older})
        _write_frame(host.conn, {"type": "notify", "notify": {"method": "state_update", "args": {"state": {"mcpServers": newer}}}})
        assert host.read()["type"] == "response"
        assert listed == [newer]
    finally:
        host.close()


# ── Virtual models ───────────────────────────────────────────────────────────


def _router(ctx: pig_sdk.Context, request: dict[str, Any]) -> dict[str, Any]:
    return {"model": {"provider": "anthropic", "id": "claude-x"}, "thinkingLevel": "low", "state": {"n": (request.get("state") or {}).get("n", 0) + 1}}


def test_virtual_models_registered_by_the_factory_travel_in_the_register_frame() -> None:
    """loader.ts:480-490 and virtual-models.ts:88-101: everything but route is declared."""
    ext = pig_sdk.Extension("py-vm-load")
    ext.register_virtual_model(pig_sdk.VirtualModel(
        provider="router", id="auto", name="Auto", route=_router,
        thinking_levels=["off", "low"], context_window=200000, max_tokens=8192, input=["text"],
    ))
    ext.register_virtual_model(pig_sdk.VirtualModel(provider="router", id="min", name="Min", route=_router))
    ext.register_virtual_model(pig_sdk.VirtualModel(provider="router", id="gone", name="Gone", route=_router))
    ext.unregister_virtual_model("router", "gone")
    host = _host(ext)
    try:
        assert host.register["virtual_models"] == [
            {"provider": "router", "id": "auto", "name": "Auto", "thinkingLevels": ["off", "low"], "contextWindow": 200000, "maxTokens": 8192, "input": ["text"]},
            {"provider": "router", "id": "min", "name": "Min"},
        ]
    finally:
        host.close()


def test_register_virtual_model_requires_a_callable_route() -> None:
    ext = pig_sdk.Extension("py-vm-route")
    with pytest.raises(TypeError, match="route"):
        ext.register_virtual_model(pig_sdk.VirtualModel(provider="p", id="i", name="n", route="not callable"))  # type: ignore[arg-type]


def test_live_virtual_model_registration_calls_the_host() -> None:
    """loader.ts:480-497."""
    def handler(ctx: pig_sdk.Context) -> None:
        ctx.register_virtual_model(pig_sdk.VirtualModel(provider="router", id="auto", name="Auto", route=_router, thinking_levels=["off"]))
        ctx.unregister_virtual_model("router", "auto")

    ext, ready = _command_ext("py-vm-live", handler)
    host = _host(ext, ready)
    try:
        calls, response = _drive(host, "r1", {"method": "command", "tool": "go"})
        assert response["response"]["error"] is None
        assert [(c["call"]["method"], c["call"]["args"]) for c in calls] == [
            ("registerVirtualModel", {"provider": "router", "id": "auto", "name": "Auto", "thinkingLevels": ["off"]}),
            ("unregisterVirtualModel", {"provider": "router", "id": "auto"}),
        ]
    finally:
        host.close()


def test_virtual_model_route_request_runs_the_router_in_the_extension() -> None:
    """loader.ts:485-487, virtual-models.ts:100: the router gets the request and a context, and its route is the response."""
    seen: list[dict[str, Any]] = []

    def route(ctx: pig_sdk.Context, request: dict[str, Any]) -> dict[str, Any]:
        seen.append({**request, "cancelled": request["signal"].is_set()})
        return _router(ctx, request)

    ext = pig_sdk.Extension("py-vm-route")
    ext.register_virtual_model(pig_sdk.VirtualModel(provider="router", id="auto", name="Auto", route=route))
    host = _host(ext)
    try:
        model = {"provider": "router", "id": "auto", "api": "pi-virtual"}
        args = {
            "provider": "router", "id": "auto",
            "request": {"model": model, "thinkingLevel": "low", "reason": "user", "state": {"n": 4}, "messages": [{"role": "user", "content": "hi"}]},
        }
        _, response = _drive(host, "r1", {"method": "virtual_model_route", "args": args})
        assert response["response"] == {"result": {"model": {"provider": "anthropic", "id": "claude-x"}, "thinkingLevel": "low", "state": {"n": 5}}, "error": None}
        request = seen[0]
        assert request["model"] == model and request["reason"] == "user" and request["messages"] == [{"role": "user", "content": "hi"}]
        assert request["cancelled"] is False
    finally:
        host.close()


def test_a_route_returning_the_absent_request_state_keeps_the_state() -> None:
    """virtual-models.ts:77-83: returning request.state, or undefined, keeps the current state. Before the first state request.state is undefined, which Python reads as None: the route must not store a null state."""
    def route(ctx: pig_sdk.Context, request: dict[str, Any]) -> dict[str, Any]:
        return {"model": {"provider": "anthropic", "id": "claude-x"}, "thinkingLevel": "low", "state": request.get("state")}

    ext = pig_sdk.Extension("py-vm-keep")
    ext.register_virtual_model(pig_sdk.VirtualModel(provider="router", id="auto", name="Auto", route=route))
    host = _host(ext)
    try:
        request = {"model": {"provider": "router", "id": "auto"}, "thinkingLevel": "low", "reason": "user", "messages": []}
        _, response = _drive(host, "r1", {"method": "virtual_model_route", "args": {"provider": "router", "id": "auto", "request": request}})
        assert response["response"] == {"result": {"model": {"provider": "anthropic", "id": "claude-x"}, "thinkingLevel": "low"}, "error": None}
    finally:
        host.close()


def test_virtual_model_route_errors_are_the_requests_error() -> None:
    """virtual-models.ts:100: a router that throws, returns nothing usable, or names no registered model fails only that request."""
    def bad(ctx: pig_sdk.Context, request: dict[str, Any]) -> Any:
        return "not a route"

    def boom(ctx: pig_sdk.Context, request: dict[str, Any]) -> Any:
        raise RuntimeError("router exploded")

    ext = pig_sdk.Extension("py-vm-errors")
    ext.register_virtual_model(pig_sdk.VirtualModel(provider="r", id="bad", name="Bad", route=bad))
    ext.register_virtual_model(pig_sdk.VirtualModel(provider="r", id="boom", name="Boom", route=boom))
    host = _host(ext)
    try:
        def route(model_id: str) -> dict[str, Any]:
            request = {"model": {}, "thinkingLevel": "off", "reason": "user", "messages": []}
            _, frame = _drive(host, "r-" + model_id, {"method": "virtual_model_route", "args": {"provider": "r", "id": model_id, "request": request}})
            return frame["response"]

        assert "route" in route("bad")["error"]["message"]
        assert route("boom")["error"] == {"message": "router exploded"}
        assert "unknown virtual model" in route("nope")["error"]["message"]
    finally:
        host.close()


# ── getSettings ──────────────────────────────────────────────────────────────


def test_get_settings_reads_the_replicated_state() -> None:
    """types.ts:1712, loader.ts:411-413: settings are synchronous upstream, so the SDK answers from state."""
    settings = {"theme": "dark", "compaction": {"enabled": True, "reserveTokens": 4096}, "defaultTools": ["+grep"]}
    seen: list[Any] = []
    ext = pig_sdk.Extension("py-settings")
    ext.command("go", "run", lambda ctx, _args: seen.append(ctx.get_settings()))
    host = _host(ext, {"state": {"settings": settings}})
    try:
        calls, _ = _drive(host, "r1", {"method": "command", "tool": "go"})
        newer = {"theme": "light"}
        _write_frame(host.conn, {"type": "notify", "notify": {"method": "state_update", "args": {"state": {"settings": newer}}}})
        _drive(host, "r2", {"method": "command", "tool": "go"})
        assert calls == []
        assert seen == [settings, newer]
    finally:
        host.close()


def test_get_settings_before_the_host_sent_any_raises() -> None:
    """loader.ts:177: getSettings is `notInitialized` until the runner binds; no constant stands in for the missing settings."""
    outcome: list[str] = []

    def handler(ctx: pig_sdk.Context) -> None:
        try:
            ctx.get_settings()
        except RuntimeError as exc:
            outcome.append(str(exc))

    ext, ready = _command_ext("py-settings-missing", handler)
    host = _host(ext, ready)
    try:
        _drive(host, "r1", {"method": "command", "tool": "go"})
        assert outcome == ["settings are not available: the host has not sent them"]
    finally:
        host.close()


# ── ctx.tools and ctx.execute_tool ───────────────────────────────────────────

_CALLABLE = [{"name": "read", "label": "Read", "description": "Read a file", "parameters": {"type": "object"}}]


def _tool_ext(name: str, execute: Any, **state: Any) -> tuple[pig_sdk.Extension, dict[str, Any]]:
    ext = pig_sdk.Extension(name)
    ext.tool("outer", "outer", {"type": "object"}, execute)
    return ext, {"state": state}


def test_ctx_tools_asks_the_host_when_it_is_read_and_only_in_a_tool() -> None:
    """types.ts:385, runner.ts:958-965: the tool context lists the callable tools as the host has them when the getter is read; other contexts have no `tools`."""
    seen: list[Any] = []

    def execute(ctx: pig_sdk.Context, args: dict[str, Any]) -> str:
        seen.append(ctx.tools)
        return "ok"

    ext, ready = _tool_ext("py-tools", execute)
    outside: list[str] = []

    def command(ctx: pig_sdk.Context) -> None:
        try:
            _ = ctx.tools
        except RuntimeError as exc:
            outside.append(str(exc))

    ext.command("go", "run", lambda ctx, _args: command(ctx))
    host = _host(ext, ready)
    try:
        calls, _ = _drive(host, "r1", {"method": "tool_call", "tool": "outer", "tool_call_id": "tc1", "args": {}}, {"getCallableTools": {"tools": _CALLABLE}})
        commands, _ = _drive(host, "r2", {"method": "command", "tool": "go"})
        assert [c["call"]["method"] for c in calls] == ["getCallableTools"] and seen == [_CALLABLE]
        assert commands == []
        assert outside == ["tools is only available while a tool runs"]
    finally:
        host.close()


_OUTCOME = {
    "toolCall": {"type": "toolCall", "id": "tc1/1", "name": "read", "arguments": {"path": "a.txt"}},
    "result": {"content": [{"type": "text", "text": "file body from the host"}], "details": {"lines": 3}, "structuredContent": {"lines": 3}},
    "isError": False,
}


def _start_tool(host: _Host) -> dict[str, Any]:
    """Run the `outer` tool and return its first host call."""
    host.request("r1", {"method": "tool_call", "tool": "outer", "tool_call_id": "tc1", "args": {}})
    return host.read()


def test_execute_tool_runs_the_named_tool_and_returns_the_outcome() -> None:
    """types.ts:386-394, runner.ts:966-983: the call names the calling tool call, and its outcome comes back as the host returned it."""
    outcomes: list[Any] = []

    def execute(ctx: pig_sdk.Context, args: dict[str, Any]) -> str:
        outcomes.append(ctx.execute_tool("read", {"path": "a.txt"}))
        outcomes.append(ctx.execute_tool("read", {"path": "b.txt"}))
        return "done"

    ext, ready = _tool_ext("py-exec", execute, callableTools=_CALLABLE)
    host = _host(ext, ready)
    try:
        calls, response = _drive(host, "r1", {"method": "tool_call", "tool": "outer", "tool_call_id": "tc1", "args": {}}, {"executeTool": _OUTCOME})
        assert response["response"]["result"] == {"content": "done"}
        assert [c["call"]["method"] for c in calls] == ["executeTool", "executeTool"]
        first, second = (c["call"] for c in calls)
        assert first["parent_request_id"] == "r1"
        assert first["args"]["callerId"] == "tc1" and first["args"]["name"] == "read" and first["args"]["args"] == {"path": "a.txt"}
        assert "wantsUpdates" not in first["args"]
        assert first["args"]["executeId"] and first["args"]["executeId"] != second["args"]["executeId"]
        assert second["args"]["args"] == {"path": "b.txt"}
        assert outcomes == [_OUTCOME, _OUTCOME]
    finally:
        host.close()


def test_execute_tool_marks_an_explicit_signal() -> None:
    """runner.ts:979-981 (`{ ...options, signal: options.signal ?? signal }`): a nested tool runs with the signal its caller passed, and with the calling tool's otherwise, so its `signal` parameter differs only in the first case. The call tells the host which case it is."""

    def execute(ctx: pig_sdk.Context, args: dict[str, Any]) -> str:
        ctx.execute_tool("read", {})
        ctx.execute_tool("read", {}, pig_sdk.ExecuteToolOptions())
        ctx.execute_tool("read", {}, pig_sdk.ExecuteToolOptions(signal=pig_sdk.ProviderSignal()))
        return "done"

    ext, ready = _tool_ext("py-exec-own", execute, callableTools=_CALLABLE)
    host = _host(ext, ready)
    try:
        calls, _ = _drive(host, "r1", {"method": "tool_call", "tool": "outer", "tool_call_id": "tc1", "args": {}}, {"executeTool": _OUTCOME})
        assert [c["call"]["args"].get("ownSignal", False) for c in calls] == [False, False, True]
    finally:
        host.close()


def test_execute_tool_failures_come_back_as_error_outcomes_not_exceptions() -> None:
    """types.ts:391-393: unknown tools, validation errors and blocked calls are `isError: true`; the SDK returns what the host says."""
    failure = {"toolCall": {"type": "toolCall", "id": "tc1/1", "name": "nope", "arguments": {}}, "result": {"content": [{"type": "text", "text": "Tool nope not found"}], "details": {}}, "isError": True}
    got: list[Any] = []
    ext, ready = _tool_ext("py-exec-error", lambda ctx, args: got.append(ctx.execute_tool("nope", None)) or "ok")
    host = _host(ext, ready)
    try:
        calls, _ = _drive(host, "r1", {"method": "tool_call", "tool": "outer", "tool_call_id": "tc1", "args": {}}, {"executeTool": failure})
        assert got == [failure]
        assert calls[0]["call"]["args"]["args"] is None
    finally:
        host.close()


def test_execute_tool_outside_a_tool_call_raises() -> None:
    """runner.ts:952-985: `executeTool` exists only on the tool context."""
    outcome: list[str] = []

    def handler(ctx: pig_sdk.Context) -> None:
        try:
            ctx.execute_tool("read", {})
        except RuntimeError as exc:
            outcome.append(str(exc))

    ext, ready = _command_ext("py-exec-outside", handler)
    host = _host(ext, ready)
    try:
        calls, _ = _drive(host, "r1", {"method": "command", "tool": "go"})
        assert calls == [] and outcome == ["execute_tool is only available while a tool runs"]
    finally:
        host.close()


def test_execute_tool_delivers_updates_in_order_before_the_outcome() -> None:
    """types.ts:367-372 (onUpdate), nested-tool-calls.ts:221: partial results reach the callback in order and before the call returns, and the callback may itself call the host."""
    events: list[Any] = []

    def execute(ctx: pig_sdk.Context, args: dict[str, Any]) -> str:
        def on_update(partial: dict[str, Any]) -> None:
            events.append(("update", partial["content"][0]["text"]))
            ctx.notify("saw " + partial["content"][0]["text"])

        outcome = ctx.execute_tool("read", {}, pig_sdk.ExecuteToolOptions(on_update=on_update))
        events.append(("outcome", outcome["isError"]))
        return "done"

    ext, ready = _tool_ext("py-exec-updates", execute)
    host = _host(ext, ready)
    try:
        call = _start_tool(host)
        assert call["call"]["method"] == "executeTool" and call["call"]["args"]["wantsUpdates"] is True
        execute_id = call["call"]["args"]["executeId"]

        notified = []

        def update(request_id: str, target: str, text: str, calls_host: bool) -> None:
            # The host sends each partial result as a request and waits for the answer (nested-tool-calls.ts:219-231); the callback's own host call comes first.
            partial = {"content": [{"type": "text", "text": text}], "details": {}}
            host.request(request_id, {"method": "execute_tool_update", "args": {"executeId": target, "result": partial}})
            if calls_host:
                frame = host.read()
                assert frame["call"]["method"] == "ui.notify"
                notified.append(frame["call"]["args"]["message"])
                host.answer(frame, None)
            answer = host.read()
            assert answer["type"] == "response" and answer["id"] == request_id and not answer["response"].get("error"), answer

        update("u1", execute_id, "one", True)
        # An update of another call is dropped, and still answered.
        update("u2", "other", "foreign", False)
        update("u3", execute_id, "two", True)
        assert notified == ["saw one", "saw two"]
        host.answer(call, _OUTCOME)
        assert host.read()["response"]["result"] == {"content": "done"}
        assert events == [("update", "one"), ("update", "two"), ("outcome", False)]
    finally:
        host.close()


def test_execute_tool_signal_cancels_the_nested_call() -> None:
    """types.ts:367-372 (signal): aborting the option signal cancels only the nested call, named by its executeId."""
    signal = pig_sdk.ProviderSignal()
    aborted = dict(_OUTCOME, isError=True)
    got: list[Any] = []

    def execute(ctx: pig_sdk.Context, args: dict[str, Any]) -> str:
        got.append(ctx.execute_tool("read", {}, pig_sdk.ExecuteToolOptions(signal=signal)))
        return "done"

    ext, ready = _tool_ext("py-exec-signal", execute, callableTools=_CALLABLE)
    host = _host(ext, ready)
    try:
        call = _start_tool(host)
        assert call["call"]["method"] == "executeTool"
        signal.set()
        cancel = host.read()
        assert cancel["call"]["method"] == "executeTool.cancel"
        assert cancel["call"]["args"] == {"executeId": call["call"]["args"]["executeId"]}
        host.answer(cancel, None)
        host.answer(call, aborted)
        assert host.read()["response"]["result"] == {"content": "done"}
        assert got == [aborted]
    finally:
        host.close()


def test_execute_tool_explicit_signal_replaces_the_calling_requests_cancellation() -> None:
    """runner.ts:980 passes `signal: options.signal ?? signal`: a given signal replaces the calling tool's, so cancelling the calling request leaves the nested call running and executeTool resolves with its outcome."""
    got: list[Any] = []

    def execute(ctx: pig_sdk.Context, args: dict[str, Any]) -> str:
        try:
            got.append(ctx.execute_tool("read", {}, pig_sdk.ExecuteToolOptions(signal=pig_sdk.ProviderSignal())))
        except Exception as exc:  # noqa: BLE001 - the test records what escaped
            got.append(exc)
        return "done"

    ext, ready = _tool_ext("py-exec-own-signal", execute, callableTools=_CALLABLE)
    host = _host(ext, ready)
    try:
        call = _start_tool(host)
        assert call["call"]["method"] == "executeTool"
        assert "parent_request_id" not in call["call"]
        _write_frame(host.conn, {"type": "cancel", "id": "r1", "cancel": {"request_id": "r1", "reason": "user"}})
        host.answer(call, _OUTCOME)
        assert host.read()["response"]["result"] == {"content": "done"}
        assert got == [_OUTCOME]
    finally:
        host.close()


def test_execute_tool_with_an_aborted_signal_cancels_after_the_call_starts() -> None:
    """The host applies a cancel only after the call it names started (extension.CallInitiated), so a signal that is already aborted sends the cancel after the call."""
    signal = pig_sdk.ProviderSignal()
    signal.set()
    ext, ready = _tool_ext("py-exec-preset", lambda ctx, args: ctx.execute_tool("read", {}, pig_sdk.ExecuteToolOptions(signal=signal)) and "done", callableTools=_CALLABLE)
    host = _host(ext, ready)
    try:
        call = _start_tool(host)
        cancel = host.read()
        assert call["call"]["method"] == "executeTool" and cancel["call"]["method"] == "executeTool.cancel"
        assert cancel["call"]["args"]["executeId"] == call["call"]["args"]["executeId"]
        host.answer(cancel, None)
        host.answer(call, _OUTCOME)
        assert host.read()["response"]["result"] == {"content": "done"}
    finally:
        host.close()


def test_cancelling_the_calling_request_releases_a_blocked_execute_tool() -> None:
    """runner.ts:983: the nested call's default signal is the calling tool's; here the host cancels the request while the nested call runs."""
    failure: list[str] = []

    def execute(ctx: pig_sdk.Context, args: dict[str, Any]) -> str:
        try:
            ctx.execute_tool("read", {})
        except RuntimeError as exc:
            failure.append(str(exc))
        return "released"

    ext, ready = _tool_ext("py-exec-cancel", execute, callableTools=_CALLABLE)
    host = _host(ext, ready)
    try:
        call = _start_tool(host)
        assert call["call"]["method"] == "executeTool"
        _write_frame(host.conn, {"type": "cancel", "id": "r1", "cancel": {"request_id": "r1", "reason": "user"}})
        assert host.read()["response"]["result"] == {"content": "released"}
        assert failure and "cancelled" in failure[0]
    finally:
        host.close()


def test_provider_config_model_entries_of_every_type_reach_the_host_unchanged() -> None:
    """types.ts:1929-1991: chat, image and classifier entries of a provider config are declared as written."""
    config = {
        "baseUrl": "https://api.example/v1",
        "models": [
            {"id": "chat-1", "name": "Chat", "api": "openai-completions", "reasoning": False, "input": ["text"], "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 1000, "maxTokens": 100},
            {"type": "image", "id": "img-1", "name": "Image", "api": "openai-images", "output": ["image", "text"], "input": ["text"]},
            {"type": "classifier", "id": "cls-1", "name": "Classifier", "api": "llama-cpp-classify", "input": ["text"], "contextWindow": 512},
        ],
    }
    ext = pig_sdk.Extension("py-provider-types")
    ext.register_provider("multi", config)
    host = _host(ext)
    try:
        assert host.register["providers"] == [{"name": "multi", "config": config}]
    finally:
        host.close()
