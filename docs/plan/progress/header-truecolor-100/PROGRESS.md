# header-truecolor-100: pin the color level in the PiG header tests (C18)

Branch `header-truecolor-100`. Base: `agg-100` `269a0458c`. Pi 1.0.0.

## Status

READY.

## Cause

The built-in header draws the pig head only when the active theme resolves in truecolor (`internal/codingagent/startup_header.go` `renderBuiltInHeader`: `mode == tui.TerminalColorModeTrueColor`); otherwise it draws the one-line text mark (D2). The header and logo-click tests built the header in whatever color level the host terminal reports. CI sets `CI=true GITHUB_ACTIONS=true` and no `COLORTERM`, so `detectCapabilitiesFromEnvironment` reports 256 colors there, the header draws the text mark, and every assertion on the head fails. A developer terminal passes because it reports truecolor (`COLORTERM=truecolor`, or a terminal that implies it, such as Herdr with `HERDR_KITTY_GRAPHICS=1`). The production behavior is correct and matches the D2 design; only the tests depended on the environment.

## Fix

- `pinTerminalColorMode(t, mode)` (renamed `pinHeaderTerminal` by review rev-header-truecolor-100, which also pins the half-block check and restores the theme in its own color mode) (`internal/codingagent/pi_logo_upstream_test.go`) sets the capability, activates the dark theme in that mode, checks the theme's mode, and restores the capabilities and the previous theme in cleanup. It follows fix-114-public-ci (`fcf00b900`) and fix-114-ci2 (`a932c13b8`), which pinned truecolor in the other tests that compare 24-bit colors.
- `newHeaderMode` pins truecolor. A test that asserts another level pins it after the fixture (`TestBuiltInHeaderWithoutTruecolorUsesTheTextMark` now uses the helper for both its 256-color and truecolor halves).
- `TestBuiltInHeaderOnboardingWithHeaderOnlyQuietStartupUpstream` builds its own mode; it pins truecolor so its "beside the pig head" layout is the one it asserts.
- `newLogoClickMode` does not pin, because `TestClickingTheTextMarkPlaysTheAnimation` builds it in 256 colors. Each test that clicks or animates the head pins truecolor first: `TestClickingTheHeaderPigPlaysTheAnimation`, `TestEveryCellOfTheHeaderPigPlaysTheAnimation`, `TestHeaderPigClickNeedsTheBuiltInSprite`, `TestHeaderPigClickIgnoredWhileAnOverlayOrTheAnimationIsOpen`, `TestPigLogoAnimationColors`. `TestClickingTheTextMarkPlaysTheAnimation` pins each case's level: 256 colors, and truecolor for the narrow case, whose text mark must come from the width alone.

No assertion is loosened; no production code changes.

## Evidence

The CI environment is reproduced with `env -i` keeping only `HOME`, `PATH`, the Go cache variables, `TERM=xterm-256color`, `CI=true` and `GITHUB_ACTIONS=true` (so no `COLORTERM`, `HERDR_*` or other terminal hints).

- Red on the base, CI environment: `go test -count=1 ./internal/codingagent -run 'Header|Logo|Sprite|Piglet|Clicking|EveryCell|Wordmark|Reload'` fails 13 tests: `TestBuiltInHeaderDrawsThePigHeadInPisLogoSlot`, `TestBuiltInHeaderAt80Columns`, `TestBuiltInHeaderUsesTheHeadAsSoonAsTheVersionFits`, `TestBuiltInHeaderFallsBackToTheTextMarkWhenNarrow`, `TestBuiltInHeaderShowsTheSelectedSprite`, `TestReloadRendersThePiGHeaderAgain`, `TestAnotherExtensionStillReplacesTheHeader`, `TestPigletSetLoginReplacesTheBuiltInArt`, `TestSpriteSetRepaintsTheHeaderThroughTheInteractiveHost`, `TestHeaderLogoAreaIsTheDrawnPig`, `TestClickingTheHeaderPigPlaysTheAnimation`, `TestEveryCellOfTheHeaderPigPlaysTheAnimation`, `TestHeaderPigClickNeedsTheBuiltInSprite`. In the developer environment (truecolor) the same command passes on the base.
- Green after the fix, same selection: CI environment, CI environment plus `COLORTERM=truecolor`, and the developer environment.
- Races: the same selection plus `TestSessionReplacementDropsTheOutgoingBuildsSprites` (the other `newHeaderMode` user), CI environment, `taskset -c 0-3`, `GOMAXPROCS=4`, `-count=50 -shuffle=on`: 0 failures (1887s). `-race -count=3 -shuffle=on` on 4 CPUs: pass.
- Full package, CI environment: `go test -count=1 ./internal/codingagent` passes (with `extensions/sdk-ts` installed by `npm ci --prefix extensions/sdk-ts --ignore-scripts`, as CI does; without it 14 Pi-oracle tests fail on the missing package, unrelated to this lane).
- `go vet`, `golangci-lint run ./internal/codingagent/...` (0 issues), `go fix -diff` (empty).

## Observed, not in scope

`go test -shuffle=1790935218379974833 ./internal/codingagent` fails `TestShellBodyRendererTruncationWarnings`, `TestShellBodyRendererCollapsedPreview` and `TestAuditEditDiffContextLinesUseTheToolDiffContextColor` on this branch; the base fails those three and `TestThemePathsFirstPathWinsCollisions`, `TestArminFramesMatchPinnedPi`, `TestPigLogoAnimationMatchesPinnedPi` and `TestUpstreamCoreSkillsOptions` with the same seed. These tests depend on global theme state another test leaves behind. CI does not shuffle, so they do not fail CI; they need their own lane.
