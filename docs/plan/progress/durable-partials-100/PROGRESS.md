# durable-partials-100

Slice: close the partial durable rows of `test-mapping-v1.0.0.json` that are not `harness-tools.test.ts` (owned by details-null-100). Base `agg-100` `48f3385e4`; it also merges `chord-guide-watch-race-100` (`d6c7ac096`), whose `chord-guide` evidence and rationale this slice extends.

## Result

| Row | Before | After |
|---|---|---|
| `chord-guide.test.ts` | partial | ported |
| `spec-usage.test.ts` | partial | ported |
| `types.test.ts` | partial | partial, narrowed to three Go-design causes |
| `session-definitions.test.ts` | partial | partial, narrowed to one Go-design cause |

## Ported

- `chord-guide`: the whole guide runs on the Chord facet host (`internal/chord`: `DefineFacet`, `DefineService`, `ProvideService`, `ProvideMany`/`Spawn`, `UseService`, `ObserveService`, `CreateFacetHost`, `CreateRemoteServiceBinding` over an in-process transport) against a real Harness. The Session's attached state is `chord/`'s type and the facet host's is `internal/chord`'s, so the test attaches the second to the first through the Session state's source stream (`documentSource`). Upstream has no bridge because both are one type.
- `spec-usage`: `durable/examples/spec_usage_upstream_test.go` holds the harness and tools examples. The host sequence and the LiveDoc revoked draft also run (upstream only compiles them).
- `types.test.ts` :248, harness half: `harness.Hook` is now generic over the task (`Hook[I, S, R, H](task, handlers H)`), as upstream's `hook<K>(task, Partial<HooksOf<K>>)`; before, it took `(AnyTask, any)` and a handler of the wrong type compiled. A handler of another type, another task's hook set and a member the task lacks now fail to compile (red-proven: with `handlers any` the "hook names come from the task's hooks" check fails). `harness_support_test.go` `addHooks` builds the registration directly because its callers pass untyped handler sets on purpose.
- `session-definitions`: version 1.5 is a compile error; snapshotAsOf of a task and a latest conversation document are asserted; the marker entry and the raw append run; three typed-entry negatives are type-checked.
- `internal/testenv/typecheck.go` (`TypeErrors`, `ExpectCompiles`, `ExpectTypeError`) is the type-check-a-snippet helper that `durable/types_upstream_test.go` carried; it now serves three tests (that file, the harness types test, session-definitions).

## Red to green

No test was red first in the strict sense (each is a port of an existing, compiling API). Compiling mutations stand in:

| Test | Mutation that fails it |
|---|---|
| `TestChordUsageGuide` canvas, reviews | Session state source drops its frames; observer never adds its comment; host not disposed; host disposed before clients detach |
| `TestTypesUpstreamHarness` | `Hook` takes `handlers any` |
| `TestSpecUsageHostSequenceRuns` | tool filter left removing bash |
| `TestSpecUsageRevokedLiveDraftPanics` | the sequence does not write the revoked draft |

Survivor: the source's `sequence <= snapshotCursor` guard (a publication racing the attach) cannot be hit deterministically; it stays because the race exists.

## QUESTION for the lead

Three rows' remaining cases have no Go form without a Go API redesign. I did not mark them approved. Choose per item: approve a numbered, scrutinized reason, or authorize the redesign.

1. `TaskId` result type (types.test.ts :48 and :248: `narrowedTask`, `TaskResult`, `TaskId<{ran}>`, `SettledTask<{ran}>`, `TaskId<CompactionResult>`). Go methods cannot take type parameters (`Harness.WaitForTask`, `Conversation.Compact`, `TaskRuntime.WaitForTask`) and `TaskId` is a named `int64` used at about 565 sites. Carrying `R` means `TaskId[R]` plus package-level typed waits, with explicit conversion between `TaskId[R]` and the erased id (Go has no default type argument or variance).
2. Record-union exclusivity (types.test.ts: 15 literals of :72 and 3 `SubmissionDraft` literals of :248). `ContextEdit`, `TaskState`, `TaskOutcome`, `TaskRecord`, `DocumentRecord`, `DocumentCreate`, `DocumentContent`, `DocumentCreateWrite`, `SubmissionRecord` and `SubmissionDraft` are one struct with a discriminant, so a member field of another variant compiles. d-foundation recorded the same choice. Sealed interfaces per member touch dozens of files (99 uses of `SubmissionRecord` in 27 files, 74 of `TaskOutcome` in 24, 62 of `DocumentCreate` in 14) and the JSON codecs of every storage backend.
3. Scope-typed document tokens (session-definitions :77 and :140: extra owner on a Session document, family access without a seed, mistyped seed, seed on retirement and on a snapshot, `watchDoc` with an owner). Upstream's runtime ignores each (`resolveAddress` reads only the arguments the scope needs), and so does Go's; only TypeScript's token type rejects them. `DocToken[T]` holds the scope as a field and `TxDoc`/`Snapshot` take variadic erased owner arguments.
4. `missingPhase` (types.test.ts :248) is impossible, not a design choice: the phase map's keys are the checkpoint's `phase` string-literal union and Go has no string literal types, so `DefineTask` cannot know the phase set. This needs a numbered reason, not code.

## Found, not fixed (outside this slice)

- `durable/harness` `TestTaskRecovery/resumes_abort_work_after_close_at_every_direct-task_abort_stage` fails on `agg-100` `48f3385e4` without these changes (`log [run abort], want [run]` at `harness_tasks_recovery_test.go:267`; it failed on every run I made, on this branch and on a clean worktree of the base). It belongs to the harness-tasks-recovery row.
- `make test-porting-release`, `known-gaps-drift` and `make generate` stop at the pre-existing owner-approval check on `packages/chord/test/delta-tracker/tracker.test.ts` (`designedOutCases`, chord-100).
- The Chord subscribers of a Session state still run on a delivery goroutine after the commit returns (upstream: before it resolves). The guide waits for them; `WaitDeliveries` joins the Session's. Unchanged, as in chord-guide-watch-race-100.

## Commands

`go test ./durable/... ./internal/testenv/` (green except the `TestTaskRecovery` case above), `go test -race` and `-count=100` of the guide (also on 4 CPUs), `go tool golangci-lint run ./durable/... ./internal/testenv/...` (0 issues), `go fix -diff` (empty), `make test-inventory`, `make source-hygiene`, `make interface-go-drift`, `make port-map-drift`.
