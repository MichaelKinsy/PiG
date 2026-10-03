# cmdpig-timeout-100 progress

Slice: preflight-100 C7 (`cmd/pig` exceeds `go test`'s 10-minute package timeout on 4 CPUs) and C10 (test-fast SIGTERM at 1265 s). Base: `agg-100` `4f4c20c1b`.

## C7: root cause

`cmd/pig` is one test binary of 1047 top-level tests. Most start real `pig`, Pi or extension processes. One `go test` process gets 10 minutes for all of them. The runtime is the package's size, not a per-process slowdown.

Measurement: `go test -json -count=1 -timeout 60m ./cmd/pig`, 4 CPUs (`taskset`), runner-shaped environment (fresh `HOME`, `CI=1`, fixtures exported as `test-fixtures.sh` does), both trees side by side on separate idle CPU sets of the same host.

| Tree | Package wall | Top-level tests | Serial-test sum | Parallel-test sum |
|---|---:|---:|---:|---:|
| `52dc576ce` (the 0.4.0 port before the Pi 1.0.0 leap; GitHub test-cli job passed in 807 s) | 1354 s | 1036 | n/a | n/a |
| agg-100 `4f4c20c1b` (Pi 1.0.0, durable) | 1476 s | 1047 | 728 s | 2381 s (63 tests) |

- The base that GitHub passed also needs 1354 s on these CPUs. The local CPUs are loaded efficiency cores (preflight-100 calls a local duration an upper bound). On GitHub, test-cli job time already grew from 728 s (`084c775d9`) to 807-949 s on the 0.4.0 port pull requests, so the package ran close to its 10 minutes there too.
- agg-100 adds 121 s (9 %). Eleven new top-level tests carry most of it (`TestReplacedSession2860AcrossSDKs` alone is 64 s; it builds an extension per SDK). The rest of the per-test differences are within the noise of a shared host.
- Per-process cost did not grow. `pig --print` with the faux provider: 0.07 s (agg-100) vs 0.13 s (base). A cold Node extension start (`-e ext.mjs`, fresh `PIG_HOME` each run): 1.14-1.25 s vs 1.21-1.26 s.
- The long pole is `TestRPCBedrockConverseStreamObservation`: 12 cases × 200 runs = 2400 `pig` processes, 227-449 s on 4 CPUs. Its run count is its stability evidence; it is not reduced.
- When the package passes its deadline, `testbudget.Wait` gives the remaining tests `max(deadline - 10 s, 1 s)`, so the tests that run last report "RPC output did not close" before the timeout panic. That is the symptom, not a test defect.

## C7: fix

`cmd/pig` runs in 5 shards, each its own `go test` process under its own package timeout. The package timeout is unchanged.

- `automation/ci/test-shard-pattern.sh <shard> <shards> <package>` lists the package's tests (`go test -list .`) and prints the anchored `-run` pattern of the tests whose FNV-1a name hash selects that shard. The shards partition the package; a new test lands in one shard without a list edit. It fails closed when the listing fails, a shard is empty, the arguments are not a shard of a count, or the pattern would pass Windows' command-line limit.
- `automation/ci/test-grouped.sh`: `CLI_SHARDS=5`. Modes `cli`, `default` and `stress` run every shard in order, under a private scratch `TMPDIR` each, and continue past a failing shard so one run reports all of them. The other heavy packages keep their group.
- `.github/workflows/ci.yml` Windows `native`: the package step drops `cmd/pig`; five steps `Test cmd/pig, shard N of 5` run the same shards with the job's existing `go test -timeout 30m`.
- `test/ci-images`: `TestCLIShardsPartitionTheCLIPackage` (cli runs `CLI_SHARDS` `go test` processes whose patterns select each test exactly once; a failing shard does not stop later shards), `TestShardPatternFailsClosed`, `TestWindowsShardsTestEverySelectedPackage` (one Windows step per shard, the shard count equal to `CLI_SHARDS`, and `cmd/pig` dropped from the unsharded package step). The grouped-tests fixture's fake `go` now lists tests and records `-run` patterns.

Shard count: measured on 4 CPUs of the same host, each shard with the default 10-minute timeout.

| Shards | Shard wall times (s) | Worst |
|---|---|---:|
| 4 | 193, 436, 201, 566 | 566 (Bedrock and Azure observations hash together) |
| 5 | 273, 224, 187, 443, 232 | 443 |

Five shards keep the worst shard at 74 % of the budget on the slow local CPUs. For Windows (PowerShell preflight: the whole package needs 27.8 min) the worst shard is about 30 % of the package, about 9 minutes per step against `-timeout 30m`.

Mutation evidence (each mutation fails the named test, then restored):

- `cli` runs the whole package as before → `TestLinuxTestShardsPartitionPackages`, `TestCLIShardsPartitionTheCLIPackage` ("cli ran 0 go test processes, want 5 shards").
- `run_cli_shards` returns at the first failing shard → "cli ran 4 of 5 shards after a failing shard".
- Windows job lacks the last shard step → "native shard tests cmd/pig shards [1 2 3 4], want one step for each of [1 2 3 4 5]".
- `CLI_SHARDS` differs from the Windows steps' count → "cmd/pig shard step runs shard 1 of 5, want 6 shards as make test-cli runs".
- Windows package step still includes `cmd/pig` → "native shard tests cmd/pig whole as well as in its shards".
- No empty-shard guard → `TestShardPatternFailsClosed` (prints `^()$`).
- Shard index off by one → partition tests fail.
- Listing failure ignored → `TestShardPatternFailsClosed` (prints `^(TestPartial)$`).

## C10: root cause

The SIGTERM came from another lane's cleanup command, not from the repository or the runner.

- At 06:24:34Z the agg-100 lane ran `pkill -f "[a]gg-100/round.sh"; pkill -f "[a]gg-100/shard.sh"; pkill -f "[m]ake -k ci-"; sleep 3; pkill -f "[e]xtensioncorpus"; ...` to stop its own runs.
- test-grouped.sh runs `go test -p 12 <every fast package>`, and that argument list contains `github.com/MichaelKinsy/PiG/test/parity/cmd/extensioncorpus`. `pkill -f "[e]xtensioncorpus"` matched the preflight test-fast `go test` process and every other lane's `go test` of the fast package set.
- `go` does not handle SIGTERM, so it died at once. Bash printed `Terminated`, `run_group` returned 143, removed its scratch directory (the 13 s until 06:24:50, when the log was last written), and make reported `Error 143`; the job exited 2 (`FAIL-2` at 1265 s).
- Reproduced in an isolated session: the same run_group shape (`go test ... ./test/parity/cmd/extensioncorpus` under make) and `pkill -s <that session> -f "[e]xtensioncorpus"` print exactly `Terminated`, `make: *** [...: ci-test-fast] Error 143`, make exit 2.
- Action for operators: scope cleanup kills to the session or process group that started the work (`pkill -s`, `kill -- -<pgid>`), never `pkill -f` on a package or command name on a shared host. test-fast needs a rerun; it does not need a code change.

## Other finding (not fixed here)

`cmd/pig` `TestRPCAssistantContentCarriesOpenBlockIndex` failed in the full agg-100 run. It reads `event.Partial` after the Anthropic stream goroutine has moved on, so under CPU contention every event shows the final message (the thinking block's `index` is already removed at `content_block_stop`). It passes alone; it fails in each of four concurrent `-count=300` runs on 2 CPUs (`-race` reports no data race). Owner: the `ai` Event Stream / RPC partial snapshot; the fix belongs with that context, not with the CI schedule.

## Commands

- `go test ./test/ci-images -count=1`
- `make test-cli` on 4 CPUs, runner-shaped environment: PASS in 1639 s including fixtures and cache warming; shards 247, 230, 203, 485 and 261 s, each under its own 10-minute timeout.
- `go test ./test/docs-drift`, `go run ./test/parity/cmd/sourcehygiene -diff-base HEAD`, `golangci-lint run ./test/ci-images/...`.
- shellcheck 0.9.0 on `automation/ci/test-grouped.sh` and `automation/ci/test-shard-pattern.sh`.
