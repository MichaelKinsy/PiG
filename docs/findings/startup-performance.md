# Startup and steady-state performance findings

## Status and reference

PiG is a Go implementation of [Pi](https://github.com/badlogic/pi-mono). This investigation uses the real Pi 0.87.1 CLI as the reference, not an emulated extension loader. PiG was created by Michael Kinsy and originally developed at Hewlett Packard Enterprise.

The integrated cold-start controls meet the updated median target of at most 1.15 times Pi. No-extension startup is faster than the frozen 0.2.0 baseline. **Warm package startup remains above the target.** The original absolute cold targets of 760 ms for TS and 980 ms for a package also remain unmet. Passing deterministic proxies or the existing eval budgets does not close these gaps.

The landed changes bundle the pinned AI/TUI implementations, preserve their shared identities, enable source-validated native bytecode caching at library import, and defer unused private Unicode initialization. They also fix a worker-exit race and avoid structured-cloning decoded worker envelopes. The complete current startup matrix is below. Node steady-state qualification remains incomplete.

## Method and identities

- Baseline A is the frozen 0.2.0 binary from `ecdd029f1cf8af7f4cca89aa1266abaf00620c76`.
- Historical candidate C is the frozen binary from `e04490990bc7248e314aa83873dd895362dc4bbe`.
- The previous integrated after control contains Go `7c551a5fc` and Node `93c710dd2`, merged at `a38c7775f`.
- The current Node comparison uses the same stable Go source on both sides: stable `fbcc3a7d5`, semantic option cleanup `4f0b755b2`, and shared-first-encoder `9e98282c7`. CI `cc5dd5e32` is merged at `a5a70dcad`. The before overlay uses the committed older Node runtime archive and digest, not an older Go binary.
- Builds use Go 1.27.1, Node 24.19.0, and `-buildvcs=false -trimpath -ldflags='-s -w'` for the matched integrated controls.

The original `perf-stack-check-evidence/measure.py` and fixtures are unchanged. Separate adapters add the real Pi CLI. Timing ends at the first JSON assistant `message_start`, followed by successful complete output and shutdown. RPC preflights prove that the fixture command registers. Pi loads the original faux-provider extension; PiG uses its native faux provider. Pi's faux import cost remains in the comparison.

Cold trials use fresh HOME, agent directories, application caches, and TMPDIR. They do not drop the OS page cache. Warm trials use populated application caches and a fresh process after three unmeasured seeding launches. Each accepted startup comparison uses 25 rotated trials per build, workload, and cache state on CPUs 8–15. Fifty interleaved no-extension controls isolate the Node-only effect. Other profiles use explicitly identified affinities and are not mixed into causal timing tables. The machine is shared. No timing retry, sample removal, timeout increase, output normalization, or notification removal supports the results.

## Root causes and fixes

| Cause | Introduction or amplification | Fix and evidence |
|---|---|---|
| Provider-filtered model results reserve capacity for the entire catalog | `c318b77163` contains the oversized `ai.ListModels` allocation. `74e303f540` adds `GetAllModelData` and multiplies it across providers; `6989fb38e` integrates the metadata composition into Session refresh. | `56f619be9` builds the provider index once and returns result-sized storage. Allocation guards fail on the original projection. |
| Runtime identity walks and reads every embedded Node file on each process start | `749215aeab` introduces content hashing at runtime. `b2aca1dc11` expands the closure to the independent Pi SDK Session graph. | Build-time digest in `56f619be9`; shared ZIP/Zstandard materialization in `20181ffe7`. Warm-hit guards require no archive open or decompression. |
| Catalog publication serializes and copies the same model data repeatedly | `828ce33067` introduces the full `ModelRegistryState`; later native-provider registration work `5db47e1d4` expands metadata. The binding publishes three distinct states. | `7c551a5fc` removes JSON cache-key serialization and the extra outer-envelope pass. `9e98282c7` shares the first model encoding with the full registry state. All three publications, getters, order, and errors remain. |
| Scalar compatibility cloning takes a JSON round trip | Existing model conversion becomes more expensive under repeated full-catalog projection. | `56f619be9` removes scalar round trips; `7c551a5fc` shares the clone with ModelInfo; `9e98282c7` removes duplicate intermediate struct allocations. Complex values retain serialization semantics. |
| A lightweight extension pays for unused SDK/presentation imports | Jiti integration in `749215aeab` eagerly resolves virtual namespaces. The full Pi widget graph is introduced in `7a6072864`; public integration `596a34ab6` adds a runtime import from the full TUI barrel. | `a3d351fbb` resolves virtual namespaces on demand. `679f5bdcc` removes remaining private barrel, highlighting, and schema edges while preserving complete namespaces and shared state. |
| Transform reuse depends on a temporary directory | Jiti already enables its filesystem cache; PiG does not initially provide a stable product cache root. | `679f5bdcc` uses `<config-root>/cache/jiti`, separated by loader bytes, Jiti/Node versions, and compiler options. Jiti still validates source contents and reevaluates factories. This improves reuse across TMPDIR changes, not the fixed-TMPDIR timing workload. |
| A package that really imports AI/TUI parses the full unbundled graph and initializes unused Unicode machinery | The faithful full-library closure adds real module parsing and initialization work. Removing exports is not an acceptable fix. | The current shared bundles retain pinned implementations, full exports, canonical stateful leaves, and original asset URLs. Private RGI regex compilation and segmenter construction move to first use. Public segmenter getters return real shared native instances. |
| Repeated incomplete-frame concatenation copies an expanding prefix | `bbe9095d8` introduces the IO worker and `Buffer.concat([buffer, chunk])`. | `93c710dd2` fills one header and one bounded body. The red proxy measures 36,562,689 copied bytes for 2,097,334 wire bytes. |
| Structured-cloning decoded envelopes repeats catalog graph serialization across the worker port | The IO worker in `bbe9095d8` decodes the socket frame, then posts the resulting object graph to the main thread. | Forward the validated original JSON text and decode it with the main thread's captured parser. Worker validation, heartbeat service, byte accounting, ordering, errors, and all three catalog publications remain unchanged. |
| Worker exit closes a transferred port before its queued messages are delivered | The IO worker's independent exit and MessagePort queues permit `finish()` to overtake parser errors or complete envelopes. | The current exit handler pumps pending messages before closing. Forced exit-first tests reproduce lost errors and lost ordinary envelopes, then verify error text, frame order, one close, and no replay. |

The generic bundler originally exposed two correctness defects during qualification. Esbuild splitting could lose `export *` names from external leaves; the generator now emits the exact TypeScript-checker-derived names. Raw coding-agent imports of AI compat could create a second core/catalog beside the bundled namespace; all index, compat, and providers/all consumers now use canonical entries. Full export, model-value, shared-state, and `ModelsError` identity tests reject these defects.

No new extension API, process, worker, timer, subscription, or TUI-loop operation is introduced. Private deferred computations stay synchronous on the existing Node subprocess thread. Factory import/execution remains ordered and awaited. Native bytecode stores code, not evaluated factories or snapshots; Node validates source bytes and VM compatibility before reuse. The runtime flushes when it processes the host's ready message rather than relying on normal Node exit. Existing cache configuration and disable options remain authoritative.

## Historical complete control

These are 25-run medians from the previous integrated control, not measurements of the newer source. The full median/p90 table and exact artifact identities are in [startup-phase-analysis.md](../performance/startup-phase-analysis.md).

| Workload | A, ms | C, ms | Integrated after, ms | Pi, ms | After / Pi |
|---|---:|---:|---:|---:|---:|
| No extensions, warm | 108.41 | 111.32 | 88.87 | — | — |
| No extensions, cold | 121.27 | 131.08 | 110.64 | — | — |
| TS, warm | 536.27 | 935.94 | 546.67 | 491.85 | 1.111 |
| TS, cold | 540.21 | 1294.23 | 984.73 | 1033.35 | 0.953 |
| Package, warm | 567.27 | 1041.34 | 748.02 | 487.92 | 1.533 |
| Package, cold | 601.88 | 1453.12 | 1232.31 | 1091.43 | 1.129 |

Package cold p90 is 1523.03 ms versus Pi's 1220.49 ms, or 1.248 times Pi. The median pass is not a tail-latency pass. The after executable is 46,600,455 bytes, SHA-256 `67b4e35a54a2ec1c4801fc3550294f76a726d5cab4254fc531d0ee2cbd5abd07`. Its compressed Node archive is 9,253,377 bytes, approximately 19.9% of the executable.

The apparent no-extension regression from approximately 117 to 160 ms crosses Go revisions. Fifty matched runs give warm/cold 162.85/175.15 ms before Node changes and 163.00/174.75 ms afterward. Node-only deltas are +0.09% and −0.23%. The combined Go fix reaches 87.61/102.94 ms. A recording, failing `node` executable is never invoked by the no-extension control, and no Node runtime is materialized.

## Complete current startup control

This matrix measures `abb743382`, including shared libraries and worker-exit draining from `d023e8b7a`, JSON-text forwarding from `abb743382`, and Go Session fix `4716afdfb` merged at `7825c3fa5`. All 550 invocations pass the unchanged output and shutdown checks. Each displayed cell contains 25 rotated trials on CPUs 8–15. Values are median / p90 milliseconds; p90 uses the same nearest-rank calculation as the earlier report.

| Workload | A | C | Current | Pi 0.87.1 | Current / Pi median |
|---|---:|---:|---:|---:|---:|
| No extensions, warm | 113.52 / 124.95 | 124.20 / 135.40 | 97.12 / 109.49 | — | — |
| No extensions, cold | 122.37 / 137.69 | 136.11 / 143.80 | 114.70 / 125.45 | — | — |
| TS, warm | 534.87 / 594.81 | 982.75 / 1068.63 | 522.19 / 597.73 | 489.55 / 545.18 | 1.067 |
| TS, cold | 544.45 / 580.22 | 1312.72 / 1416.13 | 997.29 / 1070.00 | 1084.14 / 1131.41 | 0.920 |
| Package, warm | 562.73 / 606.57 | 1043.12 / 1106.31 | 579.40 / 608.06 | 485.99 / 506.70 | 1.192 |
| Package, cold | 584.38 / 645.07 | 1393.23 / 1467.14 | 1071.62 / 1163.33 | 1053.96 / 1129.34 | 1.017 |

No-extension startup remains within the 1.05-times-A budget in both cache states. TS meets the 1.15-times-Pi median budget in both states. Package cold meets it, but **package warm misses by 20.52 ms**, calculated from unrounded medians. Its warm p90 is 1.200 times Pi. The original absolute cold targets remain unmet. This is not a complete startup-target pass.

The current executable is 46,858,503 bytes, SHA-256 `e6e31620337e19045ff24a483f7b5a4cb92297918fee9ab9dcfcdf937c4710f3`. The Node archive is 9,453,183 bytes, SHA-256 `c4ee60b06600e894b7a3cbf05217cc6395e5f158f5f361668cd6e2ab82eec5df`, approximately 20.2% of the executable and below the 12 MiB proxy ceiling. Raw `final-none` and `final-extensions` outputs, summary JSON, and the generated median/p90 table are retained in `perf-node-library-evidence/evidence/` beside the lane handoffs.

The Go owner's subsequent cache-hit comparison removes normalization copies and per-model ordering maps, compares eligible model data directly, and owns a snapshot only on a miss. Its 1000/2000-model cached lookup allocation counts become constant instead of 4755/9755. The full cached registry benchmark improves approximately 13.5 → 12.9 ms on CPUs 16–23, with allocation counts approximately 33.4k → 26.1k. Owner-reported whole-path controls use merged Go `a5007d39b` plus four cache-hit edits and the frozen Node `abb743382` archive: 550 valid samples, 25 per cell. No-extension warm/cold is 90.92/98.38 ms versus A 95.87/108.89; TS is 523.96/1066.02 ms versus Pi at 548.78/1185.86 ms; package is 625.11/1150.71 ms versus Pi at 528.84/1151.70 ms. Package warm still misses the threshold by approximately 16.95 ms (1.182 times Pi). The old/new encoding package controls are 620.60 → 625.11 ms warm and 1147.77 → 1150.71 ms cold. They support the allocation reduction but no package-startup latency improvement. Raw controls and profiles are retained in the Go lane's `perf-go-startup-resume/` evidence directory. These are a separate matched experiment, not interchangeable absolute timings with the table above.

## Phase attribution

The original stack-check reports runtime closures of 24/561/1727 files and 740080/4192160/24980788 bytes for A/B/C. Cold build-check medians grow from 3 to 46 to 139.5 ms. Warm TS Go CPU totals grow from 2.78 to 2.80 to 5.16 seconds over twelve starts; catalog publication accounts for 1.63 seconds in A and 2.47 seconds in C. These are the frozen original profiles, not observations of a moving integration tip.

Five cold diagnostic starts on CPUs 8–15 compare Go `20181ffe7` plus Node `679f5bdcc` with Pi. Pi performs its SDK/TUI graph during CLI bootstrap; PiG launches a separate runtime. Median spans do not add exactly, and some phases overlap.

| Phase | PiG TS, ms | Pi TS, ms | PiG package, ms | Pi package, ms |
|---|---:|---:|---:|---:|
| Bootstrap before internal clock | 42.95 | 500.27 | 34.81 | 491.20 |
| Services / SDK staging | 22 | 12 | 18 | 32 |
| Runtime materialization / verification | 306 | Not needed | 297 | Not needed |
| Pre-spawn preparation | 10 | Not needed | 9 | Not needed |
| Node bootstrap / runtime graph | 114.40 | Included above | 110.75 | Included above |
| Manifest / Runtime construction | 11.61 | Included below | 13.00 | Included below |
| Extension import, including Pi faux import | 207.52 | 449 | 404.16 | 455 |
| Factory | 0.12 | Approximately 1 | 0.17 | Approximately 1 |
| IO-worker startup / connect | 50.07 | Not needed | 53.60 | Not needed |
| Registration handshake | 4 | In-process | 3 | In-process |
| Post-registration to first assistant event | 338.64 | See next rows | 314.17 | See next rows |
| Pi non-extension runtime setup | Included above | 78 | Included above | 85 |
| Pi timing summary to first assistant event | Included above | 56.08 | Included above | 52.34 |
| Whole process | 1100.51 | 1111.34 | 1262.48 | 1130.47 |

The OS spawn call itself is below one millisecond. Node bootstrap, module work, and worker initialization dominate that region. Pi's target-only imports are 8/22 ms, but its faux import pays approximately 433–441 ms of first Jiti/Babel initialization; excluding that cost would be misleading.

After Go `7c551a5fc`, diagnostic post-registration spans improve from 338.64 to 255.51 ms for TS cold and 314.17 to 270.74 ms for package cold. Warm spans improve from 409.40 to 326.22 and 371.27 to 301.41 ms. All retain three notifications.

Go's later warm package profile on CPUs 8–15 measures 298.5 ms after handshake: model selection 12.5, print options 14.5, Runtime 0, Session 1, and three publications 265 ms. Publication medians are 70.5/126.5/61.5 ms. The first owned copy costs 23.5 ms, first catalog JSON 54.5 ms, and two provider-state passes total 28 ms. This is not repeated Runtime/Session construction.

The first-encoder change gives matched 25-run warm package 784.84 → 740.53 ms against Pi at 508.42 ms; TS 567.07 → 515.81 ms against Pi at 493.22 ms; no extensions 89.41 → 86.09 ms. Whole-binding benchmarks on CPUs 16–23 improve approximately 99.7 → 78.3 ms. Snapshot copy improves 7.17 → 6.11 ms and 2.73 → 1.47 MB. These allocation comparisons are independent of timing thresholds.

## Current Node library comparison

The stable-before column and library candidate use identical Go source, including the first-encoder fix. Values are 25-run medians on CPUs 8–15 from `candidate.jsonl`. The candidate includes shared AI/TUI bundles, late native bytecode caching, and private RGI deferral, but not the later segmenter deferral.

| Workload | Stable before, ms | Library candidate, ms | Pi, ms | Candidate / Pi |
|---|---:|---:|---:|---:|
| TS, warm | 549.44 | 538.60 | 524.47 | 1.027 |
| TS, cold | 1010.68 | 1002.78 | 1054.47 | 0.951 |
| Package, warm | 728.35 | 592.43 | 484.27 | 1.223 |
| Package, cold | 1294.71 | 1185.54 | 1144.10 | 1.036 |

A separate 25-run segmenter experiment records warm 634.52 → 638.24 ms against Pi at 543.05 ms and cold 1152.86 → 1127.45 ms against Pi at 1128.52 ms. Warm paired deltas contain substantial noise, including −139 and +363 ms. This does not establish a whole-start warm improvement. A separate constructor diagnostic on CPUs 56–63 records 12.35 ms spent constructing the unused grapheme segmenter before the change and no construction afterward; startup is 385.7/367.8 ms in that diagnostic. Different affinities and instrumentation are not combined with the acceptance matrix.

Four current warm main-thread profiles on CPUs 48–55 retain approximately 21.5 ms per start in `compileSourceTextModule`, 6.6 ms in Marked regex construction, 7.5 ms in the AI bundle's top-level evaluation, and 12.4 ms of GC samples. These samples identify remaining work; they are not a wall-clock decomposition or a target pass.

Rejected experiments remain in the evidence: early native caching alone saves warm time but adds approximately 53 ms cold cost on its original base; provider import deferral alone is immediately defeated by Pi's required compat snapshot; worker preparation overlap changes warm 791.93 → 791.31 ms and does not justify its ownership complexity. The original compat snapshot remains eager. The current bundles create more cold-budget headroom, so any renewed early-cache experiment must be compared separately rather than reusing the earlier result.

## Worker-channel follow-up

A matched 25-run comparison of `d023e8b7a` and JSON-text forwarding records package warm 631.04 → 605.87 ms against Pi at 519.34 ms, and cold 1160.66 → 1154.90 ms against Pi at 1121.98 ms. Warm remains 1.167 times Pi, so this alone does not close the target. The comparison precedes the Session merge and changes only the Node runtime archive.

Four warm CPU/allocation profiles per implementation on CPUs 48–55 use both the main and IO-worker profilers. Worker `post` samples fall from 12.55 to 1.60 ms per start. Main-thread work moves from native message deserialization into explicit JSON decoding: `(program)` samples fall from 35.95 to 18.61 ms, while `deliver` accounts for 17.00 ms afterward. Sampled retained main/worker heap shifts from 10.20/5.52 MB to 12.39/3.15 MB. These are sampled heaps and CPU observations, not peak RSS or a guaranteed memory reduction. The decoded result remains a fresh object graph. No cache or retained raw-frame collection is added.

`TestNodeProviderSocketFrameCopyIsLinear` now also rejects structured-cloned envelope objects, checks the original UTF-8 byte accounting, and proves a later extension replacement of `JSON.parse` cannot change transport decoding. It fails on the previous packet shape and passes with JSON text. Malformed-frame, synchronous-provider, dialog, cell-lifecycle, and forced exit-first guards pass afterward.

Two catalog-data experiments are rejected for warm startup. Keeping individual JSON modules external records warm 618.48 → 630.65 ms and cold 1154.55 → 1137.31 ms. Grouping the same JSON inputs into one native JSON module records warm 603.17 → 613.94 ms and cold 1152.55 → 1128.26 ms. Both preserve data in the probes, but neither establishes the needed warm gain; neither changes production.

A separate early-bytecode-cache probe on the bundled runtime records warm 627.74 → 601.07 ms against Pi at 512.40 ms and cold 1159.48 → 1169.99 ms against Pi at 1128.09 ms. A subsequent complete four-way comparison finds only 7.23 ms additional warm gain after JSON-text forwarding: baseline 628.85, JSON channel 605.37, channel plus early cache 598.14, and Pi at 501.16 ms. Cold values are 1226.05, 1171.96, 1225.42, and 1166.13 ms respectively. All four columns have 25 trials per cache state. The combination still misses the warm target and adds 53.46 ms cold cost over the channel-only implementation. It is rejected. Production retains library-import activation and its existing late-cache phase proxy.

## Warm TS print/RPC priority: integration gap, not a new Node regression

The later corner report uses frozen stable `1beced154` plus its ignore/credential fixes. That source does not include the landed Go startup work or Node `abb743382`. A new shared control holds the corner fixture, command, environment, readiness endpoint and CPU affinity constant. It runs 25 rotated warm trials after three seeds per build/mode on CPUs 80–87. All 300 measured processes and 36 seeding processes succeed. This is distinct from the CPUs 8–15 package matrix above.

The combined executable is Go `efeedff23` with the exact Node archive and generated digest from `abb743382`, SHA-256 `cf6cac7ca7490ec0932a7b4698b1b5533ed85d3e8b1b63776e25323203b47bc4`. The TS fixture only registers `corner-alpha`, SHA-256 `c4bce9a4a1dbd7da57e1d8d03d339937aab0cc7e665b4fe258cbb780a61b9974`. Pi additionally loads the corner fixture's original faux provider. Print readiness is exact `42` output; RPC requires successful `get_state`, then `get_commands` proves extension registration before EOF. Interactive measures the first model-footer frame, separately from initialization completion. All require successful exit.

| Warm TS mode | Frozen corner base, ms | Combined, ms | 0.2.0, ms | Pi 0.87.1, ms |
|---|---:|---:|---:|---:|
| Print | 549.96 | **291.49** | 314.34 | 357.85 |
| RPC | 562.92 | **292.21** | 318.99 | 348.58 |
| Interactive first frame | 384.37 | **203.74** | 210.84 | 379.67 |

The combined build meets the owner's warm TS print/RPC-at-or-below-Pi target without another production optimization. The required action is to integrate the already-landed Go and Node work together, including the generated runtime archive/digest and named deterministic proxies. Do not remove workers, publications, validation, exports, or diagnostics to optimize the obsolete control. The earlier warm-package miss remains a separate measured obligation.

### Node phase proof on the exact combined artifact

Five phase-only warm print observations and five separate Inspector CPU profiles use the same observer, fixture and combined binary on CPUs 48–55. These diagnostics are not substituted for the CPUs 80–87 acceptance control. The phase-only whole-process medians are 303.14 ms for PiG and 365.62 ms for Pi. Median Node spans are:

| Node span | ms |
|---|---:|
| Preload to runtime module graph ready | 59.605 |
| Runtime construction | 3.014 |
| Jiti entry import | 3.684 |
| Factory execution | 0.121 |
| IO-worker start/connect | 43.262 |
| Registration enqueue to ready receipt | 2.844 |
| Ready application and cache flush | 0.640 |

The Go internal trace puts spawn at 12 ms, handshake at 158–161 ms, and model resolution at 166 ms. Pi reports the target import at 1 ms and faux import at 20 ms; its shared graph is already loaded during CLI bootstrap. The Node CPU profiles, stopped while processing ready, average 7.852 ms in module compilation, 7.646 ms in internal-loader compilation, 7.020 ms in CJS wrapping, and 4.455 ms in GC per start. Inspector `post` itself costs 16.771 ms and is instrumentation, not a production optimization opportunity. Those profiles do not cover post-ready catalog decoding.

The Go owner's independent host diagnostic records exactly three error-free publications in every one of five print and five RPC starts. Their encoded catalog sizes are 1,164,231 / 1,169,785 / 1,169,785 bytes. Print registry-state work is 46.83 / 14.74 / 14.99 ms, and publication through outer encoding is 52.37 / 20.93 / 20.46 ms; RPC publication spans are 51.69 / 19.84 / 19.72 ms. Warm-only Go profiles, excluding all seeds, attribute approximately 90 ms and 41 MB per start to publication/frame/state work. The result preserves work that still has a measurable cost; it does not omit it.

Node logs observe all three registry updates applied in four of five phase runs, and two before host teardown in the fifth. That is not evidence of an omitted host publication or proof that every final notification has been consumed before print exits. Exact host-frame and binding-callback guards remain the authority. The measured speedup does not rely on a changed consumption barrier.

Shared acceptance evidence is owned by `perf-startup-corners` under `bin/perf-warm-headless/`. Node hooks, raw phase logs, profiles and identities are under `bin/perf-warm-headless-node/` and the durable `perf-node-library-evidence/warm-headless-node/`. The hooks are diagnostic only and no new runtime policy is shipped.

## Blocking proxies and advisory timing

`cc5dd5e32` adds `make startup-proxies`, the Linux required startup shard, and `.github/workflows/startup-performance.yml`. `automation/perf/startup-proxies.json` names every required test. The runner rejects missing or skipped names and verifies the compiler-derived production embed inventory contains the compressed archive, not a second raw runtime tree. The archive ceiling is 12 MiB, not a claim that any archive below that ceiling has acceptable startup time.

The Node group adds guards for deferred dependencies; Jiti invalidation and factory reevaluation; exact bundle regeneration and pinned-source comparison; full AI/TUI exports and shared identities; native cache source validation, disabling, and ready-time flush; unused private Unicode initialization; linear copying; malformed-frame rejection; and worker-exit drain ordering. Allocation and copy budgets derive from input ownership or a measured reference implementation, not a newly observed count.

Red and mutation evidence includes:

- eager presentation imports trip `TestNodeStartupDefersPresentationDependencies`;
- changed entry/dependency bytes with unchanged size/mtime force Jiti recompilation;
- raw AI compat imports and missing external-star exports break the library identity guard;
- eager RGI loading and removed native-cache flush each break their named guard;
- unused segmenter construction fails at library import before the deferral;
- all four forced exit-first malformed frames lose their error before the transport fix;
- a compiling exit-handler mutation makes the ordinary-frame test observe only `close`, instead of `first`, `last`, `close`.

The advisory job measures current, 0.2.0, and pinned Pi in 25 warm/cold trials per workload, retains raw output and identities, and reports budget misses without blocking CI. Invalid output, failed registration, missing oracle, and timeout remain visible failures. It does not replace steady-state evals or parity qualification.

## Steady-state findings

The existing `make evals` budgets pass on the stable merged source, but they miss a resumed-session regression relative to A. The Go lane's matched ten-run 2000-exchange control reports 258.4 ms before, 151.7 ms for A, and 467.1 ms for Pi. Its locally tested fixes reach 164.8 ms, a latency overhead of approximately 8.6 percent above A rather than 70.3 percent above A. Request bytes are unchanged. These are owner-reported results for Session fix `4716afdfb`, merged here at `7825c3fa5` after its full coding/codingagent, Session parity, race, startup-proxy, and native/Windows vet/lint checks passed.

| Cause | Introducer | Local fix and regression evidence |
|---|---|---|
| `Runtime.Open` reads history for cwd validation, then `NewSession` opens it again | `40845f8ae` | Reuse the validated opened Session. Deleting the file after validation exposes the second read before the fix. |
| `restoreSessionRuntimeState` projects every message solely to obtain model/thinking settings | `73eae43c7` | Use Pi's `getSessionContextSettings` behavior without full projection. Allocation guards cover public branches and poisoned headers. |
| `SetCacheReadPriceSource` reparses history even when no cache-read price is missing | `8c85993b33` | Return without reprocessing when there are no misses. The old binding makes 4013 allocations; miss callbacks remain tested. |

### Existing autocomplete round-trip benchmark

After the restart, the existing `BenchmarkNodeAutocompleteRoundTrip` compares the frozen pre-channel archive from `d023e8b7a` with the current archive on identical Go source. It exercises a real Node autocomplete provider, its callback into the wrapped provider, and a 32-item response. Ten alternating before/after pairs each run 1000 operations on CPUs 48–55. Independent medians are 0.651/0.735 ms per operation, but the median paired after-minus-before difference is −0.020 ms, with individual pair differences from −0.488 to +0.265 ms. Those noisy measurements do not establish a steady-state regression or a gain. Go-side allocation medians remain 157 allocations and approximately 13.0 KB per query; these are not Node heap allocation counts.

A separate same-process MessageChannel diagnostic isolates parse/clone cost without claiming complete-path latency. It validates identical decoded results for small calls, 32-item autocomplete responses, a 64 KiB text value, and a 2048-record graph. JSON-text forwarding is cheaper for the first two shapes and the graph, but adds approximately 16.6 microseconds for the large text shape. The tradeoff remains visible. No payload threshold, alternate transport schema, timing gate, or additional optimization is introduced from these results. Raw alternating benchmark logs and the diagnostic are in `bin/perf-node-resume/`.

### Headless custom-message binding

The first Node steady-state command probe exposed a parity bug, not a timing result. Pi 0.87.1 accepted a registered RPC slash command that called `pi.sendMessage` with `triggerTurn: false`; it persisted the idle custom message and emitted `message_start`, then `message_end`, before acknowledging the handled prompt (`agent-session.ts:1934-1993` and `rpc-mode.ts:394-414`). PiG acknowledged the command with no message events and logged `host call sendMessage failed: not_ready: agent session not initialized`.

The Session already existed. `cmd/pig/session_extension_actions.go` bound `SendUserMessage` but not `SendMessage`, and the shared headless bridge never installed the `sendMessage` callback. Warming up with a model turn would not repair the absent binding. The delegated fix uses the Session owner's existing frozen `SendMessage` API from `f30eefde6` and consumes the later settlement-ordering dependency at `38c83d691`. It adds no second Session implementation or task owner. Both the in-process action and subprocess callback resolve the current Session at the call site. Subprocess options preserve the `TriggerTurn` pointer and delivery mode.

The binding and immutable Session dependency are landed together in `c32024692`. `TestRPCExtensionCustomMessageBeforeFirstModelPrompt` originally fails at command completion with missing events and the `not_ready` diagnostic. It passes ten runs after binding. Additional real RPC tests preserve `nextTurn` priority over a true trigger and prove that a true trigger starts a Session-owned turn. `rpc/38-rpc-idle-custom-message` compares full JSON output and stderr against Pi for three pairs, aliasing only independent timestamps. A compiling mutation that removes the callback makes that pair fail on missing events and the original diagnostic. No timeout, output endpoint, or warmup is changed to hide the bug.

The dependency merge retains the Go startup fixes: Runtime reuses its opened Session only when no explicit SessionManager is supplied; context restoration retains the settings-only lookup plus the frozen API's explicit-model selection gate. Both sides' restoration regressions remain. Full agent/coding/internal and CLI tests, focused Session races, native vet, Windows touched-package vet, package lint, generation, startup proxies, the full Session parity family, and RPC28/29/35/36/38 at declared durability pass. The first cross-RPC attempt correctly rejected a binary made stale by generation; the passing rerun rebuilds it rather than bypassing freshness.

The newly unblocked command/custom-message workload runs 25 alternating fresh-process PiG/Pi pairs with 20 operations per shape, 4000 verified operations in total, on CPUs 48–55. Median latency to the expected custom `message_end` is 2.163/0.379 ms for small details, 13.943/0.687 ms for a 64 KiB Unicode text value, 22.189/0.556 ms for a 256-record graph, and 25.400/0.238 ms for local Text rendering. The shapes execute in that order in each process, so later shapes include the preceding Session history; this is not a pure renderer comparison. Every operation also requires the command response and exact content/details/display. First-call effects are retained. The original A binary cannot supply this measurement's message-event endpoint, so no fabricated A latency is reported.

A separate Go CPU/allocation profile attributes 0.95 of 1.79 sampled CPU seconds and 143.91 MB of 471.61 MB sampled allocation space to `Session.refreshProjectedContext` beneath `appendCustomMessage`, largely reparsing prior entries. Startup materialization is also in that profile and is not attributed to steady state. This identifies a Session projection follow-up rather than proving the new Node transport is responsible. The Session owner retains that optimization; the binding fix does not introduce an incremental projection cache or change history semantics. Raw operation rows, complete outputs, and profiles are under `bin/perf-node-steady/` and `bin/perf-node-resume/`.

Node steady-state latency, streaming/rendering, tool dispatch, and long-lived memory attribution are not yet complete. Startup improvements and existing green eval budgets are not evidence for those paths.

## Evidence and qualification boundaries

The durable earlier bundles are `perf-node-startup-evidence/`, `perf-node-followup-evidence/`, and `perf-go-startup-evidence/` beside the lane handoffs. Current source, measurement adapters, unchanged fixtures, all raw matrices, CPU/heap profiles, rejected experiments, and regression logs are under the Node worktree's `bin/perf-warm-package/`. Go warm publication and eval evidence is under its `bin/perf-go-startup/evidence/`.

The current pinned-library comparison, complete vendored TUI upstream tests, bundle regeneration, cache/identity guards, all named startup proxies, full repository lint, full repository vet, and Windows subprocess vet pass. The worker-exit drain change passes ten targeted runs and its compiling mutation fails as expected. After JSON-text forwarding and the exact Session merge, the maintained `make test-subprocess` and `make test-conformance` targets pass their complete packages in 306.427 and 284.127 seconds respectively. These targets prebuild and reuse the required fixtures. No test subset or timeout changes are used. The broad final qualification remains incomplete. Earlier stable-base blockers include the malformed-tool error assertion, packed schema timeout, export-renderer ordering, pending hot-path mappings, and D78 scrutiny; those must be rechecked on the final merged source rather than assumed fixed or reported as new failures. The extension-family rerun fails only `29-export-tool-renderers`, with the existing assistant event content-index/order mismatch. A direct full conformance invocation without the maintained fixture-prebuilding step hits Go's default ten-minute package timeout while `TestPackedOrderedToolResults/rust` has run for 25 seconds. That failed invocation remains recorded; the subsequent complete maintained-runner result above supplies the passing evidence. `make ci-contracts` stops at porter validation. `make ci-drift` reports coverage generated from 475 scenarios where the current inventory contains 476. `make lint-changed` cannot find a merge base with local `main`; direct touched-package lint passes. These failures remain visible, and no limit or comparator is widened. Full conformance and the current startup matrix are complete as reported above; the warm-package budget, remaining integration/parity failures, and Node steady-state work remain explicit release obligations.
