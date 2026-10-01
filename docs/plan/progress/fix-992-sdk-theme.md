# fix-992-sdk-theme: ctx.ui.theme appearance, colors, style and faint fg in every SDK

Base: `fix-992-sdk-surface` @ 2f5b3dc60 (its red commit already ported the theme-style tests and stubs for Go, Rust, Python, Node). Scope: the `ctx.ui.theme.{appearance,colors,style}` cluster. The lead also added the Go/Python/Rust `theme.fg` faint-token close (SGR 22;39); fix-992-parity-misc (accepted) closes the same piece in the Go/Python/Rust SDKs and Node, so its code and unit tests won in the merge (item 6); this lane keeps only the cross-SDK conformance row. The Node modelRegistry and `provider.images`/`provider.classifiers` cells stay with fix-992-sdk-surface.

## Upstream rules

- `theme.ts:311-336` `appearance` (declared, detected, else the terminal's) and `colors` (a concrete color per token; a token set to "" takes the terminal's reported color or the appearance guess; a faint token is mixed 0.4 toward the background). The host resolves both, because they depend on the terminal's reported colors, which an extension process cannot see; the palette carries `appearance` and `colors` (`{kind: indexed|rgb|oklch, ...}`).
- `theme.ts:342-367` `style(text, {fg, bg, ...attributes})`: a token is accepted only in its own slot (`Unknown theme color: <token>`), a faint token adds `dim`, a color renders with `foregroundAnsi`/`backgroundAnsi` in the theme's own mode, attributes are SGR drawn directly (not chalk).
- `theme.ts:361-365,399-402` `fg` closes a faint token (opening ends in SGR 2) with `\x1b[22;39m`.
- `theme-style.test.ts` (the ported inputs): token styling, unknown/wrong-slot tokens, OKLCH, appearance, terminal-default colors.

## Commits (red/green order)

1. `2f5b3dc60` (base, from fix-992-sdk-surface) red: ported theme-style tests with signature stubs. Red run recorded there: `extensions/sdk` 4 UITheme tests fail, `sdk-rs` 3 of 4 theme tests fail, `sdk-py` every theme-style case fails, `TestNodeThemeAppearanceColorsAndStyle` (`theme.colors` undefined), `TestExtensionThemePaletteCarriesAppearanceColorsAndMode` (no `appearance`, no `colors`), `TestThemeAcrossSDKs` fails for Go (fused, strict, packed), Rust, Python, Node.
2. `78527e178` green: host palette (appearance, colors, theme's own mode) + Go/Rust/Python/Node `style`, `appearance`, `colors`; `themeFromPalette` carries the resolved appearance and colors; sdk-surface map rows; parity doc row.
3. `4f53063ff` red (lead addition): faint-token `theme.fg` unit tests and the conformance row `TestThemeFaintForegroundAcrossSDKs` (the system theme with no reported terminal colors, every foreground token compared with the host's `Theme.FgText`). Red run: Go, Python, Rust unit tests fail with `...x\x1b[39m` for the faint token; the conformance row fails for every SDK.
4. `63799df64` green: Go `UITheme.Fg`, Python `Theme.fg`, Rust `Theme::fg`, Node `ThemeShim.fg` close a faint token with SGR 22;39. Superseded by item 6: parity-misc's identical fix replaced it in the merge.
5. test(extensions): out-of-gamut OKLCH vectors. Mutation check showed the original vector (`oklch(1 0.3 150)` -> white) also passes without the chroma bisection (the achromatic fallback is white); new vectors (`oklch(0.7 0.3 150)`, `oklch(0.5 0.4 30)`) fail with `for range 0`. The conformance table has the same two colors.
6. Merge of `rev-fix-992-parity-misc` (lead instruction): its faint `fg` code and unit tests (`ui_theme_faint_test.go`, `test_theme_faint.py`, `node_theme_shim_faint_test.go`, Rust `fg_closes_a_faint_token_with_sgr_22_39`) are kept as is; this lane's duplicate unit tests are dropped (the conformance row stays); `UITheme.Style` uses parity-misc's `faintOpening`. Generated files (runtime-node.zip and digest, `pig-go.json`) regenerated after the merge.
7. `chore(port-99): regenerate generated files` (only what differs after the merge, including `docs/extension-sdk-surface.md` and recommendations).

## Mutation checks (key fixes)

- Go SDK chroma reduction loop `20 -> 0`: caught by `TestUIThemeStyleMapsOutOfGamutOklchByLosingChroma` (after commit 5; not by the original vectors).
- Host palette `colors` emptied: `TestExtensionThemePaletteCarriesAppearanceColorsAndMode` fails. Host palette `mode` forced to truecolor: same test fails (256color).
- Python half-up rounding replaced by banker's `round`: `fractional rgb rounds half up` fails. Rust `attributes.dim = true` removed: the faint-token style case fails.

## Load tests

`GOMAXPROCS=4 go test -race -count=24` over the Go SDK theme tests and `TestExtensionThemePalette...`; `taskset -c 0-3` with four CPU burners, `-race -count=6` over `TestThemeFaintForegroundAcrossSDKs` and `TestThemeAcrossSDKs/{go,python,node}`: all pass. The theme code is pure functions over an immutable replicated palette; no goroutine, process or stream path is added.

## Behavior notes

- The palette's `mode` was the terminal capability's mode; it is now the theme's own mode (`theme.ColorMode()`), as Pi's `getColorMode()` is, because the escape sequences in the palette are built for it.
- `colors` follow the terminal's reported colors only as often as the host publishes the palette (state snapshot, theme change). Pi's `colors` getter is live. The host does not republish when the terminal's colors change alone; republishing on a terminal-color change is a separate host feature.
- A malformed wire color (unknown kind, a missing or non-finite channel, an index outside 0-255) is dropped from `colors`, as a token without an escape sequence is. In `style`, a malformed color in the options raises in Python (`ValueError`) and follows Pi's TypeError in Node.

## Deferred / environment

- The pinned Pi 0.99.2 npm package was missing during the first runs and installed later; `TestPiThemeHelpersMatchThePinnedPackage` (the one that re-checks `ThemeShim` against Pi's own theme), `cmd/pig` (full) and `internal/codingagent` (full) pass with it.
- `coding/extension/host/subprocess` (full): `TestNodeModelRegistryTypedOperations`, `TestNodeModelRegistryVirtualModels` and `TestNodeProviderConfigImagesAndClassifiers` are the fix-992-sdk-surface lane's red tests (Node modelRegistry and provider images/classifiers, not this lane's scope); `TestNodeVendoredTuiUpstreamTests/native-clipboard-linux` needs Xvfb, which this host lacks. An `extensions/sdk-py/.venv` left by `uv run` makes `TestPackedRunnerWithAnotherSDKsRegisterShapeNamesTheRebuild` fail (it copies the SDK tree), so the venv and caches are removed after each run.
- `make parity-family FAMILY=extensions-runtime` fails on scenarios 26, 28, 29, 34, 55 and 56 on this branch and on its base (2f5b3dc60), with the same diffs (builtin command order `llama`/`mcp`, truecolor vs system-theme colors in 56, export renderers); the set of other failures (24, 25, 35b, 36, 54, 65, 66) differs from run to run on both, so it is load flakiness, not this lane. None of them asserts `ctx.ui.theme.appearance`, `colors` or `style`.
- `make generate` stops at `known-gaps` with `packages/ai/test/images-models.test.ts ... designedOutCases differ from the approved policy list`, from another lane. The generated files this lane owns were regenerated before it.
- Rust SDK fmt/clippy are not clean on the base; `theme_color.rs` is rustfmt- and clippy-clean (the Oklab constants carry `#[allow(clippy::excessive_precision)]` so they stay bit-identical to oklab.ts). `go fix -diff` reports files outside this lane.

## Stubs other families must fill

None. The base's `ExtensionThemePalette` export (renamed from `extensionThemePalette` for the conformance test) now has its production caller path (`ActiveExtensionTheme`, `GetTheme`).
