# sprite-picker-preview: the pig beside the /sprite picker's rows (D2)

`sprite-picker.png` is the `/sprite` picker in a wide truecolor terminal. The arrow is on `Green PiG: Ready to build.`, and the green pig of that sprite is drawn in the preview column right after the list: four cells past the longest option row, top-aligned with the first option. On a wide terminal it stays next to the list instead of floating at the dialog's right edge. The pig follows the arrow: `Create your own...`, the last row, is not a sprite and draws none, so its list is the plain selector's full-width list. Below a 40-cell list body the preview is dropped, as before.

Reproduce it with:

```bash
go build -o /tmp/pig ./cmd/pig
/tmp/pig
```

Then type `/sprite` and move with the arrow keys.

The column is `tui.ExtensionSelectorComponent.SetPreview`, off for every selector that does not ask for one, so the dialogs Pi defines render exactly as before. `coding/piglogin/picker.go` supplies the head of the sprite under the cursor (`piglogin.HeadLines`) as the interactive host's custom component; a UI context without one (the RPC and no-op contexts) gets the plain selector with the same rows and title. The layout is held by `TestSpritePickerDrawsThePigOnTheRightOfTheRows`, `TestSpritePickerCreateRowDrawsNoPreview` and the width sweep at widths 1-120 in `tui/component_width_table_test.go`.
