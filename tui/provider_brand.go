package tui

// Port of coding-agent components/provider-brand.ts: animated brand styles for the names of preferred partner
// providers in selectors. Radius shimmers in its logo colors and OpenRouter glows in its brand color. Selectors apply
// them to the selected row only.

import (
	"math"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// ProviderBrandStyle colors text for animation time elapsed (ProviderBrandStyle).
type ProviderBrandStyle func(text string, elapsed time.Duration) string

// GetProviderBrandStyle is the animated style of a provider's name, or nil when the provider has none
// (getProviderBrandStyle).
func GetProviderBrandStyle(providerID string) ProviderBrandStyle {
	switch providerID {
	case ai.RadiusProviderID:
		return func(text string, elapsed time.Duration) string {
			return RadiusShimmer(text, elapsed, ActiveTheme().GetColorMode())
		}
	case "openrouter": //portlint:allow providerliteral provider-brand.ts getProviderBrandStyle `if (providerId === "openrouter")` names the same partner
		return openRouterGlow
	}
	return nil
}

// radiusColors are the four colors of the Radius logo, in the order they stream across the text.
var radiusColors = sync.OnceValue(func() []Color {
	colors := make([]Color, 0, 4)
	for _, hex := range []string{"#4d9abf", "#83ccd2", "#f1be57", "#f09082"} {
		color, err := ParseColor(hex)
		if err != nil {
			panic(err)
		}
		colors = append(colors, color)
	}
	return colors
})

const (
	// radiusCharsPerColor is the width of each color band, in characters.
	radiusCharsPerColor  = 4
	radiusCharsPerSecond = 10
)

// RadiusShimmer colors text with the Radius logo colors flowing left to right; elapsed is the animation time
// (radiusShimmer).
func RadiusShimmer(text string, elapsed time.Duration, mode TerminalColorMode) string {
	colors := radiusColors()
	cycle := float64(len(colors) * radiusCharsPerColor)
	offset := float64(elapsed.Seconds() * radiusCharsPerSecond)
	var result strings.Builder
	index := 0
	for _, char := range text {
		position := math.Mod(math.Mod(float64(index)-offset, cycle)+cycle, cycle)
		band := int(math.Floor(position / radiusCharsPerColor))
		t := position/radiusCharsPerColor - float64(band)
		// Smoothstep keeps each band recognizable while still blending into the next one.
		amount := t * t * (3 - float64(2*t))
		mixed, err := MixColors(colors[band], colors[(band+1)%len(colors)], amount, ColorMixSpaceSrgb)
		if err != nil {
			mixed = colors[band]
		}
		result.WriteString(ForegroundAnsi(mixed, mode))
		result.WriteRune(char)
		index++
	}
	result.WriteString("\x1b[39m")
	return result.String()
}

// openRouterGlowColors are OpenRouter's brand colors per theme appearance: lime on dark backgrounds, violet on light
// ones, where lime would be unreadable. The glow breathes between base and the brighter peak (OPENROUTER_GLOW).
var openRouterGlowColors = sync.OnceValue(func() map[ThemeAppearance][2]Color {
	parse := func(hex string) Color {
		color, err := ParseColor(hex)
		if err != nil {
			panic(err)
		}
		return color
	}
	return map[ThemeAppearance][2]Color{
		"dark":  {parse("#c6f243"), parse("#efffb0")},
		"light": {parse("#5d32e1"), parse("#8f6bff")},
	}
})

// upstream: packages/coding-agent/src/modes/interactive/components/provider-brand.ts:GLOW_PERIOD_MS
const openRouterGlowPeriod = 2400 * time.Millisecond

// openRouterGlow colors text in OpenRouter's brand color, slowly brightening and dimming.
func openRouterGlow(text string, elapsed time.Duration) string {
	theme := ActiveTheme()
	glow := openRouterGlowColors()
	pair, ok := glow[theme.Appearance()]
	if !ok {
		pair = glow["dark"]
	}
	amount := (1 - math.Cos(2*math.Pi*float64(elapsed)/float64(openRouterGlowPeriod))) / 2
	mixed, err := MixColors(pair[0], pair[1], amount, ColorMixSpaceOklch)
	if err != nil {
		mixed = pair[0]
	}
	return ForegroundAnsi(mixed, theme.GetColorMode()) + text + "\x1b[39m"
}

// BrandAnimationFrame is the interval of the brand animation (ANIMATION_FRAME_MS).
// upstream: packages/coding-agent/src/modes/interactive/components/provider-brand.ts:ANIMATION_FRAME_MS
const BrandAnimationFrame = 50 * time.Millisecond

// StartBrandAnimation calls requestRender every animation frame while isAnimating reports true, on its own
// goroutine, and returns the function that stops it (startBrandAnimation). The stop function returns once the
// goroutine has exited, so no request follows it, and it may be called more than once.
func StartBrandAnimation(requestRender func(), isAnimating func() bool) (stop func()) {
	done, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(BrandAnimationFrame)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if isAnimating() {
					requestRender()
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(done) })
		<-finished
	}
}
