package piglogin

import (
	"context"
	"errors"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// spritePickerTitle is the picker's title, shown by /sprite with and without a preview column.
const spritePickerTitle = "Choose a PiG sprite"

// spriteOptions are the picker's rows: every sprite as "Name: Tagline", then the row that explains how to create one.
func spriteOptions(variants []Variant) []string {
	options := make([]string, 0, len(variants)+1)
	for _, variant := range variants {
		options = append(options, variant.Name+": "+variant.Tagline)
	}
	return append(options, CreateOption)
}

// chooseSprite shows the sprite picker and returns the chosen row, or "" when the user dismissed it. The rows carry the
// pig of the sprite they highlight, drawn beside them on the right.
// pig divergence (D2): the picker draws a pig beside its rows; Pi has no /sprite and no sprite to draw.
//
// A UI context that cannot mount a custom component answers the custom call with no result (the RPC and no-op
// contexts) or fails it (a subprocess extension's bridge), and those answer select instead, so the plain extension
// selector, which has no preview column, is the fallback. A custom call that the user dismissed ends the choice here.
func chooseSprite(ctx context.Context, ui extension.UIContext, variants []Variant, options []string) (string, error) {
	result, err := ui.Custom(ctx, spritePickerFactory(variants, options), extension.CustomOptions{})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return "", nil
		}
	} else if selected, ok := result.(string); ok {
		return selected, nil
	}
	selected, err := ui.Select(ctx, spritePickerTitle, options, nil)
	if errors.Is(err, context.Canceled) {
		return "", nil
	}
	return selected, err
}

// spritePickerFactory builds the picker for the interactive host: the extension selector's list, keys and hints, with
// the pig of the highlighted sprite drawn beside the list. Selecting a row ends the ui.Custom call with the row's text;
// cancelling ends it with "".
func spritePickerFactory(variants []Variant, options []string) extension.CustomFactory {
	return func(_ extension.CustomHost, theme extension.Theme, _ extension.KeybindingsManager, done func(any)) (extension.Component, error) {
		mode := tui.TerminalColorModeTrueColor
		if t, ok := theme.(*tui.Theme); ok && t != nil {
			mode = t.ColorMode()
		}
		selector := tui.NewExtensionSelector(spritePickerTitle, options)
		selector.SetPreview(func(selected int) []string {
			// The create row is not a sprite: it draws no preview, so the selector keeps the full-width list it has
			// without one.
			if selected < 0 || selected >= len(variants) {
				return nil
			}
			return HeadLines(variants[selected], mode)
		})
		return &spritePicker{selector: selector, done: done}, nil
	}
}

// spritePicker is the picker in the editor slot: the selector's rows, keys and preview column, and the choice it
// carries back through the custom call's done callback.
type spritePicker struct {
	selector *tui.ExtensionSelectorComponent
	done     func(any)
}

func (p *spritePicker) Render(width int) []string { return p.selector.Render(width) }

func (p *spritePicker) Invalidate() { p.selector.Invalidate() }

// HandleInput lets the selector complete on the key that completes it, then ends the custom call with its choice.
func (p *spritePicker) HandleInput(data string) {
	if p.selector.Done() {
		return
	}
	p.selector.HandleInput(data)
	if p.selector.Done() {
		p.done(p.selector.SelectedValue())
	}
}
