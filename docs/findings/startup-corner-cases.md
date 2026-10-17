# Startup corner cases

## Scope and status

This investigation fixes two startup defects on the assigned stable base `1beced15480cb8d46db113af9874d7316baf0b4f`:

1. Resource discovery recompiles ignore regexes for every candidate path.
2. Concurrent native-provider refresh and catalog publication can deadlock during `session_start`.

The credential-lock repair is landed as `f7a88e55d`, and the ignore optimization is landed as `e4182a09a`. The complete ignore control has 396 valid invocations, including 360 measured trials and 36 cache-seeding processes. The [corner matrix](startup-corner-matrix.md) records 2,250 measured process starts across the requested workloads, including explicit error and unqualified outcomes. The investigation is complete, but release qualification is not: the RPC refresh-order discrepancy, cold Rust cost, unsupported low-memory configuration, historical-baseline failures, and broad gate failures remain explicit obligations. No failed trial is treated as a fast startup.

PiG is a Go implementation of [Pi](https://github.com/earendil-works/pi). Pi 0.87.1 is the reference implementation. Michael Kinsy created PiG, which was originally developed at Hewlett Packard Enterprise.

## Identities and method

The stable base does not contain the Node lane's `abb743382` or the Go lane's `7c551a5fc`. Their catalog, runtime-archive, library, worker-channel, and Session optimizations are separate work. This investigation does not reimplement them. Read the Node lane's `docs/findings/startup-performance.md` and the Go lane's startup report before comparing these results with the integrated candidate.

The historical 0.2.0 executable comes from `ecdd029f1cf8af7f4cca89aa1266abaf00620c76`. Its SHA-256 is `f2c9803ed9e1d671ab09b53366e2fff5514c8363201b47b21a3098764e899885`, and its size is 35,270,919 bytes. The frozen executable is not rebuilt with a newer source tree. Stable and optimization controls use Go 1.27.1, `-buildvcs=false -trimpath -ldflags='-s -w'`, and identical source except for the named fix. Node is 24.19.0, Python is 3.12.3, Rust is 1.97.1, and tmux is 3.7b. Installed npm is 11.17.0 rather than the documented 12.1.0. No toolchain is installed or replaced.

The advisory driver rotates builds within each five-trial cell. Session/native controls use CPUs 64–71. The complete ignore-control and remaining corner batches use CPUs 80–87 while native compilation runs on 64–71. Comparisons within a batch use identical affinity. The deliberate CPU/IO-load experiment waits for the ordinary native timings to finish. Each cold trial has a fresh HOME, agent directory, application cache, and TMPDIR. Warm trials retain only that cell's application state after one unmeasured seeding process. OS page caches and shared compiler dependency caches are not flushed. The host is shared. No timing threshold blocks a test.

Print readiness means exact `42\n` output from the hermetic faux provider. RPC readiness means the successful `get_state` response; the process then completes `get_commands` before EOF. RPC checks verify extension command registration and all 256 prompt and 256 skill commands in the resource fixture. Interactive first-frame timing ends at the rendered model footer, not completion of `session_start`. The internal `interactive-ready` trace remains a separate checkpoint. A first frame can precede a startup deadlock, so successful exit is also required. Pi uses the repository's faux-provider extension; PiG uses its native faux provider. Pi's extra import is included, not subtracted. No worker credentials or settings enter a trial.

The 20-package inventory and npm lock come from the exit lane's `packages20.json` and `packages/package-lock.json`. The inventory combines the recovered WSL package names and issue #71 set. These versions were resolved on this host. The original WSL lock is unavailable, so these runs are not a reproduction of the original 18.5 s / 13.2 s versions.

## Fix 1: compile ignore rules once per discovery walk

`internal/ignorerules/ignorerules.go` previously called `regexp.MatchString` inside `match`. That helper compiles a new regex on every call. Skill discovery checks each path against every applicable rule, including a second directory-form check. The same helper serves Package resource discovery, Node source discovery, and execution-environment skill discovery.

Each `Rule` now owns an immutable compiled matcher. Both rule constructors compile it once. Candidate matching preserves the existing basename-glob branch, regex spelling, rule order, negation, and invalid-pattern result. The cache belongs to the discovery walk, not the process. Reload constructs fresh rules. No file read, diagnostic, validation, or candidate is removed. No goroutine, lock, timer, global cache, or public capability is added.

Pi's `packages/coding-agent/src/core/skills.ts:183-188` constructs one ignore matcher for the walk and appends each directory's rules. Lines 257–265 reuse that matcher for candidate filtering and recursive discovery. The optimization follows that lifetime without changing the existing matching contract.

### Deterministic red/green guards

| Guard | Original failure | Fixed result |
|---|---|---|
| `TestIgnoreRulesCandidateAllocationBudget` | 300 allocations per candidate; relative-path-only baseline is 0 | Matches the 0-allocation baseline |
| `TestStartupSkillsIgnoreWorkBudget` | 247,983 additional allocations for 32 skills and 32 non-matching patterns; two original constructor calls allocate 10 times | Additional work stays within two measured matcher constructions, independently of candidate count; a later ignore-file edit takes effect |
| `TestIgnoreRulesOrderAndWalkOwnership` | Behavioral preservation guard, not red-proven | Keeps matching, negation precedence, earlier slice ownership, and fresh-walk state |
| `TestCompiledIgnoreRulePreservesMatching` | Differential preservation guard against the original algorithm, not red-proven | Preserves empty, malformed, basename, path, Unicode, escaped, and directory patterns |

The two added startup/allocation packages complete in 0.004 s and 0.058 s in the focused green run. The test budgets compare work and allocations, not elapsed time.

The representative benchmark checks 256 skill paths against 64 non-matching rules. Five samples give median 159.19 → 1.19 ms per walk and approximately 1,322,181 → 0 allocations per walk. Median allocated bytes fall from approximately 127.5 MB to 19 bytes; the small fixed residual comes from runtime matcher-pool work, not per-candidate allocation. CPU and allocation profiles identify regex compilation as the original cost center. `regexp.compile` accounts for 72.78% of sampled CPU. These are diagnostic benchmark measurements, not CI timing limits.

### Complete ignore-control matrix

Each cell is the median of five valid trials. All 360 measured and 36 seeding invocations complete successfully. `resources` contains 256 skills, 256 prompt templates, and 64 non-matching ignore patterns. The same input tree and all discovered commands remain present after the change. Interactive values measure the first model-footer frame, not completion of initialization.

| Workload | Mode | Cache | Stable 1bec, ms | Ignore fix, ms | 0.2.0, ms | Pi, ms |
|---|---|---|---:|---:|---:|---:|
| none | print | cold | 83.97 | 84.16 | 48.87 | 750.16 |
| none | print | warm | 73.34 | 70.87 | 39.67 | 365.61 |
| none | rpc | cold | 97.38 | 97.38 | 54.86 | 759.17 |
| none | rpc | warm | 84.01 | 84.13 | 46.44 | 357.06 |
| none | interactive | cold | 105.39 | 108.52 | 63.15 | 790.60 |
| none | interactive | warm | 92.53 | 94.08 | 51.30 | 386.02 |
| ts | print | cold | 801.89 | 773.46 | 333.39 | 772.57 |
| ts | print | warm | 556.08 | 548.44 | 315.82 | 357.51 |
| ts | rpc | cold | 795.52 | 784.29 | 338.15 | 743.63 |
| ts | rpc | warm | 564.55 | 559.56 | 319.00 | 345.48 |
| ts | interactive | cold | 611.02 | 614.45 | 225.10 | 780.72 |
| ts | interactive | warm | 379.92 | 383.23 | 208.90 | 376.93 |
| resources | print | cold | 2380.36 | 226.02 | 455.52 | 908.57 |
| resources | print | warm | 2351.90 | 217.88 | 439.94 | 510.54 |
| resources | rpc | cold | 2372.77 | 239.04 | 2318.34 | 901.85 |
| resources | rpc | warm | 2390.96 | 228.24 | 2330.16 | 497.25 |
| resources | interactive | cold | 519.64 | 151.62 | 457.12 | 931.31 |
| resources | interactive | warm | 496.10 | 137.50 | 439.30 | 541.30 |

The warm resource trace reaches `interactive-ready` at a median 2317 → 210 ms on the internal clock. The first frame previously preceded a further 1.8 seconds of initialization. The shared matcher fix removes that cost as well. The warm resource checkpoint `extension-discovery-start` falls from approximately 383–394 ms to 22–23 ms across modes. Initial services and warm SDK synchronization remain approximately 2 and 4 ms. Full cumulative checkpoints and raw Pi timing namespaces remain in the evidence.

No-extension and TS controls do not exercise the ignore-rule hotspot. Their small differences are not claimed as speedups. The assigned stable base remains slower than 0.2.0 on several controls, and these results do not close the Go/Node lanes' remaining startup targets.

## Fix 2: avoid registry reentry inside credential transactions

The exit lane's real 20-package interactive run stops at `pre-emit-session-start`. It remains stuck at the 120-second observation deadline before shutdown starts. The SIGQUIT dump identifies this cycle:

- Catalog publication calls `HasConfiguredAuth`, holding the registry read lock while waiting for the auth-store mutex.
- A provider refresh holds the auth-store mutex in `Modify` and calls `ModelRegistry.RuntimeAPIKey`, which tries to acquire the registry read lock again.
- Another refresh waits for the registry write lock to publish its result. Go's writer preference blocks the new read, completing the cycle.

`internal/codingagent/native_provider.go` now reads the key through the `RuntimeCredentials` pointer already captured by the operation. It does not reacquire the registry lock from inside the credential transaction. The same overlay remains authoritative, including current non-persistent runtime keys. Credential serialization, callback execution, persistence, refresh errors, cancellation, and catalog publications remain intact. This is not a change to auth-store locking or a shorter timeout.

Pi's `packages/coding-agent/src/core/model-runtime.ts:133` retains one credentials object. `packages/coding-agent/src/core/runtime-credentials.ts:24-27,38-44` reads the overlay and delegates modification to its base store. `packages/ai/src/models.ts:460-490` resolves refresh credentials through that operation's credential store. The Go bridge no longer adds a registry-lock dependency to that operation.

`TestNativeRefreshCredentialTransactionDoesNotReenterRegistry` registers a real native provider and drives the joined refresh caller. Its credential-store double holds a registry writer during the modification callback. The original code compiles, then deterministically blocks in `RuntimeAPIKey`; an external five-second SIGQUIT probe captures that stack. The test itself contains no timer, sleep, polling loop, or detached goroutine. The fixed test covers missing credentials, stored API keys, OAuth credentials, runtime-key overrides, an empty model catalog, and callback errors. It also checks that resolution does not fabricate a missing credential or persist the runtime override. The missing-credential case is added after the initial fix and is separately red-proven with a compiling original-source overlay. The focused initial green package completes in 0.053 s. Three race-enabled repetitions pass.

The exit lane's complete 20-package re-probe supplies the nearest full-process caller evidence. Both Go columns include this lock fix, and all 15 processes exit successfully. Median interactive-ready times are 10592.7 ms with the original shutdown path, 10559.6 ms with the exit lane's shutdown fix, and 4956.2 ms for Pi. These are warm, online runs with the resolved package lock, not the original WSL versions. No failing original startup sample is converted into a latency value.

The exit lane's before-lock executable has SHA-256 `9ee8deedeff184c590c8c9bc311245de5f1ae4e7518cdcba88b24d08a4648f3e`. Its after-lock executable has SHA-256 `066e1380bc9fd0357f9434269bc2d2b7456d32a67addfebd9babdfe387310605`. Both use this exact `native_provider.go`, SHA-256 `8d4c51c5102776cd6961c6f30d0fd51586f61fcfe1fd839691b752eee68c8bea`. The only difference between those executables is the exit lane's eight-line shutdown change. Its overlays, phase table, complete outputs, and package lock are retained in the external `fix-exit-latency` evidence bundle.

## Good patterns to preserve

| Pattern | Location | Why it matters |
|---|---|---|
| Walk-scoped immutable derived state | `internal/ignorerules/ignorerules.go:14,88` | Avoids repeated compilation without retaining stale rules after reload |
| Shared authoritative credential overlay | `ai/runtime_credentials.go:58,117`; `internal/codingagent/native_provider.go:225` | Preserves non-persistent keys without inverting registry and credential locks |
| Warm SDK marker check | `coding/extension/pigsdk/pigsdk.go:174` | Current SDKs avoid staging work and lock acquisition |
| Bounded, joined session-list workers | `internal/codingagent/session_listing.go:132-150` | Caps simultaneous file work and drains workers on cancellation |
| Streaming session summaries | `internal/codingagent/session_resume.go:244` | Avoids retaining parsed history objects while building picker metadata |
| Bounded header-only continue discovery | `internal/codingagent/session_resume.go:506-525` | Does not read every transcript body to choose the recent session |
| Explicit startup lock-wait trace | `cmd/pig/startup_trace.go:70` | Makes cross-process SDK contention diagnosable |

## Anti-patterns and ownership

| Finding | Location | Disposition |
|---|---|---|
| Per-candidate regex compilation | Original `internal/ignorerules/ignorerules.go:89-105` | Fixed here; allocation and CPU evidence identify the cost |
| Credential transaction reacquires registry state | Original `internal/codingagent/native_provider.go:224` | Fixed here; real process dump and deterministic guard identify the lock cycle |
| Repeated catalog projection and encoding | `internal/codingagent/extension_model_registry.go`; `coding/extension/host/subprocess/ui_bridge.go` | Go startup lane owns the existing fixes; unchanged here |
| Repeated resume projection and loading | `coding/session.go`; `coding/runtime.go` | Earlier Go startup lane owns Session reuse/settings/price-binding fixes; unchanged here |
| Full pinned Node graph materialization and import | `coding/extension/host/subprocess/builder_node.go`; runtime-node sources | Earlier Node/Go startup lanes own the fixes; unchanged here |
| RPC starts catalog refresh before extension loading | `cmd/pig/rpc_mode.go:278` versus Pi `main.ts:920-928` | Open, reported to the lead; ten unavailable/slow-catalog RPC trials per case observe two requests versus one in Pi |
| Fresh packed Rust compilation | `coding/extension/host/runtimecell/rust_packed.go:111` | Measured approximately 39 seconds inside build-check; retained as an optimization opportunity, not changed without a separate build-correctness investigation |
| Unbounded goroutines or quadratic session scans | No additional finding established by this investigation | Session-list workers are bounded; the RPC refresh task above has a separate lifetime obligation |

## Evidence and qualification

Raw outputs, identities, advisory drivers, profiles, red/green logs, phase tables, and frozen binaries are retained in the external `perf-startup-corners` evidence bundle. `manifest.sha256` identifies the final evidence files. The immutable controls are under `binaries/`; accepted final outputs and reproducible fixture builders are under `final/`. The earlier ignore checkpoint and invalid calibration evidence remain separate. See [the complete matrix](startup-corner-matrix.md) for all modes/cache states, phase limitations, failure semantics, and harness corrections.

Known integration gates are not weakened. `make ci-contracts` reports 166 pending/partial hot-path test-mapping blockers. `make ci-drift` reports D78 without `SCRUTINIZED:approved`. Full `go test ./...` fails the existing closure-ledger assertions and reaches the ten-minute extension-conformance package deadline while `TestPackedOrderedToolResults/rust` has run for 21 seconds. That invocation is a failure, not a passing retry. The touched cmd/pig, codingagent, subprocess, resource-discovery, source-discovery, and harness packages pass. Full lint, full vet, Windows touched-package vet, and the startup and model-runtime-store-catalog parity families pass. No timeout, comparator, mapping, or approval is weakened.
