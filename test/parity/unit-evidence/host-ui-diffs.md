# Host UI differences: Pi 0.87.1

## Scope and oracle

This report covers the extension-free `/settings`, `/name`, `/hotkeys`, model picker and `/tree` comparisons for release 0.3.0.
The oracle is the installed Pi 0.87.1 executable at `extensions/sdk-ts/node_modules/.bin/pi`.
The source references below resolve under `.upstream/current/`.
The probes use tmux, true color, fresh agent and Session directories, offline mode and disabled extensions.
The hotkeys probe uses a 100×150 pane to retain all three tables.
The other canonical probes use 100-column panes.

The initial side-by-side escaped captures and red/green logs are retained in `/tmp/host-ui-diffs-evidence/` on the worker.
The canonical scenarios reproduce the comparisons without those artifacts.

## Root causes and fixes

| Surface | Pi rule and source | PiG cause | Fix and regression |
|---|---|---|---|
| Settings image rows | `packages/tui/src/terminal-image.ts:77-85` rejects automatic graphics under tmux/screen. `packages/coding-agent/src/modes/interactive/components/settings-selector.ts:727-763` gates Show images and Image width, not Auto-resize images or Block images. | D44's outer Herdr detection ran before the immediate multiplexer boundary. | Check tmux/screen first. Keep explicit Pi protocol/settings override precedence. `TestTmuxImageDetectionPrecedesOuterHerdr`; `settings/09-settings-tmux-image-capability.toml`. |
| Name output | `packages/coding-agent/src/modes/interactive/interactive-mode.ts:6408-6430` uses Spacer followed by `Text(theme.fg("dim", ...), 1, 0)`. | Confirmation used plain Markdown; lookup used SGR faint. Both lost padding/spacing and interpreted Markdown in names. | Use the plain-text sink and theme-dim foreground. Give the sink its upstream leading Spacer. `TestNameCommandUsesPaddedThemeText`; tightened `slash-commands/02-name-set-get.toml`. |
| Hotkeys | `packages/coding-agent/src/modes/interactive/interactive-mode.ts:6541-6654` renders Navigation, Editing and Other Markdown tables between borders with a padded accent/bold title. | The command enumerated all application actions as an unrelated flat list and hardcoded editor keys. | Generate Pi's built-in table rows from effective keybindings and compose the same components. Delete the obsolete flat formatter. `TestHotkeysCommandRendersUpstreamTables`; `slash-commands/09-hotkeys-styled-tables.toml`. |
| Model frame | `packages/coding-agent/src/modes/interactive/interactive-mode.ts:2400-2422` owns the above-editor gap in the widget container. `packages/coding-agent/src/modes/interactive/components/model-selector.ts:99-159` uses empty Spacer rows internally. | The gap belonged to Editor, so replacing it removed the gap. The picker padded internal empty rows as Text. | Move the host gap to its owning container and use empty picker spacer rows. `TestEditorFrameDoesNotOwnHostSpacer`, `TestModelSelectorSpacersMatchUpstream`, `TestHostSpacerSurvivesEditorReplacementAndWidgetClear`; `model-resolver-selector/19-model-picker-frame-spacing.toml`. |
| Tree frame | `packages/coding-agent/src/modes/interactive/components/tree-selector.ts:1383-1394` includes a leading Spacer, `Text(theme.bold("  Session Tree"), 1, 0)` and a bottom Spacer even for an empty filtered list. | The leading/bottom gaps were missing and the title's bold span excluded its inner indentation. | Match component spacing and title wrapping. `TestTreeFrameSpacingAndTitleStyles`; `tree/06-tree-header-spacing.toml`. |
| Paragraph before table | `packages/tui/src/components/markdown.ts:491-498` inserts a blank row after a paragraph when the next block is a table. | PiG's line parser required an explicit blank source line. | Recognize the paragraph/table boundary in Markdown, rather than adding extra whitespace to the hotkeys text. `TestMarkdownParagraphBeforeTableSpacing`; the hotkeys scenario exercises the caller. |
| Model cancellation | `packages/coding-agent/src/modes/interactive/interactive-mode.ts:5207-5210` restores the editor without appending a message. | `/model` appended “Model switch cancelled.” | Remove that output. `TestModelHandlerEmptyArgsPickerCancelled`; `model-resolver-selector/20-model-picker-cancel-silent.toml`. |

Representative escaped differences before the fix:

```text
/name lookup:
Pi:   <space>\x1b[38;2;102;102;102mSession name: demo\x1b[39m
PiG:  \x1b[2mSession name: demo\x1b[0m

/tree title:
Pi:   <space>\x1b[1m<space><space>Session Tree\x1b[0m
PiG:  <space><space><space>\x1b[1mSession Tree\x1b[0m

/settings under tmux with HERDR_ENV=1 and HERDR_KITTY_GRAPHICS=1:
Pi:   Auto-compact, Auto-resize images, Block images, ...
PiG:  Auto-compact, Show images, Image width, Auto-resize images, Block images, ...
```

## Red/green evidence

All five requested scenarios failed before the production changes.
The six initial command/frame/capability unit tests failed before the fixes.
The hotkeys scenario then exposed the paragraph/table boundary bug; its pure Markdown regression also failed before that fix.
All six final command scenarios pass `output_equal` and `escaped_output_equal` for three paired runs.
The existing name comparator changes from normalized equality of one cropped lookup to escaped equality of confirmation, intervening spacing and lookup.

The cancellation unit/scenario were added after the fix and were mutation-proven by restoring the original append.
That mutation compiled and failed on the extra cancellation line in both comparators.
The additional host-spacer replacement/clear unit test was added after the fix; it is supplemental, not separate red proof.
The model-frame scenario supplies the red proof for that root cause.

Moving the spacer also requires the Editor's mouse row offsets to follow `packages/tui/src/components/editor.ts:632-665` and the multiline dialog to own its explicit gap at `packages/coding-agent/src/modes/interactive/components/extension-editor.ts:76`.
Existing mouse/content/status assertions retain their checks at the corrected component coordinates.
The standalone autocomplete golden is regenerated with `UPDATE_GOLDEN=1 go test ./internal/tui/parity -run '^TestParityEditorAutocompletePopup_GoldenVisibleState$'` after reviewing `editor.ts:556-627`; only the obsolete first blank row moves to the bottom of the fixed-height screen.

## Verification

The following checks pass:

- `go test ./...`.
- `go vet ./...`.
- `GOOS=windows go vet ./tui ./internal/tui/parity ./internal/codingagent`.
- `go test -race ./tui`.
- `go tool golangci-lint config verify`.
- `go tool golangci-lint run ./tui/... ./internal/codingagent/...`.
- `make lint-changed LINT_BASE=public/fix/extension-parity-022` and `make lint`.
- `make ci-contracts ci-drift`.
- `go fix -diff ./...` produces no changes.
- Declared-durability `make parity-family` runs for settings, slash-commands, model-resolver-selector, tree, autocomplete, interactive-rendering, fullscreen, tui-components and selectors.

The worker has no local `main` ref, so the changed-file lint command uses the task's actual base.
The config scenarios require unsetting the worker's unrelated `PIG_CODING_AGENT_DIR`; otherwise they inspect the worker's own resources instead of their fixture home.
No test timeout, skip, retry or comparator normalization is added.
One outer command invocation was cut off before the full repository tests completed; the subsequent complete run passes with Go's unchanged default test deadline.

Regeneration uses `go run ./test/parity/cmd/gointerfaces -out test/parity/interfaces/pig-go.json` and `make coverage RESULTS=`.
The latter deliberately reports no historical run status from another workspace's transient result file.
No PORT_MAP entry is promoted and no new divergence or lint suppression is added.
D44 is narrowed to preserve Pi's immediate multiplexer boundary.

## Resource disposition

The changes add no worker, process, timer or IPC call.
The host spacer survives editor/selector replacement and is recreated when widget contents change.
Name and hotkeys components live with the transcript and are released when it is cleared, as upstream components are.
Hotkeys constructs a fixed set of built-in rows and does not traverse Session history.
The existing model refresh retains its cancellation and join ownership.

Representative Linux/amd64 profiles use `BenchmarkHotkeysCommandRender` and `BenchmarkEditorStatusBorder`, each with `-benchmem -count=3 -cpuprofile ... -memprofile ...`.
Hotkeys construction plus rendering at width 100 measures 0.91–0.92 ms, about 147 KB and 3655 allocations per command.
The 120-column status editor render measures 5.7–6.5 µs, 3552 bytes and 35 allocations per render.
CPU/allocation profiles are retained with the capture artifacts.
These are resource measurements, not before/after speedup claims.

## Separate blocker found during cross-family review

The existing `selectors/10-extension-editor.toml` glyph crop ends at the cancel label.
Attempting to strengthen it to full escaped equality exposes a pre-existing missing `ctrl+g external editor` hint and action in PiG's multiline dialog.
Pi defines the hint at `packages/coding-agent/src/modes/interactive/components/extension-editor.ts:92-99` and the action at `:114-137`.
PiG's `tui/extension_editor.go` and `internal/codingagent/session_selectors.go:651` do not implement that action.
This is not a palette difference, despite the old scenario comment.
The incorrect comment is removed and the existing glyph comparator is retained at three runs; no full-dialog parity claim is made.
The coordinator must assign the external-editor action, terminal handoff, error/cancellation and SDK caller verification as a separate blocker.
Adding a hint alone would advertise an unavailable action and is not a fix.
