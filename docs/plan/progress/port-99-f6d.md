# Lane port-99-f6d: extension API host side and the wire contract (family 6, sub-lane 6D)

Branch `port-99-f6d` from `porter/pi-0.99.1` (15904a765). Scope: `docs/plan/progress/family-6-split.md` section 3, lane 6D. Upstream sources: `.upstream/v0.99.1/packages/coding-agent/src/core/extensions/{types,runner,loader,wrapper}.ts`, `core/tools/tool-definition-wrapper.ts`, `core/sdk.ts`, `core/agent-session-services.ts`, `core/virtual-models.ts` (types), `core/mcp-servers.ts` (types).

## Contract commit (stubs only, no behavior)

Lets lanes 6E and 6F compile their red tests. Every new type, field, event name and wire shape is declared; behavior bodies `panic("not implemented: ...")` or return `errNotImplemented`.

In-process API (`coding/extension`):

| upstream 0.99.1 | Go |
|---|---|
| `ToolExposure`, `ToolAnnotations`, `ToolNamespace`, `ToolDefinition.{outputSchema,exposure,namespace,annotations}`, `ToolInfo.{exposure,namespace,annotations}` (`types.ts:509-596`, `2063`) | `tool.go` (hunks identical to lane `port-99-f8-mcp`, which added them first) |
| `ToolDefinition.{defaultActive,prepareLoadout}`, `ToolLoadout`, `ToolLoadoutChanges` (`types.ts:541-607`) | `tool.go`, `tool_loadout.go` |
| `ExecuteToolOptions`, `ExtensionToolContext` (`tools`, `executeTool`), `ExtensionContextActions.{executeTool,getCallableTools}`, `AgentToolCallOutcome`, `AgentTool` view (`types.ts:367-395`, `2161-2169`) | `tool_context.go`: `ToolContext` (retrieve with `ToolContextFromContext`), `ToolActions` embedded in `ContextActions`, `NewToolContext`, `WithToolContext` |
| `parentToolCallId` on `tool_call`, `tool_result`, `tool_execution_*` events; `structuredContent` on `tool_result` and its result (`types.ts:1044-1219`, `1424`) | `events.go` |
| events `provider_stream_event`, `mcp_servers_change` (`types.ts:699`, `884`) | `events.go`, `API.OnProviderStreamEvent`, `API.OnMcpServersChange`, constants in `internal/codingagent/extensions.go` |
| `pi.registerMcpServer`, `unregisterMcpServer`, `getMcpServers`, `registerVirtualModel`, `unregisterVirtualModel`, `getSettings` (`types.ts:1708`, `1833-1855`) | `API` methods (`extensiontest.Fake` records them); `Settings` (`settings.go`) |
| `Extension.replaceable` (`types.ts:2209`) | `Extension.Replaceable` |
| virtual-model routing types, `VIRTUAL_MODEL_API`, `VIRTUAL_MODEL_STATE_ENTRY` (`virtual-models.ts:29-101`) | `virtual_model.go` (in this package because the API needs them and `coding` imports `coding/extension`); lane 6E aliases them in `coding/virtual_models.go` |
| `ProviderConfig.{images,classifiers}`, chat/image/classifier `ProviderModelConfig` (`types.ts:1896-1991`) | `provider.go`: one struct with `Type` and `Output`; lane 6B consumes |
| `ExtensionRuntime` MCP registry, pending virtual models, `createContext` | `runtime_registrations.go` (stubs) |
| `runner.createToolContext`, `bindCore` executeTool/getCallableTools, `reportUnhandledMcpServers` | `host/inproc/tool_context.go` (stubs) |
| `ExtensionActions.getSettings`, `ProviderActions.{register,unregister}VirtualModel` | `host_actions.go` |

Wire (`coding/extension/host/subprocess`): `protocol_extension_api.go` holds the new shapes; `protocol.go` gains `RegisterPayload.{McpServers,VirtualModels}`, `ToolDecl.{OutputSchema,Exposure,Namespace,DefaultActive,PreparesLoadout}` (its caller-free `Annotations map[string]string` is now upstream's `ToolAnnotations`), `ToolResult.StructuredContent`, `StatePayload.{Settings,McpServers,CallableTools}`. Calls: `registerMcpServer`, `unregisterMcpServer` (reply: `McpServersResult`), `registerVirtualModel`, `unregisterVirtualModel`, `executeTool`, `executeTool.cancel`. Host to extension: request `virtual_model_route`, request `tool_prepare_loadout`, notify `execute_tool_update`. `getSettings`, `getMcpServers` and `ctx.tools` are answered from replicated state, not host calls. `host_calls_registry.go` returns `errNotImplemented` for the calls; `HostCallbacks` gains `GetSettings`, `GetCallableTools`, `ExecuteTool`.

Copied verbatim from lane `port-99-f8-mcp` because the API needs `McpServerConfig`, `RegisteredMcpServer` and `McpServerRegistry` to compile: `coding/extension/mcp_servers.go`, `internal/orderedjson/orderedjson.go`, and the `tool.go` hunks. They are identical on both branches, so the merge is clean unless f8 changes them first.

Files outside 6D's list that the contract touches: `internal/codingagent/extensions.go` (two event-name constants, no owner in the split). Wiring `OnProviderStreamEvent` needs one line in `coding/session_cache_warming.go` (`cacheWarmingStreamFn`, lane 6E's file); it lands in the green commit after 6E agrees.

## Red

Commits `f495f91a8` (runner-level tests, stubs return errors instead of panicking) and the wire/model-config/SDK tests that follow the merge of `porter/pi-0.99.1` (family 4). Evidence: `docs/plan/evidence/port-99-f6d-red.txt`.

| test file | new tests | red |
|---|---:|---:|
| `coding/extension/host/inproc/mcp_servers_registration_test.go` | 4 | 4 |
| `coding/extension/host/inproc/virtual_models_registration_test.go` | 3 | 3 |
| `coding/extension/host/inproc/tool_context_test.go` | 3 | 3 |
| `coding/extension/host/inproc/tool_result_structured_content_test.go` | 3 | 2 (1 guard) |
| `coding/extension/host/subprocess/extension_api_wire_test.go` | 12 | 12 |
| `coding/extension/provider_model_config_types_test.go` | 2 (+5 subtests) | 1 (3 subtests) |
| `coding/sdk_stream_options_upstream_test.go` | 1 | 1 |

Contract change in this step: `Runner.BindToolActions` is removed (`ContextActions.ToolActions` carries the actions, as `BindCore` and `BindTools` already do for the other actions), stub bodies return errors or zero values, `HostCallbacks.FindModel` resolves the physical model a router names.

## Green

Commits after the red set: `92a08eea8` (runtime, runner and subprocess host), the release-on-stop commit, the `provider_stream_event` and per-call tool-context commit, `EventBus` rename plus changelog, and the harness and lazy-regexp fixes. Evidence: `docs/plan/evidence/port-99-f6d-green.txt`.

Behavior landed (each source cites `.upstream/v0.99.1/...:line` at the function):
- MCP servers: registration, ownership, replace-own, change events delivered in order by one drainer, `reportUnhandledMcpServers` once per name (`loader.ts:456-478`, `runner.ts:459-462`).
- Virtual models: queue until `bindCore`, then immediate; routing through the extension (`virtual_model_route` request, cancel frame on context cancel, model resolved through `HostCallbacks.FindModel`).
- Tool context: `runner.CreateToolContext` per call id, `executeTool` (parent cancel, extension cancel, ordered updates, error outcome `<caller>/0` without an action) and `getCallableTools` (`runner.ts:948-985`).
- `tool_result` `structuredContent` chain and wire `ToolResult.StructuredContent` into `agent.AgentToolResult`.
- Wire: tool exposure fields, `prepareLoadout`, register-payload servers and models with load-time rollback, release at stop, replace (successor keeps its names) and disable, replicated `settings`, `mcpServers` (never null) and `callableTools`.
- coding: `provider_stream_event` (`sdk.ts:372-386`), per-call tool context in `sessionBoundTool`, `bridgeTool.OutputSchema`.
- `extension.EventBus` is `Emit`/`On` (lead answer 1); the changelog carries the migration note. The wire side of `pi.events` for native SDKs is lane 6F-events.

Harness edits to lane 6D's own red wire tests, none of which changes an assertion: `notify()` skips unrelated notifies (the host also sends `model_registry_update`); handler goroutines use `callErr` and never call `t.Fatal` after the test ended (found by the burner run); `boolPtr` became `new(true)` per `go fix`.

Stubs and hand-offs other lanes must fill:
- 6E: session wiring of the `HostCallbacks` `ExecuteTool`, `GetCallableTools`, `FindModel`, `GetSettings` and of `ContextActions.ToolActions`/`GetSettings` (`coding/session*.go`). Until then a session's nested calls return the error outcome and `getSettings` answers empty.
- 6F: the SDK side of every new wire shape, the conformance rows and the `pi.events` bridge.
- Integrator: `pig-go.json`/interface ledgers, PORT_MAP, `docs/extension-api-parity.md` rows; `internal/codingagent/extensions.go` gained two event-name constants.

Not verifiable on this host: `make parity-family` (`parity-deps` refuses to run through the shared `extensions/sdk-ts/node_modules`; a run with `-o parity-deps` found no usable Pi 0.87.1 comparator under the temporary HOME, so its failures are not evidence). Baseline failures unrelated to this lane: version-guarded oracle tests (`TestRPC33*`, `Test*FauxAgentObservationOracle`, and the same in `cmd/pig`), `TestNodeVendoredTuiUpstreamTests` (needs Xvfb), the stdout-backpressure frame tests (fail on `staging/porter/pi-0.99.1`), and the `internal/codingagent/tools` grep tests (no `rg` under the temporary HOME).
