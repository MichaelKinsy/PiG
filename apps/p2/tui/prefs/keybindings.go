// Package prefs holds p2's TUI preferences: pi's keybinding ids and defaults (src/tui/keybindings.ts), the guest-local preferences and their merge
// (src/tui/preferences.ts) and the server-side TUI defaults and settings schema (src/runtime/tui-preferences.ts). They are one package because
// they refer to each other, and the runtime and the launcher both import them.
//
// pi: apps/p2/src/tui/keybindings.ts
package prefs

// pi's keybinding ids and defaults, copied from coding-agent core/keybindings.ts (APP_KEYBINDINGS, the KEYBINDINGS platform overrides and the
// legacy-name migration) and core/key-id-schema.ts. Pure data: the platform is the guest's (from the launcher's `capabilities`), never this
// process's. Shared by the launcher and the worker.

import (
	"reflect"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// KeyPlatform is the guest's platform and whether it uses the Windows keys.
type KeyPlatform struct {
	Platform    string
	WindowsKeys bool
}

// DefaultKeyPlatform is DEFAULT_KEY_PLATFORM.
var DefaultKeyPlatform = KeyPlatform{Platform: "linux"}

type appBinding struct {
	id          string
	defaultKeys []string
	description string
}

func keys(k ...string) []string {
	if k == nil {
		return []string{}
	}
	return k
}

// appKeybindingRows is appKeybindings' table in declaration order, for one guest platform (coding-agent `APP_KEYBINDINGS`).
func appKeybindingRows(p KeyPlatform) []appBinding {
	win, platform := p.WindowsKeys, p.Platform
	pick := func(windows, other []string) []string {
		if win {
			return windows
		}
		return other
	}
	darwin := func(a, b []string) []string {
		if platform == "darwin" {
			return a
		}
		return b
	}
	suspend := keys("ctrl+z")
	if platform == "win32" {
		suspend = keys()
	}
	return []appBinding{
		{"app.interrupt", keys("escape"), "Cancel or abort"},
		{"app.clear", keys("ctrl+c"), "Clear editor"},
		{"app.exit", keys("ctrl+d"), "Exit when editor is empty"},
		{"app.suspend", suspend, "Suspend to background"},
		{"app.thinking.cycle", keys("shift+tab"), "Cycle thinking level"},
		{"app.thinking.save", keys("ctrl+s"), "Save thinking level"},
		{"app.model.cycleForward", keys("ctrl+p"), "Cycle to next model"},
		{"app.model.cycleBackward", pick(keys("alt+p"), keys("shift+ctrl+p")), "Cycle to previous model"},
		{"app.model.select", keys("ctrl+l"), "Open model selector"},
		{"app.tools.expand", keys("ctrl+o"), "Toggle tool output"},
		{"app.thinking.toggle", keys("ctrl+t"), "Toggle thinking blocks"},
		{"app.session.toggleNamedFilter", keys("ctrl+n"), "Toggle named session filter"},
		{"app.editor.external", keys("ctrl+g"), "Open external editor"},
		{"app.message.copy", keys("ctrl+x"), "Copy selection or last assistant message"},
		{"app.message.followUp", pick(keys("ctrl+q"), keys("alt+enter")), "Queue follow-up message"},
		{"app.message.dequeue", pick(keys("alt+q"), keys("alt+up")), "Restore queued messages"},
		{"app.clipboard.pasteImage", pick(keys("alt+v"), keys("ctrl+v")), "Paste files on macOS, images, or text from clipboard"},
		{"app.session.new", keys(), "Start a new session"},
		{"app.session.tree", keys(), "Open session tree"},
		{"app.session.fork", keys(), "Fork current session"},
		{"app.session.resume", keys(), "Resume a session"},
		{"app.tree.foldOrUp", darwin(keys("alt+left", "ctrl+left"), keys("ctrl+left", "alt+left")), "Fold tree branch or move up"},
		{"app.tree.unfoldOrDown", darwin(keys("alt+right", "ctrl+right"), keys("ctrl+right", "alt+right")), "Unfold tree branch or move down"},
		{"app.tree.editLabel", keys("shift+l"), "Edit tree label"},
		{"app.tree.toggleLabelTimestamp", keys("shift+t"), "Toggle tree label timestamps"},
		{"app.session.togglePath", keys("ctrl+p"), "Toggle session path display"},
		{"app.session.toggleSort", keys("ctrl+s"), "Toggle session sort mode"},
		{"app.session.rename", keys("ctrl+r"), "Rename session"},
		{"app.session.delete", keys("ctrl+d"), "Delete session"},
		{"app.session.deleteNoninvasive", keys("ctrl+backspace"), "Delete session when query is empty"},
		{"app.models.save", keys("ctrl+s"), "Save model selection"},
		{"app.models.enableAll", keys("ctrl+a"), "Enable all models"},
		{"app.models.clearAll", keys("ctrl+x"), "Clear all models"},
		{"app.models.toggleProvider", keys("ctrl+p"), "Toggle all models for provider"},
		{"app.models.reorderUp", keys("alt+up"), "Move model up in order"},
		{"app.models.reorderDown", keys("alt+down"), "Move model down in order"},
		{"app.tree.filter.default", keys("ctrl+d"), "Tree filter: default view"},
		{"app.tree.filter.noTools", keys("ctrl+t"), "Tree filter: hide tool results"},
		{"app.tree.filter.userOnly", keys("ctrl+u"), "Tree filter: user messages only"},
		{"app.tree.filter.labeledOnly", keys("ctrl+l"), "Tree filter: labeled entries only"},
		{"app.tree.filter.all", keys("ctrl+a"), "Tree filter: show all entries"},
		{"app.tree.filter.cycleForward", keys("ctrl+o"), "Tree filter: cycle forward"},
		{"app.tree.filter.cycleBackward", keys("shift+ctrl+o"), "Tree filter: cycle backward"},
		// p2-only launcher action (section 3.2): opens the launcher's local file picker. Unbound by default.
		{"p2.attach", keys(), "Attach a local file (launcher)"},
	}
}

// AppKeybindingIDs lists appKeybindings' ids in declaration order.
func AppKeybindingIDs() []string {
	rows := appKeybindingRows(DefaultKeyPlatform)
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.id
	}
	return ids
}

// AppKeybindings is appKeybindings: pi's `app.*` table for one guest platform (coding-agent `APP_KEYBINDINGS`).
func AppKeybindings(p KeyPlatform) pitui.KeybindingDefinitions {
	rows := appKeybindingRows(p)
	definitions := make(pitui.KeybindingDefinitions, len(rows))
	for _, row := range rows {
		definitions[row.id] = pitui.TUIKeybindingDef{DefaultKeys: row.defaultKeys, Description: row.description}
	}
	return definitions
}

// BuiltinKeybindings is builtinKeybindings: pi's full built-in table (`KEYBINDINGS`): pi-tui ids with coding-agent's platform overrides, then `app.*`.
func BuiltinKeybindings(p KeyPlatform) pitui.KeybindingDefinitions {
	definitions := make(pitui.KeybindingDefinitions, len(pitui.TUIKeybindings)+48)
	for id, definition := range pitui.TUIKeybindings {
		definitions[id] = definition
	}
	override := func(id string, defaultKeys []string) {
		definition := definitions[id]
		definition.DefaultKeys = defaultKeys
		definitions[id] = definition
	}
	switch {
	case p.Platform == "win32":
		override("tui.editor.undo", keys("ctrl+z"))
	case p.WindowsKeys:
		override("tui.editor.undo", keys("alt+z"))
	default:
		override("tui.editor.undo", keys("ctrl+-"))
	}
	if p.WindowsKeys {
		override("tui.altScreen.previousPrompt", keys("ctrl+up"))
		override("tui.altScreen.nextPrompt", keys("ctrl+down"))
		override("tui.altScreen.search", keys("ctrl+f"))
	} else {
		override("tui.altScreen.previousPrompt", keys("ctrl+shift+up", "ctrl+up"))
		override("tui.altScreen.nextPrompt", keys("ctrl+shift+down", "ctrl+down"))
		override("tui.altScreen.search", keys("ctrl+shift+f"))
	}
	for id, definition := range AppKeybindings(p) {
		definitions[id] = definition
	}
	return definitions
}

// PrivilegedActions are the launcher-owned action ids (section 3.1 K grants). The worker can never bind or rebind them.
var PrivilegedActions = []string{
	"app.clipboard.pasteImage",
	"app.editor.external",
	"app.suspend",
	"app.message.copy",
	"p2.attach",
}

// IsPrivileged reports whether id is a launcher-owned action.
func IsPrivileged(id string) bool { return slices.Contains(PrivilegedActions, id) }

// keybindingNameMigrations are the legacy keybinding names pi migrated (coding-agent core/keybindings.ts KEYBINDING_NAME_MIGRATIONS).
var keybindingNameMigrations = map[string]string{
	"cursorUp": "tui.editor.cursorUp", "cursorDown": "tui.editor.cursorDown", "cursorLeft": "tui.editor.cursorLeft",
	"cursorRight": "tui.editor.cursorRight", "cursorWordLeft": "tui.editor.cursorWordLeft", "cursorWordRight": "tui.editor.cursorWordRight",
	"cursorLineStart": "tui.editor.cursorLineStart", "cursorLineEnd": "tui.editor.cursorLineEnd", "jumpForward": "tui.editor.jumpForward",
	"jumpBackward": "tui.editor.jumpBackward", "pageUp": "tui.editor.pageUp", "pageDown": "tui.editor.pageDown",
	"deleteCharBackward": "tui.editor.deleteCharBackward", "deleteCharForward": "tui.editor.deleteCharForward",
	"deleteWordBackward": "tui.editor.deleteWordBackward", "deleteWordForward": "tui.editor.deleteWordForward",
	"deleteToLineStart": "tui.editor.deleteToLineStart", "deleteToLineEnd": "tui.editor.deleteToLineEnd", "yank": "tui.editor.yank",
	"yankPop": "tui.editor.yankPop", "undo": "tui.editor.undo", "newLine": "tui.input.newLine", "submit": "tui.input.submit",
	"tab": "tui.input.tab", "copy": "tui.input.copy", "selectUp": "tui.select.up", "selectDown": "tui.select.down",
	"selectPageUp": "tui.select.pageUp", "selectPageDown": "tui.select.pageDown", "selectConfirm": "tui.select.confirm",
	"selectCancel": "tui.select.cancel", "interrupt": "app.interrupt", "clear": "app.clear", "exit": "app.exit", "suspend": "app.suspend",
	"cycleThinkingLevel": "app.thinking.cycle", "cycleModelForward": "app.model.cycleForward", "cycleModelBackward": "app.model.cycleBackward",
	"selectModel": "app.model.select", "expandTools": "app.tools.expand", "toggleThinking": "app.thinking.toggle",
	"toggleSessionNamedFilter": "app.session.toggleNamedFilter", "externalEditor": "app.editor.external", "followUp": "app.message.followUp",
	"dequeue": "app.message.dequeue", "pasteImage": "app.clipboard.pasteImage", "newSession": "app.session.new", "tree": "app.session.tree",
	"fork": "app.session.fork", "resume": "app.session.resume", "treeFoldOrUp": "app.tree.foldOrUp", "treeUnfoldOrDown": "app.tree.unfoldOrDown",
	"treeEditLabel": "app.tree.editLabel", "treeToggleLabelTimestamp": "app.tree.toggleLabelTimestamp", "toggleSessionPath": "app.session.togglePath",
	"toggleSessionSort": "app.session.toggleSort", "renameSession": "app.session.rename", "deleteSession": "app.session.delete",
	"deleteSessionNoninvasive": "app.session.deleteNoninvasive",
}

var keyModifiers = []string{"ctrl", "shift", "alt", "super"}

// namedKeys are the string values of pi-tui's Key object (the base keys other than [a-z0-9]).
var namedKeys = func() map[string]bool {
	set := map[string]bool{}
	value := reflect.ValueOf(pitui.Key)
	for i := range value.NumField() {
		set[value.Field(i).String()] = true
	}
	return set
}()

var keybindingID = lazyregexp.New(`^[A-Za-z][\w-]*(?:\.[A-Za-z][\w-]*)+$`)


// IsKeyID is isKeyId: at most 64 characters, up to four distinct modifiers, then one base key (a-z, 0-9 or a pi-tui Key name).
func IsKeyID(value string) bool {
	if len(value) > 64 {
		return false
	}
	// KEY_ID's lookahead: no modifier appears twice.
	for _, modifier := range keyModifiers {
		marker := modifier + "+"
		if i := strings.Index(value, marker); i >= 0 && strings.Contains(value[i+len(marker):], marker) {
			return false
		}
	}
	rest := value
	for range 4 {
		stripped := false
		for _, modifier := range keyModifiers {
			if after, ok := strings.CutPrefix(rest, modifier+"+"); ok {
				rest, stripped = after, true
				break
			}
		}
		if !stripped {
			break
		}
	}
	if len(rest) == 1 && (rest[0] >= 'a' && rest[0] <= 'z' || rest[0] >= '0' && rest[0] <= '9') {
		return true
	}
	return namedKeys[rest]
}

const maxKeys = 8

// SanitizeKeybindings is sanitizeKeybindings: it validates one keybindings document (pi `keybindings.json` or the `keybindings` object of p2 prefs):
// migrates legacy names, keeps only well-formed ids with valid key ids (pi's KeyIdSchema), and reports what was dropped. raw is a decoded JSON value;
// a key's value is a string or an array of strings. A document that is not an object has no entries.
func SanitizeKeybindings(raw any) (config pitui.KeybindingsConfig, invalid []string) {
	config = pitui.KeybindingsConfig{}
	object, ok := raw.(map[string]any)
	if !ok {
		return config, invalid
	}
	// Document order is the order JS enumerates; a decoded Go map has none, so ids are visited sorted (invalid reports follow it).
	names := make([]string, 0, len(object))
	for name := range object {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, key := range names {
		if key == "$schema" {
			continue
		}
		id := key
		if migrated, ok := keybindingNameMigrations[key]; ok {
			id = migrated
		}
		// pi: an explicit new-style name wins over its legacy alias.
		if _, has := object[id]; id != key && has {
			continue
		}
		var values []any
		if list, isList := object[key].([]any); isList {
			values = list
		} else {
			values = []any{object[key]}
		}
		valid := keybindingID.MatchString(id) && len(id) <= 128 && len(values) <= maxKeys
		var list []string
		for _, value := range values {
			text, isString := value.(string)
			if !isString || !IsKeyID(text) {
				valid = false
				break
			}
			list = append(list, text)
		}
		if !valid {
			invalid = append(invalid, truncate(key, 128))
			continue
		}
		config[id] = list
	}
	return config, invalid
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit]
}

// PrivilegedResolution is what resolvePrivilegedKeys returns.
type PrivilegedResolution struct {
	// Keys are the effective launcher keys per privileged id.
	Keys map[string][]string
	// Lost are the keys a privileged action lost to a non-privileged binding (shown by /hotkeys).
	Lost []LostKey
}

// LostKey is one key a privileged action lost.
type LostKey struct{ ID, Key, To string }

// ResolvePrivilegedKeys is resolvePrivilegedKeys. Section 6.2 "privileged vs anything": a privileged key colliding with any non-privileged binding
// keeps the key only when pi has the same default collision (pi resolves it by mode, e.g. Ctrl+G = external editor / search next, where the worker
// suspends interception while searching). Otherwise the privileged action loses the key.
func ResolvePrivilegedKeys(definitions pitui.KeybindingDefinitions, user pitui.KeybindingsConfig) PrivilegedResolution {
	effective := func(id string) []string {
		if keys, ok := user[id]; ok {
			return keys
		}
		return definitions[id].DefaultKeys
	}
	others := map[string][]string{}
	claim := func(id string, keys []string) {
		for _, key := range keys {
			others[key] = append(others[key], id)
		}
	}
	ids := make([]string, 0, len(definitions))
	for id := range definitions {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if !IsPrivileged(id) {
			claim(id, effective(id))
		}
	}
	// User bindings for ids the launcher does not know (plugin commands) count too; plugin defaults never do.
	userIDs := make([]string, 0, len(user))
	for id := range user {
		userIDs = append(userIDs, id)
	}
	slices.Sort(userIDs)
	for _, id := range userIDs {
		if _, known := definitions[id]; !IsPrivileged(id) && !known {
			claim(id, user[id])
		}
	}
	resolution := PrivilegedResolution{Keys: map[string][]string{}}
	for _, id := range PrivilegedActions {
		kept := []string{}
		for _, key := range effective(id) {
			var colliding []string
			for _, other := range others[key] {
				_, userID := user[id]
				_, userOther := user[other]
				sameDefault := slices.Contains(definitions[id].DefaultKeys, key) && !userID && !userOther && slices.Contains(definitions[other].DefaultKeys, key)
				if !sameDefault {
					colliding = append(colliding, other)
				}
			}
			if len(colliding) > 0 {
				resolution.Lost = append(resolution.Lost, LostKey{ID: id, Key: key, To: colliding[0]})
			} else {
				kept = append(kept, key)
			}
		}
		resolution.Keys[id] = kept
	}
	return resolution
}
