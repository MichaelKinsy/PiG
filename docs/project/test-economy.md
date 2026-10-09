# Test economy

PiG's suites are large: `make test` takes about 7,800 CPU-seconds and 31 minutes on a 196-core host, and `make ci-drift ci-contracts` about 1,400 CPU-seconds. Many agents and developers run tests on one host at the same time. This document defines which tier of tests to run when, and the rules that keep each run's cost proportional to the change. CI and the release preview still run every suite in full. Nothing here removes a test or lowers what CI proves.

## Tiers

| Tier | When | Commands | Cost |
|---|---|---|---|
| Inner loop | After each edit | `go test ./<pkg> -run '<Tests>'` for the code you edit | seconds |
| Change gate | Before each commit you hand off | `make test-changed`, `make lint-changed`, `go vet` and `GOOS=windows go vet` on the packages you touched | minutes |
| Contract gate | Once before you report a change ready | `make ci-drift ci-contracts`, plus `make parity-family FAMILY=<f>` for each parity family the change reaches | minutes |
| Full suites | CI, the integrator, and the release preview | `make test`, `make check`, `make verify`, `make parity`, `make vet`, `make lint`, `go test ./...` | tens of minutes |

`make test-changed` tests the packages a change can affect. `automation/ci/changed-packages.py` maps each file changed since the merge base with `TEST_BASE` (default `@{upstream}`, then `main`) to Go packages:

- the package whose directory holds a changed Go file;
- the package that embeds a changed file;
- the package whose directory, or a `testdata` directory under it, holds a changed file;
- a package whose Go source names the changed file's repository path, as one string or as the quoted components of a `filepath.Join` call. A file at the repository root (`Makefile`, `README.md`) also names files of other directories, so its bare name counts only as a relative path (`"../../Makefile"`) or a later `filepath.Join` argument (`root, "Makefile"`).

It then adds every package whose tests import one of those packages, directly or transitively (the reverse closure of `go list -test -deps`). The edited packages run under `-race`; the other affected packages run without it (`TEST_CHANGED_RACE=affected` or `none` changes this). A change to `go.mod`, `go.sum` or `go.work` affects every package. The script names packages that build only under a tag (the parity runner, the integration tier) and files that no package test reads; their own make targets and CI cover them.

`make test-changed` prints each package result, the output of failed tests, and the slowest tests and packages. The raw `go test -json` events stay in `tmp/test-changed/<time>/`.

## Rules for running tests

- Do not pass `-count=1` unless the test reads an input through a child process. The go test cache keys a result on the test binary, the environment variables the test read and the files the test process opened. It does not see what a child process reads: a script, a Node or Python oracle, a fixture build. A test that runs a repository script reads the script itself first (`repoScript` in `test/ci-images`), so an edit to the script reruns it. A test or build whose child process reads other files keeps `-count=1`; the make recipe says why (`docs-drift`, `standard-check`, the parity runner). A failure is never cached.
- CI runs every test. It restores the Go build cache, test results included, from earlier commits, so `test-grouped.sh`, `test-race.sh` and the Go test recipes pass `-count=1` when `CI` is set.
- Use `-race` for the packages you edited and for concurrency work, not for the whole module.
- Do not run `go test ./...` or `make test` to check one change. Use `make test-changed`.
- Bound fuzzing. `go test -fuzz` starts one worker per `GOMAXPROCS` by default, which on a 196-core host is 196 workers. Pass `-parallel` (at most 4 on a shared host) and `-fuzztime` (at most `2m`). Long fuzzing campaigns run on a dedicated machine.
- Build with `-trimpath` on a shared host. Without it every package's build-cache key contains its absolute directory, so each checkout recompiles the whole module: 280 CPU-seconds for `go build ./...`, 776 for `go vet ./...` and 921 for `GOOS=windows go vet ./...` in a fresh worktree. With `-trimpath` a second checkout of the same source links only.
- Set `GOCACHE` and `GOMODCACHE` explicitly. The go command derives both from `HOME`, so a test or script that replaces `HOME` would otherwise compile into an empty cache.
- Set `PIG_NODE_MODULES_STORE` to a host directory to share the locked Node trees (`extensions/sdk-ts`, the interface extractor, `durable/interop`) between checkouts. Each checkout otherwise holds its own 333 MB copy.
- `make port-lint` and `make interface-gaps` replay a stored passing run while no tracked, modified or untracked file has changed (`automation/ci/cached-gate.sh`). A failure always reruns. `PIG_GATE_CACHE=0` forces a run; CI always runs them.

## Rules for writing tests

- Find files from the package directory, not from `runtime.Caller`. `go test` runs every test binary in its package directory; `testenv.PackageDir` and `testenv.ModuleRoot` return it and the module root. Under `-trimpath`, `runtime.Caller` reports a module-relative import path, not a file system path.
- Start an expensive oracle (a Node or Pi process, a built binary, a type-checked module) once per package with `sync.Once` or `TestMain`, not once per case.
- Wait on a channel, a file, a process exit or a condition, never on `time.Sleep`. A sleep makes the test slow on an idle host and flaky on a busy one.
- Stop every process a test starts, on every path. Kill the process group in `t.Cleanup` when the process may start children or ignore signals. `make test` fails a group that leaves a process running under its scratch `TMPDIR` (`automation/ci/reap-test-processes.sh`) and kills it.
- Scope temporary files. A package whose tests start pig, build binaries or create temporary files outside `t.TempDir()` calls `testenv.RunScoped` or `testenv.ScopeTempDir` from `TestMain`. `make test` fails a group that leaves anything in its scratch `TMPDIR`.
- A test that runs the go command with a fresh `HOME` writes `go/telemetry/mode` containing `off` into that home's configuration directory. Otherwise the go command starts a detached telemetry child that writes into the directory after the test removed it.
- Keep CPU-heavy loops (fuzzing, mutation, stress repetition) out of the default run. Gate them behind an explicit flag or environment variable and run them in a dedicated job.

## Top costs measured

Measured on a shared 196-core, 1 TB development host at commit `54220fb62` on 2026-10-07, each command alone in a systemd user scope. CPU is the scope's total CPU time.

| Rank | Cost | CPU-seconds | Wall | Peak memory |
|---:|---|---:|---:|---:|
| 1 | `make test`, all groups (fast 2,773; `cmd/pig` 8 shards 2,908; conformance 1,136; subprocess 1,005) | 7,822 | 31 min | 6.6 GB |
| 2 | A fresh worktree's first build per configuration, without `-trimpath`: `go vet ./...` | 776-940 | 32-36 s | 6.6 GB |
| 3 | `GOOS=windows go vet ./...` in a fresh worktree | 921 | 35 s | 6.9 GB |
| 4 | `make ci-contracts` (of which `interface-gaps` 431-512) | 753-794 | 4 min | 6.6 GB |
| 5 | `make ci-drift` (of which `port-lint` 570) | 612 | 52 s | 7.0 GB |
| 6 | `go test -race -count=1 ./internal/codingagent ./tui` | 439-651 | 4.5-6 min | 2.7 GB |
| 7 | `make test` cache warm-up: four `go build ./...` passes that relink every command | 270 per run when warm | 20 s | 2.8 GB |
| 8 | `go build ./...` in a fresh worktree | 280 | 25 s | 2.8 GB |
| 9 | golangci-lint on two packages in a fresh worktree | 264 | 26 s | 2.4 GB |
| 10 | Leaked processes: test binaries, Node extension cells and tmux servers from failed runs, still running hours to days later | 10 to 15 processes at a time | - | - |

In three days of coding-agent sessions on that host, 78% of 5,653 `go test` invocations passed `-count=1`, 26% passed `-race`, and 365 ran `go test ./...`.
