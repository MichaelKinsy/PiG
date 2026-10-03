# d-harness-c: pi-durable task, tool, graph, and ownership tests

Lane: d-harness-c. Branch `team/smc1/d-harness-c`, from staging `agg-100`, merged with d-foundation, d-storage, d-session, d-harness-a, d-harness-b, and d-env-tools. Upstream `.upstream/v1.0.0/packages/durable`.

## Status

READY for review. The six assigned test files are ported and green under `go test -race`. `harness-tools.test.ts` was the one `partial` row; slice details-null-100 closed it (below).

## What the lane owns

| Upstream | Go |
|---|---|
| `src/harness/tool.ts` | lane b's `durable/harness/tool.go` (this lane's own port was dropped in the merge, like the scheduler's) |
| `src/harness/task-graph.ts` | `durable/harness/task_graph.go` |
| `test/harness-tasks.test.ts` | `harness_tasks_test.go`, `harness_tasks_scheduling_test.go` |
| `test/harness-tasks-recovery.test.ts` | `harness_tasks_recovery_test.go` |
| `test/harness-task-graph.test.ts` | `harness_task_graph_test.go` |
| `test/harness-ownership.test.ts` | `harness_ownership_test.go`, `harness_ownership_tools_test.go` |
| `test/harness-tools.test.ts` | `harness_tools_test.go`, `harness_tools_progress_test.go`, `harness_coding_tools_test.go` |
| `test/harness-tools-recovery.test.ts` | `harness_tools_recovery_test.go`, `harness_coding_tools_test.go` |

`harness_coding_tools_test.go` is an external test package (`harness_test`): `durable/tools` imports `durable/harness`, so the cases that run the real read, edit, and bash tools cannot sit in the internal test package.

Source written by this lane: `task_graph.go` and `signal_link.go` (synchronous signal links, below).

## Decisions

- SQLite: the lane adds none. The tests open d-storage's pure-Go SQLite storage (`modernc.org/sqlite`, `CGO_ENABLED=0`).
- Scheduler and tool task: the lane first ported both (commits `f7515b291` and after), then found lane b's `scheduler.go` and `tool.go` and dropped its own copies in the merges. All task and tool tests run against lane b's code and are green.
- Scheduler (history): the lane first ported `scheduler.ts` itself (commit `f7515b291`, first red/green runs of the task tests), then found lane b's `scheduler.go` and dropped its own copy in the merge. All task tests run against lane b's scheduler.
- Pi#10320 (built-in tools omit `replay`): ported exactly as Pi 1.0.0 ships it. `.upstream/v1.0.0/packages/durable/src/tools/*.ts` sets no `replay`, so every built-in tool defaults to `unsafe` and an interrupted call is answered, never rerun. `durable/tools` sets none either. `harness_tools_recovery_test.go` proves the unsafe path with the real bash tool, and the safe-rerun matrix with a tool that does set `replay`.
- `ToolTaskCheckpoint` marshals `arguments` for the `execute` phase even when empty (`{"phase":"execute","arguments":{},"replay":"unsafe"}`), as upstream's stored intent.
- A tool whose `ToolExecutionApi.Output` is called after its call settled panics with Pi's message (`Tool call <id> has settled`), where upstream throws; the method has no error return.

## Edits to other lanes' files

Each is the smallest change that makes an upstream case pass. Review them with the owner; lane b is still editing `scheduler.go`.

| File | Change | Why |
|---|---|---|
| `scheduler.go` | agent resolution runs under `runHandler` | A throwing settings getter crashed the process; upstream rejects the waiting callers (`harness-tasks` "observes a failed agent resolution"). |
| `scheduler.go` | `sealing` flag set before the mutex in `seal`; the dispatch check reads it | A step holds the mutex across registry reads. A close that begins inside one could not stop the invocation dispatching its next phase (`harness-tasks` "starts no next phase when close seals during a step"). |
| `scheduler.go` | `sealed` channel closed after `closing` is set; `Join` waits for it | `Session.Close` starts `BeforeClose` (Join) and the close listeners on different goroutines, so `Join` could reach `background.Wait()` before the seal and race a `kick` adding to the group (`-race` failure in ownership and crash cases). |
| `scheduler.go`, `harness.go` | `newLinkedSignal`, `linkSignal`, and `withSignal` use `signal_link.go` | Chord's `withAbortSignal` propagates an abort in the same turn. `context.AfterFunc` runs on another goroutine, so a submit queued on the line behind the abort mark could be admitted first (`harness-ownership` "rejects a handle operation queued on the line"). |
| `agent.go` | `AddTools` reads the stored `remove` array through `plainValue` | The draft returns its array as a handle, so addTools never removed a name from a stored `{ remove }` filter (`harness-tools` "drops control keys"). |
| `stubs.go` | `TaskGraph`, `TaskGraphWatch`, `TaskGraphView` stubs deleted | Replaced by `task_graph.go`. |

## Go mappings and test adaptations

Stated in each file header and at the site. The assertions are upstream's; what changes is how a case is driven.

- A Promise is a blocking call. Upstream's microtask ordering becomes an explicit wait: the commit that must hold the line is in storage (`gate.entered`) before the call that must queue behind it starts.
- `Promise.all` of two memo candidates is two goroutines. Upstream pins the winner to the first caller; goroutines cannot, so the case asserts one durable winner for both callers.
- The parallel tools of a round start in no fixed order; the case asserts the pair `{start a, start b}` before either ends.
- The scheduler's signal is `context.Canceled` where upstream's is an `AbortError`.
- The second `abortTask` of `harness-tasks` "signals and joins the run" lands before the abort invocation finishes only by microtask order; the abort handler waits for the second call.
- The slow abort handler of `harness-ownership` also ends with its invocation's signal, which close cancels. Upstream's handler awaits its gate alone, and a close right after `abortTask` seals before the abort invocation starts only by microtask order.
- The three `details()` promises of `harness-tools` are three goroutines, constrained only where upstream constrains them (first settles before third).

## Closed by details-null-100 (formerly the `partial` row)

`harness-tools.test.ts` is `ported`. The two cases below were partial here and are closed in slice details-null-100:

1. "uses explicit null details instead of the last reported value": `ai.ToolResultMessage` and `agent.ToolResultMessage` carry `DetailsNull`, which JSON writes as `"details":null` and decodes from it. The Go case asserts the stored message is exactly Pi's over the memory, SQLite and JSONL storages.
2. "drops control keys set to undefined instead of faulting": an unset `terminate` is Pi's `terminate?: true` left out. The Go case asserts the stored control is `{ addTools: ["extra"] }`, as Pi's `copyJson` with `omitUndefinedProperties` leaves it. `durable.ToolControl` keeps an empty `addTools` list as `[]`.

## Red/green evidence

The scheduler and tool sources were written before the tests, because the Session, Storage, and Harness APIs landed while the lane worked, so no test was red before its source. Each test was then proven able to fail with a compiling mutation of the source; the source was restored.

| Mutation | Result |
|---|---|
| `scheduler.go` decide: drop the abort-mark rule | `TestTask*`, `TestOwnership` hang until the test timeout |
| `scheduler.go` `cancellationIntent`: ignore a held failed outcome | `TestOwnership` cascade cases fail |
| `tool.go` replay rule `&&` to `\|\|` (rerun against lane b's `tool.go` after the merge) | `TestToolRecovery` "reruns a tool only when both policies are safe" fails |
| `tool.go` skip `ClearProgress` before a safe rerun | `TestToolRecovery` "clears the interrupted attempt's progress" fails |
| `tool.go` throw ending `failed` to `completed` | `TestToolResults`, `TestOwnedConversationsFromTools` fail |
| `tool.go` details diff always false | `TestToolProgressAndLifetime` fails |
| `task_graph.go` reverse the created conversation order | `TestTaskGraphView` (2 cases) fails |
| `task_graph.go` drop the deletion operation | `TestTaskGraphView` (3 cases) fails |

Bugs found in the process: the five rows of "Edits to other lanes' files". One flake was a test defect: the first graph rebuild raced the late child's reservation (`TestTaskGraphView`); it now waits for that reservation.

## Commands

`go test -race -count=3 ./durable/harness/` (all lanes' tests in the package), `-count=10` over each of my test groups, `go vet ./durable/...`, `go tool golangci-lint run ./durable/harness/` (0 issues in this lane's files), `make port-map-drift`, `make interface-go-drift`, `make test-inventory` (531 ported, 6 partial).

`make check-scratch-paths` fails on the other lanes' PROGRESS files, not on this one. `go test ./durable/storage/sqlite` fails `TestPicoSqliteStorage` (an unknown-op message) on the merged tree; that is d-storage's.

## Test mapping (`test-mapping-v1.0.0.json`)

Five rows designed-out to ported (`harness-tasks`, `harness-tasks-recovery`, `harness-task-graph`, `harness-ownership`, `harness-tools-recovery`); `harness-tools` designed-out to partial, then ported in details-null-100.
