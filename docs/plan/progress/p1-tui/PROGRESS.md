# p1-tui: Pi 1.0.0 TUI test port (phase 1, tests only)

Area: fullscreen default, `quietStartup: "header"`, Apple Terminal wordmark. The logo-click easter egg (`BuiltInHeader.handleMouse`, `pi-logo-animation.ts`) is out of scope.

Upstream: `.upstream/v1.0.0` (tag a13d35a74) against `.upstream/v0.99.2`. The branch is rebased onto the 1.0.0 pin commit `a8c294c02` on `porter/pi-0.99.1`, and `.upstream/current` links to `v1.0.0`. The ported tests cite `.upstream/v1.0.0/...` paths and fail until the product changes land.

Phase 1 rule: no product fixes and no loosened tests. Production edits are signature stubs that keep the 0.99.2 behavior, so the new tests compile and fail.

## Upstream test inventory (v0.99.2..v1.0.0, this area)

| Upstream test file | Change | Case | Go test | Status |
|---|---|---|---|---|
| `coding-agent/test/settings-manager.test.ts` | changed | :481 defaults to fullscreen and persists regular mode | `internal/codingagent/settings_manager_upstream_test.go#TestUpstreamSettingsManager/TUI_mode/...` | ported, red |
| same | changed | :494 falls back to fullscreen for unsupported values | same | ported, red |
| same | changed | :502 does not recognize the old uiMode setting (input is now `uiMode: "regular"`) | same | ported, red |
| `coding-agent/test/interactive-mode-status.test.ts` | new | :1245 hides resource listing but keeps the startup header with header-only quiet startup | `internal/codingagent/loaded_resources_quiet_header_upstream_test.go#TestLoadedResourcesQuietStartupHeaderUpstream` | ported, red |
| same | new | :1260 hides the startup header with full quiet startup unless verbose | same | ported, green (0.99.2 already hides the header for `true` and shows details when verbose) |
| same | changed | :456 matches login command arguments by provider id and name (adds `subscription` inputs and the `/login radius` → `Radius · account` assertion at :511) | `internal/codingagent/login_autocomplete_upstream_test.go#TestInteractiveLoginArgumentCompletionUpstream` | ported, red. This case belongs to the `/login` Radius change, outside the nominal area; it is ported here because it is in the same changed file. |
| same | changed | `createShowLoadedResourcesThis` takes `quietStartup: QuietStartup` and stubs `shouldShowStartupDetails` | `setUpstreamQuietStartup` helper (decodes the value through `Settings.UnmarshalJSON`) | ported |
| `tui/test/autocomplete-skill-slash.test.ts` | new | :35 completes commands after leading whitespace and preserves it (#10218) | `tui/autocomplete_wrappers_upstream_test.go#TestUpstreamAutocompleteSkillSlash/completes_commands_after_leading_whitespace_and_preserves_it` | ported, red |
| same | new | :57 completes command arguments after leading whitespace (#10218) | `...#TestUpstreamAutocompleteSkillSlash/completes_command_arguments_after_leading_whitespace` | ported, red |
| `tui/test/regression-slice-by-column-ansi-order.test.ts` | new file | :7 keeps a reset at the slice start after earlier style codes (#10169) | `tui/widthx/slice_by_column_ansi_order_upstream_test.go#TestUpstreamSliceByColumnANSIOrderRegression` | ported, red |
| same | new file | :12 does not leak color into text after a highlighted token (#10169) | same | ported, red |
| `coding-agent/test/system-theme.test.ts` | new | :87 keeps pastel palette colors pastel at other lightnesses (#10255) | `tui/system_theme_upstream_test.go#TestGenerateSystemThemeColorsUpstream/keeps_pastel_palette_colors_pastel_at_other_lightnesses` | ported, red. Theme, not one of the three named features; ported because it is the only changed TUI-theme test. |

All new and changed upstream test cases for this area are ported.

Changed upstream test files outside this area (not ported here): `ai/test/{anthropic-oauth,constrained-sampling,oauth-callback-server}.test.ts`, `codemode/test/sandbox.test.ts`, `coding-agent/test/experimental-*.test.ts`, `coding-agent/test/{mcp-extension,mcp-oauth-refresh,mcp-oauth-store,oauth-selector,tool-search}.test.ts`, `coding-agent/test/mcp-conformance/`, `coding-agent/test/suite/*`, `durable/test/*`, `mcp/test/*`, `server/test/conformance.test.ts`.

## Features without an upstream test

Upstream changed these sources without a test. Where a Go test can assert the source-defined output, an implementation-derived red test was added (same convention as `pi_logo_upstream_test.go`).

| Behavior | Upstream source | Go test | Status |
|---|---|---|---|
| `piWordmark()`: "Pi" in coral and yellow, in the terminal color mode | `pi-logo.ts:36-40` | `internal/codingagent/pi_logo_upstream_test.go#TestPiWordmarkUpstream` | added, red |
| Apple Terminal header: `Pi vX` line, key hints on the next line, no half-block logo | `interactive-mode.ts:1000-1006`, `pi-logo.ts:32-34` | `internal/codingagent/pi_logo_apple_terminal_darwin_test.go#TestBuiltInHeaderShowsTheWordmarkInAppleTerminalUpstream` (darwin only, because `isAppleTerminalSession` requires `process.platform === "darwin"`) | added; compiles (`GOOS=darwin go vet`), not run on this Linux host; expected red |
| Compact onboarding drops " and loaded resources" with `quietStartup: "header"` | `interactive-mode.ts:1045-1048` | `internal/codingagent/pi_logo_upstream_test.go#TestBuiltInHeaderOnboardingWithHeaderOnlyQuietStartupUpstream` | added, red |
| `--tui-mode` help text says fullscreen is the default | `cli/args.ts:326` | none: `cmd/pig/help_upstream.txt` is generated from the pinned Pi by `automation/gen/gen-help.sh` | regenerates with the pin move |
| `/settings` quiet startup values `true`/`header`/`false` and new descriptions | `settings-selector.ts:556-560, 708, 924` | none | follow-up for the fix lane (`internal/codingagent/slash_session_handlers.go:859`) |
| cmd/pig startup banner gate honors `"header"` | `interactive-mode.ts:996` | none: `showBanner` is computed inline in `cmd/pig/main.go:982` | follow-up for the fix lane |
| Model scope line hidden by `"header"` | `interactive-mode.ts:940` | none | follow-up for the fix lane |
| Logo-click easter egg | `interactive-mode.ts:254-263, 1058`, `pi-logo-animation*.ts` | none | skipped by task scope |
| `TuiAltScreen.getScreenLines()` | `tui-alt-screen.ts:315-318` | none upstream | interface-inventory follow-up |
| `UserMessageComponent` drops the wrapping `Box` (same output); `Markdown`/`Text`/`Box` flatten cached lines | `user-message.ts`, `markdown.ts`, `text.ts`, `box.ts`, `utils.ts:flattenLines` | none upstream (memory only, identical output) | no test to port |

## Signature stubs (production, 0.99.2 behavior)

- `internal/codingagent/startup_visibility.go`: `(*InteractiveMode).shouldShowStartupHeader` and `shouldShowStartupDetails` both return `Verbose || !Settings.QuietStartup`. No production code calls them yet. Pi calls them from the header gate, the model-scope line and `showLoadedResources`.
- `internal/codingagent/startup_header.go`: `piWordmark(mode)` returns `""`. The header never calls it.

## Failure list by suspected root cause

1. **`tuiMode` default is still regular.** `SettingsManager.GetTuiMode` returns `"fullscreen"` only for an explicit `"fullscreen"` (`internal/codingagent/settings.go:1768`); Pi 1.0.0 returns `"regular"` only for an explicit `"regular"` (`settings-manager.ts:1349`).
   - `TestUpstreamSettingsManager/TUI_mode/defaults_to_fullscreen_and_persists_regular_mode`
   - `TestUpstreamSettingsManager/TUI_mode/falls_back_to_fullscreen_for_unsupported_values`
   - `TestUpstreamSettingsManager/TUI_mode/does_not_recognize_the_old_uiMode_setting`
   - The fix changes the default for every interactive run that has no `tuiMode` setting (`internal/codingagent/interactive.go:1127`). Existing Go tests and parity scenarios that assume the regular default need review.
2. **`quietStartup` is a bool.** `Settings.QuietStartup` is `bool` and the wire field is `*bool` (`internal/codingagent/settings.go:266, 401`). The settings decoder drops `"header"` as undecodable, so it reads as `false`. Pi 1.0.0 has `QuietStartup = boolean | "header"` with `getQuietStartup` returning `true`, `"header"` or `false` (`settings-manager.ts:111-112, 1089-1092`). Header and details visibility are split (`interactive-mode.ts:1409-1417`).
   - `TestLoadedResourcesQuietStartupHeaderUpstream/hides_resource_listing_but_keeps_the_startup_header_with_header-only_quiet_startup`: the listing renders 2 children instead of 0.
   - `TestBuiltInHeaderOnboardingWithHeaderOnlyQuietStartupUpstream`: the onboarding line keeps " and loaded resources".
   - The fix also changes `setUpstreamQuietStartup` callers only if the JSON field name changes. Existing tests that assign `Settings.QuietStartup = true` need updating if the field type changes.
3. **No Apple Terminal wordmark.** `piWordmark` is a stub and `renderBuiltInHeader` always draws the half-block logo (`internal/codingagent/startup_header.go:35-39`).
   - `TestPiWordmarkUpstream`
   - `TestBuiltInHeaderShowsTheWordmarkInAppleTerminalUpstream` (darwin only)
4. **Slash autocomplete requires `/` at column 0.** `SlashOnlyProvider.GetSuggestions` and the combined provider test `strings.HasPrefix(before, "/")` on the untrimmed text (`tui/autocomplete.go:106, 181`, `tui/file_autocomplete.go:117`); Pi 1.0.0 uses `textBeforeCursor.trimStart()` (`autocomplete.ts:338-380`). `SuggestionTask` has the same check (`tui/file_autocomplete.go:148`).
   - `TestUpstreamAutocompleteSkillSlash/completes_commands_after_leading_whitespace_and_preserves_it`: `" /"` falls through to path completion of the filesystem root.
   - `TestUpstreamAutocompleteSkillSlash/completes_command_arguments_after_leading_whitespace`: no suggestions.
5. **`SliceByColumn` emits boundary codes before pending codes.** At the first in-range ANSI code, Pi 1.0.0 emits `pendingAnsi + code` and clears `pendingAnsi` (`utils.ts:1285-1289`). `tui/widthx/slice.go` keeps the old order: the slice starts `\x1b[39m\x1b[32m` instead of `\x1b[32m\x1b[39m`, so color leaks past fullscreen selection and search highlights (#10169).
   - `TestUpstreamSliceByColumnANSIOrderRegression/keeps_a_reset_at_the_slice_start_after_earlier_style_codes`
   - `TestUpstreamSliceByColumnANSIOrderRegression/does_not_leak_color_into_text_after_a_highlighted_token`
6. **System theme lacks the palette chroma cap.** Accent chroma is 0.179 against the pink's 0.089 (2x); `userMessageBg` chroma is 0.108 and `customMessageBg` 0.154, both above 0.1. Pi 1.0.0 caps palette chroma (`system-theme.ts`, #10255, #10293).
   - `TestGenerateSystemThemeColorsUpstream/keeps_pastel_palette_colors_pastel_at_other_lightnesses`
   - Corroborated by the existing Pi oracle `tui#TestGenerateSystemThemeColorsMatchesUpstream`, which now runs against Pi 1.0.0 and reports differences in 27 of its cases (for example case 23 `customMessageBg` `#317cbf` against Pi `#437cb3`). That test was not changed by this lane.
7. **`/login` labels every OAuth method "subscription", and the API-key login list ignores the runtime.**
   - `TestInteractiveLoginArgumentCompletionUpstream`: `/login radius` yields `Radius · subscription/API key`; Pi 1.0.0 yields `Radius · account`.
   - Cause a: `tui.FormatAuthSelectorProviderType(authType)` has no subscription argument (`tui/oauth_selector.go:53`); Pi 1.0.0 passes `provider.subscription` from `auth.oauth.isSubscription` (`interactive-mode.ts:405-417, 5722`).
   - Cause b: `oauthProviderList("login-api-key")` lists every `ai.APIKeyProviders()` entry without consulting `RequestAuthRuntime` (`internal/codingagent/interactive_auth.go:65-72`). The runtime's Radius provider has no API-key method, yet `/API key` is listed. Pi builds both lists from `modelRuntime` providers (`interactive-mode.ts:5722-5741`). This cause is suspected, not yet confirmed against a Pi probe.

## Ledger

`test/parity/interfaces/test-mapping-v1.0.0.json` moves the five area files from `pending` to `partial`: `settings-manager.test.ts`, `interactive-mode-status.test.ts`, `system-theme.test.ts`, `autocomplete-skill-slash.test.ts` and the new `regression-slice-by-column-ansi-order.test.ts`. Each entry keeps its 0.99.2 evidence, adds the new Go tests, and names the failing cases in its rationale. `go run ./test/parity/cmd/testinventorycheck` reports OK. The release gate (`-release-policy`) still blocks on these partial hot-path files until the fixes land, as it does on the other pending 1.0.0 files.

## Environment notes

- The worktree had no `extensions/sdk-ts/node_modules`, so every Pi-oracle test failed with `ENOENT`. After the rebase, `npm ci --ignore-scripts` in `extensions/sdk-ts` installed Pi 1.0.0 locally (not committed).
- Before the rebase (pin 0.99.2), the only failures in `./internal/codingagent`, `./tui` and `./tui/widthx` were the tests listed above.
- On `a8c294c02` alone, these tests outside this area also failed because of the pin move. After merging `48fff2a29` (porter tip), they pass and only the area tests above fail:
  - `internal/codingagent#TestAppKeybindingDefinitionsMatchUpstreamInventory`, `tui#TestTUIKeybindingDefinitionsMatchUpstreamInventory`, `tui#TestTreeAppKeybindingFixtureMatchesInventory`: `test/parity/interfaces/behavior-inputs-v1.0.0.json` does not exist yet.
  - `internal/codingagent#TestPiSharedFilesRoundTrip`, `TestPiSharedFilesConcurrentWriters`, `TestSessionEntryWritersPreserveSurrogateEscapes`, `TestW3SessionPiComparison`, `TestSettingsThemeLayerPresenceMatchesPi`: the Pi oracle scripts assert the installed Pi version is `0.99.2` (`'1.0.0' !== '0.99.2'`).
- `tui#TestUpstreamEditorCompletionIgnoresInvalidArgumentResult` failed once in a full `./tui` run on a cold toolchain and passed 20/20 alone and in a later full run. It is outside this area and was not changed.

## Commands

```
go test ./internal/codingagent ./tui ./tui/widthx -count=1   # failures listed above only
GOOS=darwin go vet ./internal/codingagent
go vet ./internal/codingagent ./tui/...
go tool golangci-lint run ./internal/codingagent/ ./tui/ ./tui/widthx/   # 0 issues
```
