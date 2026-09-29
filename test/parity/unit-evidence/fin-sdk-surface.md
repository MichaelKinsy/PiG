# fin-sdk-surface evidence

This is historical evidence from planning the unpublished 0.2.1 candidate, now named 0.3.0. Version references and gate results below describe that earlier investigation.

## Scope and reference

This lane keeps `test/parity/cmd/sdksurface` as the 0.2.1 member gate. It does not implement the separate 0.3.0 Pi runner or compiler generator. Coordination messages went to `gen-upstream-sync`, the four predecessor lanes, and `integrate-021`.

Reference: Pi 0.87.1 from `extensions/sdk-ts/node_modules/.bin/pi` and `.upstream/v0.87.1`. Host: Linux amd64 worker, Go 1.27.1, Node 24.19.0, Rust 1.97.1, Python 3.12.3. Tests use the checked-in faux provider, not a network model.

The predecessor handoffs are merged in the requested order. Final loader `749215aeab`, runtime surface `f04db0b7c3`, registry `e0fb8162fb`, and editor completion `89f8ef8b2a` are incorporated. The loader and runtime completion commits squash their handoffs. Their merges therefore apply the audited handoff-to-completion content deltas while preserving already integrated SDK, registry and editor changes. No rebase or force push rewrites the shared history.

## Upstream contracts

Paths below are relative to `.upstream/v0.87.1/packages/`.

| Contract | Pi source | Production change and proof |
|---|---|---|
| Every mode binds newSession, fork and switchSession to the runtime owner | `coding-agent/src/modes/print-mode.ts:80-101`, `coding-agent/src/modes/rpc/rpc-mode.ts:324-344`, `coding-agent/src/modes/interactive/interactive-mode.ts:1921-1963` | Shared Session command actions, headless bridge bindings, and interactive adapters. Tests: `TestExtensionSessionReplacementActions`, `TestHeadlessExtensionReplacementBindings`, `TestInteractiveExtensionReplacementBindings`; scenarios 39–42. |
| Await before hooks, honor cancel, drain the outgoing run, emit shutdown, install the destination, emit start, then return | `coding-agent/src/core/agent-session-runtime.ts:132-191,197-353` | Decode both typed and subprocess JSON cancellation results. Replacement calls await `Session.Abort`. The regression also checks an already-cancelled caller and that replacement cannot finish before an active run drains. |
| Fork defaults to before a user message; at accepts any entry | `coding-agent/src/core/agent-session-runtime.ts:263-288` | Validate the selected entry and preserve the requested leaf. The interactive adapter restores selected user text after replacement. |
| ParentSession applies to in-memory managers too | `coding-agent/src/core/agent-session-runtime.ts:232-238` | Preserve parent metadata in the in-memory constructor. `TestExtensionNewSessionKeepsParentInMemory` is mutation-proven. |
| A replacement context reads the destination SessionManager, including an empty session | `coding-agent/src/core/agent-session-runtime.ts:214-259,341-353` | Scope host page cursors and SDK history/index/cache state to session identity. Node rejects old synchronization generations and drops its indexed view. |
| Imported agent-core and inherited layout-symbol methods exist | `coding-agent/src/core/extensions/virtual-modules.ts`, `tui/src/components/stack.ts`, `tui/src/layout-node.ts` | Probe the supplied agent-core module and resolve LAYOUT_NODE from its actual module. `TestProbeFindsVendoredPackageAndSymbolMembers` was red. |
| Error cause is conditional on constructor inputs | `ai/src/auth/resolve.ts:18-27`, coding-agent's `CredentialSynchronizationError`, agent-core's error constructors | Supply non-default error inputs rather than classifying absent optional fields from invalid zero-argument construction as missing implementations. |
| Ordinary RPC setup emits protocol responses, not internal tool inventories | `coding-agent/src/modes/rpc/rpc-mode.ts` | Remove unconditional host diagnostic stderr. Scenario 21 now checks complete equal output and runs three times. |

D30 and D61 still apply to replacement: the runner is host-scoped and Services/Resources retain the startup project. `setup` and `withSession` remain missing callback capabilities, explicitly recorded in the surface exceptions. These fixes do not claim their implementation.

## Red and mutation evidence

Logs and profiles are retained under `/tmp/fin-sdk-gates/`; paired failure artifacts are under this worktree's `test/parity/artifacts/`.

| Regression | Before or compiling mutation | Result after fix |
|---|---|---|
| `TestExtensionSessionReplacementActions` | Before binding, the before-hook trace was empty. Forcing cancellation false changes the session and fails the test. | Pass; includes cancellation and run-drain ordering. |
| `TestInteractiveExtensionReplacementFailureUsesFatalPath` | The adapter returned an ordinary command error instead of taking the fatal-runtime path for new/fork/resume. | Pass; crash state changes only on the owner loop. Scenario 43 passes three pairs and the nonfatal-adapter mutation fails. |
| `TestStatePushRestartsCursorOnSessionReplacement` | Disabling the identity reset sends one entry from a three-entry destination after an outgoing cursor of two. | Pass; the whole destination page arrives. |
| `TestSessionIdentityClearsEmptyReplacementAndCaches` | Disabling identity reset retains the outgoing history in an empty destination. | Pass; history and both Go caches release their references. |
| `TestNodeRuntimeSDKSurfaceAdditions` | After resetting history, `_cachedIndex` still retained the outgoing Node entry/index maps. | Pass after clearing the indexed view with the session generation. |
| `TestExtensionNewSessionKeepsParentInMemory` | Dropping the constructor's parent assignment loses `/parent.jsonl` from the header. | Pass with no persisted file. |
| `TestProbeFindsVendoredPackageAndSymbolMembers` | Agent.abort and both inherited LAYOUT_NODE methods were reported absent. | Pass, including error cause and TaggedError fields. |
| `TestPackageProbeDoesNotConstructArbitraryClasses` | The probe invoked a fixture constructor that wrote a file. | Pass; non-error classes are not constructed for inventory. |
| `TestEverySDKCanReachEveryWireCapability` | Hard-coded input files missed generic Go helper calls, new native session modules, and Node's editor lifecycle notifications. | Pass after scanning production module files and binding the alternate/state-backed mechanisms to conformance. No fake capability gap is added. |
| Scenario 21 | Complete output equality failed on three internal RPC stderr lines. | Equal output after removing diagnostics. |
| Scenarios 39–42 | A binary built with the cancellation mutation fails every mode. Print/JSON artifacts and RPC output report cancel=false; TUI fails its cancelled transition. | Three complete Pi/PiG pairs pass in each mode. |

The mutation commands use Go `-overlay` files under `/tmp`, so runtime mutations compile without changing the worktree during another test. `mutation-host.log`, `mutation-mirror.log`, `mutation-cancel.log`, `mutation-parent.log`, and `mutation-parity.log` retain the failures.

## Paired scenarios

- `39-extension-session-replacement`: print; complete output equality plus the complete lifecycle/history artifact.
- `40-json-extension-session-replacement`: JSON; the complete lifecycle/history artifact.
- `41-rpc-extension-session-replacement`: RPC; complete normalized output equality. Only the generated notification UUID is replaced. Command IDs, event order, payloads and histories remain unchanged.
- `42-tui-extension-session-replacement`: interactive; complete equal lifecycle/history crop, including cancelled and successful replacements. The fixture uses the driver's isolated CWD for its writable files.

- `43-tui-extension-replacement-failure`: a missing fork entry takes Pi's fatal command-context path and exits 1. Pi stops before its queued error redraw paints, so the scenario asserts the exact exit status rather than a nonexistent stable error frame. `TestInteractiveExtensionReplacementFailureUsesFatalPath` checks crash recording and owner-loop mutation; restoring the nonfatal adapter compiles and fails scenario 43 because PiG stays alive.

Each declares three runs. The retained durability run records `completed_pairs=3` for all four. `tmp/fin-sdk-gates-modes-results.json` holds the structured results. Artifact normalization in 39/40 does not alter the artifact: it contains no variable IDs, paths or timestamps.

Scenario 21 now excludes ambient checkout skills with `--no-skills` while retaining its explicit `--skill` resource, as Pi does. This isolates the intended CLI source-info contract rather than normalizing an extra skill out of the result. The separately observed ancestor `.agents/skills/setup-pig` discovery difference is reported to the resource and integration owners; this lane does not claim to fix that discovery policy. The integration owner separately closes same-name `/btw` skill priority at `8ed29c07fe`.

## Corpus before and after

The direct consumer is `pi-cc-extensions@0.9.5`, whose shipped `extensions/feature/shell/aliases.ts` registers `/clear` by awaiting `ctx.newSession()`. The full package, not just the alias file, is loaded in the replay.

Tarball: `https://registry.npmjs.org/pi-cc-extensions/-/pi-cc-extensions-0.9.5.tgz`.
Integrity from the retained installed package lock: `sha512-9UrpuL/vw5JNZ0k1qEEodixE8NzpQ6/9wwqqiLhzhw9S8tQ6nYXVkCfEFj2CLkf0z/e7lBpo2S7vN14em3M1rw==`.

| Package/path | Before | Pi 0.87.1 | PiG after |
|---|---|---|---|
| Full pi-cc-extensions package, `/clear`, print/no-session | A compiling mutation restoring the two pre-fix action builders from `ffc51b173f` reports `unsupported: newSession not available`; trace is startup → quit. | startup → shutdown:new → start:new → shutdown:quit; exit 0 | Identical lifecycle trace; exit 0; no error. |
| pi-powerline-footer `/cd` | Requires imported SessionManager plus switchSession.withSession and destination-CWD ownership. | Supports those callbacks. | Not claimed fixed: withSession is missing and D61 still applies. The welcome-overlay fix belongs to the loader lane. |

Replay artifacts are retained in `/tmp/fin-sdk-corpus/{pi-native,pig-native,pig-unbound}/`; the final integrated replay is in `{pi-delivery,pig-delivery}/` and again produces identical traces, exit 0 and empty stderr. They were produced under the worktree's `tmp/fin-sdk-corpus/` and moved outside the source tree before source-marker gates, so cached historical runtimes do not masquerade as current production code. The observer records lifecycle reasons without volatile session IDs. The package and dependency closure are copied from the corpus cache before running; no cached corpus root is modified. The old-binding binary is `tmp/fin-sdk-unbound-mutant`, built using `/tmp/fin-sdk-unbound-overlay.json`.

The invocation uses `--no-extensions -e <copied package> -e <observer> --model test-faux/faux-1 --no-session --print /clear`; Pi additionally loads `test/parity/testdata/test-faux-provider.ts`. HOME and agent directories are per-engine, offline flags are set, and PATH names the installed Node binary and `~/flakes3-tools` directly. An initial environment attempt resolved mise shims under a fresh HOME and failed on tool resolution; it is retained separately and is not counted as a product pass or failure.

The original 46-package report's pi-cc load failure was `registerMarkdownTransformer` missing. That cause is fixed by the runtime lane. This replay additionally exercises the session-control cause covered here. It is not a claim that all 46 packages or every command in pi-cc are conformant.

## Surface gate

The generated snapshot enumerates 458 extension API rows and 1,817 package-export/public-instance-member rows. Static factories and overload-specific behavioral reachability are outside this probe. A symbol or vendored function origin is not behavioral proof.

All stale branch-ownership exceptions are removed. Completed readonly SessionManager and registry data/auth/refresh/configuration facades have implemented rows. Native `getProvider` and `getRegisteredNativeProvider` remain explicitly missing. Native registration overloads and configured-versus-checked auth availability remain partial. Every remaining missing cell has a current reason; D77/D78 are not treated as approvals.

## Resource and performance disposition

Replacement operations remain owned and awaited. They drain the outgoing run, then replace the Session. Interactive filesystem/session work is not run on the input/render loop; the adapter acknowledges the posted redraw and fork prefill before it returns. Host history pages keep the existing 4 MB paging target and wire frame limit. Replacements release SDK entry arrays, indexes and cached branch views. No detached production goroutine or retry is introduced.

`BenchmarkSessionMirrorReplacement` uses 1,000 representative approximately 5 KB entries and alternates populated and empty sessions. Three measurements were 18.97–19.09 ms per replacement pair, approximately 260 MB/s, 5.64 MB allocated and 3,023–3,025 allocations. This is a workload measurement, not a claimed speedup. CPU and allocation profiles are `/tmp/fin-sdk-gates/mirror-{cpu,mem}.pprof`. The benchmark runs entirely off the TUI loop.

## Gate disposition

The owning commands and final results are recorded in the lane's final report. Focused Session, host, native SDK and registry/mirror conformance tests pass, including Node isolated/packed and fused Go. Go and Windows vet, the repository and nested Go SDK linters, Python tests and REUSE pass. The known full-suite policy blockers are the explicitly pending D77 and D78 records; no approval, skip, timeout increase, or weakened comparator is used to hide them.

The full repository test command is `make test`, which prebuilds and shares fixture artifacts. A raw concurrent `go test` run exhausted its package-wide ten-minute budget while repeatedly compiling Rust fixtures; the maintained grouped runner avoids that redundant compilation. Do not classify that first invocation as a passing run.

The coverage last-run column defect is separately fixed by the integration owner at `6ebaa7134e`; this lane does not duplicate it. Static coverage is regenerated with `make coverage RESULTS=` and runtime results are retained separately.

### Final verified commands

- `make parity-family FAMILY=extensions-runtime`: the final integrated family passes in 168.568 seconds, including scenarios 39–43 and all predecessor scenarios with their declared durability. Scenarios 39–42 and 35/35b/35c also pass a focused durability re-probe. Scenario 43 passes three real-Pi pairs and fails under the compiling nonfatal mutation.
- `make test-subprocess test-cli test-conformance`: passes through the repository fixture scheduler after final predecessor merges. The subsequent final `go test ./coding ./internal/codingagent ./cmd/pig -count=1` also passes (9.416 s, 16.714 s, 304.288 s).
- `make test-sdk-go test-sdk-rs test-sdk-ts`: passes. Python pytest and REUSE pass using their installed cached tools.
- `go vet ./...`, Windows vet for touched mode/host/parity/conformance/TUI packages, `make lint-changed LINT_BASE=da3b0ffb4b`, and `make lint`: pass. The explicit lint base is the handoff base because this worktree has no merge base with a local `main`.
- `make ci-contracts ci-drift`: contracts, inventory/recommendation drift, SDK surface, scenario lint, port-map drift, coverage drift and divergence consistency pass. Divergence quality rejects only the unapproved D77 and D78 records. The remaining `divergence-guard`, `source-hygiene`, and `docs-drift` targets pass independently.
- `make test`: the production test packages pass; the full result remains red on the three closure assertions that require all divergence records to be approved. The final `go test ./test/parity/...` confirms exactly `TestImportCurrentDenominatorsAccountsEveryLedgerRow`, `TestDivergenceDashboardMatchesCurrentHeadingsAndScrutiny`, and `TestFoundationDashboardAccountsCurrentDenominatorsWithoutCredit` fail because 31 records are approved and two are pending. D77/D78 remain pending. No tests are weakened to treat them as approved.
- `go fix -diff ./...`: empty after applying the standard-library rewrites in the surface generator.
