# Rows of N-DUR slice harness (Pi e5c6182b, d280e1ff, 35dfa757 final state at 6d4b7443)

Branch `fix/follow-ndur-harness` on `fix/follow-ndur` @ 9f56026a3a. Nothing was compiled or run: the Session line (writer, `Touch`, `CommitInternal`, `LineStorage`, `OnJobEnd`, `KnownLatestEntryId`, `Hooks.MembershipKeys`, `Transaction.CreatedTaskIds`) is not on this branch yet; the production code is written against the Go forms NDurSession announced. Every claim below is "written, not run".

## Red record
- d280e1ff: red commit 18a6b5aa65 (tests only: `ProviderRequest`, `GenerationHooks.BeforeProviderRequest` undefined). The base did not compile at all (`durable/session`, `durable/storage/sqlite` fail on the new `Storage.Commit`), so no failing test line exists; port commit a5436df516, fix 4th commit "providerPayload returns nil when no hook replaced the body".
- e5c6182b: the port (48f0603761) was committed BEFORE its tests (04f1a10c76, 3e9753de0c) because the sibling Session did not compile. Not red-proven. Red proof still owed: run the ported cases against the pre-port scheduler.

## (a) Upstream files
| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| src/harness/scheduler.ts | ported, unverified | harness/scheduler.go, scheduler_region.go, scheduler_idle.go, scheduler_index.go | test:harness/scheduler_retention_chains_test.go#TestSchedulerRetentionChains (changed case), harness_ownership_tools_test.go (changed case) | see design note |
| src/harness/context.ts | ported, unverified | harness/context.go | test:harness/context_kept_range_test.go#TestKeptContextRanges, context_range_test.go | `current`, `rangeEntry`, `advanceRange`; bounds from `Session.LatestEntryId/LatestHeadMarker` |
| src/harness/generation.ts | ported | harness/generation.go | harness_tools_test.go#TestGenerationHooks | beforeProviderRequest; attempt stored as ProgressCommit; cutoff from `tx.LatestEntryId` |
| src/harness/tool.ts | ported, unverified | harness/tool.go | harness_live_deltas_test.go | running slot + nested arguments in a ProgressCommit, intent commit separate; safe rerun re-shows running (arguments in sorted member order: Go decodes the checkpoint without member order) |
| src/harness/harness.ts | ported, unverified | harness/harness.go | - | `ReadOnLine(func(storage))`, `MembershipKeys` hook, no storage in components |
| src/harness/submissions.ts, task-graph.ts, view.ts | ported, unverified | harness/{submissions,task_graph,view}.go | existing tests | `NewSubmissions/NewTaskGraphView/NewConversationViews` lose the storage parameter |
| src/harness/types.ts | ported | harness/types.go | harness_tools_test.go | `ProviderRequest`, `ProviderPayload`, `BeforeProviderRequest` |
| src/types.ts, index.ts hooks | no Go change needed beyond the contract commit | - | - | - |
| tools/read.ts, search.ts, tool-search.ts, codemode.ts | pending (not examined): carried by M0 `dp/fix/060c-integrate` (5eb8bb963a ed99f429d6 ...) | - | - | not ported twice |

## (b) PORT_MAP: scheduler.ts row gains `scheduler_region.go`, `scheduler_idle.go`; add `harness/context_kept_range_test.go`.

## (c) Tests
- Ported: harness-tools (2 new hook cases), harness-context-range (new), harness-context (row counts 0/21/22), harness-blocked, -cancellation-barrier, -lifecycle, -tool-hooks, -nested-tools, -restart-failure (commits wrappers, `<=` retention bound), -ownership (refusal wrappers; replaced case "runs a handle operation queued behind the abort mark..."), -scheduler-indexes (commit counted per batch), -scheduler-retention (both changed cases), -structured (refusing wrapper), -live-deltas (5 commits, per-batch kinds, persistWithinMs 50), -view (+1 frame), -observers/-task-graph (adopting=false), -inbox, -submissions (reopen), -deferred-progress, -generation (persistWithinMs 100), -storage-failure (already had the note append). All Storage wrappers (harness + codemode tests) take batches; `controlledStorage` and `lateResolvingStorage` follow session-support.ts; `refusingAt` added.
- MISSING (pending): NEW harness-group-commit.test.ts (796), NEW harness-idle-scopes.test.ts (591; port plan: `idleState()` is `TaskScheduler.idleState()`, `SchedulerIdleState`), harness-tasks (holdLine, CountingStorage documentScans/`documentCacheSizes.taskDocuments`, publication-at-adoption cases), harness-tools-recovery (+64 safe-tool rerun case), `harness-submissions`/`harness-task-graph` done, benches nested-images/nested-tools/scheduler-fanout/tool-output, codemode/search/tools-read differential (carried), the StoreStorage re-run of harness tests (mechanism proposal: a `PIG_HARNESS_STORAGE=store` factory read by `openChat/openTasks` once NDurChord's `StoreStorage` opener exists, excluding the four cases of vitest.store.config.ts).
- Existing Go tests that assume 14 commits per run / old counts were only updated where Pi's diff changes them (live-deltas, view, restart-failure, context rows); `harness_support`-style commit-count assertions elsewhere were not searched.

## Region design note
`regionLocked(s, decide, refresh)` (scheduler_region.go): on the job's own goroutine, under `s.mu`: set `s.reading`, run `decide` (no state change, helpers call `recordLocked(key)`), `Session.Touch` every recorded key under the same hold of `s.mu` (so no `observe` runs between decide and touch; NDurSession removes a part's keys after its internal listeners), if any gate: drop plan/error, release `s.mu`, wait all gates, `AssertHealthy`, relock, `refresh` (`loadScopesLocked(false)`), rerun. Side effects (`setTask`, `runnable`/`cascadePending`, `endLocked`, `resolveLocked` with migrate, reports, `SettleOutcome`) only after the clean check. No goroutine per region. Rule: while holding `s.mu` never call anything that can wait for a gate (tx reads, `LineStorage` reads, `SettleOutcome`, `WithdrawInputs`): `loadChainLocked/loadScopesLocked/lineTaskLocked` and `withoutLock` release `s.mu` around them. Sites: abort, waitForTask, reconcile (`marksLocked`, withdrawn, finalize rounds), reserve (`planReservationLocked`, abandoned), step (`rulesLocked`/`progressLocked`), terminate, commitState, validateWait (`checkWaitLocked` with `ended`), OwnedTasks, gated. `MembershipKeys` runs at admission (Session `Hooks.MembershipKeys`).
Idle (35dfa757 final state, structurally Pi's): `addScopesAboveLocked`, `idleScopesLocked`, `settleIdleScopes`, `expireContextsLocked`, `markIdleLocked` (sort on clock going back), `runsNowLocked`, `earliestSleepLocked`; the integrator resolves the overlap with the carried commits ad6d659c09/fefd5ca8a3.

## (d) Async contracts
- scheduler.go: regions block the calling job's goroutine (Pi: awaits on the line); waits are released by publication of the keys' writers; Session failure surfaces through `AssertHealthy`; `observe` still settles idle waits for good and wakes via `DeferUntilPublished`; evidence: none run.
- harness.go/submissions.go/task_graph.go/view.go/context.go: `ReadOnLine(func(storage))` blocks until the job ends; no new goroutines.
- generation.go: `onPayload` runs synchronously inside the provider call; hook errors are reported by `Hooks().Each`.

## (e) New public Go API
`harness.ProviderRequest`, `ProviderPayload`, `GenerationHooks.BeforeProviderRequest`, `TaskScheduler.MembershipKeys`, `SchedulerIdleState`; `CaptureContextBounds(ctx, session, storage, id, at, visible)`; `NewSubmissions/NewTaskGraphView/NewConversationViews` without storage.

## Open points
- `durable/core/**` (its own session/driver emulation, `driver/hook.go` has no `beforeProviderRequest`, wire PROTOCOL.md) was not touched.
- Needs from siblings: `Session.{Touch, AssertHealthy, CommitInternal, CommitInternalWithTicket, LineStorage, OnJobEnd, KnownLatestEntryId, LatestEntryId, LatestHeadMarker}`, `session.Hooks.MembershipKeys`, `Transaction.CreatedTaskIds`, `durabletest.CommitWrites` (storage branch).
- Unverified inference: kept-range row counts in `harness_context_ranges_test.go` retention case (6/0/6) and `rooted 21/22`.
