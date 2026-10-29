# Lane D0b rows (durable/core delta tracker vs Pi `a9228305`)

## Finding 1: nothing to port into `durable/core/internal/delta/tracker.go`
The premise (a pre-a9228305 weak registration per change) does not hold. `durable/core/internal/delta/tracker.go` has no registry at all: `Tracker` is `{value, revision}`. Staleness is by revision: `overlay.assertOpen` and `Change.Prepare` refuse a draft whose `baseRevision != tracker.revision`, `Tracker.Adopt` refuses a prepared one the same way. Because revisions only grow, every competing open or prepared draft is stale the moment one is adopted, which is exactly what Pi's `#open` / `#competitors` registry (a9228305) and the older `#contexts` set exist to compute. The tracker holds no draft, so an abandoned draft is reclaimed immediately (Pi holds one per tracker until its next change). Allocation and WeakRef cost: none.
Adding `weak`/map state would break the core's rules: `durable/core/purity_test.go` forbids `sync`, `reflect`, `runtime`, goroutines and package-level mutable state in core packages, and the core is a deterministic single-writer library (layers abi, jv, internal/delta, ...). Disposition: `designed-out` for the registry mechanics, rationale "revision comparison (assertOpen, Adopt) carries Pi's competitor staling; core must stay sync- and runtime-free".

## (a) upstream files
| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| `packages/chord/src/delta/tracker.ts` (a9228305, durable/core copy) | designed-out (registry only; behavior already equal) | `durable/core/internal/delta/tracker.go` | `test:durable/core/internal/delta/tracker_competing_test.go#TestTrackerOlderOfTwoOpenDraftsIsStaleWhenTheNewerIsAdopted` (+4 more) | see Finding 1 |

## (c) tests ported against the durable tracker (all green with no production change, so there is no red commit)
| upstream | PiG test |
|---|---|
| tracker.test.ts "makes the older of two open drafts stale when the newer is adopted" | `TestTrackerOlderOfTwoOpenDraftsIsStaleWhenTheNewerIsAdopted` |
| "... the newer ... when the older is adopted" | `TestTrackerNewerOfTwoOpenDraftsIsStaleWhenTheOlderIsAdopted` |
| "makes every other of three drafts stale, open or prepared" | `TestTrackerEveryOtherOfThreeDraftsIsStaleOpenOrPrepared` |
| "keeps an abandoned draft a competitor of the next change" (WeakRef count: designed-out) | `TestTrackerAbandonedDraftIsStaleAfterTheNextAdoptionAndHeldByNothing` |
| "tracks one change at a time without a WeakRef" | designed-out: the tracker has no registry to count |
| retention.worker.ts "abandoned-draft-released" (reclaimed side) | `TestTrackerReleasesAnAbandonedDraftWithoutAnotherChange`; the "tracker holds its latest open draft" half is designed-out |
| retention.worker.ts "tracker-with-open-draft" | not ported: the overlay holds its Tracker strongly (Pi: WeakRef); the tracker holds no draft and the retained drafts hold only their own revision |

## Finding 2: can durable/core use `chord/delta` directly (Pi has one tracker)?
Not without breaking core's layering, and it is large:
- `chord/delta` imports `reflect`, `encoding/json`, `sync`, `weak`, and `internal/chord/chordjson` (values are `*chordjson.Object`); `durable/core` forbids exactly these (purity_test.go forbiddenImports) and its values are `jv.Object`/`[]any` with WTF-8 strings (jv has no reflection or encoding/json). The core is also built for TinyGo/wasm (`durable/core/budget/tinygo`).
- Size: `durable/core/internal/delta` is 2,648 non-test lines (apply, handles, op, pending, tracker) against 13,481 lines in `chord/delta`; `internal/delta` is imported by 34 non-test files across core, core/{docs,inbox,session,store,turn,contracttest/boundary,session/sessiontest,session/cmd/tgprobe}, and 155 core files use jv values.
- API differences: core handles are `Obj`/`Arr` (chord: `Object`/`Array`); core has `Change.PrepareThen` and `Change.BaseRoot` (absent in chord); chord has `AppendText`, `PrepareReplaceRoot`, per-Change mutexes (core is single-writer, no locks), `Op` as `[]any` with `chordjson` values vs core's own `Op`.
- The two are already cross-checked by `TestTrackerMatchesChord` (tracker_oracle_test.go, oracle against Pi) and `chord/delta` oracle tests, so the duplicate is guarded; the honest unification would be a shared dependency-free kernel both import, which needs chord/delta to drop reflect/encoding/json/sync (a redesign, not a cutover). Recommendation: keep two ports, record the divergence with rationale; do not cut over.
