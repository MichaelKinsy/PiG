# Node extension startup measurements

## Scope and result

This report measures PiG's Node loader changes in `679f5bdcc` against Pi 0.87.1 and the frozen performance baselines. It does not include the separate Go catalog, serialization, compression, or materialization changes.

The loader no longer imports unused presentation and schema graphs during a lightweight extension's startup. It retains Pi's complete virtual namespaces and exported identities when an extension imports them. Jiti's source-validated filesystem cache persists across temporary-directory changes and separates compiler/runtime identities. These changes do **not** meet the complete release startup targets by themselves. No-extension startup remains a Go-owned cost. Cold TS and package startup remain above the requested 760 ms and 980 ms targets. The combined Go/Node candidate requires its own measurement and qualification.

## Revisions and method

| Label | Source |
|---|---|
| A | `ecdd029f1cf8af7f4cca89aa1266abaf00620c76`, original 0.2.0 baseline |
| C-before | `e04490990bc7248e314aa83873dd895362dc4bbe`, original frozen compatibility candidate |
| Lane-before | `606c92e44`, supplied integration base before Node changes |
| C-after, Node only | `ca0e27955` plus the production Node changes committed in `679f5bdcc` |
| Pi | Real Pi 0.87.1 CLI from its pinned npm package |

The first two optimization comparisons use `606c92e44`. The final private-UI-import comparison uses matched before/after binaries on `ca0e27955`. The main table spans these experiments; use the paired optimization table for attribution rather than treating the moving integration baseline as a Node-only change.

All PiG binaries use Go 1.27.1 and `go build -p 6 -trimpath -ldflags='-s -w'`. Node is 24.19.0. Timing runs rotate build order serially on CPUs 8–15 of the shared Intel Xeon 6746E host. The Go lane does not run its timing matrix on those CPUs concurrently. Other diagnostic and verification jobs use different CPUs; the host is not exclusively reserved.

The original `perf-stack-check-evidence/measure.py` and copied fixtures remain unchanged. A separate driver calls its `run` function and adds the Pi CLI command adapter. Each workload/cache/build cell contains 25 measurements after three unmeasured warm launches. The three matrices contain 1,350 measured invocations: three workloads × two cache states × 25 runs × (five initial columns + two cache columns + two final columns). Every measured invocation passes the harness's unchanged output/error checks. There are no timing retries or discarded measurements.

Cold means a fresh HOME, agent/config/cache directories, and TMPDIR. It does not mean a dropped OS page cache. Warm means a fresh process with that workload's populated application caches. Environments are allowlisted and contain no worker auth or settings. Pi uses the original faux-provider extension to avoid real credentials and network requests; PiG uses its built-in faux provider. Pi therefore loads an additional provider extension. The Pi no-extension diagnostic is retained in the raw data but is not a like-for-like no-extension comparison.

Latency ends at the first JSON assistant `message_start`, not at an internal request-dispatch timestamp. This follows `packages/agent/src/agent-loop.ts:402-418` and `packages/coding-agent/src/modes/print-mode.ts:109-119`. Independent RPC profile captures confirm that the TS fixture's `alpha-entry` or package fixture's `widget-factory-probe` command actually registers.

## Final comparison

Values are median / p90 milliseconds, with 25 samples per cell.

| Workload | A | C-before | Lane-before | C-after, Node only | Pi 0.87.1 |
|---|---:|---:|---:|---:|---:|
| No extensions, warm | 100.75 / 115.97 | 116.69 / 136.90 | 157.44 / 174.59 | 160.25 / 177.74 | — |
| No extensions, cold | 116.08 / 138.04 | 131.99 / 149.98 | 180.68 / 201.48 | 175.13 / 186.11 | — |
| TS extension, warm | 503.81 / 522.52 | 932.29 / 953.84 | 968.74 / 1021.54 | 876.04 / 932.36 | 469.24 / 553.88 |
| TS extension, cold | 534.77 / 557.83 | 1264.59 / 1395.30 | 1308.85 / 1411.10 | 1211.55 / 1283.58 | 1025.74 / 1094.27 |
| Package, warm | 575.84 / 604.21 | 1051.76 / 1119.51 | 1124.80 / 1223.85 | 1058.02 / 1139.47 | 497.37 / 519.23 |
| Package, cold | 595.38 / 635.21 | 1391.50 / 1537.70 | 1470.73 / 1572.31 | 1476.61 / 1540.56 | 1067.80 / 1146.42 |

### Paired optimization measurements

Each arrow compares 25 runs of binaries differing only by that optimization on the same integration base. Values are median milliseconds; the evidence includes p90, maximum, and every sample.

| Optimization | Workload | Warm before → after | Cold before → after |
|---|---|---:|---:|
| Defer barrel, highlighting, and schema imports | TS | 968.74 → 884.12 | 1308.85 → 1263.22 |
| Defer barrel, highlighting, and schema imports | Package | 1124.80 → 1097.45 | 1470.73 → 1431.91 |
| Persistent, compiler-separated Jiti cache | TS | 928.47 → 932.44 | 1258.33 → 1276.94 |
| Persistent, compiler-separated Jiti cache | Package | 1065.50 → 1070.98 | 1428.93 → 1404.61 |
| Defer remaining private UI imports | TS | 909.76 → 876.04 | 1266.28 → 1211.55 |
| Defer remaining private UI imports | Package | 1067.70 → 1058.02 | 1482.53 → 1476.61 |

The cache relocation has no material timing improvement in the original fixed-TMPDIR workload: Jiti already enables filesystem caching by default. Its regression test proves reuse without loading Babel after TMPDIR changes. A fresh HOME remains a genuinely cold compilation. Package startup still imports the complete TUI/AI graph because the fixture requests those namespaces; no export is removed to improve its result.

### Binary contribution

A is 33.637 MiB. C-before is 58.051 MiB. Lane-before and the final Node-only artifact are both 58.383 MiB. These changes do not materially shrink the executable.

The final embedded runtime contains 24,984,883 payload bytes. A size-only build with the embed directive removed is 37,093,639 bytes, versus 61,219,079 bytes for the real artifact. The embedded runtime therefore contributes approximately **23.008 MiB, or 39.4%** of this executable. The size-only binary is not executed or shipped. Compression and shared materialization remain owned by the Go lane.

## Profiles and resource lifetime

Each profile group contains four fresh-process cold starts. Node's CPU and heap profilers run together, separately from this lane's timing measurements, on CPUs 56–63. Heap sampling uses a 32 KiB interval. These are sampled retained heap estimates, not peak RSS or exact total allocation counts.

| Diagnostic | Before | Final |
|---|---:|---:|
| TS `compileSourceTextModule`, sum across four starts | 211.182 ms | 24.432 ms |
| TS Jiti/Babel flat samples, sum across four starts | 519.205 ms | 524.582 ms |
| TS sampled retained heap, median | 17.466 MiB | 13.815 MiB |
| Package `compileSourceTextModule`, sum across four starts | 267.624 ms | 280.924 ms |
| Package sampled retained heap, median | 20.733 MiB | 20.597 MiB |

The intermediate profiles identify another approximately 15 ms per start in `pi-tui/utils.js`, reached through private widget/editor imports. Deferring those imports removes that unused path for the lightweight TS extension. The final package still requests it. Babel remains a cold cost; replacing Pi's compiler or native-import rules is not an acceptable performance shortcut.

No new task, socket, timer, or host/UI-loop work is introduced. Deferred imports execute synchronously in the existing Node subprocess when the caller needs the original implementation. Interactive `loadAllHighlightLanguages` retains Pi's Promise and scheduling behavior. Process shutdown owns the same module state as before. Profile-only exit instrumentation flushes V8 data after registration and is not shipped or used as shutdown qualification.

The transform cache holds Jiti's generated source artifacts, not evaluated factories. Jiti checks source contents on every transformed load. Loader content, Jiti and Node versions, and Jiti environment options separate cache namespaces. Cache files remain disposable under `<config-root>/cache/jiti`; they are not immutable cell artifacts and are not included in the existing cell-pruning commands.

## Behavioral proof

Pi 0.87.1 `packages/coding-agent/src/core/extensions/loader.ts:490-501` selects its virtual modules and disables evaluated-module caching. `virtual-modules.ts:14-38` shares the TUI, AI, coding-agent, and TypeBox namespaces. `packages/tui/src/keybindings.ts:311-320` shares one manager. `packages/coding-agent/src/modes/interactive/theme/theme.ts:1078-1097,1186-1205` defines the synchronous highlighting and fallback behavior retained here.

- `TestNodeStartupDefersPresentationDependencies` fails before the barrel/schema/highlighter deferral. Its strengthened form also fails before the private TUI-utility deferral. It passes with the final code and verifies factory reevaluation, complete TUI export values, alias identity, shared keybindings, and synchronous highlighting.
- `TestNodeJitiCachePersistsWithoutStaleSource` fails before the persistent cache exists. It proves a warm hit without loading Babel, changed entry and dependency bytes with preserved size/mtime, rejection and recovery from invalid source, compiler options, explicit cache disabling, loader/compiler identity changes, and fresh factories. The invalid-source assertion requires the import Promise itself to reject with the compiler error. A compiling Node-hook mutation that swallows import rejection passes the earlier process-exit-only assertion and fails this tightened guard.
- `extensions-runtime/56-node-lazy-imports` compares the entire output with real Pi, including the complete TUI export inventory, shared values/state, Text layout, ANSI highlighting, and unknown-language fallback. It passes three pairs. A Go-compiled embed-overlay mutation that returns unhighlighted code makes the scenario fail.
- Existing Node module-resolution, isolated/packed lifecycle, widget/editor, and complete vendored TUI upstream tests pass. `TestConformance_*` passes across the actual SDK transports.
- `TestVendoredPiDistMatchesThePinnedPackage`, `TestNodeSDKBundleRegeneratesExactly`, and `TestNodeVendorManifestIncludesPinnedNativeAssets` pass. The byte guard allows only the two explicit theme import relocations; it does not relax implementation-body comparison.

## Qualification limits

Linux/Windows vet and full repository lint pass. The broader qualification is not green. On `ca0e27955`, the extension family completes 77 scenarios: 76 pass and `29-export-tool-renderers` fails. Unchanged before controls reproduce its tool/event ordering differences. Earlier runs also expose nondeterministic PiG-only tmux warnings in renderer scenarios; a later pass does not close them.

A clean `git archive ca0e27955` reproduces `TestUpstreamRunnerRejectsToolWithoutParameterSchema` expecting stderr where the loader uses `FactoryLoadError`. Full conformance reaches the unchanged deadline in `TestPackedToolSchemaRejectionAcrossSDKs/go/startup`; its Python provider-producer cases also fail with `name 'cancel' is not defined`. The initial full-repository run also fails self-update, managed-tool, and Radius tests because the test harness globally forces offline mode. Removing that harness override makes all three affected packages pass against their local fake servers; those failures are not product blockers. `make ci-contracts` rejects pending/partial hot-path mappings; `make ci-drift` rejects D78 scrutiny. The integrator owns these recorded blockers. No tests, timeout, comparator, or divergence approval is weakened in this lane. The stable post-public-sync merge remains separate from these frozen measurements.

## Evidence

The retained evidence bundle is named `perf-node-startup-evidence/` and accompanies the performance handoff. It contains the unchanged measurement harness and fixtures, the separate matrix driver, all raw timing outputs and samples, summaries, CPU/heap profiles, binary hashes and compressed lane binaries, runtime identity, regression/mutation logs, before controls, and gate logs. The original A/C binaries also remain in `perf-stack-check-evidence/`.

The runtime content digest used by Go's `nodeRuntimeDigest` is `684a7a0971b1df5ef886bdaa84b9b8785abac67cf1aecab44471400aedab1ca5`. This identifies the Node files that the Go lane must use when regenerating its compressed runtime artifact.
