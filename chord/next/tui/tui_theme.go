package tui

import (
	"sync"

	"github.com/MichaelKinsy/PiG/chord/next/surfaces"
)

// Ports packages/chord/src/tui/theme.ts.

// TerminalTheme is colours by role for the terminal: from the active theme, as the terminal shows them (true colour,
// else the nearest of 256; a theme's `tui` overrides first). A plugin asks for a role (`theme.Fg("accent", "Notes")`),
// never writes raw colours; a new theme applies at the next redraw.
type TerminalTheme struct {
	depth surfaces.ColorDepth

	mu        sync.Mutex
	theme     surfaces.Theme
	listeners []*func()
}

// NewTerminalTheme is the lookup of theme for a terminal of depth ("truecolor" or "256").
func NewTerminalTheme(theme surfaces.Theme, depth surfaces.ColorDepth) *TerminalTheme {
	return &TerminalTheme{theme: theme, depth: depth}
}

// Theme is the active theme.
func (t *TerminalTheme) Theme() surfaces.Theme {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.theme
}

// Fg is text in role's colour (the default colour after it).
func (t *TerminalTheme) Fg(role, text string) string {
	return "\x1b[" + surfaces.TerminalColor(t.Theme(), role, t.depth, "fg") + "m" + text + "\x1b[39m"
}

// Bg is text on role's colour as its background.
func (t *TerminalTheme) Bg(role, text string) string {
	return "\x1b[" + surfaces.TerminalColor(t.Theme(), role, t.depth, "bg") + "m" + text + "\x1b[49m"
}

// OnChange calls listener when the active theme changes (to redraw) until the returned removal.
func (t *TerminalTheme) OnChange(listener func()) func() {
	entry := &listener
	t.mu.Lock()
	t.listeners = append(t.listeners, entry)
	t.mu.Unlock()
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		for i, l := range t.listeners {
			if l == entry {
				t.listeners = append(t.listeners[:i:i], t.listeners[i+1:]...)
				return
			}
		}
	}
}

// Set makes theme the active theme.
func (t *TerminalTheme) Set(theme surfaces.Theme) {
	t.mu.Lock()
	t.theme = theme
	listeners := append([]*func(){}, t.listeners...)
	t.mu.Unlock()
	for _, listener := range listeners {
		(*listener)()
	}
}
