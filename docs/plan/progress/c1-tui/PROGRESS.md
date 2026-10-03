# c1-tui: Pi 1.0.0 TUI code port (phase 1, product code)

Area: fullscreen default, `quietStartup: "header"`, Apple Terminal wordmark (replaced by the PiG mark per D2). Upstream `.upstream/v1.0.0` against `.upstream/v0.99.2`. Branch `c1-tui` from `porter/pi-0.99.1`, with `p1-tui` merged up to `8a1b14420` and `rev-fix-114-pig-header` merged at `b76c97f2b`.

## Status

READY. Every sibling test in this area is green. The two wordmark tests assert the PiG text mark per D2 (owner answer below).

## Red → green (sibling tests)

| Test | Root cause (p1-tui list) | Fix commit |
|---|---|---|
| `tui/widthx#TestUpstreamSliceByColumnANSIOrderRegression/keeps_a_reset_at_the_slice_start_after_earlier_style_codes` | 5 | `ad14b4ba9` |
| `tui/widthx#TestUpstreamSliceByColumnANSIOrderRegression/does_not_leak_color_into_text_after_a_highlighted_token` | 5 | `ad14b4ba9` |
| `tui#TestUpstreamAutocompleteSkillSlash/completes_commands_after_leading_whitespace_and_preserves_it` | 4 | `ad14b4ba9` |
| `tui#TestUpstreamAutocompleteSkillSlash/completes_command_arguments_after_leading_whitespace` | 4 | `ad14b4ba9` |
| `tui#TestGenerateSystemThemeColorsUpstream/keeps_pastel_palette_colors_pastel_at_other_lightnesses` (and the Pi oracle `TestGenerateSystemThemeColorsMatchesUpstream`) | 6 | `ad14b4ba9` |
| `internal/codingagent#TestUpstreamSettingsManager/TUI_mode/defaults_to_fullscreen_and_persists_regular_mode` | 1 | `37d2802c7` |
| `internal/codingagent#TestUpstreamSettingsManager/TUI_mode/falls_back_to_fullscreen_for_unsupported_values` | 1 | `37d2802c7` |
| `internal/codingagent#TestUpstreamSettingsManager/TUI_mode/does_not_recognize_the_old_uiMode_setting` | 1 | `37d2802c7` |
| `internal/codingagent#TestLoadedResourcesQuietStartupHeaderUpstream/hides_resource_listing_but_keeps_the_startup_header_with_header-only_quiet_startup` | 2 | `37d2802c7` |
| `internal/codingagent#TestBuiltInHeaderOnboardingWithHeaderOnlyQuietStartupUpstream` | 2 | `37d2802c7` |
| `internal/codingagent#TestInteractiveLoginArgumentCompletionUpstream` | 7 | `98f5aeba4` |

| `internal/codingagent#TestPiWordmarkUpstream` (asserts the D2 "PiG." text mark) | wordmark | `1a0b6b280` |
| `internal/codingagent#TestBuiltInHeaderShowsTheWordmarkInAppleTerminalUpstream` (darwin; ` PiG. vX` per D2, compiled with `GOOS=darwin`, not run on this Linux host) | wordmark | `1a0b6b280` |

## Upstream source changes ported

- `tui/src/utils.ts` `sliceByColumn` pending-ANSI order (#10169).
- `tui/src/autocomplete.ts` slash detection on `trimStart()` (#10218), through `widthx.JSTrimStart` (JavaScript whitespace set).
- `coding-agent/.../theme/system-theme.ts` palette chroma cap (#10255, #10293).
- `tui/src/tui-alt-screen.ts` `getScreenLines()` → `TuiAltScreen.GetScreenLines`.
- `user-message.ts` drops the wrapping `Box`; `Markdown` pads and paints the background itself (3 allocations per render instead of 7; same output).
- `settings-manager.ts` fullscreen default and `QuietStartup = boolean | "header"`; `interactive-mode.ts` `shouldShowStartupHeader`/`shouldShowStartupDetails`, onboarding line; `settings-selector.ts` quiet-startup values and descriptions; `cli/args.ts` help text (regenerated `cmd/pig/help_upstream.txt`).
- `oauth-selector.ts`/`interactive-mode.ts` `account` vs `subscription` labels for `/login` and `/logout`; the API-key login list follows the runtime's providers.

Drift exposed by the fullscreen default and fixed at the source:

- `stopInteractiveTui` (`interactive-mode.ts:845-852`, unchanged since 0.99.1): a transcript exit hides overlays, switches to the main-screen renderer without starting it, renders, and stops there. PiG dumped the layout from the alternate screen and lost the blank row before the resume hint. `switchTuiMode` gains Pi's `startRenderer` parameter; `tui.HideOverlay` is exported as upstream `hideOverlay`. Regression: `internal/codingagent#TestStopInteractiveTuiFullscreenExitOutput` (mutation-checked); parity `interactive-rendering/22-exit-final-layout`.
- `paintBgWith` appended `\x1b[K` after the padding; Pi's `applyBackgroundToLine` (`utils.ts:1099-1108`) does not. In fullscreen the erase changed the captured cells past a custom message. Regression: `tui#TestPaintBgWithEndsAtPadding`; parity `extensions-runtime/22-custom-message-markdown-wrap`.
- `extensions-runtime/20-differential-render-overflow-terminates` asserts the main-screen differential render; both binaries now default to fullscreen, so the scenario passes `--tui-mode regular` to both.

New parity scenario: `startup/14-startup-quiet-header` (pig and Pi 1.0.0 match; a mutant that treats `"header"` like `true` fails it).

## QUESTION (owner): answered

The lead answered with owner option A: under D2 the PiG mark replaces Pi 1.0.0's Apple Terminal "Pi" wordmark, and fix-114-pig-header owns it. Merge `1a0b6b280` brings in `rev-fix-114-pig-header` and ports Pi 1.0.0's Apple Terminal layout: the wordmark and the version on the first line, the key hints from the start of the second (`interactive-mode.ts:1001-1003`). fix-114 had kept the previous upstream's two-line slot. `piWordmark` returns the one-line `PiG.` mark (`piglogin.FallbackMark`). `TestPiWordmarkUpstream` asserts that mark byte for byte in truecolor and its 256-color form; `TestBuiltInHeaderShowsTheWordmarkInAppleTerminalUpstream` asserts ` PiG. vX`; fix-114's `TestBuiltInHeaderUsesTheOneLineMarkInAppleTerminal` asserts the 1.0.0 layout and fails if the hints keep the old 4-cell indent. D2 (`docs/parity/DIVERGENCES.md`), `docs/site/docs/tui.md` and the `pi-logo.ts` row in `test/parity/upstream-sync/v1.0.0.toml` record the divergence. The logo click easter egg (`pi-logo-animation*.ts`) is designed out under D2 in the sync ledger, PORT_MAP and the behavior-input mapping.

## Left for other lanes

- Login lane: "not configured" wording, the Radius `/login` method menu and `formatAuthSelectorProviderStatus` (`oauth-selector.ts`, `interactive-mode.ts`); parity login scenarios fail on the base binary as well.
- Misc/CLI lane: `main.ts` now rejects `--provider` without `--model`; the regenerated help text already says so, the enforcement is not ported.
- Pre-existing gap: PiG never prints Pi's `Model scope: …` startup line (`interactive-mode.ts:940-953`). Pi resolves `session.scopedModels` (with extension providers and thinking levels) before `init()`; PiG resolves scoped models after the renderer starts and only their IDs. Porting it is a structural change outside this delta. `"header"` therefore only hides the resource listing; the docs do not mention the model-scope line.
- Ledger: the `interactive-mode.ts` and `oauth-selector.ts` rows in `test/parity/upstream-sync/v1.0.0.toml` stay pending (login lane). `test/parity/interfaces/test-mapping-v1.0.0.json` moves the five area files (`settings-manager.test.ts`, `interactive-mode-status.test.ts`, `system-theme.test.ts`, `autocomplete-skill-slash.test.ts`, `regression-slice-by-column-ansi-order.test.ts`) from `partial` to `ported`; `make test-porting-release` no longer reports a TUI hot-path finding. Interface-mapping rows for `QuietStartup` and `GetScreenLines` are pending with the ledger-wide pass.

## Evidence

- `go test ./tui/... ./internal/codingagent ./cmd/pig ./coding/...`: only failures are pre-existing ones outside this area (`cmd/pig#TestFauxRPCObservation`, `#TestTestFauxRPCObservation`, `coding#TestRPC33*`, `#TestFauxAgentObservationOracle`, `#TestTestFauxAgentObservationOracle`: oracle scripts that still assert the previous upstream pin; `coding/extension/host/subprocess#TestNodeVendoredTuiUpstreamTests/native-clipboard-linux`: no Xvfb on the host).
- `make parity-fast` against Pi 1.0.0: 24 failures, the same 24 that fail with the binary built from the merge base `67d8f66e4` (login wording, changelog, model-picker fuzzy filter, context-percentage token estimates, Pi-oracle `node` errors in `24-runtime-cost-and-resource-order`, `28-session-reload-ui`, `29-session-rebind-ui`). The two regressions the fullscreen default exposed are fixed before that run; `20-differential-render-overflow-terminates` was fixed after it, which leaves 23.
- After merging `rev-fix-114-pig-header`, every startup scenario passes against Pi 1.0.0. `03-startup-expanded-help` failed on the merge base as well. The runner's diff report showed the D2 onboarding line, but the real difference was the placement of `\x1b[39m` around the first hint row in tmux's escaped capture. Raw pipe-pane logs show both binaries write that row with the same bytes after Ctrl+O; tmux keeps the row's earlier used width, which differs because D2's 18-cell sprite fills the 100-column compact row and Pi's 4-cell logo does not (Pi's own capture changes between 99 and 100 columns). The scenario now runs at 99 columns, where D2 uses the 4-cell mark, and passes with `escaped_output_equal`. The other scenarios fix-114 touched also pass: `26-extension-load-order`, `28-prompt-precedence`, `55-package-manifest-startup`, `selectors/01-config-empty`, `14-config-ctrl-c-exits` and `15-config-escape-exits`.
- Fullscreen default and scenarios written for the main screen: `settings/03-settings-filter-theme`, `settings/04-settings-theme-submenu`, `model-resolver-selector/13-scoped-models-fuzzy-filter` and `14-model-picker-fuzzy-filter` timed out for both binaries because a step waited for text that the alternate screen, which keeps no history, never shows a second time. They now wait for the new filter text and check the unchanged rows as visible state (8e355e76c). `slash-commands/04-changelog-structure` and `11-changelog-complete-history` read the long changelog from main-screen history with `--tui-mode regular` (7be3fb709).
- Fullscreen key routing bug: Pi's alternate-screen renderer consumes PageUp/PageDown, the other viewport keys, mouse and search before the focused editor-slot selector (`tui-alt-screen.ts` handleViewportInput:740-751). PiG's selector loops delivered those keys to the selector, so PageDown refiltered the model picker. The shared modal input helpers now apply `HandleViewportInput` after terminal theme replies, as the editor path does. Red→green: `TestFullscreenViewportKeysPrecedeEditorSlotSelector` (red: PageDown reached the selector), `TestRegularModeSelectorReceivesPageDown`, and the new scenario `24-fullscreen-model-picker-page-down-scrolls-viewport` (red on the previous binary: the picker moved to model-two). `22-model-picker-unbound-keys-refilter` keeps its main-screen contract with `--tui-mode regular`.
- The same order now applies to a subprocess extension's `ui.custom` component (`ext_ui_context.go` RunRemoteOverlay): every chunk passes through theme replies and the fullscreen viewport on the owner loop first. In the editor slot PageDown scrolls the transcript; a focused overlay still receives it. Red→green: `TestFullscreenViewportKeysPrecedeEditorSlotExtensionUI/editor_slot` (red: the extension received PageDown). The 25 extension custom-UI and overlay scenarios pass.
- The eight `fullscreen/*main-screen*` scenarios cover `tui-main-screen.ts` but ran on the alternate screen once fullscreen became the default; they now pass `--tui-mode regular` (86472a9f6). 03, 05, 07, 08 and 09 pass. 04, 06 and 10 differ only in the context token estimate (1.4% vs 1.5%; 1,929 vs 1,952 tokens), a system prompt size difference outside the TUI area.
