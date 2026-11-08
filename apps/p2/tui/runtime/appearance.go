// Package runtime is the TUI half of the p2 runtime (apps/p2/src/runtime/tui*.ts): per-runtime preference state, rendering contribution
// registries, and the TuiRuntime that hosts screens. It sits above components and tools and below the top-level tui package.
package runtime

// Per-runtime preference state (p2-tui-port.md sections 4.2, 5.3): server defaults, the launcher's guest-local preferences, terminal colors and
// capabilities in; effective keybindings, settings and a ThemeRef out.
//
// pi: apps/p2/src/runtime/tui-appearance.ts

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// TuiCapabilities are the guest's terminal capabilities: true color and the key platform.
type TuiCapabilities struct {
	TrueColor bool
	prefs.KeyPlatform
}

// TuiAppearance is the per-runtime preference state.
type TuiAppearance struct {
	Theme        *theme.ThemeRef
	Settings     prefs.TuiSettings
	Keybindings  pitui.KeybindingsConfig
	ThemeSetting string
	// ThemeError is why the theme setting did not resolve; "" when it did.
	ThemeError   string
	Local        prefs.TuiLocalPreferences
	Server       *prefs.TuiServerPreferences
	Terminal     theme.TerminalState
	Capabilities TuiCapabilities
	themeKey     string
	onChange     func(*TuiAppearance)
}

// NewTuiAppearance is the constructor; initial is the theme to start with, nil to resolve one from the (empty) preferences.
func NewTuiAppearance(onChange func(*TuiAppearance), initial *theme.Theme) *TuiAppearance {
	a := &TuiAppearance{
		Keybindings: pitui.KeybindingsConfig{}, Local: prefs.EmptyPreferences(),
		Capabilities: TuiCapabilities{KeyPlatform: prefs.DefaultKeyPlatform}, onChange: onChange,
	}
	if initial == nil {
		resolved, _ := a.resolve()
		initial = resolved
	}
	a.Theme = theme.NewThemeRef(initial)
	if a.themeKey == "" {
		a.themeKey = a.key()
	}
	return a
}

// ColorMode is "truecolor" when the settings or the terminal allow it, else "256color".
func (a *TuiAppearance) ColorMode() pitui.TerminalColorMode {
	trueColor := a.Capabilities.TrueColor
	if a.Settings.Terminal != nil && a.Settings.Terminal.TrueColor != nil {
		trueColor = *a.Settings.Terminal.TrueColor
	}
	if trueColor {
		return "truecolor"
	}
	return "256color"
}

// KeyPlatform is the platform the key hints are for.
func (a *TuiAppearance) KeyPlatform() prefs.KeyPlatform { return a.Capabilities.KeyPlatform }

// AppearanceUpdate is the patch of Update: a nil member is unchanged.
type AppearanceUpdate struct {
	Local        *prefs.TuiLocalPreferences
	Server       **prefs.TuiServerPreferences
	Terminal     *theme.TerminalState
	Capabilities *TuiCapabilities
}

// Update applies a patch, re-resolves the effective preferences and the theme when its inputs changed, and tells the owner.
func (a *TuiAppearance) Update(patch AppearanceUpdate) {
	if patch.Local != nil {
		a.Local = *patch.Local
	}
	if patch.Server != nil {
		a.Server = *patch.Server
	}
	if patch.Terminal != nil {
		a.Terminal = *patch.Terminal
	}
	if patch.Capabilities != nil {
		a.Capabilities = *patch.Capabilities
	}
	effective := prefs.EffectivePreferences(a.Server, a.Local)
	a.Settings = effective.Settings
	a.Keybindings = effective.Keybindings
	a.ThemeSetting = effective.Theme
	if key := a.key(); key != a.themeKey {
		a.themeKey = key
		resolved, err := a.resolve()
		a.ThemeError = ""
		if err != nil {
			a.ThemeError = err.Error()
		}
		a.Theme.Set(resolved)
	}
	a.onChange(a)
}

func (a *TuiAppearance) resolve() (*theme.Theme, error) {
	return theme.ResolveTheme(theme.ResolveOptions{Setting: a.ThemeSetting, Themes: a.Local.Themes, Mode: a.ColorMode(), Terminal: a.Terminal})
}

// key identifies the theme's inputs: a change of it re-resolves the theme.
func (a *TuiAppearance) key() string {
	encoded, _ := json.Marshal([]any{a.ThemeSetting, a.Local.Themes, a.Terminal, a.ColorMode()})
	return string(encoded)
}
