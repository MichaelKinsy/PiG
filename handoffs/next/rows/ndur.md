# Rows of lane N-DUR (durable delta e7a0df27..6d4b7443 + D-store) for the integrator

Branch `fix/follow-ndur` on `fix/follow-pi-next` `65411f70dd`; Pi target T = `6d4b7443637356fe928118df3f1f6d922fcc191e`. Delete this file after transcribing. It is the concatenation of eight slice sections (storage, chordstore, sessionunits, session, harness, hgc, hidle, htail); each keeps its own tables (a)-(e), red record and open points, so a row may appear under the slice that owns the file. Slice branches (`fix/follow-ndur-<slice>`) are merged by merge commits.

## Summary

Ported at T (Go): `Storage.Commit(batches, receive)` with prefix semantics and the optional `PublishingStorage` for memory, JSONL, SQLite and the new `StoreStorage`; `ChordStore.CommitSeries` (replaces `CommitInBatch`); `durable/storage/store` (the six D-store files); the group-commit Session (`writer.go` new, `session.go`, `transaction.go`, `guard.go`, `held.go`, `retention.go`), the three new session units (`keys.go`, `conversation_tails.go`, `cached_storage.go`); the harness at T (decision regions in `scheduler.go` + `scheduler_region.go`, idle scopes `scheduler_idle.go` (35dfa757 final state), `context.go` kept ranges, `generation.go` incl. `d280e1ff` `beforeProviderRequest`, `tool.go`, `harness.go`, `submissions.go`, `task_graph.go`, `view.go`, `types.go`); `durabletest` (`CommitWrites`, conformance for batch commit, benchmark). Covering upstream tests ported: see the per-slice (c) tables (new: `session-group-commit`, `session-cached-storage`, `session-conversation-tails`, `store-storage-commit`, `harness-group-commit`, `harness-idle-scopes`; changed: 20+ harness/session/storage tests).

Evidence (xd670, tern-ci:v6, Go 1.27.3, fresh rsync of the head): `go build ./...`, `go vet -tags 'pigfault live' ./durable/... ./chord/...` clean; `go test -race -count=1 ./durable/... ./chord/next/store/...` green except `durable/interop` (below); `go test -count=1` of the touched packages and their callers (`coding`, `env`, `chord/next`, `chord/sqlite`, `bench/durableperf`) green; `TestReachLibrariesPortPublishedEntryPoints` green with `durable:./storage/store` in `libraryEntries`.

`durable/interop` (cross-runtime tests against Pi Durable): RED against the committed pin oracle (Pi e7a0df27): `TestScriptedSessionStoresMatchPiDurable`, `TestSecondScriptedSessionStoresMatchPiDurable`, `TestScriptedSessionJsonlFilesMatchPiDurable/{first,second}`, `TestUnawaitedJobCommittedAfterTheAnswerChangesOnlyOrder` differ only in commit sequence numbers (T stores 12 commits per run instead of 14: tool-slot running status and generation attempt ride the next commit). GREEN with the oracle built at T (`PI_UPSTREAM_REF=6d4b7443637356fe928118df3f1f6d922fcc191e; eval "$(python3 automation/pin/pin.py env)"; make durable-interop-deps; go test ./durable/interop/`: `ok`): the stored bytes of the scripted sessions (SQLite and JSONL) match Pi Durable at T. These five tests therefore turn green when the integrator bumps the pin and refreshes the durable oracle.

## Evidence renames the integrator must apply (ledgers are integrator-only; Pi renamed these describes/cases, the Go tests follow)

- `test/parity/interfaces/test-mapping-v1.4.0+e7a0df27.json` (session-adoption.test.ts): `durable/session/session_adoption_test.go#TestSessionAdoptionInsideStorageCommit` -> `#TestSessionAdoptionAfterStorageStoredACommit`.
- `test/parity/async-contracts.toml`: `durable/session/session_adoption_test.go#TestPublicationAtAdoption` -> `#TestPublicationAfterStorage`; `durable/harness/harness_task_graph_test.go#TestTaskGraphViewAdvancesWithTheConversationViewInsideTheOneStorageCommitThatChangesBoth` -> `#TestTaskGraphViewAdvancesWithTheConversationViewAtThePublicationOfTheOneCommitThatChangesBoth`.
- `make generate` on the fresh clone stops at `known-gaps` (exit 2): `test inventory: entry "packages/durable/test/session-adoption.test.ts" evidence "...#TestSessionAdoptionInsideStorageCommit" has no matching fragment` (the first rename above).
- `test/parity/testprune-reviewed.toml` gained one entry (commit 875c5c04b3, jsonl reclamation test, cite `storage/jsonl/storage.ts:322`) for `make test-expectation-lint`.

## Pending / not ported (exact)

- `packages/durable/src/{tools/read,search,tool-search/tool-search,codemode/codemode}.ts` deltas (04414839, 79006f06, c7372af4) are carried by `dp/fix/060c-integrate` (M0), not in this base: record `pending (carried by M0)`; the harness slice did not port them twice. The 060c Go carry of `35dfa757` (commits ad6d659c09, fefd5ca8a3) is superseded by this lane's final-state scheduler (`scheduler_idle.go`): M0/integrator must resolve the overlap in `durable/harness/scheduler*.go` and `harness.go` in favor of this lane's files (the carried idle-scope tests `durable/core/driver/idle_scope_pi_test.go` belong to the PiG `durable/core` engine and stay).
- `packages/chord/src/next/store/store.ts` `announceTable`/`tableVersion` (Pi 4b535d57, slice 7b reload): not e5c6182b; the lane that ports 4b535d57 (K-fold) adds it.
- `packages/durable/src/storage/indexeddb.ts` and `test/indexeddb-storage.test.ts`: designed-out (browser-only; rationale in storage.md).
- `durable/core/**` (PiG's own Durable core engine, its Cloudflare facade `host/cf/src/facade/tx.ts`, `driver/hook.go`, `PROTOCOL.md`) is outside this lane: its driver `txProxy` gained `LatestEntryId` (a one-entry descending scan) so it still implements `durable.Tx`; `beforeProviderRequest` and a `latestEntryId` op in the core/facade are NOT ported (the core follows its own contract; owner decision whether the core mirrors Pi's hook).
- Benchmarks `nested-images-bench`, `nested-tools-bench`, `scheduler-fanout-bench`, `tool-output-bench`: no Go counterpart (designed-out, htail.md).
- Not mirrored: Pi `docs/group-commit.md`, `docs/spec.md`, `CHANGELOG.md`, `README.md` of packages/durable (docs, no PiG mirror).

## Open questions / design notes for the integrator

1. Go mapping of microtask/macrotask publication (session.md "Go mapping design note"): ordering waits use goroutine dumps only up to 128 goroutines and yield beyond (best-effort order at scale).
2. Go-only test devices: `gcFlush` (25 ms pause) stands for Pi's macrotask flush in `harness_group_commit_test.go`; `slowKeep` in the retention test (harness.md "Later findings"); `lineadmission`/`holdLine` replace stack matching.
3. `StoreStorage` harness re-run: `PIG_DURABLE_TEST_STORAGE=store` + `make test-durable-store` (htail.md section f); four vitest-excluded cases are skipped under it only.
4. The five `*MatchPi`/interop oracle tests in `durable/harness` and `durable/interop` run against the committed pin's oracle; refresh them with the pin bump.
5. Changelog fragments: `changelog.d/ndur-{storage,chordstore,session,harness}-*.md`.



---
# Slice `storage`

## Rows of lane N-DUR slice `storage` (Pi `6d4b7443`, group commit `e5c6182b`) for the integrator

Branch `fix/follow-ndur-storage` on `fix/follow-ndur` `9f56026a3a`. Delete this file after transcribing. Red commit `3827fa2233` (ported tests and suite alone), port `875c5c04b3` + the follow-up fixes of this branch.

Red record: `go vet ./durable/storage/... ./durable/durabletest/...` at `3827fa2233` fails with
`durable/storage/memory.go:307:25: cannot use (*MemoryStorage)(nil) ... does not implement durable.Storage (wrong type for method Commit)`
(same for `*SqliteStorage`, `*JsonlStorage`): the tests and the conformance suite call `Commit(ctx, batches, receive)` and `durabletest.CommitWrites`, the backends still had `Commit(ctx, writes, adopt)`.
Green: `go vet ./durable/storage/... ./durable/durabletest/...`; `go test -count=1 -skip TestSqliteSchemaMatchesPiForEveryPrefix ./durable/storage/... ./durable/durabletest/...` and the same with `-race` for `./durable/storage ./durable/storage/jsonl ./durable/storage/sqlite/... ./durable/durabletest`. Run with `durable/storage/*pi_oracle_test.go` moved away (they, `session_pi_oracle_test.go` first, need the session slice to compile: `session.CreateSession`, `CommitWith`; their one `Commit` call in `memory_pi_oracle_test.go:786` is migrated mechanically and unverified). `TestSqliteSchemaMatchesPiForEveryPrefix` needs `.upstream/current` (absent in the runner tree), not a change of this slice.

### (a) Upstream files

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

### (b) PORT_MAP rows to add / replace

| source | PiG | status |
|---|---|---|
| `packages/durable/src/testing/commit-writes.ts` | `durable/durabletest/commit_writes.go` | ✅ |
| `packages/durable/src/storage/indexeddb.ts` | none | designed-out (browser-only, see (a)) |

The rows of `memory.ts`, `jsonl/storage.ts`, `sqlite/storage.ts`, `storage-conformance.ts`, `storage-benchmark.ts` keep their PiG files; only their upstream pin moves.

### (c) Tests (per upstream test file)

- `test/memory-storage.test.ts`: ported: the conformance registration, "does not expose retained state through a prepared commit". designedOut: "delivers the receipts of the batches applied before one whose apply throws, then rejects": `PreparedMemoryCommit.Apply` cannot fail in Go (no throwing apply) and `MemoryStorage` cannot be subclassed to replace `prepareCommit`; the deferred receipts in `Commit` cover a panic (Go's `finally`), untested.
- `test/jsonl-storage.test.ts`: ported: every migrated `commitWrites` call; "poisons the storage when an append fails after its batches applied, before a waiting read goes on" (`TestJsonlStoragePoisonsWhenAnAppendFailsAfterItsBatchesApplied`); "serializes the complete candidate..."; `JsonlStorage batches` "appends a batch's markers in one write and recovers a shorter prefix from a torn one" (`TestJsonlStorageBatches`); "removes complete unconfirmed sidecar tails..." now commits a conversation write at seq 4 (Pi dropped the empty commit). Go test `TestJsonlCommitNamesAFailedSidecarPathJoin` (Go-only, `:827`) changed: Pi at T poisons after a path-join failure once the batch applied; it asserts the poisoned error with that cause. Concurrency of the read test: Go starts the commit and the read on goroutines; the read sees the batch whether it waited or ran after.
- `test/sqlite-storage.test.ts`: ported: migrated calls; `SqliteStorage batches` (`TestSqliteStorageBatches`): "rejects the whole call, the batches before it included, when a later batch's check read fails" and "rejects a batch call whose check read fails in the database, and refuses only for its own checks". Pi's variants (undefined rejection, synchronous throw, primitive rejection, Error rejection) collapse to one Go case each: a Go database call fails only by an error return.
- `test/sqlite-facade.test.ts`, `test/sqlite-migrations.test.ts`, `test/sqlite-cloudflare.test.ts`: ported (only `commitWrites` calls changed): `TestPortableSQLiteFacadeSettlement`, `TestDurableSQLiteMigrations`, `TestDurableObjectSqliteAdapter`.
- `test/indexeddb-storage.test.ts`: designedOut with the file (browser-only). Its new `commit` cases "stores the batches before a refused one, and the storage goes on", "resolves a copy against the state an earlier batch of the call left", "refuses a batch whose copy fails before it writes anything, keeping the batches before it" are backend-neutral and covered by the shared suite cases "stores the batches before the first one refused, and none after it" and "resolves a batched copy against the state the earlier batches left" (run by memory, JSONL, SQLite, Durable Object SQLite); the two "rejects with nothing stored when IndexedDB fails during a batch's checks / writes" cases are IndexedDB-specific (SQLite's equivalents are `TestSqliteStorageBatches`).
- Storage benchmark scenarios: no scenario changed; seeding goes through `CommitWrites`.

### (d) Async contracts

One row per async source file; Go form = blocking `Commit` (the Session serializes commits).

| file | contract | evidence |
|---|---|---|
| storage/memory.ts | `Commit` applies batches in order under the storage lock; `receive(index, seq)` once per stored batch, in order, synchronously, after the last apply and before the lock releases (so before any read sees the batches and before the next commit); refusal ends the call with `Stored` batches; a closed storage is an error | test:durable/durabletest/storage_conformance_cases_test.go (receive case) via `TestMemoryStorageConformance` |
| storage/jsonl/storage.ts | reads block while a commit's append runs (`writing`), return after it or the poison error; `ctx` cancels a waiting read; receipts after the markers are appended, sidecar reclamation after the receipts | test:durable/storage/jsonl/jsonl_storage_test.go#TestJsonlStorageBatches, #TestJsonlStoragePoisonsWhenAnAppendFailsAfterItsBatchesApplied |
| storage/sqlite/storage.ts | one transaction per call; receipts after COMMIT returned; database failure = error return, whole call rolled back, no receipts | test:durable/storage/sqlite/sqlite_storage_test.go#TestSqliteStorageBatches |

### (e) New public Go API

`durabletest.CommitWrites(ctx, storage, writes) (durable.Seq, error)`. `MemoryStorage.Commit`, `JsonlStorage.Commit`, `SqliteStorage.Commit` take `(ctx, [][]durable.StorageWrite, func(index int, seq durable.Seq)) (durable.CommitResult, error)`. No other exported name changes.

### Open points for the integrator

Callers of the old `Storage.Commit(ctx, writes, adopt)` outside this slice, `durable/session`, `durable/harness`, `durable/storage/store`:
- `durable/codemode/call_restart_pi_test.go:179,210` (`queuedStorage`), `durable/codemode/call_test.go:31,40` (`countHeldStorage`), `durable/codemode/support_test.go:585,598` (`heldStorage`): wrappers implementing `Commit` and delegating to `MemoryStorage.Commit`.
- `durable/storage_rejected_copy_test.go:42` and `durable/core/driver/harness_restart_failure_core_test.go:139`: direct `Commit(ctx, writes, func(durable.Seq) {})`; use `durabletest.CommitWrites`.
- `durable/storage/session_pi_oracle_test.go` (this slice's directory) uses `session.CreateSession`/`CommitWith` and blocks the whole `durable/storage` test package until the session slice compiles.


---
# Slice `chordstore`

## Rows of slice `chordstore` (lane N-DUR, Pi `e5c6182b` / T `6d4b7443`) for the integrator

Branch `fix/follow-ndur-chordstore` on `fix/follow-ndur` `9f56026a3a`. Delete this file after transcribing. Two phases: (1) the Chord store's `CommitSeries` (replaces `CommitInBatch`), (2) the new Go package `durable/storage/store` (`StoreStorage`, the D-store files that are not on next yet).

### Red records

- Phase 1, red commit `be4bafbdcb` (`test(chord/store): cover CommitSeries (red)`): `go vet ./chord/next/store/` -> `vet: chord/next/store/store_test.go ("a commit series", merged from next, KFold 34e3cae773):17:39: undefined: SeriesCommit`. Green: `go vet ./chord/next/store/ && go test -count=1 -race ./chord/next/store/` -> `ok ... 29.8s`.
- Phase 2, red commit `68bd891721` (`test(durable/storage/store): cover StoreStorage (red)`), run on the merge of `fix/follow-ndur-storage` @875c5c04b3 (`4f917e2e91`, so the batch `Storage` shape compiles) with the package's production files absent: `go vet ./durable/storage/store/` -> `vet: durable/storage/store/support_test.go:35:3: undefined: StoreStorage`. Green: `go vet ./durable/storage/store/ ./chord/next/...` clean; `go test -count=1 -race ./durable/storage/store/ ./chord/next/store/` -> both `ok` (17.6s, 29.2s); `go test -count=1 -run TestReachLibrariesPortPublishedEntryPoints ./test/parity/interface-closure/autobind/` -> `ok`.

### (a) Upstream files

| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| packages/chord/src/next/store/store.ts (e5c6182b part: `commitSeries`, `SeriesCommit`, `SeriesResult`, `#series`, `#writeSeries`, `#checkCommit`, `#writeCommit`, `#closeSeries`, `writeMeta`, `watchCalls`, `#write` split) | ported | chord/next/store/store.go | test:chord/next/store/store_test.go ("a commit series", merged from next, KFold 34e3cae773)#TestCommitSeries | `CommitInBatch` removed (no other caller existed in the tree); `Commit` and `CommitSeries` share `write` and `writeMeta`; `watchCalls` is the `watchedCalls` executor (a failed call marks the check a backend failure, not a refusal) |
| packages/chord/src/next/store/store.ts (4b535d57 part: `announceTable`, `tableVersion`, `#announced`) | pending | none | none | not e5c6182b: it belongs to commit 4b535d57 (slice 7b, reload in the kernel) together with claims.ts, table-handle.ts and table-migration.ts, which this lane does not own; the lane that ports 4b535d57 adds it |
| packages/chord/src/next/store/README.md | designed-out | none | none | the Go store has no README; the Go doc comments of `CommitSeries`/`SeriesCommit` carry the text |
| packages/chord/src/next/store/index.ts (`SeriesCommit`, `SeriesResult` exports) | designed-out | none | none | export barrel: Go exports from the package (`SeriesCommit`, `SeriesResult` in store.go) |
| packages/chord/test/next/bench/store.ts (+15, `commitWrites` in the durable benchmarks) | pending | none | none | no Go port of `bench/store.ts` exists in `chord/next` (the Go benchmarks are `host/bench_*_test.go`, `link/bench_test.go`); nothing to update |
| packages/durable/src/storage/store/commit.ts | ported | durable/storage/store/commit.go | test:durable/storage/store/store_storage_commit_test.go#TestStoreStorageRefusals | `DurableCommit` is a `SeriesCommit` (`Series()`) and a `StagedWrite`; `RecordType` and `CommitTables` exported as in Pi |
| packages/durable/src/storage/store/documents.ts | ported | durable/storage/store/documents.go | test:durable/storage/store/store_storage_test.go#TestStoreStorageArchives | `meta` is the JSON of `durable.DocumentRecord` (Go struct key order, `createdAt` after `key`; Pi writes the create's key order): the text is never compared, only parsed |
| packages/durable/src/storage/store/index.ts | designed-out | durable/storage/store/index.go | none | re-export barrel; `index.go` carries the package doc and the shared Go forced differences |
| packages/durable/src/storage/store/queries.ts | ported | durable/storage/store/queries.go | test:durable/storage/store/store_storage_test.go#TestStoreStorageReviewRegressions | `TableQueries` keeps its shapes in a mutex-guarded map (reads run concurrently) |
| packages/durable/src/storage/store/storage.ts | ported | durable/storage/store/storage.go | test:durable/storage/store/store_storage_test.go#TestStoreStorageClaimsAndClose | `Open(ctx, store, owner)`; `Commit` through `CommitSeries` (a staging failure is the batch's refusing check); optional `PublishingStorage`: `Publishing` = the attached host's `batch.Within` |
| packages/durable/src/storage/store/tables.ts | ported | durable/storage/store/tables.go | test:durable/storage/store/store_storage_test.go#TestStoreStorageHeadMarkers | a `json(...)` cell is a JSON tree at staging (Go structs are not JSON values to the store's codec): marshalled like the other Go storages, parsed to a tree, marshalled again by the store |
| packages/durable/test/store-storage-support.ts, store-storage-node.ts | ported | durable/storage/store/support_test.go (`openStoreStorage`, `OpenNodeSqliteStorage`) | test:durable/storage/store/store_storage_test.go#TestStoreStorageConformance | the opener of the harness-suite-on-store; the re-run itself is pending (see (c)) |
| packages/durable/vitest.store.config.ts | pending | none | none | needs a hook in `durable/harness` (NDurHarness's package, not edited here); exact proposal in the open points |

### (b) PORT_MAP rows to add / replace

| source | PiG | status |
|---|---|---|
| `packages/chord/src/next/store/store.ts` | `chord/next/store/store.go` (`CommitSeries` replaces `CommitInBatch`; `announceTable`/`tableVersion` belong to 4b535d57, pending) | 🟡 |
| `packages/durable/src/storage/store/{commit,documents,queries,storage,tables}.ts` | `durable/storage/store/{commit,documents,queries,storage,tables}.go` | ✅ |
| `packages/durable/src/storage/store/index.ts` | `durable/storage/store/index.go` (package doc) | ✅ |

### (c) Tests

- `packages/chord/test/next/store/store.test.ts` ("a commit series", 9 `it`, the `it.each` as 3 subtests): all ported to `chord/next/store/store_test.go ("a commit series", merged from next, KFold 34e3cae773)` (same inputs and expected results; Go has no microtasks: "runs a commit queued behind a series after the series' receipts" queues both calls through `startQueued`, which takes the queue place at the call). missing: none. designedOut: none.
- `packages/durable/test/store-storage-commit.test.ts` -> `durable/storage/store/store_storage_commit_test.go`: ported: one transaction per call (2), refusals (the first, staging, the malformed pair against Memory, the two later-state cases, the copy reading earlier batches), failures (backend failure, failed COMMIT, the 3-batch statement-by-statement matrix with statement counts 29 / 23, closed storage / stopped store, queued call released), durability, other users (3). missing (pending): "publishes each commit of a call in a host batch of its own, after the call's one transaction" (needs `SessionImpl` of NDurSession); designedOut: "refuses a batch whose rows staging cannot encode, keeping the batches before it" (an `EntryId` is an `int64`: Go cannot hold the non-integer id 1.5 the case sends; the encode failure path itself is `table.Encode`'s error in `stage`), "GC / keeps nothing of a settled call" (WeakRef/gc).
- `packages/durable/test/store-storage.test.ts` -> `durable/storage/store/store_storage_test.go`: ported: conformance (memory and across reopen), claims and close (2), the store's semantics (2), archives (2; Chord's own document in the same store is created with `chordstore.CreateDocument` for owner `app.memo` instead of a host object), head markers, reads, commits (the publishing case at storage level without a Session, the four-statement guard), review regression. missing (pending): "publishes each commit of a Session inside the attached host's batch" (SessionImpl), "a derive over the conversation view and the task graph" and "a deferred Durable commit, then an immediate one" (Harness over a host: NDurHarness/NDurSession). designedOut: "GC / a closed StoreStorage is collected while its store stays open".
- Harness test files over `StoreStorage` (`vitest.store.config.ts`): pending, see open point 2.

### (d) Async contracts

| file | contract | evidence |
|---|---|---|
| chord/next/store/store.go `CommitSeries` | blocks until its turn in the commit queue, then one backend transaction; per commit: check on a step at the next seq (a failed backend call of the check rolls the series back and returns that error, store goes on; any other check error is the refusal: the series stops, prefix commits), writes (an error rolls back, store goes on); a failed BEGIN/COMMIT/ROLLBACK stops the store `storage_failed`; after COMMIT memory (lastSeq, allocator value, catalog copy only if a commit changed a table), then `receive` per stored commit in order before the next queue entry; a failing receive stops the store; `ctx` counts the step (no abort once queued) | test:chord/next/store/store_test.go ("a commit series", merged from next, KFold 34e3cae773)#TestCommitSeries |
| durable/storage/store/storage.go `Commit` | same through `CommitSeries`; `Close` refuses new calls, waits for commits underway, releases the claims, leaves the store open; reads are guarded reads of the store, not waited for by Close | test:durable/storage/store/store_storage_commit_test.go#TestStoreStorageFailures |
| durable/storage/store/storage.go `Publishing` | runs `publish` once in the attached host's `batch.Within(...)`; plainly without a host | test:durable/storage/store/store_storage_test.go#TestStoreStorageCommits |

### (e) New public Go API

- `chord/next/store`: `SeriesCommit{Check func(*Step) error; Writes []StagedWrite}`, `SeriesResult{Stored int; Refused error}`, `(*ChordStore).CommitSeries(commits, receive func(index int, seq int64, ids []int64) error, ctx) (SeriesResult, error)`. Removed: `(*ChordStore).CommitInBatch`.
- `durable/storage/store`: `StoreStorage`, `Open(ctx, store, owner)`, `DurableCommit`, `NewDurableCommit`, `CommitTables`, `RecordType`, `TableQueries`, `StorageQueries`, `Condition`, `RecordRow`, the tables `RecordIds`/`Conversations`/`Entries`/`EntryHeads`/`Tasks`/`Submissions`, row builders (`ConversationRow`, `EntryRow`, `TaskRow`, `SubmissionRow`), `EncodeIndexed`, `ScopeNamespaces`, `NamespaceOf`, `AddressOf`, `HistoryOf`, `EngineRecord`, `DurableRecord`, `RecordOfEntry`, `AliveAt`, `DocumentRead`, `ReadAt`.
- Autobind: library entry `durable:./storage/store` -> `./durable/storage/store` added to `libraryEntries`.

### Open points

1. `chord/next/store` `Attachment`'s doc still says adapters "adopt" in the batch: Pi's rename to "publish" is wording only.
2. Re-running the `durable/harness` tests over `StoreStorage` (Pi's `vitest.store.config.ts` replaces the SQLite opener by `store-storage-node.ts`, leaves four raw-SQL tests out): proposal for NDurHarness: the harness tests open their storage through one helper (the Go counterpart of `openNodeSqliteStorage`); give it a package-level `var openStorage = func(t, path) durable.Storage { sqlite... }` and add an `init`-time switch (environment variable `PIG_DURABLE_STORE=1` or a build tag `storestorage`) in a `_test.go` file of `durable/harness` that points it at `store.OpenNodeSqliteStorage`-equivalent (`durable/storage/store` exports `Open`; its test helper `openStoreStorage` is the opener to copy), skipping the four tests named in `vitest.store.config.ts` (`resumes a retry backoff after reopen`, `sleeps again for a retry stored as a running phase`, `polls a checkpoint stored without a thinking level`, `refuses to create the root of an empty storage`). Pending until `durable/harness` has such a helper.
3. Design note: the per-commit `receive` of `StoreStorage.Commit` ignores `ids` (a Durable commit creates no engine ids through the step; its documents carry explicit ids, as in Pi).


---
# Slice `sessionunits`

## Rows of N-DUR slice `sessionunits` (Pi `6d4b7443`, group commit `e5c6182b`) for the integrator

Branch `fix/follow-ndur-sessionunits` on `fix/follow-ndur` `9f56026a3a`. Red commit `602aad6f53` (tests only); port commit `fe4ef64288`. Delete this file after transcribing.

Red command (xd670, the port files removed from the synced tree, tests copied into a scratch package `durable/zzunits` created by the command itself):
`go vet ./durable/zzunits/` -> `vet: durable/zzunits/cached_storage_test.go:110:50: undefined: zzunits.CachedStorage`.
Green: the same scratch package with the three files and their tests, `go vet ./durable/zzunits/ && go test -count=3 -race ./durable/zzunits/` -> `ok`; `gofmt -l` prints nothing for the six files. `durable/session` itself does not compile until the session slice lands, so it was not built; the scratch copy needs nothing outside `durable`. The test backend is a small in-test Storage (`countingBackend`: it embeds `durable.Storage`, implements `Commit`, `Task` and counts reads), because `storage.MemoryStorage` and `durabletest.CommitWrites` only take the new `Commit` when the storage slice lands; the tests do not depend on them.

### (a) Upstream files

| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| packages/durable/src/session/keys.ts | ported | durable/session/keys.go | test:durable/session/keys_test.go#TestGatedStorageReadsTouchTheirKeysFirst | key functions return strings of the same text; `OwnedKey` is generic over `TaskId \| string` (Pi's union); `ROOTS_KEY/INTENT_KEY/ALL_KEYS` are `RootsKey/IntentKey/AllKeys`; `touch` is a blocking `func(ctx, key) error` (Pi's `Promise<void> \| undefined`), a failed touch fails the read and skips it; `entry`'s overloads are `Entry`/`VisibleEntry` (the latter touches the conversation, then the entry, as `#after` chains) |
| packages/durable/src/session/conversation-tails.ts | ported | durable/session/conversation_tails.go | test:durable/session/conversation_tails_test.go#TestConversationTailsAnswersCutoffFromNewestMarkerOnlyAtOrAfterIt | `latest`/`marker` take blocking `read` callbacks returning `(value, error)`; `peek` is `Peek(id) (EntryId, TailState)`; `size`/`markers` getters are `Size()`/`Markers()`; guarded by a mutex never held across `read` (several goroutines may Apply and read) |
| packages/durable/src/session/cached-storage.ts | ported | durable/session/cached_storage.go | test:durable/session/cached_storage_test.go#TestCachedStorageKeepsEndedTasksATaskWaitsOnUntilItMovedOnFromItsWait | `published` is `Published`, `sizes` is `Sizes()` (`CachedSizes`), `readsInFlight` is `ReadsInFlight()`; `Lru` is the unexported generic `lru` (shared with the tails' two bounded maps, which Pi writes as `use`/`put` over a `Map`); one mutex guards the state, never held across a backend read; `scopeOf` is `ScopeOf(storage) func(ctx, publish)` (nil when none) |

### (b) PORT_MAP rows to add

| source | PiG | status |
|---|---|---|
| `packages/durable/src/session/keys.ts` | `durable/session/keys.go` | ✅ |
| `packages/durable/src/session/conversation-tails.ts` | `durable/session/conversation_tails.go` | ✅ |
| `packages/durable/src/session/cached-storage.ts` | `durable/session/cached_storage.go` | ✅ |

### (c) Tests

- `test/session-cached-storage.test.ts` (7 cases): all ported (`cached_storage_test.go`). Added: `TestCachedStorageReadFromSeveralGoroutinesWhileCommitsPublish` (the `-race` case for the readers). designedOut: none; the "frozen" half of "keeps the written record itself, frozen: no copy" (`Object.isFrozen(record.state)`) has no Go counterpart, the port asserts the sharing (two reads return the same pointer, the written record's checkpoint pointer is the one kept).
- `test/session-conversation-tails.test.ts` (5 cases): 4 ported; designedOut: "freezes the markers it keeps" (`Object.freeze`; kept markers are shared pointers, immutable by convention). Added: `TestConversationTailsPeekIsThreeStated`, `TestConversationTailsReadErrorsAreReturnedAndNothingIsKept`.
- `keys.ts` has no test file of its own upstream; `keys_test.go` (key texts, `TableKeys`, `GatedStorage` touch order and failed touch) is added. Upstream tests that exercise keys indirectly: `test/session-group-commit.test.ts` (session slice).

### (d) Async contracts

| source | contract | evidence |
|---|---|---|
| session/keys.ts (`GatedStorage.#after`) | the read runs after `touch(key)` returned; `touch` may block until the newest unpublished writer's gate opened, nil at once when none; sequential touches (conversation, then entry or appends) in Pi's order; a touch error (ctx cancelled) is the read's error and the read does not run; `Commit`/`MintId`/`Close` do not touch | test:durable/session/keys_test.go#TestGatedStorageReadsTouchTheirKeysFirst, #TestGatedStorageFailedTouchSkipsTheRead |
| session/conversation-tails.ts (`latest`, `marker`) | `read` runs without the lock; a result is kept after it returns, a failed read keeps nothing and returns its error; the marker of a cutoff is kept only when no newer entry can change it | test:durable/session/conversation_tails_test.go#TestConversationTailsReadErrorsAreReturnedAndNothingIsKept |
| session/cached-storage.ts (`#keep`) | a read in flight registers a version for its record; a publication that wrote the record meanwhile bumps it and the read result is returned but not kept; versions exist only while a read is in flight; read errors decrement the count and keep nothing | test:durable/session/cached_storage_test.go#TestCachedStorageKeepsReadOnlyWhenNoCommitOverlapped, #TestCachedStorageDropsReadOverlappedByPublicationAndKeepsVersionsOnlyForReadsInFlight |

### (e) New public Go API (interface IDs for the integrator)

`durable/session`: `TaskKey`, `ConversationKey`, `EntryKey`, `AppendKey`, `SubmissionKey`, `RequestKey`, `DocumentKey`, `IncarnationKey`, `ScopeKey`, `OwnedKey`, `RootsKey`, `IntentKey`, `AllKeys`, `TableKeys`, `GatedStorage`, `NewGatedStorage`; `ConversationTails`, `NewConversationTails`, `HeadMarker`, `TailState`, `TailUnknown`, `TailNone`, `TailKnown`; `CachedStorage`, `NewCachedStorage`, `CachedSizes`, `ScopeOf`.

### Design notes / open points

- `CachedStorage.Publishing` is always callable, because a Go method cannot be absent: with a backend that has no `PublishingStorage` it runs `publish` directly, which is what the Session does for a Storage without the capability. A caller that must tell the two apart uses `ScopeOf(backend)`.
- Cached records are shared pointers to a struct copy of the written record (a shallow copy of the `Value`, since a write holds it by value); callers must not mutate what Storage returns.
- No changelog fragment: no user-visible behavior change.


---
# Slice `session`

## Rows of lane N-DUR slice `session` (Pi `6d4b7443`, group commit `e5c6182b`) for the integrator

Branch `fix/follow-ndur-session` on `fix/follow-ndur` `9f56026a3a` (merged: `fix/follow-ndur-sessionunits` `fe4ef64288`, `fix/follow-ndur-storage` `40221e5662`). Red commit `770c1cc493` (tests only); port commit `5762fb403e` and the commits after it. Delete this file after transcribing.

Red record (xd670, `~/ndur-tools/ndur-run.sh session -- 'go vet ./durable/session/...'` at `770c1cc493`): fails to compile, first lines
`durable/session/transaction.go:290:20: cannot use (*Transaction)(nil) ... *Transaction does not implement durable.Tx (missing method LatestEntryId)`,
`durable/session/guard.go:58:10: ... rangedGuard does not implement durable.Storage (wrong type for method Commit)`,
`durable/storage/memory.go:307:25: ... *MemoryStorage does not implement durable.Storage (wrong type for method Commit)`.
Green: `go vet ./durable/session/...` and `go test -race -count=1 ./durable/session/...` pass in the container with the two files that import `durable/harness` removed there (`codemode_checkpoints_upstream_test.go`, `session_document_retention_test.go`: they build when the harness slice compiles); `go test -race -count=20 -run 'GroupCommit|Writer|Adoption|Publication|CommitInternal|OnJobEnd|KnownLatest|Touch|AWake'` passes (one failure of my own test string in `TestAReadOnlySession...` fixed after). `gofmt`, `go fix -diff`, `make lint-changed` and the changelog/divergence gates were NOT run (budget): pending.

### Go mapping design note (also the header of `durable/session/session.go`)

Pi's event loop gives group commit two guarantees Go's scheduler does not; the Session keeps them with the shared `internal/entered` mechanism (a goroutine has "entered" once it is parked in a wait, blocked on a lock, or has exited):
1. A publication queues one dispatch to the host's observers, which runs a microtask later, before every wait the commit resolves. Here `writerHost.Publishing` runs the part's publication inside the Storage's scope (`PublishingStorage`, else directly), then `endPublication` runs the queued dispatch, then the work the observers deferred (`DeferUntilPublished`: waiter wakes, aborts), and returns once the goroutines those wakes readied have entered (`wakeAndEnter`: goroutines parked before the wake and runnable after it, from two goroutine dumps; only when something was deferred).
2. Outcomes are delivered in line order and the continuations of what one part resolved start before the next part of the call is published (macrotask hop). A fence (`delivery` queue in line order) is served by one delivery goroutine the Session owns: it releases the caller of each ready job in line order and waits for that caller to have entered before it releases the next; the writer's `Hop` before the next part waits for that goroutine to be idle, then for the watch deliveries (`scheduler.AwaitEntered`). A part's outcome settles synchronously on the writer's goroutine (`Part.OnSettled`), so readiness never depends on a caller's wake-up.
Differences from Pi, each deliberate: a part's keys leave the unpublished set after its own observers ran and before the dispatch (Pi unregisters first; a Go region holding the scheduler lock between a key's removal and its observer would see a clean key and stale state); `Part.Published` (touch of changed documents, unpin of taken holds) runs after the keys left so an eviction sweep it arms sees them gone; a failure caused by an own observer's panic fails the Session before the part resolves for its caller (Pi: one microtask later, still before the caller's continuation); a Storage scope that runs the publication later runs the dispatch inside that scope; gate waits are not cancellable (Pi's are not either); a `Close` waits for the fence to empty.

### (a) Upstream files

| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| packages/durable/src/session/writer.ts | ported | durable/session/writer.go | test:durable/session/session_group_commit_test.go#TestGroupCommitBatching, test:durable/session/session_group_commit_internal_test.go#TestWriterFailsTheSessionForAThrowInTheReadCachesWriteThrough | `Writer`, `PartSpec`/`Part`, `WriterHost` (+`Publishing`, `Hop`), `PublishFailure`; per-part outcomes of design doc 3.4; one owned goroutine (`Wait` joins it); `Part.Wait`/`OnSettled`/`Published` in place of the promise |
| packages/durable/src/session/session.ts | ported | durable/session/session.go, guard.go, held.go, retention.go, woken.go | test:durable/session/session_group_commit_test.go, session_line_api_test.go, session_adoption_test.go, session_observers_test.go | logical admission (takeHeld before keys, `Hooks.MembershipKeys`), unpublished write set per key (`Touch`/`AwaitTouch`/`Drained`), `MAX_OUTSTANDING` (`waitRoom`), outcome fences with exemption (`CommitInternal`), deferred commits as parts without writes, cache write-through + tails at publication (`writerHost.Stored`), `readOnLine((storage))` over `GatedStorage` (`rangedGated` keeps the entry range capability), `LatestEntryId`/`LatestHeadMarker`/`KnownLatestEntryId`, gated cold loads, `*` barriers (close, unload, version reload, hold timer), task-document index, sweep skips documents with unpublished changes, `LineSizes`, `OnJobEnd`; `GuardedStorage.Commit` guards the call and the writer fails the Session |
| packages/durable/src/session/transaction.ts | ported | durable/session/transaction.go | test:durable/session/session_group_commit_test.go | `Tx.LatestEntryId`, host `touch/taskDocuments/latestEntryId/latestHeadMarker`, `CreatedTaskIds`, `WriteKeys`, gated retirement scans; "frozen shared read results" has no Go form (see (c)) |
| packages/durable/src/session/forks.ts | unchanged-observable | durable/session/forks.go | none | no delta; its reads go through the gated storage the transaction holds |
| packages/durable/src/session/observation.ts (guardObserver) | unchanged-observable | durable/session/observation.go | none | no code change (its comment still says adopt) |
| packages/durable/src/session/{keys,conversation-tails,cached-storage}.ts | not mine | see sessionunits.md | | |

### (b) PORT_MAP rows to add / replace

| source | PiG | status |
|---|---|---|
| `packages/durable/src/session/writer.ts` | `durable/session/writer.go` | ✅ |
| `packages/durable/src/session/session.ts` | `durable/session/{session,guard,held,retention,woken}.go` | ✅ (woken.go has no upstream: Go mapping) |

### (c) Tests (per upstream test file)

- `test/session-group-commit.test.ts`: ported (`session_group_commit_test.go`, `session_group_commit_internal_test.go`, support in `group_commit_support_test.go`): batching, gates (leaked entry, creation at address, conversation creation, five entry reads, unkeyed scan, line read after un-awaited commit, held document readers x2), outcomes x2, failures per part (reject, other parts, first error after another failure, rejection value, refusal value, adoption throws), publication throws (held adoption x2 variants, cache write-through via `Writer` with its own host), deferred commits x2, publication hop, storage scope x2, failure between publications x3, upkeep x4, backpressure, randomized x5 seeds. designedOut: the `undefined`/`null` rejection values (a Go error is never nil; the cases are ported with an error value); `Transaction.prototype.adopt`/`adoptHeld`/`CachedStorage.prototype.published` spies become scope-aborted prepared changes and a fake `WriterHost`; `eventually`/`flush`/`settled` are `awaitQueued`/`quiesce`/`settled` over `entered`. Added: `session_line_api_test.go` (commitInternal exemption, onJobEnd, known tails, touch/drained, wake-before-next-publication, read-only Session).
- `test/session-support.ts`: ported (`sessiontest/support.go`: `Commit(batches, receive)`, `RefusingAt`; the Pi `LateResolvingStorage` stays with the harness slice).
- `test/session-adoption.test.ts`: ported, new order expectations. Missing: "publishes a JSONL commit once the storage reclaimed its sidecar and resolved" (needs a recording env for the Go JSONL storage; the old file's comment said the order differed, it now matches Pi's): pending.
- `test/session-observers.test.ts`: ported (`adopting: false`). `test/session-document-retention.test.ts`: the `storage.commit` wrapper has no Go counterpart; the Go test only changed `ReadOnLine` call sites; the file builds only with the harness slice.
- Existing Go tests updated to Pi's new expectations: `session_documents_test.go` (settles admitted commits before close), `session_observers_test.go` (attaches atomically), `watch_doc_admission_test.go`, `snapshot_admission_test.go` (a call queues behind a job that holds the line at a gate, via `lineadmission`, not behind a commit in storage), `session_adoption_internal_test.go`, `line_ticket_test.go`, deferred/retention `ReadOnLine` call sites; `retentionClock` moved to `retention_clock_test.go`.
- Not run (budget): `session_checkpoints_migrations_test.go` etc. ran green in the full-package run only with the two harness-dependent files removed.

### (d) Async contracts

| file | contract | evidence |
|---|---|---|
| session/writer.ts | one goroutine sends everything admitted while no call is in flight as one `Commit`; parts publish in line order with a hop between; a refusal/rejection/adoption failure fails the Session after the received parts are published and settles every part (own error or `SessionFailed`), gates open once settled; a call settling after another failure publishes nothing; never strands an outcome or gate (`abandon`); `Wait` joins it | test:durable/session/session_group_commit_test.go#TestGroupCommitFailuresPerPart, #TestGroupCommitAFailureBetweenTwoPublications |
| session/session.ts | jobs run one at a time on their callers' goroutines; the line is released after callback, settlement, admission; reads wait at gates (never cancellable); outcomes delivered in line order by the fence (exempt jobs at once); backpressure at 256; close drains writer and fence, stores holds through the writer; `Touch` takes only the writer's and the job lock | test:durable/session/session_group_commit_test.go#TestGroupCommitOutcomes, #TestGroupCommitBackpressureAndBounds, session_line_api_test.go |
| session/session.ts (GuardedStorage.commit) | guarded call, not failing the Session itself | test:durable/session/session_adoption_test.go |

### (e) New public Go API (`durable/session`)

`Writer`, `NewWriter`, `WriterHost`, `PartSpec`, `Part`, `PublishFailure`, `PublishFailureKind` (+ `PublishAdoption`, `PublishListener`); on `*SessionImpl`: `Touch`, `AwaitTouch`, `AssertHealthy`, `Drained`, `LineStorage`, `OnJobEnd`, `LineSizes` (`LineSizes`), `LatestEntryId`, `KnownLatestEntryId`, `LatestHeadMarker`, `CommitInternal`, `CommitInternalWithTicket`; `ReadOnLine`/`ReadWithTicket` now take `func(storage durable.Storage) (any, error)`; `Hooks.MembershipKeys`; `(*Transaction).CreatedTaskIds`, `WriteKeys`, `LatestEntryId`; `DocumentCacheSizes.TaskDocuments`/`Tails`; `SnapshotErased`/`WatchDocErased`/`ObserveDocErased`/`SnapshotAsOfErased` report line admission through `lineadmission.From(ctx)`.

### Open questions

- NDurHarness reports a deadlock in their scheduler tests with many `TaskScheduler.reconcile` goroutines parked in `CommitInternalWithTicket` waiting for the line (`session.go onLine`). Not reproduced here and not diagnosed. Candidates on the Session side: a job holding the line in `waitRoom` (256 outstanding) while the fence head is a ticket that was taken and not yet used or released (the delivery goroutine only releases ready heads); a delivery goroutine waiting (`entered.AwaitBlocked`) for a caller that keeps running. A goroutine dump of the hang is needed.
- `DeferUntilPublished` still defers for any goroutine while a publication is in progress (as before); a call from a non-publishing goroutine would be deferred.
- `Part.Published`/hop use two goroutine dumps per publication that deferred wakes: a cost on very large goroutine counts.


---
# Slice `harness`

## Rows of N-DUR slice harness (Pi e5c6182b, d280e1ff, 35dfa757 final state at 6d4b7443)

Branch `fix/follow-ndur-harness`, merged into `fix/follow-ndur`. Statuses below are final: see the "Final run record" at the end (the first draft of this file was written before the Session line compiled; its "unverified" marks are superseded).

### Red record
- d280e1ff: red commit 18a6b5aa65 (tests only: `ProviderRequest`, `GenerationHooks.BeforeProviderRequest` undefined). The base did not compile at all (`durable/session`, `durable/storage/sqlite` fail on the new `Storage.Commit`), so no failing test line exists; port commit a5436df516, fix 4th commit "providerPayload returns nil when no hook replaced the body".
- e5c6182b: the port (48f0603761) was committed before its tests because the Session slice did not compile then. The first run of the ported cases on the merged tree failed as recorded in "Run record" below (TestLiveDeltas 8 vs 5 commits, TestTaskContextReadIsExtendedWithOnlyNewerEntries, the abort/close/runtime waits, and a hang), then went green after the fixes. Pi's group-commit cases of `harness-group-commit.test.ts` and `harness-idle-scopes.test.ts` (ported by hgc/hidle) passed at first run because the production code was already on the base: there is no separate red for them.

### (a) Upstream files
| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| src/harness/scheduler.ts | ported | harness/scheduler.go, scheduler_region.go, scheduler_idle.go, scheduler_index.go | test:harness/scheduler_retention_chains_test.go#TestSchedulerRetentionChains (changed case), harness_ownership_tools_test.go (changed case) | see design note |
| src/harness/context.ts | ported | harness/context.go | test:harness/context_kept_range_test.go#TestKeptContextRanges, context_range_test.go | `current`, `rangeEntry`, `advanceRange`; bounds from `Session.LatestEntryId/LatestHeadMarker` |
| src/harness/generation.ts | ported | harness/generation.go | harness_tools_test.go#TestGenerationHooks | beforeProviderRequest; attempt stored as ProgressCommit; cutoff from `tx.LatestEntryId` |
| src/harness/tool.ts | ported | harness/tool.go | harness_live_deltas_test.go | running slot + nested arguments in a ProgressCommit, intent commit separate; safe rerun re-shows running (arguments in sorted member order: Go decodes the checkpoint without member order) |
| src/harness/harness.ts | ported | harness/harness.go | - | `ReadOnLine(func(storage))`, `MembershipKeys` hook, no storage in components |
| src/harness/submissions.ts, task-graph.ts, view.ts | ported | harness/{submissions,task_graph,view}.go | existing tests | `NewSubmissions/NewTaskGraphView/NewConversationViews` lose the storage parameter |
| src/harness/types.ts | ported | harness/types.go | harness_tools_test.go | `ProviderRequest`, `ProviderPayload`, `BeforeProviderRequest` |
| src/types.ts, index.ts hooks | no Go change needed beyond the contract commit | - | - | - |
| tools/read.ts, search.ts, tool-search.ts, codemode.ts | pending (not examined): carried by M0 `dp/fix/060c-integrate` (5eb8bb963a ed99f429d6 ...) | - | - | not ported twice |

### (b) PORT_MAP: scheduler.ts row gains `scheduler_region.go`, `scheduler_idle.go`; add `harness/context_kept_range_test.go`.

### (c) Tests
- Ported: harness-tools (2 new hook cases), harness-context-range (new), harness-context (row counts 0/21/22), harness-blocked, -cancellation-barrier, -lifecycle, -tool-hooks, -nested-tools, -restart-failure (commits wrappers, `<=` retention bound), -ownership (refusal wrappers; replaced case "runs a handle operation queued behind the abort mark..."), -scheduler-indexes (commit counted per batch), -scheduler-retention (both changed cases), -structured (refusing wrapper), -live-deltas (5 commits, per-batch kinds, persistWithinMs 50), -view (+1 frame), -observers/-task-graph (adopting=false), -inbox, -submissions (reopen), -deferred-progress, -generation (persistWithinMs 100), -storage-failure (already had the note append). All Storage wrappers (harness + codemode tests) take batches; `controlledStorage` and `lateResolvingStorage` follow session-support.ts; `refusingAt` added.
- Ported by other slices of this lane: NEW harness-group-commit.test.ts (hgc.md), NEW harness-idle-scopes.test.ts (hidle.md), harness-tasks and harness-tools-recovery group-commit cases, the audit, the benches (designed-out: no Go counterpart) and the StoreStorage re-run (`PIG_DURABLE_TEST_STORAGE=store`, `make test-durable-store`) (htail.md).
- Existing Go tests that assume 14 commits per run: audited, see the Final run record.

### Region design note
`regionLocked(s, decide, refresh)` (scheduler_region.go): on the job's own goroutine, under `s.mu`: set `s.reading`, run `decide` (no state change, helpers call `recordLocked(key)`), `Session.Touch` every recorded key under the same hold of `s.mu` (so no `observe` runs between decide and touch; NDurSession removes a part's keys after its internal listeners), if any gate: drop plan/error, release `s.mu`, wait all gates, `AssertHealthy`, relock, `refresh` (`loadScopesLocked(false)`), rerun. Side effects (`setTask`, `runnable`/`cascadePending`, `endLocked`, `resolveLocked` with migrate, reports, `SettleOutcome`) only after the clean check. No goroutine per region. Rule: while holding `s.mu` never call anything that can wait for a gate (tx reads, `LineStorage` reads, `SettleOutcome`, `WithdrawInputs`): `loadChainLocked/loadScopesLocked/lineTaskLocked` and `withoutLock` release `s.mu` around them. Sites: abort, waitForTask, reconcile (`marksLocked`, withdrawn, finalize rounds), reserve (`planReservationLocked`, abandoned), step (`rulesLocked`/`progressLocked`), terminate, commitState, validateWait (`checkWaitLocked` with `ended`), OwnedTasks, gated. `MembershipKeys` runs at admission (Session `Hooks.MembershipKeys`).
Idle (35dfa757 final state, structurally Pi's): `addScopesAboveLocked`, `idleScopesLocked`, `settleIdleScopes`, `expireContextsLocked`, `markIdleLocked` (sort on clock going back), `runsNowLocked`, `earliestSleepLocked`; the integrator resolves the overlap with the carried commits ad6d659c09/fefd5ca8a3.

### (d) Async contracts
- scheduler.go: regions block the calling job's goroutine (Pi: awaits on the line); waits are released by publication of the keys' writers; Session failure surfaces through `AssertHealthy`; `observe` still settles idle waits for good and wakes via `DeferUntilPublished`; evidence: none run.
- harness.go/submissions.go/task_graph.go/view.go/context.go: `ReadOnLine(func(storage))` blocks until the job ends; no new goroutines.
- generation.go: `onPayload` runs synchronously inside the provider call; hook errors are reported by `Hooks().Each`.

### (e) New public Go API
`harness.ProviderRequest`, `ProviderPayload`, `GenerationHooks.BeforeProviderRequest`, `TaskScheduler.MembershipKeys`, `SchedulerIdleState`; `CaptureContextBounds(ctx, session, storage, id, at, visible)`; `NewSubmissions/NewTaskGraphView/NewConversationViews` without storage.

### Open points
- `durable/core/**` (its own session/driver emulation, `driver/hook.go` has no `beforeProviderRequest`, wire PROTOCOL.md) was not touched.
- Needs from siblings: `Session.{Touch, AssertHealthy, CommitInternal, CommitInternalWithTicket, LineStorage, OnJobEnd, KnownLatestEntryId, LatestEntryId, LatestHeadMarker}`, `session.Hooks.MembershipKeys`, `Transaction.CreatedTaskIds`, `durabletest.CommitWrites` (storage branch).
- Unverified inference: kept-range row counts in `harness_context_ranges_test.go` retention case (6/0/6) and `rooted 21/22`.

### Run record (merged tree, xd670, `go test -race -count=1 -timeout 18m ./durable/harness/`, after the fixes above)
Fixed since the first runs: waits that matched stacks or counted `LineJobs` behind a held storage call now use `holdLine` and `lineadmission.With` (`queueOnLineAdmitted`, `holdLine` in task_support_test.go); the generation hand-over case collects stored publications (`observeStoredLiveOps`); `TestTaskContextReadIsExtendedWithOnlyNewerEntries` expects 0 rows; superseded old cases of harness_tasks_test.go / harness_tasks_scheduling_test.go removed (NDurTail's group-commit files carry them).
Still failing (first lines of output):
- `TestOwnedConversationsFromTools/runs a handle operation queued behind the abort mark...`: `harness_ownership_tools_test.go:276: the late submit failed: context canceled`. Pi checks cancellation when a job starts; here the handle's invocation signal, cancelled by the mark's publication, cancels the submit while it waits. Owner: harness (boundConversation) / Session ctx handling.
- `TestTaskRuntime/orders runtime commits against the step`: `harness_tasks_scheduling_test.go:313: Condition was not reached` (the `LineJobs() > jobs` wait for the step: the step is admitted before the count is read; needs an admission hook on the scheduler's step).
- `TestReservationPassTakesItsLinePositionInThePublishingTurn`: `run 0: task commits first:pending | second:pending | first:running | second:running, want ... first:running,second:running`. A reservation pass no longer sees a task whose creation is unpublished (liveness-only read, group-commit §4.3), so two reservations may take two commits; the Go-only test must accept both orders.
- `TestSchedulerRetention/forgets a 20,000-deep chain of ended tasks without recursion`: hangs past 5m52s. Suspect `MembershipKeys`/`chainKeysLocked` walking the whole ended chain at every creation (O(n^2) with a `Touch` per node) or region reruns; not diagnosed.
- `TestPublicationAtAdoption` hung (old cases, removed; NDurTail's replacements are green).
- Pi oracle tests (`TestResolveAgentMatchesPi`, `TestCompactionHelpersMatchPi`, `TestContextDerivationMatchesPi`, `TestLiveDocHelpersMatchPi`, `TestPromptPlanningMatchesPi`) fail only because the runner tree has no `.upstream/current` (rsync excludes it); not a code failure.

### Final run record (head after this commit)
`go test -race -count=1 -timeout 25m ./durable/harness/` on xd670 (merged tree): every test passes except the 11 Pi oracle tests (`TestResolveAgentMatchesPi`, `TestCompactionHelpersMatchPi`, `TestContextDerivationMatchesPi`, `TestLiveDocHelpersMatchPi`, `TestPromptPlanningMatchesPi`, `TestAdmitSubmissionMatchesPiOnSeededPrograms`, `TestAdmitInputEntryMatchesPiOnSeededPrograms`, `TestTaskGraphViewMatchesPiOnSeededPrograms`, `TestToolResultHelpersMatchPi`, `TestUsageLedgerMatchesPiOnSeededPrograms`, `TestConversationViewsMatchPi`): `ERR_MODULE_NOT_FOUND` for `typebox` / `@earendil-works/pi-ai` imported from `durable/interop/` on the runner (node modules not installed there); not a code difference. No data race, no hang. `make lint-changed` findings in this slice were fixed (unused stack-matching helpers removed, import order).
Fixed after the first runs (production): `observe`'s idle-scope walk is one walk per chain per publication (`walkedUp`, scheduler_idle.go): the 20,000-deep chain no longer is O(n^2); a bound conversation's `Submit` checks the invocation signal before it queues and runs without it (Pi checks cancellation when a job starts; the Go linked ctx would otherwise cancel a started admission).
Test adaptations (all Go-only devices, not Pi cases): `queueOnLineAdmitted`/`lineadmission.With` for queued aborts; `LineJobs` waits replaced by `holdLine` forms (NDurTail's files) and a macrotask `flush()`; `TestReservationPassTakesItsLinePosition...` holds the second creation by running its callback and accepts one or two commits for the two reservations (group commit §4.3); `TestASchedulerChainReadThatFailsTheSessionEndsIt` reads a cold task (seeded live task 11 owned by ended task 10) because the Session's record cache serves anything it wrote.
Audit: no assertion of Pi's 14 commits per run exists in the Go harness tests beyond `harness_live_deltas_test.go` (now 5 stored publications) and the `commitCount()` checks of harness_conversations/conversation_busy_dedupe (unchanged in Pi, passing). The kept-range row counts (context ranges 0 / 21 / 22, retention case 6/0/6) passed in the final run.
Open: Pi's `beforeProviderRequest` is not in `durable/core/driver/hook.go` (wire protocol); `tools/read.ts`, `search.ts`, `tool-search.ts`, `codemode.ts` deltas are carried by M0 and were not examined here.

Later findings (integrator's pass, after the last run record above):
- `TestSchedulerRetentionBelowAForgottenConversation/aborts work created in a forgotten conversation below an abort-marked owner before it runs` hung intermittently (`-count=30`): with Go's Hold the `keep` child's abort handler ends it at once, so the cascade ends `keep` and then P and the late work, created below an ended owner, correctly runs. Pi's test relies on the JS timing of the abort commit (late creation always wins). Go adaptation: `keep`'s abort waits on its gate (`slowKeep`/`SlowAbort` in `retentionForgotten`) so P keeps holding while `late` is created; the scheduler recorded the `intent` key correctly. This is a Go-only determinism device, not a Pi case.
- `TestHarnessClose/completes reads queued at the seal...` failed 8/8 at `-count=40` before the Session fix `c95de2bb66` (close waits for the admitted jobs' outcomes before Storage closes).
- `TestOneDocumentPerNestedCallKeyedByItsKey` compares the two parallel nested calls' documents as a set (goroutines do not fix their creation order; the TS test awaits one microtask order).


---
# Slice `hgc`

## Rows of slice `hgc` (lane N-DUR, Pi `6d4b7443`): harness-group-commit.test.ts

Branch `fix/follow-ndur-hgc` on `fix/follow-ndur` `19ed965483`. Only new test files; no production code changed.

### (c) Upstream tests ported

#### packages/durable/test/harness-group-commit.test.ts

Go: `durable/harness/harness_group_commit_test.go` (cases), `durable/harness/harness_group_commit_support_test.go` (helpers). Four `describe` blocks are `TestGroupCommitSchedulerRegions`, `TestGroupCommitRecordsADecisionCopies`, `TestGroupCommitReadsOnTheLine`, `TestGroupCommitOutcomesAndPublicationThroughTheHarness`; each `it` is a subtest of the same name.

Ported (17 of 17), missing none, designedOut none:

- scheduler regions: "holds a live owner whose end races a creation below its ended tasks: live P, ended B and A, empty C"; "holds an owner whose end follows a creation below its child's pending end: live P, ending B, conversation C"; "ends a child before its next phase when its owner's abort mark is unpublished at its step"; "refuses the second of two opposite waits admitted behind each other"; "starts no invocation on a reservation before the reservation is published"; "runs a migration once although its reservation waits for an unpublished write of the task"; "reports a failed migration once although its reservation waits for an unpublished write of the task"; "makes a reader of the roots wait for a creation under a root a pending end would unlist"; "walks a creation's memberships past an ancestor whose pending end is unpublished"; "reserves the live work of the owner above an ancestor whose end is pending, for a creation below it"; "keeps the nodes a chain load installed for an unpublished creation through the sweep".
- records a decision copies: "marks a child in a cascade from its current record, not one its pending commit replaces"; "marks the other tasks of a failFast wait from their current records".
- reads on the line: "reads no task record from Storage for more live and awaited tasks than the record cache's count".
- outcomes and publication through the Harness: "finds the submission an unpublished commit admitted with the same request ID"; "runs a wait continuation of one commit before the next commit of the same call is published"; "signals an invocation when it closes during a call in flight, and stores that call before Storage closes".

Go mappings (header of both files): module-level `gates`/`runs`/`refusals`/`runtimes` are one `gcFixtures` per test; a call the test awaits later is `gcGo` (`callAsync`: it has taken its line position when it returns); `await flush()` is `gcFlush` (25 ms); `harness as SessionImpl` is `*harnessImpl` (`ReadOnLine`, `AwaitTouch`); `SCHEDULER_INDEX_SIZES` is `tasks.indexSizes()`; `setTask` is the scheduler's `setTask`; the `ControlledStorage` subclass is `gcCountingStorage` over `controlledStorage`. One strengthening-free deviation: where upstream reads `status(d) === "running"` right after an `eventually` for P, the port waits for it with `eventually` (a task set running by another goroutine has no order against the test's read; D can only be pending or running there).

### Red record

The tests compile and were green on the first run against the ported production code of `fix/follow-ndur` (the production port landed before this slice), so there is no red run to record; the red commit is the test commit itself, `0702161314`. Red command shape: `~/ndur-tools/ndur-run.sh hgc -- 'go test -count=1 -timeout 120s -run "TestGroupCommit(SchedulerRegions|RecordsADecisionCopies|ReadsOnTheLine|OutcomesAndPublicationThroughTheHarness)" -v ./durable/harness/'`.

Green: the same command: 17/17 pass (13.4 s; the 300-child case is 12.4 s of it). `-race -count=3`: ok, 44.5 s.

### Open failures

None. Observation for the production owner: "reads no task record from Storage ..." takes about 12 s for 300 children (the TS case is ms); it passes with 0 Storage task reads, so it is a cost question, not a correctness one.


---
# Slice `hidle`

## Rows of N-DUR slice hidle (Pi 35dfa757 tests: harness-idle-scopes.test.ts at 6d4b7443)

### (c) Tests: test/harness-idle-scopes.test.ts (591 lines) -> durable/harness/harness_idle_scopes_test.go
- ported (all 23 cases): `TestIdleWaitersByScope` (3 cases), `TestIdleKeptContextRanges` (12: busy-again expiry, reconcile-pass expiry, idle order with clock back, retention zero, kept expiry on re-read, entry commit finds expired, entry commit cannot extend, shorter/longer/zero retention, zero-retention idle, sweep without full check),
  `TestIncrementalIdleStateEqualsAFullRecomputeAfterEveryStepOfRandomNestedWork` (200 steps, fixed PCG seed, fullIdleChecks 0), `TestWakeReadsSleepingTasksAndScansLiveOnesOnlyWhileNothingRuns` (7 table cases + running invocation).
- missing: none. designedOut: none.
- Go forms: the state is `TaskScheduler.idleState()`/`SchedulerIdleState` (scheduler_idle.go); entry-row counts are Pi's (5 and 0: no bounds probe row on this branch); no `keep` helper (Pi loads the chain in the creating commit; the earlier 1.4.0 port needed one).
- Go differences: Pi's `ControlledStorage.reopen` has no Go equivalent and closing a Harness closes its MemoryStorage, so the "chain not loaded yet" case leaves the first (never resumed) Harness unclosed, as the 060c port did; `quiet` waits for the scheduler's reconcile flags plus 2x50 ms instead of four microtask flushes; the random sequence uses math/rand/v2 PCG(42,42), not Pi's LCG, so the steps differ but the property checked is the same.
- Red record: none. The production port (scheduler_idle.go) was already on the base, so the cases were committed against ported code and passed at first run (one name clash `TestKeptContextRanges` with context_kept_range_test.go fixed by naming mine `TestIdleKeptContextRanges`).
- Run: `go test -race -count=1 -timeout 200s -run 'TestIdleWaitersByScope|TestIdleKeptContextRanges|TestIncrementalIdleState|TestWakeReadsSleeping' ./durable/harness/` -> ok (20.5s).
- Open failures: none.


---
# Slice `htail`

## Rows of slice `htail` (lane N-DUR, Pi `e5c6182b` / T `6d4b7443`): harness-tail tests, audit, benches, the store variant

Branch `fix/follow-ndur-htail` on `fix/follow-ndur` @ 19ed965483. Delete this file after transcribing. Test code only (no production file touched). No changelog fragment: nothing user-visible.

### Red record
None. The production code of the group commit was already on the base, so the ported cases compile and pass at 19ed965483 (`go vet ./durable/harness/` clean; `go test -run 'TestTaskPhasesGroupCommit|TestTaskCloseGroupCommit|TestPublicationAtAdoptionGroupCommit|TestToolRecoveryGroupCommit' ./durable/harness/` ok, `-race -count=15` ok). What is red is the OLD Go cases they supersede: `TestPublicationAtAdoption/resolves a waitForIdle() called after adoption, before the storage resolved, after host listeners` hangs (harness_tasks_test.go:1191, `hosted.wait` inside the gap, the host listeners now run after the storage call) and expects `committing=true`. NDurHarness was sent the replacement list.

### (a) Files
| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| packages/durable/test/harness-tasks.test.ts (+74) | ported (new file) | durable/harness/harness_tasks_group_commit_test.go | test:durable/harness/harness_tasks_group_commit_test.go#TestTaskPhasesGroupCommit, #TestTaskCloseGroupCommit, #TestPublicationAtAdoptionGroupCommit | see (c) |
| packages/durable/test/harness-tools-recovery.test.ts (+64) | ported (new file) | durable/harness/harness_tools_recovery_group_commit_test.go | test:durable/harness/harness_tools_recovery_group_commit_test.go#TestToolRecoveryGroupCommit | see (c) |
| packages/durable/vitest.store.config.ts, test/store-storage-node.ts | ported | durable/harness/store_variant_support_test.go (`openNodeSqliteStorage`, `openStoreStorage`), Makefile `test-durable-store` + `DURABLE_STORE_FILES`; the five opener sites (`tkOpenSqlite`, `openSqlite`, and the `openSqlite` closures of harness_conversations/lifecycle/task_graph) call it | `PIG_DURABLE_TEST_STORAGE=store` run, see (f) | mechanism: env var selects `store.Open` over `chord/next/store` on the same SQLite file (Pi: module alias); the four excluded names (testNamePattern) are skipped by the opener through `t.Name()` |
| packages/durable/test/{nested-images-bench,nested-tools-bench,scheduler-fanout-bench,tool-output-bench}.ts | designed-out | none | none | no Go counterpart exists (`grep -rn Benchmark durable/`: none mirrors them; gap-durable.md maps tool-output-bench to durable/storage/storage_bench_test.go / durabletest/storage_benchmark.go, owned by NDurStorage). The change is the `commit` wrapper counting `batches.length` and flattening batches, and a doc comment: Go's `Commit` takes batches by construction and its benchmark does not wrap it |

### (c) Tests (per upstream file: ported / missing / designedOut)
- harness-tasks.test.ts: ported (case name = upstream title unless noted):
  - "commits results with entries atomically, keeps memos until terminal, and retires task documents" (documentScans == 0, `DocumentCacheSizes().TaskDocuments == 0`; CountingStorage = `documentScanCountingStorage`) supersedes the same-named case of harness_tasks_test.go.
  - "writes no fault when close seals while the step after a failed phase is queued" and "rejects a runtime commit that was queued on the line when close sealed it" (blocker = `holdLine(harness)`: a `Harness.Commit` whose callback waits; the second admits its commit through `lineadmission.With` on `runtime.Commit`) supersede the same-named cases of harness_tasks_scheduling_test.go.
  - "resolves a waitForIdle() called while the storage stores the commit at its publication, after host listeners" (expects `committing=false`), "ends a task wait when the Harness closes while the terminal commit is stored, and still stores it" (reopens the store, task terminal), "calls a host listener added on a Harness during a publication only when one was registered at it" (`followCommits` + `during`) supersede the three old cases of harness_tasks_test.go named in the file.
  - missing: none; designedOut: none.
- harness-tools-recovery.test.ts: ported "reruns a safe tool whose running slot a crash after its intent lost, or a sibling stored, and shows it running" (two subtests, with and without sibling). missing/designedOut: none.
- Audit of the other changed files (Go test that carries each changed case; all in NDurHarness's files and already ported, none MISSING):
  - harness-blocked (commitWrites): harness_blocked_test.go. harness-cancellation-barrier (`HoldMarks` per batch, commitWrites x2): harness_cancellation_barrier_test.go (`holdMarks`, `durabletest.CommitWrites`). harness-context-range (new): context_kept_range_test.go "is not advanced when an entry was committed while its scan ran off the line". harness-context (rows 0 / 21 / 22): harness_context_ranges_test.go, context_extend_test.go. harness-deferred-progress (attempt presentation): harness_deferred_progress_upstream_test.go:289. harness-generation (persistWithinMs 100): harness_generation_test.go:220. harness-inbox (line held by a waiting callback, RecordingStorage per batch): harness_inbox_test.go:290, `inboxRecordingStorage`. harness-lifecycle (commitWrites): harness_lifecycle_test.go `seedRunningTask`. harness-live-deltas (5 commits, deferred attempt, persistWithinMs 50, per-batch kinds): harness_live_deltas_test.go. harness-nested-tools (batches, commitWrites): tool_nested_calls_doc_test.go, tool_task_v1_records_test.go. harness-observers (adopting=false, renamed titles): harness_observers_test.go. harness-ownership (3 refusal wrappers, replaced case "runs a handle operation queued behind the abort mark ..."): harness_ownership_ended_test.go, harness_ownership_tools_test.go. harness-restart-failure: the `<= 49` bound in harness_restart_failure_test.go; the commitWrites case "drops the partial past the limit..." is in durable/core/driver/harness_restart_failure_core_test.go:964 (build tag pigfault) and its `setRestarts` helper still calls the OLD `Storage.Commit(ctx, writes, func(durable.Seq){})`: `go vet -tags pigfault ./durable/core/...` fails there (reported to NDur; fix: `durabletest.CommitWrites`). harness-scheduler-indexes (per-batch counts): scheduler_indexes_test.go `commitCounter`. harness-scheduler-retention (both changed cases): scheduler_retention_test.go:121, scheduler_retention_chains_test.go:229. harness-storage-failure (an entry before the read): harness_storage_failure_test.go:614 (`sfAppendNote`). harness-structured (2 refusal wrappers): harness_structured_test.go `rejectingStorage`/`refusingAt`. harness-submissions (reopen): harness_submissions_test.go:199. harness-task-graph (adopting=false): harness_task_graph_test.go:588-610. harness-tool-hooks (commitWrites): harness_tool_hooks_test.go:1091. harness-view (+1 frame): harness_view_test.go:281.
  - carried (record only, Go on `dp/fix/060c-integrate`, not in this base): codemode.test.ts "resolves the built-in read with an image's block, which image() shows, and with a text file's text" (04414839) -> durable/codemode/output_tool_test.go:187; tools-read-differential.test.ts (`structuredOutput`, 04414839) -> durable/tools/read_differential_test.go:53,133; search.test.ts (79006f06, c7372af4: Unicode tokenize/rank, the four #11331 ranking cases) -> durable/internal/search/{search_test.go,search_carried_test.go}; tool-search.test.ts of packages/durable: unchanged between e7a0df27 and 6d4b7443 (the changed one is packages/coding-agent/test/tool-search.test.ts, 79006f06).
  - not in the assignment, noted: harness-tools.test.ts (+69, two `beforeProviderRequest` cases) is NDurHarness's (harness_tools_test.go#TestGenerationHooks per its rows); harness-group-commit/idle-scopes belong to NDurGC/NDurIdle.

### (f) Store variant (`make test-durable-store`, `PIG_DURABLE_TEST_STORAGE=store`)
Per Pi's include list (Go test functions of the mirroring files, each run with `-timeout 120s`): PASS: after-run, compaction, continue, conversations, generation-recovery, inbox, input-entries, lifecycle (one earlier failure of `TestHarnessClose/completes reads queued at the seal ...` did not reproduce in three later runs, also passed on SQLite), nested-tools-restart, nested-tools, read-only, restart-failure, scheduler-indexes, structured, submissions, task-graph, tasks-recovery, tool-hooks, tools-recovery (incl. the new case), turn-limit, view; ownership and scheduler-retention PASS with one case skipped each (below). harness-sleep has no file in `durable/harness` (its cases are in durable/core/driver/harness_sleep_core_test.go). OPEN FAILURES, identical on plain SQLite (production, NDurHarness informed): `TestBlockedTasksOnTheReference/keeps an abort mark admitted before close in the record close writes back` (harness_blocked_test.go:147 "Condition was not reached"); hangs: `TestOwnershipBelowEndedTasks/finalizes many held siblings that end together in one pass`, `TestSchedulerRetention/forgets a 20,000-deep chain of ended tasks without recursion`, `TestSchedulerRetentionBelowAForgottenConversation/aborts work created in a forgotten conversation below an abort-marked owner before it runs` (the other cases of those files pass on both storages when these are skipped with `-skip`).

---
## Round 2 (ord)

Branch `fix/follow-ndur-r2-ord` on `fix/follow-ndur` `505ba90fcb`. Findings fixed: ndur-storage.md B2, S2, S3, S4, N5, N6; ndur-harness.md B1 and the Session half of S5/M4 (the ordering mutation that passed the harness port). Also `SessionImpl.ReadOnLineCtx` for NDurHarn's `Inspect(ctx)`. Owner decision (Main): the ordering mapping is a bug, not a mode.

### Design note: ordering without goroutine dumps (supersedes the "Go mapping design note" and open question 1 of the `session` section above)

Pi guarantees that the continuations of what a publication resolved (commit(), Submission.wait(), waitForTask()) start before the next part of the same storage call is published (docs/spec.md "Two commits of one storage call are published a macrotask apart", group-commit.md 5.3). The first port kept it by reading a dump of every goroutine (`woken.go`, `internal/entered`), which cost ~0.9 ms and 100 KB per publication that woke something and gave the order up above 128 goroutines (`orderingAffordable`), i.e. in a real process.

It is now an explicit handshake in `durable/session/resumption.go` (`woken.go` is deleted); nothing reads the state of a goroutine and nothing depends on their number:
- A wait registers before it blocks and acknowledges when it resumed: `SessionImpl.Park(ctx, done) *Parked` + `(*Parked).Resume()` (call it on every exit of the wait, first thing), or `SessionImpl.Await(ctx, done)` for a plain receive. `Park` on a channel that is closed already returns nil; `Resume` accepts nil and repeats.
- `endPublication` runs the wakes (`DeferUntilPublished`), then waits for the acknowledgement of every registered wait whose channel they closed (`awaitReadied`; the wake side, e.g. `Waiters.Settle`, is unchanged). The delivery goroutine waits for the acknowledgement of each commit caller it releases (`delivery.resumption`, acknowledged in `runFenced` right after the caller's `<-released`, and on the panic path); the writer's `Hop` waits for that goroutine to be idle.
- What the acknowledgement says is "started": the goroutine returned from its wait. The Session cannot know when code that is not its own reaches its next wait (an ordinary channel receive or lock), and waiting for it would stall every publication behind a goroutine that went on to a provider call, so it never does. A caller that needs its continuation ordered whole, up to its next wait of the Session, passes `session.WithContinuation(ctx)` to its calls and calls `Continuation.Entered()` when done; its waits' acknowledgements then come from it, and also from its next wait of the Session with that context (a commit waiting for the line or for its outcome, `AwaitTouch`'s gate, `Park`), as a continuation that goes on to wait has entered. Nothing in production code needs this; it is what the ordering tests use to be deterministic.
- Limits, stated where they are: (1) a goroutine a wake readies that is not in a registered wait, such as one selecting on the cancelled context of a task, is not waited for (cancel returned; the reaction is its own). NDurHarn's waits (`Waiter.Wait`, task waits, idle waits) must register to be ordered: `parked := session.Park(ctx, done); defer parked.Resume()`. (2) `Hop` still calls `chord.DeliveryScheduler.AwaitEntered` for watch deliveries pending, which reads goroutine dumps (`chord/watch_impl.go`, not in this slice): it costs only while a delivery is pending, and removing it needs a decision in chord, because it counts a listener blocked on a lock as entered (`entered.AwaitBlocked`), which a handshake cannot see.

Stale text of the `session` section corrected here: "`Part.Published` (touch of changed documents, unpin of taken holds) runs after the keys left" is wrong since 33008a8701: `refreshChanged` (the touch of changed documents) runs inside `adopt`, before the keys leave (`session.go adopt`, deliberately: no sweep runs between the keys' removal and it); `Part.Published` only unpins the taken holds (`finishRelease`) and schedules the eviction sweep, after the keys left. "`Part.Published`/hop use two goroutine dumps per publication that deferred wakes" no longer applies. The `session` PORT_MAP row `durable/session/{session,guard,held,retention,woken}.go` becomes `{session,guard,held,retention,resumption}.go` (no upstream file for resumption.go: Go mapping).

### Measured (xd670, tern-ci:v6, Go 1.27.3, 8 cpus, `-benchtime=300x`, -count 3 and 5)
| benchmark | before (`505ba90fcb`) | after |
|---|---|---|
| `BenchmarkSessionPublish` parked=0 (commit + one deferred wake) | 0.76-0.95 ms/op, 100.7-102.1 KB/op, 76 allocs | 25-36 µs/op, 4.28-4.31 KB/op, 65 allocs |
| ... parked=100 | 1.85-1.95 ms/op, 84.6-86.7 KB/op | 24-26 µs/op, 4.22-4.29 KB/op |
| ... parked=1000 | 23-31 µs/op, 4.0-4.1 KB/op (ordering given up) | 19-23 µs/op, 4.24-4.28 KB/op (ordered) |
| `BenchmarkSessionCommit` parked=0 / 100 / 1000 | 0.40-0.45 ms / 0.56-0.65 ms / 18-24 µs | 16-21 µs / 21-40 µs / 17-26 µs; 3.89 KB/op, 58 allocs |
| `BenchmarkOneTaskRun` (durable/harness, NDurHarn's file, one task created and awaited) | 6.25-6.88 ms/op, 509-515 KB/op, 1068-1070 allocs | 0.617-0.690 ms/op, 79.0-79.3 KB/op, 1030-1033 allocs (same on NDurHarn's `adb2ce0b33`, 0.60-0.68 ms) |
The review's 5.3-7.7 ms for a one-task run was the dumps; next's 0.3-0.6 ms is what remains. The idle-list copy per settle (harness S3) is NDurHarn's change and is not exercised by `BenchmarkOneTaskRun` (no kept contexts), so it is not measured here.

### Tests (all in `durable/session`, `-race`)
- Ordering is checked with a held continuation, so it fails deterministically when the order is lost: the caller (or woken wait) whose continuation Pi runs before the next publication is slow to start (`order.hold`, `startOne`, `start`), and the test waits (`awaitHop`, `awaitWakeHop`: `AwaitingCallers`/`AwaitingWakes`, counters of what the Session waits for now) until either the Session waits for it, or the next part got published without waiting, then releases it. `spawn` gives each call a `Continuation` (`spawnContinuing` for a continuation that says itself when it entered).
- Mutations (xd670, `-count=3`, the whole package): `awaitResumed` returns at once (all handshake off): FAIL 3/3 `TestGroupCommitBatching`, `TestGroupCommitOutcomes`, `TestGroupCommitPublication`, `TestGroupCommitAFailureBetweenTwoPublications` (both cases), `TestAWakeADeferredPublicationRunsIsEnteredBeforeTheNextPartIsPublished`, `TestOrderingHoldsAboveAHundredAndTwentyEightGoroutines`. Only the deliveries' wait removed: the same minus the wake case, 3/3. Only `awaitReadied` removed: the wake case and the 300-goroutine run, 3/3. First line of the full-off run: `session_group_commit_test.go:897: the part after the failure got <nil>, want SessionFailed caused by read failed between publications`.
- Above 128 goroutines: `TestOrderingHoldsAboveAHundredAndTwentyEightGoroutines` parks 300 goroutines, checks `runtime.NumGoroutine() > 128`, and reruns the ordering tests (Batching, Outcomes, Publication, the storage's scope, the failure between publications, the wake, the gate and line continuations).
- Handshake mechanics: `TestParkOnAChannelThatIsClosedRegistersNothing`, `TestAWaitDeregistersWhenItResumesHoweverItReturned` (its wake and a ctx abandonment: no wait left registered), `TestAContinuationThatWaitsForAGateHasEntered` and `...ForTheLineHasEntered` (a continuation that goes on to wait in the Session releases the hop; otherwise the publication it waits for never comes), `TestAPanickingCallbackDoesNotHoldTheOutcomesAfterIt` (without the ack on the panic path: `a commit after one whose callback panicked never got its outcome`, after 10 s).
- ndur-storage B2: `before/failing/after.enter()` (`entered.Await`, returns at any park) became `awaitQueued(kernel, 3)` (the writer's queued parts, as the external tests). At `505ba90fcb`, `--cpus 2 -race -count=300 -run TestGroupCommit`: 6 failures `session_group_commit_internal_test.go:220: notes [1 1 1 0], want [1 1 1 1]` plus 1 of `TestGroupCommitTheStoragesPublicationScope`. After: the same run over `TestGroupCommit|TestRandomIsPisGenerator|TestAWake|TestAContinuation|TestOrderingHolds|TestParkOn|TestAWaitDereg`: `ok`, 0 failures in 300 (590 s).
- S3: `random` is Pi's float64 generator (`math.Mod(float64(state*1103515245)+12345, 2147483648)`; the conversion keeps a fused multiply-add from changing it on arm64), one shared stream (mutex) for plans, `refuseAt` and the storage latency, as Pi's `next`. `TestRandomIsPisGenerator` pins six draws for seeds 1, 42, 99991 taken from running Pi's function in node; with exact integer arithmetic: `seed 42 draw 1 = 0.5198187828063965, want 0.5198186635971069`. What differs from Pi is only the interleaving of the writer's latency draws with the test's, which is the scheduler's here and the event loop's there; every assertion is an invariant of any interleaving.
- S4: the always-false `!errors.Is(err, err)` is gone (`Close` of the failed Session must return nil, as Pi awaits `session.close`); the `alone` case is Pi's (`TestGroupCommitSettlesADeferredCommitWhoseAdoptionFailsAlone`: one conversation with its Note, then only the deferred commit, `session.failure`, `closed == failed`, zero `lineSizes`); the identity of the failure is pinned (`==`) across what the commit rejects with, the cause of `SessionFailed` for the part after it, `Failure()` and `End().Error`. Go cannot inject its own error into an adoption without a production seam, so the failure is the adoption's own (`Prepared change has been aborted`); with the failure wrapped in `failFrom`: `session failure wrapped: Prepared change has been aborted, want Prepared change has been aborted itself`.
- N5: `keys.go` no longer says a touch returns the context's cancellation; the rows text is corrected above. N6: `TestCachedStorageKeepsWrittenRecordItselfShared` asserts what a value type can of Pi's `toBe(record)` (the checkpoint pointer and the memos map are the write's own; the record is served with its fields) and says why; `Published`' comment says the struct is a copy and what it references is shared. With `Published` copying the memos: `the written record's memos were copied`.
- Other test changes: `openGC`/`must` take `testing.TB` (the benchmarks use them); `session_group_commit_internal_test.go` no longer imports `internal/entered`.

### New public Go API (`durable/session`)
`SessionImpl.Park`, `SessionImpl.Await`, `Parked`/`Parked.Resume`, `WithContinuation`, `Continuation`/`Continuation.Entered`, `SessionImpl.ReadOnLineCtx`, `SessionImpl.ReadWithTicketCtx`. `runFenced`/`enqueue` take a `lineCall` (line admission + continuation) where they took `admitted func()`.

### Remaining / for the integrator
- NDurHarn registers its waits (item (1) above); until then a harness wait is not waited for by the hop. On this branch alone `go test -race -count=1 ./durable/harness/` (330 s) is green except twelve Pi-oracle tests (`TestResolveAgentMatchesPi`, `TestCompactionHelpersMatchPi`, ... `TestConversationViewsMatchPi`; `Pi oracle: exit status 1`), which need the `.upstream/current` oracle mirror that the container tree does not have, as in the review.
- The watch-delivery half of the hop (item (2)) is chord's.

## Round 2 (stor)
- B1: `durable/storage/testdata/memory_oracle.mjs` commits through Pi's `commitWrites` (`src/testing/commit-writes.ts`), as Go's `durabletest.CommitWrites`. Other storage oracle drivers (`sqlite/testdata/schema_oracle.mjs`) do not commit; the jsonl recovery oracle test uses the same driver. Run against a build of Pi 2c8d1feb (`PI_UPSTREAM_REF=2c8d1feb147f6b3d97e855da803bfe7b19f3c403 pin.py install-oracle`, `.upstream/current` -> the build's `src`, uncommitted): `TestJsonlStorageMatchesPiOnSeededPrograms`, `TestMemoryStorageMatchesPiOnSeededPrograms`, `TestSqliteStorageMatchesPiOnSeededPrograms`, `TestJsonlRecoveryMatchesPiOnDamagedDirectories`, `TestSqliteSchemaMatchesPiForEveryPrefix`, `TestSessionTransactionsMatchPiOnSeededPrograms` all PASS (-v). The rows text "migrated mechanically and unverified" is superseded.
- S1: `JsonlStorage.Close` takes `commitMu` (waits for a commit in progress, as memory's Close does), so a commit never meets the closed memory between batches. Test `TestJsonlStorageBatches/Close waits for a commit...` fails without the lock (`Close returned <nil> while a commit was being appended`).
- S6: the read-during-append tests start the read through `entered.Go`, await its park, assert it has not returned, then open the gate; results go through channels. Removing the read gate (`readable` returning the store at once) fails `TestJsonlStorageBatches` and `TestJsonlStoragePoisonsWhenAnAppendFailsAfterItsBatchesApplied`.
- N1: testprune cite `storage.ts:324-325,346`; stale `(:827)` comment fixed. N4: id minted before the goroutine. N3 not done (needs a raw-text json column in chord's store). S5 deferred (integrator).

## Round 2 (harn)

Lane N-DUR round 2, slice `harn` (`durable/harness/**`), branch `fix/follow-ndur-r2-harn` off `fix/follow-ndur` @ 505ba90fcb, with `fix/follow-ndur-r2-ord` merged (ReadOnLineCtx 7db64f5690, handshake 909a29ad20). Findings of `reviews/ndur-harness.md`. This section replaces the stale text of the sections above for these findings (the earlier sections are not edited).

| finding | disposition | evidence |
|---|---|---|
| B3 `TestHarnessClose/rejects ... inspect queued before reports closing` flake | fixed: the queued-read tests wait for the line's admission callback (`queueOnLineAdmitted`, `lineadmission`) with the operation's own ctx, not for the net `LineJobs()` gauge; `queueOnLine` is deleted. `Inspect`, `Get/Status/Wait`, task reads, view and task graph attachments pass their ctx to `Session.ReadOnLineCtx` so they report admission. | `TestHarnessClose`, `TestTaskGraph...`, `TestHarnessView...`, `task_runtime_get_task_test.go`; -race -count=100 --cpus 2 green (below) |
| B3 `completes reads queued at the seal ...` ("did not reproduce in three later runs") | the "accepted flake" text of (f) is withdrawn: its eight reads now queue by admission (same helper), so the gauge weakness that made it flake is gone. | same run |
| S1 bound `Submit` job-start cancellation | fixed: `boundConversation.Submit` runs the commit detached from the invocation's cancellation and checks the signal as the job starts (`Submissions.SubmitUnlessCancelled`, `startCheck`), so a submit queued behind a line job whose invocation was aborted before it starts is rejected and stores nothing (Pi harness.ts:557-563, session.ts:1086). | red `TestOwnedConversationsFromTools/rejects_a_handle_submit_whose_invocation_was_aborted_while_it_was_queued_behind_a_line_job,_and_stores_nothing` (`harness_ownership_tools_test.go:378` "was admitted as 20, want it rejected") with the production change reverted; green with it |
| S2 timed pauses | fixed: `gcFlush(t)` is `awaitQuiescent` (every goroutine but the caller parked, from a goroutine dump taken while the world is stopped: the Go reading of `await flush()`; no duration), `quiet()` polls `sweepScheduled`/`wakeScheduled`/`draining`/`reconciles` then quiesces, the two `time.Sleep(100ms)` before `Wake()` are `awaitSchedulerSettled`, the idle-scope negatives (`150ms` windows) check after quiescence, the migration_failed count uses `eventually` then a quiescent exact check, `holdLine` fails the test and rejects `entered` (N2). Other ports still use `flush()` (1 ms) in files outside this finding. | `harness_group_commit_test.go`, `harness_idle_scopes_test.go`, `quiescence_support_test.go` |
| S3 expiry allocates the whole idle order | fixed: `expireContextsLocked` takes `orderedSet.First()` (added, amortized O(1)) and stops at the first retained context. | `TestExpiringIdleContextsAllocatesNothingWhenTheOldestIsRetained` 0 B/op at 100/1000/4000; with the `Values()` loop it is 896/8192/32768 B/op (mutation) |
| S4 safe rerun argument order | fixed: `ToolTaskCheckpoint.UnmarshalJSON` keeps the arguments' text; the rerun shows `argumentsText()` (member order of the stored intent). The order is not observable through a delta `Set` that finds an equal value, so the test is at the decode (`TestAToolCheckpointKeepsItsArgumentsInTheirStoredOrderWhenDecoded`), red when the decode keeps no text (mutation). | `tool_checkpoint_order_test.go` |
| S5 wait-continuation port not discriminating | pending (not achievable at the harness): with the handshake, the Session waits for a wait's goroutine to have *started* (Park/Resume), and for a whole continuation only through `WithContinuation` on a ctx that a Session call carries; the continuation of Pi's `.then` here is test code after `WaitForTask` returned, which the Session cannot wait for (the tail of `WaitForTask` already counts as entered). A hold in the test continuation blocked the publication in the unmutated Session too (through the read job's resumption). The port is Pi's again and passes with `awaitReadied` removed; a 1-processor variant also passed under the mutation. The loss of the ordering is caught by the Session's own tests (`session_resumption_test.go`, `TestGroupCommitAFailureBetweenTwoPublications`). Owner decision: accept, or add a hook to the Session's wait. All harness waits (`Waiters.Add`, task/idle/submission waits) register with `Session.Park` before they block and resume as the first thing after (`Waiter.Wait`, `Abandon` for a registration whose line job failed after). | `util.go`, `scheduler.go`, `submissions.go` |
| M1 `regionLocked` ignoring gates hangs | fixed: the opposite-waits case fails on the missing refusal (`eventually`) instead of waiting on the deadlocked task; with the mutation `TestGroupCommitSchedulerRegions` fails in 0.9 s (was a 5 min timeout). | mutation run |
| M3 `TestIdleWaitersByScope` blind to `addScopesAboveLocked` | fixed: new case "resolve a waiter at the publication of the change that ended the last task below, before the pass after it" (a second held line job, admitted behind the task's end, keeps the reconcile pass away; only the publication's scope check resolves the waiter). Fails with `addScopesAboveLocked` doing nothing. | mutation run, `harness_idle_scopes_test.go:475` |
| S6 store variant | rows (f) "OPEN FAILURES" are stale: `make test-durable-store` passes at the lane head (review: 66 s). Not wired into CI: no durable-specific Go test job or target list exists in `.github/workflows/ci.yml` or `automation/ci` (the grouped shards run `go test ./...`); adding a job is a CI design decision, pending. The harness-sleep cases of `durable/core/driver/harness_sleep_core_test.go` open SQLite directly, so they never run on StoreStorage: designed (pending a StoreStorage open in the driver test). | `Makefile` `test-durable-store` |
| S7 | not this slice (ai package hooks). Core `beforeProviderRequest`: left (Pi's core has none). The (f) "core/driver beforeProviderRequest gap" is not a gap. | review S7 |
| N1 | fixed: `types.go` says the hook runs once per request, before pi-ai's retry. | `durable/harness/types.go` |
| N2 | fixed (S2). | |
| N3 | this section. | |

Measurement (one-task run, `BenchmarkOneTaskRun`, `durable/harness/bench_one_task_test.go`): see the final report; NDurOrd reruns it after its handshake.

Environment note: the Pi oracle tests (`*MatchPi`, `*MatchesPi`, `TestTaskGraphViewMatchesPiOnSeededPrograms`) fail on xd670's `.upstream/current` copy (the Pi sources are missing: `ERR_MODULE_NOT_FOUND` for `.../storage/memory.ts`); every other test of `go test -race ./durable/harness/` passes.

## Round 2 status (integrator's handoff)

Review findings (handoffs/next/reviews/ndur-storage.md, ndur-harness.md) fixed on this branch: storage B1/B2/S1/S3/S4/S6/N1/N4/N5/N6, harness B1 (ordering: `woken.go` replaced by the explicit park handshake `resumption.go`, `Park`/`Await`/`WithContinuation`, no dumps, no goroutine bound; `TestOrderingHoldsAboveAHundredAndTwentyEightGoroutines`; mutation: handshake off fails 6 tests 3/3), B3, S1-S4, S6, N1-N3. Measured: `BenchmarkOneTaskRun` 6.25-6.88 ms/509 KB -> 0.62-0.69 ms/79 KB; `BenchmarkSessionPublish` 0.76-0.95 ms -> 25-36 us.
Not done / decisions left: (1) harness S5: the Pi port of "runs a wait continuation of one commit before the next..." still passes with the handshake wait removed (the Session waits for the continuation to have started, not for the test code after `WaitForTask` returns); ordering loss is caught by the Session tests (`session_resumption_test.go`, `TestGroupCommitAFailureBetweenTwoPublications`): accept or add a Session wait hook. (2) `SessionImpl.Hop` still calls `chord.DeliveryScheduler.AwaitEntered` (dump-based, chord's file) while watch deliveries are pending. (3) storage S5 (KFold duplicate of CommitSeries), N3 (double marshal), harness S7 (ai-package hook gaps), core `beforeProviderRequest`: deferred as instructed. (4) Merge of `next` (pin 2c8d1feb) NOT done yet: wait for Main's signal (keep next's `chord/next/store/store.go`, this lane's `scheduler.go`, delete next's `scheduler_idle_scopes_test.go`, `harness_tasks_test.go:1172` -> `resolveIdleWaiters()`, drop duplicate series tests/changelog line). (5) The *MatchPi oracle tests need `.upstream/current` = Pi 2c8d1feb; they ran green in the storage slice against the 2c8d1feb oracle build.

## Designed-out re-review (pin 2c8d1feb)

Re-reviewed at Pi `b556cfe5` (the pin of `fix/follow-pi-next` 49e182e429; `2c8d1feb` is the pin the 33 stale citations were found at). `git diff 2c8d1feb b556cfe5 -- packages/durable` changes only the pi-schema switch (Pi 6536f801, be039fb3, 6291b9bc: `harness/{define,tool,types}.ts`, `codemode/{codemode,models}.ts`, `tool-search/tool-search.ts`, `tools/{bash,edit,read,write}.ts`, tests): no durable design-out depends on those files, and the port of the switch is the SchemaSwitch durable lane's (`fix/follow-schema-switch-durable`), not this one. `packages/chord/src/next/store` changes (archive, columns, expression, json-column, table-evolve) are the same switch; the Storage, Session and scheduler files, `storage/indexeddb.ts` and `docs/` of durable are unchanged between `2c8d1feb` and `b556cfe5`.

| row(s) (reviewed-decisions.json id) | holds at b556cfe5 | evidence |
|---|---|---|
| `pkg:durable/storage/indexeddb#IndexedDbStorage` and its 33 member/call rows, `#IndexedDbStorageOptions` (35 rows, designed-out: browser-only Storage over IndexedDB) | yes | `packages/durable/src/storage/indexeddb.ts` is untouched by `git diff 2c8d1feb b556cfe5`; it still opens with `/// <reference lib="dom" />` (:1) and `export class IndexedDbStorage implements Storage` (:408), `commit(` at :456 takes Storage's `(batches, receive)` as at 6d4b7443; PiG has no browser/WebAssembly build, so no Go caller can open it. The rationale text is re-dated to `Pi 1.4.0+b556cfe5`. |
| `pkg:agent/.#AgentTool::property:replay` (designed-out: declared, read by nothing in `agent`) | yes, lines moved | `agent/src/types.ts:502` declares `replay?`; the only readers are `durable/src/harness/tool.ts:196,208,431` of `ToolRegistration.replay` (`durable/src/harness/types.ts:354`), a type that does not extend AgentTool. The text cites those lines now (they were `:490`, `:179,189,325`, `:312`). |

The `reviewed-decisions.json` text is the source; `test/parity/interfaces/mapping-v1.4.0+b556cfe5.json` is generated from it (integrator: `make ledger-update`/`make generate`), after which `TestDesignedOutRowsCiteThePinAndC5OnlyForModelsError` has no durable row left to fail on.

`durable/contract/PIN` (`main e7a0df27`), `durable/core/session/hopcost/testdata` (`ticks.json`) and `durable/core/host/cf/test/ticks.test.ts`: the Durable core's conformance reference, kept at e7a0df27 on purpose. Pi's group commit (e5c6182b, in 2c8d1feb and later) changed when a line job's callers resume and when outcomes are delivered (a job starts before the previous one's outcome is delivered; one storage call publishes its commits a macrotask apart), so the core's tick tables and the corpus captured at e7a0df27 describe the previous Session. Moving the reference needs the corpus recaptured at the new Pi and the core's loop (`durable/core/session/loop.go`, the `sched_*` files) ported to group commit; until then `TestSchedulerMatchesPiDurable` (`basic.json` event 18, `cascade.json` event 28: Pi runs task 6's `settleOutcome` before task 5's `report`, and logs a commit before the idle waiter resolves; the core does the reverse) is red against the live Pi at 2c8d1feb and later. `TestScenariosMatchPiDurable` was red only because Pi's `Storage.commit` changed shape (`{stored}` and a list of batches); its oracle drivers (`durable/core/session/testdata/{oracle,sched_oracle}.mjs`) now record one entry per stored batch, as `durable/codemode/testdata/codemode_restart_oracle.mjs` does, and it passes.

## Round 2 (fin): merge of next, chord hop handshake, S5

- Merged `fix/follow-pi-next` (5bae8d553f, then 49e182e429 at pin b556cfe5): `chord/next/store/store.go` is next's (KFold `CommitSeries`), `chord/next/store/store_series_test.go` and `durable/harness/scheduler_idle_scopes_test.go` are dropped (next's `store_test.go` "a commit series" cases and our `harness_idle_scopes_test.go` carry the evidence; the rows above point there), `durable/harness/scheduler.go` is ours, `testprune-reviewed.toml` is the union, `orderedSet.First` is the one method of `scheduler_index.go`.
- `chord.DeliveryScheduler.AwaitEntered` (`chord/watch_impl.go`): the delivery goroutine closes `entered` as it starts; the wait is one channel receive per scheduled delivery, no `runtime.Stack`, no goroutine-count bound. The guarantee is the delivery started (as `Session.Park`/`Resume` guarantees a wait's goroutine started); a listener blocked on a lock the caller holds is entered. Tests: `chord/watch_enter_test.go` (300 parked goroutines and one processor: awaitEntered finds every delivery started; a mutation that skips the wait fails it), `chord/next/state` and `chord/next/host` tests that used the old "parked in its first wait" guarantee have the listener say it is busy. `TestHostReloadAnAbortEndsTheReportsWaitForAListenerStillRunning` became `TestHostReloadDoesNotWaitForAListenerStillRunning` (a Go listener's waits are not known to the scheduler, so the report does not wait for it to reach one).
- S5 (`TestGroupCommitOutcomesAndPublicationThroughTheHarness/runs a wait continuation ...`): fails with `awaitReadied` removed (`the end of two was published before the continuation of the wait for one finished`), passes 30/30 with it. It found a defect of the handshake: `Parked.Resume` deregistered a wait before its continuation-held acknowledgement, so a publication that scanned after the goroutine resumed did not find it. A wait now stays registered until it is acknowledged (`TestAWaitOfAContinuationStaysRegisteredUntilItEnters`; fails with the old `Resume`). This supersedes the S5 row of "Round 2 (harn)": the harness port is discriminating through `session.WithContinuation`.
- Nested calls: `NestedCalls.Execute` hands the Session line to calls in call order (a turn released at admission, or when the creation commit settled, never at the end of the call's wait); the first version released it at return and deadlocked `TestCoreAbandonedNestedCallsAfterARestartSettleInterruptedOrAbandoned`.
- Oracles at the new Pi: `durable/codemode/testdata/codemode_restart_oracle.mjs` (Storage.commit(batches); schema with pi-schema, Pi dropped `Type`), `durable/core/session/testdata/{oracle,sched_oracle}.mjs` (one record per stored batch, a tick after `receive`).
- More oracle drivers at b556cfe5: `durable/interop/{app,bench}.mjs` give tools plain JSON Schema parameters (Pi dropped `Type`; the durable oracle's closure nests pi-schema, so the interop app cannot import it), `durable/harness/testdata/taskgraph_oracle.mjs` builds `new TaskGraphView(session, blocking)` (Pi dropped the storage argument). `durable/harness/wait_deliveries_test.go` is rewritten to the start contract (a spinning listener does not hold a wait; a cancelled context ends it).
- Not green at the gates, not this lane's: `durable/core/session` `TestSchedulerMatchesPiDurable` basic.json event 18 and cascade.json event 28 (the core's loop does not model group commit's job/outcome overlap; see "Designed-out re-review"), `chord/host` `TestPackageBoundaryDoesNotDependOnPiPackagesOrFilesOutsideChord` (`internal/linkerexec`, from next's plugins lane), `chord/nextnode/plugins` `TestNodeVendoredFilesAreExactlyTheManifests` (`make vendor-chord-plugins-node` after the pin moved), `make generate` stops at known-gaps (`compat-schema.test.ts` release policy, SchemaSwitch). `make test-expectation-lint` with `TEST_EXPECT_BASE=65411f70dd` reports four commits of next (fffc16ae0a, caa5070223, e27a975dc1, e4f8e35773); with the merge base against next (`5bae8d553f`) it is clean.

## Round 3

- B1 (`reviews/ndur-round3.md`): the start handshake of fin orders no listener code and is withdrawn. `chord.DeliveryScheduler.AwaitEntered` returns once every earlier delivery is entered: its first listener call returned (Pi: `#drain` runs the listener's synchronous part in a microtask before the code that awaited a commit resumes, watch.ts:207-220, spec.md:1891-1894), or the listener went on to a wait the host knows (`chord.DeliveryEntered`, called by `Session.Park`/`Await`, the gate and line waits, and the host loop's `parking.Park`; `internal/watchentered` maps the delivery's goroutine), or the delivery ended. A call from inside a delivery marks that delivery entered and skips it, so listeners that wait for the Harness or call `Reload` do not wait for each other. No stacks and no goroutine bound; the cost is one map lookup by goroutine ID. The gap, stated as Go-only: a listener blocked on a plain lock or channel the host cannot see has not entered until it returns, so a wait whose caller holds that lock does not return (upstream has no locks). No divergence record is needed for the order; the gap is a constraint of the API (a listener's blocking goes through the host's waits). Tests restore Pi's order and fail with the old handshake: `chord/watch_enter_test.go`, `wait_deliveries_test.go` (a listener at work holds the wait; cancellation ends it), `TestGroupCommitTheHopWaitsForTheSynchronousPartOfAListener` ([0 0 0 0] vs [0 0 1 2]), `TestHostReloadAListenerThatAbortsTheContextFromItsCallbackEndsTheReportAborted` and `TestHostReloadAnAbortEndsTheReportsWaitForAListenerStillRunning`; with `DeliveryEntered` a no-op `TestHostReloadAWatchListenerMayCallReloadItself` and the parked-listener tests fail.
- S1: the contracts of `AwaitEntered`, `AwaitDeliveriesEntered`, `Hop`, `Submissions.Wait`, `WaitForIdle`, the `reloadReport` comment and the ndur-fin changelog fragment are rewritten; the KFold row (`kfold.md:691`) is true again (both tests exist).
- S3: `Parked.Abandon` acknowledges a wait that was rejected or cancelled (`Waiter.Wait` calls it when `err != nil`): `TestACancelledWaitOfAContinuationDoesNotHoldLaterPublications` (fails with `Resume`), `TestAnAbandonedWaitOfAContinuationIsAcknowledgedAtOnce`.
- N1: the two diff3 marker lines are gone from this file. N3 (storage): `chordstore.JsonText{Text, Value}` is a `json(...)` cell value whose text the caller has; `StoreStorage` passes the record's text and its parsed value, so the store no longer serializes the tree again (`TestEncodeCellStoresTheTextOfAJsonText...`).
- S7 (harness, ai package): closed by next's 920dfb6831 (merged here): `providerEventModel` returns the selected model (api set) or a synthesized one with the module's api; OpenAI completions, Responses (Azure, Codex through `p.cfg.Codex` at `openai_responses.go:1608`) and Google call `OnPayload` with it, after the client's key, base URL and Codex account id as Pi builds them (openai-completions.ts:373, openai-responses.ts:211, google-generative-ai.ts:96, openai-codex-responses.ts:283); tests `ai/provider_payload_hook_test.go`, `coding/provider_payload_hook_model_test.go`, `openai_codex_request_test.go`.
- Carried, not this lane's: `durable/core/session` `TestSchedulerMatchesPiDurable` (core group commit), and the storage/harness items of "not wired into CI" (S6/N4).
