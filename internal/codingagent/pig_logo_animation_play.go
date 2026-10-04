package codingagent

// Ports packages/coding-agent/src/modes/interactive/components/easter-egg-3d.lazy.ts (playPiLogo3d)
// Ports packages/coding-agent/src/modes/interactive/components/easter-egg-3d.ts (playEasterEgg3d)

import (
	"time"

	"github.com/MichaelKinsy/PiG/coding/piglogin"
	"github.com/MichaelKinsy/PiG/tui"
)

// logoTerminalColorQueryTimeout is the wait in milliseconds for the terminal's default colors before the animation starts.
// upstream: packages/coding-agent/src/modes/interactive/components/easter-egg-3d.ts:playEasterEgg3d
const logoTerminalColorQueryTimeout = 100

var logoOverlaySpec = tui.OverlaySpec{
	Anchor:    "top-left",
	Width:     &tui.OverlayValue{Value: 100, Percent: true},
	MaxHeight: &tui.OverlayValue{Value: 100, Percent: true},
}

// playPigLogoAnimation plays the logo easter egg. Only fullscreen mode can show it, because it dissolves the rendered
// screen, which it captures first. It then waits off the owner loop for the terminal's default colors, which fading
// needs (the theme only knows its own), and shows the animation as a fullscreen overlay that takes focus and mouse input
// and returns focus when hidden, so the rest of the UI keeps running underneath untouched. Pi loads the module on the
// first click; PiG links it.
func (m *InteractiveMode) playPigLogoAnimation(logoColumn, logoRow, logoColumns, logoRows int) {
	if m.altScreen == nil || m.tuiInst.HasOverlay() {
		return
	}
	screen := m.altScreen.GetScreenLines()
	if m.logoAnimationPlaying {
		return
	}
	ctx := m.backgroundCtx
	if ctx == nil {
		return
	}
	m.logoAnimationPlaying = true
	ui := m.tuiInst
	results := ui.QueryTerminalColors(tui.TerminalColorQueryOptions{TimeoutMs: logoTerminalColorQueryTimeout})
	m.backgroundTasks.Go(func() {
		var result tui.TerminalColorsResult
		select {
		case result = <-results:
		case <-ctx.Done():
			return
		}
		_ = m.postToMain(ctx, func() {
			m.showPigLogoAnimation(ui, screen, pigLogoAnimationOptions{logoColumn: logoColumn, logoRow: logoRow, clearColumns: logoColumns, clearRows: logoRows}, result)
		})
	})
}

// showPigLogoAnimation runs on the owner loop once the color query completed. A failed query or a renderer replaced in
// the meantime shows nothing.
func (m *InteractiveMode) showPigLogoAnimation(ui tui.Renderer, screen []string, options pigLogoAnimationOptions, result tui.TerminalColorsResult) {
	if result.Err != nil || ui != m.tuiInst || m.backgroundCtx == nil {
		m.logoAnimationPlaying = false
		return
	}
	theme := tui.ActiveTheme()
	foreground := logoToRgb(theme.ColorValues()["text"])
	if reported := result.Colors.Foreground; reported != nil {
		foreground = logoRgb{reported.R, reported.G, reported.B}
	}
	background := logoRgb{255, 255, 255}
	if theme.Appearance() == "dark" {
		background = logoRgb{0, 0, 0}
	}
	if reported := result.Colors.Background; reported != nil {
		background = logoRgb{reported.R, reported.G, reported.B}
	}
	if ui.HasOverlay() {
		m.logoAnimationPlaying = false
		return
	}
	var overlay *tui.OverlayHandle
	options.screen = screen
	animation := newPigLogoAnimation(ui.Height, options, piglogin.Active(), foreground, background, time.Now, func() {
		m.logoAnimationPlaying = false
		m.logoAnimation = nil
		overlay.Hide()
		// Pi's overlay hide() requests a render (tui.ts); PiG's overlay handle only invalidates.
		m.requestRender()
	})
	m.logoAnimation = animation
	overlay = ui.OpenOverlay(animation, logoOverlaySpec.Options())
	// Pi's showOverlay requests a render (tui.ts); PiG's OpenOverlay only invalidates.
	m.requestRender()
	animation.startTimer(m.backgroundCtx, m.backgroundTasks.Go, m.postToMain, m.requestRender)
}

// disposeLogoAnimation stops a playing animation's timer when the mode ends.
func (m *InteractiveMode) disposeLogoAnimation() {
	if m.logoAnimation != nil {
		m.logoAnimation.Dispose()
		m.logoAnimation = nil
	}
	m.logoAnimationPlaying = false
}
