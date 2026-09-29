# Frozen 0.3.0 startup performance repair

The base is `c00a3e15d4ba27cfb75a5ad6811dfed521ad6c4b`, the frozen release candidate. The freeze contains the empty-scope guard from `84f9a9e01` and the concurrent source-build/cancellation changes from `bbc57177a`. It omits the selection-metadata projection and tests from `9a46e5b33`. This change restores only that missing production optimization and its guards. It does not overwrite the freeze's newer build-progress, legacy-SDK, registration, or protocol changes.

## Root cause and fix

`ModelRegistry.RuntimeModels` constructs a full request model for every generated catalog entry, then retains only provider, ID, name, reasoning, and headers. Those temporary models copy capabilities, compatibility options, input limits, thinking maps, and request-only metadata. Every startup pays that conversion cost before resolving its selected model.

Project selection metadata directly from unmodified built-ins. Retain the full composer for configured, dynamic, Radius, and native providers. Preserve provider/model order and independent header ownership. Invoke native catalog callbacks on every lookup, including after a mutation. The implementation adds no cache, background task, or persistent state. Model/auth selection and request payload construction remain unchanged.

`TestRuntimeModelsProjectsOnlySelectionMetadata` fails on the frozen source: 19,775 projection allocations versus 19,776 for the independently executed full-conversion reference. It passes after restoration and compares all generated selection values against that reference. `TestRuntimeModelsPreservesOverlaysAndLiveNativeCatalogs` covers file overrides, nil/empty/replacement dynamic catalogs, unregister restoration, live native callback reads, model replacement, and header ownership.

## Five-run smoke measurements

Use the frozen smoke lane's exact `timing.py` and `smoke.py`. Preserve its binaries for v0.2.0 and Pi 0.87.1, faux-provider fixture, explicit environment allowlist, flags, one excluded warm-up, five rotating-order samples, 120×40 tmux panes, command launch boundaries, 5 ms capture polling, and unchanged 1.10 ratio check. Redirect only the candidate binary, evidence root, and lane-scoped tmux names.

The answering-terminal control uses the same method and sets the pane's `window-style` to `bg=#000000` before the timed launch. Tmux then answers OSC 11. The successful warm-up persists `theme: dark` through the normal application path. The no-reply control leaves the terminal unchanged and never persists a theme. No timeout, comparator, application environment override, or production theme behavior changes.

| Method | Revision | Candidate print ms | v0.2.0 print ms | Print ratio | Candidate interactive ms | v0.2.0 interactive ms | Interactive ratio |
|---|---|---:|---:|---:|---:|---:|---:|
| Original smoke, no OSC reply | Frozen base | 68.910 | 62.525 | 1.1021 | 394.212 | 316.483 | 1.2456 |
| Original smoke, no OSC reply | Fixed | 64.128 | 58.563 | **1.0950** | 406.305 | 320.970 | **1.2659** |
| Same smoke method, OSC answered | Frozen base | 71.313 | 58.160 | 1.2261 | 242.270 | 258.816 | 0.9361 |
| Same smoke method, OSC answered | Fixed | 60.056 | 59.069 | **1.0167** | 237.189 | 252.090 | **0.9409** |

Pi's fixed-series print/interactive medians are 394.704/760.958 ms without a reply and 398.875/685.513 ms with a reply. These values do not waive regression versus PiG v0.2.0.

**Result: print passes both methods; answered-terminal interactive passes; original no-reply interactive remains FAIL.** Do not mark the original release smoke gate green. All samples remain visible. The base binary build completes before timing. The short test/fixed-binary build overlaps the beginning of the base timing invocation; measured ratios are same-run controls on a shared host, not dedicated-host latency guarantees.

## Why the original interactive gate still fails

Pi 0.87.1 `packages/coding-agent/src/modes/interactive/theme/theme-controller.ts:57-83` awaits terminal background detection with a 100 ms deadline. `interactive-mode.ts:954` awaits that operation before extension startup. PiG v0.2.0 did not implement this required query. The accepted `1aba738c6` fix restored it. Removing the query, proceeding before it settles, or persisting the unconfirmed fallback changes visible appearance and extension ordering.

Fresh-home startup traces locate the wait:

| Revision / terminal | Layout built | Before session_start | Interactive ready |
|---|---:|---:|---:|
| Base / unanswered | 26 ms | 132 ms | 138 ms |
| Base / answered | 25 ms | 32 ms | 38 ms |
| Fixed / unanswered | 20 ms | 131 ms | 138 ms |
| Fixed / answered | 21 ms | 29 ms | 34 ms |

The same trace moves model resolution from 16→22 ms on the base to 17→17 ms after the metadata fix. Startup traces start after Go package initialization; do not substitute them for whole-process timing.

A separate direct-PTY control removes shell/tmux launch overhead and answers the same OSC query. It is supplementary evidence, not a replacement comparator:

| Direct PTY condition | Base print/interactive ms | Fixed print/interactive ms | Fixed-series v0.2.0 ms | Fixed-series Pi ms |
|---|---:|---:|---:|---:|
| No OSC reply | 72.467 / 182.720 | 62.991 / 168.019 | 61.926 / 76.191 | 412.258 / 574.693 |
| OSC answered | 68.067 / 74.482 | 61.298 / 72.632 | 61.273 / 76.947 | 390.717 / 472.622 |

PiG's fixed direct-PTY no-reply penalty is approximately 95 ms; Pi's is approximately 102 ms. PiG is not waiting longer than Pi. The exact original smoke numbers differ from the smoke owner's earlier run because shell/tmux launch overhead and host contention vary. They are reported without normalization or sample rejection.

## Profiles and verification

`BenchmarkRuntimeModelsSelection` on the frozen source reports 3.47–3.67 ms, 2.23 MB, and approximately 19,782 allocations per catalog selection projection. The fixed source reports 0.57–0.63 ms, 0.80 MB, and approximately 1,916 allocations. CPU and allocation profiles accompany both. Complete before/after print and interactive process profiles and execution traces are retained for answered and unanswered terminals. Temporary selection allocations are released after the caller consumes them; no new retained catalog or worker lifetime exists.

Passed:

- Red→green projection guard and overlay/native tests.
- Focused CLI model selection, scope, registry/catalog, and native Model Runtime boundary tests.
- Race detector for both new `TestRuntimeModels` guards.
- Existing concurrent independent-build, admission-bound/cancellation, and same-artifact publication tests, confirming the freeze already contains that fix.
- `go vet ./cmd/pig ./internal/codingagent`.
- `go tool golangci-lint config verify` and `make lint-changed LINT_BASE=c00a3e15d`.
- Seven scenarios at all three declared pairs: headless scoped models; stock light/dark 256-color; model direct switch, thinking suffix, save default, and saved scope order. No comparator or coverage change.

Full `make lint` still fails the freeze's three unchecked `confirmRename` results in `internal/codingagent/session_selector_rename_cancel_test.go:83,84,190`, matching the pre-freeze report. No suppression or unrelated source repair is included. The time-bounded freeze repair does not certify the full repository or release gates.

## Reproduction artifacts

The lane retains `run_timing.py`, `direct_pty.py`, `profile.py`, all raw samples/PTYs/settings, binary hashes, CPU/allocation/execution profiles, benchmark logs, red/green logs, parity logs, and a machine-readable `summary.json` under the external `freeze-fix-perf` evidence directory. The coordinator handoff records its absolute path and the final commit identity.

Run each timing command with a fresh output directory and an explicit binary path. Never overwrite a failed series or alter a timeout to obtain a pass. The untouched frozen-smoke scripts remain the source for the primary method.
