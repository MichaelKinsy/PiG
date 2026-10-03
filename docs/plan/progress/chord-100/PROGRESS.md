# chord-100: port packages/chord 1.0.0 to Go (internal/chord)

Task: chord-100 (port packages/chord 1.0.0). Branch: chord-100, base agg-100. WIP: not READY.

## API for d-foundation and the durable lanes (stable now)

Go packages, all pure Go, no pico3 dependency:

| upstream | Go |
|---|---|
| `@earendil-works/chord/delta` | `internal/chord/delta`: `Op`, `WireOp`, `Apply`, `ApplyImmutable`, `ApplyImmutableBatches(iter.Seq)`, `AssertValidOp`, `AssertValidWireOp`, `NewEncoder`, `NewDecoder`, `Overlap`, `DiffRevisions`, `IsBase`, `Track`/`Tracker`/`Change`/`Prepared`, `PrepareCandidate`, `PathError`, `UnsafePathError`, `TypeError` |
| `copyJson`, `isJsonValue` | `internal/chord/chordjson`: `Copy`, `IsValue`, `Validate`, `Stored` (any Go value to strict JSON via encoding/json; this is how durable turns structs into JsonValue) |
| root `replicatedState`, `Context` | `internal/chord`: `NewReplicatedState[T]`, `AttachReplicatedState[T]`, `ReplicatedStateSource*`, `Completion`, `SubscribeAsync`, `UncaughtErrorReporter`, `DefineService`, `ReplicatedStateOf`, `MutableReplicatedStateOf`; Context is `context.Context` |

JSON values: nil, bool, float64, string, []any, map[string]any. Op is `[]any`; paths use int or float64 indices. Object keys are visited in ascending order (Go maps have no insertion order).

Draft: Go has no Proxy. `Change.State()` returns a private deep copy to edit with plain map/slice code; `Prepare()` validates, de-aliases, restores base identity for equal containers and diffs. Typed state uses `Change(ctx, func(*T) error)` over a decoded copy, then `Tracker.PrepareCandidate`.

Not yet published for durable (next): chord/context equivalents (`withAbortSignal`, `withoutAbortSignal`, `awaitWithContext`, `withCancel`, typed context keys). Until then use `context.WithCancel`, `context.WithoutCancel`, and select on `ctx.Done()`.

## Done

All 21 chord test rows in `test-mapping-v1.0.0.json` are ported (boundary, bundle, context, delta, delta-apply-immutable, delta-clone, delta-diff, delta-tracker/retention, delta-tracker/tracker, facet-loader, facets, json, service-delivery, service-wire, services, state-delivery, state-diff, state-draft, state-fuzz, state-value, state). See READY.md for the red-to-green list and the open owner decision.

## Remaining outside this lane

- Owner approval of five `designedOutCases` lists in `test-porting-policy-v1.0.0.json` (marker `SCRUTINIZED:proposed`), which blocks `make test-porting-release` and `make generate`.
- The experimental oracle tests fail in this tree because the pinned upstream checkout lacks `node_modules` (`cross-spawn`, `@earendil-works/pi-durable`); unrelated to this change.
- Pre-existing failures seen while running gates: `interface-mapping-strict` (3,346 pending ids), `async-contracts` (experimental durable sources), `divergence-guard` baseline entries in agent/harness, a subprocess node-bundle regeneration test.
