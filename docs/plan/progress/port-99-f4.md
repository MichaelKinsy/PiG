# Lane port-99-f4: family 4, agent (upstream 0.87.1 to 0.99.1)

Branch `port-99-f4`, from staging `porter/pi-0.99.1` (40001cd42). Scope: plan Phase 2 step 4, `packages/agent` (`runToolCall`, `thinkingLevel`, structured results, `outputSchema`, `onProviderStreamEvent` forwarding) and the experimental Pico3 view/session/legacy-tracker tests.

## Environment

The published upstream 0.99.1 packages were installed in a scratch directory (a scratch directory, `--ignore-scripts`, per-command `--min-release-age=0`, owner approval for this pin only; no global npm config touched). The agent-loop `thinkingLevel` behavior was probed against `@earendil-works/pi-agent-core` 0.99.1: `message_start` carries no `thinkingLevel`; `message_end`, the `agent_end` transcript and the returned messages carry it as the last key (after `timestamp`); an undefined `reasoning` records `"off"`. No test in this scope reads hydrated catalog data.

## Upstream test delta in scope

Changed test files (SHA differs from 0.87.1): `agent-loop.test.ts` (+2 cases, `runToolCall`), `agent.test.ts` (+1 case, provider stream event forwarding), `harness/pico3/spec-view-events.test.ts` (1 case rewritten). New: `harness/pico3/legacy-tracker.test.ts` (1 case). Nothing else under `packages/agent/test` changed.

| upstream case | Go test | note |
|---|---|---|
| agent-loop `runToolCall` validates, runs the hooks, reports failures as error outcomes | `TestRunToolCall_ValidatesRunsTheHooksAndReportsFailuresAsErrorOutcomes` | Go hooks take no message/context argument, so upstream's `assistantMessage`/`context` options are not modeled; the update sink is Go's `ToolUpdateCallback` |
| agent-loop `runToolCall` lets afterToolCall replace structured content | `TestRunToolCall_LetsAfterToolCallReplaceStructuredContentAndDropsItWhenOnlyContentIsReplaced` | |
| agent "forwards provider stream event observers through AgentOptions" | `TestAgent_ForwardsProviderStreamEventObserversThroughAgentOptions` | |
| pico3 spec-view-events "a self-head commit rewrites one transcript entry..." | `TestViewSelfHeadCommitRewritesOneTranscriptEntryWithMatchingEntryHeadEvents` | replaces the 0.87.1 splice test; upstream asserts only the applied result, which the old Go code also met, so `TestViewSelfHeadCommitPublishesFieldRewritesNotATranscriptSplice` is the red-proof guard |
| pico3 legacy-tracker "preserves draft-sourced descendant edits..." | `TestTrackerPreservesDraftSourcedDescendantEditsWithoutExposingTrackerMetadata` | Go maps alias on assignment where Chord drafts copy; the port copies at the two assignments. `legacy-tracker.ts` itself has no Go home: Go's `Tracker` is already flush-based. Passes before green (guard, not red) |

Tests added beyond upstream (regression guards, each red before green): `thinking_level_test.go` (5), `structured_result_test.go` (3).

## Stubs other families must know about

- `ai.StreamOptions.OnProviderStreamEvent func(ctx, data any, model *Model) error`: signature only. Family 2 owns the provider call sites and may change the signature; `agent.AgentOptions.OnProviderStreamEvent` forwards it.
- `ai.AssistantMessage.ThinkingLevel` (json `thinkingLevel,omitempty`): field only.

## Red

Commit: the `(red)` commit on this branch (`test(agent): ... with signature stubs (red)`).

`go test ./agent/...` on the red tree: 9 failing tests in `agent` (`RunToolCall` x2, forwarding x1, structured-content rule x1 with 5 failing subcases, thinking-level loop x3, JSON x1, `LLMMessage` x1) and 1 in `agent/harness/pico3` (`TestViewSelfHeadCommitPublishesFieldRewritesNotATranscriptSplice`). Every other test in `./agent/...` passes; `go build ./...` and `go vet` of the touched trees pass.

## Green

Commit: the `(green)` commit on this branch (`feat(agent): ... (green)`). The tests of the red commit are unchanged; four existing test files only pass the new `consumeStream` thinking argument (`""`, recorded as "off").

Behavior and upstream citations (`.upstream/v0.99.1/packages/agent/src/`):

- `RunToolCall` (`agent/run_tool_call.go`): `agent-loop.ts:789-820`. The loop and `RunToolCall` share the free functions `prepareToolCall`, `executePreparedToolCall` and `finalizeExecutedToolCall` (`agent/tool_execution.go`), as upstream shares its helpers with `config: ToolCallHooks` and a `tools` argument (`agent-loop.ts:707-716`). `ToolCallHooks` also carries Go's `PrepareToolResult`, which upstream folds into `afterToolCall`. `RunToolCallOptions` has no `assistantMessage` or `context` because Go hook signatures carry neither.
- Structured results: `AgentToolResult.StructuredContent` and `AfterToolCallResult.StructuredContent` (`types.ts:82-97, 425-445`); the after-hook rule and its `??` treatment of a JSON null (`agent-loop.ts:877-889`); the tool-result message omits it (`agent-loop.ts:922-935`); a result with `IsError` is an error outcome that keeps its details (`agent-loop.ts:840`); `OutputSchemaProvider` is the optional interface for `outputSchema` (`types.ts:469-473`). Nothing in the agent consumes `OutputSchema` yet; the MCP and codemode lanes are its callers.
- `thinkingLevel` (`agent-loop.ts:408-409, 445, 460`): recorded on the final response of every request (done, error, aborted), `"off"` when the level is empty, not on `message_start` or updates, serialized after `timestamp` in `agent.AgentMessage` JSON and in the RPC assistant record (`cmd/pig/rpc_events.go`). Probed against the published `pi-agent-core` 0.99.1.
- `AgentOptions.OnProviderStreamEvent` forwards to every request (`agent.ts:122, 200, 240, 475`).
- Pico3 view: `appendViewEntries` edits the tracked entries in place and the tracker publishes the change (`harness/pico3/view.ts:90, 337-347`). `session.ts` changes (`plain(state)`, optional config fallback) are Chord draft mechanics; `tx.go` already reads config through `defaultOf` with an explicit `hasFallback`, so no Go change is needed.

Mutation checks (revert, see red, restore): `thinkingLevel` assignment removed (3 tests fail); structured-content assignment removed (2); content-only keeps structured content (2); a JSON null not treated as absent (1); `OnProviderStreamEvent` not forwarded (1); `RunToolCall` skipping the after hooks (2); `RunToolCall` skipping the before hooks (1). The view change is proven by the red run of `TestViewSelfHeadCommitPublishesFieldRewritesNotATranscriptSplice` against the old explicit-splice code.

Load: `-race -count=24` of `./agent` and of the pico3 view/tracker/watch tests, under `GOMAXPROCS=4` pinned with `taskset -c` to 4 cores shared with 6 CPU burners: pass. `go test -race ./agent/...`, `go vet ./...`, `GOOS=windows go vet ./agent/... ./ai/`, golangci-lint (0 issues) and `make divergence-guard` pass.

## Open items for the lead

1. Stale 0.87.1 oracles. Nine tests replay checked-in oracle fixtures recorded from 0.87.1 whose assistant messages have no `thinkingLevel`, and now fail because Go correctly emits it: `TestFauxAgentObservationOracle`, `TestTestFauxAgentObservationOracle`, `TestRPC33ObservationMatrix`, `TestRPC33GoogleObservationMatrix`, `TestRPC33ObservationMatrixAzure` (package `coding`), and `TestFauxRPCObservation`, `TestTestFauxRPCObservation`, `TestRPCMistralMatchesPi`, `TestRPCPiMessagesMatchesPi` (package `cmd/pig`). With the field removed from the Go output all nine pass, so this is the only difference. The probes (`coding/testdata/rpc33-observation/probe.mjs` and `providers/*/probe.mjs`) assert the 0.87.1 and OpenAI 6.40.0 pins, so re-recording them belongs to the central pin move together with family 2's OpenAI 7 wire changes; I did not edit or loosen them.
2. Parity families (`make parity-family`) do not run in this worktree: `parity-deps` refuses the shared `extensions/sdk-ts/node_modules` path and asks for the owning checkout. Run `experimental-pico3`, `rpc`, `print`, `json`, `compaction` and `tools` at merge; RPC and JSON scenarios that print an assistant message will need the 0.99.1 comparator because of `thinkingLevel`.
3. Ledger rows this lane did not touch (central regeneration): the async contract for `runToolCall`, PORT_MAP status of `agent-loop.ts`, `types.ts`, `agent.ts`, `harness/pico3/legacy-tracker.ts` (no Go home: Go's `Tracker` is already flush-based, so the row is designed-out with that reason), test-mapping hashes.
4. `TestNodeVendoredTuiUpstreamTests/native-clipboard-linux` in `coding/extension/host/subprocess` needs Xvfb, which this host lacks (unrelated to this lane).
