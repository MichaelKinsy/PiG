# Lane port-99-f9a: interactive theme and chrome (upstream 0.87.1 to 0.99.1)

Base `porter/pi-0.99.1` (e95e56c47). Scope: plan Phase 2 step 9, first half: theme system, header and logo, footer, settings and config selectors. Families: `interactive-rendering` (theme parts), `startup`, `footer`, `selectors`. Lane port-99-f9b owns tool-call rendering, slash commands, tree and export-html.

## Environment

- Every command ran with a temporary `HOME`, `PIG_HOME` and agent directories (`/tmp/f9a-env.sh`), Go 1.27.1.
- The comparator is the upstream 0.99.1 package installed by another lane in `/tmp/pi99` (read only). Oracle data was generated from its `dist/` with Node 24.19.0.
- `tui` has three baseline failures on the unchanged tree because the pinned oracle package is not installed under `extensions/sdk-ts/node_modules` on this host: `TestColorDetectionMatchesPi`, `TestExtensionDialogsMatchPiBindingsAndPalette` (and two dialog siblings). They test removed 0.87.1 theme detection or unrelated dialogs.

## Red stage

Upstream tests ported (upstream 0.99.1 citations in each test):

| upstream test | Go test |
|---|---|
| `system-theme.test.ts` (7) | `tui/system_theme_upstream_test.go` |
| `theme-style.test.ts` (6) | `tui/theme_style_upstream_test.go` |
| `themed-text.test.ts` (1) | `tui/themed_text_upstream_test.go` |
| `theme-detection.test.ts` (5 cases; the removed 0.87.1 detection tests are dropped) | `tui/theme_detection_upstream_test.go` |
| `theme-export.test.ts` (1 new) | `tui/theme_export_okhsl_upstream_test.go` |
| `theme-json.ts` `appearance` and color values (5 oracle cases from the installed validator) | `tui/testdata/theme_json_cases.json` |
| `resource-loader-theme.test.ts` (3 cases) | `internal/codingagent/resource_loader_theme_upstream_test.go` |
| `theme-controller.test.ts` (9) | `internal/codingagent/theme_controller_native_upstream_test.go` |
| `footer-width.test.ts` (2 new) | `internal/codingagent/footer_width_upstream_test.go` |
| `settings-selector.test.ts` (wheel row, system theme first) | `internal/codingagent/settings_selector_upstream_test.go` |
| `interactive-tui.test.ts` (getter supplied to the renderer switch) | `internal/codingagent/interactive_tui_wheel_upstream_test.go` |

Tests with no upstream test file, asserting the source: `pi_logo_upstream_test.go` (pi-logo.ts, header layout), `themed_notices_upstream_test.go` (ThemedText notices).

Per-case substitutions (L1):
- `theme-export.test.ts` new case: upstream reads the export section through `loadThemeJson`; PiG reads it from a loaded Theme, which must resolve all colors, so the dark vars stay next to `card`.
- `resource-loader-theme.test.ts`: PiG's theme resources load in `InteractiveMode.loadThemes`; the third case reloads the settings manager explicitly where `DefaultResourceLoader.reload` does.
- `theme-controller.test.ts`: the `ui` mock maps to a renderer wrapper (`QueryTerminalColors`), the `?2031` write and scheme-report input; see the test header.
- Not ported: `startup-session-name.test.ts` (harness only: `pathToFileURL` for a Node `--import`), `test-theme-colors.ts` (usage text of a manual script).
- Passing at red because Go was already faithful: `TestThemeColorModeUsesTerminalCapabilitiesUpstream`, `TestThemeSettingHelpersUpstream`, footer "updates cached usage totals" (the Session accumulates incrementally), controller cases "lets an explicit selection replace the initial theme" and "reloads theme settings", wheel cases for an unset or auto setting.

Signature-only stubs: `tui/system_theme.go`, `tui/theme_terminal_colors.go`, `tui/themed_text.go`, `Theme.Style/BgText/ColorValues/Appearance`, `LoadThemeFromPath`, `RoutedModelSelection`/`StatusLine.SetRoutedModelSource`, `InteractiveMode.initTheme/waitForTerminalColors`, `piLogoLines`. Mechanical rename: `tui.ColorMode` is `TerminalColorMode` (upstream unified them).

Red run (`go test ./tui ./internal/codingagent` for the new and changed tests): 42 failing subtests, all new behavior (system theme generation, theme styles, detection, ThemedText, export and validator cases, controller, resource-loader color mode, routed footer, wheel row and wiring, logo and header, notices). Every other test in the two packages is unchanged and unaffected by the stubs.

## Green stage

Commits after the red commit, in order: tui theme core (system theme, oklch/okhsl values, terminal color state), registry and `ThemedText`, terminal-color theme controller and startup theme flow, adapted tests, keybinding description, footer routed model, pi logo header, wheel scrolling setting, system-first theme submenu, resource themes in the settings color mode, `ThemedText` notices, theme tokens instead of hard-coded colors, order-independent tests.

Behavior decisions, each with its upstream 0.99.1 source:
- Unknown or invalid theme names select the system theme and report `Theme not found: X` (theme.ts `setTheme`, 772-790). PiG tests that assumed a dark fallback now assert the system theme (`ext_theme_test.go`, `ext_theme_settings_upstream_test.go`, `missing_theme_export_upstream_test.go`).
- The system theme is reserved: listed first, generated from the current terminal colors on every `Get`, never watched (theme.ts 812-818).
- Dim tokens render as SGR faint over their color and close with `\x1b[22;39m` (`tui.FgClose`); `fg` helpers in `tui/select_filterable.go`, `tree_row_format.go`, `session_selector.go`, `tool_render_shell.go`, `interactive_chat.go` and `status_line.go` use it. `tool_render.go` (lane 9b) still closes theme colors with `SGRFgReset` and leaks faint text under the system theme; lane 9b should switch its `styleWrapRows(..., tui.SGRFgReset, ...)` calls to `tui.FgClose(color)`.
- The controller queries the terminal colors with a 100 ms timeout (`terminal-colors` query, DA1 terminated), applies late replies, and never blocks `applyThemeFromSettings`; `waitForTerminalColors` pumps the owner loop until the latest query settled. No query starts without an owned background context (`backgroundCtx`).
- `Container` forwards `Invalidate` to its children when the active theme changes, as upstream's UI invalidates every component on a theme change; `ThemedText` notices (errors, warnings, status, extension errors, cache-warming, billed-token warnings, update and changelog notices, reload and trust notices, loaded-resource conflicts) rebuild from it. `NewDynamicBorderToken` resolves the border color at render time.
- `pasteImage` description and the startup hint follow upstream 0.99.1 ("Paste files on macOS, images, or text from clipboard"). This closes the lead's joint-run failure `TestAppKeybindingDefinitionsMatchUpstreamInventory`; `TestInteractiveThemeSelectionPresenceMatchesPi` passes against the upstream 0.99.1 controller in the Node oracle.
- Header: pi logo first line carries `v<version>` (D63 keeps the composite version), second line the first hint line; the product name no longer precedes the version. The onboarding line keeps D2.

Tests deleted or replaced because they tested removed 0.87.1 behavior (upstream 0.99.1 has no equivalent):
- `internal/codingagent/theme_detection_upstream_test.go`: ported `theme-detection.test.ts` of 0.87.1 (OSC 11 background detection). Superseded by `tui/theme_detection_upstream_test.go` and `theme_controller_native_upstream_test.go`.
- `internal/codingagent/interactive_theme_test.go`: OSC 11 detection through the interactive controller; superseded by `theme_controller_native_upstream_test.go`.
- In `terminal_color_query_integration_test.go`: scheme-failure background fallback, FIFO of shared-renderer queries and query-failure fallback (the detection they exercised is removed; `TestTerminalColorReplyBypassesPendingAutocomplete` stays).
- `BenchmarkInteractiveAutomaticThemeNotifications` measured the removed detection path.
- `terminal_colors_upstream_test.go` was rewritten as `TestUpstreamTerminalColorsQuery` for `terminal-colors.test.ts` `TUI.queryTerminalColors` (137, 161, 179).

Test edits with an upstream citation: `interactive_tui_wheel_upstream_test.go` replaces the layout root with the mouse region so the wheel event reaches it in a mounted fullscreen layout (`tui-alt-screen.ts` handleViewportInput dispatches to the rendered layout); `settings_selector_upstream_test.go` sets the appearance through `tui.SetTerminalColorScheme` where the removed `themeState.terminalTheme` was (interactive-mode.ts:4804). PiG-authored tests that read theme colors were re-derived from the upstream 0.99.1 dark theme with the Node oracle (`update_notice_test.go`).

Test isolation: tests that select a theme (Reload, Run, `SetTheme`) call `restoreStartupTheme`, and tests that remap keys call `restoreTUIKeybindings`; before this lane the key remapping tests leaked their hints into `TestGrepBodyRenderer` and `TestFindAndLsBodyRenderers` (lane 9b) on the base tree as well.

Notes for lane 9b: `Theme.ColorKeys()` order changed (concrete tokens, then default foreground and background); the system theme has no export colors, so export derives them from `userMessageBg` (`export.go` already does); `/hotkeys` and the new-session notice (`interactive_hotkeys.go`, `interactive_commands.go`) still bake colors into plain text and should use `themedNotice` or `tui.NewThemedText`.

Cleanup of the removed 0.87.1 background query (upstream 0.99.1 `tui.ts` has only `queryTerminalColors`): deleted `Renderer.QueryTerminalBackgroundColor`, `ConsumeOsc11BackgroundResponse`, `TerminalBackgroundColorResult`, the pending OSC 11 FIFO and `ParseOsc11BackgroundColor`; deleted `tui/terminal_color_query_test.go` (its five tests covered that FIFO and its Node-timer deadline; superseded by `TestUpstreamTerminalColorsQuery` and `tui/theme_detection_upstream_test.go`). The OSC 11 parser regression tests (JavaScript whitespace, channel arithmetic, signed hex, extra channels) stay against `ParseOscColorResponse` through a test helper, since `parseOscColorValue` is shared by every color reply.

## Gates and late findings

- `go build ./...`, `go vet ./...` (whole tree), `GOOS=windows go vet ./tui ./internal/codingagent ./coding`, `gofmt -l`, `go fix -diff ./...`, and `golangci-lint` (build tags `integration,live,parity`) on `./tui/... ./internal/codingagent/... ./coding/ ./internal/experimental/...` are clean. `python3 automation/ci/check-public-claims.py` passes.
- `make parity-family` against the upstream 0.99.1 comparator: `startup` and `footer` pass; `interactive-rendering` passes except `40-suspend-resume-session` (its fixture asserts "expected pinned Pi 0.87.1" and fails inside the comparator; it belongs to the pin-move lane); `selectors` passes except `01-config-empty`, `14-config-ctrl-c-exits`, `15-config-escape-exits` (upstream 0.99.1 lists the built-in extensions `codemode`, `mcp` and `tool-search` in `pi config`, families 7 and 8) and `10-login-subscription-providers`, `11-login-api-key-providers` (provider list and count come from the 0.99.1 model catalog, family 1 and 3). `TestCLIFixtureProjectsRunOutsideCheckout` fails on `model-runtime-store-catalog/16-image-model-data.toml` (`covers` empty, another family).
- Parity found two defects the unit tests missed, both fixed with a regression test:
  - The container now invalidates its children when the theme changes; `specialLinesComponent.Invalidate` (extension header and footer) requested a render, re-entered the renderer and deadlocked the first render after `ctx.ui.setTheme`. `Invalidate` is now a no-op (`TestThemeChangeRendersHeaderAndFooterComponents`; the mutation that restores the render request fails it after 5 s).
  - The loaded-resources listing still printed a `[Themes]` section; upstream 0.99.1 `showLoadedResources` lists none (conflict diagnostics stay). Scenarios `startup/05`, `06`, `07`, `08` were re-derived from the 0.99.1 output (last listed line is now the extension line, the Ctrl+O wait pattern is the new paste hint) and `startup/00-startup-banner` and `selectors/13-extension-theme-results` (`fallback:system`) assert the 0.99.1 output.
- Load tests: `GOMAXPROCS=4 taskset -c 0-3` with four CPU burners, `-race -count=24` over the theme, terminal color, startup prompt, footer, logo, wheel, notice and settings selector tests in `internal/codingagent` and the theme tests in `tui`: clean after two fixes. (1) A completed startup color query now applies before the next input chunk (upstream's promise continuation runs before the next terminal input event; the Go select could pick the input first). (2) The controller test fixture's stdout is a synchronized buffer (renderer goroutine and controller shared a `bytes.Buffer`). `go test -race ./tui ./internal/codingagent/... ./internal/experimental/... ./coding` passes except `internal/codingagent/tools` (`rg` and `fd` missing on this host) and `internal/experimental/services` `TestSourceStateSiblingUnsubscribeMatchesPinnedChord` (fails on the unchanged base tree).
- Full `go test ./...` failures that also fail on the unchanged base tree: `ai` (oracle `typescript` module absent), `coding/extension/host/subprocess`, `test/extension-conformance`, `cmd/pig` RPC oracle replays, `test/parity/*` tools, `internal/nativeplatform` (X11). I fixed the two that this lane caused: `internal/codingagent/export` `TestGenerateThemeVars_IncludesScrollbarThumb` (upstream 0.99.1 `dark.json:29` defines `scrollbarThumb`; the test now compares with the theme's color) and `test/parity/unit-evidence/codex-tests-modes.json` (pointed at the deleted detection tests; now names the controller tests and three mutations).
- Mutation checks (each fails the named test, restored afterwards): controller query timeout 100 ms to 1000 ms (`TestThemeControllerQueriesWithTheUpstreamTimeout`); `OnLateReply` dropped (`TestThemeControllerNativeUpstream`, `TestInteractiveThemeLateRepliesAndShutdown`); the same-colors early return removed (`TestThemeControllerNativeUpstream/re-renders_only_when_the_reported_colors_change`); `SetWheelScrollLines` call removed (`TestInteractiveTuiWheelScrollLinesFromSettingsUpstream`); watcher no longer skips the system theme (`TestThemeWatcherSkipsTheSystemTheme`); container no longer invalidates children (`TestChatNoticesFollowThemeChangesUpstream`); `FgClose` ignores the dim prefix (`TestFgCloseClosesADimForegroundWithTheFaintReset`, `TestForegroundHelpersCloseDimTokensLikeTheTheme`); `specialLinesComponent.Invalidate` requests a render (`TestThemeChangeRendersHeaderAndFooterComponents`). The startup ordering fix has no deterministic test: it only shows under CPU load and is covered by the `-race -count=24` burner run.
