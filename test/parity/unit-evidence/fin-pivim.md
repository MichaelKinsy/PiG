<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# fin-pivim: SettingsManager, CustomEditor and external editor handoff

## Reference and scope

The reference is Pi 0.87.1, from `.upstream/v0.87.1` and the installed published CLI at `extensions/sdk-ts/node_modules/.bin/pi`. This lane starts from `2ce279f060`, merges the handoffs in loader → runtime-surface order, then merges completed loader `749215aeab` and runtime-surface `f04db0b7c3`. It retains shared handoff ancestry rather than rebasing or force-pushing it. The editor source checkpoint is `08cd9f1f34`. The reported package is `pi-vim@0.14.2`, reported by slypheed on Reddit.

Artifacts live under `/tmp/fin-pivim-evidence`. The qualified final replay uses Go 1.27.1, Node 24.19.0, Rust/Cargo 1.97.1 and tmux 3.7b. The first family runs used the `flakes3-tools/tmux` wrapper, which is tmux 3.4; the final qualified runs put the real 3.7b directory first on PATH. npm 11.17.0 differs from the documented qualification version. The corpus replay uses the existing pinned npm package, not a fresh unpinned npm resolution. Node's real executable directory precedes mise's shims when the replay gives each binary a new HOME.

## Root causes and boundaries

- The old shim exported an empty SettingsManager class. Pi's `core/settings-manager.ts:354-405` supplies synchronous factories, merges, migration and file-backed storage. The handoff vendors that implementation with PiG path and shared-lock adapters.
- The old CustomEditor only conveyed decorations. Pi's `modes/interactive/components/custom-editor.ts:89-149` receives actual input and invokes its handlers synchronously. The handoff installs the actual Pi component and proxies its input, frames, text, history, autocomplete and cursor operations.
- The handoff invoked app actions through notifications only. `super.handleInput` returned before a clear reached the component, and a late host clear could erase the next key. Editor-local mutations now run synchronously in the component process. The action carries the pre-action text to the host, which executes the remaining host effects without replaying already applied editor mutations.
- Host-owned input effects now have an input-completion barrier. The existing input ticket holds the pump, not the UI loop, until the component's callbacks arrive. A host callback releases the ticket before entering a nested modal loop; subsequent main-screen input cannot run until that owner-loop callback returns. Disconnect and replacement release pending input.
- Pi's extension editor includes an external-editor hint and action (`extension-editor.ts:92-99,114-137`). Both `ctx.ui.editor` and the built-in editor-slot dialog bind that action to one host handoff. The child runs off-loop. The host cancels and joins its terminal reader before giving stdin to the child, restores successful edits on the owner loop, then resumes input and rendering. A cancelled dialog does not apply a late result.
- The external-editor scenario exposed an existing renderer bug: `RepaintAll` reset only `hasRendered`, which appended below the old dialog after the terminal handoff. Pi calls `requestRender(true)` (`extension-editor.ts:134-137`, `tui.ts:952`). `RepaintAll` now requests the existing destructive repaint path. Ordinary editor changes still use differential rendering.

- The loader predecessor moves the global gap out of Editor into the widget layout. The remote adapter now returns the component frame without adding a gap, forwards component-local mouse coordinates, and gives the extension dialog its explicit spacer.
- Broad family verification exposed an input-barrier regression in this lane: deferring every input ticket until after its handler deadlocked native `/settings` and `/fork` modal loops. Native callbacks now release the pump before they can enter a modal; only a remote editor extends its ticket through the component acknowledgement. The source fix is guarded by a compiling mutation and both affected parity scenarios.
- External-editor launch output and command splitting now follow `modes/interactive/external-editor.ts:22-23`: `Pi will resume when the editor exits.` and literal-space splitting, including empty arguments. The existing message and argument tests were red before these source fixes.

## Every CustomEditor app action

Source paths in this table are relative to `.upstream/v0.87.1/packages/coding-agent/src`. `IM` means `modes/interactive/interactive-mode.ts`. The dispatch denominator is `modes/interactive/components/custom-editor.ts:89-149`; the installed handlers are `IM:2961-3030`.

| Action | Pi source | Ownership and completion |
|---|---|---|
| `app.clear` | `IM:2994,4116-4123,4458-4461` | Editor-local `setText("")` completes in the same input turn. The second Ctrl+C within 500 ms requests host shutdown instead. The host does not echo the local clear. |
| `app.interrupt` | `IM:2965-2991` | Idle bash-mode clear is editor-local. Streaming queue restoration, abort, compaction/retry cancellation and empty-editor double Escape need host state and run in host callback order. Autocomplete Escape remains Pi Editor's local action. |
| `app.exit` | `IM:2995,4126-4129` | Host shutdown. Pi CustomEditor itself checks that the editor is empty; otherwise Ctrl+D remains local delete-forward. |
| `app.clipboard.pasteImage` | `IM:3026-3068` | Host clipboard I/O is asynchronous. The completed insertion crosses the ordered text-operation path. |
| `app.suspend` | `IM:2996,4280-4312` | Host process and terminal ownership; it does not synchronously edit text. |
| `app.thinking.cycle` | `IM:2997,4366-4376` | Host Session state and border configuration; no local text mutation. |
| `app.model.cycleForward`, `app.model.cycleBackward` | `IM:2998-2999,4378-4397` | Awaited host model transition with later border/footer update; no local text mutation. |
| `app.model.select` | `IM:3003` | Host selector takes input focus. |
| `app.tools.expand` | `IM:3004,4399-4420` | Host transcript expansion; no local text mutation. |
| `app.thinking.toggle` | `IM:3005,4433-4438` | Host settings and transcript visibility; no local text mutation. |
| `app.editor.external` | `IM:3006,4441-4452` | Host terminal handoff begins before subsequent input. The child finishes asynchronously; only success replaces editor text. |
| `app.message.copy` | `IM:3007-3010` | Host selection/message clipboard operation; no local text mutation. |
| `app.message.followUp` | `IM:3011,4315-4345,4598-4604` | Trim and clear are local. While streaming or compacting, local history insertion precedes the host queue/prompt operation. When idle, clear precedes the selected editor's `onSubmit`, as in Pi. Whitespace-only input is unchanged. |
| `app.message.dequeue` | `IM:3012,4347-4354,4577-4595` | Host queue state determines restored text. The input barrier orders the resulting text operation before the next key. |
| `app.session.new` | `IM:3013,6657-6669` | Host Session replacement is asynchronous and can be cancelled. |
| `app.session.tree`, `app.session.fork`, `app.session.resume` | `IM:3014-3016` | Host selectors own modal input. |

This classification does not turn host-dependent operations into fabricated synchronous local getters. The same-turn regression specifically proves the synchronous editor-owned clear; the host-bound cases preserve input ordering through the existing owner loop and input ticket.

## Red and green evidence

| Guard | Observed failure | Repair / final assertion |
|---|---|---|
| `TestHost_Integration_TSFileShim` | `session_start: editor.lockBorderColor is not a function` | The TypeScript fixture uses real CustomEditor history. The host test asserts installation, rendered `history-2` then `history-1`, and the corresponding text snapshots. |
| `TestNodeEditorComponentIsPisCustomEditor` | `app.clear completes synchronously: 'seed' !== ''` | Immediate read after the input returns is empty; the next input is retained. Existing comparison against Pi's actual CustomEditor frames remains. |
| `TestRemoteEditorLocalClearDoesNotReplayOverNextKey` | A compiling overlay mutation disables `localAction`; the test records `["input:\\x03", "setText:", "input:x"]` and rejects the host echo. | The pump ticket waits for the component; host handling sends no duplicate `setText`; the next key remains `x`. |
| `TestRemoteEditorDisconnectReleasesInput` | New lifetime regression. | Connection loss releases the input ticket. |
| `TestTerminalInputPassReleasesNativeKeyBeforeModalHandler` | A compiling overlay removes the native early settlement and fails with `native modal would wait on an input pump held by its opening key`. | Native modal focus handoff stays live; `20-message-output-padding` and `16-user-message-selector` pass after being red. |
| `TestMainScreenExternalEditorRepaint` | A compiling overlay restores the old `hasRendered = false` implementation; the physical buffer is not cleared. | `RepaintAll` uses the force-redraw path and restores the dialog. |
| `TestExtensionEditorExternalHint` | `external editor hint missing` | The rendered hint includes the external-editor action. |
| `TestExtensionEditorExternalAction` | New component regression after the hint fix. | Configured app binding, multiline input, asynchronous completion, submission and ignored late completion. |
| `TestInteractiveTerminalReaderHandsInputToExternalEditor` | New ownership regression. | After pause, a separate consumer reads the child's bytes; resume receives later bytes. |
| `35-custom-editor-component` | Handoff evidence records the pre-port failure. | Three escaped-output Pi/PiG pairs exercise SettingsManager, modal input, render, bash border and submit. |
| `35b-editor-action-sync` | Lead's P1 notification-only adapter fails the same-turn read; this lane independently reproduced the unit failure. | Three escaped-output pairs require `editor-action-result:"":end`. |
| `35c-extension-editor-external` | Pi returns the edited dialog; PiG initially leaves the old dialog and appends a partial repaint below it. | Three escaped-output pairs compare the title, multiline edited buffer, borders and external-editor hint. The child reads stdin itself. |

The initial scenario authoring mistakes (unsupported `C-g` spelling and a cwd outside `[tmux]`) failed both binaries and were corrected before using the scenario as behavioral evidence. A diagnostic run removed the crop to inspect the complete physical screen; the canonical crop was restored unchanged, not narrowed to hide the repaint defect.

## Corpus replay

`/tmp/fin-pivim-evidence/e2e.py` records the real npm package under separate Pi/PiG homes and working directories. The final `e2e-clean` run uses `env -i`, a local scripted OpenAI-compatible provider, non-reasoning and reasoning models, and tmux 3.7b. It drives 65 named checkpoints, preserving full ANSI panes, editor crops and cursor coordinates, shape, visibility and blinking. Cursor rows in editor comparisons are relative to the editor's top border. Full raw panes remain available for reviewing host UI differences.

The installed npm lock records `pi-vim@0.14.2` at `https://registry.npmjs.org/pi-vim/-/pi-vim-0.14.2.tgz`, integrity `sha512-CFSKJvOCNToueIMyBgsujT+gHa4sAlLOnT4VYZ6iCGKJ7M24eCVWFKKnnMt6tmP1rDE6T7k/AqBrb6LWzCzRaQ==`.

| Package | Before | After vs Pi |
|---|---|---|
| `pi-vim@0.14.2` | Coordinator handoff and corpus: `SettingsManager.create is not a function`; the decoration stand-in receives no modal input. This is historical baseline evidence, not a newly run 0.2.0 binary. | Fresh replay: insert/normal/visual/ex, motions, operators, undo/redo, yank/put, bracketed multiline paste, unsupported and safe-quit commands, `:name`, shell dispatch, slash/file completion, Ctrl+C followed immediately by text, prompt submission, resize, settled reload, global/project mode colors, switching to the reasoning model, and two thinking-level cycles produce equal editor ANSI and relative cursor state. The cursor is a blinking bar in Insert and a blinking block in Normal/Visual/EX under both binaries. |

The replay's full model-selector and tree screens differ at the already reported host selector surfaces; these are not normalized or claimed equal. The immediate reload checkpoints capture asynchronous replacement at different instants, while the explicit settled checkpoints match. The quit checkpoint includes each program's exit presentation, not an editor. These five non-equal checkpoints remain in `e2e-clean/results.json`; 60 editor checkpoints compare equal. The reasoning model reaches `minimal` then `low` in both binaries.

## Gate notes

The first unprepared full subprocess run exceeded Go's unchanged ten-minute package budget while rebuilding Rust fixtures. Its last leaf test had run for about ten seconds. The Rust unknown-handler test's `signal: killed` occurs at the package deadline, not as an extension assertion. The maintained `eval "$(./automation/ci/test-fixtures.sh --exports)"` workflow removes redundant fixture builds. No timeout is increased and no test is skipped. A full run that overlapped a predecessor merge also read conflict markers; it is retained as an invalid verification run, not a product result.

Final verification:

- `go vet ./...` and `GOOS=windows go vet ./tui/... ./internal/codingagent/... ./coding/extension/...` pass.
- `go tool golangci-lint config verify`, touched-package lint, `make lint-changed LINT_BASE=da3b0ffb4b`, and full `make lint` pass with zero issues. `go fix -diff ./...` is empty.
- `make ci-contracts` passes after regenerating `pig-go.json`, interface recommendations and coverage.
- `make ci-drift` stops at D77's missing owner approval. `divergence-guard`, `source-hygiene`, and `docs-drift` pass when run independently. No approval is invented.
- The fixture-prebuilt full `go test ./...` run passes the production packages, all SDK conformance and upstream API tests. Only the three `test/parity/closure` approval/accounting tests fail on pending D77. The final native-modal source correction also passes the complete codingagent, TUI and upstream-parity packages.
- `go test -race ./tui` and the focused codingagent editor/input-lifetime race tests pass.
- `make parity-family FAMILY=extensions-runtime`, `interactive-rendering`, and `fullscreen` pass their declared durability after the native-modal fix. The three new editor scenarios each pass three escaped-output pairs. The final qualified commands use tmux 3.7b, not the older helper wrapper.
- `reuse lint` passes using the existing cached REUSE 6.2.0 executable. The vendored SettingsManager, CustomEditor, path and HTTP-parser modules pass pinned-package provenance tests after combining the runtime lane's full path-helper closure.

The inherited `settingsLockStale` magic-literal finding is accounted at the D73 shared-lock boundary with the exact proper-lockfile 4.1.2 dependency reference (`lib/lockfile.js:208`), not a fabricated literal in Pi's settings-manager source. This is one guard marker, not approval for a PiG-specific timeout or a new divergence ID.

## Performance and resource ownership

`BenchmarkEditorRemoteCachedFrame` measures a three-line cached remote editor frame at width 120: 48.81–52.24 ns/op, 48 B/op and one defensive slice allocation. It performs no IPC or JSON work. `BenchmarkEditorStateSnapshot` exercises the UI bridge snapshot with the full resolved keybinding table: 62.3–63.9 µs/op, about 12 KB allocated in 94 allocations, and an 8,463-byte serialized snapshot. The CPU profile is dominated by JSON map encoding and key ordering. These are absolute shared-machine samples, not speedup claims.

The keybinding table remains in full state snapshots. It is independent of Session history size, and `TestStatePushDoesNotScaleWithSessionSize` passes. Node already avoids rebuilding an unchanged table. This lane measures the cost rather than adding a separate state-delta mechanism without a demonstrated need.

One input ticket bounds queued terminal work while a remote key awaits acknowledgement. The UI loop remains live. Replacement and disconnect release the ticket. External-editor handoff cancels and joins the stdin readiness reader before the child inherits the terminal, retains already-read input, awaits child completion off-loop, and resumes on the owner loop. The tests cover no host clear echo, next-key retention, native modal admission, connection loss, read ownership, restart, and late dialog completion.

## Stale matrix rows reviewed

The matrix still has older `planned` rendering-summary rows despite the detailed tool rows and scenarios 24/25 covering call/result renderers and fallback. Its keybinding/theme/syntax-helper summary rows are also older than `TestPiThemeHelpersMatchThePinnedPackage`, which compares those helpers against Pi's actual theme and keybinding-hints modules. The footer/loading/lifecycle summary rows must be reconciled by their owning families, not promoted from this editor probe. This lane updates only its editor rows and preserves their honest partial status for non-Node factory ergonomics and arbitrary host-state same-turn reads.

## Remaining owner decision

D77 is the runtime predecessor's cross-process event-bus restriction. Its default colocated Node path uses Pi's actual synchronous shared bus. Separate isolated processes cannot share arbitrary JavaScript reference identity, synchronous mutation and reentrant callbacks through a JSON relay without changing the isolation contract. `f04db0b7c3` correctly marks this pending, not approved. This lane introduces no new divergence ID and does not waive that release blocker. The final combined branch is not release-green until the owner resolves D77.
