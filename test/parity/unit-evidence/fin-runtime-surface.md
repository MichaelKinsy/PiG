<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# fin-runtime-surface: runtime helpers and first-paint Markdown transforms

## Scope and reference

This slice finishes corpus causes A/D/G/H and extension load-error/console reporting from handoff `4724431916e3e6198383bba0703fb6e80631bd7e`. It merges loader handoff `776779563bb4f66a9937161ba46bc57506da7f39` and its completed replacement `749215aeab`. Registries and SDK-surface are successor slices, not predecessors of this branch.

Pi 0.87.1 is the oracle. Sources are under `.upstream/v0.87.1/packages/`; the CLI is `extensions/sdk-ts/node_modules/.bin/pi`. Verification runs on Linux x86_64 with Go 1.27.1, Node 24.19.0, Rust/Cargo 1.97.1, Python 3.12.3 and tmux 3.7b. npm 11.17.0 is installed; no new npm resolution is used. The existing Python test environment provides pytest 9.0.3, below the SDK's declared development dependency of 9.1.1; its 20 tests pass, but this is not qualification of that declared tool version.

Artifacts remain on this machine under `/tmp/fin-runtime-logs/` and `/tmp/fin-runtime-corpus/`. The checked-in scenarios and tests are the repeatable acceptance guards. The corpus is exploratory evidence, not a claim that every exercised package is fully compatible.

## Upstream contracts and source fixes

| Contract | Exact Pi source | Repair and evidence |
|---|---|---|
| Pi supplies pi-agent-core as a virtual module; Agent uses the configured stream function | `coding-agent/src/core/extensions/virtual-modules.ts`; `coding-agent/src/core/sdk.ts:35-39` | Keep the pinned Agent/chord/telemetry closure and both namespace aliases. Remove the stale no-shim exception from `TestNodeRuntimeLoaderCoversPiVirtualModules`. The export and vendor-provenance tests cover the pinned denominator. |
| Shared bus invokes listeners synchronously with the original object and returns an unsubscribe function | `coding-agent/src/core/event-bus.ts:11-31`; `coding-agent/src/core/extensions/loader.ts:436-449` | Use Pi's actual bus within the packed Node process. Factory failure drops its subscriptions. `TestNodeRuntimeEventBusMatchesPi` and scenario 31 exercise this path. Separate-process delivery remains D77, pending approval. |
| Unsubscribe does not revoke an already selected callback; new subscriptions affect subsequent dispatches | `coding-agent/src/core/extensions/loader.ts:256-271`; `coding-agent/src/core/extensions/runner.ts:265-267,992` | Send identity-bearing subscribe/unsubscribe changes through the existing host calls. Retain callable identities for admitted host snapshots instead of skipping a removed handler. `TestNodeEventSubscriptionChangesApplyToNextSnapshot` and scenario 31 cover removal and registration during dispatch. |
| A context has enumerable own properties, including `ui`; reads remain live | `coding-agent/src/core/extensions/runner.ts:809-886` | Replace prototype-only request contexts with enumerable forwarding descriptors and request-owned signals. `TestNodeRequestContextSpreadsLikePi` catches the `pi-cc-extensions` `{ ...ctx }` crash. Scenario 31 compares the spread through a real command. |
| Markdown applies each transform in order before parsing; exceptions and non-string values preserve the current input | `coding-agent/src/modes/interactive/components/markdown-transform.ts:18-29`; `tui/src/components/markdown.ts:278-285` | Await the complete chain on a component-owned worker before publishing new content. The renderer performs no IPC. Mark the parent chat block dirty when a result arrives. Remove the extension-wide answer cache and `MarkdownTransformerState`. Retire D76. |
| Runner returns registered transformers in extension order | `coding-agent/src/core/extensions/runner.ts:725-728` | Expose `GetMarkdownTransformers`, remove the stale deferred-method entry, and update production/conformance callers. Do not classify this as an additive method. |
| Display diffs and unified patches preserve line/context/newline semantics | `coding-agent/src/core/tools/edit-diff.ts:373-516` | Re-export Pi's pinned module, including its path/child-process closure and exact cross-spawn dependencies. `TestPiDiffHelpersMatchThePinnedPackage` checks empty, unchanged, add/delete, CRLF, missing final newline, Unicode and separated changes at several context sizes. Scenario 31 exercises both public exports. |
| Built-in definitions preserve metadata and execute at `ctx.cwd` | `coding-agent/src/core/tools/tool-definition-wrapper.ts:5-39` and the read/bash/edit/write/grep/find/ls factories | Retain the handoff's working definitions and host-renderer selection. `TestPiToolFactoriesMatchThePinnedPackage` compares keys, metadata and execution with Pi. Direct marker-renderer calls remain D73. |
| Factory failures report the actual loader message; successful RPC setup has no host tool inventory | `coding-agent/src/core/extensions/loader.ts:537-557`; `coding-agent/src/modes/rpc/rpc-mode.ts` | Retain structured factory failure transport and owned console writers. Remove unconditional RPC tool-inventory diagnostics without suppressing extension-authored console output or actionable errors. `TestNodeLoadFailureReportsPiLoaderMessage`, `TestRPCDoesNotPrintHostToolInventories`, scenarios 31/32 and strengthened scenario 21 cover the boundary. |

The vendored modules, generator, host transform bridge, SDK registration and Go rendering integration carry into the runner architecture. API construction, request-context construction, registration dispatch and factory orchestration remain thin Node wiring. The change adds no second main Agent loop and no independently versioned wire format.

## Red, mutation and green evidence

| Regression | Observed failure before the source fix | After |
|---|---|---|
| `TestPiDiffHelpersMatchThePinnedPackage` | `generateDiffString is not available to extensions running in PiG`; a valid Node run reaches the throwing export. | All cases equal Pi's exact return values and patch strings. |
| `TestMarkdownTransformFirstPaintWaitsForWholeChain` | The transform blocks the simulated owner loop. After moving it off-loop, a stronger container-level test exposes the missing parent invalidation: the completed text remains absent. | The owner loop returns before the blocked callback; neither raw nor half-transformed text is painted; the completed chain appears through the chat container. |
| `TestNodeMarkdownTransformerDoesNotCacheAcrossMessages` | A compiling mutation restores early raw-input return: both strict and packed cases return `same` instead of `same:user:78:1`. | Separate identical inputs invoke the stateful transformer separately; exceptions and invalid returns preserve input. |
| `TestAsyncMarkdownReplacesPendingGeneration` | Structural regression guard added with the worker; no pre-fix test of this new worker type is claimed. | A blocked active request plus 1,000 replacements runs only the active and last pending generation; the obsolete result cannot publish. |
| `TestAsyncMarkdownOwnerCancellationDrains` | Structural regression guard added with the worker; no pre-fix test of this new worker type is claimed. | Cancellation drains the worker and prevents a late paint or restart. |
| `TestNodeEventSubscriptionChangesApplyToNextSnapshot` | Both placements record `first, first`; the selected second callback is suppressed and the dynamically registered third callback is never sent to the host. | Both record `first, second, first, third`. |
| `TestNodeRequestContextSpreadsLikePi` | Both placements fail with `context spread lost ui`; the real corpus fails in `compact-thinking.ts:986`. | All checked fields survive object spread, including the active theme. |
| `TestRPCDoesNotPrintHostToolInventories` | stderr contains `pig --rpc: loaded 1 extensions, 0 total tools` and `pig --rpc: agent has 4 tools ...`. | Successful RPC setup is quiet; the protocol response still arrives. |
| `TestNodeHeadlessConsoleUsesOneOrderedPipe` | Print/JSON/RPC configured unequal writers, giving Node two pipes and racing their host copy goroutines. | Headless stdout/stderr share one pipe as Pi's stdout takeover requires; interactive destinations stay separate. |
| `TestRunner_*` | `MarkdownTransformers` is unclassified; the handoff's `MarkdownTransformerState` has no upstream counterpart. | Use the exact upstream accessor name and remove the cache-state API. |

No comparator is weakened. Scenario 21 adds complete `output_equal` beside its artifact comparison and increases durability from one to three pairs. Scenario 33 retains `escaped_output_equal` and increases durability from one to three pairs. Scenario 31 retains complete `output_equal` with three pairs and now covers real diff helpers, callback snapshot behavior and context spreading. A later scenario-24 durability pair captured provider completion before final asynchronous tool frames; scenarios 24/25 now await the final call/result markers they compare. Their escaped comparators, crop and existing timeouts are unchanged. This is a D56 renderer-completion barrier, not a retry or extra sleep.

The reported 21/24/25 integration failures were reproduced with the inherited `PIG_CODING_AGENT_DIR` override. It bypassed the scenarios' temporary homes, leaking settings such as output padding. Loader `749215` clears that override unless a scenario explicitly sets it. After merging that source fix, the entire extensions-runtime family passes with the original parent environment still present. The original scenario comparators remain in force. The old Markdown proxy had a separate real bug: requesting a paint did not invalidate the parent container's cached block, so an untransformed frame could persist indefinitely. The new parent-invalidation regression closes that cause as well as eliminating the initial raw frame.

## Corpus before and after

The before column comes from the coordinator's recorded `da3b0ff` sweep in `CORPUS-REPORT.md`. The after column is a new local run. Each Pi/PiG pair uses a fresh HOME and working directory, the same copied installed npm closure, explicit isolated agent directories, raw Node on PATH, and the scripted OpenAI-compatible provider. No real credentials are inherited. Print runs use `hello` with closed stdin. Interactive captures use tmux 170×55 and retain startup, command and turn output with escapes.

The full 46-package rerun is in `/tmp/fin-runtime-corpus/out-reviewed/`. `fin-runtime-surface-corpus.md` records the ordered independent package denominator, package versions, manifest hashes and each pair's exit status. Pi completes print mode for 46/46 packages; PiG completes it for 42/46. These are load/print observations, not full compatibility counts. `result.json` in each artifact directory records the command and pair details. The exact candidate executable hash is in `/tmp/fin-runtime-logs/corpus-reviewed-binary.sha256`. This run contains the completed runtime fixes; the subsequent loader merge, RPC diagnostic removal and headless console pipe-ordering fix are separately re-probed by their owning parity families. A startup/print pass is not deep behavior proof.

| Package | Before | After vs Pi and remaining scope |
|---|---|---|
| `pi-subagents@0.71.0` | Load failure: missing pi-agent-core. | Both load, print exits 0, and `/subagents` opens `Select subagent`. PiG still reports missing SessionManager/session identity operations supplied by the registries successor. Independent child sessions remain unsupported as detailed below. |
| `pi-cc-extensions@0.9.5` | Load failure: missing `registerMarkdownTransformer`. | Both load and print exits 0. The later compact-thinking `{ ...ctx }` theme crash is fixed at request-context construction. Its prototype patches against Pi's live main UI and missing key hints are not claimed equivalent to the Go terminal. |
| `gentle-pi@3.7.0` | Load failure: throwing `createReadToolDefinition`. | Both load and print exits 0. Both diff helpers now match Pi in independent guards. PiG still reports missing `getCwd`, editor access and D73 `ScrollView`; full dashboard behavior is not claimed. The heuristic `/gentle` probe can select a Skill rather than a command, so it is not command acceptance evidence. |
| `pi-goal-x@0.31.9` | Runtime error: `this.unsubscribe is not a function`. | No unsubscribe error; both enter guided goal drafting and show the same goal instruction text. |
| `@henryqw/pi-task-models@7.0.2` | `/task-models` fails with `off is not a function`. | Both open the `Task models` selector and show the expected missing-config warning in a fresh home. |
| `@raindrop-ai/pi-agent@0.2.4` | Extension console output is hidden. | Both print the same version/info and missing-key warning; print exits 0. |
| `pi-web-access@0.31.0` | Extension version warning is hidden. | Both print the same dynamic-tool warning; print exits 0. PiG's summary model picker still needs the registries successor's `getAvailable`; browser launch fails on both headless runs. |
| `@henryqw/pi-subagent@22.2.3` | Load failure: active Pi identity required. | Both load and open the selector with the same fresh-home warnings. The loader predecessor owns the independently verified harness-relaunch path. |
| `@gotgenes/pi-permission-system@34.0.1` | Load failure: unresolved `#src/...` import. | Both load and open permission settings. PiG's `getSessionDir` errors remain for the registries successor. |
| `confluence-cli@2.25.2` | Load failure: missing `__dirname`. | Both load, print exits 0, and the scripted turn completes. No literal main command is claimed. |

The full rerun retains the four package-resource load failures from other slices: `@plannotator/pi-extension`, `context-mode`, `agent-comms`, and `@kontextmind/kxm`. Other nonzero feature diagnostics remain visible in the raw captures. No failure is converted into a skip or a screen-equality claim. The initial exploratory run accidentally inherited the harness agent directory and was discarded; the clean runs explicitly isolate it. Temporary corpus files were moved out of the worktree before the final contracts run so snapshot/format gates do not ingest extension-owned state.

### Independent pi-subagents sessions

`pi-subagents/src/runs/shared/child-session.js:159-228,276` loads the SDK from the running host's package root, creates a ModelRuntime, creates SettingsManager and DefaultResourceLoader instances, then calls `createAgentSession`. A paired probe of that actual `loadHostPiCodingAgent()` function produces:

- Pi: `createAgentSession`, `ModelRuntime.create`, `SettingsManager.create`, and `DefaultResourceLoader` are functions.
- PiG: `ENOENT ... runtime/harness/index.js`.

Raw outputs are `child-sdk-pi.out` and `child-sdk-pig.out` in the log directory. A bare-specifier fallback would not solve this: the current shim still lacks the independent ModelRuntime/resource/session construction path. These classes are not intrinsically impossible in a subprocess. Supporting them requires the complete Pi SDK dependency closure, independent child ownership, settings/store locking, provider inheritance and child cancellation/shutdown. This slice does not substitute the main Go Session for an independent child. D73 now records the precise failure and required boundary instead of calling every imported class inherently main-process-only.

## Performance and lifetime

`BenchmarkAsyncMarkdownCached` renders a retained 23 KB Markdown message at width 80. On this host it measures 30.94 ns/op, 0 B/op and 0 allocations/op. This is an absolute sample, not a before/after speed claim. CPU and allocation profiles are retained as `markdown.cpu`, `markdown.mem` and the two `markdown-*-top.log` files.

Each Markdown component owns at most one active and one replaceable pending transform. Replacing input cancels the active request and rejects its late result. The UI owner supplies cancellation and joins its tasks on shutdown. The subprocess proxy owns no detached goroutine or cross-message cache. Streaming segments retain their Markdown components instead of recreating a new worker on every prefix. Connection closure and D56 renderer inactivity end requests. Race tests exercise the component/parent publication path and strict/packed Node request paths.

## Gate results and integration disposition

- Focused unit tests and the complete extensions-runtime family pass after the loader merge. `interactive-rendering`, `print`, `rpc`, and `startup` family durability also pass in this run.
- `go vet ./...`, Windows vet for touched packages, `golangci-lint config verify`, touched-package lint and `make lint` pass. `make lint-changed LINT_BASE=749215aeab` uses the inspected predecessor because this checkout has no usable local `main` merge base. No lint suppression is added.
- `make ci-contracts` passes after regenerating the Go interface inventory and recommendations and running `make coverage RESULTS=`.
- `make ci-drift` reaches divergence quality and rejects D77's unapproved status. This is an intentional release blocker, not a waived gate. Remaining drift targets pass independently.
- Go/Node/Rust/Python/fused conformance passes. The nested Go SDK and Go-module tests pass. Rust SDK tests and TypeScript declaration/runtime checks pass. The Python SDK's 20 tests pass with the installed pytest qualification caveat above.
- Focused TUI/codingagent and subprocess race tests pass. Vendored dependency identity tests and `reuse lint` pass.
- The first bare full Go test run exhausted the unchanged ten-minute package budget while rebuilding Rust fixtures. The maintained fixture prebuild removes that redundant work; no timeout is increased. A concurrent loader merge interrupted one CLI test's build with conflict markers, so that interrupted result is not reported as a product failure or as successful verification.
- Final touched-package verification passes: subprocess 223.397 s, cmd/pig 245.365 s, extension conformance 35.432 s, and the runner/API parity gate. The full Go suite is not release-green: its closure tests reject pending D77. After regenerating coverage, the stable `go test ./test/parity/...` rerun fails only the three D77 approval/accounting assertions. Approval is requested explicitly; this branch does not preserve the inherited false approval marker.

D76 is retired. D77 applies only between distinct processes; the default colocated Node bus is Pi's actual bus. Strict isolation is not silently weakened. No new divergence ID, permissive comparator, normalization hiding a defect, or test skip is introduced.
