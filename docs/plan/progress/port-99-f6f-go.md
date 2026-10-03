# Lane port-99-f6f-go: the Go SDK of the upstream 0.99.1 extension API (family 6, sub-lane 6F-go)

Branch `port-99-f6f-go`. Scope: `docs/plan/progress/family-6-split.md` section 3, lane 6F, the Go SDK only (`extensions/sdk`) plus new conformance rows in `test/extension-conformance/`. The `pi.events` bridge belongs to lane 6F-events; the Rust, Python and TypeScript SDKs to their own sub-lanes.

The base is the 6D contract commit merged with family 4 (`ToolResult.structuredContent` on `agent.AgentToolResult`) and the 6D green commits (`b72ac6624`), because the conformance rows need the host side of every call. The merges are the first two commits of the branch.

## The capability list and its Go SDK shape

The SDK has no Pi types at hand: its module cannot import `coding/extension`. It mirrors the in-process names (PascalCase of Pi's camelCase) and keeps the wire of `coding/extension/host/subprocess/protocol_extension_api.go`.

| upstream 0.99.1 capability | Go SDK | wire |
|---|---|---|
| `pi.registerMcpServer`, `unregisterMcpServer` (`loader.ts:456-471`) | `Extension.RegisterMcpServer(name, McpServerConfig) error`, `UnregisterMcpServer`, the same on `Context`; `McpServerConfig`, `McpOAuthConfig`, `McpExposure`, `OrderedStrings`, `OrderedExposures` | register payload `mcp_servers` before the handshake; calls `registerMcpServer` / `unregisterMcpServer` after |
| `pi.getMcpServers` (`loader.ts:473-476`) | `Context.GetMcpServers() ([]RegisteredMcpServer, error)`, read from replicated state | `state.mcpServers`; the reply of each call carries the full list |
| `pi.registerVirtualModel`, `unregisterVirtualModel` (`loader.ts:480-495`) | `Extension.RegisterVirtualModel(VirtualModel) error`, `UnregisterVirtualModel`, the same on `Context`; `ModelRouteRequest`, `ModelRoute`, `ModelRouteReason` | register payload `virtual_models`; calls `registerVirtualModel` / `unregisterVirtualModel`; host request `virtual_model_route` |
| `pi.getSettings` (`loader.ts:411-414`) | `Context.GetSettings() (Settings, error)` | `state.settings` |
| events `provider_stream_event`, `mcp_servers_change` (`types.ts:699`, `884`) | `EventProviderStreamEvent`, `EventMcpServersChange`, `Extension.OnProviderStreamEvent`, `OnMcpServersChange` | ordinary `event` requests |
| tool fields `outputSchema`, `exposure`, `namespace`, `annotations`, `defaultActive`, `prepareLoadout` (`types.ts:579-607`) | `ToolDefinition.{OutputSchema, Exposure, Namespace, Annotations, DefaultActive, PrepareLoadout}`; `ToolExposure`, `ToolAnnotations`, `ToolNamespace`, `ToolLoadout`, `ToolLoadoutChanges` | `ToolDecl.{output_schema, exposure, namespace, annotations, default_active, prepares_loadout}`; host request `tool_prepare_loadout` |
| tool result `structuredContent`, `isError` (`agent/types.ts:424-446`) | `ToolResult.StructuredContent` (`IsError` existed) | `structured_content` |
| `ExtensionToolContext.tools`, `executeTool`, `ExecuteToolOptions` (`types.ts:367-394`, `runner.ts:948-985`) | `Context.Tools()`, `Context.ExecuteTool(name, args, *ExecuteToolOptions)`; `AgentTool`, `AgentToolResult`, `AgentToolCallOutcome`, `ExecuteToolOptions{Signal, OnUpdate}` | `state.callableTools`; calls `executeTool`, `executeTool.cancel`; notify `execute_tool_update` |
| `ToolInfo.{exposure, namespace, annotations}` (`types.ts:2063`) | `ToolInfo.{Exposure, Namespace, Annotations}` | `getAllTools` |
| provider config chat/image/classifier entries (`types.ts:1929-1991`) | the provider config is a JSON map, so `type` and `output` pass through | `providerDef.config` |
| `parentToolCallId` on `tool_call`, `tool_result`, `tool_execution_*` (`types.ts:1044-1219`) | event data is a JSON map, so the field passes through | event payload |
| `Extension.replaceable` (`types.ts:2209`) | designed out for the SDK: the CLI sets it on its built-in inline extensions (`types.ts:2005`); an authored extension has no such option | none |

`parentToolCallId`, provider config entries and `replaceable` have no SDK code to write; their conformance rows (or the reason for none) are in the evidence file.

## Red

Evidence: `docs/plan/evidence/port-99-f6f-go.red.txt`.

| test file | new tests | red |
|---|---:|---:|
| `extensions/sdk/mcp_servers_test.go` | 5 | 5 |
| `extensions/sdk/settings_test.go` | 2 | 1 (1 guard) |
| `extensions/sdk/virtual_models_test.go` | 5 | 5 |
| `extensions/sdk/tool_orchestration_test.go` | 4 | 4 |
| `extensions/sdk/execute_tool_test.go` | 7 | 6 (1 guard) |
| `test/extension-conformance/extension_api_go_test.go` | 8 rows x 3 placements (fused, strict, packed) | 24 of 24 |

The upstream test files that define the inputs are `test/suite/agent-session-tool-orchestration.test.ts` (the orchestrator tool and its `prepareLoadout`, verbatim) and the runner code of `loader.ts` and `runner.ts`. No upstream test file targets an SDK, so the assertions come from those inputs and from the cited lines.

## Green

Commits: test fixes `eef90dcdc`, green `2e8568aac`, then the stale-state fix, the `hostCallFor` refactor, README and changelog fragment. Results, the 18 mutation checks, the load test, the bug the load test found, the gaps and the row text for the integrator: `docs/plan/evidence/port-99-f6f-go.green.md`.

Open for other lanes: the host wire `ToolInfo` carries no exposure, namespace or annotations (6D/6E); provider `images` and `classifiers` implementations have no wire (6B/6D); `executeTool` through a real Session needs 6E.
