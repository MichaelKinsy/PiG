# d-harness-b progress

Lane: d-harness-b. Branch: staging `d-harness-b`. Base: staging `agg-100`. Upstream: `.upstream/v1.0.0/packages/durable`.

## Scope

The lane ports these upstream sources into `durable/harness`:

| upstream | Go |
|---|---|
| `src/harness/scheduler.ts` | `scheduler.go` |
| `src/harness/generation.ts` | `generation.go` |
| `src/harness/live.ts` | `live.go` |
| `src/harness/submissions.ts` | `submissions.go` |
| `src/harness/inbox.ts` | `inbox.go` |
| `src/harness/compaction.ts` | `compaction.go` |
| `src/harness/usage.ts` | `usage.go` |
| `src/harness/tool.ts` (taken over: no lane owned it and generation needs it) | `tool.go` |

The lane owns these upstream tests: `harness-generation`, `harness-generation-recovery`, `harness-live-deltas`, `harness-structured`, `harness-submissions`, `harness-inbox`, `harness-compaction`. `harness-structured` is the scheduler's structured-concurrency test.

Ownership was agreed with d-harness-a in the lane question thread. d-harness-a owns `chat_support_test.go`, `harness_support_test.go`, `task_support_test.go`, and `session_support_test.go`.

## Decisions

- SQLite: the lane does not open SQLite itself. d-storage owns the pure-Go `modernc.org/sqlite` storage (CGO_ENABLED=0). Harness tests use the memory storage, as upstream does.
- Documents (`pi.live`, `pi.inbox`, `pi.usage`) are mutated through `*delta.Object` drafts from `chord/delta`. Writes follow upstream statement order so the emitted Chord ops match the upstream tracker exactly. `drafts.go` holds the conversion helpers.
- `usageJSON` writes usage counters directly. `ai.Usage.MarshalJSON` derives `totalTokens`, and pi.usage must store the counter as given.
- The scheduler and submissions services take the concrete `*session.SessionImpl` and `*session.Transaction`, as upstream types them.
- Promise mapping: every awaited call is a blocking Go call. The scheduler's `queueMicrotask` passes (reconcile, drain) are owned goroutines in a `sync.WaitGroup` that `Join` drains. Invocations are goroutines with a `done` channel that `Join` waits for. Abort is context cancellation with a cause.
- One mutex guards the scheduler's in-memory mirror. It is never held while waiting for the Session line. It is held during Storage reads inside commit callbacks, which run on the line.
- A phase handler panic faults the task, as an uncaught throw does upstream.
- The partial-message throttle in generation keeps one commit in flight and a trailing 100 ms commit (generation.ts `throttle`).

## Temporary stand-ins

- `lineAdapter` in `scheduler.go` bridges d-harness-a's `ReadContext` line reader, which takes a context, to `SessionImpl.ReadOnLine(job)`. Remove it when `ReadContext` takes the Session directly.
- The private entry/message JSON codec was deleted after d-foundation landed `durable.DecodeMessage`, `durable.DecodeUserContent`, and the `EntryDraft` JSON codec.

## Cross-lane requests

- d-foundation: entry/message JSON codec. Landed.
- d-session: `CommitWith` scope, `ReadOnLine`, `Transaction.Submission/SetTask/StagedTasks/StagedConversations`, synchronous commit and close listeners, `Hooks`. Landed.
- Lead: `tool.ts` and `task-graph.ts` have no owner. Generation tool rounds and most structured tests need `ToolTask`. See the d-harness-b lane question thread.

## Method note

The task asks for tests first. The lane ported the sources first, because the shared Session, Storage, and Harness APIs were still landing and the tests could not compile. The tests are ported afterwards with upstream names and assertions; the red→green list in the READY commit states which tests were observed red.

## Status

- Sources: ported. `tool.ts` was ported here because no lane owned it; its own upstream tests (harness-tools, harness-tools-recovery) remain unported.
- Tests: all seven upstream files ported (submissions 10, generation 21, generation-recovery 7, live-deltas 11, inbox and usage 32, compaction all, structured 48) and mapped as ported in `test/parity/interfaces/test-mapping-v1.0.0.json`.
- Red to green through fixes in d-session's packages: 5 inbox cases (chord/delta splice emptying an array, 27355498c), 1 usage case (reserved-key folds, 9241919e0), a -race failure in live-deltas (Close ran BeforeClose before close listeners, 95a1411eb), 1 structured case (createTask without ownership, 96d653f19).
- Mutation checks: request-ID deduplication, the aborted stop reason of a converted partial, the retry bound, and the pinned stream options of a resent request each fail their tests when broken.
- Go mechanics recorded in test comments: a JS function in a value is a NaN (not strict JSON); Go maps have no key order; JS microtask ordering between commit listeners and waiters is awaited explicitly; throws from `Models.getModel`/`completeSimple` are a panicking provider model listing and a non-strict summary.

## Parallel-round abort order (resolved)

Pi appends the results of a parallel round aborted together in call order through microtask FIFO. Go ran the tool abort handlers concurrently, so the structured case failed 22 of 30 runs. `tool.go` now makes each tool abort wait for its abort-marked, live earlier siblings to settle (`awaitEarlierAborts`); 0 of 400 runs fail under -race.

## Beyond scope (tool.ts tests)

`harness-tools-recovery.test.ts` is fully ported (9 cases) against this lane's `tool.go`, using durable/env and durable/tools from sg-durable-tools. `harness-tools.test.ts` is not ported by this lane.

## Review follow-ups (rev-d-harness-b, ACCEPT-WITH-FIXES)

- Merged the reviewer's beforeTool panic fix (9d509e8b9).
- The partial throttle now copies once per flush (633331e13), as generation.ts does; a failing copy is reported instead of failing the stream.
- Left as is: `ai.AssistantMessage.ErrorMessage` is a plain string, so the harness cannot tell an empty `errorMessage` from an absent one where Pi uses `??`; a provider sending `""` gets the fallback text. `prepareArguments` returning a non-object becomes nil arguments; Pi types its return as the parameters' static type, and agent.ValidateToolArguments takes only an object.
- Abort ordering stays tool-level (`awaitEarlierAborts`); the reviewer accepted it and noted that scheduler-level ordering of abort invocations started in one pass would be closer to Pi when an earlier sibling's abort is delayed.
