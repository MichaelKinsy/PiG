# Rows of N-DUR slice `sessionunits` (Pi `6d4b7443`, group commit `e5c6182b`) for the integrator

Branch `fix/follow-ndur-sessionunits` on `fix/follow-ndur` `9f56026a3a`. Red commit `602aad6f53` (tests only); port commit `fe4ef64288`. Delete this file after transcribing.

Red command (xd670, the port files removed from the synced tree, tests copied into a scratch package `durable/zzunits` created by the command itself):
`go vet ./durable/zzunits/` -> `vet: durable/zzunits/cached_storage_test.go:110:50: undefined: zzunits.CachedStorage`.
Green: the same scratch package with the three files and their tests, `go vet ./durable/zzunits/ && go test -count=3 -race ./durable/zzunits/` -> `ok`; `gofmt -l` prints nothing for the six files. `durable/session` itself does not compile until the session slice lands, so it was not built; the scratch copy needs nothing outside `durable`. The test backend is a small in-test Storage (`countingBackend`: it embeds `durable.Storage`, implements `Commit`, `Task` and counts reads), because `storage.MemoryStorage` and `durabletest.CommitWrites` only take the new `Commit` when the storage slice lands; the tests do not depend on them.

## (a) Upstream files

| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| packages/durable/src/session/keys.ts | ported | durable/session/keys.go | test:durable/session/keys_test.go#TestGatedStorageReadsTouchTheirKeysFirst | key functions return strings of the same text; `OwnedKey` is generic over `TaskId \| string` (Pi's union); `ROOTS_KEY/INTENT_KEY/ALL_KEYS` are `RootsKey/IntentKey/AllKeys`; `touch` is a blocking `func(ctx, key) error` (Pi's `Promise<void> \| undefined`), a failed touch fails the read and skips it; `entry`'s overloads are `Entry`/`VisibleEntry` (the latter touches the conversation, then the entry, as `#after` chains) |
| packages/durable/src/session/conversation-tails.ts | ported | durable/session/conversation_tails.go | test:durable/session/conversation_tails_test.go#TestConversationTailsAnswersCutoffFromNewestMarkerOnlyAtOrAfterIt | `latest`/`marker` take blocking `read` callbacks returning `(value, error)`; `peek` is `Peek(id) (EntryId, TailState)`; `size`/`markers` getters are `Size()`/`Markers()`; guarded by a mutex never held across `read` (several goroutines may Apply and read) |
| packages/durable/src/session/cached-storage.ts | ported | durable/session/cached_storage.go | test:durable/session/cached_storage_test.go#TestCachedStorageKeepsEndedTasksATaskWaitsOnUntilItMovedOnFromItsWait | `published` is `Published`, `sizes` is `Sizes()` (`CachedSizes`), `readsInFlight` is `ReadsInFlight()`; `Lru` is the unexported generic `lru` (shared with the tails' two bounded maps, which Pi writes as `use`/`put` over a `Map`); one mutex guards the state, never held across a backend read; `scopeOf` is `ScopeOf(storage) func(ctx, publish)` (nil when none) |

## (b) PORT_MAP rows to add

| source | PiG | status |
|---|---|---|
| `packages/durable/src/session/keys.ts` | `durable/session/keys.go` | ✅ |
| `packages/durable/src/session/conversation-tails.ts` | `durable/session/conversation_tails.go` | ✅ |
| `packages/durable/src/session/cached-storage.ts` | `durable/session/cached_storage.go` | ✅ |

## (c) Tests

- `test/session-cached-storage.test.ts` (7 cases): all ported (`cached_storage_test.go`). Added: `TestCachedStorageReadFromSeveralGoroutinesWhileCommitsPublish` (the `-race` case for the readers). designedOut: none; the "frozen" half of "keeps the written record itself, frozen: no copy" (`Object.isFrozen(record.state)`) has no Go counterpart, the port asserts the sharing (two reads return the same pointer, the written record's checkpoint pointer is the one kept).
- `test/session-conversation-tails.test.ts` (5 cases): 4 ported; designedOut: "freezes the markers it keeps" (`Object.freeze`; kept markers are shared pointers, immutable by convention). Added: `TestConversationTailsPeekIsThreeStated`, `TestConversationTailsReadErrorsAreReturnedAndNothingIsKept`.
- `keys.ts` has no test file of its own upstream; `keys_test.go` (key texts, `TableKeys`, `GatedStorage` touch order and failed touch) is added. Upstream tests that exercise keys indirectly: `test/session-group-commit.test.ts` (session slice).

## (d) Async contracts

| source | contract | evidence |
|---|---|---|
| session/keys.ts (`GatedStorage.#after`) | the read runs after `touch(key)` returned; `touch` may block until the newest unpublished writer's gate opened, nil at once when none; sequential touches (conversation, then entry or appends) in Pi's order; a touch error (ctx cancelled) is the read's error and the read does not run; `Commit`/`MintId`/`Close` do not touch | test:durable/session/keys_test.go#TestGatedStorageReadsTouchTheirKeysFirst, #TestGatedStorageFailedTouchSkipsTheRead |
| session/conversation-tails.ts (`latest`, `marker`) | `read` runs without the lock; a result is kept after it returns, a failed read keeps nothing and returns its error; the marker of a cutoff is kept only when no newer entry can change it | test:durable/session/conversation_tails_test.go#TestConversationTailsReadErrorsAreReturnedAndNothingIsKept |
| session/cached-storage.ts (`#keep`) | a read in flight registers a version for its record; a publication that wrote the record meanwhile bumps it and the read result is returned but not kept; versions exist only while a read is in flight; read errors decrement the count and keep nothing | test:durable/session/cached_storage_test.go#TestCachedStorageKeepsReadOnlyWhenNoCommitOverlapped, #TestCachedStorageDropsReadOverlappedByPublicationAndKeepsVersionsOnlyForReadsInFlight |

## (e) New public Go API (interface IDs for the integrator)

`durable/session`: `TaskKey`, `ConversationKey`, `EntryKey`, `AppendKey`, `SubmissionKey`, `RequestKey`, `DocumentKey`, `IncarnationKey`, `ScopeKey`, `OwnedKey`, `RootsKey`, `IntentKey`, `AllKeys`, `TableKeys`, `GatedStorage`, `NewGatedStorage`; `ConversationTails`, `NewConversationTails`, `HeadMarker`, `TailState`, `TailUnknown`, `TailNone`, `TailKnown`; `CachedStorage`, `NewCachedStorage`, `CachedSizes`, `ScopeOf`.

## Design notes / open points

- `CachedStorage.Publishing` is always callable, because a Go method cannot be absent: with a backend that has no `PublishingStorage` it runs `publish` directly, which is what the Session does for a Storage without the capability. A caller that must tell the two apart uses `ScopeOf(backend)`.
- Cached records are shared pointers to a struct copy of the written record (a shallow copy of the `Value`, since a write holds it by value); callers must not mutate what Storage returns.
- No changelog fragment: no user-visible behavior change.
