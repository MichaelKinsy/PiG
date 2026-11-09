package codingagent

// Ports packages/coding-agent/src/modes/interactive/components/radius-login-selector.ts

import (
	"slices"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

const (
	radiusLoginIntro      = "Radius is a service crafted for Pi by the builders of Pi, Earendil Works"
	radiusLoginMenuPrefix = "Sign in with "
)

// radiusLoginMenu is the top-level /login selector whose "Sign in with Radius" option shimmers in the Radius logo
// colors while it is selected. It swaps the line the selector draws for the selected Radius option with the animated
// one; when the selector's row style changes or the label wraps, the line no longer matches and the option renders
// normally.
type radiusLoginMenu struct {
	*tui.ExtensionSelectorComponent
	label, text   string
	start         time.Time
	now           func() time.Time
	animating     atomic.Bool
	stopAnimation func()
}

// newRadiusLoginMenu starts the animation ticker, which requests a render every frame while the option is selected.
// Dispose stops it.
func newRadiusLoginMenu(selector *tui.ExtensionSelectorComponent, label, text string, requestRender func()) *radiusLoginMenu {
	menu := &radiusLoginMenu{ExtensionSelectorComponent: selector, label: label, text: text, start: time.Now(), now: time.Now}
	menu.stopAnimation = tui.StartBrandAnimation(requestRender, menu.animating.Load)
	return menu
}

// Render mirrors RadiusLoginMenuComponent.render.
func (r *radiusLoginMenu) Render(width int) []string {
	lines := r.ExtensionSelectorComponent.Render(width)
	theme := tui.ActiveTheme()
	selected := tui.NewPaddedText(theme.Fg("accent", "→ ")+theme.Fg("accent", r.label), 1, 0, nil).Render(width)
	index := -1
	if len(selected) > 0 {
		index = slices.Index(lines, selected[0])
	}
	r.animating.Store(index >= 0)
	if index >= 0 {
		shimmer := tui.RadiusShimmer(r.text, r.now().Sub(r.start), theme.GetColorMode())
		animated := theme.Fg("accent", "→ ") + shimmer + r.label[len(r.text):]
		rendered := tui.NewPaddedText(animated, 1, 0, nil).Render(width)
		lines[index] = ""
		if len(rendered) > 0 {
			lines[index] = rendered[0]
		}
	}
	return lines
}

// Dispose stops the animation.
func (r *radiusLoginMenu) Dispose() {
	r.stopAnimation()
	r.animating.Store(false)
}
