# Chord replica hydration flake evidence

## Contract and root cause

Pi 0.87.1 is the oracle (`f07218c4d4bbc12bef056a7058c3dd49dfe41abe`). `packages/chord/src/services/consumer.ts:464-474` starts a singleton subscription from `use`. `:501-519` waits for existing starts in `ready`; readiness does not initiate hydration. `:575-613` awaits the transport subscription, installs its snapshot, then activates buffered updates. `packages/chord/src/services/state.ts:135-158` delivers the current value as `hydrate` to a subscriber of an already hydrated replica.

The corresponding Pi test (`packages/chord/test/services.test.ts:190-195`) reads an undefined value immediately after `use`, on the same synchronous JavaScript stack. PiG's subscription runs on a separate goroutine. The test's `UseRemote -> State -> Value` sequence has no ordering edge that prevents that goroutine from installing the snapshot first. The assertion at baseline `internal/chord/runtime_test.go:202-203` incorrectly treats "Ready has not been called" as "the snapshot has not installed."

This is a test observation race, not a Go data race or a production snapshot-ordering defect. `internal/chord/binding.go:236-243` already reads the snapshot, installs it, and activates the subscription in that order. The test must control the subscription boundary rather than assume a Go goroutine behaves like a JavaScript continuation on the caller's stack. No production behavior, public API, divergence, or changelog entry changes.

## Deterministic regression

`TestSingletonReplicaHydratesThenReceivesOperationStream` now runs two explicit schedules through the real provider, endpoint, JSON-copy transport, binding, and typed replica:

- `snapshot-pending` blocks the transport's snapshot return with the existing `reviewPausedTransport`. It checks that the replica is cold, subscribes, and invokes `add` while installation is blocked. It checks that no delivery occurs, releases the snapshot, joins readiness, and checks the exact hydrate-then-update values and sequences.
- `snapshot-installed` releases the same barrier and joins readiness before observing the replica or subscribing. It checks that the replica is already hydrated and that subscription delivers the retained value before the next method update.

Both schedules retain the late-subscriber, method-error, member-kind, missing-member, disposal, and asynchronous-error assertions. The test no longer polls for deliveries: `Ready` joins snapshot installation and activation, so the recorded sequence must already be complete. Cleanup releases the barrier before joining binding disposal and disposes the endpoint and provider even if an assertion fails.

Before changing the assertion, inserting `Ready` immediately before the original `Value` check made the original test fail 100/100 race-detector repetitions. That forces the same legal completion-before-observation schedule as the CI failure without sleeps or scheduler guesses.

Two compiling Go overlays also prove the final regression:

| Mutation | Repetitions | Required failure |
|---|---:|---|
| Restore the original unconditional `if hydrated` assertion | 100 | `snapshot-installed` fails every time; `snapshot-pending` passes |
| Activate the subscription before installing its snapshot | 100 | `snapshot-pending` loses the buffered update and fails every time; `snapshot-installed` passes |

The second mutation also makes the paired Pi/Go probe fail. No mutation remains in tracked source.

## Stressed before and after

Host: Linux amd64, Go 1.27.1, Node 24.19.0. Baseline: `f4853d6926f9d077d7db300e8a424890256a3300` (`public/main`). Every repetition in this table uses `-race`.

| Configuration | Baseline repetitions | Baseline failures | Fixed repetitions | Fixed failures |
|---|---:|---:|---:|---:|
| Unrestricted affinity, GOMAXPROCS=1 | 2,000 | 0 | — | — |
| Unrestricted affinity, GOMAXPROCS=2 | 2,000 | 0 | — | — |
| Unrestricted affinity, GOMAXPROCS=8 | 2,000 | 0 | — | — |
| Two-core affinity, four busy loops, GOMAXPROCS=1 | 2,000 | 0 | 5,000 | 0 |
| Two-core affinity, four busy loops, GOMAXPROCS=2 | 2,000 | 43 | 5,000 | 0 |
| Two-core affinity, four busy loops, GOMAXPROCS=8 | 2,000 | 32 | 5,000 | 0 |

Every baseline failure is the reported premature-hydration assertion. The fixed matrix runs both schedules each time: 15,000 top-level repetitions and 30,000 subtest executions. Seven sibling tests also pass 1,000 stressed repetitions each at GOMAXPROCS=2. The whole Chord package passes ten race-detector repetitions.

Reproduction recipe (choose two CPUs from the process's allowed affinity; this host used `0,1`):

```bash
artifact_dir=tmp/flake-chord-replica
mkdir -p "$artifact_dir"
cpus=0,1
loads=()
for i in 1 2 3 4; do
  taskset -c "$cpus" bash -c 'while :; do :; done' &
  loads+=("$!")
done
trap 'kill "${loads[@]}"; wait "${loads[@]}" 2>/dev/null' EXIT
for p in 1 2 8; do
  GOMAXPROCS=$p taskset -c "$cpus" go test -race -json -count=2000 \
    -run '^TestSingletonReplicaHydratesThenReceivesOperationStream$' ./internal/chord \
    > "$artifact_dir/p$p.json" 2>&1
  printf 'GOMAXPROCS=%s exit=%s\n' "$p" "$?"
done
```

The fixed matrix uses `-count=5000`. Only the owned load PIDs are stopped and joined. The final processor run also overlaps the separate sibling stress run. No sleeps, retries, longer test timeouts, or skips are added.

## Sibling audit

The scan covers hydration/Value/Load assertions, subscription starts, readiness, and snapshot checks in all `internal/chord/*_test.go` files. No sibling has the same unguarded cold-replica assumption. The focused stressed set is:

- `TestReviewUnbindFencesSnapshotAlreadyBeingInstalled`: already uses a snapshot barrier and joins the old start before checking for stale hydration.
- `TestSingletonReplaceRehydratesAndWithdrawClears`: checks the cold value after synchronous withdrawal.
- `TestRebindAndDisposeCloseSubscriptionsExactlyOnce`: checks the cold value after rebind completes.
- `TestProviderSnapshotCoherenceAndActivationBuffering` and `TestUpstreamProviderDisposalBufferedWhileStarting`: use synchronous provider subscriptions, not asynchronous consumer starts.
- `TestUpstreamRetainedRemoteStateRevokedByDispose`: joins readiness and disposal before checking each state.
- `TestReplicaRejectsGapsAndUpdatesBeforeHydration`: drives the replica directly, without a background start.

Other replica and facet tests wait for `Ready`, a delivery, an observer channel, or host activation before asserting hydrated state. Their existing checks are unchanged.

## Paired Pi evidence

```bash
node parity/testdata/chord-replica-parity.mjs
```

The probe version-checks the installed Pi coding-agent and Chord packages, then executes the real Chord provider, endpoint, and binding with a gated JSON-copy test adapter. It runs the same pending/installed schedules and compares exact serialized delivery lines against the Go regression. Five consecutive pairs pass. The first delivery is `hydrate`, sequence 1, count 1, log `["before-subscribe"]`. The second is `update`, sequence 2, count 4, log `["before-subscribe","remote"]`.

This test-only fix changes no CLI-visible behavior. There is no canonical Chord scenario family: `PORT_MAP.md:22-41` deliberately excludes `packages/chord` from the four-package denominator. The scenario schema requires nonempty `covers`, and scenario lint requires each entry to be in PORT_MAP. The standalone paired probe provides repeatable API evidence without weakening those gates, enlarging package scope, or claiming an unrelated file. No new scenario or coverage percentage is claimed. `go test ./parity/...` passes.

## Resource lifetime and profile

No production resources or scheduling change. The test owns one barrier and subscription per subtest. Cleanup always releases and joins them. The existing `BenchmarkReviewJSONCopyIncrement` exercises the provider-to-replica path with a 64 KiB state payload and disposes the binding, endpoint, and provider. Three samples measure 589–615 microseconds/op, about 362.7 KB/op, and 116–117 allocations/op. A two-second CPU/allocation profile attributes most allocated space to JSON copies, decoded strings, and byte-buffer growth. These shared-host measurements establish no performance change.

```bash
go test -run '^$' -bench '^BenchmarkReviewJSONCopyIncrement$' -benchmem -count=3 ./internal/chord
go test -run '^$' -bench '^BenchmarkReviewJSONCopyIncrement$' -benchmem -benchtime=2s \
  -cpuprofile=tmp/flake-chord-replica/cpu.pprof -memprofile=tmp/flake-chord-replica/mem.pprof \
  -o tmp/flake-chord-replica/profile.test ./internal/chord
```

## Gates

Passed: `go test ./...`, `go vet ./...`, `GOOS=windows go vet ./internal/chord`, `go tool golangci-lint config verify`, `go tool golangci-lint run ./internal/chord`, `make lint-changed LINT_BASE=public/main`, `make lint`, `make ci-contracts ci-drift`, `go test -race -count=10 ./internal/chord`, `go test ./parity/...`, and the paired probe. `go fix -diff ./internal/chord` is empty. The contract and coverage inventories are already current; no regeneration is needed.

Raw stress JSON, deterministic-red and mutation logs, paired outputs, gate logs, and profiles are retained under ignored `tmp/flake-chord-replica/` in the working checkout, outside tracked source.
