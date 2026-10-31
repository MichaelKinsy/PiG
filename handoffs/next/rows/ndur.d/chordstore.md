# Rows of slice `chordstore` (lane N-DUR, Pi `e5c6182b` / T `6d4b7443`) for the integrator

Branch `fix/follow-ndur-chordstore` on `fix/follow-ndur` `9f56026a3a`. Delete this file after transcribing. Two phases: (1) the Chord store's `CommitSeries` (replaces `CommitInBatch`), (2) the new Go package `durable/storage/store` (`StoreStorage`, the D-store files that are not on next yet).

## Red records

- Phase 1, red commit `be4bafbdcb` (`test(chord/store): cover CommitSeries (red)`): `go vet ./chord/next/store/` -> `vet: chord/next/store/store_series_test.go:17:39: undefined: SeriesCommit`. Green: `go vet ./chord/next/store/ && go test -count=1 -race ./chord/next/store/` -> `ok ... 29.8s`.
- Phase 2, red commit `68bd891721` (`test(durable/storage/store): cover StoreStorage (red)`), run on the merge of `fix/follow-ndur-storage` @875c5c04b3 (`4f917e2e91`, so the batch `Storage` shape compiles) with the package's production files absent: `go vet ./durable/storage/store/` -> `vet: durable/storage/store/support_test.go:35:3: undefined: StoreStorage`. Green: `go vet ./durable/storage/store/ ./chord/next/...` clean; `go test -count=1 -race ./durable/storage/store/ ./chord/next/store/` -> both `ok` (17.6s, 29.2s); `go test -count=1 -run TestReachLibrariesPortPublishedEntryPoints ./test/parity/interface-closure/autobind/` -> `ok`.

## (a) Upstream files

| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| packages/chord/src/next/store/store.ts (e5c6182b part: `commitSeries`, `SeriesCommit`, `SeriesResult`, `#series`, `#writeSeries`, `#checkCommit`, `#writeCommit`, `#closeSeries`, `writeMeta`, `watchCalls`, `#write` split) | ported | chord/next/store/store.go | test:chord/next/store/store_series_test.go#TestCommitSeries | `CommitInBatch` removed (no other caller existed in the tree); `Commit` and `CommitSeries` share `write` and `writeMeta`; `watchCalls` is the `watchedCalls` executor (a failed call marks the check a backend failure, not a refusal) |
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

## (b) PORT_MAP rows to add / replace

| source | PiG | status |
|---|---|---|
| `packages/chord/src/next/store/store.ts` | `chord/next/store/store.go` (`CommitSeries` replaces `CommitInBatch`; `announceTable`/`tableVersion` belong to 4b535d57, pending) | 🟡 |
| `packages/durable/src/storage/store/{commit,documents,queries,storage,tables}.ts` | `durable/storage/store/{commit,documents,queries,storage,tables}.go` | ✅ |
| `packages/durable/src/storage/store/index.ts` | `durable/storage/store/index.go` (package doc) | ✅ |

## (c) Tests

- `packages/chord/test/next/store/store.test.ts` ("a commit series", 9 `it`, the `it.each` as 3 subtests): all ported to `chord/next/store/store_series_test.go` (same inputs and expected results; Go has no microtasks: "runs a commit queued behind a series after the series' receipts" queues both calls through `startQueued`, which takes the queue place at the call). missing: none. designedOut: none.
- `packages/durable/test/store-storage-commit.test.ts` -> `durable/storage/store/store_storage_commit_test.go`: ported: one transaction per call (2), refusals (the first, staging, the malformed pair against Memory, the two later-state cases, the copy reading earlier batches), failures (backend failure, failed COMMIT, the 3-batch statement-by-statement matrix with statement counts 29 / 23, closed storage / stopped store, queued call released), durability, other users (3). missing (pending): "publishes each commit of a call in a host batch of its own, after the call's one transaction" (needs `SessionImpl` of NDurSession); designedOut: "refuses a batch whose rows staging cannot encode, keeping the batches before it" (an `EntryId` is an `int64`: Go cannot hold the non-integer id 1.5 the case sends; the encode failure path itself is `table.Encode`'s error in `stage`), "GC / keeps nothing of a settled call" (WeakRef/gc).
- `packages/durable/test/store-storage.test.ts` -> `durable/storage/store/store_storage_test.go`: ported: conformance (memory and across reopen), claims and close (2), the store's semantics (2), archives (2; Chord's own document in the same store is created with `chordstore.CreateDocument` for owner `app.memo` instead of a host object), head markers, reads, commits (the publishing case at storage level without a Session, the four-statement guard), review regression. missing (pending): "publishes each commit of a Session inside the attached host's batch" (SessionImpl), "a derive over the conversation view and the task graph" and "a deferred Durable commit, then an immediate one" (Harness over a host: NDurHarness/NDurSession). designedOut: "GC / a closed StoreStorage is collected while its store stays open".
- Harness test files over `StoreStorage` (`vitest.store.config.ts`): pending, see open point 2.

## (d) Async contracts

| file | contract | evidence |
|---|---|---|
| chord/next/store/store.go `CommitSeries` | blocks until its turn in the commit queue, then one backend transaction; per commit: check on a step at the next seq (a failed backend call of the check rolls the series back and returns that error, store goes on; any other check error is the refusal: the series stops, prefix commits), writes (an error rolls back, store goes on); a failed BEGIN/COMMIT/ROLLBACK stops the store `storage_failed`; after COMMIT memory (lastSeq, allocator value, catalog copy only if a commit changed a table), then `receive` per stored commit in order before the next queue entry; a failing receive stops the store; `ctx` counts the step (no abort once queued) | test:chord/next/store/store_series_test.go#TestCommitSeries |
| durable/storage/store/storage.go `Commit` | same through `CommitSeries`; `Close` refuses new calls, waits for commits underway, releases the claims, leaves the store open; reads are guarded reads of the store, not waited for by Close | test:durable/storage/store/store_storage_commit_test.go#TestStoreStorageFailures |
| durable/storage/store/storage.go `Publishing` | runs `publish` once in the attached host's `batch.Within(...)`; plainly without a host | test:durable/storage/store/store_storage_test.go#TestStoreStorageCommits |

## (e) New public Go API

- `chord/next/store`: `SeriesCommit{Check func(*Step) error; Writes []StagedWrite}`, `SeriesResult{Stored int; Refused error}`, `(*ChordStore).CommitSeries(commits, receive func(index int, seq int64, ids []int64) error, ctx) (SeriesResult, error)`. Removed: `(*ChordStore).CommitInBatch`.
- `durable/storage/store`: `StoreStorage`, `Open(ctx, store, owner)`, `DurableCommit`, `NewDurableCommit`, `CommitTables`, `RecordType`, `TableQueries`, `StorageQueries`, `Condition`, `RecordRow`, the tables `RecordIds`/`Conversations`/`Entries`/`EntryHeads`/`Tasks`/`Submissions`, row builders (`ConversationRow`, `EntryRow`, `TaskRow`, `SubmissionRow`), `EncodeIndexed`, `ScopeNamespaces`, `NamespaceOf`, `AddressOf`, `HistoryOf`, `EngineRecord`, `DurableRecord`, `RecordOfEntry`, `AliveAt`, `DocumentRead`, `ReadAt`.
- Autobind: library entry `durable:./storage/store` -> `./durable/storage/store` added to `libraryEntries`.

## Open points

1. `chord/next/store` `Attachment`'s doc still says adapters "adopt" in the batch: Pi's rename to "publish" is wording only.
2. Re-running the `durable/harness` tests over `StoreStorage` (Pi's `vitest.store.config.ts` replaces the SQLite opener by `store-storage-node.ts`, leaves four raw-SQL tests out): proposal for NDurHarness: the harness tests open their storage through one helper (the Go counterpart of `openNodeSqliteStorage`); give it a package-level `var openStorage = func(t, path) durable.Storage { sqlite... }` and add an `init`-time switch (environment variable `PIG_DURABLE_STORE=1` or a build tag `storestorage`) in a `_test.go` file of `durable/harness` that points it at `store.OpenNodeSqliteStorage`-equivalent (`durable/storage/store` exports `Open`; its test helper `openStoreStorage` is the opener to copy), skipping the four tests named in `vitest.store.config.ts` (`resumes a retry backoff after reopen`, `sleeps again for a retry stored as a running phase`, `polls a checkpoint stored without a thinking level`, `refuses to create the root of an empty storage`). Pending until `durable/harness` has such a helper.
3. Design note: the per-commit `receive` of `StoreStorage.Commit` ignores `ids` (a Durable commit creates no engine ids through the step; its documents carry explicit ids, as in Pi).
