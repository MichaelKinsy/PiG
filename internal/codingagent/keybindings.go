package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	jsjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
	"github.com/MichaelKinsy/PiG/tui"
)

type KeyID = string

type KeybindingDefinition struct {
	DefaultKeys []KeyID
	Description string
}

// KeybindingConflict is upstream KeybindingConflict: a key claimed by more than one keybinding.
type KeybindingConflict struct {
	Key         KeyID
	Keybindings []string
}

type KeybindingsManager struct {
	definitions  map[string]KeybindingDefinition
	ordered      []string
	userBindings map[string][]KeyID
	resolved     map[string][]KeyID
	conflicts    []KeybindingConflict
	configPath   string
	platform     tui.KeybindingPlatform
	// merged is upstream's single KEYBINDINGS manager: the tui.* and app.*
	// table with every user override. syncToTUI installs it for tui components.
	merged *tui.TUIKeybindingsManager
}

// appKeybindingRow is one row of the app.* definition table. A row with platform set takes its keys from it; the rest take keys, and a row with
// neither has no default key (nil).
type appKeybindingRow struct {
	id          string
	description string
	keys        []KeyID
	platform    *tui.PlatformKeys
}

func (r *appKeybindingRow) keysFor(platform tui.KeybindingPlatform) []KeyID {
	if r.platform != nil {
		return r.platform.For(platform)
	}
	return r.keys
}

// appKeybindingRows are the app.* rows of upstream KEYBINDINGS (core/keybindings.ts), each with its keys by platform column. Like tui's table it is
// a package-level composite of constants, so the linker emits it as data: a map literal of slice literals is code for every row.
var appKeybindingRows = [...]appKeybindingRow{
	{id: "app.interrupt", description: "Cancel or abort", keys: []KeyID{"escape"}},
	{id: "app.clear", description: "Clear editor", keys: []KeyID{"ctrl+c"}},
	{id: "app.exit", description: "Exit when editor is empty", keys: []KeyID{"ctrl+d"}},
	{id: "app.suspend", description: "Suspend to background", platform: &tui.PlatformKeys{Other: []KeyID{"ctrl+z"}, Win32: []KeyID{}}},
	{id: "app.thinking.cycle", description: "Cycle thinking level", keys: []KeyID{"shift+tab"}},
	{id: "app.thinking.save", description: "Save thinking level", keys: []KeyID{"ctrl+s"}},
	{id: "app.model.cycleForward", description: "Cycle to next model", keys: []KeyID{"ctrl+p"}},
	{id: "app.model.cycleBackward", description: "Cycle to previous model", platform: &tui.PlatformKeys{Other: []KeyID{"shift+ctrl+p"}, Windows: []KeyID{"alt+p"}}},
	{id: "app.model.select", description: "Open model selector", keys: []KeyID{"ctrl+l"}},
	{id: "app.tools.expand", description: "Toggle tool output", keys: []KeyID{"ctrl+o"}},
	{id: "app.thinking.toggle", description: "Toggle thinking blocks", keys: []KeyID{"ctrl+t"}},
	{id: "app.session.toggleNamedFilter", description: "Toggle named session filter", keys: []KeyID{"ctrl+n"}},
	{id: "app.editor.external", description: "Open external editor", keys: []KeyID{"ctrl+g"}},
	{id: "app.message.followUp", description: "Queue follow-up message", platform: &tui.PlatformKeys{Other: []KeyID{"alt+enter"}, Windows: []KeyID{"ctrl+q"}}},
	{id: "app.message.copy", description: "Copy selection or last assistant message", keys: []KeyID{"ctrl+x"}},
	{id: "app.message.dequeue", description: "Restore queued messages", platform: &tui.PlatformKeys{Other: []KeyID{"alt+up"}, Windows: []KeyID{"alt+q"}}},
	{id: "app.clipboard.pasteImage", description: "Paste files on macOS, images, or text from clipboard", platform: &tui.PlatformKeys{Other: []KeyID{"ctrl+v"}, Windows: []KeyID{"alt+v", "ctrl+v"}}},
	{id: "app.session.new", description: "Start a new session"},
	{id: "app.session.tree", description: "Open session tree"},
	{id: "app.session.fork", description: "Fork current session"},
	{id: "app.session.resume", description: "Resume a session"},
	{id: "app.tree.foldOrUp", description: "Fold tree branch or move up", platform: &tui.PlatformKeys{Other: []KeyID{"ctrl+left", "alt+left"}, Darwin: []KeyID{"alt+left", "ctrl+left"}}},
	{id: "app.tree.unfoldOrDown", description: "Unfold tree branch or move down", platform: &tui.PlatformKeys{Other: []KeyID{"ctrl+right", "alt+right"}, Darwin: []KeyID{"alt+right", "ctrl+right"}}},
	{id: "app.tree.editLabel", description: "Edit tree label", keys: []KeyID{"shift+l"}},
	{id: "app.tree.toggleLabelTimestamp", description: "Toggle tree label timestamps", keys: []KeyID{"shift+t"}},
	{id: "app.session.togglePath", description: "Toggle session path display", keys: []KeyID{"ctrl+p"}},
	{id: "app.session.toggleSort", description: "Toggle session sort mode", keys: []KeyID{"ctrl+s"}},
	{id: "app.session.rename", description: "Rename session", keys: []KeyID{"ctrl+r"}},
	{id: "app.session.delete", description: "Delete session", keys: []KeyID{"ctrl+d"}},
	{id: "app.session.deleteNoninvasive", description: "Delete session when query is empty", keys: []KeyID{"ctrl+backspace"}},
	{id: "app.models.save", description: "Save model selection", keys: []KeyID{"ctrl+s"}},
	{id: "app.models.enableAll", description: "Enable all models", keys: []KeyID{"ctrl+a"}},
	{id: "app.models.clearAll", description: "Clear all models", keys: []KeyID{"ctrl+x"}},
	{id: "app.models.toggleProvider", description: "Toggle all models for provider", keys: []KeyID{"ctrl+p"}},
	{id: "app.models.reorderUp", description: "Move model up in order", keys: []KeyID{"alt+up"}},
	{id: "app.models.reorderDown", description: "Move model down in order", keys: []KeyID{"alt+down"}},
	{id: "app.tree.filter.default", description: "Tree filter: default view", keys: []KeyID{"ctrl+d"}},
	{id: "app.tree.filter.noTools", description: "Tree filter: hide tool results", keys: []KeyID{"ctrl+t"}},
	{id: "app.tree.filter.userOnly", description: "Tree filter: user messages only", keys: []KeyID{"ctrl+u"}},
	{id: "app.tree.filter.labeledOnly", description: "Tree filter: labeled entries only", keys: []KeyID{"ctrl+l"}},
	{id: "app.tree.filter.all", description: "Tree filter: show all entries", keys: []KeyID{"ctrl+a"}},
	{id: "app.tree.filter.cycleForward", description: "Tree filter: cycle forward", keys: []KeyID{"ctrl+o"}},
	{id: "app.tree.filter.cycleBackward", description: "Tree filter: cycle backward", keys: []KeyID{"shift+ctrl+o"}},
}

// appKeybindingDefinitionsFor returns the app.* rows of upstream KEYBINDINGS (core/keybindings.ts) for one platform column. Every call returns a
// fresh map. The default keys of all rows are copied into one array, each row owning a capacity-limited slice of it; a row with no key slice has
// nil, and one whose slice is empty has an empty one, as upstream's `[]`.
func appKeybindingDefinitionsFor(platform tui.KeybindingPlatform) map[string]KeybindingDefinition {
	total := 0
	for i := range appKeybindingRows {
		total += len(appKeybindingRows[i].keysFor(platform))
	}
	keys := make([]KeyID, 0, total)
	definitions := make(map[string]KeybindingDefinition, len(appKeybindingRows))
	for i := range appKeybindingRows {
		row := &appKeybindingRows[i]
		definition := KeybindingDefinition{Description: row.description}
		if rowKeys := row.keysFor(platform); rowKeys != nil {
			start := len(keys)
			keys = append(keys, rowKeys...)
			definition.DefaultKeys = keys[start:len(keys):len(keys)]
		}
		definitions[row.id] = definition
	}
	return definitions
}

var appKeybindingDefinitions = appKeybindingDefinitionsFor(tui.HostKeybindingPlatform())

// keybindingDefinitionsFor returns upstream KEYBINDINGS for one platform: the
// tui.* table with its per-platform defaults plus the app.* table. The TUI
// manager receives this whole table so components there resolve app actions.
func keybindingDefinitionsFor(platform tui.KeybindingPlatform) map[string]tui.TUIKeybindingDef {
	defs := tui.TUIKeybindingDefinitionsFor(platform)
	for id, def := range appKeybindingDefinitionsFor(platform) {
		defs[id] = tui.TUIKeybindingDef{DefaultKeys: def.DefaultKeys, Description: def.Description}
	}
	return defs
}

var appKeybindingOrder = []string{
	"app.interrupt", "app.clear", "app.exit", "app.suspend", "app.thinking.cycle", "app.thinking.save",
	"app.model.cycleForward", "app.model.cycleBackward", "app.model.select", "app.tools.expand",
	"app.thinking.toggle", "app.session.toggleNamedFilter", "app.editor.external", "app.message.copy",
	"app.message.followUp", "app.message.dequeue", "app.clipboard.pasteImage", "app.session.new", "app.session.tree",
	"app.session.fork", "app.session.resume", "app.tree.foldOrUp", "app.tree.unfoldOrDown",
	"app.tree.editLabel", "app.tree.toggleLabelTimestamp", "app.session.togglePath",
	"app.session.toggleSort", "app.session.rename", "app.session.delete", "app.session.deleteNoninvasive",
	"app.models.save", "app.models.enableAll", "app.models.clearAll", "app.models.toggleProvider",
	"app.models.reorderUp", "app.models.reorderDown", "app.tree.filter.default", "app.tree.filter.noTools",
	"app.tree.filter.userOnly", "app.tree.filter.labeledOnly", "app.tree.filter.all",
	"app.tree.filter.cycleForward", "app.tree.filter.cycleBackward",
}

var legacyKeybindingNameMigrations = sync.OnceValue(func() map[string]string {
	return map[string]string{
		// tui.* migrations: for users who carried over keybindings.json from
		// the legacy short-name schema (upstream renamed these to namespaced
		// IDs in commit history before v0.69.0). Mirrors upstream
		// KEYBINDING_NAME_MIGRATIONS in core/keybindings.ts.
		"cursorUp":           "tui.editor.cursorUp",
		"cursorDown":         "tui.editor.cursorDown",
		"cursorLeft":         "tui.editor.cursorLeft",
		"cursorRight":        "tui.editor.cursorRight",
		"cursorWordLeft":     "tui.editor.cursorWordLeft",
		"cursorWordRight":    "tui.editor.cursorWordRight",
		"cursorLineStart":    "tui.editor.cursorLineStart",
		"cursorLineEnd":      "tui.editor.cursorLineEnd",
		"jumpForward":        "tui.editor.jumpForward",
		"jumpBackward":       "tui.editor.jumpBackward",
		"pageUp":             "tui.editor.pageUp",
		"pageDown":           "tui.editor.pageDown",
		"deleteCharBackward": "tui.editor.deleteCharBackward",
		"deleteCharForward":  "tui.editor.deleteCharForward",
		"deleteWordBackward": "tui.editor.deleteWordBackward",
		"deleteWordForward":  "tui.editor.deleteWordForward",
		"deleteToLineStart":  "tui.editor.deleteToLineStart",
		"deleteToLineEnd":    "tui.editor.deleteToLineEnd",
		"yank":               "tui.editor.yank",
		"yankPop":            "tui.editor.yankPop",
		"undo":               "tui.editor.undo",
		"newLine":            "tui.input.newLine",
		"submit":             "tui.input.submit",
		"tab":                "tui.input.tab",
		"copy":               "tui.input.copy",
		"selectUp":           "tui.select.up",
		"selectDown":         "tui.select.down",
		"selectPageUp":       "tui.select.pageUp",
		"selectPageDown":     "tui.select.pageDown",
		"selectConfirm":      "tui.select.confirm",
		"selectCancel":       "tui.select.cancel",

		// app.* migrations.
		"interrupt":                "app.interrupt",
		"clear":                    "app.clear",
		"exit":                     "app.exit",
		"suspend":                  "app.suspend",
		"cycleThinkingLevel":       "app.thinking.cycle",
		"cycleModelForward":        "app.model.cycleForward",
		"cycleModelBackward":       "app.model.cycleBackward",
		"selectModel":              "app.model.select",
		"expandTools":              "app.tools.expand",
		"toggleThinking":           "app.thinking.toggle",
		"toggleSessionNamedFilter": "app.session.toggleNamedFilter",
		"externalEditor":           "app.editor.external",
		"followUp":                 "app.message.followUp",
		"dequeue":                  "app.message.dequeue",
		"pasteImage":               "app.clipboard.pasteImage",
		"newSession":               "app.session.new",
		"tree":                     "app.session.tree",
		"fork":                     "app.session.fork",
		"resume":                   "app.session.resume",
		"treeFoldOrUp":             "app.tree.foldOrUp",
		"treeUnfoldOrDown":         "app.tree.unfoldOrDown",
		"treeEditLabel":            "app.tree.editLabel",
		"treeToggleLabelTimestamp": "app.tree.toggleLabelTimestamp",
		"toggleSessionPath":        "app.session.togglePath",
		"toggleSessionSort":        "app.session.toggleSort",
		"renameSession":            "app.session.rename",
		"deleteSession":            "app.session.delete",
		"deleteSessionNoninvasive": "app.session.deleteNoninvasive",
	}
})

var keyIDInputs = map[KeyID][]string{
	"escape":         {"\x1b"},
	"enter":          {"\r"},
	"shift+enter":    {"\n", "\x1b\r", "\x1b\n", "\x1b[13;2u", "\x1b[13;2~", "\x1b[27;2;13~"},
	"alt+enter":      {"\x1b[13;3u", "\x1b[27;3;13~"},
	"alt+up":         {"\x1b[1;3A"},
	"alt+down":       {"\x1b[1;3B"},
	"shift+tab":      {"\x1b[Z"},
	"ctrl+c":         {"\x03", "\x1b[99;5u", "\x1b[27;5;99~"},
	"ctrl+d":         {"\x04", "\x1b[100;5u", "\x1b[27;5;100~"},
	"ctrl+g":         {"\x07", "\x1b[103;5u", "\x1b[27;5;103~"},
	"ctrl+l":         {"\x0c", "\x1b[108;5u", "\x1b[27;5;108~"},
	"ctrl+n":         {"\x0e", "\x1b[110;5u", "\x1b[27;5;110~"},
	"ctrl+o":         {"\x0f", "\x1b[111;5u", "\x1b[27;5;111~"},
	"ctrl+p":         {"\x10", "\x1b[112;5u", "\x1b[27;5;112~"},
	"ctrl+q":         {"\x11", "\x1b[113;5u", "\x1b[27;5;113~"},
	"ctrl+r":         {"\x12", "\x1b[114;5u", "\x1b[27;5;114~"},
	"ctrl+s":         {"\x13", "\x1b[115;5u", "\x1b[27;5;115~"},
	"ctrl+t":         {"\x14", "\x1b[116;5u", "\x1b[27;5;116~"},
	"ctrl+u":         {"\x15", "\x1b[117;5u", "\x1b[27;5;117~"},
	"ctrl+v":         {"\x16", "\x1b[118;5u", "\x1b[27;5;118~"},
	"ctrl+x":         {"\x18", "\x1b[120;5u", "\x1b[27;5;120~"},
	"ctrl+z":         {"\x1a", "\x1b[122;5u", "\x1b[27;5;122~"},
	"ctrl+a":         {"\x01", "\x1b[97;5u", "\x1b[27;5;97~"},
	"ctrl+left":      {"\x1b[1;5D"},
	"ctrl+right":     {"\x1b[1;5C"},
	"alt+left":       {"\x1b[1;3D"},
	"alt+right":      {"\x1b[1;3C"},
	"shift+ctrl+p":   {"\x1b[112;6u", "\x1b[27;6;112~"},
	"shift+ctrl+o":   {"\x1b[111;6u", "\x1b[27;6;111~"},
	"ctrl+backspace": {"\x17"},
}

func KeybindingsFile(agentDir string) string {
	return filepath.Join(agentDir, "keybindings.json")
}

// NewKeybindingsManager is upstream KeybindingsManager.create(agentDir): it loads agentDir's keybindings.json. An empty
// agentDir is `new KeybindingsManager()`, which has no config file, so Reload keeps the current user bindings.
func NewKeybindingsManager(agentDir string) *KeybindingsManager {
	if agentDir == "" {
		return NewKeybindingsManagerFromBindings(nil, "")
	}
	configPath := KeybindingsFile(agentDir)
	return NewKeybindingsManagerFromBindings(loadKeybindingsFile(configPath), configPath)
}

// NewKeybindingsManagerFromBindings is upstream's `new KeybindingsManager(userBindings, configPath)` (keybindings.ts:374): the
// merged KEYBINDINGS table with the given user bindings, and the file Reload reads. An empty configPath has no file.
func NewKeybindingsManagerFromBindings(userBindings map[string][]KeyID, configPath string) *KeybindingsManager {
	km := &KeybindingsManager{
		definitions:  appKeybindingDefinitions,
		ordered:      slices.Clone(appKeybindingOrder),
		userBindings: map[string][]KeyID{},
		resolved:     map[string][]KeyID{},
		configPath:   configPath,
		platform:     tui.HostKeybindingPlatform(),
	}
	for action, keys := range userBindings {
		km.userBindings[action] = slices.Clone(keys)
	}
	km.rebuild()
	km.syncToTUI()
	tui.SetAppKeyTextResolver(func(action string) string {
		keys := km.GetKeys(action)
		if len(keys) == 0 {
			return ""
		}
		parts := make([]string, len(keys))
		for i, key := range keys {
			parts[i] = string(key)
		}
		return tui.FormatKeyText(strings.Join(parts, "/"), false)
	})
	return km
}

func DefaultKeybindingsManager() *KeybindingsManager {
	return NewKeybindingsManager("")
}

// normalizeKeys is the tui keybindings.ts normalizeKeys: the keys without repeats, in order. A key is kept as written, so "CTRL+X" and
// "ctrl+x" are two keys, and an empty key stays.
func normalizeKeys(keys []KeyID) []KeyID {
	seen := map[KeyID]struct{}{}
	out := make([]KeyID, 0, len(keys))
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}

func (km *KeybindingsManager) rebuild() {
	km.resolved = make(map[string][]KeyID, len(km.definitions))
	km.conflicts = nil
	definitions := keybindingDefinitionsFor(km.platform)
	userClaims := map[KeyID][]string{}
	for action, keys := range km.userBindings {
		// upstream: packages/tui/src/keybindings.ts:KeybindingsManager.rebuild skips a user id that KEYBINDINGS does not define.
		if _, ok := definitions[action]; !ok {
			continue
		}
		for _, key := range normalizeKeys(keys) {
			userClaims[key] = append(userClaims[key], action)
		}
	}
	for key, actions := range userClaims {
		if len(actions) > 1 {
			slices.Sort(actions)
			km.conflicts = append(km.conflicts, KeybindingConflict{Key: key, Keybindings: actions})
		}
	}
	for _, action := range km.ordered {
		def := km.definitions[action]
		keys, ok := km.userBindings[action]
		if !ok {
			keys = def.DefaultKeys
		}
		km.resolved[action] = normalizeKeys(keys)
	}
	userBindings := make(map[string][]string, len(km.userBindings))
	for action, keys := range km.userBindings {
		userBindings[action] = slices.Clone(keys)
	}
	// pig additive (D92): an action whose built-in the process strips resolves to no key, as if the user unbound it.
	for action := range appActionOwners {
		if appActionStripped(action) {
			km.resolved[action] = nil
			userBindings[action] = []string{}
		}
	}
	km.merged = tui.NewKeybindingsManager(definitions, userBindings)
}

// stripOwner is the strip entry of the built-in an app action runs.
type stripOwner struct{ list, id string }

// appActionOwners are the built-ins the app actions that run one directly belong to. The session actions run their slash
// command through the registry, so a stripped command makes them do nothing; they are listed so their keys resolve to none
// too.
var appActionOwners = map[string]stripOwner{
	"app.model.select":        {pigstrip.ListCommands, "/model"},
	"app.model.cycleForward":  {pigstrip.ListCommands, "/model"},
	"app.model.cycleBackward": {pigstrip.ListCommands, "/model"},
	"app.thinking.cycle":      {pigstrip.ListCommands, "/thinking"},
	"app.message.copy":        {pigstrip.ListCommands, "/copy"},
	"app.session.new":         {pigstrip.ListCommands, "/new"},
	"app.session.tree":        {pigstrip.ListCommands, "/tree"},
	"app.session.fork":        {pigstrip.ListCommands, "/fork"},
	"app.session.resume":      {pigstrip.ListCommands, "/resume"},
}

// appActionStripped reports whether the process strips the built-in action runs.
func appActionStripped(action string) bool {
	owner, ok := appActionOwners[action]
	return ok && pigstrip.Has(owner.list, owner.id)
}

// legacyKeybindingName is upstream isLegacyKeybindingName and its KEYBINDING_NAME_MIGRATIONS[key] lookup (keybindings.ts:302,328). `key in` also finds
// the names Object.prototype inherits, and the lookup then yields the inherited value, so such a key migrates to that value's string (a function's
// source text, or "[object Object]" for __proto__) as node prints it.
func legacyKeybindingName(key string) (string, bool) {
	if next, ok := legacyKeybindingNameMigrations()[key]; ok {
		return next, true
	}
	if key == "__proto__" {
		return "[object Object]", true
	}
	if key == "constructor" {
		return "function Object() { [native code] }", true
	}
	if slices.Contains(objectPrototypeFunctionNames, key) {
		return "function " + key + "() { [native code] }", true
	}
	return "", false
}

var objectPrototypeFunctionNames = []string{"hasOwnProperty", "isPrototypeOf", "propertyIsEnumerable", "toLocaleString", "toString", "valueOf",
	"__defineGetter__", "__defineSetter__", "__lookupGetter__", "__lookupSetter__"}

func migrateKeybindingsConfig(raw map[string]any) (map[string]any, bool) {
	out := map[string]any{}
	migrated := false
	for key, value := range raw {
		nextKey := key
		if mk, ok := legacyKeybindingName(key); ok {
			nextKey = mk
			migrated = true
		}
		if key != nextKey {
			if _, exists := raw[nextKey]; exists {
				migrated = true
				continue
			}
		}
		out[nextKey] = value
	}
	return out, migrated
}

// decodeKeybindingsConfig is upstream toKeybindingsConfig (keybindings.ts:270): the $schema property is not an action, and a binding
// is kept exactly as written only when it validates against KeybindingValueSchema (key-id-schema.ts): a KeyId string or an array of
// KeyId strings. Any other value drops the binding. The manager's resolver normalizes the keys.
func decodeKeybindingsConfig(raw map[string]any) map[string][]KeyID {
	config := map[string][]KeyID{}
	for key, value := range raw {
		if key == "$schema" {
			continue
		}
		switch v := value.(type) {
		case string:
			if isKeyID(v) {
				config[key] = []KeyID{KeyID(v)}
			}
		case []any:
			keys := make([]KeyID, 0, len(v))
			valid := true
			for _, item := range v {
				s, ok := item.(string)
				if !ok || !isKeyID(s) {
					valid = false
					break
				}
				keys = append(keys, KeyID(s))
			}
			if valid {
				config[key] = keys
			}
		}
	}
	return config
}

// Reload is upstream reload(): without a config file it does nothing; otherwise the file replaces the user bindings.
func (km *KeybindingsManager) Reload() {
	if km.configPath == "" {
		return
	}
	km.userBindings = loadKeybindingsFile(km.configPath)
	km.rebuild()
	km.syncToTUI()
}

// loadKeybindingsFile is upstream KeybindingsManager.loadFromFile: a missing or unreadable file, a parse error and a value
// that is not an object (loadRawConfig answers undefined) yield no user bindings; a leading BOM is stripped.
func loadKeybindingsFile(path string) map[string][]KeyID {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string][]KeyID{}
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var raw map[string]any
	if jsjson.Unmarshal(data, &raw) != nil || raw == nil {
		// loadRawConfig accepts any object, and an array is one: Object.entries lists its indexes as keys.
		var list []any
		if jsjson.Unmarshal(data, &list) != nil || list == nil {
			return map[string][]KeyID{}
		}
		raw = make(map[string]any, len(list))
		for i, value := range list {
			raw[strconv.Itoa(i)] = value
		}
	}
	migrated, _ := migrateKeybindingsConfig(raw)
	return decodeKeybindingsConfig(migrated)
}

// syncToTUI installs upstream's merged KEYBINDINGS table and the user
// overrides as the TUI registry, as upstream interactive mode calls
// setKeybindings(keybindingsManager), so components in the tui package
// resolve tui.* and app.* actions through one manager.
func (km *KeybindingsManager) syncToTUI() {
	tui.SetKeybindings(km.merged)
}

// MatchesEditorHistory reports whether input matches an explicit
// tui.editor.historyPrevious or historyNext binding. The focused editor gives
// those precedence over app actions (custom-editor.ts handleInput), so a user
// can bind ctrl+p to history although it cycles models by default.
func (km *KeybindingsManager) MatchesEditorHistory(input string) bool {
	return km.merged.Matches(input, tui.KBEditorHistoryPrevious) || km.merged.Matches(input, tui.KBEditorHistoryNext)
}

// MatchesSelectPage reports whether input is bound to tui.select.pageUp or tui.select.pageDown.
func (km *KeybindingsManager) MatchesSelectPage(input string) bool {
	return km.merged.Matches(input, tui.KBSelectPageUp) || km.merged.Matches(input, tui.KBSelectPageDown)
}

func (km *KeybindingsManager) Save(path string) error {
	if path == "" {
		path = km.configPath
	}
	if path == "" {
		return fmt.Errorf("no keybindings path configured")
	}
	out := map[string]any{}
	for _, action := range km.ordered {
		keys, ok := km.userBindings[action]
		if !ok {
			continue
		}
		norm := normalizeKeys(keys)
		switch len(norm) {
		case 0:
			out[action] = []string{}
		case 1:
			out[action] = norm[0]
		default:
			vals := make([]string, len(norm))
			copy(vals, norm)
			out[action] = vals
		}
	}
	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// keysOf is the resolved keys of an action the merged table defines, tui.* and app.* alike as upstream's KeybindingsManager extends the tui
// manager. The TUI manager also keeps the keys of a user id no definition names (a bridge for components built without the app definitions);
// upstream's keysById holds only defined ids, so an unknown id has no keys here.
func (km *KeybindingsManager) keysOf(action string) []string {
	if _, defined := km.merged.GetDefinition(action); !defined {
		return nil
	}
	return km.merged.GetKeys(action)
}

// GetKeys is upstream getKeys: a copy of the keys resolved for a keybinding.
func (km *KeybindingsManager) GetKeys(action string) []KeyID {
	keys := km.keysOf(action)
	out := make([]KeyID, len(keys))
	for i, key := range keys {
		out[i] = KeyID(key)
	}
	return out
}

// GetResolvedBindings returns a detached snapshot of the resolved key IDs of every action of the merged table, tui.* and app.* alike (keybindings.ts getResolvedBindings; Pi lists one key as a string and several as an array, which the Go form always holds as a slice).
func (km *KeybindingsManager) GetResolvedBindings() map[string][]string {
	if km == nil {
		return nil
	}
	merged := km.merged.GetResolvedBindings()
	resolved := make(map[string][]string, len(merged))
	for action, keys := range merged {
		resolved[action] = slices.Clone(keys)
	}
	return resolved
}

// KeyText returns the un-capitalized display text for every key bound to action,
// joined by "/", mirroring upstream keyText (keybinding-hints.ts): getKeys →
// formatKeyText without capitalization. Used for the startup keybinding hints.
func (km *KeybindingsManager) KeyText(action string) string {
	keys := km.keysOf(action)
	return tui.FormatKeyText(strings.Join(keys, "/"), false)
}

func (km *KeybindingsManager) Matches(input, action string) bool {
	for _, key := range km.keysOf(action) {
		// Upstream KeybindingsManager.matches delegates every resolved KeyId to
		// matchesKey. Keeping one matcher here preserves Kitty-mode ambiguity,
		// alternate-layout, remapped-layout, and terminal-protocol semantics for
		// both defaults and user bindings.
		if tui.MatchesKeyID(input, key) {
			return true
		}
	}
	return false
}

// isGenericPrintableKeyID reports whether keyID is alt+<single printable
// char> (e.g. alt+v) or shift+<letter> (e.g. shift+l, the uppercase letter in
// legacy mode, keys.ts:1184), the forms upstream decodes generically. Named
// keys such as alt+enter or shift+tab have a multi-rune suffix and are
// excluded.
func isGenericPrintableKeyID(keyID string) bool {
	if rest, ok := strings.CutPrefix(keyID, "alt+"); ok {
		return len([]rune(rest)) == 1
	}
	rest, ok := strings.CutPrefix(keyID, "shift+")
	return ok && len(rest) == 1 && rest[0] >= 'a' && rest[0] <= 'z'
}

func (km *KeybindingsManager) Resolve(input string) string {
	for _, action := range km.ordered {
		if _, ok := km.userBindings[action]; !ok {
			continue
		}
		if km.Matches(input, action) {
			return action
		}
	}
	for _, action := range km.ordered {
		if _, ok := km.userBindings[action]; ok {
			continue
		}
		if km.Matches(input, action) {
			return action
		}
	}
	return ""
}

func (km *KeybindingsManager) SetUserBindings(bindings map[string][]KeyID) {
	km.userBindings = map[string][]KeyID{}
	for k, v := range bindings {
		km.userBindings[k] = slices.Clone(v)
	}
	km.rebuild()
}

// GetConflicts is upstream getConflicts: the keys that more than one user binding claims, each with its own copy of the keybindings.
func (km *KeybindingsManager) GetConflicts() []KeybindingConflict {
	out := make([]KeybindingConflict, len(km.conflicts))
	for i, conflict := range km.conflicts {
		out[i] = KeybindingConflict{Key: conflict.Key, Keybindings: slices.Clone(conflict.Keybindings)}
	}
	return out
}

// ExtensionKeybindingTable supplies the platform definitions and user overrides used by extension editor and UI factories. The definitions depend only on the platform, so every table of one platform shares them; the table is encoded for extensions and never modified.
func (km *KeybindingsManager) ExtensionKeybindingTable() map[string]any {
	if km == nil {
		return nil
	}
	definitions := extensionKeybindingDefinitionsFor(km.platform)
	userBindings := make(map[string][]KeyID, len(km.userBindings))
	for action, keys := range km.userBindings {
		userBindings[action] = slices.Clone(keys)
	}
	return map[string]any{"definitions": definitions, "userBindings": userBindings}
}

var extensionKeybindingDefinitions sync.Map // tui.KeybindingPlatform -> map[string]any

func extensionKeybindingDefinitionsFor(platform tui.KeybindingPlatform) map[string]any {
	if cached, ok := extensionKeybindingDefinitions.Load(platform); ok {
		return cached.(map[string]any)
	}
	definitions := map[string]any{}
	for id, def := range keybindingDefinitionsFor(platform) {
		keys := def.DefaultKeys
		if keys == nil {
			keys = []string{}
		}
		definitions[id] = map[string]any{"defaultKeys": keys, "description": def.Description}
	}
	cached, _ := extensionKeybindingDefinitions.LoadOrStore(platform, definitions)
	return cached.(map[string]any)
}

// GetDefinition returns the definition of an action in the merged tui.* and app.* table for the manager's platform, and whether the table has one (keybindings.ts getDefinition over KEYBINDINGS).
func (km *KeybindingsManager) GetDefinition(action string) (KeybindingDefinition, bool) {
	definition, ok := km.merged.GetDefinition(action)
	if !ok {
		return KeybindingDefinition{}, false
	}
	return KeybindingDefinition{DefaultKeys: slices.Clone(definition.DefaultKeys), Description: definition.Description}, true
}

// GetUserBindings returns a copy of the user's overrides (keybindings.ts getUserBindings).
func (km *KeybindingsManager) GetUserBindings() map[string][]KeyID {
	out := make(map[string][]KeyID, len(km.userBindings))
	for action, keys := range km.userBindings {
		out[action] = slices.Clone(keys)
	}
	return out
}

// KeybindingValue is one entry of a keybindings config, `KeyId | KeyId[]` (tui keybindings.ts KeybindingsConfig): Key holds a single binding, Keys
// a list. JSON reads and writes the string or the array it was given.
type KeybindingValue struct {
	Key  string
	Keys []string
}

// MarshalJSON writes the list when Keys is set and the single key otherwise.
func (v KeybindingValue) MarshalJSON() ([]byte, error) {
	if v.Keys != nil {
		return json.Marshal(v.Keys) //portlint:allow nilslicenull guarded by v.Keys != nil above; a nil list encodes the single key instead
	}
	return json.Marshal(v.Key)
}

// UnmarshalJSON reads a string as the single key and an array as the list.
func (v *KeybindingValue) UnmarshalJSON(data []byte) error {
	var list []string
	if err := json.Unmarshal(data, &list); err == nil {
		*v = KeybindingValue{Keys: list}
		return nil
	}
	var key string
	if err := json.Unmarshal(data, &key); err != nil {
		return fmt.Errorf("a keybinding is a key or a list of keys: %s", data)
	}
	*v = KeybindingValue{Key: key}
	return nil
}

// GetEffectiveConfig returns every action's resolved binding as the keybindings config holds it: the key for a single binding, the list for several (keybindings.ts getEffectiveConfig, which is getResolvedBindings of the merged table).
func (km *KeybindingsManager) GetEffectiveConfig() map[string]KeybindingValue {
	resolved := km.merged.GetResolvedBindings()
	config := make(map[string]KeybindingValue, len(resolved))
	for action, keys := range resolved {
		if len(keys) == 1 {
			config[action] = KeybindingValue{Key: keys[0]}
		} else {
			config[action] = KeybindingValue{Keys: append([]string{}, keys...)}
		}
	}
	return config
}

// effectiveBindings is getEffectiveConfig's resolved keys of every action of the merged table, tui.* and app.* alike, in the Go form
// tui.KeybindingsConfig that Runner.Shortcuts reads (interactive-mode.ts:2242 passes getEffectiveConfig() to extensionRunner.getShortcuts,
// so the reserved tui.* keys are in the shortcut conflict check).
func (km *KeybindingsManager) effectiveBindings() tui.KeybindingsConfig {
	if km == nil {
		return nil
	}
	return km.merged.GetResolvedBindings()
}
