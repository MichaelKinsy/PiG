# xd-models-iface: durable Harness models are the pi-ai Models interface

Lane xd-models-iface. Branch: staging `xd-models-iface` from staging `agg-100`. Upstream Pi 1.0.0.

## Status

READY. This branch unblocks item 1 of the exp-durable handoff (`docs/plan/progress/exp-durable/PROGRESS.md`, "Open: production `openCodingHarness`", step 1).

## Base

Merged, in order: `staging/team/smc1/exp-durable` (which carries d-foundation, d-storage, d-session, chord-100, d-harness-a, d-harness-b, d-env-tools), then `team/smc1/d-harness-c`, `team/smc1/d-env-tools` (its later chord-guide and examples commits) and `team/smc1/sg-durable-tools`.

- d-harness-c and d-harness-a both ported `task-graph.ts` and `harness-task-graph.test.ts`. The merge keeps d-harness-a's `durable/harness/task_graph.go` and `harness_task_graph_test.go`, because `internal/experimental/durableadapter` uses its typed `TaskGraph`. d-harness-c's JSON-object port is dropped; its other files are taken whole.
- `durable/harness/stubs.go` stays deleted (d-harness-a's `view.go` replaced the view stubs; `task_graph.go` replaced the graph stubs).
- Two tests collided: `TestHarnessOpen` in `harness_tasks_recovery_test.go` (upstream `harness-tasks-recovery.test.ts:826`) is now `TestTaskRecoveryHarnessOpen`, and its mapping evidence follows. d-harness-c's `emptyObjectSchema` variable now calls d-harness-a's `emptyObjectSchema()` function.
- `test-mapping-v1.0.0.json`: every conflict between a `pending` row and a lane's `ported`/`partial` row takes the lane's row.

## The change

Pi passes the coding Model Runtime as the Harness's models (`session-worker.ts:787`, `experimental/durable/runtime.ts:141`; `ModelRuntime implements Models`, `model-runtime.ts:171`). The Go Harness took the concrete `*ai.Models`.

- `durable.Models` (`durable/types.go`) is an interface of exactly the methods `packages/durable/src/harness` calls on `options.models` / `runtime.models`: `GetModel`, `StreamSimple`, `CompleteSimple`, `FetchDeferred`, `CancelDeferred` (`generation.ts:130,193,225,228,255,258,399`, `compaction.ts:112,150,172`). pi-ai's optional `options` is a variadic parameter.
- `harness.HarnessOptions.Models`, `harness.TaskSchedulerOptions.Models` and `durable.TaskRuntime.Models()` use `durable.Models`. `*ai.Models` and `*coding.ModelRuntime` both satisfy it, so every d-harness-a/b/c and examples call site that passes `*ai.Models` compiles unchanged.
- `coding.ModelRuntime.StreamSimple` and `CompleteSimple` take optional options (`...ai.StreamOptions`), as pi-ai's `streamSimple(model, context, options?)`. Every existing caller passes one value and is unchanged. The legacy `agent/harness/compaction.Models` (0.99 harness, still to be deleted) takes the same variadic form so `TestHarnessCompactionUsesModelRuntime` still passes the ModelRuntime; its test fake records `options[0]`.
- Regression: `coding/model_runtime_durable_models_test.go` asserts `var _ durable.Models = (*ModelRuntime)(nil)` and `TestModelRuntimeAnswersDurableHarnessGeneration` opens a real Harness with `Models: runtime` (a native faux provider registered on the runtime) and checks the answer entry and one provider call. Red before the change (`durable.Models` undefined; `*ModelRuntime` not assignable to `*ai.Models`). Mutation: making `ModelRuntime.StreamSimple` fail every request fails the test (`status unanswered, detail mutant`).

## For the exp-durable lane

`openCodingHarness` can now pass `harness.HarnessOptions{Models: collaborators.modelRuntime, ...}` directly; no `workerHarnessModels` adapter is needed. Steps 2 and 3 of the exp-durable open item (registry with coding tools and the pi prompt, settings, HTTP, envs; the tool task graph) remain. d-harness-c's tool task is now merged on this branch.

## Pre-existing failures (also on the lane tips, not caused here)

- `durable/storage` `TestDurableStorageRuntimeBoundaries`: `durable/env` imports `os`, `os/exec`, `path/filepath`, `syscall` because `node.go` lives in the portable `durable/env` package. Fails identically on `staging/team/smc1/exp-durable`. Owner: d-env-tools / sg-durable-tools.
- `durable/harness` `TestToolProgressAndLifetime/settles_details()_promises...`: `details map[n:2], want n: 3` in about 7 of 10 runs. Fails at the same rate on `staging/team/smc1/d-harness-c`. Owner: d-harness-c / d-harness-b (`tool.go`).
- `internal/experimental/services` `TestModelsConcurrentCatalogRevisionsAreDistinct`: `got 71..99, want 100` in about half the runs. Fails on `staging/team/smc1/exp-durable`. Owner: exp-durable.
- Node-oracle tests in `internal/experimental` and `agent/harness/pico3` fail in this worktree only because `extensions/sdk-ts/node_modules` and `test/parity/interface-extractor/node_modules` are not installed.
- `golangci-lint` on `durable/...`: `drafts.go` unused `setIndexJSON`, `harness_inbox_test.go` goimports, `tool.go` gocritic appendAssign x2 and gosec G103. All present on the lane tips.
- `make generate` stops at `known-gaps`: the release policy row for `packages/chord/test/delta-tracker/tracker.test.ts` lists `designedOutCases` without `SCRUTINIZED:approved` (chord-100 owner approval). The steps after it (`custom-factory-ledger`, `coverage`) were run directly.

## Commands

`go build ./...`, `go vet ./...`, `go test ./coding/ ./durable/... ./agent/harness/... ./internal/experimental/...`, `go test -race` on the new test, `make interface-proposals`, `make interface-go-drift`, `make coverage RESULTS=`.
