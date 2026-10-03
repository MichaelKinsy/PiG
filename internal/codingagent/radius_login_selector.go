package codingagent

// Ports packages/coding-agent/src/modes/interactive/components/radius-login-selector.ts

import (
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// radiusColors are the four colors of the Radius logo, in the order they stream across the text.
var radiusColors = sync.OnceValue(func() []tui.Color {
	colors := make([]tui.Color, 0, 4)
	for _, hex := range []string{"#4d9abf", "#83ccd2", "#f1be57", "#f09082"} {
		color, err := tui.ParseColor(hex)
		if err != nil {
			panic(err)
		}
		colors = append(colors, color)
	}
	return colors
})

const (
	// radiusCharsPerColor is the width of each color band, in characters.
	radiusCharsPerColor   = 4
	radiusCharsPerSecond  = 10
	radiusAnimationFrame  = 50 * time.Millisecond
	radiusLoginIntro      = "Radius is a service crafted for Pi by the builders of Pi, Earendil Works"
	radiusLoginMenuPrefix = "Sign in with "
)

// radiusShimmer colors text with the Radius logo colors flowing left to right; elapsed is the animation time.
func radiusShimmer(text string, elapsed time.Duration, mode tui.TerminalColorMode) string {
	colors := radiusColors()
	cycle := float64(len(colors) * radiusCharsPerColor)
	offset := elapsed.Seconds() * radiusCharsPerSecond
	var result strings.Builder
	index := 0
	for _, char := range text {
		position := math.Mod(math.Mod(float64(index)-offset, cycle)+cycle, cycle)
		band := int(math.Floor(position / radiusCharsPerColor))
		t := position/radiusCharsPerColor - float64(band)
		// Smoothstep keeps each band recognizable while still blending into the next one.
		amount := t * t * (3 - 2*t)
		mixed, err := tui.MixColors(colors[band], colors[(band+1)%len(colors)], amount, tui.ColorMixSpaceSrgb)
		if err != nil {
			mixed = colors[band]
		}
		result.WriteString(tui.ForegroundAnsi(mixed, mode))
		result.WriteRune(char)
		index++
	}
	result.WriteString("\x1b[39m")
	return result.String()
}

// radiusLoginMenu is the top-level /login selector whose "Sign in with Radius" option shimmers in the Radius logo
// colors while it is selected. It swaps the line the selector draws for the selected Radius option with the animated
// one; when the selector's row style changes or the label wraps, the line no longer matches and the option renders
// normally.
type radiusLoginMenu struct {
	*tui.ExtensionSelectorComponent
	label, text string
	start       time.Time
	animating   atomic.Bool
	stop        chan struct{}
	stopOnce    sync.Once
}

// newRadiusLoginMenu starts the animation ticker, which requests a render every frame while the option is selected.
// Dispose stops it.
func newRadiusLoginMenu(selector *tui.ExtensionSelectorComponent, label, text string, requestRender func()) *radiusLoginMenu {
	menu := &radiusLoginMenu{ExtensionSelectorComponent: selector, label: label, text: text, start: time.Now(), stop: make(chan struct{})}
	go func() {
		ticker := time.NewTicker(radiusAnimationFrame)
		defer ticker.Stop()
		for {
			select {
			case <-menu.stop:
				return
			case <-ticker.C:
				if menu.animating.Load() {
					requestRender()
				}
			}
		}
	}()
	return menu
}

// Render mirrors RadiusLoginMenuComponent.render.
func (r *radiusLoginMenu) Render(width int) []string {
	lines := r.ExtensionSelectorComponent.Render(width)
	theme := tui.ActiveTheme()
	selected := tui.NewPaddedText(theme.FgText("accent", "→ ")+theme.FgText("accent", r.label), 1, 0, nil).Render(width)
	index := -1
	if len(selected) > 0 {
		index = slices.Index(lines, selected[0])
	}
	r.animating.Store(index >= 0)
	if index >= 0 {
		shimmer := radiusShimmer(r.text, time.Since(r.start), theme.ColorMode())
		animated := theme.FgText("accent", "→ ") + shimmer + r.label[len(r.text):]
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
	r.stopOnce.Do(func() { close(r.stop) })
	r.animating.Store(false)
}
