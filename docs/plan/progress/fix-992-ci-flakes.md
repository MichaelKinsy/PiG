# fix-992-ci-flakes

Scope: public-CI flakes on PR #111 (run 36822376858) and two joint-run-6 conformance items. Base: staging `porter/pi-0.99.1`. No upstream 0.99.2 test is ported by this lane: every case is Pig-owned (the behaviors are Pig additions or Go concurrency, with the Pi counterpart cited per case).

## 1. packed-cell workspace hash racing the repository `tmp/` (FIXED)

Failure: `pig install --validate-only piglets/porter/extensions/pig-porter` failed with `hash Go workspace module <repo>: lstat <repo>/tmp/test-fixtures/ru...`. The Porter extension's workspace modules include the whole repository (`replace github.com/MichaelKinsy/PiG => ../../../..`). `goPackedCellHash` hashed that module with `hashTree`, which walks every file (docs, `tmp/`, test output) and `lstat`s each one. `tmp/test-fixtures/rust-target` is the cargo target directory of the fixture build (`Makefile:75`), written by other packages' tests while this one ran, so a listed file vanished before `d.Info()`.

Root cause: the key covered the module's whole directory tree, not the files `go build` reads. Fix (`coding/extension/host/runtimecell/hash_go_module.go`): `hashGoModule` hashes exactly a Go module's build source set: root `go.mod`/`go.sum`, the non-test `.go` and cgo/asm/header sources of every package directory, and the files its `//go:embed` patterns select. It excludes nested modules (a directory with its own `go.mod`), `testdata`, `vendor`, `.`- and `_`-prefixed directories (cmd/go's package-pattern rules), `_test.go` files and everything else. A directory that disappears between its parent's listing and its own read held no source and is skipped; an unreadable package file or directory still fails closed. Workspace modules use it (`go_packed.go`), and so does the Piglet build lock's local Go replacement digest (`coding/pigletbuild/records.go`), which hashed the same repository root with the same walk (`piglet validate piglets/porter/pig-porter.yaml` reaches it). The SDK root keeps `hashTree`: `TestPackedCellHashesTrackSymlinkedSDKChanges` pins that tree-wide contract and the SDK root has no concurrent writers.

Tests (all red on the stub commit, green after):
- `TestHashGoModuleCoversExactlyTheBuildSourceSet`: 24 paths, each changed (and removed) with the hash required to move only for source-set members.
- `TestHashGoModuleToleratesEntriesThatVanishDuringTraversal`: deterministic. A wrapper `fs.FS` lists a phantom file and directory that vanish before they are opened, in `tmp/test-fixtures`.
- `TestHashGoModuleFailsClosedOnUnreadablePackageFile`.
- `TestHashGoModuleIgnoresConcurrentChurnOutsidePackages`: on-disk module, 500 ms of create/delete under `tmp/test-fixtures/rust-target` while hashing.
- `TestGoPackedCellHashTracksWorkspaceModuleSourceSetOnly`: the real `goPackedCellHash` path.
- `TestBuildInputsLocalGoReplacementDigestIsItsGoSourceSet`: the lock path.

Mutation checks: tolerance for a vanished directory removed -> churn test fails with `readdirent tmp/test-fixtures/rust-target/release: no such file or directory` (the CI error class); `testdata` no longer excluded -> coverage test fails.

"Tests must not write into the repo tree": not changed. The fixture build's cargo target under `tmp/` is the documented `make test-fixtures` location (`test/ci-images/makefile_test.go` pins it), and the key no longer depends on it.

## 2. `TestPiSharedFilesConcurrentWriters` ELOCKED (NOT REPRODUCED; no code change)

Pi's synchronous stores (settings, trust) retry `lockfile.lockSync` 10 times 20 ms apart (`.upstream/v0.99.1/packages/coding-agent/src/core/settings-manager.ts:300-328`, `trust-manager.ts:137-164`, `auth-storage.ts:69-93`), a 200 ms window; its async stores (auth, models) retry for 30 s. `internal/pilock` ports the same budget and `stale` (10 s sync). Pi can throw ELOCKED only if PiG's writer holds a store's lock for the whole window.

Measured on the current code (instrumented `pilock.Release`, not committed): typical hold 0.1-1 ms; over 240 runs x ~130 holds, the maximum was 23 ms with 8 concurrent instances; 57 ms on one CPU with 16 burners. Nothing in a hold sleeps, syncs or waits: mkdir, probe `utimes`+`stat`, read-modify-write, join the heartbeat goroutine, `rmdir`. No leftover lock directory was observed after any run.

Reproduction attempts, all PASS (every run on the unmodified test):
- 320 runs (8 instances x 40) unloaded.
- `taskset -c 0-1` + 16 CPU burners x 22 runs; `taskset -c 0` + 16 burners x 3; + 100 burners x 2 (3 min each).
- `-race` build, `GOMAXPROCS=1`, `taskset -c 0`, 30 burners x 12.
- cgroup CPU quotas of 1.0, 0.3, 0.1 and 0.05 cores (quota verified to apply), 3 runs each.
- The whole `internal/codingagent` package in one process on 4 cores (the test took 17 s instead of 1.2 s and passed).

Conclusion: the budget is exhausted only when the PiG holder is descheduled or blocked on the file system for more than ~180 ms while holding a lock. That is a property of the shared runner's load, not a code path I can show; the pilock critical section already does the minimum Pi does. I did not weaken the test and did not add a retry. Open question QUESTION-1 (posted to the lead): the attempt-1 log (which store, which side) is needed to go further.

## 3. `TestExperimentalDurableServerCompositionRemoteB/server_runtime_replaces_an_exited_worker_on_the_next_attach` (FIXED at the source, e2e not reproduced)

Test: SIGKILL the worker, wait until the manager retires it (`workerPids` entry gone), attach the same session again from the same connection, expect a new PID.

Root cause: `SessionRouter.attachClientNow` returns early when the client's current attachment names the requested Session (`session-router.ts:162-163`). Upstream's termination handler (`#open`: `handle.terminated.then(invalidate)`, `session-router.ts:290-312`) runs in the same microtask drain as the termination, and its attachment release settles there too, so no later request sees the dead attachment. In Go the watcher and the release run on other goroutines. A re-attach arriving after the manager removed the worker but before the release cleared `r.attachments[client]` returned success with no replacement worker, and the test then saw `replacementPID` absent. The sibling windows (acquire, RemoveSession, Close) were already closed by `applyTermination` / `invalidateTerminated` (the earlier main fix, #102); this was the remaining read.

Fix (`internal/experimental/routing/session_router.go`): `retireTerminatedAttachment` applies an already signalled termination to the client's current attachment and joins its release before the same-Session comparison. The release error stays the invalidation's to report (`session-router.ts:307-309`).

Red/green: `TestRouterReattachAfterSignalledTerminationReplacesTheRetiredAttachment` (`router_termination_test.go`), deterministic: the existing `retiredHandle` parks the watcher after the termination is signalled. Red: `OpenSession calls = 1, want 2` x3. Green: passes; `-race -count=24` under 8 burners on 4 cores pass. The end-to-end subtest did not fail in 24 + 10 runs under load (24 under 4-core burners in parallel x4, 10 with `GOMAXPROCS=1`), so the unit test is the proof.

## 4. conformance `TransportsMatch/subprocess-go` (NOT ROOT-CAUSED; QUESTION-2) and `NativeEventBusStress` cost (BOUNDED, lead option B)

`TransportsMatch/subprocess-go`: 25 runs of `{inproc-go,subprocess-go}` under 24 burners on 4 cores and a `-race` run all passed; no data race is reported. The failing recording diff from AGG r1 is needed to find the diverging field (QUESTION-2). No code changed.

`TestNativeEventBusStressKeepsEveryDeliveryAndBoundedState` (Pig-owned, D19; no upstream test). Measured cause: CPU-bound, linear in emits. Every emit makes 101 sequential `events.dispatch` round trips (`event_bus.go:dispatchEvent`, one request per listener), so 10000 emits are ~1M round trips per language; the Host (~190% CPU), each Node realm (~125%) and each Go runner (~50%) saturate 4 cores.

Per language, one subtest, on this loaded shared host:

| emits | go | python | rust (includes a ~58 s cargo build) |
|---:|---:|---:|---:|
| 10000 (before, measured) | 98 s | 133 s | not run alone; ~58 s build + ~100 s (extrapolated from 2000 emits = 81 s) |
| 2000 | 20 s | 29 s | 81 s |
| 1000 (after) | 11-19 s | 15-23 s | 68-70 s (the build is ~58 s of it) |
| 400 | 5 s | 8 s | 63 s |

Whole test after: 94-113 s, three runs. Lead's 4-core figure before: ~19 min.

Change (lead's option B): 1000 emits; the heap bound keeps its per-emit sensitivity (`busStressHeapPerEmit` = 64 MiB / 10000 = 6710 B per emit, so 6.7 MB at 1000 emits); 100 subscribers, four concurrent realms, the slow listener and the exactly-once count are unchanged. New direct proof of "keeps nothing per emit" (event-bus.ts:12-33): `Host.EventBusRetention` (`coding/extension/host/subprocess/event_bus_retention.go`, a product-neutral diagnostic of D19 state: channels, listeners, handlers, in-flight emissions, dispatch snapshots, xref realms/leases/holds/expected). The run records it before the first emit and, after the last delivery, requires exactly the pre-run state with no emission and no snapshot. The xref payload leases (250-500 after 1000 emits) are released by the owners' asynchronous unpin notifications, which drain in under a second, so the test polls until the ledger equals its pre-run state and fails with the stuck counts after 30 s. Red/green: the red commit (stub returns zero) fails with `registry before the run = {...}, want 101 listeners`; the first green run, before the drain wait, failed with `Leases:251/500`, which is how the asynchronous release was found. Mutation: dropping `delete(b.emissions, id)` in `settle` -> `Host retains {... Emissions:1 ...}`. `-count=4` of go and python and a `-race` run pass.

### Proposal A (post-0.4.0, not started): batched same-realm dispatch

`dispatchEvent` calls each listener of a snapshot in order, one request each. Consecutive listeners owned by the same realm (a plugin that subscribes to many channels, 25 per realm in the stress) could be sent in one `events.dispatch` request carrying the handler ids; the realm would call them in registration order and return per-listener errors, which `dispatchEvent` reports exactly as it does now. Order, snapshot semantics (a listener removed mid-emit is still called), nested emits and the synchronous-prefix rule are unchanged because the realm still runs each handler to the end of its synchronous prefix before the next. Expected gain: ~4x fewer round trips in the stress (25-listener runs) and for every real emit with consecutive same-realm listeners. Cost: the host plus all four SDK runtimes (node, go, rust, python) must implement the batch and a conformance row must fail when any one does not (AGENTS.md: a capability is unfinished until it lands in all). Measurements above (10 ms per 101-listener emit, CPU split) are the baseline.

## Gates

go build ./..., go vet and `GOOS=windows go vet` (runtimecell, pigletbuild, experimental/routing), gofmt, `go fix -diff`, golangci-lint (`--build-tags=integration,live,parity`, 0 issues), `go test -race` of runtimecell/pigletbuild(no race)/routing, `-race -count=24` of routing and the hash tests under 8 burners. `make generate` regenerates `test/parity/interfaces/pig-go.json` (the new exported `GoModuleSourceDigest`) and then stops at an existing release-policy error unrelated to this lane (`packages/ai/test/images-models.test.ts` designedOutCases).

Burners: every burner PID this lane started was recorded in a PID file under the temp directory and stopped with `kill <pid>`. Incident: before the host rule, one `pkill -f` of the burner pattern ran once and may have stopped other lanes' burners; reported to the lead.
