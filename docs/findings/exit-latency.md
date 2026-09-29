# Exit latency with 20 Packages

## Contract and source fix

Pi 0.87.1 stops terminal rendering, awaits every `session_shutdown` handler, disposes the Session, and exits. It does not impose a 200 ms shutdown deadline. See `packages/coding-agent/src/modes/interactive/interactive-mode.ts:4138–4177`, `core/agent-session-runtime.ts:406–412`, `core/extensions/runner.ts:988–1018`, and `core/agent-session.ts:1170–1193` in the pinned upstream tree.

PiG's `Host.Shutdown` removed extensions individually. Each provider removal published a newly projected and encoded catalog to sibling connections, even though those siblings were also shutting down. A 20-Package CPU profile attributed 1.65 seconds to `ForgetProviderRegistration → PublishModelCatalog` during teardown. The measured Host shutdown took 1.962 seconds.

`coding/extension/host/subprocess/host.go` now detaches every ending connection from the UI bridge before removing any provider. It uses the existing owner-scoped cleanup operation. It still sends transport shutdown barriers, cancels the owned processes, joins connection and process work, releases cache leases, and removes sockets. Individual removal, crash recovery, and reload retain live-sibling catalog publication. The change does not shorten any timeout, skip an awaited handler, change an SDK API, or add a background task.

The after profile measured Host shutdown at 284 ms, with no sampled catalog-rebuild CPU under `Host.Shutdown`. These diagnostic profile timings are individual observations, not the five-run performance comparison below.

## Matched whole-process measurements

Environment: Linux amd64, Intel Xeon 6746E, Go 1.27.1, Node 24.19.0, Pi 0.87.1. The npm executable reports 11.17.0, below the qualified documentation version. No toolchain was installed or replaced. Measurement processes use CPUs 32–39 and isolated HOME, agent, cache, and temporary directories. No provider credentials or paid model calls are used.

The driver opens a 120×40 PTY and starts an interactive Session with an initial `/latency-ready` extension command. The command writes a monotonic timestamp. The driver then sends Ctrl+D to the empty editor and drains terminal output until process exit. Readiness includes command dispatch, not merely a banner or the first editor frame. One separately recorded warmup precedes five rotated before/after/Pi trials. All 15 accepted trials exit with code zero. No failed sample is silently replaced.

A warm startup deadlock initially prevented the comparison. The captured goroutine dump exposed a registry/credential-store lock cycle before `session_start` completed. `perf-startup-corners` owns its fix: the credential transaction reads the operation's captured `credentials.RuntimeAPIKey`, rather than reacquiring the ModelRegistry lock through `r.RuntimeAPIKey`. Both PiG columns below include that identical one-line prerequisite through a diagnostic Go build overlay. Only the after column includes this slice's shutdown change. The failed attempts remain in the evidence bundle; they are not included as successful trials.

| Median of five runs | Before shutdown fix, with common lock prerequisite | After shutdown fix, with common lock prerequisite | Pi |
|---|---:|---:|---:|
| Interactive command-ready | 10,592.73 ms | 10,559.55 ms | 4,956.23 ms |
| Ctrl+D to exit | 4,764.84 ms | 3,165.06 ms | 2,131.02 ms |

The shutdown median improves by 1,599.78 ms, or 33.6%. No startup improvement is claimed. The remaining 1.03-second exit gap is not claimed closed: the after diagnostic trace spends time in awaited extension RPC and state traffic as well as process cleanup. This slice does not bypass those operations to reach an absolute target.

The same five runs retain the built-in startup checkpoints. Each cell below is the median of that phase's elapsed time, so the cells need not sum to the overall median. The extension and `session_start` phases dominate. The startup owners receive the raw traces and CPU/allocation profiles; no duplicate startup optimization lands here.

| Startup phase | Before | After |
|---|---:|---:|
| Services, SDK staging, discovery | 27 ms | 29 ms |
| Extension build, launch, registration | 4,456 ms | 4,475 ms |
| Model, prompt, Session construction | 59 ms | 61 ms |
| TUI, theme, extension wiring | 509 ms | 515 ms |
| session_start and resource discovery | 5,507 ms | 5,485 ms |
| Final rendering and editor readiness | 0 ms | 1 ms |

The reported WSL 3.2-second/0.2-second pair was a single observation with a different package snapshot. This replay resolves the same package names, not WSL's unavailable lock. In this snapshot, `@tian.zuo/pi-antigravity` itself takes approximately two seconds during shutdown in both Pi and PiG. The result therefore does not reproduce WSL's absolute Pi exit time.

The assigned base, `1beced15480cb8d46db113af9874d7316baf0b4f`, does not contain the prior Go `7c551a5fc` or Node `abb743382` startup changes. The startup lanes own their integration and new startup hotspots. Reimplementing their catalog, encoding, or runtime-cache optimizations here would duplicate their work.

## Deterministic guards and benchmark

`TestHostShutdownDetachesCatalogBeforeProviderRemoval` runs the real Host cleanup with zero, one, and twenty connection/provider registrations under `testing/synctest`. Each redundant catalog read sleeps one virtual second. Before the fix, the twenty-provider case performs nineteen reads and takes nineteen virtual seconds. After the fix, it performs no reads and advances no virtual time. It also requires empty extension, connection, and provider registries. A compiling baseline-source overlay reproduces the failure after implementation.

`TestHostShutdownDoesNotAwaitTransportCleanup` supplies a fake extension that reads the one-way transport shutdown barrier but blocks its own cleanup until the Host cancels it. The Host must deliver the barrier, cancel the owner, join the extension, and return without advancing virtual time. This transport guard passes before and after; it does not authorize skipping an awaited `session_shutdown` event. Existing mode shutdown guards and the canonical shutdown-order scenario retain that ordering requirement.

`BenchmarkHostShutdownProviderCatalog` uses 2,000 model records and zero, one, or twenty connections/providers. Setup and peer joins remain outside the measured interval. Results are medians of five ten-iteration samples with allocation reporting:

| Connections/providers | Before | After | Before allocations | After allocations |
|---|---:|---:|---:|---:|
| 0 | 0.742 µs | 0.672 µs | 0 B / 0 allocs | 0 B / 0 allocs |
| 1 | 97.03 µs | 94.81 µs | 6,263 B / 14 allocs | 3,628 B / 16 allocs |
| 20 | 148.39 ms | 0.860 ms | 44,138,480 B / 460,093 allocs | 46,242 B / 272 allocs |

The fake transports exercise teardown allocation and subscription ownership. The real 20-Package PTY comparison supplies process-boundary evidence. All recorded parent and extension PIDs are absent after the completed matrix. Full subprocess tests also cover process reaping, usage-lease release, socket ownership, reload, and fused/packed cleanup. CPU, allocation, and execution-trace profiles are retained; no peak-RSS reduction is claimed.

## Package snapshot

The original W3 names come from `staging/team/lead/inbox:WIN-NOTES.md:162`. Combining those nine with pi-vim and the eleven issue #71 names from `staging/team/win/w3-acceptance:native-proof/w3/A-packages.md`, then deduplicating pi-lens, gives the following set. The driver passes all twenty installed package directories through ordinary Package settings. Network checks and the selected packages' own update hooks are not disabled; the warnings in raw terminal output remain evidence, not filtered successes.

| Package | Version |
|---|---|
| pi-lens | 4.3.0 |
| @tian.zuo/pi-antigravity | 0.11.3 |
| pi-free | 2.8.4 |
| @observal/pi-insights | 1.2.3 |
| @specode/pi-subscription-usage | 1.1.1 |
| pi-open-agents | 0.1.22 |
| @benvargas/pi-claude-code-use | 2.2.1 |
| pi-secret-guard | 1.2.15 |
| pi-session-switch | 0.4.0 |
| pi-observational-memory | 3.1.4 |
| @tmustier/pi-usage-extension | 0.9.5 |
| pi-mcp-adapter | 3.1.0 |
| pi-web-access | 0.33.0 |
| pi-rtk-optimizer | 0.9.0 |
| pi-auto-update | 0.1.5 |
| @upstash/context7-pi | 0.1.2 |
| pi-hermes-memory | 0.9.9 |
| @dietrichgebert/ponytail | 4.10.0 |
| pi-msg-queue | 1.0.2 |
| pi-vim | 0.14.2 |

## Verification and limits

Pass:

- New regression tests, five repetitions; baseline-source mutation fails behaviorally.
- Full `go test ./coding/extension/host/subprocess` (379.872 seconds).
- Three race repetitions of Host shutdown, reaping, leases, Node recovery cancellation, catalog publication, and reload cleanup guards.
- Interactive shutdown/input/owned-work race guards and CLI shutdown/event-order tests, three repetitions.
- `go build ./...`, `go vet ./...`, and Windows cross-vet of the subprocess package.
- Linter configuration verification, `make lint-changed LINT_BASE=1beced154`, and full `make lint` with zero issues. The default local `main` has no merge base; the fixed assigned base supplies the changed-file denominator.
- `go fix -diff ./coding/extension/host/subprocess` produces no diff.
- `make divergence-guard`; no suppressions or baseline changes.
- Existing `extensions-runtime/15-footer-status-reload-composition`, `31-extension-runtime-surface`, and `33-shutdown-order`: three pairs each, unchanged comparators. Initial oracle setup could not resolve the mise wrapper's package; the successful run names the installed Pi 0.87.1 `dist/cli.js` explicitly and verifies its version.

`go test ./...` is not green. Its retained failures are the CLI thinking test's TempDir cleanup of a read-only downloaded Go module, three `test/parity/closure` denominator/scrutiny assertions, and the conformance package's ten-minute timeout during a 22-second Rust fixture build. None is counted as passing evidence or hidden with a larger timeout, retry, changed expectation, or skipped test. These remain repository-wide qualification blockers for the lead.

No exported signature, CLI flag, setting, parity scenario, or mirrored documentation changes. No generated inventory or coverage claim changes.

## Reproduction and artifact identity

Durable evidence is retained in the external `fix-exit-latency` bundle. `measure.py` contains the PTY driver and complete environment. `packages20.json` and `packages/package-lock.json` bind the fixture. `profile.py` creates instrumentation-only overlays. `before-lock-overlay.json` and `after-lock-overlay.json` bind the common startup prerequisite. `shutdown.patch` is this slice's production diff; `native-provider-lock.patch` belongs to `perf-startup-corners` and is not staged here.

```sh
python3 measure.py --packages 20 --builds before-lock,after-lock,pi --runs 5
go test ./coding/extension/host/subprocess -run '^TestHostShutdown' -count=5
go test ./coding/extension/host/subprocess -run '^$' -bench '^BenchmarkHostShutdownProviderCatalog$' -benchtime=10x -count=5 -benchmem
```

| Artifact | SHA-256 |
|---|---|
| pig-before-lock | `9ee8deedeff184c590c8c9bc311245de5f1ae4e7518cdcba88b24d08a4648f3e` |
| pig-after-lock | `066e1380bc9fd0357f9434269bc2d2b7456d32a67addfebd9babdfe387310605` |
| lock-native_provider.go | `8d4c51c5102776cd6961c6f30d0fd51586f61fcfe1fd839691b752eee68c8bea` |
| baseline-host.go | `0f94b5e0d87fc443615fcf076bbc6c3bc2893edef596ed81ea0cb8ec17b1fe58` |
| packages/package-lock.json | `db1c789c13e6ff53f5d0f6419c0a7440a067b830243bed6763c3872527febe37` |
| shutdown.patch | `14cec525fdc8e48c81fcad25c6ebd5aee9db488b761cab49186b248e2942f996` |
| native-provider-lock.patch | `5ac943427f7a693ffe282d51eb49efd839cd0e2b2aee52fa730a8188086d0e0d` |
