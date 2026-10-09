package codingagent

// Ports packages/coding-agent/src/modes/interactive/components/easter-egg-3d.lazy.ts (playPiLogo3d, playArmin3d)
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

// playPigLogoAnimation plays the logo easter egg. Only fullscreen mode, or a frontend session that shows fullscreen
// overlays (D91), can show it, because it dissolves the rendered screen, which it captures first. It then waits off the
// owner loop for the terminal's default colors, which fading
// needs (the theme only knows its own), and shows the animation as a fullscreen overlay that takes focus and mouse input
// and returns focus when hidden, so the rest of the UI keeps running underneath untouched. Pi loads the module on the
// first click; PiG links it.
func (m *InteractiveMode) playPigLogoAnimation(logoColumn, logoRow, logoColumns, logoRows int) {
	m.playEgg3d(func(rows func() int, screen []string, variant piglogin.Variant, foreground, background logoRgb, onDone func()) *pigLogoAnimation {
		return newPigLogoAnimation(rows, pigLogoAnimationOptions{screen: screen, logoColumn: logoColumn, logoRow: logoRow, clearColumns: logoColumns, clearRows: logoRows}, variant, foreground, background, time.Now, onDone)
	})
}

// playPig3d plays the 3D pig of /arminsayshi and /pigsayhi (Pi's playArmin3d). It reports false when it cannot play, so
// the caller can fall back to the inline pig head: only fullscreen mode, or a frontend session that shows fullscreen
// overlays (D91), can show it.
func (m *InteractiveMode) playPig3d() bool {
	return m.playEgg3d(func(rows func() int, screen []string, variant piglogin.Variant, foreground, background logoRgb, onDone func()) *pigLogoAnimation {
		return newPig3dAnimation(rows, screen, variant, foreground, background, time.Now, onDone)
	})
}

// egg3dFactory builds an easter egg's animation once the colors are known.
type egg3dFactory func(rows func() int, screen []string, variant piglogin.Variant, foreground, background logoRgb, onDone func()) *pigLogoAnimation

// playEgg3d is Pi's lazy playEasterEgg3d: false outside fullscreen mode, true without playing while an overlay is shown.
// pig additive (D91): a frontend session that shows fullscreen overlays plays it as fullscreen mode does, from the screen
// as the session shows it.
func (m *InteractiveMode) playEgg3d(build egg3dFactory) bool {
	surface := m.eggSurface()
	if m.altScreen == nil && surface == nil {
		return false
	}
	if m.tuiInst.HasOverlay() {
		return true
	}
	var screen []string
	if surface != nil {
		screen = surface.ScreenLines()
	} else {
		screen = m.altScreen.GetScreenLines()
	}
	if m.logoAnimationPlaying {
		return true
	}
	ctx := m.backgroundCtx
	if ctx == nil {
		return true
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
			m.showPigLogoAnimation(ui, screen, build, result)
		})
	})
	return true
}

// showPigLogoAnimation runs on the owner loop once the color query completed. A failed query or a renderer replaced in
// the meantime shows nothing.
func (m *InteractiveMode) showPigLogoAnimation(ui tui.TUI, screen []string, build egg3dFactory, result tui.TerminalColorsResult) {
	if result.Err != nil || ui != m.tuiInst || m.backgroundCtx == nil {
		m.logoAnimationPlaying = false
		return
	}
	theme := tui.ActiveTheme()
	foreground := logoToRgb(theme.Colors()["text"])
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
	rows, options, requestRender := ui.Height, logoOverlaySpec.Options(), m.requestRender
	var clock *eggClock
	if surface, ok := ui.(*tui.TuiSurface); ok {
		// pig additive (D91): the animation covers the session's screen, and pauses while its view is hidden.
		clock = &eggClock{hidden: func() bool {
			screen, ok := surface.Screen()
			return ok && screen.Hidden
		}}
		rows = func() int {
			if screen, ok := surface.Screen(); ok {
				return screen.Rows
			}
			return surface.Height()
		}
		options.Fullscreen = true
		requestRender = clock.unlessHidden(m.requestRender)
	}
	var overlay *tui.OverlayHandle
	animation := build(rows, screen, piglogin.Active(), foreground, background, func() {
		m.logoAnimationPlaying = false
		m.logoAnimation = nil
		overlay.Hide()
		// Pi's overlay hide() requests a render (tui.ts); PiG's overlay handle only invalidates.
		m.requestRender()
	})
	if clock != nil {
		// The clock reads the wall clock until the view is first hidden, so the start the factory took from time.Now holds.
		animation.now = clock.now
	}
	m.logoAnimation = animation
	overlay = ui.ShowOverlay(animation, options)
	// Pi's showOverlay requests a render (tui.ts); PiG's ShowOverlay only invalidates.
	m.requestRender()
	animation.startTimer(m.backgroundCtx, m.backgroundTasks.Go, m.postToMain, requestRender)
}

// eggSurface is the frontend renderer while it draws this run and its session shows fullscreen overlays, unless the user
// asked for reduced motion.
func (m *InteractiveMode) eggSurface() *tui.TuiSurface {
	if m.altScreen != nil || m.surface == nil || m.tuiInst != tui.TUI(m.surface) {
		return nil
	}
	if screen, ok := m.surface.Screen(); !ok || screen.ReduceMotion {
		return nil
	}
	return m.surface
}

// pig additive (D91): eggClock is the clock of an animation on a frontend's screen. It stands still while the session's
// view is hidden, so the animation pauses there and goes on where it was when the view shows again. It runs on the
// owner loop.
type eggClock struct {
	hidden func() bool
	// wall is the wall clock, time.Now when nil.
	wall func() time.Time
	// held is how long the view was hidden before, and since when it is hidden now (zero while it shows).
	held  time.Duration
	since time.Time
}

func (c *eggClock) now() time.Time {
	now := time.Now()
	if c.wall != nil {
		now = c.wall()
	}
	if c.hidden() {
		if c.since.IsZero() {
			c.since = now
		}
		return c.since.Add(-c.held)
	}
	if !c.since.IsZero() {
		c.held += now.Sub(c.since)
		c.since = time.Time{}
	}
	return now.Add(-c.held)
}

// unlessHidden is render, skipped while the view is hidden: a paused animation draws nothing.
func (c *eggClock) unlessHidden(render func()) func() {
	return func() {
		if !c.hidden() {
			render()
		}
	}
}

// disposeLogoAnimation stops a playing animation's timer when the mode ends.
func (m *InteractiveMode) disposeLogoAnimation() {
	if m.logoAnimation != nil {
		m.logoAnimation.Dispose()
		m.logoAnimation = nil
	}
	m.logoAnimationPlaying = false
}
