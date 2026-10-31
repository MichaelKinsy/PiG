# Rows of lane N-DUR slice `storage` (Pi `6d4b7443`, group commit `e5c6182b`) for the integrator

Branch `fix/follow-ndur-storage` on `fix/follow-ndur` `9f56026a3a`. Delete this file after transcribing. Red commit `3827fa2233` (ported tests and suite alone), port `875c5c04b3` + the follow-up fixes of this branch.

Red record: `go vet ./durable/storage/... ./durable/durabletest/...` at `3827fa2233` fails with
`durable/storage/memory.go:307:25: cannot use (*MemoryStorage)(nil) ... does not implement durable.Storage (wrong type for method Commit)`
(same for `*SqliteStorage`, `*JsonlStorage`): the tests and the conformance suite call `Commit(ctx, batches, receive)` and `durabletest.CommitWrites`, the backends still had `Commit(ctx, writes, adopt)`.
Green: `go vet ./durable/storage/... ./durable/durabletest/...`; `go test -count=1 -skip TestSqliteSchemaMatchesPiForEveryPrefix ./durable/storage/... ./durable/durabletest/...` and the same with `-race` for `./durable/storage ./durable/storage/jsonl ./durable/storage/sqlite/... ./durable/durabletest`. Run with `durable/storage/*pi_oracle_test.go` moved away (they, `session_pi_oracle_test.go` first, need the session slice to compile: `session.CreateSession`, `CommitWith`; their one `Commit` call in `memory_pi_oracle_test.go:786` is migrated mechanically and unverified). `TestSqliteSchemaMatchesPiForEveryPrefix` needs `.upstream/current` (absent in the runner tree), not a change of this slice.

## (a) Upstream files

| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| packages/durable/src/storage/memory.ts | ported | durable/storage/memory.go | test:durable/storage/memory_storage_test.go#TestMemoryStorageConformance | `Commit(ctx, batches, receive)`: each batch `prepareCommitLocked` then `applyLocked`, stop at the first refusal (a refusal is the error of the check, returned in `CommitResult.Refused`); a closed storage returns an error, not a refusal. The old `adopt` ran under the storage lock so no read saw a batch before its adoption: `receive` is called under the same lock (deferred, so a panicking apply still delivers the receipts of the batches before it, Pi's `finally`) |
| packages/durable/src/storage/clone.ts | unchanged-observable | durable/storage/clone.go | none | not in the delta |
| packages/durable/src/storage/jsonl/storage.ts | ported | durable/storage/jsonl/storage.go | test:durable/storage/jsonl/jsonl_storage_test.go#TestJsonlStorageBatches | `Commit`: per batch check against memory (`PrepareCommit`, `Apply`, `adoptSidecarState`), sidecar reclamations merged across batches, then one `appendCommits` (sidecars per file, fsync, all markers in one append); receipts after the append, reclamation after the receipts; a failure once a batch applied poisons (`failedCommit`), before the waiting reads go on. `writing` channel gates every read (`readable(ctx)`, all context-taking reads; `MintId` is not gated, as Pi's `mintId`) |
| packages/durable/src/storage/sqlite/storage.ts | ported | durable/storage/sqlite/storage.go | test:durable/storage/sqlite/sqlite_storage_test.go#TestSqliteStorageBatches | one transaction, per batch `prepareDocumentActions` + `checkBatch` (global ids, document actions, `resolveCopies`: the copy now resolves in the check stage, `applyDocumentActions` takes `action.content`), refusal stops and commits the prefix, metadata written once if anything stored. `watchedExecutor` marks a failing database call: such a failure returns the error (unknown outcome, whole call rolled back); only Durable's own checks refuse |
| packages/durable/src/storage/sqlite/{database,schema,migrations,rows,working_set...}.ts | unchanged-observable | durable/storage/sqlite/* | none | not in the delta (`working_set` Go-only retained entries are collected across batches and retained after COMMIT) |
| packages/durable/src/storage/indexeddb.ts | designed-out | none | none | (browser-only Storage backed by window IndexedDB: the file opens with /// <reference lib="dom" /> and every method drives IDBDatabase/IDBTransaction, which exist only in a browser; PiG is a Go process with no browser or WebAssembly build, so no caller can reach it; the Storage contract it implements is the one memory.go/jsonl/sqlite satisfy; Pi 16f5f0af4d) |
| packages/durable/src/testing/commit-writes.ts | ported | durable/durabletest/commit_writes.go | test:durable/storage/memory_storage_test.go#TestMemoryStorageConformance | `CommitWrites(ctx, storage, writes) (Seq, error)`: one batch, its seq; the refusal returned as the error (`Stored == 0`) |
| packages/durable/src/testing/index.ts | ported | durable/durabletest/durabletest.go (package surface) | test:durable/durabletest/storage_conformance_cases_test.go#TestCreateStorageConformanceCasesMatchUpstream | the barrel is the package: `CommitWrites` is exported next to the existing names |
| packages/durable/src/testing/storage-benchmark.ts | ported | durable/durabletest/storage_benchmark.go | test:durable/storage/storage_write_benchmark_test.go | every seed/write goes through `CommitWrites`; no scenario changed |
| packages/durable/src/testing/storage-conformance.ts | ported | durable/durabletest/storage_conformance.go | test:durable/durabletest/storage_conformance_cases_test.go#TestCreateStorageConformanceCasesMatchUpstream | `checkReceipts` (one receive per stored batch, in order, increasing seqs, all before the call returns, `stored` equals the receipts); the receive case renamed; four new cases (batches in order, prefix before the first refusal, malformed later batch, batched copy); close case commits a root write |
| packages/durable/src/testing/record-key-order.ts | unchanged-observable | durable/durabletest/record_key_order.go | none | not in the delta |

## (b) PORT_MAP rows to add / replace

| source | PiG | status |
|---|---|---|
| `packages/durable/src/testing/commit-writes.ts` | `durable/durabletest/commit_writes.go` | ✅ |
| `packages/durable/src/storage/indexeddb.ts` | none | designed-out (browser-only, see (a)) |

The rows of `memory.ts`, `jsonl/storage.ts`, `sqlite/storage.ts`, `storage-conformance.ts`, `storage-benchmark.ts` keep their PiG files; only their upstream pin moves.

## (c) Tests (per upstream test file)

- `test/memory-storage.test.ts`: ported: the conformance registration, "does not expose retained state through a prepared commit". designedOut: "delivers the receipts of the batches applied before one whose apply throws, then rejects": `PreparedMemoryCommit.Apply` cannot fail in Go (no throwing apply) and `MemoryStorage` cannot be subclassed to replace `prepareCommit`; the deferred receipts in `Commit` cover a panic (Go's `finally`), untested.
- `test/jsonl-storage.test.ts`: ported: every migrated `commitWrites` call; "poisons the storage when an append fails after its batches applied, before a waiting read goes on" (`TestJsonlStoragePoisonsWhenAnAppendFailsAfterItsBatchesApplied`); "serializes the complete candidate..."; `JsonlStorage batches` "appends a batch's markers in one write and recovers a shorter prefix from a torn one" (`TestJsonlStorageBatches`); "removes complete unconfirmed sidecar tails..." now commits a conversation write at seq 4 (Pi dropped the empty commit). Go test `TestJsonlCommitNamesAFailedSidecarPathJoin` (Go-only, `:827`) changed: Pi at T poisons after a path-join failure once the batch applied; it asserts the poisoned error with that cause. Concurrency of the read test: Go starts the commit and the read on goroutines; the read sees the batch whether it waited or ran after.
- `test/sqlite-storage.test.ts`: ported: migrated calls; `SqliteStorage batches` (`TestSqliteStorageBatches`): "rejects the whole call, the batches before it included, when a later batch's check read fails" and "rejects a batch call whose check read fails in the database, and refuses only for its own checks". Pi's variants (undefined rejection, synchronous throw, primitive rejection, Error rejection) collapse to one Go case each: a Go database call fails only by an error return.
- `test/sqlite-facade.test.ts`, `test/sqlite-migrations.test.ts`, `test/sqlite-cloudflare.test.ts`: ported (only `commitWrites` calls changed): `TestPortableSQLiteFacadeSettlement`, `TestDurableSQLiteMigrations`, `TestDurableObjectSqliteAdapter`.
- `test/indexeddb-storage.test.ts`: designedOut with the file (browser-only). Its new `commit` cases "stores the batches before a refused one, and the storage goes on", "resolves a copy against the state an earlier batch of the call left", "refuses a batch whose copy fails before it writes anything, keeping the batches before it" are backend-neutral and covered by the shared suite cases "stores the batches before the first one refused, and none after it" and "resolves a batched copy against the state the earlier batches left" (run by memory, JSONL, SQLite, Durable Object SQLite); the two "rejects with nothing stored when IndexedDB fails during a batch's checks / writes" cases are IndexedDB-specific (SQLite's equivalents are `TestSqliteStorageBatches`).
- Storage benchmark scenarios: no scenario changed; seeding goes through `CommitWrites`.

## (d) Async contracts

One row per async source file; Go form = blocking `Commit` (the Session serializes commits).

| file | contract | evidence |
|---|---|---|
| storage/memory.ts | `Commit` applies batches in order under the storage lock; `receive(index, seq)` once per stored batch, in order, synchronously, after the last apply and before the lock releases (so before any read sees the batches and before the next commit); refusal ends the call with `Stored` batches; a closed storage is an error | test:durable/durabletest/storage_conformance_cases_test.go (receive case) via `TestMemoryStorageConformance` |
| storage/jsonl/storage.ts | reads block while a commit's append runs (`writing`), return after it or the poison error; `ctx` cancels a waiting read; receipts after the markers are appended, sidecar reclamation after the receipts | test:durable/storage/jsonl/jsonl_storage_test.go#TestJsonlStorageBatches, #TestJsonlStoragePoisonsWhenAnAppendFailsAfterItsBatchesApplied |
| storage/sqlite/storage.ts | one transaction per call; receipts after COMMIT returned; database failure = error return, whole call rolled back, no receipts | test:durable/storage/sqlite/sqlite_storage_test.go#TestSqliteStorageBatches |

## (e) New public Go API

`durabletest.CommitWrites(ctx, storage, writes) (durable.Seq, error)`. `MemoryStorage.Commit`, `JsonlStorage.Commit`, `SqliteStorage.Commit` take `(ctx, [][]durable.StorageWrite, func(index int, seq durable.Seq)) (durable.CommitResult, error)`. No other exported name changes.

## Open points for the integrator

Callers of the old `Storage.Commit(ctx, writes, adopt)` outside this slice, `durable/session`, `durable/harness`, `durable/storage/store`:
- `durable/codemode/call_restart_pi_test.go:179,210` (`queuedStorage`), `durable/codemode/call_test.go:31,40` (`countHeldStorage`), `durable/codemode/support_test.go:585,598` (`heldStorage`): wrappers implementing `Commit` and delegating to `MemoryStorage.Commit`.
- `durable/storage_rejected_copy_test.go:42` and `durable/core/driver/harness_restart_failure_core_test.go:139`: direct `Commit(ctx, writes, func(durable.Seq) {})`; use `durabletest.CommitWrites`.
- `durable/storage/session_pi_oracle_test.go` (this slice's directory) uses `session.CreateSession`/`CommitWith` and blocks the whole `durable/storage` test package until the session slice compiles.
