# xd-transcript-bridge: Transcript service on the durable view's exact frames (Pi 1.0.0)

Status: READY.

Branch `xd-transcript-bridge` from `agg-100`, with `exp-durable`, `d-harness-a`, `d-harness-b`, `d-harness-c`, `sg-durable-tools` and `d-env-tools` merged. This is exp-durable's Next item 2.

## Done

- `durableadapter.Session.ViewState` attaches an `internal/chord` state to `viewFrames` (`internal/experimental/durableadapter/view_source.go`), a `ReplicatedStateSource` that republishes `Conversation.ViewState`'s publications (its `InternalSnapshot` and `SubscribeSource`, upstream `getReplicatedStateInternals`). The snapshot is the durable state's publication at subscription. Each later publication becomes the next cursor with its own operations, and the strict JSON value advances by them with structural sharing. The view state's `CommittedStateSource` buffers frames without bound, so a slow consumer never receives a root replacement (review rev-xd-transcript-bridge replaced the original `Conversation.Watch` feed, whose 100-frame overflow published one). The attaching context bounds only the attachment, as upstream's `viewState` context (`durable/src/harness/view.ts:92-104`, attach `:124-151`). Dispose disposes the durable state and releases the mount. Upstream serves `conversation.viewState` itself (`coding-agent/src/experimental/services/transcript-provider.ts:6-13`); Chord publishes the source's operations and never re-diffs them (`chord/src/types.ts:73-109`).
- Models service: `Activate` and `Refresh` read the catalog inside the state change, so concurrent calls publish catalog revisions in order, as Pi's single synchronous turn does (`models-provider.ts:87-91`, `:114-120`).

## Red to green

- `internal/experimental/durableadapter/view_state_test.go#TestViewState/publishes_each_durable_view_revision_with_its_exact_operations`: red before (each published batch was a whole-view `["r", ...]` replacement, not the watch frame's ops); green after. The other three cases (overflow replacement and the revision after it, attach-only context, already-cancelled context) guard the new source; mutations of the strict conversion, the cursor, and the watch context each fail them.
- `internal/experimental/services/transcript_provider_upstream_test.go#TestPortWave08ExperimentalTranscriptProvider`: the port of the single upstream case now also asserts the consumer's views are the oracle watch's frames, in order.
- `internal/experimental/services/models_provider_test.go#TestModelsConcurrentCatalogRevisionsAreDistinct`: failed 162 of 200 runs before, 200 of 200 pass after.

## Evidence

`BenchmarkViewStatePublication` (one revision reaching a remote Transcript subscriber, 1000 seeded entries, 200 iterations): before 18.8 ms, 10.3 MB, 45,400 allocations per revision, growing with the transcript; after 0.2 ms, 0.2 MB, 166 allocations. The CPU and allocation profile puts the remaining bytes in durable's entry-list copy (`view.ts` advance spreads the entries) and the immutable splice of the entries array.

## Gates

`go test ./internal/experimental/...` green (the Node-oracle tests need `extensions/sdk-ts/node_modules`); `-race` clean for durableadapter and services; `go vet` (also `GOOS=windows`) and `golangci-lint` clean on `internal/experimental/...`. `make ci-contracts ci-drift` failures are all on the merged base and outside this slice: the chord `tracker.test.ts` release-policy approval, durable format-version fields, stale generated coverage, private paths in other lanes' progress files, and the `agent/harness` divergence-guard hits and invalid allow markers (exp-durable Next item 3).

## Merge notes

d-harness-c and d-harness-a both added `durable/harness/task_graph.go` and its test; d-harness-a's (typed, with the real `view.go`) is kept and d-harness-c's view stubs are dropped. d-harness-c's `TestHarnessOpen` is renamed `TestTaskRecoveryHarnessOpen` (mapping updated) and its tests reuse the shared `emptyObjectSchema` helper.

`durable/storage` `TestDurableStorageRuntimeBoundaries` fails on the merged base: `durable/env` imports `os`, `os/exec`, `path/filepath` and `syscall` since d-env-tools put the Node environment in the portable package. That is the env and storage lanes' decision.

`durable/harness` `TestToolProgressAndLifetime/settles_details()_promises_with_coalesced_progress_commits_and_the_terminal_commit` (d-harness-c) fails about half the runs on the merged base, before and after this lane's changes: "details map[n:2], want n: 3". Upstream records the third `api.details({ n: 3 })` synchronously before `execute` returns and only its settlement is pending (`packages/durable/test/harness-tools.test.ts:915-927`). The Go test calls the blocking `api.Details` on a goroutine and sleeps 1 ms, so the terminal commit can run before the third value is recorded. A faithful fix needs `ToolExecutionApi.Details` to record the value synchronously and return a wait handle; that is d-harness-c's API. The review (rev-xd-transcript-bridge) made the test deterministic without an API change: the tool waits until each goroutine's value is recorded before it goes on, which is upstream's synchronous record (`tool.ts:202-209`). The API gap stays d-harness-c's.
