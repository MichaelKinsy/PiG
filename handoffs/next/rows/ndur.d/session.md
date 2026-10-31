# Rows of lane N-DUR slice `session` (Pi `6d4b7443`, group commit `e5c6182b`) for the integrator

Branch `fix/follow-ndur-session` on `fix/follow-ndur` `9f56026a3a` (merged: `fix/follow-ndur-sessionunits` `fe4ef64288`, `fix/follow-ndur-storage` `40221e5662`). Red commit `770c1cc493` (tests only); port commit `5762fb403e` and the commits after it. Delete this file after transcribing.

Red record (xd670, `~/ndur-tools/ndur-run.sh session -- 'go vet ./durable/session/...'` at `770c1cc493`): fails to compile, first lines
`durable/session/transaction.go:290:20: cannot use (*Transaction)(nil) ... *Transaction does not implement durable.Tx (missing method LatestEntryId)`,
`durable/session/guard.go:58:10: ... rangedGuard does not implement durable.Storage (wrong type for method Commit)`,
`durable/storage/memory.go:307:25: ... *MemoryStorage does not implement durable.Storage (wrong type for method Commit)`.
Green: `go vet ./durable/session/...` and `go test -race -count=1 ./durable/session/...` pass in the container with the two files that import `durable/harness` removed there (`codemode_checkpoints_upstream_test.go`, `session_document_retention_test.go`: they build when the harness slice compiles); `go test -race -count=20 -run 'GroupCommit|Writer|Adoption|Publication|CommitInternal|OnJobEnd|KnownLatest|Touch|AWake'` passes (one failure of my own test string in `TestAReadOnlySession...` fixed after). `gofmt`, `go fix -diff`, `make lint-changed` and the changelog/divergence gates were NOT run (budget): pending.

## Go mapping design note (also the header of `durable/session/session.go`)

Pi's event loop gives group commit two guarantees Go's scheduler does not; the Session keeps them with the shared `internal/entered` mechanism (a goroutine has "entered" once it is parked in a wait, blocked on a lock, or has exited):
1. A publication queues one dispatch to the host's observers, which runs a microtask later, before every wait the commit resolves. Here `writerHost.Publishing` runs the part's publication inside the Storage's scope (`PublishingStorage`, else directly), then `endPublication` runs the queued dispatch, then the work the observers deferred (`DeferUntilPublished`: waiter wakes, aborts), and returns once the goroutines those wakes readied have entered (`wakeAndEnter`: goroutines parked before the wake and runnable after it, from two goroutine dumps; only when something was deferred).
2. Outcomes are delivered in line order and the continuations of what one part resolved start before the next part of the call is published (macrotask hop). A fence (`delivery` queue in line order) is served by one delivery goroutine the Session owns: it releases the caller of each ready job in line order and waits for that caller to have entered before it releases the next; the writer's `Hop` before the next part waits for that goroutine to be idle, then for the watch deliveries (`scheduler.AwaitEntered`). A part's outcome settles synchronously on the writer's goroutine (`Part.OnSettled`), so readiness never depends on a caller's wake-up.
Differences from Pi, each deliberate: a part's keys leave the unpublished set after its own observers ran and before the dispatch (Pi unregisters first; a Go region holding the scheduler lock between a key's removal and its observer would see a clean key and stale state); `Part.Published` (touch of changed documents, unpin of taken holds) runs after the keys left so an eviction sweep it arms sees them gone; a failure caused by an own observer's panic fails the Session before the part resolves for its caller (Pi: one microtask later, still before the caller's continuation); a Storage scope that runs the publication later runs the dispatch inside that scope; gate waits are not cancellable (Pi's are not either); a `Close` waits for the fence to empty.

## (a) Upstream files

| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| packages/durable/src/session/writer.ts | ported | durable/session/writer.go | test:durable/session/session_group_commit_test.go#TestGroupCommitBatching, test:durable/session/session_group_commit_internal_test.go#TestWriterFailsTheSessionForAThrowInTheReadCachesWriteThrough | `Writer`, `PartSpec`/`Part`, `WriterHost` (+`Publishing`, `Hop`), `PublishFailure`; per-part outcomes of design doc 3.4; one owned goroutine (`Wait` joins it); `Part.Wait`/`OnSettled`/`Published` in place of the promise |
| packages/durable/src/session/session.ts | ported | durable/session/session.go, guard.go, held.go, retention.go, woken.go | test:durable/session/session_group_commit_test.go, session_line_api_test.go, session_adoption_test.go, session_observers_test.go | logical admission (takeHeld before keys, `Hooks.MembershipKeys`), unpublished write set per key (`Touch`/`AwaitTouch`/`Drained`), `MAX_OUTSTANDING` (`waitRoom`), outcome fences with exemption (`CommitInternal`), deferred commits as parts without writes, cache write-through + tails at publication (`writerHost.Stored`), `readOnLine((storage))` over `GatedStorage` (`rangedGated` keeps the entry range capability), `LatestEntryId`/`LatestHeadMarker`/`KnownLatestEntryId`, gated cold loads, `*` barriers (close, unload, version reload, hold timer), task-document index, sweep skips documents with unpublished changes, `LineSizes`, `OnJobEnd`; `GuardedStorage.Commit` guards the call and the writer fails the Session |
| packages/durable/src/session/transaction.ts | ported | durable/session/transaction.go | test:durable/session/session_group_commit_test.go | `Tx.LatestEntryId`, host `touch/taskDocuments/latestEntryId/latestHeadMarker`, `CreatedTaskIds`, `WriteKeys`, gated retirement scans; "frozen shared read results" has no Go form (see (c)) |
| packages/durable/src/session/forks.ts | unchanged-observable | durable/session/forks.go | none | no delta; its reads go through the gated storage the transaction holds |
| packages/durable/src/session/observation.ts (guardObserver) | unchanged-observable | durable/session/observation.go | none | no code change (its comment still says adopt) |
| packages/durable/src/session/{keys,conversation-tails,cached-storage}.ts | not mine | see sessionunits.md | | |

## (b) PORT_MAP rows to add / replace

| source | PiG | status |
|---|---|---|
| `packages/durable/src/session/writer.ts` | `durable/session/writer.go` | ✅ |
| `packages/durable/src/session/session.ts` | `durable/session/{session,guard,held,retention,woken}.go` | ✅ (woken.go has no upstream: Go mapping) |

## (c) Tests (per upstream test file)

- `test/session-group-commit.test.ts`: ported (`session_group_commit_test.go`, `session_group_commit_internal_test.go`, support in `group_commit_support_test.go`): batching, gates (leaked entry, creation at address, conversation creation, five entry reads, unkeyed scan, line read after un-awaited commit, held document readers x2), outcomes x2, failures per part (reject, other parts, first error after another failure, rejection value, refusal value, adoption throws), publication throws (held adoption x2 variants, cache write-through via `Writer` with its own host), deferred commits x2, publication hop, storage scope x2, failure between publications x3, upkeep x4, backpressure, randomized x5 seeds. designedOut: the `undefined`/`null` rejection values (a Go error is never nil; the cases are ported with an error value); `Transaction.prototype.adopt`/`adoptHeld`/`CachedStorage.prototype.published` spies become scope-aborted prepared changes and a fake `WriterHost`; `eventually`/`flush`/`settled` are `awaitQueued`/`quiesce`/`settled` over `entered`. Added: `session_line_api_test.go` (commitInternal exemption, onJobEnd, known tails, touch/drained, wake-before-next-publication, read-only Session).
- `test/session-support.ts`: ported (`sessiontest/support.go`: `Commit(batches, receive)`, `RefusingAt`; the Pi `LateResolvingStorage` stays with the harness slice).
- `test/session-adoption.test.ts`: ported, new order expectations. Missing: "publishes a JSONL commit once the storage reclaimed its sidecar and resolved" (needs a recording env for the Go JSONL storage; the old file's comment said the order differed, it now matches Pi's): pending.
- `test/session-observers.test.ts`: ported (`adopting: false`). `test/session-document-retention.test.ts`: the `storage.commit` wrapper has no Go counterpart; the Go test only changed `ReadOnLine` call sites; the file builds only with the harness slice.
- Existing Go tests updated to Pi's new expectations: `session_documents_test.go` (settles admitted commits before close), `session_observers_test.go` (attaches atomically), `watch_doc_admission_test.go`, `snapshot_admission_test.go` (a call queues behind a job that holds the line at a gate, via `lineadmission`, not behind a commit in storage), `session_adoption_internal_test.go`, `line_ticket_test.go`, deferred/retention `ReadOnLine` call sites; `retentionClock` moved to `retention_clock_test.go`.
- Not run (budget): `session_checkpoints_migrations_test.go` etc. ran green in the full-package run only with the two harness-dependent files removed.

## (d) Async contracts

| file | contract | evidence |
|---|---|---|
| session/writer.ts | one goroutine sends everything admitted while no call is in flight as one `Commit`; parts publish in line order with a hop between; a refusal/rejection/adoption failure fails the Session after the received parts are published and settles every part (own error or `SessionFailed`), gates open once settled; a call settling after another failure publishes nothing; never strands an outcome or gate (`abandon`); `Wait` joins it | test:durable/session/session_group_commit_test.go#TestGroupCommitFailuresPerPart, #TestGroupCommitAFailureBetweenTwoPublications |
| session/session.ts | jobs run one at a time on their callers' goroutines; the line is released after callback, settlement, admission; reads wait at gates (never cancellable); outcomes delivered in line order by the fence (exempt jobs at once); backpressure at 256; close drains writer and fence, stores holds through the writer; `Touch` takes only the writer's and the job lock | test:durable/session/session_group_commit_test.go#TestGroupCommitOutcomes, #TestGroupCommitBackpressureAndBounds, session_line_api_test.go |
| session/session.ts (GuardedStorage.commit) | guarded call, not failing the Session itself | test:durable/session/session_adoption_test.go |

## (e) New public Go API (`durable/session`)

`Writer`, `NewWriter`, `WriterHost`, `PartSpec`, `Part`, `PublishFailure`, `PublishFailureKind` (+ `PublishAdoption`, `PublishListener`); on `*SessionImpl`: `Touch`, `AwaitTouch`, `AssertHealthy`, `Drained`, `LineStorage`, `OnJobEnd`, `LineSizes` (`LineSizes`), `LatestEntryId`, `KnownLatestEntryId`, `LatestHeadMarker`, `CommitInternal`, `CommitInternalWithTicket`; `ReadOnLine`/`ReadWithTicket` now take `func(storage durable.Storage) (any, error)`; `Hooks.MembershipKeys`; `(*Transaction).CreatedTaskIds`, `WriteKeys`, `LatestEntryId`; `DocumentCacheSizes.TaskDocuments`/`Tails`; `SnapshotErased`/`WatchDocErased`/`ObserveDocErased`/`SnapshotAsOfErased` report line admission through `lineadmission.From(ctx)`.

## Open questions

- NDurHarness reports a deadlock in their scheduler tests with many `TaskScheduler.reconcile` goroutines parked in `CommitInternalWithTicket` waiting for the line (`session.go onLine`). Not reproduced here and not diagnosed. Candidates on the Session side: a job holding the line in `waitRoom` (256 outstanding) while the fence head is a ticket that was taken and not yet used or released (the delivery goroutine only releases ready heads); a delivery goroutine waiting (`entered.AwaitBlocked`) for a caller that keeps running. A goroutine dump of the hang is needed.
- `DeferUntilPublished` still defers for any goroutine while a publication is in progress (as before); a call from a non-publishing goroutine would be deferred.
- `Part.Published`/hop use two goroutine dumps per publication that deferred wakes: a cost on very large goroutine counts.
