# d-session: pi-durable Session kernel

Lane: d-session. Branch `d-session` from the `agg-100` aggregate, merged with `d-foundation` and `d-storage`. Upstream `.upstream/v1.0.0/packages/durable/src/session` and `packages/chord/src/delta`.

## Status

READY for review.

## Layout

| Upstream | Go |
|---|---|
| `durable/src/session/session.ts` | `durable/session/session.go` |
| `durable/src/session/transaction.ts` | `durable/session/transaction.go` |
| `durable/src/session/observation.ts` | `durable/session/observation.go` |
| `durable/src/session/forks.ts` | `durable/session/forks.go` |
| `durable/test/session-support.ts` | `durable/session/sessiontest/support.go` (exported so harness lanes reuse ControlledStorage) |
| `chord/src/delta/{index,tracker,draft}.ts` | `chord/delta` (overlay tracker, ops, immutable applier) |
| `chord/src/services/state.ts`, `state-internals.ts` | `chord/state.go` |
| `chord/src/json.ts` copyJson | `chord/json.go` |

## Decisions

- No lane owned Chord. The Session needs the overlay tracker, so this lane ported `chord/delta`: revocable `*delta.Object`/`*delta.Array` draft handles that record exact operations (container assignment is one `s`, structural array edits are `p`/`m`, string growth `a`/`t`, more than 4096 ops fold into `r`, payloads shared with the revision). d-foundation adopted `durable.Op = delta.Op`, `Draft[T] = *delta.Object`, `AttachedReplicatedState = *chord.AttachedReplicatedState`.
- A settled draft panics with `delta.ErrRevoked` (upstream TypeError); invalid placements return `*delta.StrictJSONError`.
- The mutation line is a FIFO ticket queue. Microtask deliveries (watch drains, state attachment drains) run on Session-tracked goroutines; `SessionImpl.WaitDeliveries` is upstream's test `flush()`.
- Harness overrides of `conversationCreated`/`beforeClose` are `session.Hooks` passed to `NewSessionImpl`.
- A typed document callback cannot return an error (`CheckpointWhen`, `Initial`); a panic in it rejects the commit like an upstream throw (`callDefinition`).
- SQLite: not this lane (d-storage, modernc.org/sqlite already in go.mod).

- The Harness builds views and event watches on committed observations (view.ts:90/:104, events.ts:132), so `NewCommittedStateSource`/`NewCommittedWatch` are exported, bound to a Session's delivery scheduler, with upstream's public `Advance`/`CloseSession`/`Cancel`/`ObserveCancellation`. `(*SessionImpl).CommitSubscriptions()` replaces upstream's monkeypatched `subscribeCommits` count (harness-lifecycle.test.ts:331); `(*SessionImpl).LineJobs()` lets tests wait for queued line work without sleeping. `observation_api_test.go` pins that surface.

## Red → green

- `TestSessionDocumentWatches/folds_retirement_into_an_overflow_reset_and_then_closes` was red: the overflow fold of a retirement emitted `["r", <typed nil map>]`; fixed in `observation.go` advance.
- `TestSessionConversationDocumentForks/rolls_every_copied_base_back...` and `TestSessionDocumentCheckpoints/rolls_back_every_prepared_document...` were red (a throwing predicate panicked through the Session) until `callDefinition`.
- d-storage `TestPicoSqliteStorage` replay case was red against `chord/delta` (`unknown op` vs upstream `unknown op verb:`); fixed in `chord/delta/ops.go`.
- `TestSessionCloseRunsListenersBeforeBeforeClose` was red (reported by d-harness-b): `Close` started the `BeforeClose` goroutine before running close listeners; listeners now return first, as session.ts:350-366.
- `chord/delta` `TestTrackerEmptiesArraysThroughEveryRemovalPath` was red on all four paths (splice, pop, shift, SetLen; reported by d-harness-b): splicing out every element left a nil entry list, which reads as an untouched array, so the array reappeared and no op was recorded. splice now builds a non-nil list.
- `chord/delta` `TestTrackerFoldsReservedKeyMutationsAtTheNearestSafeAncestor` was red (reported by d-harness-b): mutations under `__proto__`/`constructor`/`prototype` emitted unsafe paths and Prepare failed; they now fold into one whole-value set at the nearest non-reserved ancestor (root: `r`), as tracker.ts:1637-1659.
- `TestCreateTaskRequiresOwnership` was red (reported by d-harness-b): a zero `TaskOwnership` created a conversation-owned task; upstream dereferences `options.ownership.kind` (transaction.ts:387) and throws, so createTask now rejects it with "Tx.createTask() requires options.ownership" before minting.
- Other tests passed on first run; each file is red-proven by a compiling mutation (restored afterwards): structural array flag off (checkpoints/documents no-op cases), poison dropped (documents :497), observedOperations version branch off (migrations :463, cache :794), fork-source write check off (forks :414), pending-op check off (documents :311 :340), watch overflow off (watches :124 :143 :166), ReadAfterWrite off (tables :84 :120), checkpoint base decision ignored (checkpoints :20 :41 :102), state cursor +2 (states :37 :54), family seed ignored (documents :287, forks pagination, definitions).

## Test mapping

`test-mapping-v1.0.0.json`: session-states, session-tables, session-documents, session-forks, session-watches, session-checkpoints-migrations → ported; session-definitions → partial (TypeScript-only `@ts-expect-error` overload negatives have no Go compile-time form with erased variadic tokens). `make test-porting-release` / `known-gaps-drift` therefore list session-definitions next to d-foundation's types/spec-usage partial rows (owner decision pending for type-only cases). d-foundation's spec-usage `revokedDraft` example can now use `delta.ErrRevoked`.

## Gates

`go test -race ./durable/... ./chord/...`, `go vet` (also GOOS=windows), `go tool golangci-lint run ./durable/session/... ./chord/...` (0 issues), `go fix -diff` empty, `make test-inventory`, `make port-map-drift`, `make check-scratch-paths`, `make interface-go-drift` clean.
