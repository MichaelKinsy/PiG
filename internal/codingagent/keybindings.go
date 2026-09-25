package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/tui"
)

type KeyID = string

type KeybindingDefinition struct {
	DefaultKeys []KeyID
	Description string
}

type KeybindingConflict struct {
	Key     KeyID
	Actions []string
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

// appKeybindingDefinitionsFor returns the app.* rows of upstream KEYBINDINGS
// (core/keybindings.ts) for one platform column.
func appKeybindingDefinitionsFor(platform tui.KeybindingPlatform) map[string]KeybindingDefinition {
	return map[string]KeybindingDefinition{
		"app.interrupt":                 {DefaultKeys: []KeyID{"escape"}, Description: "Cancel or abort"},
		"app.clear":                     {DefaultKeys: []KeyID{"ctrl+c"}, Description: "Clear editor"},
		"app.exit":                      {DefaultKeys: []KeyID{"ctrl+d"}, Description: "Exit when editor is empty"},
		"app.suspend":                   {DefaultKeys: tui.PlatformKeys{Other: []KeyID{"ctrl+z"}, Win32: []KeyID{}}.For(platform), Description: "Suspend to background"},
		"app.thinking.cycle":            {DefaultKeys: []KeyID{"shift+tab"}, Description: "Cycle thinking level"},
		"app.thinking.save":             {DefaultKeys: []KeyID{"ctrl+s"}, Description: "Save thinking level"},
		"app.model.cycleForward":        {DefaultKeys: []KeyID{"ctrl+p"}, Description: "Cycle to next model"},
		"app.model.cycleBackward":       {DefaultKeys: tui.PlatformKeys{Other: []KeyID{"shift+ctrl+p"}, Windows: []KeyID{"alt+p"}}.For(platform), Description: "Cycle to previous model"},
		"app.model.select":              {DefaultKeys: []KeyID{"ctrl+l"}, Description: "Open model selector"},
		"app.tools.expand":              {DefaultKeys: []KeyID{"ctrl+o"}, Description: "Toggle tool details"},
		"app.thinking.toggle":           {DefaultKeys: []KeyID{"ctrl+t"}, Description: "Toggle thinking blocks"},
		"app.session.toggleNamedFilter": {DefaultKeys: []KeyID{"ctrl+n"}, Description: "Toggle named session filter"},
		"app.editor.external":           {DefaultKeys: []KeyID{"ctrl+g"}, Description: "Open external editor"},
		"app.message.followUp":          {DefaultKeys: tui.PlatformKeys{Other: []KeyID{"alt+enter"}, Windows: []KeyID{"ctrl+q"}}.For(platform), Description: "Queue follow-up message"},
		"app.message.copy":              {DefaultKeys: []KeyID{"ctrl+x"}, Description: "Copy selection or last assistant message"},
		"app.message.dequeue":           {DefaultKeys: tui.PlatformKeys{Other: []KeyID{"alt+up"}, Windows: []KeyID{"alt+q"}}.For(platform), Description: "Restore queued messages"},
		"app.clipboard.pasteImage":      {DefaultKeys: tui.PlatformKeys{Other: []KeyID{"ctrl+v"}, Windows: []KeyID{"alt+v"}}.For(platform), Description: "Paste image from clipboard (text fallback)"},
		"app.session.new":               {DefaultKeys: nil, Description: "Start a new session"},
		"app.session.tree":              {DefaultKeys: nil, Description: "Open session tree"},
		"app.session.fork":              {DefaultKeys: nil, Description: "Fork current session"},
		"app.session.resume":            {DefaultKeys: nil, Description: "Resume a session"},
		"app.tree.foldOrUp":             {DefaultKeys: tui.PlatformKeys{Other: []KeyID{"ctrl+left", "alt+left"}, Darwin: []KeyID{"alt+left", "ctrl+left"}}.For(platform), Description: "Fold tree branch or move up"},
		"app.tree.unfoldOrDown":         {DefaultKeys: tui.PlatformKeys{Other: []KeyID{"ctrl+right", "alt+right"}, Darwin: []KeyID{"alt+right", "ctrl+right"}}.For(platform), Description: "Unfold tree branch or move down"},
		"app.tree.editLabel":            {DefaultKeys: []KeyID{"shift+l"}, Description: "Edit tree label"},
		"app.tree.toggleLabelTimestamp": {DefaultKeys: []KeyID{"shift+t"}, Description: "Toggle tree label timestamps"},
		"app.session.togglePath":        {DefaultKeys: []KeyID{"ctrl+p"}, Description: "Toggle session path display"},
		"app.session.toggleSort":        {DefaultKeys: []KeyID{"ctrl+s"}, Description: "Toggle session sort mode"},
		"app.session.rename":            {DefaultKeys: []KeyID{"ctrl+r"}, Description: "Rename session"},
		"app.session.delete":            {DefaultKeys: []KeyID{"ctrl+d"}, Description: "Delete session"},
		"app.session.deleteNoninvasive": {DefaultKeys: []KeyID{"ctrl+backspace"}, Description: "Delete session when query is empty"},
		"app.models.save":               {DefaultKeys: []KeyID{"ctrl+s"}, Description: "Save model selection"},
		"app.models.enableAll":          {DefaultKeys: []KeyID{"ctrl+a"}, Description: "Enable all models"},
		"app.models.clearAll":           {DefaultKeys: []KeyID{"ctrl+x"}, Description: "Clear all models"},
		"app.models.toggleProvider":     {DefaultKeys: []KeyID{"ctrl+p"}, Description: "Toggle all models for provider"},
		"app.models.reorderUp":          {DefaultKeys: []KeyID{"alt+up"}, Description: "Move model up in order"},
		"app.models.reorderDown":        {DefaultKeys: []KeyID{"alt+down"}, Description: "Move model down in order"},
		"app.tree.filter.default":       {DefaultKeys: []KeyID{"ctrl+d"}, Description: "Tree filter: default view"},
		"app.tree.filter.noTools":       {DefaultKeys: []KeyID{"ctrl+t"}, Description: "Tree filter: hide tool results"},
		"app.tree.filter.userOnly":      {DefaultKeys: []KeyID{"ctrl+u"}, Description: "Tree filter: user messages only"},
		"app.tree.filter.labeledOnly":   {DefaultKeys: []KeyID{"ctrl+l"}, Description: "Tree filter: labeled entries only"},
		"app.tree.filter.all":           {DefaultKeys: []KeyID{"ctrl+a"}, Description: "Tree filter: show all entries"},
		"app.tree.filter.cycleForward":  {DefaultKeys: []KeyID{"ctrl+o"}, Description: "Tree filter: cycle forward"},
		"app.tree.filter.cycleBackward": {DefaultKeys: []KeyID{"shift+ctrl+o"}, Description: "Tree filter: cycle backward"},
	}
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

var legacyKeybindingNameMigrations = map[string]string{
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

// keyIDDisplay returns human-readable display text for a key ID.
func keyIDDisplay(id KeyID) string {
	// Capitalize each segment: "ctrl+o" → "Ctrl+O", "alt+up" → "Alt+Up".
	parts := strings.Split(string(id), "+")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "+")
}

func KeybindingsFile(agentDir string) string {
	return filepath.Join(agentDir, "keybindings.json")
}

func NewKeybindingsManager(agentDir string) *KeybindingsManager {
	km := &KeybindingsManager{
		definitions:  appKeybindingDefinitions,
		ordered:      slices.Clone(appKeybindingOrder),
		userBindings: map[string][]KeyID{},
		resolved:     map[string][]KeyID{},
		configPath:   KeybindingsFile(agentDir),
		platform:     tui.HostKeybindingPlatform(),
	}
	km.rebuild()
	km.syncToTUI()
	tui.SetAppKeyTextResolver(func(action string) string {
		keys := km.Get(action)
		if len(keys) == 0 {
			return ""
		}
		parts := make([]string, len(keys))
		for i, key := range keys {
			parts[i] = string(key)
		}
		return tui.FormatKeyText(strings.Join(parts, "/"), false)
	})
	if agentDir != "" {
		_ = km.Reload()
	}
	return km
}

func DefaultKeybindingsManager() *KeybindingsManager {
	return NewKeybindingsManager("")
}

func normalizeKeys(keys []KeyID) []KeyID {
	seen := map[KeyID]struct{}{}
	out := make([]KeyID, 0, len(keys))
	for _, key := range keys {
		key = KeyID(strings.TrimSpace(strings.ToLower(string(key))))
		if key == "" {
			continue
		}
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
	userClaims := map[KeyID][]string{}
	for action, keys := range km.userBindings {
		for _, key := range normalizeKeys(keys) {
			userClaims[key] = append(userClaims[key], action)
		}
	}
	for key, actions := range userClaims {
		if len(actions) > 1 {
			slices.Sort(actions)
			km.conflicts = append(km.conflicts, KeybindingConflict{Key: key, Actions: actions})
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
	km.merged = tui.NewKeybindingsManager(keybindingDefinitionsFor(km.platform), userBindings)
}

func migrateKeybindingsConfig(raw map[string]any) (map[string]any, bool) {
	out := map[string]any{}
	migrated := false
	for key, value := range raw {
		nextKey := key
		if mk, ok := legacyKeybindingNameMigrations[key]; ok {
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

func decodeKeybindingsConfig(raw map[string]any) map[string][]KeyID {
	config := map[string][]KeyID{}
	for key, value := range raw {
		switch v := value.(type) {
		case string:
			config[key] = normalizeKeys([]KeyID{KeyID(v)})
		case []any:
			var keys []KeyID
			for _, item := range v {
				if s, ok := item.(string); ok {
					keys = append(keys, KeyID(s))
				}
			}
			config[key] = normalizeKeys(keys)
		}
	}
	return config
}

func (km *KeybindingsManager) Reload() error {
	if km.configPath == "" {
		km.userBindings = map[string][]KeyID{}
		km.rebuild()
		km.syncToTUI()
		return nil
	}
	data, err := os.ReadFile(km.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			km.userBindings = map[string][]KeyID{}
			km.rebuild()
			km.syncToTUI()
			return nil
		}
		return err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	migrated, _ := migrateKeybindingsConfig(raw)
	km.userBindings = decodeKeybindingsConfig(migrated)
	km.rebuild()
	km.syncToTUI()
	return nil
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

func (km *KeybindingsManager) Get(action string) []KeyID {
	keys := km.resolved[action]
	out := make([]KeyID, len(keys))
	copy(out, keys)
	return out
}

// ResolvedBindings returns a detached snapshot of every canonical action's
// resolved key IDs for extension-shortcut conflict checks.
func (km *KeybindingsManager) ResolvedBindings() map[string][]string {
	if km == nil {
		return nil
	}
	resolved := make(map[string][]string, len(km.resolved))
	for _, action := range km.ordered {
		resolved[action] = slices.Clone(km.resolved[action])
	}
	return resolved
}

// DisplayFor returns a human-readable key hint for the first binding of
// the given action (e.g. "Alt+Up"). Returns the raw KeyID if no
// display mapping exists.
func (km *KeybindingsManager) DisplayFor(action string) string {
	keys := km.resolved[action]
	if len(keys) == 0 {
		return action
	}
	return keyIDDisplay(keys[0])
}

// KeyText returns the un-capitalized display text for every key bound to action,
// joined by "/", mirroring upstream keyText (keybinding-hints.ts): getKeys →
// formatKeyText without capitalization. Used for the startup keybinding hints.
func (km *KeybindingsManager) KeyText(action string) string {
	return tui.FormatKeyText(strings.Join(km.resolved[action], "/"), false)
}

func (km *KeybindingsManager) Matches(input, action string) bool {
	for _, key := range km.resolved[action] {
		// Upstream KeybindingsManager.matches delegates every resolved KeyId to
		// matchesKey. Keeping one matcher here preserves Kitty-mode ambiguity,
		// alternate-layout, remapped-layout, and terminal-protocol semantics for
		// both defaults and user bindings.
		if tui.MatchesKeyID(input, string(key)) {
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
		km.userBindings[k] = normalizeKeys(v)
	}
	km.rebuild()
}

func (km *KeybindingsManager) Conflicts() []KeybindingConflict {
	out := make([]KeybindingConflict, len(km.conflicts))
	copy(out, km.conflicts)
	return out
}

func (km *KeybindingsManager) HotkeyLines() []string {
	lines := make([]string, 0, len(km.ordered)+12)
	for _, action := range km.ordered {
		def := km.definitions[action]
		keys := km.Get(action)
		if len(keys) == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("%-18s: %s", prettyKeys(keys), def.Description))
	}
	lines = append(lines,
		"Enter             : submit",
		"Shift+Enter       : insert newline",
		"Up/Down           : history navigation (empty editor)",
		"Ctrl+A            : move to start of line",
		"Ctrl+E            : move to end of line",
		"Alt+B             : word backward",
		"Alt+F             : word forward",
		"Alt+Bksp, Ctrl+W  : delete word backward",
		"Alt+D, Alt+Del    : delete word forward",
		"Ctrl+U            : kill to line start",
		"Ctrl+K            : kill to line end",
		"Ctrl+Y / Alt+Y    : yank / yank-pop",
		"Ctrl+/            : undo",
	)
	return lines
}

func prettyKeys(keys []KeyID) string {
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, prettyKey(key))
	}
	return strings.Join(parts, ", ")
}

func prettyKey(key KeyID) string {
	parts := strings.Split(string(key), "+")
	for i, part := range parts {
		switch strings.ToLower(part) {
		case "ctrl":
			parts[i] = "Ctrl"
		case "shift":
			parts[i] = "Shift"
		case "alt":
			parts[i] = "Alt"
		case "escape":
			parts[i] = "Esc"
		case "enter":
			parts[i] = "Enter"
		case "backspace":
			parts[i] = "Bksp"
		case "pageup":
			parts[i] = "PgUp"
		case "pagedown":
			parts[i] = "PgDn"
		default:
			if len(part) == 1 {
				parts[i] = strings.ToUpper(part)
			} else {
				parts[i] = strings.ToUpper(part[:1]) + part[1:]
			}
		}
	}
	return strings.Join(parts, "+")
}
