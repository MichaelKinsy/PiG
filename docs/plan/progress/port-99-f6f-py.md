# Lane port-99-f6f-py: the Python SDK for the upstream 0.99.1 extension API (family 6, sub-lane 6F-py)

Branch `port-99-f6f-py` from the 6D contract commit `906c8fe28`, then merged with staging `port-99-f6d` (`88d5d92ea`) so the Host side of every capability is real: the lane changes only the Python SDK. Scope: `docs/plan/progress/family-6-split.md` section 3 lane 6F (Python) and section 4 (`pi.events`). Owned: `extensions/sdk-py/**` (the only editor of `pig_sdk/__init__.py`) and new files under `test/extension-conformance/`.

## Capability map

Each row is one upstream 0.99.1 capability (`.upstream/v0.99.1/packages/coding-agent/src/core/`) and its Python spelling. Names follow the SDK convention (snake_case; the wire keeps the names of `protocol_extension_api.go`).

| upstream | Python | wire |
|---|---|---|
| `registerMcpServer`, `unregisterMcpServer` (`extensions/types.ts:1834-1837`, `loader.ts:456-478`) | `Extension.register_mcp_server(name, config)`, `unregister_mcp_server(name)`, and the same on `Context` | factory time: `RegisterPayload.mcp_servers`; after load: calls `registerMcpServer`, `unregisterMcpServer`, reply `McpServersResult` replaces the replicated list |
| `getMcpServers` (`types.ts:1840`) | `Context.get_mcp_servers()` | replicated `StatePayload.mcpServers`, no host call |
| `registerVirtualModel`, `unregisterVirtualModel` (`types.ts:1851-1854`, `loader.ts:480-497`) | `VirtualModel` dataclass, `Extension.register_virtual_model(model)`, `unregister_virtual_model(provider, id)` | `RegisterPayload.virtual_models`; calls `registerVirtualModel`, `unregisterVirtualModel`; request `virtual_model_route` |
| `getSettings` (`types.ts:1712`) | `Context.get_settings()` | replicated `StatePayload.settings`; raises until the host sent it (upstream `notInitialized`, `loader.ts:177`) |
| `provider_stream_event`, `mcp_servers_change` (`types.ts:699-709`, `884-890`) | `EVENT_PROVIDER_STREAM_EVENT`, `EVENT_MCP_SERVERS_CHANGE`, `on_event(...)` | event requests, as every other event |
| `ToolDefinition.{outputSchema,exposure,namespace,annotations,defaultActive,prepareLoadout}` (`types.ts:579-607`) | `ToolDefinition.{output_schema,exposure,namespace,annotations,default_active,prepare_loadout}`, `ToolLoadout`, `ToolLoadoutChanges` | `ToolDecl.{output_schema,exposure,namespace,annotations,default_active,prepares_loadout}`; request `tool_prepare_loadout` |
| tool results `structuredContent`, `isError` (`agent/src/types.ts:424-446`) | keys `structured_content`, `is_error` of the returned dict (as `details`, `usage`, `terminate` already are) | `ToolResult` |
| `ExtensionToolContext.tools`, `executeTool`, `ExecuteToolOptions` (`types.ts:367-395`, `runner.ts:952-985`) | `Context.tools` (property), `Context.execute_tool(name, args, options)`, `ExecuteToolOptions(signal, on_update)`; only while a tool runs | `StatePayload.callableTools`; call `executeTool`, `executeTool.cancel`, notify `execute_tool_update` |
| `parentToolCallId` on tool events (`types.ts:1044-1219`) | key `parentToolCallId` of the event dict (the SDK passes events through) | events |
| `ProviderConfig` chat, image and classifier entries (`types.ts:1929-1991`) | `register_provider(name, config)` passes the entries as written | `ProviderDecl.config` |
| `pi.events` (`event-bus.ts:12-33`, `loader.ts:501-513`) | `Extension.events`, `Context.events`: `on(channel, handler) -> unsubscribe`, `emit(channel, data)` | `events.on`, `events.off`, `events.emit` calls flagged `value`, request `events.dispatch` (lane port-99-f6f-events) |

Not an SDK capability: `Extension.replaceable` (`types.ts:2209`). Only the resource loader sets it, for an inline extension it is given (`resource-loader.ts:1137`), and `pi` has no call for an extension to set it on itself.

## Red

Python: `extensions/sdk-py/tests/test_extension_api_099.py` (29 cases) and `test_event_bus.py` (12 cases, now `test_event_bus_semantics.py`). Conformance rows against the real Host: `test/extension-conformance/python_extension_api_test.go` (8 rows), each under `strict` (one process) and `shared-ok` (one packed cell). Red commit `20d139b04`; evidence: `docs/plan/evidence/port-99-f6f-py-red.txt` (37 Python cases and 6 of the 8 Go rows fail; the other cases pass on purpose, below).

Cases that pass at red on purpose, because they pin behavior the SDK already has and the capability depends on: the two event names (`test_event_constants_of_the_new_events`, `test_new_event_handlers_are_declared_and_receive_the_event`), the pass-through of `structured_content` and `is_error` (`test_tool_result_carries_structured_content_and_is_error_unchanged`, `TestPythonSDKToolResultCarriesStructuredContentAndIsError`), and the provider model entries (`test_provider_config_model_entries_of_every_type_reach_the_host_unchanged`, `TestPythonSDKProviderConfigWithImageAndClassifierModels`). They are regression guards for the Host's decoding, not proof of new SDK code; the conformance rows read the value on the Host side.

## Green

Commits, in order (see `git log`): red; green (`feat(sdk-py): Python SDK for the upstream 0.99.1 extension API and pi.events`); merge of the events lane (`staging port-99-f6f-events`); `feat(sdk-py): implement pi.events in pig_sdk/event_bus.py`; style; this progress commit with `events.release`. Evidence: `docs/plan/evidence/port-99-f6f-py-green.txt`.

Changes to ported tests, each cited in place:
- `test_mcp_servers_registered_by_the_factory_travel_in_the_register_frame`: the red commit expected `[wiki, docs-v2]`; `mcp-servers.ts:212-215` (`servers.set`) keeps the position of a replaced key, so the answer is `[docs-v2, wiki]`. Mis-port of mine.
- `TestPythonSDKExecuteToolAndCallableTools`: the fixture formats `isError` with Python's `%s`, which prints `False`. Mis-port of mine.
- `tests/test_event_bus.py` of the red commit is now `tests/test_event_bus_semantics.py`, so the events lane's file of that name merges without a conflict; one assertion says `unhandled error` in lower case, as the events lane's wire test does.

Design notes:
- Registrations during the factory travel in the register frame; a live registration is a host call. `Context` methods send the call with the request as its parent; `Extension` methods send it without one.
- `execute_tool` waits on an event whose `set` also wakes the update queue, so `on_update` runs on the calling thread and never on the read loop; the callback may call the host. An `on_update` error is raised after the outcome (upstream does not say; an error is not swallowed).
- `get_settings` raises until the host sent settings (upstream `notInitialized`); no constant stands in.
- `pi.events` follows the events lane's wire (`value: true`, `json`, `events.dispatch`, `events.release`). A listener stays callable after `unsubscribe()` until the host releases it, so memory follows the host's snapshots, not the number of subscribe calls.

Overlap with the events lane: it declared `EventBus` in `pig_sdk/event_bus.py` and a `Context.events` and `Extension.events` in `__init__.py` in its red commit (`c7b15a3d1`), and its red tests for the Python wire. This lane implemented `event_bus.py` on that layout and merged the branch. If the events lane also writes a Python green, its `event_bus.py` and `__init__.py` hunks conflict with this lane's; keep this lane's (its rows and the events lane's Python rows all pass).

## Mutation and load

Mutation: 27 of 27 Python mutants killed by the lane's unit tests, 9 of 9 by the Host conformance rows (evidence file). Load: `go test -race -test.count=24` of the 8 Python rows under 8 burners on 4 cores passes (192 of 192); the Python unit tests pass 24 of 24 under the same burners. The events lane's stress row (`TestNativeEventBusStressKeepsEveryDeliveryAndBoundedState`) fails at its own 10-minute cap under 4 burners on 4 cores for the Go realm and for the Python realm alike (unloaded: go 83.5 s, python 108.5 s): flagged for the events lane, not closed here.

## Deferred and exceptions

See `docs/plan/evidence/port-99-f6f-py-docs-rows.md`: `ProviderConfig.images` and `classifiers` and the native `Provider` members for image and classifier models need reverse calls the wire does not have (host lane 6B); `Extension.replaceable` is a loader field, not an SDK capability. The rows for `docs/extension-api-parity.md`, `docs/extension-sdk-surface.md` and `test/parity/async-contracts.toml` are drafted there for the integrator.
