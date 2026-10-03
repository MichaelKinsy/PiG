# Lane port-99-f6e: AgentSession, nested tool calls, virtual models, orchestration (family 6, sub-lane 6E)

Branch `port-99-f6e`. Scope: `docs/plan/progress/family-6-split.md` section 3, lane 6E. Base: the 6D contract commit 906c8fe28, then merges of `staging/porter/pi-0.99.1` (families 1-4; needed for `agent.RunToolCall`) and the tip of `staging/port-99-f6d` (c73b7b8cd; the real `ToolContext`, `Runner.CreateToolContext`, `ExecuteToolOptions.Signal`, registrations). Upstream sources: `.upstream/v0.99.1/packages/coding-agent/src/core/{nested-tool-calls,virtual-models,agent-session,model-runtime}.ts`.

## Lead answers applied

Binding answers 1-4 of the lane brief are 6D/6F/6A/6G matters; none changes this lane's files. The header-only session question (answer 4) is 6A's.

## Test inventory (upstream 0.99.1 → Go)

| upstream | cases | Go |
|---|---|---|
| `test/nested-tool-calls.test.ts` | 6 | `coding/nested_tool_calls_upstream_test.go` (4 runner + 2 recorder) |
| `test/compaction-nested-calls.test.ts` | 1 | `internal/codingagent/compaction/nested_calls_upstream_test.go` |
| `test/suite/agent-session-tool-orchestration.test.ts` | 3 | `coding/session_tool_orchestration_upstream_test.go`: cases 1 and 3 ported; case 2 needs family 8's codemode/tool-search extensions, pending there (its rule is guarded by `TestToolsWithDefaultActiveFalseAreActiveOnlyWhenNamed`) |
| `test/virtual-models.test.ts` | 15 (7 ModelRuntime, 8 session) | `coding/virtual_models_upstream_test.go` |
| `test/suite/virtual-models.test.ts` | 13 | `coding/virtual_models_suite_upstream_test.go`: 12 ported; "projects the session once per request" (`:291-303`, a `vi.spyOn(buildSessionProjection)` count) needs a projection counter in 6A's `session_manager.go`: pending |
| `test/agent-session-concurrent.test.ts` | 2 changed | `coding/session_concurrent_upstream_test.go` (`steer`/`followUp` resolve `"queued"`) |
| `test/suite/agent-session-compaction.test.ts` | 1 changed | `coding/session_compaction_suite_upstream_test.go` (seeded session) + guard `TestManualCompactionResolvesAuthOnlyWhenPiSummarizesItself` |
| `test/suite/regressions/7150-rpc-prompt-during-compaction.test.ts` | 1 changed | `coding/session_prompt_disposition_upstream_test.go` (no Go port existed) + 3 guards for the other dispositions |
| `test/suite/regressions/5943-session-start-notify.test.ts` | 7 | unchanged behavior: upstream replaced a local `createUiContext` with the shared `createTestUiContext` helper; the theme part waits on family 9. No Go change (existing ports in `internal/codingagent/session_start_notify_upstream_test.go` keep their inputs and expectations) |
| `test/jev-router-example.test.ts` | 2 | pending: needs family 3's `typesafe` classifier provider, 6B's `Models.Classify` and 6F's Node SDK `registerVirtualModel` over the wire (`examples/extensions/jev-router.ts` runs in the Node runtime) |

Field stubs added in the red commit (data only, no behavior): `ai.NestedToolCallRecord`, `ai.NestedToolCalls`, `ToolResultMessage.NestedCalls` (`ai` and `agent`, decoded by `agent/message_json.go`, copied by `agent/messages.go`), `ParentToolCallID` on `agent.ToolExecution{Start,Update,End}Event` (`agent/agent.go`; family 4's package: cross-lane stub, see below).

Signature stubs (panic `not implemented`): `coding/nested_tool_calls.go`, `coding/virtual_models.go`, `Session.CallableToolNames` in `coding/session_loadout.go`. `PromptDisposition`, `QueuedInputDisposition`, `PromptOptions.PreflightResult func(PromptDisposition)`, `Steer`/`FollowUp` returning `(QueuedInputDisposition, error)` changed signature in `coding/session_prompt.go` (mechanical caller edits in seven existing test files).

## Red

`docs/plan/evidence/port-99-f6e.red.txt`: 39 new or changed Go tests, each run alone. FAIL for the intended reason: 36 (stub panics or missing behavior), PASS in red: `TestPromptReportsStartedForATurn`, `TestPromptReportsHandledForCommandsAndInputHandlers`, `TestSessionToolOrchestrationLeavesResultsWithoutNestedCallsUnchanged` (Go already faithful).

## Green

Commits: `6dcf40a81` (nested calls, dispositions, deferred compaction auth, tool loadout), `d383ba7fe` (ModelRuntime virtual models), `3ba556924` (Session routing, retry/state entries, limits model, summarization routing, restore), then the gate fixes.

Changes to ported or existing tests (each is a Go-harness correction, not an expectation change):
- `virtual_models_suite_upstream_test.go`: the `before_agent_start` handler returns a pointer (`*BeforeAgentStartEventResult`, the Go runner's result type); the suite session clears the agent session id because upstream's harness builds its Agent without one (`test/suite/harness.ts:191`), otherwise faux prompt-cache usage doubles the reported context tokens.
- `session_prompt_disposition_upstream_test.go`: the event slice is read under the harness mutex (a data race under `-race`).
- `session_provider_prompt_test.go`: 0.99.1 de-duplicates active tool names (`agent-session.ts:1502`), so a repeated name lists once.
- `agent/tool_result_fields_test.go`: the field list includes `nestedCalls` (`ai/src/types.ts`).

Known differences (for the reviewer): `turn_end` tool-result copies carry no `nestedCalls` (the agent copies the message before the record is applied; the persisted and `message_end` messages do); `nestedCalls` is written after `timestamp` in session JSON; `prepareLoadout` hooks run under the tool registry lock.

Open: 6D's `TestSessionToolCallsGetAToolContextForTheirCallID` expects nested id `<id>/0` from an unbound runner; the Session now binds `ExecuteTool`, so the id is `<id>/1`. Six oracle tests fail on their upstream-version guard until the pin moves (`TestRPC33AzureTickOrder`, `TestFauxAgentObservationOracle`, `TestTestFauxAgentObservationOracle`, `TestRPC33GoogleObservationMatrix`, `TestRPC33ObservationMatrix`, `TestRPC33ObservationMatrixAzure`).

Cross-lane stubs to report: `ai.NestedToolCall*` and `agent.ToolResultMessage.NestedCalls` (family 1/4 omitted them), `ParentToolCallID` on agent tool-execution events, `Agent.ToolExecutionMode`, `event_bridge.go` parent-id parameter, `Services.ensureSettings` (bare ModelRuntime services).
Ledgers for the integrator (not committed here): PORT_MAP rows for `nested-tool-calls.ts`, `virtual-models.ts`, `agent-session.ts` (partial), and the test-mapping rows for the ported files above.
