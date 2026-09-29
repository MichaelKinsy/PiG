# Go startup performance measurements

## Scope and result

The earlier Go changes in `56f619be9`, `20181ffe7`, and `7c551a5fc` remove catalog allocation amplification, cache unchanged extension-facing catalog JSON, publish a compressed Node runtime through the existing immutable artifact cache, and remove repeated JSON work without dropping notifications. The earlier combined candidate meets the no-extension target and the updated cold-start target of at most 1.15 times Pi. It does not meet the original cold TS/package targets of 760 ms and 980 ms. These historical measurements do not qualify later integration changes.

## Stable-tip cache-hit recheck

The stable merge is `a5007d39b`, with parents `4716afdfb` and `1beced154`. The follow-up removes per-model ordering maps and temporary model normalization from unchanged catalog reads. It compares eligible model data directly and ignores only the runtime `Provider` field. It owns normalized data only on a cache miss. The same comparator preserves extension registration and definition order for both typed and projected models.

`TestCatalogCacheHitDoesNotAllocatePerModel` fails against the original encoding path: 64 scalar models require 260 allocations and 128 require 516. The corrected path has constant allocation count for those scalar catalogs. This is not constant memory or constant work: pointer lists and returned JSON bytes still scale with catalog size, and nested metadata still needs comparison. `TestCatalogCacheHitIgnoresOnlyRuntimeProvider` checks that a runtime provider change does not invalidate the projection or retain the provider, while a metadata provider change does invalidate it. Existing complete-publication tests retain all notifications, getter calls, model order, nil/empty states, signed zero, mutable values, opaque marshalers, and JSON errors.

Five-sample `BenchmarkExtensionCatalogEncoding` controls retain CPU and allocation profiles. The cached full registry path falls from approximately 33,400 to 26,100 allocations and from 12.4 MB to 11.4 MB allocated per operation. Concurrent compilation affects the initial after timing samples, so their elapsed time is not used as an optimization claim. Whole-process matched controls supply latency evidence. The bridge still owns one snapshot and one encoding. The change adds no goroutine, timer, lock, cache history, or extension lifetime.

The new complete matrix uses the maintained `automation/perf/startup_timing.py` trial and frozen fixtures. Each cell contains 25 rotated invocations. Both Go controls contain the same stable merge and exact Node `abb743382` archive overlay. The before control restores the original encoding path while holding the shared ordering helper fixed. The overlay is measurement-only; it is not a claim that the Node branch has merged. The branch's own archive is regenerated separately with `go generate ./coding/extension/host/subprocess`.

These trials run on CPUs 8–15 with Go 1.27.1 and Node 24.19.0. They use fresh processes, isolated temporary homes outside the checkout, three unmeasured warm seeding launches, and fresh application caches for cold launches. They do not clear OS caches. Unlike the historical harness, they contain no repository ancestor context. Untimed RPC preflights verify extension commands. Every timed invocation must produce the exact answer `42`, complete `agent_end`, and exit successfully. Pi 0.87.1 uses the same faux-provider extension as earlier controls. No timing sample is retried or removed.

All 550 timed invocations pass validation. Values below are median / p90 milliseconds; every cell contains 25 samples.

| Workload | Cache | Before | After | 0.2.0 | Pi |
|---|---|---:|---:|---:|---:|
| No extensions | Warm | 78.45 / 96.52 | 90.92 / 104.55 | 95.87 / 114.87 | — |
| No extensions | Cold | 100.67 / 113.98 | 98.38 / 116.26 | 108.89 / 117.43 | — |
| TS | Warm | 537.50 / 598.59 | 523.96 / 612.07 | 550.28 / 626.35 | 548.78 / 684.47 |
| TS | Cold | 1061.27 / 1223.00 | 1066.02 / 1370.82 | 583.41 / 688.32 | 1185.86 / 1263.17 |
| Package | Warm | 620.60 / 653.33 | 625.11 / 672.47 | 604.47 / 695.34 | 528.84 / 769.61 |
| Package | Cold | 1147.77 / 1197.26 | 1150.71 / 1324.16 | 605.09 / 653.92 | 1151.70 / 1226.87 |

No-extension medians meet the 1.05-times-0.2.0 budget. TS warm/cold ratios are 0.955/0.899 times Pi; package cold is 0.999 times Pi. **Package warm remains above the 1.15-times-Pi budget:** 1.182 times Pi, or 16.95 ms above the limit. The original absolute cold targets also remain unmet. This small allocation improvement does not establish a package startup latency improvement; before/after package medians differ by approximately 3–5 ms in the opposite direction. The no-extension warm control also varies despite not using the Node runtime. These measurements do not close the remaining warm-package performance obligation.

The after executable is 51,605,767 bytes, SHA-256 `cf6cac7ca7490ec0932a7b4698b1b5533ed85d3e8b1b63776e25323203b47bc4`. Its measurement-only Node archive is 9,453,183 bytes, SHA-256 `c4ee60b06600e894b7a3cbf05217cc6395e5f158f5f361668cd6e2ab82eec5df`. The branch-only executable is 51,405,063 bytes, SHA-256 `5baae54398634b441080dbe4b9d4cbb652efb7e775441211bfc5bef19066fd68`. Binary size changes since the earlier table span the stable integration merge and are not attributed to this cache-hit edit.

Pi `packages/coding-agent/src/core/model-runtime.ts:236-281` preserves provider composition order and the model snapshot; `296-315` refreshes availability separately. Those semantics remain unchanged. Native and Windows vet, full repository lint, focused catalog races, the named startup proxies, and the complete AI, extension, coding, and codingagent package tests pass. The startup and model-runtime-store-catalog families pass all 11 and 20 selected outcomes respectively against real Pi, with declared durability. The startup family includes its existing boot-only scenario; that outcome does not prove catalog behavior.

The stable merge changes `RegisterProvider` to return validation errors. Lint exposes three old test calls that discard those errors. Checking them makes the replacement fixture fail because it omits the required base URL, as Pi `provider-composer.ts:171,265` requires. The fixture now supplies that URL, checks every registration error, and asserts both cache reuse and invalidation. The corrected catalog tests pass. No lint suppression or comparator relaxation is added.

Final broad qualification is not green. `make generate` completes; only the generated Go inventory and recommendations differ beyond the intended runtime archive, and those integrator-owned ledgers are restored after checking. `make ci-contracts` then reaches the pending/partial hot-path test release policy. `make ci-drift` reaches D78's missing `SCRUTINIZED:approved`. The initial divergence-consistency failure comes from an ignored historical baseline tree inside the checkout; moving that tree intact into the external evidence bundle removes those obsolete markers without changing the gate.

`go test ./...` completes with passing production, CLI, subprocess, and cross-SDK conformance packages. It fails only in `test/parity/closure`: `TestImportCurrentDenominatorsAccountsEveryLedgerRow`, `TestDivergenceDashboardMatchesCurrentHeadingsAndScrutiny`, and `TestFoundationDashboardAccountsCurrentDenominatorsWithoutCredit` expect all divergence rows to be provisional, while the unapproved D78 row remains open. A fixed-tree control reproduces all three failures after generation stops. D78 approval remains with its owner; neither assertions nor the ledger are weakened here. The installed npm is 11.17.0 rather than the qualified 12.1.0; no tool is replaced for these measurements.

The matched matrix, profiles, raw outputs, overlays, fixture and binary identities, and gate logs are retained in the `perf-go-startup-resume` evidence bundle beside the lane handoff. The historical tables below remain separate controls.

## Historical source and method

| Label | Source |
|---|---|
| A | `ecdd029f1cf8af7f4cca89aa1266abaf00620c76`, original 0.2.0 baseline |
| Historical C | `e04490990bc7248e314aa83873dd895362dc4bbe`, original compatibility candidate |
| C-before | `606c92e44`, this lane's supplied base |
| Go after | Production changes in `20181ffe7`, on `ca0e27955` |
| Go + Node | The same Go code with the exact runtime tree from `679f5bdcc` |
| Pi | Real Pi 0.87.1 at `/tmp/ev78/pi` |

All binaries use Go 1.27.1 and `go build -p 6 -trimpath -ldflags='-s -w'`. Node is 24.19.0. Runs execute serially on CPUs 8–15 of the shared Intel Xeon 6746E host. The Node lane does not time workloads on those CPUs concurrently. Go diagnostics and gates use CPUs 16–23. This is not an exclusively reserved host.

The supplied `measure.py` and fixtures remain unchanged. A separate driver calls its original `run` function, rotates build order, and adapts the Pi command. Each timing cell has 25 runs after three unmeasured warm launches. The final A/C-before/after/Pi table spans the initial and final controlled batches. Use the paired optimization table for attribution. There are no timing retries or discarded timing samples.

Cold means a fresh HOME, PiG/Pi agent directory, application cache, and short TMPDIR. OS page caches remain populated. Warm means a new process with populated application caches. The measurement environment is allowlisted. No worker auth or settings enter it. Pi uses the original faux-provider extension; PiG uses its built-in faux provider. Pi therefore loads one additional provider extension. Both execute the same prompt and extension/package fixtures.

The workload cwd is empty and nested under the lane checkout, as in the original harness. Repository ancestor context is present. The evidence retains the base and generated `AGENTS.md` snapshots; coverage regeneration changes that generated context during development. These are not context-free startup measurements.

Latency ends at the first JSON assistant `message_start`. It is an upper bound on request dispatch, not an internal provider timestamp. This follows Pi `packages/agent/src/agent-loop.ts:402-418` and `packages/coding-agent/src/modes/print-mode.ts:109-119`. The complete process must also exit successfully and emit the expected agent-end event. Separate plain-print RSS trials require exactly `42\n`.

## First checkpoint comparison

Values are median / p90 milliseconds. Every displayed timing cell has 25 measurements.

| Workload | A | Historical C | C-before | Go after | Go + Node | Pi |
|---|---:|---:|---:|---:|---:|---:|
| No extensions, warm | 103.99 / 118.65 | 120.90 / 134.24 | 155.23 / 170.72 | 91.42 / 104.21 | 86.66 / 100.62 | — |
| No extensions, cold | 112.43 / 126.62 | 131.83 / 149.07 | 173.68 / 185.41 | 102.49 / 117.39 | 101.79 / 119.56 | — |
| TS extension, warm | 531.34 / 579.13 | 943.40 / 1014.23 | 978.77 / 1049.60 | 758.50 / 788.29 | 637.05 / 657.74 | 484.74 / 528.48 |
| TS extension, cold | 550.79 / 592.62 | 1332.03 / 1383.13 | 1352.36 / 1527.48 | 1208.32 / 1268.74 | 1084.78 / 1175.21 | 1106.41 / 1221.47 |
| Package, warm | 561.54 / 589.69 | 1033.06 / 1075.90 | 1065.89 / 1100.81 | 858.62 / 887.85 | 831.53 / 871.37 | 477.58 / 497.92 |
| Package, cold | 603.64 / 627.16 | 1427.54 / 1472.63 | 1449.87 / 1541.32 | 1314.15 / 1565.64 | 1312.11 / 1482.83 | 1070.21 / 1240.01 |

The Go-only no-extension medians are 12.1% below A when warm and 8.8% below A when cold. The Go-only TS and package warm medians improve by 22.5% and 19.4% against C-before. Cold improvements are smaller: 10.7% and 9.4%.

### Final matched publication controls

Node `93c710dd2` is fixed in both PiG columns. The before column uses Go `20181ffe7`; the after column includes the late changes in `7c551a5fc`. Values are median / p90 milliseconds from 25 runs per cell. Pi runs in the same rotated matrix.

| Workload | Before | After | Pi |
|---|---:|---:|---:|
| No extensions, warm | 93.40 / 100.76 | 91.60 / 99.14 | — |
| No extensions, cold | 107.85 / 122.66 | 104.20 / 117.23 | — |
| TS extension, warm | 604.63 / 642.73 | 536.57 / 570.34 | 471.28 / 510.29 |
| TS extension, cold | 1020.13 / 1070.42 | 969.53 / 1018.57 | 1067.27 / 1131.38 |
| Package, warm | 813.34 / 909.91 | 732.81 / 790.81 | 475.46 / 510.80 |
| Package, cold | 1235.01 / 1294.83 | 1188.46 / 1325.10 | 1066.96 / 1150.38 |

The final cycle-error guard lands after these timing samples. It changes invalid cyclic inputs, not these valid fixtures. The post-guard source and artifact are identified below. The Node lane runs the final controls after merging the complete source.

The Node lane's final independently run merged-source controls report 25 valid samples per cell: cold TS 984.73 ms versus 1033.35 ms for Pi (0.953 times Pi), and cold package 1232.31 ms versus 1091.43 ms for Pi (1.129 times Pi). Its no-extension warm/cold medians are 88.87/110.64 ms versus A 108.41/121.27 ms. The merged artifact SHA-256 is `67b4e35a54a2ec1c4801fc3550294f76a726d5cab4254fc531d0ee2cbd5abd07`. Five phase probes retain all three registry notifications and reduce package post-registration time from 314 ms to 271 ms. These results are owned by the Node lane's final report, not substituted into this lane's raw matrix.

### Paired optimization measurements

Each arrow uses 25 runs per side and cache state. The catalog/initial archive/scalar-copy comparisons use the frozen `606c92e44` base. The codec comparison uses matched `ca0e27955` binaries. The raw data retain p90, maximum, commands, cache state, and every output.

| Change | Workload | Warm before → after, ms | Cold before → after, ms |
|---|---|---:|---:|
| Exact-size provider index and catalog publication cache | No extensions | 155.23 → 101.45 | 173.68 → 122.26 |
| Exact-size provider index and catalog publication cache | TS | 978.77 → 856.65 | 1352.36 → 1234.10 |
| Exact-size provider index and catalog publication cache | Package | 1065.89 → 930.95 | 1449.87 → 1314.53 |
| Generated digest, shared materialization, initial Deflate archive | TS | 856.65 → 805.94 | 1234.10 → 1419.46 |
| Generated digest, shared materialization, initial Deflate archive | Package | 930.95 → 903.09 | 1314.53 → 1504.63 |
| Scalar compatibility copy without JSON round trip | No extensions | 108.12 → 91.82 | 124.82 → 105.85 |
| Scalar compatibility copy without JSON round trip | TS | 805.94 → 762.97 | 1419.46 → 1365.83 |
| Scalar compatibility copy without JSON round trip | Package | 903.09 → 846.71 | 1504.63 → 1442.94 |
| Zstandard instead of Deflate | TS | 729.32 → 716.11 | 1325.62 → 1159.85 |
| Zstandard instead of Deflate | Package | 879.70 → 877.29 | 1432.54 → 1299.85 |

Deflate is rejected as the final codec because its cold cost consumes the catalog gains. Its diagnostic cold TS profile attributes 1.56 CPU seconds across eight starts to inflation. The final archive uses standard ZIP Zstandard method 93 with pooled, single-threaded decoders inside the existing bounded extraction workers. Warm hits do not open the archive, so small warm codec differences are not attributed to decompression.

A reflection cache over already projected maps is also rejected. It increases the publication benchmark from about 42 ms to 46 ms and raises allocations. The first retained implementation hashes composed typed metadata before constructing the larger extension projection. Profiling the late print path then shows that constructing this hash still serializes unchanged data. The final implementation owns a typed metadata snapshot and compares its complete data, including floating-point bits, before reusing the encoded projection. It retains one snapshot and encoding per bridge binding, not a history of revisions. Opaque Go marshalers and fallback provider identities retain the original uncached evaluation path and call count.

### Late print-path attribution

Temporary trace and pprof-label overlays show that runtime construction takes less than one millisecond and session construction takes about one millisecond in the representative TS capture. The remaining work is three catalog publications: `SetHostAction(getModels)` takes 54 ms, `SetHostAction(getModelRegistryState)` takes 66 ms, and the final `PublishModelCatalog` takes 28 ms. No publication is removed.

The final changes share the scalar compatibility copy with `ModelInfo`, replace serialized cache keys with owned typed comparison, and enclose already-marshaled registry JSON directly in the notification envelope. The transport keeps the same terminal-error priority, frame-size check, queue, work accounting, and backpressure. Exact frame tests cover complete bytes, repeated notifications, ordering, escaping, and invalid state. A compiling copy mutation fails the queue-ownership guard. An initial two-allocation-difference assertion is rejected because race instrumentation makes that assertion unstable; it is not used as acceptance evidence.

Naive `reflect.DeepEqual` is rejected by a red signed-zero regression. The final comparator uses float32/float64 bits and explicit type, nil, length, and member checks. Canonical-data eligibility detects active-ancestor cycles before snapshotting and admits repeated acyclic subgraphs. Cycle review also exposes a pre-existing unbounded copy in `ModelInfo` sampling parameters. The corrected clone preserves cycles for the standard JSON encoder to reject, while completed shared subgraphs retain the previous independent-copy behavior. A bounded-stack test child proves the pre-fix failure; no production depth cap or format version is added.

## Binary, allocation, and lifetime

C-before is 61,219,079 bytes (58.383 MiB). The first optimized Go artifact is 46,584,071 bytes (44.426 MiB), a 23.9% reduction. Its runtime archive is 9,252,517 bytes (8.824 MiB), or 19.9% of that executable. It contains 24,987,005 uncompressed bytes. The first Go + Node artifact has the same executable size; its runtime content identity is `684a7a0971b1df5ef886bdaa84b9b8785abac67cf1aecab44471400aedab1ca5`. The final post-cycle-guard combined artifact is 46,600,455 bytes. Its Node `93c710dd2` runtime has content digest `28969dce309436fc24a987c561308a99601e895ec82082e002336c2b33359eb7`.

Representative allocation and CPU benchmarks are committed with the implementation:

- `BenchmarkListModelsByProvider` reduces the former approximately 17.5 MB of provider-filtered backing arrays per catalog traversal to allocations sized to the actual results.
- `BenchmarkExtensionCatalogEncoding` measures the real registry composition, projection, and serialization path. The initial cache measures approximately 40 ms uncached versus 23–24 ms cached. After the late changes, samples measure approximately 34 ms uncached versus 13.5–13.8 ms cached, with approximately 156,000 versus 33,500 allocations. This is not an allocation-free claim.
- `BenchmarkModelInfoCatalog` improves from approximately 9.4 ms / 5.39 MB to 4.2 ms / 3.59 MB by sharing the scalar compatibility copy.
- `BenchmarkModelCatalogFrame` improves from approximately 2.97 ms to 2.42 ms for its stable catalog fixture. The queue receives the original encoded bytes; it does not parse and encode them again.
- `BenchmarkCloneCatalogCompat` improves from approximately 5.0 ms / 2.36 MB to 0.95 ms / 0.56 MB per generated catalog. Reading only field values avoids constructing unused reflection metadata. The regression guard rejects a mechanically modernized iterator that loses the allocation advantage.
- `BenchmarkNodeRuntimeMaterialization` measures approximately 32 ms for the original embedded tree, 54–55 ms for Deflate, and 39–40 ms for Zstandard. Full cold-process measurements remain the primary acceptance evidence.

Eight-start cold TS allocation profiles attribute approximately 708 MB to the former `ListModels`, versus 14 MB after indexing. Aggregate allocation space falls from approximately 1.92 GB to 0.99 GB in those diagnostic captures. These are sampled allocation totals, not peak RSS, and profiling runs are separate from timing.

Peak RSS below uses `/usr/bin/time -v`, 25 plain-print samples per cell after a separate warm seed. It is the largest inherited process high-water RSS, not the sum of concurrent Go and Node RSS. These samples use the Zstandard/flat-cache artifacts before the final opaque-marshaler guard; their model inputs take the same canonical-data path.

| Workload | Go only median / p90, MiB | Go + Node median / p90, MiB |
|---|---:|---:|
| TS warm | 112.31 / 123.27 | 105.31 / 113.78 |
| TS cold | 143.50 / 164.50 | 141.75 / 162.75 |
| Package warm | 118.93 / 128.10 | 117.88 / 126.28 |
| Package cold | 148.75 / 162.75 | 143.50 / 161.00 |

Compression is not claimed to reduce first-materialization RSS. Decoder buffers and the retained catalog encoding have a measured memory cost. Decoder work stays within the existing eight-worker limit; the ZIP adapter forces one decoder thread per reader. Readers return to a GC-managed pool. Files close before publication. No new detached task, timer, socket, extension runtime, or TUI-loop operation is introduced.

The runtime's build-time digest covers every embedded path and byte. The shared entry also includes the existing runtime version. Both isolated and packed launchers link from the same flat `ext/node-runtime-<hash>` cache tier. Filesystems without hard links receive copies. Each launcher remains self-contained after the shared entry is pruned. A usage lease protects linking; the existing readiness, identity, target, size, lock, and atomic-publication checks remain in force. Warm validation uses that immutable-artifact contract, not a new claim of full-tree tamper detection.

A regression guard exposes the initial nested cache layout as unmanaged by the existing GC. The corrected flat entry is independently classified, protected while leased, and removable when expired without damaging a launcher. Another guard proves that unchanged content opens no archive, changed runtime content cannot reuse an old tree, and packed identities include the runtime version.

The unchanged Loader fixture retains its interval. For each Go artifact, 25 EOF and 25 SIGTERM probes acknowledge the shutdown hook, exit with 0 or 143, and leave no Node PID. Maximum exits are 65.23 ms / 39.64 ms for Go-only and 32.76 ms / 91.72 ms for Go + Node. Three Pi probes per action also pass. The three-second watchdog never fires. A harness readiness bug initially reads the marker between file creation and its write; that failed attempt is retained. The corrected barrier waits for the complete numeric PID within the original deadline. It does not change the fixture, timeout, or retry an invocation.

## Behavioral evidence and qualification

Pi `packages/ai/src/models.ts:306-325` preserves provider/model enumeration and treats provider catalog failures as best-effort. `packages/ai/src/types.ts:982-1017` defines the model metadata retained here. `packages/coding-agent/src/core/model-runtime.ts:246-281,296-315` separates provider composition and model snapshots from auth availability. `packages/coding-agent/src/core/extensions/loader.ts:479-577,599-630` awaits loading and factory initialization. No export, load-order rule, request-auth expression, or extension wire shape changes intentionally.

Red or compiling-mutation guards cover result-sized catalog allocation, scalar-copy allocation, complete serialized compatibility values, nested metadata mutation, provider order and auth changes, extra user-marshaler evaluation, archive/source drift, content/version cache identity, concurrent publication, and cache GC ownership. Cached and uncached publication comparisons assert complete JSON bytes, including nil/empty values and errors. Native and Windows vet, full repository lint, focused races, `go fix -diff`, divergence-guard, source-hygiene, and docs-drift checks pass.

The startup and model-runtime-store-catalog families pass declared Pi pairs. The combined artifact passes the Node lane's exact `56-node-lazy-imports` scenario. The focused `TestConformance_*` suite passes across real SDK transports. Full qualification is not green:

- `extensions-runtime/29-export-tool-renderers` fails with tool/event ordering differences. The unchanged before binary reproduces the failure.
- `TestUpstreamRunnerRejectsToolWithoutParameterSchema` expects stderr where the current loader uses a structured factory failure. Original and optimized Go materialization controls both fail.
- Full conformance reaches the unchanged deadline in `TestPackedToolSchemaRejectionAcrossSDKs/go/startup`. Python provider-producer cases also report `name 'cancel' is not defined`.
- `make ci-contracts` rejects existing pending/partial hot-path test mappings after local generated inventories are refreshed.
- `make ci-drift` rejects D78 because it lacks `SCRUTINIZED:approved`.
- Cleanup finds two orphaned interactive `pig-parity` children from the model family (`22-partial-scoped-models` and `13-scoped-google-catalog`). They remain after SIGTERM and exit after SIGQUIT. Their exact commands and PIDs are retained in `orphaned-parity-processes.json` and reported to the integrator. This is separate from the successful headless Loader lifecycle probes.

No comparator, timeout, test expectation, or divergence approval is weakened. These historical controls precede the stable merge and do not qualify its later source.

## Evidence bundle

`perf-go-startup-evidence/` accompanies the lane handoff. It contains the original measurement harness and fixtures, separate drivers, all timing/RSS/exit samples and raw outputs, profiles, benchmarks, compiling mutations, baseline controls, gate logs, generated runtime identities, and compressed binaries. The main batch files are `matrix.jsonl`, `codec.jsonl`, `joint.jsonl`, and `final.jsonl`; `final-table.md` and `summary.json` are derived summaries.

The first Go-only measured binary has SHA-256 `0009e3e5545beccd28464834959928bbc4b0614a6b5b6c84f72a87c54a2055e3`. The first combined measured binary has SHA-256 `11a5f8cb967dc1bd83c237ac83c8de76d68888e0e549b2b2e33231dda9520967`. The final post-cycle-guard combined artifact uses Go `7c551a5fc` production code and the immutable Node runtime snapshot from `93c710dd2`; its SHA-256 is `9a71365fd9f4e7aa13471d334ca483b4462c658155703e350c051a526ae2204f`. The final matched matrix is `exact.jsonl`. Rebuild the archive with `go generate ./coding/extension/host/subprocess` after merging Node source changes.
