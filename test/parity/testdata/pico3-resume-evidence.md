# Pico3 resume and hold regression evidence

This is historical evidence from planning the unpublished 0.2.1 candidate, now named 0.3.0. The release-coordination failure below describes that earlier branch, not the current version pins.

## Contract and cause

Pi 0.87.1 is the oracle (`f07218c4d4bbc12bef056a7058c3dd49dfe41abe`). `packages/agent/src/harness/pico3/harness.ts:366-389` calls `reconcileOrphans` without awaiting it, but its synchronous prefix snapshots unknown live tasks before `resume` returns. Persistence and scheduler startup finish asynchronously. A later unregister/register gap must not change that snapshot.

PiG moved the snapshot into the resume goroutine. A delayed goroutine could see a temporarily unregistered task kind and terminalize its pending task as `orphaned`. Moving only the snapshot back into the caller preserves Pi's ordering. Persistence errors still go to `OnReport`, and `Suspend` still joins `resumeDone` before closing the scheduler and storage.

Two test assumptions also failed under CPU contention:

- `Hold` pauses dispatch, not a reservation already in progress (`packages/agent/src/harness/pico3/scheduler.ts:124-154`). An installed-Pi probe queued a reservation behind a blocked host commit, held dispatch, then released that commit. Pi reported `held running quiescent true` and completed the task after release. The three Go tests that need a durable pending baseline now acquire their hold in setup, before `Resume` can queue a reservation.
- Terminal state is published before invocation cleanup (`packages/agent/src/harness/pico3/scheduler.ts:218-232,297-302`), and `quiescent` tests the invocation map (`:480-481`). The replacement test now uses `WaitForTask` and `synctest.Wait` before asserting final quiescence.

PR #56 (`fd402a59c98a703b356e945a01c2e4db30b35feb`) fixes a similar test observation error: terminal/idle reads do not acknowledge post-line watch delivery (`packages/agent/src/harness/pico3/session.ts:1373-1381`). It does not fix the resume snapshot bug. Its watch-fold changes are not duplicated here.

The scan covered every `Hold`, `Quiescent`, and runtime kind unregister/register use in `agent/harness/pico3/*_test.go`. The sibling pending-baseline fixes are `TestHoldUsesReplacementKind` and `TestStorageJSONLPartialSidecarAppendIsUnpublished`. Other registration tests use task/provider gates or benefit from the shared `Resume` fix.

## Red and green

Host: Linux amd64, Go 1.27.1, Node 24.19.0. Baseline: `713707ba70`. Every repetition below uses the race detector.

| Baseline run of `TestSchedulerHoldReplacementRunsPendingRecord` | Runs | Failures |
|---|---:|---:|
| Default CPU configuration | 500 | 0 |
| `GOMAXPROCS=1` | 3,000 | 0 |
| `GOMAXPROCS=2` | 3,000 | 1 pending/running assertion |
| `GOMAXPROCS=8` | 3,000 | 0 |
| Two-core affinity, four busy loops, `GOMAXPROCS=1` | 5,000 | 0 |
| Two-core affinity, four busy loops, `GOMAXPROCS=2` | 5,000 | 3 orphaned outcomes, 15 premature quiescence assertions |

`TestResumeSnapshotsOrphansBeforeReturning` failed 100/100 baseline runs before the production edit. The final test covers known-at-resume, created-after-resume, and unknown-at-resume tasks. Registration after resume cannot rescue a task already selected for orphaning.

After the fix, both the reported test and the three-case resume regression passed 5,000 repetitions at each of `GOMAXPROCS=1`, `2`, and `8`, with two-core affinity and four busy loops: 15,000 clean repetitions per top-level test, including 45,000 resume case executions. Both sibling hold tests passed another 1,000 stressed repetitions each. The whole Pico3 package passed ten race-detector repetitions.

The stress recipe compiles once and constrains both the test process and its owned load processes to the same two available CPUs. Stop and join only those load PIDs after testing:

```bash
go test -race -c -o "$artifact_dir/pico3.test" ./agent/harness/pico3
# Run four `while :; do :; done` bash processes under the same taskset affinity.
for p in 1 2 8; do
  GOMAXPROCS=$p taskset -c "$cpus" "$artifact_dir/pico3.test" \
    -test.count=5000 \
    -test.run='^(TestSchedulerHoldReplacementRunsPendingRecord|TestResumeSnapshotsOrphansBeforeReturning)$'
done
```

No sleeps, retries, extended test deadlines, skips, or scheduler behavior changes are used to make these assertions pass.

## Paired oracle and mutation

`test/parity/scenarios/experimental-pico3/01-resume-orphan-snapshot.toml` compares exact output from the Go regression with the installed, version-checked Pi experimental Pico3 package. Pico3 is not reachable from Pi's normal CLI. The probe calls the published package API instead.

```text
PICO3_RESUME known pending completed replacement
PICO3_RESUME new pending completed replacement
PICO3_RESUME unknown terminal orphaned -
```

Commands:

```bash
node test/parity/testdata/pico3-resume-pi.mjs
make parity-family FAMILY=experimental-pico3
go test ./test/parity/...
```

All pass. The paired scenario runs five pairs. A compiling Go overlay restoring baseline `agent/harness/pico3/harness.go` makes all three resume cases fail: known/new become terminal instead of pending, and unknown stays pending instead of becoming terminal. The scenario fails on exit code and exact output. Removing the overlay restores five clean pairs.

## Resource lifetime and profile

`BenchmarkResumeHoldReplacement` exercises open, hold, resume, registration, task admission, terminal wait, and close. Each iteration joins startup and invocations and closes storage. The fix moves the existing live-task snapshot into the caller; it adds no background worker, queue, retained registry, or unbounded resource. Snapshot work remains proportional to live tasks.

Three baseline samples measured 261–278 microseconds/op, about 88.5 KB/op and 1,177–1,178 allocations/op. Three fixed samples measured 285–291 microseconds/op, about 88.6 KB/op and 1,177–1,178 allocations/op. These samples ran on a shared host and do not establish a speed change. A separate two-second CPU/allocation profile measured 269 microseconds/op. JSON cloning/number normalization and transaction construction dominate allocated space, not the moved snapshot.

```bash
go test -run '^$' -bench '^BenchmarkResumeHoldReplacement$' -benchmem -count=3 ./agent/harness/pico3
go test -run '^$' -bench '^BenchmarkResumeHoldReplacement$' -benchtime=2s \
  -cpuprofile="$artifact_dir/cpu.pprof" -memprofile="$artifact_dir/mem.pprof" \
  -o "$artifact_dir/profile.test" ./agent/harness/pico3
```

## Gates and release coordination

Passed: `go vet ./...`, `GOOS=windows go vet ./agent/harness/pico3/...`, `go tool golangci-lint config verify`, `go tool golangci-lint run ./agent/harness/pico3/...`, `make lint-changed LINT_BASE=public/main`, `make lint`, `make ci-contracts ci-drift`, `go test ./test/parity/...`, the paired family, and package race tests. `go fix -diff ./agent/harness/pico3/...` is empty. The Go interface inventory and coverage are regenerated with `go run ./test/parity/cmd/gointerfaces -out test/parity/interfaces/pig-go.json` and `make coverage RESULTS=`. The empty `RESULTS` avoids importing unrelated cached run claims.

The complete `go test ./...` run has one release-coordination failure: `TestParseChangelog_RealFile` requires the newest changelog entry to equal `coding.PigVersion`. This task requires a `0.2.1` changelog entry, but this branch still declares `0.2.0`. The release owner must align the version when integrating the 0.2.1 changes. This lane does not weaken the test or independently bump the release version. All other packages pass.

Two earlier commands exceeded the command harness's 420-second execution budget: a whole-package `-race -count=100` run and an initial `go test ./...` run. Neither is counted as passing evidence. No individual test deadline changed. The completed full run above exposes the changelog mismatch rather than hiding it.

GitHub log retrieval returned HTTP 403, so the reported CI failure is reproduced locally rather than attributed to an unread CI log. Raw local logs and profiles are retained under `tmp/flake-pico3-scheduler-hold/` in the working checkout, outside tracked source.
