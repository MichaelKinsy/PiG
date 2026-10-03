# fix-header-art

Lane: the built-in startup header draws the original PiG sprite login art (owner feedback on fix-114-pig-header, 2026-10-01). Base: the rev-fix-114-pig-header review branch.

Renders (truecolor, real binary in tmux, captured with `tmux capture-pane -e` and drawn cell by cell, half blocks as exact two-color cells):

- [fix-header-art/header-truecolor.png](fix-header-art/header-truecolor.png): the default `pig-default` sprite at 100 columns.
- [fix-header-art/header-sprites-and-fallbacks.png](fix-header-art/header-sprites-and-fallbacks.png): `pig-default`, `sheriff` and `pink` (classic gradient wordmark) at 100 columns, then the text mark at 52 columns, without truecolor and in Apple Terminal.
- [fix-header-art/header-text-mark.png](fix-header-art/header-text-mark.png): the text mark without truecolor and in Apple Terminal.

## Source of the art

PiG Standard's `piglogin` at MichaelKinsy/PiG d86eb93 (`piglets/standard/extensions/piglogin`), as carried by Pigpen (`components/pig-login/extensions/piglogin` and `components/pig-snake/port/upstream/piglogin-{art,variants}.go.txt`, the owner's bundle). The owner's bundle is byte-identical to Pigpen 07d65cb; its art file is byte-identical to d86eb93's `art.go`, and its goldens to d86eb93's `login-hpe-agentic-*.golden`. The data is unchanged: the 12-pixel "PiG." hero wordmark with its drop shadow (32 by 14), the 16-by-14 pig mascot (dark outline, eyes with white highlights, snout with nostrils), the ten sprites, and the sheriff's hat. No HPE text, no HPE tagline: the built-in header shows neither the sprite name nor its tagline (the `/sprite` picker still lists "Agentic PiG: Your tools. Your models. Your workflow." for `pig-default`, as the original did).

Note for the owner: the default sprite's wordmark is the original's flat website text (#E8F4F0) over a dark green shadow with a green period (#16866F), not a gradient. The cyan-to-navy gradient is the other nine sprites' wordmark, as in the original. The goldens pin this; changing the default to a green gradient would be new artwork, not the original.

## Design

- `coding/piglogin`: `art.go` and `variants.go` are the original files (package renamed; `FindVariant`, `ByID` and `Default` are this package's lookup), `definition.go` is the original `LoginDefinitionFor` (hero drawing included) returning the host's `extension.LoginDefinition`. `mark.go` is only the text mark now. The tiny 6x4 pig and 3x4 font, `SpriteMark`, `FallbackMark`, `MarkFor` and the `Mini`/`Period` fields are gone.
- `internal/codingagent/startup_header.go`: where the art fits (53 columns: 51 cells plus the padded text's margins), the terminal aligns half blocks (not Apple Terminal) and the theme is truecolor, the header is the 7 art lines, the version, then the hints at the left margin (the layout the v1.0.0 upstream mirror uses where its logo is not drawn). Otherwise the text mark takes the then-pinned Pi's 4-cell logo slot, so the hints wrap exactly as Pi's (scenario 13 at 52 columns is byte-equal from the first hint on). The art is drawn by the native login template's pixel renderer (`renderLoginPixels`, now shared with `RenderLoginHeader`) from the sprite's login definition, so a Piglet setting the same login draws the same pixels; it is cached per sprite (the header renders every frame).
- Text mark legibility: "PiG" is bold in the terminal's own foreground (a pale sprite such as mint or cloud in its body color was illegible on a light background), and the period takes the wordmark's period color.
- Overridable: `ctx.ui.setHeader` and a Piglet's `ctx.ui.setLogin` (D60) replace the header slot as before; `setHeader(undefined)` and `/reload` restore the art. `/sprite`, `/sprite list`, `/sprite set <id>` keep the ten original sprites.
- Quiet startup and modes: unchanged (`LoginVisible`); RPC/print have no header. In the v1.0.0 upstream mirror, fullscreen keeps the header in the same container and `quietStartup: "header"` hides details only, so the art stays; that needs no change here when it is ported.
- D2 records the row count: 12 lines compact and 29 expanded against the then-pinned Pi's 5 and 22, so 7 rows taller, and why.

## Tests

- Ported original tests (`coding/piglogin/login_art_test.go`): `TestDefaultVariantIsTheWebsiteGreenPig`, `TestPigDefaultResolvesByIDAndFromSavedState`, `TestPigDefaultMascotMatchesWebsitePig`, `TestPigDefaultLogoMatchesWebsiteWordmark`, `TestOtherVariantLogosFollowTheirPig`, `TestHeroDrawsPiGWithPeriodInsideTheGrid`, `TestEveryVariantProducesCompleteLoginDefinition`, `TestNoVariantDrawsTheSmallBrandWordmark`, `TestDefaultHeaderGoldenRenders` (50/66/120 against the original goldens), and pig-play's `TestEveryVariantSpriteIsTheSharedGrid`, `TestFindVariantFallsBackToDefault`, `TestEveryVariantPaletteCoversItsSprite`. The state twins were already in `state_test.go`.
- New host tests (`internal/codingagent/pi_logo_upstream_test.go`): `TestNativeLoginTemplateMatchesTheOriginalGoldens` (the real renderer, not the test mirror, against the goldens), `TestBuiltInHeaderDrawsTheOriginalArt` (art lines read from the golden file, version line, first hint, row counts 12/29), `TestBuiltInArtIsTheNativeLoginArt`, `TestBuiltInHeaderUsesTheArtAsSoonAsItFits` (53/52), `TestBuiltInHeaderFallsBackToTheTextMarkWhenNarrow`, `TestBuiltInHeaderWithoutTruecolorUsesTheTextMark`, `TestBuiltInHeaderUsesTheTextMarkInAppleTerminal`, `TestBuiltInHeaderShowsTheSelectedSprite`, `TestReloadRendersThePiGHeaderAgain`, `TestAnotherExtensionStillReplacesTheHeader`, `TestPigletSetLoginReplacesTheBuiltInArt`, plus the existing quiet-startup, narrow-terminal and `/sprite` tests.
- `coding/piglogin/mark_test.go`: the text mark (width 4, bold default foreground, period color, 256-color), sprites differ, the sheriff's hat.
- Mutations, each killed: art threshold `>=` to `>`; no truecolor gate; no half-block gate; version beside the art; `SetLogin` not installing its renderer; a wordmark pixel; a mascot eye pixel; text mark letters in the body color; a margin on the built-in art; the art ignoring the selected sprite.
- Parity: `startup` family green (00 asserts the art's first and last lines and the version line; 13 moved to 52 columns, where the text mark keeps Pi's wrap; its `both_contain` follows the wrap). `make parity-fast` result is in the lane report.
