# chord-guide-watch-race-100 progress

Slice: preflight-100 C22. `durable/harness` `TestChordUsageGuide/runs_the_job_output_watch_until_the_producer_retires_its_document` lost the frame of the producer's last output commit when `watch.Stop()` ran before the delivery goroutine. Base: `agg-100` `269a0458c`.

## Upstream contract

- `packages/durable/src/session/session.ts` `#runCommit` calls `#publish` synchronously before `return result`, so every commit listener runs before the commit's promise resolves.
- `packages/durable/src/session/observation.ts` `CommittedWatch.advance` queues the drain with `queueMicrotask`. That microtask is ahead of the committer's continuation, and `#drain` takes the oldest frame and enters the listener synchronously. A started watch with no callback in flight therefore has the committed frame in delivery, and `watch.value` equal to it, before the committer resumes. `stop()` clears only the frames that are still pending.
- `packages/durable/test/chord-guide.test.ts:358` depends on this: it stops the watch once `waitForTask` resolves and asserts `rendered.slice(0, 1)` equals `["$ make\nok\n"]` without waiting.

## Cause

`durable/session/observation.go` `CommittedWatch.Advance` only started a goroutine. The frame stayed in `pending` until that goroutine ran. A `Stop` after the commit returned could clear the frame first, so the Go port dropped a frame that upstream always delivers.

## Fix

`CommittedWatch.Advance` (`deliverLocked`) moves the oldest pending frame into delivery before it returns to the publishing commit when the watch is started and no callback is in flight. It marks the callback in flight and makes the frame the watch value. The listener still runs on a Session-tracked delivery goroutine and is never invoked inline. `Start` keeps its asynchronous drain of the frames buffered before it. If a commit publishes while that drain is still scheduled, the commit takes the oldest buffered frame, as upstream's start microtask would have run first. Frames behind an in-flight callback stay pending, and `Stop` discards them as upstream does.

## Evidence

- New regression test `durable/session/session_watches_test.go` `TestSessionWatchEntersTheCommittedFrameBeforeTheCommitReturns`: start, commit, stop, then join deliveries. Before the fix it failed 50 of 50 runs with `-cpu 1` and with `-cpu 4` on CPUs 0-3, on both assertions (`Value()` after the commit, and the frame delivered after `Stop`). After the fix it passed 200 of 200 runs with `-cpu 1,4`, with and without `-race`.
- `TestChordUsageGuide` job-output case: the `eventually` poll after `Stop` is replaced by `WaitDeliveries` and an exact check. Before the fix: 26 of 50 failures with the original test and 23 of 50 with the tightened test (`taskset -c 0-3 go test -count=50`). After the fix: 0 of 200 (`taskset -c 0-3 go test -count=200 ./durable/harness -run '^TestChordUsageGuide$'`), and 0 of 200 with `-race`.
- `go test ./durable/...` passes. `go test -race ./durable/...` passes. `golangci-lint run ./durable/session/... ./durable/harness/...` reports 0 issues. `source-hygiene` and `test-inventory-drift` pass.
- `test-mapping-v1.0.0.json`: the chord-guide row now lists the session regression test as evidence.

## Found while testing (not caused by this change; same rate on the base)

- `durable/harness` `TestOwnership/retries_marks_found_through_an_edge_loaded_after_reopen_when_their_commit_is_rejected` hangs. On CPUs 0-3 it hung in 5 of 20 runs with the fix and 6 of 20 on the base (`-test.timeout 20s`). The goroutine dump shows the test in `WaitForTask` for `late` and the held owner tasks in `holdTask`. This test does not use watches. It makes `go test -count=3 ./durable/harness` reach the package timeout.
- `internal/experimental/durableadapter` `TestViewState/publishes_each_durable_view_revision_with_its_exact_operations` failed 16 of 30 runs with the fix and 19 of 30 on the base (`-race`, CPUs 0-3). The test waits until the remote-provider subscriber has every frame and then reads a second subscriber's list without waiting for it, so the last frame can be missing.
- Remaining sibling: `sessionSourceAttachment` (the Chord state source) still delivers on its goroutine after the publishing commit returns. In upstream, Chord subscribers run synchronously in the drain microtask, so they have seen the frame before the committer resumes. The Go canvas and diff-review guide cases wait with `eventually` for that reason. Running subscribers before `Commit` returns needs a reentrancy design (a subscriber may commit), so this lane does not change it.
