<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# Extension UI state regression evidence

## Scope and reference

This lane starts at `fdcf667708`. Pi 0.87.1 is the reference implementation. The oracle is `extensions/sdk-ts/node_modules/.bin/pi`; source citations resolve under `.upstream/v0.87.1`. The design at `f50f294b62:docs/design/pi-extension-runner.md` was read without merging it. These fixes belong to its retained host composition and authoritative state boundaries (§§4.2, 4.3, 4.6), not to a second extension runner.

## Source defects and guards

| Defect | Source fix | Failing guard and Pi contract |
|---|---|---|
| A custom header replaces the built-in renderer's outer blank rows. | Put the two spacers in the host header container shared by regular and fullscreen layouts. The replaceable component supplies only content. Quiet startup supplies no outer spacers. | `TestExtensionHeaderKeepsHostSpacers`; `53-extension-header-spacers` fails before the fix and passes three escaped-output pairs. Pi `packages/coding-agent/src/modes/interactive/interactive-mode.ts:1008-1015,2467-2488`. |
| Node initializes usage to a zero object, ignores absence, and merges snapshots. The native host call also manufactures a zero object. The snapshot DTO and native Go/Rust SDKs turn null counts into zero. | Preserve absent usage, nullable counts, and replacement semantics through the current wire. Use pointers/Options in Go/Rust. Python already preserves JSON null fields. | `TestContextUsageWirePreservesAbsenceAndNull`, `TestNodeStateClearsAbsentValues`, `TestContextUsagePresenceAcrossSDKs`. Go, Rust, fused Go and packed Go/Rust fail the unknown-count row before the SDK change. Pi `packages/coding-agent/src/core/extensions/runner.ts:362-374,872-879`; `packages/coding-agent/src/core/agent-session.ts:3858-3900`. |
| Interactive extension usage uses a second, last-assistant-only calculation. It has no estimate for an empty session and truncates percentages. | Bind both direct and subprocess extension reads to the existing Session projection callback used by the stock footer. Preserve fractional percentage values. | `TestExtensionContextUsageUsesSessionProjection` fails on empty, unknown and fractional usage before the fix. The real pi-cc replay first changes from `0%/0` to `?/128k` when the fabricated object is removed; the shared Session calculation closes the remaining difference and produces Pi's `0%/128k`. Pi `packages/coding-agent/src/core/agent-session.ts:3858-3900`. |
| Omitted snapshot fields retain the previous model, session name/file, lists, prompt and flags. | Serialize explicit clears and distinguish an absent delta field from a present null/empty value. Keep session history pages incremental. | `TestNodeSnapshotClearsPreviousSessionState` fails in isolated and packed Node before the change. Pi `packages/coding-agent/src/core/extensions/runner.ts:809-886`; `packages/coding-agent/src/core/session-manager.ts:1156-1158,1316-1327`. |
| Node `pi.setModel()` mutates local state optimistically, sends a model object where the wire expects an identity string, and returns no Promise/result. | Await the existing host operation with the model identity. Publish authoritative state before sending its result. Preserve false, rejection and cancellation instead of inventing success. | `TestNodeSetModelAwaitsAuthoritativeState` is red first; the real-Pi model-transition scenario initially reports `model selection failed` on PiG. Pi `packages/coding-agent/src/core/extensions/loader.ts:406-408`; `packages/coding-agent/src/core/agent-session.ts:3087-3090`. |
| The footer provider exposes undefined instead of null for no Git branch. | Store null for absent branch state and return it unchanged. | `TestNodeStateClearsAbsentValues` and the footer assertion in `54-extension-context-usage-presence` fail first with `branch=undefined` versus Pi's `branch=null`. Pi `packages/coding-agent/src/core/footer-data-provider.ts:127-128`. |

The usage scenario runs against a compacted saved session without post-compaction assistant usage. It selects zero-window and usable-window models repeatedly and compares the complete escaped trace. A compiling runtime mutation changes null token/percentage counts back to zero. The scenario fails with `{tokens:0,percent:0}` versus Pi's `{tokens:null,percent:null}`. No comparator is weakened and no timeout is increased.

The header unit test covers ordinary, internally blank, empty and replacement frames, restoration, and quiet startup. Existing startup and fullscreen scenarios check unchanged built-in spacing and the shared mount paths.

## Runtime default audit

The audit covers every field in the Node runtime's initial `state` object and the externally read `ready`/registry defaults. Internal handler maps, pending-call queues and component caches are not extension-visible host facts.

| Fields | Disposition |
|---|---|
| `contextUsage` | Initialize to undefined. A present null clears it; unknown `tokens`/`percent` remain null. Remove the duplicate runtime-level usage field. |
| `model`, `ready.model` | The context's absent model stays undefined. Explicit null clears the replica. Remove the setter's fabricated partial model. |
| `session.sessionName`, `session.sessionFile` | Empty host strings are the Go carrier for absence. Existing public getters return undefined. Always transmit clears so an old name/file cannot survive. |
| `session.leafId`, entries, metadata | The public leaf getter already returns null, missing entries/labels return undefined, and `getHeader()` returns null. Empty entries and the host's session ID/directory are real session facts. Keep the existing session-identity reset and bounded page protocol. Pi `session-manager.ts:1413-1423,1503-1563`. |
| `activeTools`, `allTools`, `commands`, `allThemes` | Empty collections are valid, not absence. Transmit null/empty clears and rebuild empty arrays rather than retaining the previous collection. |
| `thinkingLevel` | The context getter already converts the unbound empty carrier to undefined; host-bound values come from the active session. Transmit an explicit empty clear. Pi's API action is guarded before binding (`loader.ts:149-179,411-413`). |
| `systemPrompt`, `systemPromptOptions` | Pi returns an empty prompt before binding and an options object, not absence (`runner.ts:372-374`). Clear empty prompt snapshots and remove the duplicate prompt fallback. Options remain supplied by the existing host binding. This audit does not claim full system-prompt-option parity. |
| `flags` | Missing flags already return undefined and false values survive `??`. Clear the replicated map explicitly. Factory defaults remain in the existing registration state. |
| `isIdle`, `projectTrusted`, `hasPendingMessages`, `hasUI` | True/true/false/false are Pi's real unbound defaults (`runner.ts:320-374`). Existing snapshots preserve false values and UI identity. |
| `editorText`, `toolsExpanded` | Empty text and false are Pi's no-UI defaults (`runner.ts:338-351`). Both fields already carry explicit clears. |
| `footerData` | No branch is null. Empty status maps and provider count zero are real initial values (`footer-data-provider.ts:103-113,127-163`). Preserve branch changes and clearing. |
| `ready.mode` | Default to Pi's `print`, not `tui` (`runner.ts:357`). The existing widget-layout unit fixture now explicitly binds TUI mode instead of relying on the incorrect default. |
| `ready.cwd`, geometry, registry | The host supplies cwd and terminal geometry before dispatch. The geometry additions are PiG-owned. Empty registry maps/lists do not fabricate an available model, provider, key or error; existing lookups preserve absence. No change is needed for this absent-value audit. |

## Locked npm replay

The replay copies the original corpus installs into new private homes. It does not install newer npm packages or modify the original locks. It uses the integration corpus's local scripted OpenAI-compatible provider and 170×55 tmux panes. For each package, Pi runs before PiG: print `hello`, startup, the package command, then another model turn. The original observation windows remain unchanged.

| Package | Version | Lock SHA-256 | Command |
|---|---|---|---|
| `@companion-ai/feynman` | `0.5.8` | `378b5990dfb8eb6a4bf410a413d2aca64a870ca22f133a6c4c5b449c703482fd` | `/service-tier` |
| `pi-cc-extensions` | `0.9.5` | `38664d42223466821d73641a3dbc16d385882a44b3295d0e2543bb71d888163c` | `/ccstyle` |

The final startup, command and turn escaped panes match for both packages. Only Feynman's independently generated, truncated session UUID is replaced with a same-length constant. Every row, space, ANSI sequence and other field remains in the comparison. All four print runs exit 0. pi-cc displays `0%/128k`, and both headers keep the same surrounding rows as Pi. pi-cc's fallback source is its locked `extensions/feature/shell/footer.ts:324-327`.

Raw scripts, logs, lock/integrity records and panes are retained under `tmp/fix-ext-ui-state/` in this worktree. `corpus-red` contains the baseline; `corpus-green` retains the intermediate `?/128k` observation; `corpus-release` contains the final replay. `compare-corpus.py` records the allowed UUID substitution. `evidence-manifest.json` identifies retained files by SHA-256. No package process is retained after replay cleanup.

## Resource behavior

The header still renders cached component lines. The container adds no IPC, history lookup, worker or callback lifetime. State publication and model selection run on existing extension workers, not on the TUI input/render loop. The model call uses the existing request cancellation and connection cleanup. Session usage no longer has a second history traversal algorithm in the UI bridge. Ordered session paging is unchanged.

On Linux amd64, Go 1.27.1, Intel Xeon 6746E, three samples of `BenchmarkExtensionHeaderContainer` at width 100 with five content rows measure 1.07–1.13 µs/op, 1,456 B/op and 5 allocations. `BenchmarkNoUIBridgeSnapshot` measures 155–166 ns/op, 288 B/op and 1 allocation. CPU/allocation profiles identify container row assembly and defensive copies, not extension IPC. These are current-cost measurements, not claims of a measured speedup. Profiles and reports are in the evidence directory.

## Verification

| Command | Result |
|---|---|
| `go test ./...` with maintained fixture exports | Only the three inherited `test/parity/closure` approval/accounting assertions fail. The subprocess package passes, as do coding, UI, CLI, conformance and module-hash tests. |
| `go test ./test/parity/...` | The same three inherited closure failures; the other parity packages pass. |
| `go test ./test/gomodule ./coding/extension ./internal/codingagent ./extensions/sdk/... ./test/extension-conformance` | Pass. |
| Focused `go test -race` on the header, usage, snapshot and model-state regressions | Pass. |
| Rust SDK `cargo test` | 26 unit tests and one doctest pass; one existing factory example doctest remains ignored. |
| `make parity-family FAMILY=extensions-runtime` | Pass at declared durability. The final two new scenarios independently pass three escaped-output pairs each. |
| `make parity-family` for `startup`, `interactive-rendering`, `footer`, `fullscreen` | Pass for runnable hermetic scenarios. The two existing credential-dependent footer scenarios remain unexecuted. |
| `go vet ./...`; Windows vet for touched extension, UI, conformance and Go SDK packages | Pass. |
| `go tool golangci-lint run` for touched packages and the Go SDK; lint configuration verification; `make lint`; `make lint-changed LINT_BASE=fdcf667708` | Pass, with no suppressions. The explicit base is necessary because this lane checkout has no local `main` merge base. |
| `make -k ci-contracts ci-drift` | Only the inherited 358 open hot-path upstream-test obligations and missing D77/D78 approvals fail. Interface drift, scenarios, coverage, source hygiene and remaining targets pass. |
| `make coverage coverage-drift lint-scenarios interface-go-drift`; `go fix -diff ./...` | Pass; go-fix emits no diff. |

The three inherited full-Go failures are `TestImportCurrentDenominatorsAccountsEveryLedgerRow`, `TestDivergenceDashboardMatchesCurrentHeadingsAndScrutiny`, and `TestFoundationDashboardAccountsCurrentDenominatorsWithoutCredit`. They are the same release blockers recorded in `docs/release/0.2.1-integration.md`. This lane does not approve D77/D78 or close unrelated upstream tests.

Development failures also identify two fixture mistakes: the new model-call mock initially rejected the header/footer clears produced by a state update, and the compacted-session scenario initially used a path relative to the wrong working directory. Both fixtures are corrected without relaxing behavioral assertions. A transient duplicate comma in the Node edit causes syntax failures in exploratory runs and is removed before the final complete suite. Those runs are retained, not counted as behavioral mutation evidence. The SDK checksum is regenerated from `TestRootGoSumPinsNestedModuleHashes`. The final corpus binary SHA-256 is `0340144e5f4fbef24110833b40a7a553cb7517693c731958dfcd4ec9c0bfe7e2`.
