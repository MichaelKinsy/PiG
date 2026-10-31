# Rows of slice `hgc` (lane N-DUR, Pi `6d4b7443`): harness-group-commit.test.ts

Branch `fix/follow-ndur-hgc` on `fix/follow-ndur` `19ed965483`. Only new test files; no production code changed.

## (c) Upstream tests ported

### packages/durable/test/harness-group-commit.test.ts

Go: `durable/harness/harness_group_commit_test.go` (cases), `durable/harness/harness_group_commit_support_test.go` (helpers). Four `describe` blocks are `TestGroupCommitSchedulerRegions`, `TestGroupCommitRecordsADecisionCopies`, `TestGroupCommitReadsOnTheLine`, `TestGroupCommitOutcomesAndPublicationThroughTheHarness`; each `it` is a subtest of the same name.

Ported (17 of 17), missing none, designedOut none:

- scheduler regions: "holds a live owner whose end races a creation below its ended tasks: live P, ended B and A, empty C"; "holds an owner whose end follows a creation below its child's pending end: live P, ending B, conversation C"; "ends a child before its next phase when its owner's abort mark is unpublished at its step"; "refuses the second of two opposite waits admitted behind each other"; "starts no invocation on a reservation before the reservation is published"; "runs a migration once although its reservation waits for an unpublished write of the task"; "reports a failed migration once although its reservation waits for an unpublished write of the task"; "makes a reader of the roots wait for a creation under a root a pending end would unlist"; "walks a creation's memberships past an ancestor whose pending end is unpublished"; "reserves the live work of the owner above an ancestor whose end is pending, for a creation below it"; "keeps the nodes a chain load installed for an unpublished creation through the sweep".
- records a decision copies: "marks a child in a cascade from its current record, not one its pending commit replaces"; "marks the other tasks of a failFast wait from their current records".
- reads on the line: "reads no task record from Storage for more live and awaited tasks than the record cache's count".
- outcomes and publication through the Harness: "finds the submission an unpublished commit admitted with the same request ID"; "runs a wait continuation of one commit before the next commit of the same call is published"; "signals an invocation when it closes during a call in flight, and stores that call before Storage closes".

Go mappings (header of both files): module-level `gates`/`runs`/`refusals`/`runtimes` are one `gcFixtures` per test; a call the test awaits later is `gcGo` (`callAsync`: it has taken its line position when it returns); `await flush()` is `gcFlush` (25 ms); `harness as SessionImpl` is `*harnessImpl` (`ReadOnLine`, `AwaitTouch`); `SCHEDULER_INDEX_SIZES` is `tasks.indexSizes()`; `setTask` is the scheduler's `setTask`; the `ControlledStorage` subclass is `gcCountingStorage` over `controlledStorage`. One strengthening-free deviation: where upstream reads `status(d) === "running"` right after an `eventually` for P, the port waits for it with `eventually` (a task set running by another goroutine has no order against the test's read; D can only be pending or running there).

## Red record

The tests compile and were green on the first run against the ported production code of `fix/follow-ndur` (the production port landed before this slice), so there is no red run to record; the red commit is the test commit itself, `0702161314`. Red command shape: `~/ndur-tools/ndur-run.sh hgc -- 'go test -count=1 -timeout 120s -run "TestGroupCommit(SchedulerRegions|RecordsADecisionCopies|ReadsOnTheLine|OutcomesAndPublicationThroughTheHarness)" -v ./durable/harness/'`.

Green: the same command: 17/17 pass (13.4 s; the 300-child case is 12.4 s of it). `-race -count=3`: ok, 44.5 s.

## Open failures

None. Observation for the production owner: "reads no task record from Storage ..." takes about 12 s for 300 children (the TS case is ms); it passes with 0 Storage task reads, so it is a cost question, not a correctness one.
