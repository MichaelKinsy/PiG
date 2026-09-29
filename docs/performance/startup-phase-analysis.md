# Startup phase analysis

This follow-up separates the historical candidate from the same-base Node comparison. It then measures the combined Go/Node path instead of attributing Go costs to the Node loader. The final merged-source control meets the updated cold-start target: TS is 0.953× Pi and the package is 1.129× Pi. No-extension startup is faster than 0.2.0. The warm package gap remains visible below; this is not a claim that warm package startup is within 15% of Pi.

## No-extension control

The original 117 → 160 ms comparison crosses integration revisions. It is not a before/after measurement of the Node changes. Fifty interleaved runs per build and cache state confirm this distinction on CPUs 8–15:

| Build | Warm median / p90, ms | Cold median / p90, ms |
|---|---:|---:|
| 0.2.0 A (`ecdd029f1`) | 105.06 / 120.60 | 116.70 / 131.58 |
| Historical candidate C (`e04490990`) | 118.28 / 132.54 | 128.03 / 140.98 |
| `ca0e27955`, before Node changes | 162.85 / 177.48 | 175.15 / 200.07 |
| Same base, after Node changes | 163.00 / 178.92 | 174.75 / 191.29 |
| Go `20181ffe7` plus Node changes | 87.61 / 98.82 | 102.94 / 116.03 |

The same-base Node delta is +0.09% warm and −0.23% cold. The later Go fixes remove the historical Go startup amplification. The combined result is faster than A in both cache states. The two same-base control binaries use identical build flags, including `-buildvcs=false`; they differ in the Node changes, not in Go source revisions.

A separate cold probe places a recording, failing `node` executable first on PATH. No invocation is recorded, no Node runtime cache is materialized, and the faux request succeeds. The startup trace contains no extension lifecycle marks. The ordinary no-extension path does not launch Node.

## Phase instrumentation

The combined baseline contains Go `20181ffe7` and Node `679f5bdcc`. Pi is the real pinned 0.87.1 CLI. The original TS/package fixtures, measurement functions, and cache definitions remain unchanged. Cold means a fresh HOME, agent/config/cache roots, and TMPDIR, not a dropped OS page cache. Pi needs the original faux-provider extension in addition to the measured fixture; PiG has a native faux provider. The table explicitly includes that Pi extension cost.

PiG uses `PIG_STARTUP_TRACE`. A diagnostic-only Node load hook records module readiness, factory import/execution, socket-worker startup, and ready-state application against the parent's monotonic clock. Pi uses its own `PI_TIMING` groups from `packages/coding-agent/src/core/timings.ts:1-55` and the call sites in `main.ts:567-967` and `core/extensions/loader.ts:553-568`. Parent pipe timestamps account for bootstrap work before those internal clocks start. Neither hook ships in the runtime.

The table gives median cold spans from five diagnostic starts per workload/runtime on CPUs 8–15. The stages are operational comparisons, not matching function boundaries. Pi performs its SDK/TUI import graph during CLI bootstrap; PiG starts a separate Node runtime. Pi's runtime interval nests its extension timings. Median spans do not add exactly, and socket connection/handshake overlap by a small amount.

| Phase | PiG TS, ms | Pi TS, ms | PiG package, ms | Pi package, ms |
|---|---:|---:|---:|---:|
| Process bootstrap before internal startup clock | 42.95 | 500.27 | 34.81 | 491.20 |
| Services / SDK staging (Pi: other main setup) | 22 | 12 | 18 | 32 |
| Runtime materialization and verification | 306 | Not needed | 297 | Not needed |
| Pre-spawn preparation | 10 | Not needed | 9 | Not needed |
| Node bootstrap and runtime module graph | 114.40 | Included above | 110.75 | Included above |
| Node manifest and Runtime construction | 11.61 | Included in runtime | 13.00 | Included in runtime |
| Extension import (Pi includes faux-provider import) | 207.52 | 449 | 404.16 | 455 |
| Extension factory | 0.12 | Approximately 1 | 0.17 | Approximately 1 |
| Socket IO worker startup / connect | 50.07 | Not needed | 53.60 | Not needed |
| Registration handshake | 4 | In-process | 3 | In-process |
| Post-registration through first assistant event | 338.64 | See below | 314.17 | See below |
| Pi non-extension runtime setup | Included above | 78 | Included above | 85 |
| Pi timing-summary → first assistant event | Included above | 56.08 | Included above | 52.34 |
| **First assistant event, whole process** | **1100.51** | **1111.34** | **1262.48** | **1130.47** |

The OS spawn call itself takes less than one millisecond in these traces. The larger Node startup costs are interpreter/module initialization and the IO worker, not the `Start` call.

Pi's target extension imports take 8 ms for TS and 22 ms for the package in the cold trace; its faux-provider import pays the first Jiti/Babel initialization, approximately 433–441 ms. Comparing those target-only import spans with PiG's first import would omit a material part of Pi's measured path.

The largest remaining PiG-only regions are materialization/verification and post-registration catalog publication. Node receives an initial 104-byte registry update, then two complete registry updates of approximately 1.16 MB each. Applying each received model snapshot in Node takes roughly 1–2 ms on the diagnostic CPUs; waiting for it accounts for much more time. The Go lane's additional trace labels identify three preserved publications: binding `getModels`, binding `getModelRegistryState`, and final `PublishModelCatalog`. Its reference cold trace spends approximately 148 ms across those publications on CPUs 16–23. That trace is not numerically mixed into the CPUs 8–15 table.

## Node receiver copy amplification

Worker CPU profiles show repeated `Buffer.concat` calls while accumulating an incomplete frame. A real-worker regression records **36,562,689 copied bytes for 2,097,334 wire bytes**. The receiver now fills one four-byte header and one size-bounded body instead. JSON parsing, frame order, worker-owned heartbeat, unread-byte backpressure, socket errors, and callback ownership remain unchanged.

`TestNodeProviderSocketFrameCopyIsLinear` exercises the actual socket worker, a fragmented Unicode payload, and a heartbeat whose JSON properties are not in canonical order. `TestNodeFrameBufferPreservesEveryFragmentBoundary` covers every split position, byte-at-a-time delivery, empty bodies before/between/after ordinary frames, the exact limit, an oversized header, and callback failure. `TestNodeProviderSocketRejectsBadFrameBeforeNextEnvelope` verifies the actual worker's oversized, empty, invalid-JSON, and null-frame errors against Node's own parser and proves that a later envelope does not escape after failure. The copy guard fails on the original implementation and passes after the change. No complete body is exposed before all its bytes are filled.

Four cold package worker profiles contain 34.76 ms of `Buffer.concat` samples before the change and none afterward. These are CPU-profile samples across four starts, not a whole-process wall-clock decomposition. The corresponding 25-run interleaved timing comparison is:

| Workload | Combined baseline, warm / cold ms | Linear receiver, warm / cold ms | Pi, warm / cold ms |
|---|---:|---:|---:|
| TS extension | 628.59 / 1044.95 | 619.61 / 1035.19 | 499.30 / 1051.45 |
| Package | 826.93 / 1258.95 | 814.67 / 1243.76 | 493.98 / 1065.78 |

The receiver change saves approximately 9–15 ms. On this boundary the TS cold target is met, but package cold startup remains 1.167× Pi, approximately 18 ms above the 1.15× threshold. This is not a success claim for the final target. The Go publication optimization is evaluated separately without removing frames or changing callbacks.

## Final integrated control

The final executable contains Go `7c551a5fc` and the Node receiver/runtime from `93c710dd2`, merged in `a38c7775f`. It is rebuilt from this merged source after the signed-zero and cycle-error guards, not taken from the earlier overlay experiment. Build flags remain `-buildvcs=false -trimpath -ldflags='-s -w'` with Go 1.27.1. Each cell below contains 25 fresh-process measurements; the build order rotates within each workload/cache group on CPUs 8–15. All 550 invocations satisfy the unchanged output checks. Values are median / p90 milliseconds.

| Workload | 0.2.0 A | Candidate C | After | Pi 0.87.1 | After / Pi median |
|---|---:|---:|---:|---:|---:|
| No extensions, warm | 108.41 / 126.19 | 111.32 / 131.48 | 88.87 / 97.54 | — | — |
| No extensions, cold | 121.27 / 129.92 | 131.08 / 142.95 | 110.64 / 117.70 | — | — |
| TS extension, warm | 536.27 / 583.51 | 935.94 / 1025.79 | 546.67 / 597.58 | 491.85 / 578.31 | 1.111 |
| TS extension, cold | 540.21 / 580.38 | 1294.23 / 1356.08 | 984.73 / 1044.99 | 1033.35 / 1124.86 | **0.953** |
| Package, warm | 567.27 / 596.11 | 1041.34 / 1095.37 | 748.02 / 788.89 | 487.92 / 515.13 | 1.533 |
| Package, cold | 601.88 / 690.15 | 1453.12 / 1597.30 | 1232.31 / 1523.03 | 1091.43 / 1220.49 | **1.129** |

A and C retain the original `ecdd029f1` and `e04490990` binaries. The matched 50-run experiment above, not this historical table, isolates the effect of Node changes. The original absolute cold targets of 760/980 ms remain unmet. The updated relative cold **median** target is met without relabeling warm samples as cold or reusing application caches in a cold run. Package cold p90 remains 1523.03 ms versus Pi's 1220.49 ms (1.248×); the median result is not a claim that its tail is within 15%.

The final artifact is 46,600,455 bytes (44.442 MiB), SHA-256 `67b4e35a54a2ec1c4801fc3550294f76a726d5cab4254fc531d0ee2cbd5abd07`. The compressed Node archive is 9,253,377 bytes, approximately 19.9% of the executable. Its runtime content digest is `28969dce309436fc24a987c561308a99601e895ec82082e002336c2b33359eb7`.

### Largest PiG-only phase: catalog publication

The Go lane removes repeated JSON wrapping and key serialization, shares the scalar compatibility clone with `ModelInfo`, and compares an owned typed snapshot before reusing encoded metadata. The encoder keeps the existing queue, byte-size checks, closed-connection/error priority, and backpressure. It publishes all three snapshots in the same order. Exact-byte and opaque-callback guards cover this boundary. Review rejects naive float equality with a signed-zero regression and catches cyclic metadata before it can recurse indefinitely; cycles reach the standard JSON error, while acyclic shared subgraphs retain their previous independent-copy semantics. See the accompanying `perf-go-startup-evidence/` report and source tests.

Five additional diagnostic starts per workload/cache state use the same CPUs 8–15 and tracing hooks after the final merge:

| Post-registration → first assistant | Before, ms | After, ms | Registry notifications before / after |
|---|---:|---:|---:|
| TS cold | 338.64 | 255.51 | 3 / 3 |
| TS warm | 409.40 | 326.22 | 3 / 3 |
| Package cold | 314.17 | 270.74 | 3 / 3 |
| Package warm | 371.27 | 301.41 | 3 / 3 |

These diagnostic spans include work beyond the three Go publication functions and are not substituted for the uninstrumented timing matrix. Cold materialization remains approximately 294–301 ms in the final traces. The optimization attacks the measured post-registration cost; it does not move the cost into a hidden warmup, omit model data, or drop notifications.

## Evidence and qualification

The follow-up evidence bundle is `perf-node-followup-evidence/`. It retains all 50-run controls, 25-run receiver comparisons, raw Go/Pi timing output, parent and Node phase events, CPU/heap profiles, regression logs, and artifact identities. The initial CPUs 56–63 phase probes remain separate from the CPUs 8–15 comparisons. Pi's accepted CPU profiles use normal JSON-mode exit; exploratory RPC profiles terminated by SIGUSR2 are retained but are not used as lifecycle qualification.

Linux and Windows vet, full lint, the frame tests, Node provider/lifecycle tests, and `TestConformance_*` pass. The final merged extension-family run completes 77 scenarios: 76 pass and export29 retains its pre-existing tool/event ordering failure. `make ci-contracts` initially stops at generated recommendation drift from the Go merge; after local regeneration it reaches the existing pending/partial hot-path test obligations and fails there. The generated inventories are restored before committing, as required by the integration lane. `make ci-drift` stops at D78 scrutiny. Those integration gates are not reported as green. No timeout, comparator, cache-integrity check, or protocol shape is relaxed.
