# port-99-f6f-go green evidence

Lane 6F-go: the Go SDK of the upstream 0.99.1 extension API. Commits: red `b12349321`, test fixes `eef90dcdc`, green `2e8568aac`, then the stale-state fix and refactor recorded below. Host: Linux, Go 1.27.1, Node 24.19.0.

## Results

- `cd extensions/sdk && go test -race -count=1 .`: ok, 18.6 s. The 25 new SDK rows (mcp servers 6, settings 2, virtual models 5, tool orchestration 5, execute tool 7) all pass. The red run had 23 of them, 21 failing; the loadout defaults row and the reply-ordering row came after.
- `go test -race ./test/extension-conformance -run ExtensionAPI`: 9 rows x 3 placements (fused, strict, packed) pass.
- `go test -race ./coding/extension/ ./coding/extension/host/inproc/ ./coding/extension/pigsdk/ ./coding/extension/source/`: ok. `./coding/extension/host/subprocess/`: one failure, `TestNodeVendoredTuiUpstreamTests/native-clipboard-linux` ("native clipboard qualification requires Xvfb": no `Xvfb` on this host); no other failure, and the failure is not in code this lane touches.
- `golangci-lint run --build-tags=integration,live,parity ./extensions/sdk/... ./test/extension-conformance/...`: 0 issues. `gofmt -l`: empty. `go vet ./...` and `GOOS=windows go vet` on both packages: clean. `go fix -diff`: only `extensions/sdk/json/encode.go` and `indent.go`, vendored files that were already flagged before this lane and stay byte for byte.

- Whole `./test/extension-conformance` package under `-race`: every row passes except `TestSelectedToolsPiContract`, which needs the upstream package in `extensions/sdk-ts/node_modules` (not installed in this worktree; `ERR_MODULE_NOT_FOUND`). It does not touch this lane's files.

## Rows that passed in red

- `TestGetSettingsFailsWhenTheHostSentNone`, `TestToolOrchestrationIsOnlyAvailableInsideATool`: the stub returned an error too. Mutation M5 (a missing settings object answered with `{}`) fails the first. The second is a guard for the "no calling id, no host call" rule; the mutation that drops the `toolCallID` check is not written because the row cannot fail while `ExecuteTool` needs a calling id for its wire argument.
- `TestExtensionAPIToolEventsCarryParentAndStructuredContentGo` (added after red): the Go SDK hands event data through as a map, so `parentToolCallId` and `structuredContent` needed no SDK code. It is a guard for the host's forwarding of both fields and of a handler's replacement `structuredContent`.
- `TestExtensionAPIProviderModelTypesGo`: provider configs are JSON maps in the SDK, so the chat, image and classifier entries pass through; the row proves the host decodes them.

## Bug found by the load test (found at green, not by a ported test)

Symptom: under CPU load the strict conformance row saw `register_late = "registered late sees docs+taken"`, so `GetMcpServers` right after `RegisterMcpServer` returned the list without the new server. Cause: the host sends a state update before its reply, the read loop routes the reply to the caller at once, and the message loop applies the older update afterwards, overwriting the reply's list. Fix: the read loop stamps each reply with the number of notifications it had queued (`callResultMsg.notifySeq`), and the caller waits for the message loop to apply that many before it installs the reply (`extensions/sdk/mcp_servers.go`, `tool_orchestration.go`). A width handler, which runs on the message loop, has no request and does not wait. Regression: `TestRegisterMcpServerReplyOutranksAnOlderStateUpdate`, deterministic (a blocked width handler holds the loop while the stale update and the reply arrive); mutation M18 (no wait) fails it. `ExecuteTool` uses the same sequence instead of the count at the time it resumes.

The conformance row was strengthened in the same step: `register_late` now reads `GetMcpServers` inside the same handler, so a reply that is not installed fails the row (mutation M3 survived the row before that).

Second finding: the runner delivers `mcp_servers_change` on its own goroutine, so the harness's error listener could report "connection closed" after the test ended; the harness ignores extension errors once cleanup starts.

## Mutation checks

Each mutation was compiled, run against the named rows, and reverted.

| id | mutation | killed by |
|---|---|---|
| M1 | ordered maps marshal sorted keys | `TestOrderedMcpMapsKeepInsertionOrderThroughTheWire` |
| M2 | queued MCP registration appends instead of replacing | `TestRegisterMcpServerBeforeRunQueuesInRegisterPayload` |
| M3 | MCP call reply not installed as the replicated list | `TestRegisterMcpServerAtRuntimeCallsTheHostAndRefreshesTheReplicatedList`, conformance `TestExtensionAPIReplicatedStateAndLateRegistrationGo` |
| M4 | state update ignores settings | `TestGetSettingsReturnsACopyOfTheReplicatedSettings` |
| M5 | missing settings answered with an empty object | `TestGetSettingsFailsWhenTheHostSentNone` |
| M6 | the update callback holds the message loop | `TestExecuteToolDeliversUpdatesInOrderOffTheMessageLoop` |
| M7 | the signal never sends `executeTool.cancel` | `TestExecuteToolSignalCancelsTheNestedCall` |
| M8 | route result drops the state | `TestVirtualModelRouteRunsInTheExtension`, `TestExtensionAPIVirtualModelRouteRunsInTheExtensionGo` |
| M9 | the tool declaration omits `prepares_loadout` | `TestRegisterToolSendsUpstreamExposureFields`, `TestExtensionAPIRegistrationsAtLoadGo`, `TestExtensionAPIExecuteToolOrchestrationGo` |
| M10 | state update ignores callable tools | `TestToolsReadsTheReplicatedCallableTools` |
| M11 | queued virtual model appends instead of replacing | `TestRegisterVirtualModelBeforeRunQueuesTheDeclarationWithoutTheRoute` |
| M12 | the result wire drops `structured_content` | `TestToolResultAndToolInfoCarryUpstreamFields`, `TestExtensionAPIStructuredResultsGo` |
| M13 | `executeTool` names the request as the caller | `TestExecuteToolRunsNestedCallsForTheCallingTool`, `TestExtensionAPIExecuteToolOrchestrationGo` |
| M14 | the register payload omits MCP servers | `TestRegisterMcpServerBeforeRunQueuesInRegisterPayload`, three conformance rows |
| M15 | `ExecuteTool` returns before queued updates are delivered | `TestExecuteToolDeliversUpdatesInOrderOffTheMessageLoop` |
| M16 | the route runs with a context that is not the request's | `TestVirtualModelRouteObservesCancellation` |
| M17 | an unknown tool's exposure is empty | `TestToolLoadoutTreatsAnUnknownOrUnsetToolAsDirect` (added after M17 survived) |
| M18 | the reply is applied without waiting for older state | `TestRegisterMcpServerReplyOutranksAnOlderStateUpdate` |

## Load test

CPU burners: 12 busy loops pinned to cores 4-7, the tests pinned to the same cores with `GOMAXPROCS=4` (cores 0-3 carry other lanes' burners).

- `go test -race -count=48` of the new SDK rows: ok, 79 s (before the stale-state fix; the fix is covered by the deterministic row above and the rerun below).
- `go test -race -count=48` of the new SDK rows, rerun after the stale-state fix: ok, 160 s.
- Conformance rows, race build: the fused placement `-count=24`: PASS; the strict and packed placements `-count=3`: PASS. An earlier attempt at `-count=24` for all placements exceeded 25 minutes because each strict and packed row builds and starts a Go process, and it produced the failures recorded above (the stale-state overwrite and the post-test error report), both fixed.

## Gaps and stubs other lanes must fill

1. Host wire `subprocess.ToolInfo` carries no `exposure`, `namespace` or `annotations` (`agent-session.ts:1449-1462` puts them on `getAllTools()` entries). The SDK's `ToolInfo` decodes them (`TestToolResultAndToolInfoCarryUpstreamFields`), but no conformance row can pass until `subprocess.ToolInfo` and `internal/codingagent.ExtensionToolInfos` fill them. Owner: 6D/6E.
2. Provider config `images` and `classifiers` (the implementations, `types.ts:1896-1898`) have no subprocess wire. The chat, image and classifier model entries pass through and are proved. Owner: 6B (provider objects) with 6D's wire.
3. `executeTool` end to end through a real Session (`agent-session-tool-orchestration.test.ts:76-84`, the `nestedCalls` record) needs lane 6E. The conformance rows use a stand-in ExecuteTool action that numbers nested calls `<parent>/<n>` and runs the fixture's own tools over the wire; the orchestrator's expected text, `helped | echo: hi | Tool run_tools not found`, is upstream's.
4. `Extension.replaceable` (`types.ts:2005`, `2209`): designed out for an authored extension; the CLI sets it on built-in inline extensions.
5. `docs/extension-sdk-surface.md` is generated from upstream `types.ts`; after the pin move check the Go column for the surfaces above and add `test/parity/sdk-surface.toml` entries where the Go name differs from the default rule (`Extension.RegisterMcpServer`, `Context.GetMcpServers`, `Context.ExecuteTool`, `Context.Tools`, `Context.GetSettings`, `ToolDefinition.PrepareLoadout`, `Extension.OnMcpServersChange`, `Extension.OnProviderStreamEvent`).

## Row text for `docs/extension-api-parity.md` (integrator)

| surface | mapping | evidence | status |
|---|---|---|---|
| `registerMcpServer`, `unregisterMcpServer`, `getMcpServers` | Go SDK: `RegisterMcpServer(name, McpServerConfig) error`, `UnregisterMcpServer`, `Context.GetMcpServers()`; before `Run` a registration goes in the register payload's `mcp_servers`, after load it is the `registerMcpServer` call whose reply lists every registered server; the list is read from `state.mcpServers` | `TestRegisterMcpServerBeforeRunQueuesInRegisterPayload`, `TestRegisterMcpServerAtRuntimeCallsTheHostAndRefreshesTheReplicatedList`, `TestRegisterMcpServerReplyOutranksAnOlderStateUpdate`, `TestGetMcpServersReadsReplicatedState`, `TestExtensionAPIRegistrationsAtLoadGo`, `TestExtensionAPIReplicatedStateAndLateRegistrationGo` | Go complete; Rust, Python and Node rows are their lanes' |
| `registerVirtualModel`, `unregisterVirtualModel` | `RegisterVirtualModel(VirtualModel) error`; declaration without the route in `virtual_models` or the `registerVirtualModel` call; the host's `virtual_model_route` request runs the route with the request's cancellation | `TestRegisterVirtualModel*`, `TestVirtualModelRoute*`, `TestExtensionAPIVirtualModelRouteRunsInTheExtensionGo` | Go complete |
| `getSettings` | `Context.GetSettings()` from `state.settings`; a host that sent none is an error (upstream's not-initialized getter) | `TestGetSettings*`, `TestExtensionAPIReplicatedStateAndLateRegistrationGo` | Go complete |
| tool `outputSchema`, `exposure`, `namespace`, `annotations`, `defaultActive`, `prepareLoadout`; `ToolInfo` fields | `ToolDefinition` fields, `ToolDecl` wire, `tool_prepare_loadout` request | `TestRegisterToolSendsUpstreamExposureFields`, `TestToolPrepareLoadoutRunsInTheExtension`, `TestToolLoadoutTreatsAnUnknownOrUnsetToolAsDirect`, `TestExtensionAPIRegistrationsAtLoadGo`, `TestExtensionAPIExecuteToolOrchestrationGo` | Go partial: `ToolInfo` fields wait for the host wire (gap 1) |
| result `structuredContent`, `isError`; tool events `parentToolCallId`, `structuredContent` | `ToolResult.StructuredContent`, `structured_content`; event data is a map | `TestToolResultAndToolInfoCarryUpstreamFields`, `TestExtensionAPIStructuredResultsGo`, `TestExtensionAPIToolEventsCarryParentAndStructuredContentGo` | Go complete |
| `ctx.tools`, `ctx.executeTool` | `Context.Tools()`, `Context.ExecuteTool`; `executeTool` and `executeTool.cancel` calls, `execute_tool_update` notify, `state.callableTools` | `TestExecuteTool*`, `TestToolsReadsTheReplicatedCallableTools`, `TestToolOrchestrationIsOnlyAvailableInsideATool`, `TestExtensionAPIExecuteToolOrchestrationGo`, `TestExtensionAPIExecuteToolUpdatesAndCancellationGo` | Go partial: the Session side is lane 6E (gap 3) |
| events `provider_stream_event`, `mcp_servers_change` | `OnProviderStreamEvent`, `OnMcpServersChange` | `TestOnProviderStreamEventAndMcpServersChangeRegisterUpstreamEventNames`, `TestExtensionAPIEventsGo` | Go complete |
| provider config chat, image and classifier entries | provider config is a JSON map | `TestExtensionAPIProviderModelTypesGo` | Go complete for entries; implementations are gap 2 |

Async contract (Go SDK, all realizations): `RegisterMcpServer`, `RegisterVirtualModel` and their unregister forms are synchronous host calls; upstream's are synchronous functions that throw, so the call returns after the host applied the change and its refusal is the returned error. `ExecuteTool` maps upstream's awaited Promise to a blocking call: it returns after the outcome and after every partial result reached `OnUpdate`, which runs on its own goroutine in order; without `Signal`, the calling request's cancellation ends the call with an error; a `Signal` replaces the request's cancellation (upstream runner.ts:980, `options.signal ?? signal`), so the call is sent without the calling request as parent, survives the request's cancellation, and its cancellation sends `executeTool.cancel`, after which the host's error outcome is returned. `virtual_model_route` is a host request the extension answers on its own goroutine, cancelled with the request. `tool_prepare_loadout` is a host request; a failure or a null answer leaves the loadout unchanged, as upstream's `undefined`.
