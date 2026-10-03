# fix-99-iserror: tool result `isError` on the RPC/JSON wire, and the fresh-prompt tool loadout

Base `9b925f30d` (`rev-fix-99-j5-rpc`). Oracle: upstream 0.99.1 (`extensions/sdk-ts/node_modules/.bin/pi`, `make parity-deps`). Every Pi run used a temporary `HOME` and `PI_CODING_AGENT_DIR`. Pig binaries ran with a temporary `HOME`, `PIG_HOME` and `PIG_CODING_AGENT_DIR`.

The lead's answer moved the push target to `fix-99-iserror` and this file. The extra item `TestUpstreamCodemodeModels` moved to port-99-f6h-model-types, which owns classify.

## Item 1: `result.isError` on tool_execution_end

upstream 0.99.1 keeps two flags apart. `event.isError` says the call failed. `result.isError` exists only when the tool RETURNED a failure (`.upstream/v0.99.1/packages/agent/src/types.ts:436-441`; `coding-agent/src/core/tools/bash.ts:403-409`; `extensions/codemode/execute.ts:318`; `extensions/mcp/tools.ts:224`). A thrown error, an unknown tool, invalid arguments, a blocked call and a throwing hook give `createErrorToolResult`, `{content, details: {}}` with no isError (`agent-loop.ts:748-752,856-861,902-910`). `afterToolCall`'s `isError` replaces only the call's flag (`agent-loop.ts:890`). The wire writes `result: finalized.result, isError: finalized.isError` (`agent-loop.ts:912-919`).

Probe (upstream 0.99.1, `--mode json`, test-faux twin, prompt `Run: bash failing exit`, command `echo out; exit 3`):

    "result":{"content":[{"type":"text","text":"out\n\n\nCommand exited with code 3"}],"structuredContent":{"output":"out\n","truncated":false,"exit_code":3,"wall_time_seconds":0},"isError":true},"isError":true

The lead's task text and the previous review said afterToolCall "deletes and re-appends" structuredContent. The probe shows otherwise. With a `tool_result` handler that returns only `details`, Pi writes `content, details, structuredContent, isError`. `agent-loop.ts:878-889` spreads the old result over a new object, so every key keeps its position, and `result.structuredContent = structuredContent` assigns an existing key without moving it. Only a structuredContent the hook adds to a result that had none comes last. Pig models exactly that.

### Representation

- `agent.ToolExecutionEndEvent.IsError` is the call's flag (`event.isError`). `agent.AgentToolResult.IsError` is `result.isError`: the tool returned it.
- `AgentToolResult.Thrown` marks a result that stands for an error Pi's tool throws. The built-ins (read, write, edit, grep, find, ls, and the shell tool's aborts and timeouts) represented throws as `IsError` results with `details: {}`. The agent reports the call as failed and drops the result's own `IsError` and `Thrown`. A returned Go error has the same effect. Bash's non-zero exit is a returned error and is not marked.
- `AgentToolResult.StructuredContentAppended` records a hook-added structuredContent for the wire order.
- Hooks still see the call's flag in `result.IsError` (Pi's `context.isError` beside the result). `PrepareToolResult` may still change it.
- An empty `AfterToolCallResult` is Pi's `undefined` and leaves the result untouched.
- `cmd/pig/rpc_events.go` `rpcToolResult` writes content, details, structuredContent, isError, usage, terminate in the tool's order.
- The extension `tool_execution_end` payload carries structuredContent, isError, usage, terminate and the call's `isError`. The interactive card takes `{...event.result, isError: event.isError}` (`interactive-mode.ts:3539`). Nested call events use the outcome's flag (`nested-tool-calls.ts:240-247`).
- `ToolResultEventOverride` had dropped the chained structuredContent, so any `tool_result` handler removed it (`agent-session.ts:672-678`, `runner.ts:1187,1228-1234`). Fixed.
- `toolDefinition` dropped `agent.OutputSchemaProvider`, so codemode's `tools.bash()` threw on a non-zero exit instead of resolving to the structured result (`bash.ts:262`, `codemode/execute.ts:201-209`). Fixed as a separate green commit. It turns the existing failing port `TestUpstreamCodemodeOptionsAndStore/resolves bash calls to structured results, also for non-zero exit codes` green.

### Red

`docs/plan/evidence/fix-99-iserror-red.txt` (commit `0efc3aaff`). New tests: `agent/tool_result_iserror_test.go` (flags of thrown, panicking, unknown, invalid, blocked, hook-set and hook-cleared calls, Thrown marker, StructuredContentAppended, RunToolCall outcomes), `cmd/pig/rpc_tool_wire_test.go` (exact wire per Pi order, real-binary RPC process), `coding/nested_tool_call_events_test.go`, `internal/codingagent` (extension payload, interactive flag, override structuredContent), `TestBuiltinFailureDetails` (Thrown), and parity scenarios `json/05,06,07` and `rpc/42` against upstream 0.99.1 (the test-faux twins got the prompt `Run: bash failing exit` on both sides).

Re-ported tests, each with its 0.99.1 citation in the test: `TestRPCToolExecutionResultExactWire` (`agent-loop.ts:906-919`; it pinned the 0.87.1 shape), the two json_mode tool-end tests, `TestAgentLoop_DoesNotExecuteToolCallsFromLengthTruncatedMessage` (`agent-loop.test.ts:476` asserts `toolEnd.isError`), the RPC33 observation projection, the session admission ends, the tool-renderer event builders, and the two `agent_loop_upstream` "isError" checks that read the wrong flag.

### Mutation checks (each restored)

- Drop `isError` from `rpcToolResult`: `TestRPCToolExecutionEndWritesResultIsErrorUsageAndTerminateInPiOrder` fails.
- Drop the `Thrown` case: `TestAgentLoop_ToolExecutionEndKeepsResultIsErrorApartFromTheEventFlag/result_marked_Thrown` fails.
- Drop `IsError` from the emitted event: the agent flag tests fail.
- Drop the override's structuredContent: `TestToolResultEventOverrideCarriesStructuredContent` fails.
- Never set `StructuredContentAppended`: `TestAgentLoop_ToolExecutionEndRecordsAStructuredContentTheHookAppended` fails.
- Interactive ignores the event flag, the extension event reads the result flag, the nested emit drops the flag: `TestInteractiveToolEndRendersTheCallFlagNotTheResultFlag`, `TestDispatchToolExecutionEndDeliversTheFinalizedResultAndTheCallFlag`, `TestNestedToolCallEndEventsKeepTheResultFlagApartFromTheCallFlag` fail.
- Parity scenarios `json/05,06,07` and `rpc/42` failed on the red tree (pig lacked `result.isError`, and with a details hook lacked structuredContent) and pass on the green tree.

## Item 2: `TestRPCFreshPromptDeclaresStructuredSystemOnce`

upstream 0.99.1 (probed, `--mode rpc`, with and without `--no-extensions`) declares `[read, bash, edit, write]` in a fresh prompt. The codemode and tool_search built-ins register `defaultActive: false` (`extensions/codemode/index.ts:41`, `extensions/tool-search/index.ts:14`), so `agent-session.ts:3503-3506` does not activate them. `--tools read,codemode` declares `[read, codemode]`, and `--no-builtin-tools --tools tool_search` declares `[tool_search]`; naming a declarable tool activates it (`agent-session.ts:3487-3491`).

Pig was wrong. The Session registry applied the rule, but `rpcPrepare` (`cmd/pig/rpc_mode_runtime.go`) appended every extension tool to the startup loadout and `rpcSetInitialActiveTools` activated all of them. Fix: `coding.ExtensionToolStartsActive` holds Pi's rule once, and both the RPC prompt and the Session registry use it. The registry check also lacked the membership test for a named tool (`allowed != nil && declarable` for any tool). The same fix turns `TestRPCPlanModeEmptyToolsSurviveProcessResume` green (it saw `toolsBeforePlanMode:[codemode tool_search]` on the base).

Tests: `TestExtensionToolStartsActive`, `TestRPCStartupDeclaresOnlyTheToolsPiActivates`, and the existing test.

## Item 3: `newAnthropicTestProvider`

Not deleted. With the gate's build tags (`integration,live,parity`) `go tool golangci-lint run ./ai/` reports 0 issues: the helper is used by the `//go:build live` files. It is unused only without the `live` tag.

## Verification

- `go build ./...`, `go vet`, `GOOS=windows go vet ./agent ./cmd/pig ./coding ./internal/codingagent/...`, gofmt clean.
- `go tool golangci-lint run --build-tags=integration,live,parity ./agent/... ./cmd/pig/... ./coding/ ./internal/codingagent/...`: 0 issues. `go fix -diff` reports only `coding/extension/host/runtimecell/build_failure.go`, which is untouched here.
- `make parity-family FAMILY=json` and `FAMILY=rpc` pass against upstream 0.99.1.
- Load: `GOMAXPROCS=4 taskset -c 0-3` with four CPU burners, `-race -count=24` for the agent loop tests, the nested-call tests, the RPC wire and JSON tests and the codingagent dispatch tests; `-count=6` for the real-binary RPC process tests. All pass.

## Failures that predate this lane (same on base `9b925f30d`)

`TestInteractiveThemeSelectionPresenceMatchesPi`; internal/experimental and nativeplatform (radius source, X11, clipboard); `TestReplacedSession2860New`; `TestNodeRuntimeShimsExportEveryPinnedPiValue`, `TestPiThemeHelpersMatchThePinnedPackage`, `TestNodeVendoredTuiUpstreamTests`; `TestSelectedToolsPiContract`, `TestRustSDKVirtualModelRoute`, `TestPythonSDKVirtualModelRegistrationAndRouting`; `TestLinkedPackagesCompileNoRegexpAtInit` (toolsearch and the codemode renderer compile regexps at init); `TestUpstreamCodemodeModels` (moved to f6h); `TestUpstreamAgentSessionCodemodeTool/{runs nested calls in parallel, keeps structured content that tool_result handlers replace}` (the Node extension's `outputSchema` does not reach the nested tool view, so a script receives the text of `stats`).

## Deferred and known edges

- `--tools` order: Pi declares the tools in the order given (`--tools write,bash,read` gives `[write, bash, read]`); Pig orders them by registry. Found while probing item 2. Not fixed; it needs its own lane and a numbered divergence or a fix.
- The Node extension `outputSchema` does not reach `ToolContext` tools (see above). That is the Node SDK bridge (6F-node).
- Go cannot tell an explicit `isError: false` or `terminate: false` from an absent key, and cannot tell a hook that returns `{}` from one that returns `undefined`. Pi writes the explicit `false`; Pig omits it. Builtin producers only ever write `isError: true` (`codemode/execute.ts:318`, `mcp/tools.ts:224`, `bash.ts:408`).
- A hook-added `details` on a result that had no `details` key would be written after `isError` in Pi. Pig always writes it after content. No built-in returns a result without `details` together with `isError`.
- Order of a tool's own keys is fixed to content, details, structuredContent, isError, usage, terminate. An SDK tool's own key order does not cross the wire.
