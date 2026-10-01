# Docs rows for the Python SDK of lane port-99-f6f-py

Text for the integrator to place in `docs/extension-api-parity.md`, `docs/extension-sdk-surface.md` and `test/parity/async-contracts.toml` (the lane does not edit those files: split doc section 5). Upstream citations are `.upstream/v0.99.1/packages/coding-agent/src/core/`.

## `docs/extension-sdk-surface.md`, Python column

| surface | Python realization |
|---|---|
| `pi.registerMcpServer`, `pi.unregisterMcpServer` (`extensions/types.ts:1834-1837`) | implemented `Extension.register_mcp_server`, `unregister_mcp_server`, `Context.register_mcp_server`, `unregister_mcp_server` |
| `pi.getMcpServers` (`types.ts:1840`) | implemented `Context.get_mcp_servers` (replicated state; no factory-time read, see exceptions) |
| `pi.registerVirtualModel`, `pi.unregisterVirtualModel` (`types.ts:1851-1854`) | implemented `VirtualModel`, `Extension.register_virtual_model`, `unregister_virtual_model`, and the same on `Context` |
| `pi.getSettings` (`types.ts:1712`) | implemented `Context.get_settings` |
| `ToolDefinition.outputSchema`, `exposure`, `namespace`, `annotations`, `defaultActive`, `prepareLoadout` (`types.ts:579-607`) | implemented `ToolDefinition.output_schema`, `exposure`, `namespace`, `annotations`, `default_active`, `prepare_loadout`; `ToolLoadout`, `ToolLoadoutChanges`, `ToolAnnotations`, `ToolNamespace`, `ToolExposure` |
| `AgentToolResult.structuredContent`, `isError` (`agent/src/types.ts:424-446`) | implemented as the keys `structured_content` and `is_error` of the returned dict |
| `ExtensionToolContext.tools`, `executeTool`, `ExecuteToolOptions` (`types.ts:367-395`) | implemented `Context.tools`, `Context.execute_tool`, `ExecuteToolOptions` |
| `provider_stream_event`, `mcp_servers_change` (`types.ts:699-709`, `884-890`) | implemented `EVENT_PROVIDER_STREAM_EVENT`, `EVENT_MCP_SERVERS_CHANGE` through `on_event` |
| `parentToolCallId` on tool events (`types.ts:1044-1219`) | implemented: event dicts pass through |
| `ProviderConfig` chat, image and classifier model entries (`types.ts:1929-1991`) | implemented for the dict config of `register_provider` |
| `ProviderConfig.images`, `ProviderConfig.classifiers` (`types.ts:1896-1898`) | missing exception: the implementations are functions, and the wire has no reverse call for them (see Deferred) |
| `pi.events.emit`, `pi.events.on` (`event-bus.ts:12-33`) | implemented `Extension.events`, `Context.events` (`EventBus.emit`, `EventBus.on`); replaces D83 boundary 5 for Python |
| `Extension.replaceable` (`types.ts:2209`) | not an SDK surface: only the resource loader sets it for an inline extension (`resource-loader.ts:1137`) |

## `docs/extension-api-parity.md` rows

Extension API methods: `pi.registerMcpServer`/`unregisterMcpServer`/`getMcpServers`, `registerVirtualModel`/`unregisterVirtualModel`, `getSettings`: Python `Extension` and `Context` methods. A registration made while the factory runs is queued and travels in the register frame (`mcp_servers`, `virtual_models`), so it is applied when the factory succeeds, in order, as `applyRuntimeChange` does (`loader.ts:259-262`). A queued unregister removes the queued entry; a repeated MCP name replaces its entry in place (`mcp-servers.ts:212-215`). After load each call is a host call. The reply of an MCP call is the whole registry; the read loop applies it in frame order, before the call returns and never over a later state push, so `get_mcp_servers` after a registration sees it. `get_mcp_servers` and `get_settings` read the state the host pushes; `get_settings` raises until the host sent settings (`loader.ts:177`).

Tool definition features: `output_schema`, `exposure`, `namespace`, `annotations` and `default_active` are declared in `ToolDecl`; `prepare_loadout` is answered by the request `tool_prepare_loadout`, whose payload builds a `ToolLoadout` (`get_exposure` of an unregistered tool is `"direct"`, `agent-session.ts:1480-1482`). An answer of `None` changes nothing.

Context APIs: `ctx.tools` and `ctx.execute_tool` exist only in the context of a `tool_call` request, as `createToolContext` builds them only for a tool call (`runner.ts:952-985`).

Async contract (`ctx.execute_tool`, Python realm): the call blocks its calling thread and returns the outcome; it never raises for a tool failure (`types.ts:391-393`). Without an option `signal` the calling request is the call's parent, so cancelling the request releases the blocked call with an error and cancels the nested call in the host (`runner.ts:980`, default signal). A given option `signal` replaces the request's cancellation (`options.signal ?? signal`): the call is sent without a parent, cancelling the calling request leaves it running, and `execute_tool` returns its outcome. The option `signal` cancels the nested call alone: the SDK sends `executeTool.cancel` for the call's `executeId` from a thread of its own, after the call was sent, because the host applies a cancel only after the call it names started. Partial results arrive as `execute_tool_update` notifies, which the read loop queues; the caller's thread runs `on_update` for each in order and returns after the last one that preceded the result. An `on_update` that raises does not stop the nested call; the first error is raised from `execute_tool` after the outcome (upstream does not specify this edge). No blocking work runs on the read loop.

Async contract (`virtual_model_route`, Python realm): the request runs the router on a worker thread as any request; `request["signal"]` is the request's cancellation. The router's error or a route without a model fails only that request. A route whose `state` is `None` keeps the current state, as upstream's `undefined` does (`virtual-models.ts:77-83`).

Event bus lifecycle (Python realm): see the events lane's row. Python adds: a subscription made while the factory runs is sent before the register frame (the read loop has not started; the loading thread reads the host's answer and serves the pings and `events.dispatch` requests that arrive meanwhile, as a node runtime serves a dispatch during its factory); handler IDs are unique in the process, because the Host keys a listener by realm (process) and ID and a packed cell runs several extensions in one process; `unsubscribe()` never raises and prints a host refusal to stderr (`EventEmitter.off` never throws); a listener stays callable after `unsubscribe()` because the host dispatches the snapshot it took before the off call (`EventEmitter` clones its listener array for each emit); `emit` before the extension is connected raises.

## `test/parity/async-contracts.toml`

One entry for `ctx.execute_tool` (contract text above) and one for `virtual_model_route`, both with evidence `extensions/sdk-py/tests/test_extension_api_099.py` and `test/extension-conformance/python_extension_api_test.go` (`TestPythonSDKExecuteToolAndCallableTools`, `TestPythonSDKVirtualModelRegistrationAndRouting`).

## Exceptions and deferred (for the READY report)

1. `ProviderConfig.images` and `ProviderConfig.classifiers`, and the native `Provider` members `getAllModels`, `filterAllModels`, `generateImages`, `classify` (`ai/src/models.ts:144-260`): the implementations are functions, and the wire has no reverse call for them (`NativeProviderDeclaration.Methods` names only the 0.87.1 members, and `native_provider.go` proxies only those). The lane that owns the host side (model runtime and classifier, 6B) defines the reverse calls; the Python SDK then adds them next to `filter_models` in `pig_sdk/provider.py`. Until then a config that carries a function fails with the SDK's JSON check rather than being ignored.
2. `get_mcp_servers` and `get_settings` inside the factory: upstream's `getMcpServers` works while extensions load (`loader.ts:476-478`), and `getSettings` throws (`loader.ts:177`). The Python factory runs before the connection exists, so the host's registry is not known there. `Context` has no factory-time form, and `Extension` has no getter, so the difference is not reachable through the API; no divergence record is needed unless an `Extension.get_mcp_servers` is added.
3. `Extension.replaceable`: not an SDK capability (see the surface table).
