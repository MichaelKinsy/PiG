package sdk

import (
	"encoding/json"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

// hostKeybindingTable is the last keybinding table the host published, kept so an unchanged table does not rebuild the global manager
// (the Node runtime compares the encoded table the same way).
var hostKeybindingTable struct {
	sync.Mutex
	encoded string
}

// applyHostTuiState installs the host's replicated pi-tui state: the effective keybinding table (every tui.* and app.* definition
// with the user's keybindings.json overrides) as pi-tui's global keybindings, as Pi installs its manager with setKeybindings at
// startup, and the resolved terminal capabilities as pi-tui's capability cache, so tui.GetCapabilities answers what the host's
// terminal supports. Both mirror the Node runtime's applyKeybindings and applyTerminalCapabilities.
func applyHostTuiState(keybindings, capabilities json.RawMessage) {
	if len(keybindings) > 0 {
		var table struct {
			Definitions  map[string]json.RawMessage `json:"definitions"`
			UserBindings map[string]json.RawMessage `json:"userBindings"`
		}
		if json.Unmarshal(keybindings, &table) == nil && table.Definitions != nil {
			hostKeybindingTable.Lock()
			changed := hostKeybindingTable.encoded != string(keybindings)
			hostKeybindingTable.encoded = string(keybindings)
			hostKeybindingTable.Unlock()
			if changed {
				definitions := tui.KeybindingDefinitions{}
				for id, raw := range table.Definitions {
					var def struct {
						DefaultKeys json.RawMessage `json:"defaultKeys"`
						Description string          `json:"description"`
					}
					_ = json.Unmarshal(raw, &def)
					definitions[id] = tui.KeybindingDefinition{DefaultKeys: wireKeys(def.DefaultKeys), Description: def.Description}
				}
				user := tui.KeybindingsConfig{}
				for id, raw := range table.UserBindings {
					user[id] = wireKeys(raw)
				}
				tui.SetKeybindings(tui.NewKeybindingsManager(definitions, user))
			}
		}
	}
	if len(capabilities) > 0 {
		var caps struct {
			Images     string `json:"images"`
			TrueColor  bool   `json:"trueColor"`
			Hyperlinks bool   `json:"hyperlinks"`
		}
		var probe map[string]json.RawMessage
		if json.Unmarshal(capabilities, &probe) == nil && probe != nil && json.Unmarshal(capabilities, &caps) == nil {
			tui.SetCapabilities(tui.TerminalCapabilities{Images: tui.ImageProtocol(caps.Images), TrueColor: caps.TrueColor, Hyperlinks: caps.Hyperlinks})
		}
	}
}

// wireKeys is a key list given as one string or an array.
func wireKeys(raw json.RawMessage) []string {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		return many
	}
	return []string{}
}

// hostTheme is the host's active theme as Pi's own process-global theme is: the pi-coding-agent helpers (keyHint, getMarkdownTheme, ...)
// color with it. The host has one theme, so extensions sharing a process share it.
var hostTheme atomic.Pointer[globalTheme]

// installedTheme is the theme initTheme installed, which stands for the global theme until the host's theme changes, as Pi's own theme change replaces it.
var installedTheme atomic.Pointer[globalTheme]

// globalTheme is one global theme with the palette it was decoded from, which a host helper that draws with the theme (highlightCode) is given when the theme is
// not the host's active one.
type globalTheme struct {
	theme UITheme
	raw   json.RawMessage
}

// setHostTheme records the palette the host published. A palette that differs from the last one the host published ends what initTheme installed; the same
// palette, which every state snapshot repeats, leaves it.
func setHostTheme(theme UITheme, raw json.RawMessage) {
	previous := hostTheme.Swap(&globalTheme{theme: theme, raw: raw})
	if previous != nil && string(previous.raw) != string(raw) {
		installedTheme.Store(nil)
	}
}

// currentHostTheme is the global theme: the one initTheme installed, else the theme the host last published, or the zero theme (modifiers only) before one arrives.
func currentHostTheme() UITheme {
	if theme := installedTheme.Load(); theme != nil {
		return theme.theme
	}
	if theme := hostTheme.Load(); theme != nil {
		return theme.theme
	}
	return UITheme{}
}

// installedThemePalette is the palette of the theme initTheme installed, nil while the global theme is the host's.
func installedThemePalette() json.RawMessage {
	if theme := installedTheme.Load(); theme != nil {
		return theme.raw
	}
	return nil
}
