# gap-chord: Pi 1.0.4 `packages/chord` accounting

Reference: `.upstream/v1.0.4/packages/chord` (npm `@earendil-works/chord` 1.0.4). The package source and tests are byte-identical to 1.0.0 (only `package.json` differs), so the 1.0.0 port needed no behavior re-port; this lane re-checked it against 1.0.4 and closed the gaps listed under "Closed in this lane". PORT_MAP gets no rows from this lane; integration adds them from the table below.

## Counts (PORT_MAP file rule: ported over intended-portable, n/a excluded; the percentages are in the lane report)

| | before | after (if integration adds these rows) |
|---|---|---|
| `packages/chord/src` rows in PORT_MAP | none of 29 (outside the denominator) | 29 rows |
| chord ported share | not counted | 26 ✅ of 26 intended (3 n/a) |
| PORT_MAP aggregate | 379 of 493 | 405 of 519 |
| chord test files with a ported ledger disposition | all 21 `*.test.ts`; none of the 9 support files accounted | all 21 ported; all 9 support files accounted |

## Source map

Evidence is `file:line` of the Go declaration. "n/a" means designed out (mechanism, not behavior).

| upstream (`packages/chord/src/`) | Go | status |
|---|---|---|
| `index.ts` | every runtime export has a Go counterpart in `internal/chord` (types.go:137 error codes, wire.go, state_wire_parse.go:202, state_codec.go:75), `internal/chord/chordjson` and `chord/` | ✅ |
| `api.ts` | `internal/chord/facets.go:859` CreateFacetHost, `:83` CreateStaticFacetLoader, `:91` CombineFacetLoaders, `service.go:69` DefineService, `binding.go:87` CreateRemoteServiceBinding, `state.go:389` NewReplicatedState, `:646` AttachReplicatedState (`chord/state.go:363` public form) | ✅ |
| `types.ts` | `internal/chord/types.go` (Context is `context.Context`; `JsonRepresentation`, `RemoteServiceContract`, `IsAny` are compile-time TypeScript machinery with no Go form, replaced by reflection classification at provide time, `provider.go:777`) | ✅ |
| `json.ts` | `internal/chord/chordjson/chordjson.go:70` Copy, `Stored`, `IsValue`; `chord/json.go:12` | ✅ |
| `context/index.ts` | `internal/chord/chordctx/chordctx.go:14` NewKey, WithValue, WithAbortSignal, WithoutAbortSignal, WithCancel, Await | ✅ |
| `delta/index.ts` | `chord/delta/delta.go:317` AssertValidOpValue, `apply.go:227` Apply, ApplyImmutable, `apply.go:255` ApplyImmutableBatchesSeq, `codec.go:17` Encoder/Decoder, `overlap.go:17` | ✅ |
| `delta/diff.ts` | `chord/delta/diff.go:611` DiffRevisions | ✅ |
| `delta/tracker.ts` | `chord/delta/tracker.go:50` (overlay with Object/Array draft handles, the Proxy draft), driven by `internal/chord/state.go` `Edit` and `state_overlay.go` for typed `Change`; array roots through `chord/delta.TrackArray` (`Change.Elements`, `Tracker.Root`, `Prepared.ValueRoot`, `PrepareReplaceRoot`); `chord/delta/copy_tracker.go` `DiffCandidate` diffs a typed-state candidate | ✅ |
| `delta/draft.ts` | TypeScript mapped type; the Go draft is `chord/delta` Object/Array | n/a |
| `delta/apply-immutable-trusted.ts` | self-produced-op fast path of the tracker; the Go trackers materialize candidates themselves and `ApplyImmutable` covers the checked path (apply_immutable_upstream_test.go) | n/a |
| `delta/revision-validator.ts` | WeakSet memo of validated JS container identities; a decoded Go revision has no shared identity and is validated by `chordjson.Validate` (`state.go:880`) | n/a |
| `facets/host.ts` | `internal/chord/facets.go:833` facetKernel | ✅ |
| `facets/loader.ts` | `internal/chord/facets.go` disposal join inside CombineFacetLoaders (`AggregateError`) | ✅ |
| `services/consumer.ts` | `internal/chord/binding.go`, `binding_async.go` | ✅ |
| `services/provider.ts` | `internal/chord/provider.go:84`, `endpoint.go:32` | ✅ |
| `services/state.ts` | `internal/chord/state.go` (`:389` mutable, `:646` attached, `:822` replica) | ✅ |
| `services/state-codec.ts` | `internal/chord/state_codec.go:75` | ✅ |
| `services/state-internals.ts` | `chord/state.go` ReplicatedStateInternals; `internal/chord/state.go` replicaCore | ✅ |
| `services/wire.ts` | `internal/chord/state_wire.go`, `state_wire_parse.go`, `wire.go` | ✅ |
| `services/handle.ts` | `internal/chord/views.go`, `facets.go:562` serviceSlot | ✅ |
| `services/instances.ts` | `internal/chord/keyed.go:30` instanceDirectory | ✅ |
| `services/errors.ts` | `internal/chord/types.go:137-148` | ✅ |
| `services/loopback.ts` | `internal/chord/endpoint.go:105` NewLoopbackTransport | ✅ |
| `node.ts` | `internal/experimental/node_facets.go:56` CreateFacetBundleLoader, `:68` artifact loader, `:271` ReadFacetBundleArtifact, `:319` ReadFacetBundleManifest | ✅ |
| `bundler.ts` | `internal/experimental/plugin_package_build.go:114` BundleFacets, `facet_bundle_build.go:52` BundleFacetPackage | ✅ |
| `node/manifest.ts` | `internal/experimental/facet_bundle.go:13` | ✅ |
| `node/bundle-loader.ts` | executed by the vendored upstream `node-facets/chord/node/bundle-loader.js` in an isolated Node process (`node_facets_protocol.go`) | ✅ |
| `node/bundle.ts` | `internal/experimental/plugin_package_build.go` (pinned esbuild Go compiler) | ✅ |
| `node/package.ts` | `internal/experimental/plugin_package_metadata.go`, `facet_bundle_build.go` | ✅ |

Non-source files: `README.md` and `src/delta/README.md` are documentation (Pi's contract text; the Go tests cite the delta README for "batches are exact but not canonical"); `PLANNING.md` is upstream planning prose; `package.json`, `tsconfig.build.json` are TypeScript packaging with no Go form.

### `delta/tracker.ts`

Two Go trackers port it. The overlay tracker `chord/delta` (`Object`/`Array` handles, the Proxy draft) now records the exact operation batch Pi records, order included: `chord/delta/tracker_pi_ops_test.go` replays 1,500 seeded edit scripts (set, delete, delete and re-add, push, unshift, shift, pop, splice, reverse, sort, index writes, row field edits, string append/cut/replace) against golden batches that `chord/delta/testdata/pi_change_operations.mjs` records from the pinned `tracker.ts`. Differential testing found four differences, now fixed: emission order (Pi buckets dirty nodes by path length and keeps first-dirty order within a bucket, while the Go tracker walked the tree), `d` before `s` for a re-added base key, deletes in deletion order and base-element overwrites in write order, and no-op writes (an equal primitive) that must not dirty a node.

Typed state is rebuilt on that tracker. An object-rooted `MutableReplicatedState` owns a `chord/delta` Tracker, and two entry points publish through it:

- `Edit(ctx, func(*delta.Object) error)` is Pi's `change(context, draft => ...)` with the draft handle itself. Its batches equal Pi's exactly, order included (`internal/chord/state_pi_ops_test.go` `TestEditPublishesPiOperationsForRandomizedEdits`, the same 1,500 golden scripts), and it carries the lifecycle cases of `tracker.test.ts` (revoked draft, abort on error or panic, restoring edits publish nothing, reentrancy).
- `Change(ctx, func(T) error)` edits a typed Go value, whose edit history Go cannot observe. It diffs the candidate against the base with a scratch tracker (which restores container identity so moved elements match by content), then replays those edits through the overlay draft, so the published batch comes from the same emitter and ordering as `Edit`. Before this, the copy-diff batch differed from Pi's for 245 of 400 scripts. The batch is Pi-valid for an edit script equivalent to the typed change, not necessarily the script a TypeScript caller would have written; `TestChangeReplaysToPiValuesForRandomizedEdits` proves the value and the replay for all 1,500. This is the language mechanic of typed state, not a divergence: Pi has no typed `Change`.
- An array-rooted state uses the same tracker: `chord/delta.TrackArray` is Pi's `track([...])` (tracker.ts:321), its operations address the root with the empty path, and `EditArray(ctx, func(*delta.Array) error)` is `Edit` for it. A typed array root is a pointer to a slice (`*[]any`) so `Change` can grow it. `Edit` on an array root and `EditArray` on an object root return an error. 800 seeded scripts over array roots (`chord/delta/testdata/pi_array_root_operations.mjs`, rows and numbers, push/unshift/shift/pop/splice/reverse/sort/index writes/length/nested edits) match Pi's batches exactly (`chord/delta` `TestTrackerArrayRootOperationsMatchPiForRandomizedEdits`, `internal/chord` `TestEditArrayPublishesPiOperationsForRandomizedEdits`), the typed path replays to Pi's values (`TestChangeOnAnArrayRootReplaysToPiValues`), and `tracker.test.ts` "emits deletion and supports root arrays" is `TestTrackerSupportsRootArrays`. The port found that adopting over an empty array looked stale (a zero-capacity Go slice has no allocation to compare), fixed with `sameRoot` and guarded by `TestTrackerAdoptsOverAnEmptyArrayRoot`.
- Naming forced by Go's static types: Pi's one `track`/`value`/`state`/`prepareReplace` serve both root kinds; `chord/delta` keeps `Track`, `Value`, `State`, `PrepareReplace` typed for an object root (they return nil for an array root) and adds `TrackArray`, `Root`, `Elements`, `BaseRoot`/`ValueRoot` and `PrepareReplaceRoot` for either kind, so existing object-root callers keep their types.
- A replacement may change the root between an object and an array, as tracker.ts `prepareReplace` allows (one `r` operation). The tracker takes the new root kind natively (`PrepareReplaceRoot`, `TestTrackerRootKindsAndReplacement`, `TestReplaceChangesTheRootKind`).

Added `Array.Reorder(permutation)` to `chord/delta` so a derived `m` operation can be replayed. The typed path is the Go adaptation above.

`testdata/pi_change_operations.mjs --folds` records a second golden set (`pi_change_operations_folds.json`, 200 scripts) with edits below reserved keys and runs of row edits, so the emission position of reserved-key folds and dense-region splices is compared with Pi too. Both sets drive `TestTrackerOperationsMatchPiForRandomizedEdits`, `TestEditPublishesPiOperationsForRandomizedEdits` and `TestChangeReplaysToPiValuesForRandomizedEdits`.

## Test map

All 21 `*.test.ts` files carry `ported` dispositions in `test/parity/interfaces/test-mapping-v1.0.4.json` with evidence files that exist (checked), pinned to the 1.0.4 content hash. 238 of 274 upstream case titles appear verbatim in the Go tests; the other 36 are covered under renamed Go tests per the ledger rationale (bundle.test.ts, context.test.ts, and the tracker/state-draft proxy cases with Go equivalence tests). That residual set was cross-referenced by title only and is flagged for review.

| upstream test | Go |
|---|---|
| `boundary.test.ts` | `internal/chord/boundary_test.go` |
| `bundle.test.ts` | `internal/experimental/bundle_upstream_test.go` |
| `context.test.ts` | `internal/chord/chordctx/chordctx_upstream_test.go` |
| `delta-apply-immutable.test.ts` | `chord/delta/apply_immutable_upstream_test.go` |
| `delta-clone.test.ts`, `state-value.test.ts`, `state-fuzz.test.ts`, `state-draft.test.ts` | `chord/delta/copy_tracker_upstream_test.go`, `chord/delta/tracker_equivalence_test.go` |
| `delta-diff.test.ts`, `state-diff.test.ts` | `chord/delta/diff_upstream_test.go` |
| `delta.test.ts` | `chord/delta/delta_upstream_test.go` |
| `delta-tracker/tracker.test.ts` | `chord/delta/copy_tracker_cases_test.go`, `chord/delta/tracker_upstream_test.go`, `chord/delta/tracker_equivalence_test.go` |
| `delta-tracker/retention.test.ts` | `chord/delta/retention_upstream_test.go` |
| `facet-loader.test.ts` | `internal/chord/facet_loader_upstream_test.go` |
| `facets.test.ts` | `internal/chord/facets_upstream_test.go` |
| `json.test.ts` | `internal/chord/chordjson/chordjson_upstream_test.go` |
| `service-delivery.test.ts` | `internal/chord/service_delivery_upstream_test.go` |
| `service-wire.test.ts` | `internal/chord/service_wire_upstream_test.go` |
| `services.test.ts` | `internal/chord/services_upstream_test.go` |
| `state-delivery.test.ts` | `internal/chord/state_delivery_upstream_test.go` |
| `state.test.ts` | `internal/chord/state_upstream_test.go` |

### Support files (previously unmapped)

| upstream | disposition |
|---|---|
| `test/helpers.ts` | One re-export of `createLoopbackServiceTransport`; Go `NewLoopbackTransport` (`endpoint.go:105`) is what the Go tests import. |
| `test/delta-tracker/retention.worker.ts` | Node worker spawned by `retention.test.ts` for `--expose-gc`/`WeakRef`; the Go test observes the heap in-process (`retention_upstream_test.go`). Designed out with its test. |
| `test/delta-traversal.bench.ts`, `test/delta-benchmark/benchmark.ts`, `benchmark.worker.ts` | Tracker scenarios ported as `chord/delta/tracker_bench_test.go` (import, committed-read, draft-read, sparse, queue, sort, dense, unshift). Not run by `vitest --run`. The Node-worker orchestration and V8 heap sampling are replaced by `testing.B` ns/op and allocations; the `watch-*` scenarios need durable watch fan-out and are not ported (they exercise `packages/durable`, not chord). |
| `test/delta-benchmark/memory-benchmark.ts`, `memory-benchmark.worker.ts` | V8 heap and GC-pause measurement through `PerformanceObserver`; no Go counterpart (designed out). Retention is proven by `retention_upstream_test.go`. |
| `test/delta-benchmark/conversation-view-benchmark.ts`, `.worker.ts` | Provisional Pico5 `ConversationView` architecture experiment that the file itself marks as not a compatibility commitment; PiG tracks Session behavior instead of Pico5 (PORT_MAP package scope). Designed out. |

## Closed in this lane

1. **Stale Node facet runtime.** `internal/experimental/node-facets/chord` was the published Chord 0.87.1 dist while the port tracks 1.0.4 (attached sources, reset updates, provider draining, `delta/tracker.js`, atomic `ReplicatedStateInternals.snapshot`). `automation/ci/vendor-node-facets-chord.py` now materializes the import closure of the driver's roots from the integrity-locked dist, `make node-facets-chord-check` gates drift (in `check-core`), and `driver.mjs` reads `state.snapshot()` instead of the removed `state.sequence`/`state.value`. Red: `TestNodeFacetsRemoteMethodsAndState` failed against the new closure before the driver change.
2. **Overlay array queue cost.** `chord/delta` `node.splice` copied the entire entry list per Shift/Push/Pop/Unshift/Splice (1.6 GB for 1,000 shift+push steps over 100,000 rows); `tracker.ts` splices in place. Fixed with `slices.Replace`; `TestTrackerQueueStepsDoNotCopyTheEntryList` was red at 66.9 MB and is green. Found by porting the benchmark scenarios.
3. **Overlay emission order and no-op rules** (above): `tracker.go` emission by path-length bucket and first-dirty order, `d`+`s` for re-added keys, deletion and write order, no-op writes.
4. **Typed state on the overlay tracker** with `Edit` and `Array.Reorder` (above).
5. **Benchmarks ported** (above) and **stale `v1.0.0` source citation** in `internal/chord/types.go` replaced.

## Open items for the owner or review

None open.

## Scope notes for the owner

- Chord's service, facet, context and bundler runtime stays under `internal/` for 0.5.0 (lead decision: a public Go API is a long-term commitment). Only `chord` and `chord/delta` are importable today.
- Review (rev-gap-chord) checked the title-unmatched cases: each is ported under a PascalCase test name, a `t.Run` title, or a `file.test.ts:<line>` citation, except the escaped-draft half of state.test.ts:34, now ported on the `Edit` path (`TestEditLifecycle`).
