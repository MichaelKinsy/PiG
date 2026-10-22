package tui

import "github.com/MichaelKinsy/PiG/extensions/sdk/tui/internal/engine"

// Keybinding is pi-tui's Keybinding: a keybinding id such as "tui.select.confirm" or "app.interrupt".
type Keybinding = string

// KeybindingDefinition is pi-tui's KeybindingDefinition: the default keys (as a list) and the description of one keybinding.
type KeybindingDefinition = engine.TUIKeybindingDef

// KeybindingDefinitions is pi-tui's KeybindingDefinitions.
type KeybindingDefinitions = engine.KeybindingDefinitions

// KeybindingsConfig is pi-tui's KeybindingsConfig: key overrides by keybinding id, each as a key list.
type KeybindingsConfig = engine.KeybindingsConfig

// KeybindingConflict is pi-tui's KeybindingConflict.
type KeybindingConflict = engine.TUIKeybindingConflict

// KeybindingsManager is pi-tui's KeybindingsManager: the effective keys of every keybinding, with Matches, GetKeys, GetDefinition,
// GetConflicts, SetUserBindings, GetUserBindings and GetResolvedBindings. Pi's getResolvedBindings gives a lone key as a string and
// several as an array; here every entry is a key list.
type KeybindingsManager = engine.TUIKeybindingsManager

// TUIKeybindings is pi-tui's TUI_KEYBINDINGS: the tui.* keybinding definitions.
var TUIKeybindings = engine.TUIKeybindings

// NewKeybindingsManager is `new KeybindingsManager(definitions, userBindings)`.
func NewKeybindingsManager(definitions KeybindingDefinitions, userBindings KeybindingsConfig) *KeybindingsManager {
	return engine.NewKeybindingsManager(definitions, userBindings)
}

// SetKeybindings is pi-tui's setKeybindings. The SDK installs the host's effective table (every tui.* and app.* definition with the
// user's keybindings.json overrides) as the global manager when the host publishes it, as Pi's startup does.
func SetKeybindings(keybindings *KeybindingsManager) { engine.SetKeybindings(keybindings) }

// GetKeybindings is pi-tui's getKeybindings: the global manager, over TUIKeybindings until one is installed.
func GetKeybindings() *KeybindingsManager { return engine.GetKeybindings() }
