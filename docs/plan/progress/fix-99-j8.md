# fix-99-j8: codemode-default fallout, parity false pass, ordered tool results

Base: staging `porter/pi-0.99.1`. Pi source: `.upstream/v0.99.1`.

## Red run

Recorded before any production change (Go 1.27.1, temporary HOME):

| Test | Symptom |
|---|---|
| `cmd/pig` `TestRPCPlanModeEmptyToolsSurviveProcessResume` | snapshot `toolsBeforePlanMode:[codemode tool_search]`, want `[]` |
| `cmd/pig` `TestSystemPromptOptionsThroughHeadlessStartup/rpc/*` | `selectedTools` lists `codemode`, `tool_search` |
| `cmd/pig` `TestLinkedPackagesCompileNoRegexpAtInit` | `regexp.MustCompile` at `tool_codemode_renderer.go:34`, `toolsearch.go:53-56` |
| `test/parity/runner` `TestResolveUpstreamPiBinFailsWhenNotInstalledByDefault`, `...OverrideIsMissingByDefault`, `TestResolvePigBinFailsWhenUnsetByDefault`, `...MissingByDefault` | helper skips instead of failing |
| `test/parity/cmd/checkparityran` `TestEveryParityTargetRequiresScenariosToRun/{parity-family,parity-driver}`, `TestQCSmokeRequiresScenariosToRun` | no results file, no `require-parity-ran` |
| `cmd/pig` `TestNodeExtensionSeesToolExecutionResultInInsertionOrder` | Node sees `{"text":"42\n","type":"text"}`, Pi `{"type":"text","text":"42\n"}` |
| `internal/codingagent` `TestToolExecutionEventsKeepInsertionOrderOnTheWire`, `TestDispatchAgentLoopEvent_DeliversAllAgentLoopEvents`, `TestToolResultEmptyTextHookRoundTrip`, `TestToolResultExtensionContentPreservesAndClearsImages` | args, content blocks sorted alphabetically on the wire |

Tests that asserted the Go representation (`map[string]any` for args, result and content blocks) now assert the wire JSON that a subprocess extension receives; Pi has no Go-side representation to preserve.

## Green

| Item | Change |
|---|---|
| 1, 2 | The j6 lane landed `coding.ExtensionToolStartsActive` for the same defect while this lane ran. Its version is the one on the branch after the merge. Pi: `defaultActive: false` at `extensions/codemode/index.ts:41` and `extensions/tool-search/index.ts:14`; activation at `agent-session.ts:3487-3519`. |
| 3 | `tool_codemode_renderer.go` and `toolsearch.go` use `lazyregexp.New`. |
| 4 | `ResolvePigBin` and `ResolveUpstreamPiBin` fail when the binary is missing; `PIG_PARITY_ALLOW_ZERO=1` skips. `parity-family`, `parity-driver` and `qc-smoke.sh` read a results file and fail on zero executed scenarios (other targets already did). The scoped results file lives under `tmp/parity-results/` of the worktree, so a concurrent run elsewhere cannot supply the scenarios a run counts. |
| 5 | `ToolExecutionStartEvent`, `ToolExecutionUpdateEvent` and `ToolExecutionEndEvent` carry `WireArgs`, `WirePartialResult` and `WireResult` (`json:"-"`) and marshal them in place of the Go maps. In-process Go handlers still see the maps (the in-process conformance reference asserts them). The end result keeps `content`, `details`, `structuredContent`, `usage`, `isError`, `terminate` in the order `packages/agent/src/types.ts` declares them; a tool that spreads its own result can reorder them in Pi, which Go cannot know. |
| 6 | `WireModelOperations` subscribes with `ModelRegistry.ObserveChanges`, which composes with the single `SetChangeListener` slot. |
| 7 | `piglet_extension_tool_scope_test.go` waits for the `get_state` response (RPC) and for the startup extension listing (interactive) instead of fixed sleeps. |

Test representation: my red commit first replaced two in-process map assertions (`event_bridge_dispatch_test.go`, `tool_result_content_test.go`) with wire JSON. The green commit restored them, because `test/extension-conformance` `TestProductionToolExecutionPayloadsMatchAcrossSDKs/inproc-go` fixes the in-process contract; the wire assertions live in `event_bridge_wire_order_test.go` and in the real-binary `TestNodeExtensionSeesToolExecutionResultInInsertionOrder`.

## Evidence

- `go build`, `go vet`, `GOOS=windows go vet` (cmd/pig, coding, internal/codingagent), gofmt, `go fix -diff`, golangci-lint (0 issues).
- `go test ./coding ./internal/codingagent ./cmd/pig` pass; `./test/extension-conformance` passes (854 s). `./coding/extension/...` passes except `TestNodeRuntimeShimsExportEveryPinnedPiValue` and `TestNodeVendoredTuiUpstreamTests/native-clipboard-linux`. The first passes once `.upstream/current` points at v0.99.1; the second needs Xvfb, which this host lacks.
- Load: `GOMAXPROCS=4 taskset -c 0-3` with four CPU burners: `-race -count=24` on the wire-order, dispatch and change-listener tests; the tool-scope test `-count=4`.
- `make parity-family FAMILY=json`: 4 scenarios ran. With `PIG_PARITY_PI_BIN=/nonexistent` the run fails; with `PIG_PARITY_ALLOW_ZERO=1` it reports the opt-out.
- Mutation: each fix's red run is its mutation (the fix reverted); the change-listener test fails with `SetChangeListener` restored.

## Not done

- `tool_result` events (`ToolResultEventBase.Content` and `Input`) still reach a Node handler with sorted keys. The same dual representation is needed, but `ToolResultEventBase` is embedded in every variant, so a `MarshalJSON` on it would hijack the outer struct's marshaling. It needs its own design.
- `partialResult.content` of `tool_execution_update` is a string in PiG and an array of blocks in Pi (`agent-loop.ts:779-786`, `AgentToolResult`). The conformance oracle asserts the string form. It is a separate drift.
