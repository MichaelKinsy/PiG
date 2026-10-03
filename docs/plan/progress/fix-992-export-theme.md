# fix-992-export-theme

P5 of the 0.99.2 parity aggregate: the export HTML theme variables differ between Pi and PiG in scenarios 01/02/03/04/06-export-cli and 29-export-tool-renderers.

## Findings

- Environment dependence: none. upstream 0.99.2 writes identical `--accent: #800080` and `--exportPageBg: rgb(0, 0, 0)` for COLORTERM in {truecolor, 24bit, unset} and TERM in {xterm-256color, dumb}. Its export reads `Theme.colors` (Color values through `colorToHex`), never the ANSI mode (`theme.ts:903-906`).
- Root cause 1: `pi --export` runs before `initTheme` (`main.ts:633` versus `:898`), so `getResolvedThemeColors(undefined)` loads the system theme (`theme.ts:904`: `loadTheme(themeName ?? currentThemeName ?? SYSTEM_THEME_NAME)`). PiG read `tui.ActiveTheme()`, which falls back to the built-in dark theme before any selection.
- Root cause 2: `deriveExportColors` (`export-html/index.ts:42-107`) was never ported. For a theme without an export section (the system theme, `theme.ts:924-925`) PiG used the constants `#18181e/#1e1e24/#3c3728` instead of deriving the page, card and info backgrounds from `userMessageBg`.

## Red

Commit `test(export-html): port upstream 0.99.2 export theme tests with signature stubs (red)`. Upstream has no new or changed export or theme test between 0.99.1 and 0.99.2 for this scope (`diff -rq` of packages/coding-agent/test, src/core/export-html and src/modes/interactive/theme is empty), so the tests encode Pi's observed values.

Failing for the right reason (stubs return nil or an empty struct):
- `internal/codingagent/export`: `TestDeriveExportColors` (9 cases).
- `tui`: `TestExportThemeIsSystemUntilAThemeIsSelected`, `TestExportThemeIsTheSelectedTheme`.
- `cmd/pig`: `TestExportCLIUsesTheSystemThemeColors` (3 COLORTERM values; PiG exported the dark theme's `#a798d7` accent and `#21252c` page background).

## Green

- `fix(export-html): export reads the selected or system theme and derives page colors; non-interactive modes apply the configured theme (green)`: `tui.ExportTheme` (selected theme, else a generated system theme; theme.ts:904), `deriveExportColors` and its helpers (index.ts:42-107, 119-125), and `initTheme` in cmd/pig for print, JSON and RPC runs (main.ts:898; one helper now shared with `pig config`). The existing `TestGenerateThemeVars_IncludesScrollbarThumb` now selects the dark theme first (Pi theme.ts:904), and `TestMissingConfiguredThemeExportsWithActiveFallback` expects an `rgb()` page background (index.ts:42-107,121; the original asserted `#rrggbb`, which Pi never writes for the system theme).
- `fix(node-runtime): the active host theme closes a faint token with SGR 22;39 ...(green)`: ThemeShim.fg (theme.ts:363, 399-402). Runtime archive and digest regenerated.
- `test(export-html): pin both sides of deriveExportColors' luminance threshold (mutation-proven)`: a `> 0.9` mutation survived the first nine cases.

## Evidence

- Parity against upstream 0.99.2 (stand-in oracle `/tmp/tmp.AA1tLTiLWH`, version 0.99.2, via PIG_PARITY_PI_BIN, because the mise 0.99.2 install is absent), serial: 01, 02, 03, 04, 05, 06 export-cli, 29-export-tool-renderers, 38/39 rpc-export-html pass. 56-node-lazy-imports passes with the updated expectation from fix-992-parity-fixtures (`38;5;2`); it fails before this change.
- Full hermetic parity run on the green tree (24-way, 340 s): 38 failures, none in export-html or theme. The 24/25 tool-renderer scenarios, which the aggregate listed as P6, pass. The remaining failures are P1/P2/P3/P4/P6 groups owned by other lanes, plus fixtures that import Pi's mise 0.99.2 install (`pi exit_code=1`), which does not exist on this host.
- Mutations (each caught): `ExportTheme` always `ActiveTheme()` (tui tests), `initTheme` without the selection (`TestInitThemeAppliesTheConfiguredTheme`), luminance threshold 0.5 to 0.9 (`TestDeriveExportColors`), the main.go `initTheme` call removed (parity 29).
- Load: `-race -count=24` on `internal/codingagent/export` and `tui` theme tests under `GOMAXPROCS=4 taskset -c 0-3`; `-race -count=6` for the cmd/pig theme and export tests. Both pass.
- Gates: build, vet, `GOOS=windows go vet` (tui, export, cmd/pig), gofmt, golangci-lint (tui, export, cmd/pig, subprocess: 0 issues), `go fix -diff` empty.
- `go test -race ./coding/extension/host/subprocess` exceeds 600 s under host load; without `-race` the package runs in 475 s and fails only on `native-clipboard-linux` (Xvfb not installed on this host). `cmd/pig` has `TestRPCInputEnd...` (aggregate item B, rpc_shutdown_test.go) timing out under load, unrelated.
- `make generate` stops at the known-gaps release policy (aggregate item C, images-models designed-out row, owner decision). It wrote the interface inventory and recommendations for the new symbols; those are the final chore commit.

## Deferred

- Python and Rust SDK themes still close a faint token with SGR 39 (fix-992-sdk-surface owns those theme cells; QUESTION posted).
- No stub for another family.
