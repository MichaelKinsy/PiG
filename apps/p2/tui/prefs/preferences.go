package prefs

// Guest-local TUI preferences (p2-tui-port.md section 4.2): validated by the launcher, posted to the worker as data and merged over the server's
// defaults there. Pure (no fs/env); shared by the launcher, the worker and the server.
//
// pi: apps/p2/src/tui/preferences.ts

import (
	"encoding/json"
	"slices"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// MaxPreferenceFile and MaxCustomThemes bound what the launcher reads.
const (
	MaxPreferenceFile = 256 * 1024
	MaxCustomThemes   = 64
)

// TuiLocalPreferences is the `preferences` launcher message body.
type TuiLocalPreferences struct {
	Keybindings pitui.KeybindingsConfig
	// Theme is the theme setting; "" when unset.
	Theme    string
	Settings TuiSettings
	// Themes are the validated custom themes by name (pi's `~/.pi/agent/themes/*.json`). Their order is the sorted names: a Go map has none.
	Themes map[string]theme.ThemeJSON
	// Problems are display-only diagnostics: invalid entries and unreadable files, without paths or contents.
	Problems []string
}

// EmptyPreferences is EMPTY_PREFERENCES.
func EmptyPreferences() TuiLocalPreferences {
	return TuiLocalPreferences{Keybindings: pitui.KeybindingsConfig{}, Themes: map[string]theme.ThemeJSON{}, Problems: []string{}}
}

// IsThemeSetting is isThemeSetting: a name of 1-64 characters without control characters or "/", or two such names joined by one "/".
func IsThemeSetting(value string) bool {
	part := func(text string) bool {
		n := 0
		for _, r := range text {
			if r < 0x20 || r == '/' {
				return false
			}
			n++
			if n > 64 {
				return false
			}
		}
		return n >= 1
	}
	for i := range len(value) {
		if value[i] == '/' {
			return part(value[:i]) && part(value[i+1:])
		}
	}
	return part(value)
}

// SanitizeServerPreferences is sanitizeServerPreferences: the defaults as served by `TuiPreferences`: no privileged bindings, only whitelisted settings.
// raw is a decoded JSON value.
func SanitizeServerPreferences(raw any) TuiServerPreferences {
	value, _ := raw.(map[string]any)
	config, _ := SanitizeKeybindings(value["keybindings"])
	keybindings := map[string][]string{}
	for id, keys := range config {
		if keys != nil && !IsPrivileged(id) {
			keybindings[id] = keys
		}
	}
	out := TuiServerPreferences{Keybindings: keybindings}
	if name, ok := value["theme"].(string); ok && IsThemeSetting(name) {
		out.Theme = name
	}
	out.Settings, _ = SanitizeSettings(value["settings"])
	return out
}

// Effective is effectivePreferences' result.
type Effective struct {
	Keybindings pitui.KeybindingsConfig
	// Theme is "" when no source sets one.
	Theme    string
	Settings TuiSettings
}

// EffectivePreferences is effectivePreferences. Effective preferences in the worker: built-in < server defaults < pi files < p2 prefs (the launcher
// already merged the last two). Server bindings for privileged ids are ignored even if a server sends them.
func EffectivePreferences(server *TuiServerPreferences, local TuiLocalPreferences) Effective {
	serverBindings := pitui.KeybindingsConfig{}
	var serverSettings TuiSettings
	serverTheme := ""
	if server != nil {
		for id, keys := range server.Keybindings {
			// sanitizeKeybindings over the served bindings: ids and key ids are re-checked.
			raw := make([]any, len(keys))
			for i, key := range keys {
				raw[i] = key
			}
			if config, _ := SanitizeKeybindings(map[string]any{id: raw}); !IsPrivileged(id) {
				for sanitized, value := range config {
					serverBindings[sanitized] = value
				}
			}
		}
		serverSettings = server.Settings
		if IsThemeSetting(server.Theme) {
			serverTheme = server.Theme
		}
	}
	keybindings := pitui.KeybindingsConfig{}
	for id, keys := range serverBindings {
		keybindings[id] = keys
	}
	for id, keys := range local.Keybindings {
		keybindings[id] = keys
	}
	themeSetting := local.Theme
	if themeSetting == "" {
		themeSetting = serverTheme
	}
	return Effective{Keybindings: keybindings, Theme: themeSetting, Settings: MergeSettings(serverSettings, local.Settings)}
}

// ParseLocalPreferences is parseLocalPreferences, the worker side of the `preferences` message: it re-checks the shape (cheap; the launcher already
// validated). raw is a decoded JSON value.
func ParseLocalPreferences(raw any) TuiLocalPreferences {
	value, ok := raw.(map[string]any)
	if !ok {
		return EmptyPreferences()
	}
	themes := map[string]theme.ThemeJSON{}
	if object, isObject := value["themes"].(map[string]any); isObject {
		names := make([]string, 0, len(object))
		for name := range object {
			names = append(names, name)
		}
		slices.Sort(names)
		if len(names) > MaxCustomThemes {
			names = names[:MaxCustomThemes]
		}
		for _, name := range names {
			document, isDocument := object[name].(map[string]any)
			if !isDocument || !IsThemeSetting(name) || containsSlash(name) {
				continue
			}
			if _, hasColors := document["colors"].(map[string]any); !hasColors {
				continue
			}
			encoded, err := json.Marshal(document)
			if err != nil {
				continue
			}
			var parsed theme.ThemeJSON
			if json.Unmarshal(encoded, &parsed) == nil {
				themes[name] = parsed
			}
		}
	}
	keybindings, _ := SanitizeKeybindings(value["keybindings"])
	settings, _ := SanitizeSettings(value["settings"])
	out := TuiLocalPreferences{Keybindings: keybindings, Settings: settings, Themes: themes, Problems: []string{}}
	if name, ok := value["theme"].(string); ok && IsThemeSetting(name) {
		out.Theme = name
	}
	if problems, ok := value["problems"].([]any); ok {
		for _, item := range problems {
			if text, isString := item.(string); isString && len(out.Problems) < 50 {
				out.Problems = append(out.Problems, text)
			}
		}
	}
	return out
}

func containsSlash(text string) bool {
	for i := range len(text) {
		if text[i] == '/' {
			return true
		}
	}
	return false
}
