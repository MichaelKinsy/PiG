# Startup corner matrix

This appendix accompanies [Startup corner cases](startup-corner-cases.md). Values are milliseconds. `cold / warm` means five process trials in each application-cache state, not cold OS page caches. Interactive timing measures the first main-screen model footer or the first populated resume-picker row. It does not imply completion of extension initialization. Every successful row also requires process exit. The report and raw traces distinguish `interactive-ready` from that first frame.

The complete evidence contains 2,250 measured process starts, plus seeding and calibration invocations. Expected error exits are recorded separately from successful readiness. No failed sample becomes a fast-start result. These measurements describe the frozen assigned base and named fixes, not the newer Go/Node startup branches.

## Native extensions

`factory` uses PiG's packed-cell planner with one conventional factory. `isolated` uses an exact standalone. Each is the ordinary `pig extension init` fixture for its language. This compares supported source forms, not an artificial isolation switch on identical source. All registrations reach their handshake checkpoint. Pi does not load Go, Rust, or Python extensions, so there is no truthful native-language Pi column; the TS control in the main report is the reference-language comparison. This extension mechanism is D19/D20.

| Source form | Build | Print cold / warm | RPC cold / warm | Interactive cold / warm |
|---|---|---:|---:|---:|
| Go factory | Stable | 570.5 / 273.6 | 559.4 / 290.2 | 361.0 / 97.1 |
| Go factory | 0.2.0 | 425.2 / 169.0 | 404.0 / 174.6 | 284.4 / 58.5 |
| Go isolated | Stable | 519.7 / 267.6 | 514.0 / 285.3 | 332.9 / 96.6 |
| Go isolated | 0.2.0 | 387.5 / 160.2 | 389.1 / 166.9 | 275.2 / 56.5 |
| Python factory | Stable | 403.2 / 386.0 | 421.8 / 407.9 | 219.5 / 198.8 |
| Python factory | 0.2.0 | 262.6 / 254.8 | 276.3 / 266.1 | 142.6 / 129.8 |
| Python isolated | Stable | 376.3 / 327.3 | 387.7 / 340.7 | 206.9 / 156.4 |
| Python isolated | 0.2.0 | 243.4 / 209.8 | 247.1 / 221.2 | 135.1 / 103.9 |
| Rust factory | Stable | 40147.2 / 261.7 | 39613.4 / 275.6 | 39084.3 / 97.5 |
| Rust factory | 0.2.0 | 31513.1 / 155.8 | 31052.3 / 162.9 | 30354.8 / 60.7 |
| Rust isolated | Stable | 7408.7 / 268.5 | 7426.8 / 275.2 | 7088.7 / 97.5 |
| Rust isolated | 0.2.0 | 5726.6 / 158.8 | 5698.2 / 164.7 | 5582.4 / 61.0 |

The cold Rust factory cost is inside `build-check`: approximately 39.3 seconds of the 39.6-second RPC start. The standalone's median build-check is approximately 7.1 seconds. Both builders run Cargo release builds (`coding/extension/host/runtimecell/rust_packed.go:111`; `coding/extension/host/subprocess/builder.go:774`). A different compile mode is not used to hide the cost. Shared Cargo dependency downloads are warm; cell artifacts are cold. This is an unresolved cold-build cost, not a demonstrated correctness defect or an optimization implemented here.

## Session corners

The huge fixture contains 50,000 linked message entries, including 500 approximately 66 KB tool outputs. The transcript is approximately 45.9 MB. The driver restores it before every huge-session trial, outside the timer, because a print turn may compact it. It retains application caches for warm trials. The 1,000-session fixture contains four messages per file. `continue1000` uses `-c`. `resume1000` uses `-r` in interactive mode and selects an explicit file in print/RPC, where there is no interactive picker.

| Case | Build | Print cold / warm | RPC cold / warm | Interactive cold / warm |
|---|---|---:|---:|---:|
| 50,000 entries | Stable | 3773.7 / 3815.9 | 2608.9 / 2654.7 | 2783.1 / 2625.9 |
| 50,000 entries | 0.2.0 | 2260.5 / 2248.3 | 1129.3 / 1140.2 | 1185.7 / 1119.7 |
| 50,000 entries | Pi | 1529.5 / 1087.3 | 1037.4 / 628.9 | 7186.6 / 6615.8 |
| Continue among 1,000 | Stable | 123.4 / 105.6 | 132.9 / 118.3 | 136.8 / 127.1 |
| Continue among 1,000 | 0.2.0 | 83.8 / 72.8 | 89.2 / 77.7 | 90.3 / 82.0 |
| Continue among 1,000 | Pi | 755.1 / 364.5 | 740.7 / 355.9 | 783.3 / 399.9 |
| Resume among 1,000 | Stable | 81.6 / 72.4 | 95.0 / 82.4 | 74.1 / 73.8 |
| Resume among 1,000 | 0.2.0 | 47.6 / 39.8 | 54.1 / 44.2 | 73.7 / 74.4 |
| Resume among 1,000 | Pi | 734.0 / 354.1 | 736.8 / 345.1 | 414.0 / 299.8 |

These are startup observations, not a claim that PiG's long-history rendering or compaction is equivalent because it is faster. The earlier Go startup lane owns the already-identified duplicate Session open, settings projection, and price-binding work absent from this base. No competing Session repair is introduced here.

## Slow factory and failure boundaries

The slow factory awaits 250 ms and then registers a command. RPC verifies that command exists. No implementation skips or backgrounds the factory.

| Mode | Cache | Stable | 0.2.0 | Pi |
|---|---|---:|---:|---:|
| print | cold | 1041.9 | 581.5 | 1036.5 |
| print | warm | 797.0 | 565.7 | 606.3 |
| rpc | cold | 1037.2 | 589.1 | 1009.0 |
| rpc | warm | 812.6 | 573.0 | 604.0 |
| interactive | cold | 927.1 | 487.9 | 1119.4 |
| interactive | warm | 635.1 | 461.0 | 630.6 |

Every failure cell has five trials in each mode/cache state. The table below gives exit outcomes, not readiness. Full per-cell time-to-error medians remain in `table-faults.md` in the evidence bundle.

| Case | Stable | 0.2.0 | Pi | Interpretation |
|---|---|---|---|---|
| Factory throws `corner-factory-failed` | Exit 1; diagnostic retained | Exit 1; diagnostic retained | Exit 1; diagnostic retained | No successful startup is claimed |
| Factory calls `process.exit(23)` | Exit 1 with extension-load failure | Exit 1 with extension-load failure | Exit 23 | Subprocess containment boundary, D56/D20, not equal process-exit behavior |
| 1 GiB virtual-address limit | Exit 2; cgo thread creation fails | Exit 2 | SIGTRAP; V8 CodeRange reservation fails | Runtime reservation failure, not a usable low-memory configuration |

`prlimit --as=1073741824` measures virtual-address limits, not physical-memory pressure. No writable delegated memory cgroup is used. The result does not establish a supported RSS limit. No timeout is lengthened to obtain a successful sample.

## Offline, unavailable/slow catalog, and partial cache

A loopback Radius catalog returns HTTP 503. The slow case delays that response by 250 ms. A `session_start` extension awaits an explicit selected-provider refresh and reports its result. Offline mode makes zero requests. The corrupt-cache fixture writes a truncated `models-store.json`. The final contract check requires the expected diagnostic or success marker, not merely a responsive CLI.

| Case | Mode | Cache | Stable | 0.2.0 | Pi |
|---|---|---|---:|---|---:|
| offline | print | cold | 1002.6 | contract not reached | 770.3 |
| offline | print | warm | 757.6 | contract not reached | 359.5 |
| offline | rpc | cold | 1001.7 | contract not reached | 762.0 |
| offline | rpc | warm | 789.7 | contract not reached | 352.5 |
| offline | interactive | cold | 615.7 | contract not reached | 780.6 |
| offline | interactive | warm | 375.8 | contract not reached | 378.9 |
| unavailable catalog | print | cold | 984.7 | contract not reached | 781.5 |
| unavailable catalog | print | warm | 761.7 | contract not reached | 388.6 |
| unavailable catalog | rpc | cold | 995.2 | contract not reached | 781.1 |
| unavailable catalog | rpc | warm | 800.9 | contract not reached | 405.1 |
| unavailable catalog | interactive | cold | 603.1 | contract not reached | 780.5 |
| unavailable catalog | interactive | warm | 382.6 | contract not reached | 392.5 |
| slow catalog | print | cold | 1239.9 | contract not reached | 1026.0 |
| slow catalog | print | warm | 1026.6 | contract not reached | 649.2 |
| slow catalog | rpc | cold | 1245.1 | contract not reached | 1029.2 |
| slow catalog | rpc | warm | 1031.2 | contract not reached | 643.5 |
| slow catalog | interactive | cold | 591.7 | contract not reached | contract not reached |
| slow catalog | interactive | warm | 375.2 | contract not reached | contract not reached |
| partial cache | print | cold | 981.1 | contract not reached | 766.5 |
| partial cache | print | warm | 759.3 | contract not reached | 378.0 |
| partial cache | rpc | cold | 997.3 | contract not reached | 758.6 |
| partial cache | rpc | warm | 769.6 | contract not reached | 370.0 |
| partial cache | interactive | cold | 599.9 | contract not reached | 789.9 |
| partial cache | interactive | warm | 375.6 | contract not reached | 401.1 |

The 0.2.0 process exits successfully without reaching the newer refresh-result contract, so its times are not comparable successful measurements. For slow-network interactive Pi, the first frame is visible but the harness sends Ctrl+D before the awaited marker is reported. Those cells remain unqualified. They are not evidence that Pi's refresh failed or that PiG is faster at completing it. Print and RPC supply the completed slow-refresh evidence.

### Open RPC ordering discrepancy

All ten cold/warm RPC trials for each unavailable/slow catalog make two loopback catalog requests in PiG and one in Pi. Print makes one on both. `cmd/pig/rpc_mode.go:278` launches background refresh before loading extensions and creating the Session. Pi's `packages/coding-agent/src/main.ts:920-928` launches it after runtime and Session validation, immediately before `runRpcMode`. The extension's later refresh therefore supersedes different work. PiG also lacks an explicit join for that goroutine.

This is reported to the lead as an unresolved startup/RPC lifecycle defect. The earlier RPC lane's pane is unavailable, so no handoff to that owner is assumed. This investigation does not delete the background refresh or change its timeout. A repair needs a deterministic ordering/cancellation guard and a full RPC caller probe. The two fixes in this report do not close this discrepancy.

## Shared agent directory and host load

`shared` runs four processes against the same agent directory and application cache. Each cell is the median of five cohort maxima: the time until the last of the four starts is ready. All 360 measured cohort processes complete. `load` adds four CPU loops on the same eight-CPU affinity and a 32 MiB rewrite/fsync loop. It does not saturate all eight cores or emulate a system-wide storage outage. Ordinary native timings finish before this experiment starts. Only owned load processes are terminated and joined.

| Case | Build | Print cold / warm | RPC cold / warm | Interactive cold / warm |
|---|---|---:|---:|---:|
| Four starts | Stable | 888.9 / 686.7 | 901.1 / 706.4 | 718.2 / 511.6 |
| Four starts | 0.2.0 | 401.5 / 364.3 | 420.7 / 387.4 | 276.2 / 246.1 |
| Four starts | Pi | 893.9 / 414.9 | 839.3 / 407.4 | 884.0 / 426.2 |
| CPU + IO load | Stable | 793.7 / 566.7 | 802.7 / 583.9 | 628.6 / 396.1 |
| CPU + IO load | 0.2.0 | 341.6 / 314.7 | 343.5 / 325.3 | 233.4 / 216.5 |
| CPU + IO load | Pi | 781.2 / 360.9 | 749.2 / 360.5 | 789.9 / 385.9 |

## Twenty packages

These rows use the resolved package set identified in the main report. The Go executable contains only the ignore and credential-lock fixes on the assigned stable base. It does not contain the exit lane's shutdown optimization or the older Go/Node lanes' startup optimizations. These controls use `--offline`; an extension can still explicitly request network work. The exit lane's separate online, post-`session_start` command barrier remains the stronger proof that the lock fix unblocks complete initialization.

| Build | Print cold / warm | RPC cold / warm | Interactive cold / warm |
|---|---:|---:|---:|
| Stable + two fixes | 16846.3 / 11531.3 | 16246.4 / 10796.0 | 9309.8 / 4437.2 |
| 0.2.0 | Failed / failed | Failed / failed | Failed / failed |
| Pi | 21926.3 / 4801.6 | 23146.3 / 4693.6 | 20286.0 / 3952.1 |

All five 0.2.0 trials in every cell reject pi-lens's declared skill directory as a missing `skills/SKILL.md` and exit 1. The current code accepts that package shape. No startup median is assigned to the old failure.

## Phase and memory evidence

`phases-go.md` records every available cumulative startup checkpoint for each Go case/mode/cache/build. `phases-pi.md` records Pi's main and extension timing namespaces. Raw JSONL preserves per-trial labels; absent instrumentation stays absent. Go's trace starts after runtime/package initialization, while Pi's timing reset excludes Node/CLI bootstrap. Their totals are not directly interchangeable.

A separate `GODEBUG=inittrace=1` control observes approximately 0.10 ms in Go runtime initialization and entry into `main` package initialization at 25–26 ms. It is diagnostic, not a five-run wall-clock budget. Settings/auth are included in services checkpoints; catalog projection is interleaved with later Session and bridge work. Existing traces do not isolate each one as an independent span, so this report does not invent a precise catalog-only time for every case.

The exit lane's five-run online 20-package phase medians are services 27/29 ms, extension build/load/registration 4456/4475 ms, model/prompt/Session 59/61 ms, TUI/theme 509/515 ms, and `session_start`/resources 5507/5485 ms for its original/optimized shutdown binaries. Both include the credential-lock fix. Startup is unchanged by that shutdown optimization.

The JSONL rows retain sampled parent-process VmHWM. They do not include every extension child or compiler and are not a peak process-tree memory claim. Allocation profiles directly support the ignore-matcher fix. Rules remain walk-owned; the change introduces no background task, IPC, global cache, or surviving file handle.

## Calibration failures and exclusions

The evidence retains early invalid calibration runs separately from the completed matrix:

- Long temporary directory names exceeded Linux's Unix socket path limit. The corrected driver uses short, hashed temporary home names.
- A parity make target rebuilt a candidate path during a calibration batch. Accepted controls use a separate immutable `pig-ignore` executable.
- The first resume-picker detector waited for an escape hint that the picker does not render. The corrected detector requires an actual session row.
- An early huge-session warm print batch reused history modified by the previous turn. The accepted `session-final` batch restores the fixture before each launch.
- An advisory helper named `queue.py` shadowed Python's standard `queue` module during the concurrent-start setup and started an unintended measurement batch. The helper is renamed. Its outputs and cancellation record are retained; the accepted `session-final`, frozen native control, and `stress-shared` batches are separate.

These are harness defects, not product successes. No product timeout, comparator, diagnostic, or work requirement is weakened to accommodate them.
