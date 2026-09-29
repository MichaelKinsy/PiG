# D78 Provider carriers

## Current implementation checkpoint

The registered-native Provider surface is now implemented in Go, Rust, Python, and Node. Initial author/reader rows and packed/fused placement probes pass, including main Model Runtime execution. Cross-process Node getters and filters use a synchronous IO-worker bridge rather than Promises. Callback publications and interactions retain their caller-owned functions. Captured handles survive unregister and fail after owner disconnect. References and active calls govern callback reclamation.

This is not final closure. The exact ModelRuntime/createAgentSession module closure comes from `fix-node-createagentsession`; that merge and locked npm replay remain pending here. Builtin/composed Provider retrieval is under scope review with the lead. Final all-SDK failure/cancellation/replacement evidence and complete gates are still in progress. D78 remains pending.

`Runtime.callSync(method, args)` is the internal Node adapter contract. It returns the decoded host result or throws synchronously. It permits `ui.custom.control`, Provider reference operations, synchronous Provider `getModels`/`filterModels`/publication updates, and synchronous login notifications. `ui.custom.control` takes `{key,action,hidden?}` and returns `{hidden,focused,visible,bounds?}`. Its host implementation belongs to the createAgentSession lane and must not call back into Node. Parent request cancellation and connection failure end the wait; heartbeat stays on the IO worker. Other handlers are not admitted by the synchronous pump.

The carrier lane retains current logs in its external evidence directory, named `fin-d78-carriers`. The initial native host-stream probe exposed a real source bug: `ai/event_stream.go` rejected `done` before `start`, unlike Pi `utils/event-stream.ts:44-63`. `TestAssistantMessageEventStreamDoneCanTerminateBeforeStart` and the all-SDK main-runtime caller are red then green after removing that guard; no synthetic start is inserted. Existing tests remain unchanged. Other sequence guards were reported to the lead. `TestNativeProviderRegistrationWinsBeforeAuthCompletes` also fails on the old source because a slow older auth check reinstalled an old Provider. Registration now commits before checking auth and refuses stale completion.

## Same-cell checkpoint (`c74b4e3b84`)

The following evidence records the earlier same-cell-only correction. Its remaining-work descriptions are historical, not the current implementation inventory.

### Status

This change does **not** complete the fin-d78-carriers task or close D78. It fixes native Provider retrieval between Node extensions in the same cell. Go, Rust and Python authoring/retrieval and cross-process Node carriers remain unimplemented. D78 remains `SCRUTINIZED:pending`. No exception, skip, retry, timeout increase, or fallback is added.

The design at `f50f294b62:docs/design/pi-extension-runner.md` was read without merging it. Sections 4.5 and S.2 keep real Provider objects and direct callbacks inside a shared Node cell. This correction adds no second registration protocol, evaluator, proxy that discards callbacks, or replacement Provider implementation.

## Upstream contract and source correction

All citations refer to Pi 0.87.1 under `.upstream/v0.87.1`.

- `packages/coding-agent/src/core/model-runtime.ts:744-750` retains the supplied native Provider in the runtime's shared map. `packages/coding-agent/src/core/model-registry.ts:101-103,165-167` returns that object from both getters.
- `packages/ai/src/models.ts:99-156` defines the complete Provider surface. `getModels` and `filterModels` return synchronously. Stream/fetch methods return an Event Stream immediately. Auth and refresh methods receive callbacks; they are not serializable metadata.
- `packages/ai/src/auth/types.ts:109-115,175-272` defines caller-supplied AuthContext, login interactions, API-key check/resolve, OAuth rotation, and toAuth.

PiG's `RuntimeModelRegistry.getRegisteredNativeProvider` searched only the requesting extension's `nativeProviders` map and threw D78 for every other owner, including members of its own Node cell. `cell.mjs` now supplies one cell-owned object table to its Runtime instances. Successful factory completion publishes the original object. Active native replacement changes that table. A failed factory never publishes. Connection failure/shutdown removes entries only if that Runtime still owns them, so an old owner cannot remove a replacement. Separate cells have separate tables. The host's existing per-extension registration and reverse callback maps remain separate.

This is an object-retention correction, not full registration linearization. The existing fire-and-forget host registration path and cross-process registry snapshots are not replaced with a synchronous authority bridge. In particular, D78 closure still requires coherent registration/removal and stale-handle tests through that bridge, not only the initial same-cell retrieval proved here.

## Red and green evidence

Raw evidence is retained under `tmp/fin-d78/` in this worktree. The archive `tmp/fin-d78-evidence.tar.gz` has SHA-256 `316064e68b2c9a6ed98bbf0a4b1acf8fe157850f5a5690becef6b8df9d33865a`.

- `TestNodeProviderObjectsFollowCellOwnership` fails before the production correction with `same-cell carrier missing` and actual `undefined`. It then passes identity, failed-factory rollback, native replacement, config replacement, explicit unregister, isolation of a separate table, shutdown, and connection-failure cleanup cases.
- `extensions-runtime/53-node-provider-object-carrier` fails on the unmodified binary: Pi writes the result artifact; PiG prints the D78 foreign-object error and produces no artifact. After the correction it passes all three declared Pi/PiG pairs. The artifact contains fixed JSON without paths, variable timestamps, or ANSI; no replacement normalization is configured.
- The fixture calls both registry getters and every Provider method, verifies reference identity via the shared Pi event bus, injects AuthContext and login/publication callbacks, verifies synchronous filters and immediate Event Streams, exercises thrown stream errors, and cancels an active stream. It covers API-key and OAuth methods, headers, refresh/update order, streamSimple, fetchDeferred and cancelDeferred.
- `TestNodeProviderObjectCarrier` drives the same fixtures through `Host.LoadAll` and the real command connection. It verifies values that a metadata-only carrier cannot produce.
- A compiling mutation removes the shared-table argument in the production `cell.mjs` constructor call. Both the conformance test and newly built paired scenario fail with the D78 error. Restoring the argument makes them pass. This conformance test was added after the fix and is mutation-proven, not originally red-proven.

Commands:

```sh
go test ./coding/extension/host/subprocess -run '^TestNodeProviderObjectsFollowCellOwnership$' -count=1
go test ./test/extension-conformance -run '^TestNodeProviderObjectCarrier$' -count=1
make parity-family FAMILY=extensions-runtime
```

The complete `extensions-runtime` family passes at declared durability with the qualified tmux 3.7b, including the existing registry/session and native host-callback scenarios. The initial family runs mistakenly put all of `~/flakes3-tools` on PATH; its `tmux` wrapper executes `/usr/bin/tmux` 3.4. One later run records a late `tmux extended-keys is off` warning inside scenario 25's crop. The qualified run exposes only that directory's `rg` and `fd`, uses the installed tmux 3.7b, and passes without assertion or timeout changes. Both the failed unqualified run and corrected run are retained. The touched subprocess and conformance packages pass with the maintained fixture exports. Linux `go vet ./...`, Windows vet on both touched Go packages, lint configuration verification, and `go tool golangci-lint run` on both packages pass.

## Locked npm corpus path

The unmodified `pi-btw@0.6.1` package is copied from the integrator's existing corpus installation into private homes. No npm resolution or package source edit occurs.

- Lock SHA-256: `43faa182eff083cffa2650270f60906ef81872ae1a405b85fa0caace376eb566`.
- npm integrity: `sha512-DFRoz+DyA6Wbyj3lQ5bAyPe80l6EGU8/zc9KcY7v1j6oXJd7D9hcWyFWImUoXKQKzGfxuX3dBW4Ef8uEj5NVlw==`.
- Replay: `python3 tmp/fin-d78/replay-corpus.py`.
- Inputs: the native carrier fixture, the real package's `extensions/btw.ts`, model `carrier-provider/carrier-model`, and `/btw carrier question` in a 170×55 terminal.
- Pi produces an independent side-thread answer `simple` and displays `1 exchange · idle` and `Ready for a follow-up. Hidden BTW thread updated.`.
- The compiling mutation prints the D78 foreign-object error.
- Corrected PiG gets past that error and reports `_piCodingAgent.ModelRuntime.create is not a function`. This is **not** a passing package result. The package calls the native getter at `extensions/btw.ts:327`, then the independently imported ModelRuntime at `:335`. Pi implements that factory at `packages/coding-agent/src/core/model-runtime.ts:173-219`; PiG still exports a `hostOnlyClass` for it in `runtime-node/shims/pi-coding-agent.mjs`.

The first replay observation predicate incorrectly expected `carrier answer`; Pi correctly selected streamSimple and answered `simple`. Its capture proves completion. The corrected predicate uses the package's completed-exchange state without increasing the time budget. Screens, escaped screens, stderr, command lines, locks, and binary digests are retained under `tmp/fin-d78/corpus/`.

## Resource disposition

Object getters add no IPC, serialization, tasks, or UI-loop work. The cell table retains one current original Provider per native registration; failed factories do not enter it, and owner cleanup removes retained entries. Captured objects remain ordinary JavaScript references. Cross-process handle cleanup and registration ordering remain required for D78, not proven by this map.

The existing representative `BenchmarkNativeProviderStream` exercises native auth and partial/terminal delivery through a real Node process. On this Linux amd64 Xeon 6746E run it measures 716042 ns/op, 34588 B/op and 357 allocations/op. CPU/allocation profiles are in `tmp/fin-d78/{cpu,mem}.pprof` and the text summaries. This is a host-stream cost observation, not a before/after speedup claim or a benchmark of the synchronous getter.

## Final gate results

- `make -k ci-contracts ci-drift` exits 2. `test-porting-release` reports the inherited 358 open hot-path obligations. `divergence-quality` rejects D77 and D78 because neither is approved. All other constituent checks pass, including SDK/interface drift, scenario lint, coverage drift, divergence guard, source hygiene and docs drift.
- `go test ./test/parity/...` fails only the existing three pending-divergence accounting assertions in `test/parity/closure`: `TestImportCurrentDenominatorsAccountsEveryLedgerRow`, `TestDivergenceDashboardMatchesCurrentHeadingsAndScrutiny`, and `TestFoundationDashboardAccountsCurrentDenominatorsWithoutCredit`. No assertion is weakened.
- `make lint` and `make lint-changed LINT_BASE=fdcf667708` pass. Bare `make lint-changed` cannot find a local `main` merge base in this supplied worktree; the explicit task base resolves that harness configuration issue.
- `go test ./...` passes the production packages and fails only the same three `test/parity/closure` assertions. The maintained fixture exports are applied; no timeout is changed.
- Race-enabled runs of both new tests pass.
- `make coverage RESULTS=tmp/fin-d78/coverage-results.json` regenerates the coverage files. That input preserves the integrator's prior results from `tmp/integrate-021/final-gates/parity-results.json` and replaces the new scenario's outcome with this lane's captured result. It does not claim that other families were freshly re-run here. The full extensions-runtime family was also re-run successfully here.

## Remaining release blockers

1. Implement the native Provider data/callback authoring API and `getProvider`/`getRegisteredNativeProvider` in Go, Rust and Python with the same callback semantics.
2. Implement connection/generation-owned cross-process Node proxies. Preserve synchronous filters, injected auth contexts, login interactions, publication callbacks, deferred methods, immediate Event Streams, cancellation, errors, replacement and cleanup. Returning registry auth or catalog snapshots for arbitrary Provider callbacks is not equivalent.
3. Extend conformance across all SDKs and isolated/packed/fused realizations. The new Node row alone is not that closure.
4. Fix the independent ModelRuntime/AgentSession import path before claiming the locked pi-btw package works. This correction deliberately does not replace those classes with another handwritten shim.
5. Keep D78 pending and preserve the release gate failure until the complete implementation and evidence exist.
