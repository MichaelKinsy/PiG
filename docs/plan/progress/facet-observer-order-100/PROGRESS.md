# facet-observer-order-100: an isolated facet's in-host keyed observation is live when activation proceeds (C24)

Branch `facet-observer-order-100`. Base: `agg-100` `16118d1e5`. Pi 1.0.0.

## Status

READY.

## Cause

`TestNodeFacetObserverRunsPrefixWithoutAwaitingPromise` read the Node observer's log right after `Spawn("first")` and sometimes saw `[]`. The test does not read too early: PiG differed from Pi.

An isolated (Node) facet observes a remotely exposable in-host keyed service through `chord.ObserveFacetService`, which uses the host's internal loopback binding. `keyedBinding.observe` launches the subscription start (`transport.Subscribe`, snapshot spawn, `Activate`, `markReady`) on a goroutine, and facet activation did not wait for it. `CreateFacetHost` could therefore return before the subscription existed or was ready. A later `Spawn` then landed in the start's snapshot or in its pre-activation buffer, and the observer ran on the start goroutine after `Spawn` returned.

In Pi, `FacetLifecycle.activate` (`packages/chord/src/facets/host.ts`) starts each observation synchronously. `KeyedBinding.#start` (`services/consumer.ts`) calls the loopback `subscribe` synchronously and resumes one microtask later, at `activate`'s first `await`: after the first activation callback's synchronous prefix, or when `activate` returns. The in-host subscription and its snapshot observers are therefore live before the next callback, the next facet's activation, and `createFacetHost`'s return. A later `spawn` applies every observer during instance delivery, before `spawn` returns (`services/instances.ts` `#start`).

Probe of Pi 1.0.0 (`node probe.ts` against `.upstream/current/packages/chord/src`, facets `later` uses a service of `observer`, `observer` observes the keyed service that `provider` spawns `zero` into on activation, then the caller spawns `one`):

- no observer callbacks: `["activate provider","observe","activate later","host created","observe","spawned one"]`
- two observer callbacks: `["activate provider","activate observer 1","observe","activate observer 2","activate later","host created","observe","spawned one"]`

## Fix

- `keyedBinding.observe` returns the subscription start the observer joined; `RemoteServiceBinding.observeConnecting` exposes it inside the package (`Observe` is unchanged).
- A facet observation start returns `(stop, connecting)`. `ObserveFacetService` returns the internal loopback start for an in-host remote observation; local and external-source observations return none, because an external source subscribes asynchronously in Pi too.
- `facetLifecycle.activateNow` waits for those starts after the first activation callback, or after the observations start when the facet has no callback. A start error is still reported by the binding, as Pi's unawaited `#starting`. The same path serves reload activation.

The test is unchanged.

## Evidence

Load: `taskset -c 100-103 go test -count=1 -p 12 ./internal/... ./tui/... ./ai/... ./agent/...` in a loop on the same 4 CPUs.

- Red on the base under load: `taskset -c 100-103 internal/experimental.test -test.run '^TestNodeFacetObserverRunsPrefixWithoutAwaitingPromise$' -test.count=50` failed twice in two runs with `observer prefixes after first spawn = [], want ["observed-1"]` (1 of 50 each).
- Green under the same load: `-test.count=200` passes (178s). All `TestNodeFacet*` with `-test.count=10` pass.
- New regression `internal/chord` `TestIsolatedFacetKeyedObservationJoinsActivation` asserts both Pi traces above. On the base it fails 200 of 200 runs for both subtests (`go test -count=200`). Mutation: waiting only at the end of activation fails the two-callback subtest 50 of 50. Green: `-count=200`, `-race -count=200` (10 repeats), and `-count=200` on 4 loaded CPUs.
- `go test -race ./internal/chord/...` passes; `go test -race -count=3 -run TestNodeFacet ./internal/experimental` passes.
- `go vet`, `golangci-lint run ./internal/chord/` (0 issues), `gofmt`, `make source-hygiene`.

## Observed, not in scope

`go test ./internal/experimental` in this worktree fails `TestEncodeControlLineExactUpstreamJSON`, `TestCoordinator*` and `TestPinnedUpstreamRadiusSource` with `Cannot find module 'typebox'` or a missing `test/parity/interface-extractor/node_modules/typescript`: the worktree has no Node dependencies installed. They do not use the changed code.

Go facets' `ObserveService` resolves remotely exposable in-host keyed services through the local registry, which runs snapshot observers when the observation starts, before the facet's first activation callback. Pi routes them through the internal loopback binding and runs them after that callback (the trace above). The ported upstream case tolerates both orders. This lane does not change it; it needs its own lane.
