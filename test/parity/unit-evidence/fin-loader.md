<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# fin-loader: Pi 0.87.1 corpus loading and widget layout

## Scope and reference

This slice finishes handoff `776779563bb4f66a9937161ba46bc57506da7f39` on the `fin-loader` completion branch. It owns corpus causes E/F, the delayed powerline welcome overlay, empty custom footers, and above-editor widget layout. It has no predecessor in the handoff merge order. No new divergence ID is used.

The reference is the published Pi 0.87.1 CLI in `extensions/sdk-ts/node_modules/.bin/pi`, with source in `.upstream/v0.87.1`. The host is Linux x86_64. Verification uses Go 1.27.1, Node 24.19.0, Python 3.12.3, Rust/Cargo 1.97.1, and tmux 3.7b. npm 11.17.0 is installed; the corpus replay uses existing lockfile-pinned package copies and does not resolve new npm versions. `rg` and `fd` are on PATH.

Upstream references below are relative to `.upstream/v0.87.1/packages/`.

| Contract | Pi source | PiG repair |
|---|---|---|
| Published CLI loads every extension with jiti, virtual modules, `moduleCache: false`, and `tryNative: false` | `coding-agent/src/core/extensions/loader.ts:36-50,477-509` | Vendor jiti 2.7.0 unchanged; use its resolver, transform, CommonJS globals, JSON handling, and interop in isolated and packed Node cells. |
| Extensions see Pi's process markers/title and can relaunch the current harness through `execPath argv[1]` | `coding-agent/src/cli/setup.ts:4-9`; compiled package directory: `coding-agent/src/config.ts:389-401` | Preserve Node's `execPath`; provide Pi-visible identity and a host-owned PiG relaunch entry. Keep potentially long/private argv in a mode-0600 file, not inherited environment values. |
| A handler's independent scheduled work is not part of its completed invocation | `coding-agent/src/core/extensions/runner.ts:988-1017` | Mark Node request stores settled before replying. Later host calls inherit the extension connection lifetime, not a dead parent request. Active requests still propagate cancellation. |
| A present footer component replaces the built-in footer even when it renders no lines | `coding-agent/src/modes/interactive/interactive-mode.ts:2427-2445` | Distinguish an empty frame from clearing the footer. |
| Above-editor widgets have one leading spacer, including the empty set; string-list entries use `Text(line, 1, 0)` and first-ten-entry truncation | `coding-agent/src/modes/interactive/interactive-mode.ts:2322-2331,2394-2422` | Move the gap out of `tui.Editor` into the widget container. Render Node lists with the vendored Pi `Container`/`Text`, including width updates. Keep component construction off the host render loop. |
| An Editor starts at its border; mouse coordinates are component-local | `tui/src/components/editor.ts:556-623,630-679` | Remove the extra editor row and adjust mouse/autocomplete offsets. Update the component tests and the reviewed autocomplete golden with its owning regeneration command. |
| RPC widgets carry raw strings and placement, not rendered terminal rows | `coding-agent/src/modes/rpc/rpc-mode.ts:195-208` | Keep Node RPC string lists unrendered. Send one serialized request without also publishing a cached TUI widget or a synthetic clear on shutdown. |

The packed cell key also includes all embedded runtime bytes. `TestNodePackedCellKeyCoversTheEmbeddedRuntime` substitutes a different runtime digest and requires a different key. This prevents an upgrade from reusing the previous loader/runtime.

`widget-component.mjs`, the harness bridge, the request-lifetime fix, the footer fix, and the cache digest carry into the runner architecture. The loader/API glue blocks use the requested `0.3.0: replaced by Pi runner wiring` marker. The editor now starts at row zero; a custom-editor adapter must not strip or add an editor-owned spacer.

## Red, mutation, and green evidence

Artifacts are retained under `/tmp/fin-loader-evidence/`. The mutation builds use Go's `-overlay` with disposable file copies. They never alter the worktree while another gate runs.

| Regression | Before or compiling mutation | Result after repair |
|---|---|---|
| `TestNodeExtensionModulesLoadLikePinnedPi` | Replace jiti import with native import; both topologies fail with `ERR_IMPORT_ATTRIBUTE_MISSING` on the fixture JSON import. `node --check` succeeds first. | Isolated and packed imports match the pinned jiti probe across package imports/exports, emitted suffixes, index files, JSON, CJS/ESM interop, CommonJS globals, and import metadata. |
| `TestNodeExtensionSeesPiProcessIdentity` | Substitute title `node`; both topologies report the wrong title while the rest of the probe still executes. | Correct title, argv, warning suppression, package identity, package directory, and relaunch argument delivery. |
| `TestNodeUICallScheduledByAFinishedHandlerRuns` | Return a settled AsyncLocalStorage request from `activeRequest`; the late overlay cannot open/render in either topology. The mutated JavaScript parses and the Go test binary compiles. | A deterministic gate releases work only after the first command returns; both topologies open, render, accept input, close, and shut down. This replaces the inherited test's timer race. |
| `TestExtUIContextEmptyCustomFooterReplacesTheBuiltInFooter` | Require a nonempty frame before suppressing the built-in footer; its cwd and `(auto)` rows reappear. | Empty frame suppresses the footer; clearing restores it. |
| `TestAboveEditorWidgetSpacerPrecedesWidgets` | Current handoff returns no row for the empty widget set. The original scenario also shows a blank row between the widget and editor. | Empty, populated, and cleared widget sets retain the leading gap and put the border immediately after the widgets. |
| `TestNodeStringWidgetsUsePiTextLayout` | Raw `ordinary` differs from Pi Text's padded row in both topologies. | Empty, ordinary, multiline/ANSI/wide text, overflow beyond ten entries, replacement, and resize match the pinned Pi Text implementation. A nondefault muted color distinguishes truncation styling from an unstyled fallback. |
| `TestUIBridge_HandleCall_SetWidget_RequestObserverPreservesPlacement` | A serialized request also updates the TUI cache. | No duplicate cache publication; raw lines and placement are preserved. |
| `TestResolvePigBinIsolatesInheritedAgentDirectory` | The runner leaves `PIG_CODING_AGENT_DIR` inherited from its caller. | Explicitly clear the inherited override so the temporary `PIG_HOME`, including a scenario's own home fixture, remains authoritative. |
| `34-empty-custom-footer` | Build the handoff layout with an overlay; the complete escaped capture fails on both the misplaced gap and missing widget indent. | Three exact escaped-output pairs pass, from a fixed chat notification through the widgets, editor, and empty footer. |

The final footer scenario strengthens `output_normalized_equal` to `escaped_output_equal` and expands the crop above the widget. It does not crop away the failing gap. `rpc/19-rpc-extension-ui-select` strengthens substring-only checks to complete `output_normalized_equal`; only generated UI request UUIDs are replaced. This guards the raw widget payload, placement, event order, duplicate emission, and command response together.

The inherited module-loading, process-identity, and delayed-overlay scenarios move from 31–33 to loader's reserved `34-` prefix. Module-loading and process-identity durability increases from one to three pairs. No existing comparator is weakened.

## Corpus replay

The before column is the coordinator's corpus recording on `da3b0ff`, not a newly invented baseline. The after column is a fresh run on this machine. Each Pi/PiG home gets a separate copy of the same installed npm closure and settings. The replay uses the original scripted OpenAI-compatible server on its own port, print `hello`, and tmux 170×55 startup/command/turn captures. The package locks in the artifacts retain full dependency integrities.

| Package and pinned version | Before | After vs Pi |
|---|---|---|
| `confluence-cli@2.25.2` | Cannot load: `__dirname is not defined in ES module scope`. | Loads; print exits zero with `42`; startup and turn screens match after application-name/path/time normalization. The package has no literal main command in the original corpus driver. |
| `@henryqw/pi-subagent@22.2.3` | Cannot load: requires active Pi process identity. | Loads; startup, `/subagent`, and turn screens match under the same normalization. The real ephemeral executor relaunches PiG and returns exactly Pi's result object: success, exit 0, output `42`, stop reason `stop`, input 10/output 5/total 15, zero cost, empty stderr. |
| `@gotgenes/pi-permission-system@34.0.1` | Cannot load: unresolved extensionless `#src/...` import; `getPackageDir` is also unavailable. | Loads and opens `/permission-system` settings. Full runtime compatibility is **not** claimed: `getSessionDir` and unsubscribe-return errors remain the runtime/registry lanes' H/J integration blockers. |
| `pi-powerline-footer@0.18.0` | Welcome overlay never opens; empty custom footer leaves the built-in footer drawn. | Welcome opens, the built-in footer stays suppressed, and the widget gap matches Pi. After an explicit single-key dismissal, `/powerline` produces the same complete command screen. Custom-editor prompt/border differences remain for pivim; recent-session data and the initial token estimate also differ and are not normalized away or claimed equal. |

The original timed corpus sequence can type `/powerline` while the welcome overlay still owns input. Its command screen therefore is not a reliable command-completion assertion. A second controlled probe waits for the welcome, sends one Space, waits for closure, and then types `/powerline`. Both display `Powerline disabled`; their full command screens match after the application-name replacement. The raw first sequence is retained alongside the controlled probe, not hidden.

Top-level package integrity pins:

- permission-system: `sha512-0iJr3H2FVD12zYvSLMjozxjtKZvQfnCpKSTK/PLjRtoHpB6emYAAKLNAxSLC4jypd/kf5gEst8EUMXLEcCFOPg==`
- confluence-cli: `sha512-7g85CkfZU87ChTJ01qqZ9LFSanlJgiSW3FkYlYNpqkSQKUhkzGG1sVk+iJE0s9eS46BL2ETJdbIdmFJREQayPA==`
- pi-subagent: `sha512-9hc+kckMzJj+u8t8uAPJo867+rIywx1I3/J1wGvK1lKUJcsweA3XA9HXTydmfbLAUTOlDKyUOFg2plHmf+cJbg==`
- powerline-footer: `sha512-lVzhreltNGubR2xbUrwUbIpxMZfhQHhSCP2LacnQ+QOUQUXDyuRW5SKW+cqF/c2gyyPG9zry2a9DiSD3R2FSjA==`

## Verification commands and dispositions

- `go vet ./...` and Windows vet for the touched subprocess, codingagent, TUI, and TUI parity packages pass. The scenario runner is build-tagged test infrastructure and is exercised with `-tags=parity` on Linux, not included as an empty Windows package.
- `go tool golangci-lint config verify`, touched-package lint, and `make lint` pass with zero issues. No suppression is added.
- `make lint-changed LINT_BASE=0c5e9fce8e` passes. This checkout has no local `main` ref, so the explicit base is the handoff's parent; the default command reports that missing ref rather than silently choosing another base.
- `make ci-contracts ci-drift` passes after `make coverage RESULTS=`. An empty results argument avoids importing another lane's machine-local parity-results file. The Go interface inventory is unchanged by this completion.
- `go test ./test/parity/...` and the parity-tagged environment regression pass.
- `go test -race ./tui` passes.
- `make test` passes with the maintained fixture prebuild and bounded scheduler. The initial bare `go test ./...` exhausted its unchanged ten-minute package budget while rebuilding Rust fixtures; the only running leaf test had run for eleven seconds. It also exposed the stale standalone-editor golden, which is corrected from Pi's border-first contract using `UPDATE_GOLDEN=1 go test ./internal/tui/parity -run '^TestParityEditorAutocompletePopup_GoldenVisibleState$'`. No timeout is increased and no test is skipped. Final direct-suite verification passes with `eval "$(./automation/ci/test-fixtures.sh --exports)"; go test ./...` and the same ten-minute default: cmd/pig 222 s, subprocess 203 s, and extension conformance 32 s.
- Paired durability runs cover `extensions-runtime`, `interactive-rendering`, `startup`, `print`, `json`, `rpc`, `fullscreen`, and `autocomplete`. The RPC re-probe detects and closes the raw-list/duplicate-publication regression instead of weakening its assertions.

### Performance and ownership

`BenchmarkAboveEditorWidgetLayout` measures a cached widget plus editor at width 120. Before: 2.20–2.44 µs/op, 3856 B/op, 12 allocations. After: 2.25–2.61 µs/op, 3904 B/op, 12 allocations. These are shared-machine samples, not a speed claim. The additional container row costs 48 B per render in this benchmark. CPU/allocation profiles are retained as `widget.cpu`, `widget.mem`, and `pprof-*.log`; allocation is dominated by `Container.renderBorrowedLocked` (94%), with no IPC or JSON on the render path.

Widget text is constructed and rerendered in Node, capped at Pi's first ten entries plus its truncation notice. Existing width-tagged frames and replacement/clear/shutdown ownership remain in use. The timer regression closes the overlay and shuts down each Host. Harness argv files live in the Host's private runtime directory and are removed with it. The corpus driver terminates only its own named `parity-*` tmux sessions and its own scripted server.

### Integration ownership

The coordinator owns the separately reported exact `/btw` versus `/skill:btw` completion-precedence regression. This slice does not change the command catalog or skill ordering. Permission-system H/J gaps and powerline custom-editor/context data remain explicit cross-lane integration obligations, not loader parity claims.
