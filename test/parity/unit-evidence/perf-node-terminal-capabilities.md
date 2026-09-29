# x-perf: CLI test time and EventBus startup ratio

Baseline: release staging `606595120`. Comparison: public main `f5c98329d`. Reference: Pi 0.87.1 (`.upstream/v0.87.1`). Host: shared Linux amd64, 128 CPUs, ext4 on NVMe, Go 1.27.1, Node 24.19.0 (also checked with Node 26.7.0), Rust 1.98.1, tmux 3.4 through a mise shim. Other lanes ran on the host during these measurements; the load average ranged from 5 to 80. Each run records the load where it matters.

## CLI package test time

`go test -json -count=1 ./cmd/pig/`, one run at a time, with the same toolchain environment and the same installed Pi comparator:

| Tree | Top-level tests | All test cases | Package time | Process wall | CPU user + sys |
|---|---:|---:|---:|---:|---:|
| main `f5c98329d` | 508 | 869 | 504.6 s, 461.5 s | 7:44 | 797.6 + 369.4 = 1,167 s |
| release `606595120` | 745 | 2,101 | 321.6 s, 318.4 s | 5:21 | 372.2 + 156.7 = 529 s |

On this host the release package takes less wall time and less than half the CPU of main. The 505 tests present in both trees sum to 152.7 s of top-level elapsed time on the release and 461.1 s on main; no shared test became slower by more than its run-to-run noise except `TestEmbeddedPackedAuthDiscoveryAndLoginByLanguage` (45.8 s against 36.2 s; it builds the Rust packed runner). The release has 2.4 times as many test cases. Of the added time, 157 top-level tests in files that launch the pinned Pi comparator account for 83.5 s of elapsed time, against 1.7 s for 76 such tests on main. The largest, `TestCLISelectsSessionBeforeNameValidation` (36.8 s), runs 48 serial subtests; each starts Pi's `dist/cli.js` (0.74 to 0.82 s wall, 1.0 to 1.1 s user CPU) and PiG (0.04 s). The growth is therefore more tests, mainly Pi-oracle tests, and not a slower product. The 600 to 775 s report did not reproduce on this host; oracle-heavy tests scale with the Node startup cost of the host that runs them.

The first release run in a lane environment without a configured `cargo` failed the Rust fixtures through the mise shim. Every number above uses `RUSTUP_HOME`, `CARGO_HOME` and the rustup proxies directly.

## EventBus startup

`06-eventbus-pubsub` was run with the parity runner (`-pig-parity.serial=true`, twelve alternating pairs unless stated), the pinned Pi 0.87.1 comparator and a freshly built PiG:

| Condition | PiG median | Pi median | Ratio |
|---|---:|---:|---:|
| Node 26.7.0 through mise, load 20 | 1,068 ms | 1,070 ms | 1.00 |
| Node 24.19.0, load 20 | 947 ms | 966 ms | 0.98 |
| Declared four runs, three repetitions, load 20 | 1,006 / 1,074 / 970 ms | 1,047 / 1,033 / 1,148 ms | 0.84 to 1.04 |
| Sixteen busy loops on eight pinned CPUs | 1,271 ms | 1,465 ms | 0.87 |
| Two pinned CPUs, declared runs | 1,283 / 742 ms | 1,168 / 887 ms | 1.10 / 0.84 |
| Single pairs (`runs=1`, as `parity-fast` and `parity-perf`), six repetitions | 862 to 1,239 ms | 956 to 1,060 ms | 0.81 to 1.18 |

The 2.15 to 2.87 ratio did not reproduce on this host under any of these conditions. The complete `extensions-runtime` family, including this scenario's performance assertion at its declared durability, passes with the fix.

A direct tmux harness that mirrors the scenario and `PIG_STARTUP_TRACE=1` attribute PiG's startup. Medians of eight fresh-home runs, load 20 to 40, in milliseconds from process start:

| Mark | main | release | release, prewarmed home |
|---|---:|---:|---:|
| `build-check-done` (Node runtime materialized) | 60 | 162 | 58 |
| `handshake-start` (Node reached `register`) | 348 | 388 | 288 |
| `handshake-done` | 352 | 444 | 364 |
| `tui-layout-built` | 412 | 488 | 370 |
| `interactive-ready` | 666 | 737 | 714 |

Four PiG-only costs appear on this path. None exists in Pi's single process.

1. **Duplicate terminal probe (fixed here).** The Node runtime constructor calls Pi's `initTheme()`, which reads `getCapabilities()`. Under tmux, Pi's `probeTmuxHyperlinks` runs `execSync("tmux display-message ...")` (terminal-image.ts:53-67). The host then probes again while it builds the handshake snapshot. Pi resolves capabilities once per process and its extensions share that cache (terminal-image.ts:34,160-169; main.ts:853,887). A Node CPU profile shows `spawnSync` at 70 ms of main-thread time inside the Runtime constructor. main did not call `initTheme()` in the constructor and had no probe.
2. **Cold runtime materialization (not changed).** A fresh `PIG_HOME` extracts 1,761 files (30 MiB) from the zstd archive and hard-links 1,732 of them into the launcher, against 636 files (5.6 MiB) copied on main. Extraction costs 80 to 110 ms wall with eight workers and about 200 ms on one CPU; the link pass costs about 30 ms, and a parallel link pass does not reduce it. The contents are the vendored Pi module graph; reducing them is a packaging decision outside this lane.
3. **Model catalog replication (not changed).** Interactive wiring (`wireSubprocessHostCallbacks`) sends three complete `model_registry_update` notifications of 1.17 MB each before `session_start`; the first omits registry state. Temporary marks measure 92 ms for the wiring on the main startup path, 118 ms CPU per run in `PublishModelCatalog`. `TestModelCatalogBinding*` pins these three publications and their bytes, so reducing them needs the owning lane's decision.
4. **Grammar loading order (not changed).** On `ready`, the Node runtime starts `loadAllHighlightLanguages()`, which blocks the child's event loop for about 130 ms before the host dispatches `session_start`. Pi starts it only after `rebindCurrentSession()` and the first `renderNow()` (interactive-mode.ts:1033,1054-1055). In this scenario the load finishes before `session_start` arrives, so it does not delay the measured line on this host.

## Fix

The host passes its resolved capabilities to each Node extension process in `PIG_TERMINAL_CAPABILITIES` (TerminalCapabilitiesPayload JSON) for isolated and packed spawns. An inherited value is always removed first. The runtime seeds pi-tui's cache with it before `initTheme()`, removes it from `process.env`, and applies later state snapshots through the same normalization. The Node process no longer probes the terminal, and an extension factory sees the host terminal's capabilities, as a Pi extension shares Pi's cache. PiG applies terminal settings overrides before it loads extensions (existing `cmd/pig/main.go:960` before `:1085`); a factory therefore sees override-applied values. Pi loads extension factories inside `createAgentSessionRuntime` (`main.ts:845`) before `setCapabilityOverrides` (`main.ts:853`), so a Pi factory sees override-applied values only when a startup TUI ran first (`cli/startup-ui.ts:84`: session picker, startup selector, first-time setup) and raw detection otherwise. With `terminal.images`, `terminal.trueColor` or `terminal.hyperlinks` set, a Node factory's `getCapabilities()` therefore differs from Pi's default path until the first state snapshot, which precedes every handler. Before this change the Node process detected its own capabilities and matched Pi's default path instead. This residual difference has no divergence number; see the rev-x-perf review. Go, Rust and Python SDKs are unchanged; they do not consume terminal capabilities.

## Red and green

- `TestNodeRuntimeSeedsTerminalCapabilitiesBeforeTheme` fails before the fix: the runtime probes the recording tmux stand-in and reports `{images:null,trueColor:false,hyperlinks:false}` instead of the seeded `{images:"kitty",trueColor:true,hyperlinks:true}`.
- `TestNodeRuntimeStartsWithHostTerminalCapabilities` fails before the fix through the production Host spawn: `factory-time capabilities = map[hyperlinks:false images:<nil> trueColor:false]`, and the stand-in logged `display-message -p #{client_termfeatures}`.
- Both pass after the fix, with `-race -count=3`.

## Before and after

Direct harness, eight runs per batch, alternating batches. Milliseconds:

| Load | Build | `handshake-start` → `handshake-done` | `tui-layout-built` | `interactive-ready` | Marker visible |
|---|---|---:|---:|---:|---:|
| 9 to 20 | before | 55 / 57 | 479 / 504 | 734 / 820 | 847 / 927 |
| 9 to 20 | after | 3 / 3 | 434 / 440 | 711 / 762 | 810 / 853 |
| 55 to 80 | before | 88 / 66 | 556 / 529 | 806 / 813 | 945 / 943 |
| 55 to 80 | after | 3 / 3 | 374 / 416 | 598 / 658 | 716 / 813 |

The host's single probe moves from the handshake to the spawn preparation, so `build-check-done` → `spawn-done` grows by about 50 ms; the Node process's own probe and its subprocess disappear. `strace -f -e execve` shows one `tmux display-message` per PiG start after the fix and two before.

Parity runner, twelve alternating pairs:

| Load | Before (PiG / Pi) | After (PiG / Pi) |
|---|---|---|
| 57 | 935 / 1,029 ms; 994 / 1,051 ms | 889 / 1,021 ms; 933 / 1,017 ms |
| 17 | 854 / 911 ms; 912 / 904 ms | 884 / 946 ms; 879 / 886 ms |

At low load the runner difference is within its run-to-run noise. The 2.0 limit, comparator, crop and runs are unchanged.

## Verification

- `go build ./...`; `go vet` and `GOOS=windows go vet` for the subprocess package and `cmd/pig`.
- Node runtime selection (`ci-node-runtime` pattern plus packed and snapshot tests): pass except `TestNodeVendoredTuiUpstreamTests/native-clipboard-linux`, which needs xcb development files and fails identically on the baseline.
- `go test ./test/extension-conformance/`: pass. `make parity-family FAMILY=extensions-runtime`: pass.
- `make lint`: pass after the goimports grouping in `owner_upgrade_test.go`, which failed on the baseline.
- `make test-porting-release`, `make divergence-guard`, `make source-hygiene`, `make sdk-surface-drift`, `make behavior-contracts`, `make custom-factory-ledger-drift`: pass. `make async-contracts` fails on the baseline and after the fix with the same `rpc/35-rpc-queue-prompt-await` evidence finding.
