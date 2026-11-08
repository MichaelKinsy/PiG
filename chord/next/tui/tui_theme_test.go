package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/MichaelKinsy/PiG/chord/next/surfaces"
)

// Ports packages/chord/src/tui/theme.ts (Pi has no test file for it).
func TestTerminalThemeGivesARolesColourAndTheDefaultAfterItAndTellsListenersOfANewThemeUntilRemoved(t *testing.T) {
	dark := surfaces.Theme{ID: "d", Name: "d", Mode: "dark", Colors: map[string]string{"accent": "#ff0000"}}
	light := surfaces.Theme{ID: "l", Name: "l", Mode: "light", Colors: map[string]string{"accent": "#00ff00"}, Tui: map[string]string{"accent": "42"}}
	theme := NewTerminalTheme(dark, "truecolor")
	var _ *TerminalTheme = theme
	assert.Equal(t, "d", theme.Theme().ID)
	assert.Equal(t, "\x1b[38;2;255;0;0mNotes\x1b[39m", theme.Fg("accent", "Notes"))
	assert.Equal(t, "\x1b[48;2;255;0;0mNotes\x1b[49m", theme.Bg("accent", "Notes"))
	calls := 0
	remove := theme.OnChange(func() { calls++ })
	theme.Set(light)
	assert.Equal(t, 1, calls)
	assert.Equal(t, "l", theme.Theme().ID)
	assert.Equal(t, "\x1b[38;5;42mx\x1b[39m", theme.Fg("accent", "x"))
	remove()
	theme.Set(dark)
	assert.Equal(t, 1, calls)
	assert.Equal(t, "\x1b[38;5;196mx\x1b[39m", NewTerminalTheme(dark, "256").Fg("accent", "x"))
}
