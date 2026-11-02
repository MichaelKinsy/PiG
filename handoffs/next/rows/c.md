# Rows of lane C (Pi durable commits after `5f312dac`, up to `e7a0df27`) for the integrator

Branch `fix/follow-c` (on `fix/follow-c2` @ `4eaae70a1e`). Delete this file after transcribing. Red commit `a1c73ab9c6` (ported tests; they use the new API and do not compile against the previous production code), green `4f472a342f`. Changelog fragments: `changelog.d/follow-c-s{1,2,3,4,5,7}-*.md`.

**Stored-format break (format-versions ledger, integrator):** `pi.live` 2 -> 3 (`run.turns`, `run.first`, `run.afterRun`, `runFailure.first`; migrates on read, giving a stored run `turns: 0`; this is Pi's own `migrate` in `live.ts`, e7a0df27 line 204, for every older stored version), `pi.generation` 1 -> 2 (`unoffered` slot flag; phases `finish`, `drain`), `pi.tool` 2 -> 3 (`unoffered` input), submission records may have `type: "continue"`. An older PiG leaves those tasks blocked and cannot read a conversation whose `pi.live` was migrated.

Not in this lane: the store half (`14b4c56e`, `6eb99ad6`: `storage/store/*`, `registry-reader`, `closeEnds` "session closed" observer ends; D-store). Contract rows E35-E37, `U-session-observers`, `U-harness-observers`, `U-harness-registry-reader`, `U-store-storage`, SC stay pending for D-store.

## (a) Upstream files

| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| packages/durable/src/codemode/codemode.ts | ported | durable/codemode (no import-time WASM load in Go) | test:durable/codemode/imports_test.go | `toCodemodeIdentifier` import path: Go has no barrel; the extension builds without starting a sandbox (the QuickJS wasm inflates lazily) |
| packages/durable/src/errors.ts | ported | durable/errors.go | test:durable/errors_test.go, durable/errors_pi_oracle_test.go#TestStorageRequestErrorAndSessionFailedMatchPi | `ConversationIdle`, `ReadAfterWrite(operation)` (field `Operation`) |
| packages/durable/src/harness/agent.ts | ported | durable/harness/agent.go, durable/harness_types.go | test:durable/harness/harness_turn_limit_test.go | `maxTurns` in `AgentState`/`AgentChange`/`Agent` with validation |
| packages/durable/src/harness/compaction.ts | unchanged-observable | durable/harness/compaction.go | test:durable/harness/harness_compaction_test.go#TestPiLiveMigrations | only the `pi.live` v3 tests changed |
| packages/durable/src/harness/define.ts | ported | durable/harness/define.go | test:durable/harness/harness_after_run_test.go | `runEndTask` -> `RunEndTaskOf` |
| packages/durable/src/harness/generation.ts | ported | durable/harness/generation.go, durable/core/turn/generation*.go | test:durable/harness/harness_after_run_test.go, harness_turn_limit_test.go, harness_continue_test.go, harness_tool_hooks_test.go; contract E32-E34, U-harness-* | phases `finish`/`drain`, `holdRun`, `endAnsweredRun`, `endAnsweredCutOff`, `takesContinuation`, `failNothingToAnswer`, turn limit, unoffered calls, `startRun(RunStart)`; `pi.generation` v2 |
| packages/durable/src/harness/harness.ts | ported | durable/harness/harness.go | test:durable/harness/harness_submit_in_commit_test.go | `submit(tx, draft)` -> `Conversation.SubmitIn`/bound handle (Go has no overloads) |
| packages/durable/src/harness/inbox.ts | ported | durable/harness/inbox.go | test:durable/harness/harness_continue_test.go | `BoundaryResult.First` |
| packages/durable/src/harness/live.ts | ported | durable/harness/live.go | test:durable/harness/harness_live_deltas_test.go, harness_compaction_test.go#TestPiLiveMigrations | `LiveRun`, `RunFailure.First`, `pi.live` v3, `nothing_to_answer`/`turn_limit` |
| packages/durable/src/harness/scheduler.ts | ported | durable/harness/scheduler.go | test:durable/harness/harness_after_run_test.go | `entries` runtime read, `endedByCrashes` (Go marks the commit context: a WeakMap on the returned state object has no Go equivalent) |
| packages/durable/src/harness/submissions.ts | ported | durable/harness/submissions.go | test:durable/harness/harness_continue_test.go, harness_submit_in_commit_test.go | continue, `whenIdle`, `admit`, `traced` |
| packages/durable/src/harness/task-graph.ts | unchanged-observable | durable/harness/task_graph.go | test:durable/harness/harness_task_graph_test.go | only the D-store `closeEnds` hunk changed after 5f312dac |
| packages/durable/src/harness/telemetry.ts | ported | durable/harness/telemetry.go | test:durable/harness/harness_telemetry_test.go, durable/telemetry_pi_oracle (TestDurableTelemetrySchemaMatchesPi) | span description, `continue` value |
| packages/durable/src/harness/tool.ts | ported | durable/harness/tool.go | test:durable/harness/harness_tool_hooks_test.go, tool_pi_oracle_test.go#TestToolResultHelpersMatchPi | `reject`, `settleFromSlot`, `afterHarnessResult`, unoffered; `pi.tool` v3 |
| packages/durable/src/harness/types.ts | ported | durable/harness/types.go, durable/harness_types.go | test:durable/harness/harness_after_run_test.go | `SubmissionDraft` continue/`WhenIdle`, `HookApi.Entry/Entries`, `RunSummary`, `RunEndTask`, `GenerationHooks.AfterRun` |
| packages/durable/src/harness/util.ts | ported | durable/harness/util.go | test:durable/harness/harness_hook_reads_test.go | `conversationEntries` |
| packages/durable/src/harness/view.ts | unchanged-observable | durable/harness/view.go | test:durable/harness/harness_view_test.go | only the D-store `closeEnds` hunk changed |
| packages/durable/src/index.ts | ported | durable/types.go, durable/errors.go | test:durable/types_upstream_test.go | exports `ConversationIdle`, `LiveRun`, `RunEndTask`, `RunSummary`, `runEndTask` (`registryReader` is D-store) |
| packages/durable/src/session/session.ts | ported | durable/session/session.go | test:durable/session/session_tx_place_submission_test.go | `ownsTransaction` -> `OwnsTransaction` (`closeEnds` hunk is D-store) |
| packages/durable/src/session/transaction.ts | ported | durable/session/transaction.go | test:durable/session/submission_change_internal_test.go | `Context`, `HasTableWrite`, ownership brand, message "placed input or continue" |
| packages/durable/src/types.ts | ported | durable/types.go | test:durable/types_upstream_test.go | `SubmissionRecord` continue, `TaskRuntime.Entries` (Observer doc is D-store) |

Beyond Pi's TS: `durable/core/**` (Go core engine, cf host facade, ts control, contract corpus E32-E34 + U rows un-pended; `durable/core/host/PROTOCOL.md` section "Pi 7e15f960..e7a0df27 additions", `docs/plan/durable-core/ABI.md`, `CONTRACT.md`).

## (b) PORT_MAP rows

All rows keep their targets; the new Go files are `durable/core/turn/generation_afterrun.go` (core form of `afterRun`) and the test files below.

## (c) Upstream tests

| upstream test | disposition | PiG test |
|---|---|---|
| codemode-imports.test.ts | ported | durable/codemode/imports_test.go |
| harness-tool-hooks.test.ts (17 cases) | ported | durable/harness/harness_tool_hooks_test.go |
| harness-tools / harness-nested-tools(-restart) hunks | ported | durable/harness/harness_tools_test.go, harness_tools_progress_test.go, tool_task_v2_test.go, tool_task_v1_records_test.go |
| harness-continue.test.ts | ported | durable/harness/harness_continue_test.go (13) |
| harness-turn-limit.test.ts | ported | durable/harness/harness_turn_limit_test.go (11) |
| harness-submit-in-commit.test.ts | ported | durable/harness/harness_submit_in_commit_test.go (24) |
| harness-hook-reads.test.ts | ported | durable/harness/harness_hook_reads_test.go |
| harness-after-run.test.ts | ported | durable/harness/harness_after_run_test.go (TestAfterRun 27, TestEndRun 2); the `{tasks: "x"}` hook case is designed-out: `RunEndTasks.Tasks` is typed, so Go rejects it at compile time |
| harness-compaction (pi.live v3), -generation*, -live-deltas, -view-run, -ownership, -scheduler-retention, -restart-failure, -run-failure, -structured, -input-entries, -submissions, -telemetry hunks | ported | the same-stem durable/harness/*_test.go files |
| session-* / spec-usage / errors hunks | ported | durable/session/*_test.go, durable/errors*_test.go, durable/error_classes_test.go |
| examples 32, 33, 34 | ported | durable/examples/{tests_after_edits,turn_limit_continue,submit_in_commit}_example_test.go |
| durable/test/store-storage*, harness-registry-reader, examples 35-37 | not in this lane | D-store |

## (d) Async-contract rows

| source | contract | evidence |
|---|---|---|
| generation `finish`/`drain` | `afterRun` hooks run off the line once; the requested tasks are created together as the generation's children in the commit moving to `drain`; the run (and the conversation) stays busy until they are terminal; a crash before creation asks again, after never; cut off `restartLimit` times the run ends `completed` and reports `task_interrupted` | harness_after_run_test.go |
| `Conversation.SubmitIn` | admission inside the caller's commit before its first table write (`ReadAfterWrite` after); stored or lost with that commit; rejection throws before any write; the invocation-bound form also fails once the invocation is aborted | harness_submit_in_commit_test.go |
| tool task results | `afterTool` runs on every result the task writes, hooks skipped past the restart limit and in an abort handler's last attempt | harness_tool_hooks_test.go |
| turn limit | the round reaching `maxTurns` runs its tools, then the run ends `unanswered` `turn_limit` like a failure (`afterFailedRun`); a continue resumes | harness_turn_limit_test.go |

## (e) New interface IDs (public Go API)

`durable.SubmissionTypeContinue`, `durable.WhenIdle`, `durable.WhenIdleReject`, `durable.ConversationIdle`, `durable.NewConversationIdle`, `durable.ReadAfterWrite.Operation` (was `Method`), `durable.ConversationHandle.SubmitIn`, `harness.Conversation.SubmitIn`, `durable.TaskRuntime.Entries`, `harness.HookApi.Entry/Entries`, `harness.AgentState.MaxTurns`, `harness.AgentChange.MaxTurns`, `harness.LiveRun.Turns/First/AfterRun`, `harness.RunFailure.First`, `harness.RunNothingToAnswer`, `harness.RunTurnLimit`, `harness.GenerationHooks.AfterRun`, `harness.RunSummary`, `harness.RunEndTask`, `harness.RunEndTasks`, `harness.RunEndTaskOf`, `harness.ToolSlot.Unoffered`, `session.Session.OwnsTransaction`, `session.Transaction.Context/HasTableWrite`.

## Known open

- `TestStreamedPartialsKeepAWatchersMemoryProportionalToTheText` (durable/harness) fails on the pristine base as well (timing under load); separate lane.
- cf facade `examples-types` fails on `registryReader` (D-store examples 35-37) and `env/decode.ts` (present on the base).
