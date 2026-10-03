# READY: chord-100

Branch: chord-100 (base agg-100). Review: Opus. Commands run: `go test -race ./internal/chord/...` (green), `go tool golangci-lint run ./internal/chord/... ./agent/harness/pico3/...` (0 issues), `make test-inventory`, `make interface-inventory-drift`, `make interface-go-drift`, `make port-map-drift`, `make docs-drift`, `make source-hygiene` path check.

## Not green, by cause

- `make test-porting-release` and `make generate` (known-gaps step): five rows list `designedOutCases` that need `SCRUTINIZED:approved` from the owner. The policy rationale says `SCRUTINIZED:proposed`; I did not write an approval.
- Environment: `internal/experimental` oracle tests need `node_modules` in the pinned upstream tree.

## Scope

`internal/chord/delta` (ops, applier, codec, overlap, diff, tracker), `internal/chord/chordjson`, `internal/chord/chordctx`, a 1.0.0 `state.go`, per-subscriber provider queues with `reset` updates, and the facet fixes below. `agent/harness/pico3` aliases the chord types so experimental still builds.

## Red to green

The delta package was written before its tests, so those tests were not red first. Mutation checks stand in: each line below names the break that turns a test red.

| Finding | Test | Red when |
|---|---|---|
| `DiffRevisions` emitted `null` for the items of a pure removal, and an empty batch was `nil` | `TestDeltaDiff`, `TestStateDiff` (removal cases) | failed on first run, fixed in `diff.go` |
| A change with a competing adoption left a prepared result without an owner | `TestDeltaImmutableTrackerLifecycle` | failed on first run, fixed in `tracker.go` |
| An aliased draft container restored one base container at two paths (random map order) | `TestTrackerAliasedDraftPlacementsStayIndependentWhateverTheKeyOrder` | `claimed` check removed |
| Typed `Change` could not emit `m` or narrow splices for moved elements | `state_upstream_test.go` "preserves compact string, splice, and permutation operations" | `matchMoved` removed |
| Applying 1,000 writes to a 20,000-field map copied it once per write | `TestImmutableApplicationComplexity` | `own` always copies (1.3 GB allocated) |
| Per-subscription delivery bound | `TestPublicDelivery` bound cases | `maxPendingDeliveries = 101` (19 failures) |
| Provider overflow reset | `TestProviderDeliveryQueues`, `TestRebaselines...` | `maxPendingUpdates = 1000` (10 failures) |
| Reset must carry full root replacements | `TestServiceWireProtocol` reset case | validation removed |
| Retired keyed close failure was reported to a log and the reload went on | `TestFacetHost` replacement and retirement cases | `ServiceSpawner` returned no close error: fixed in `facets.go` |
| A facet received a new handle on each `UseService` for one service | `TestFacetHost` scopes singleton views | fixed in `facets.go` (`facetRuntime.refs`) |
| Host phase error masked the facet lifecycle error for a handle used during setup or after dispose | `TestFacetHost` several cases | order swapped in `facets.go`, `facet_remote.go` |
| Three 0.87.1 chord tests asserted behavior 1.0.0 changed (listener failures returned, reentrant hydration, reentrant dispose order) | `TestStateListenerFailureIsReportedAfterCommit`, `TestDeltaSubscribeHydrationChangeIsSerialized`, `TestReviewReentrantProviderDisposeDeliversUnavailable` | updated to the 1.0.0 rule with the upstream source cited |

## Go mappings a reviewer should confirm

Object keys are visited in ascending order. A Promise-returning listener returns a `Completion`. The default reporter for failures nobody handled is `SetUncaughtErrorReporter` (stderr, process keeps running). The Draft proxy is a private working copy that `Prepare` validates, de-aliases and diffs, so mutation after placement, handle revocation, tracker region-folding operation shapes and JavaScript-only cases are designed out per case in the mapping rationale. These are not yet numbered in `docs/parity/DIVERGENCES.md`; packages/chord is outside the Stock PiG observable surface, so I recorded them in the mapping rationale and the package comment instead.
