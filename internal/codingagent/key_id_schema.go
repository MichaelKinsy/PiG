package codingagent

import (
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/tui"
)

// keyIDModifiers is the modifier list of key-id-schema.ts.
var keyIDModifiers = [...]string{"ctrl", "shift", "alt", "super"}

// keyIDBaseKeyList is Object.values(Key) in declaration order: the named and symbol keys of keys.ts. A letter or digit is a base key too.
var keyIDBaseKeyList = sync.OnceValue(func() []string {
	k := tui.Key
	var list []string
	for _, id := range []tui.KeyID{
		k.Escape, k.Esc, k.Enter, k.Return, k.Tab, k.Space, k.Backspace, k.Delete, k.Insert, k.Clear, k.Home, k.End,
		k.PageUp, k.PageDown, k.Up, k.Down, k.Left, k.Right,
		k.F1, k.F2, k.F3, k.F4, k.F5, k.F6, k.F7, k.F8, k.F9, k.F10, k.F11, k.F12,
		k.Backtick, k.Hyphen, k.Equals, k.Leftbracket, k.Rightbracket, k.Backslash, k.Semicolon, k.Quote, k.Comma, k.Period, k.Slash,
		k.Exclamation, k.At, k.Hash, k.Dollar, k.Percent, k.Caret, k.Ampersand, k.Asterisk, k.Leftparen, k.Rightparen, k.Underscore,
		k.Plus, k.Pipe, k.Tilde, k.Leftbrace, k.Rightbrace, k.Colon, k.Lessthan, k.Greaterthan, k.Question,
	} {
		list = append(list, string(id))
	}
	return list
})

// keyIDBaseKeys is keyIDBaseKeyList as a set. Both are built on first use, so commands that load no keybindings do not pay for them.
var keyIDBaseKeys = sync.OnceValue(func() map[string]struct{} {
	list := keyIDBaseKeyList()
	set := make(map[string]struct{}, len(list))
	for _, id := range list {
		set[id] = struct{}{}
	}
	return set
})

// KeyIDPattern is the KeyIdSchema pattern source of key-id-schema.ts, with each key escaped as /[\\^$.*+?()[\]{}|]/g does.
func KeyIDPattern() string {
	list := keyIDBaseKeyList()
	escaped := make([]string, len(list))
	for i, key := range list {
		escaped[i] = strings.NewReplacer(`\`, `\\`, "^", `\^`, "$", `\$`, ".", `\.`, "*", `\*`, "+", `\+`, "?", `\?`, "(", `\(`, ")", `\)`,
			"[", `\[`, "]", `\]`, "{", `\{`, "}", `\}`, "|", `\|`).Replace(key)
	}
	var duplicate []string
	for _, m := range keyIDModifiers {
		duplicate = append(duplicate, m+`\+.*`+m+`\+`)
	}
	return "^(?!.*(?:" + strings.Join(duplicate, "|") + "))(?:(?:" + strings.Join(keyIDModifiers[:], "|") + `)\+){0,4}(?:[a-z0-9]|` + strings.Join(escaped, "|") + ")$"
}

// KeybindingIDs lists every keybinding id of KEYBINDINGS in declaration order.
func (km *KeybindingsManager) KeybindingIDs() []string { return slices.Clone(km.ordered) }

// isKeyID is the KeyIdSchema pattern of key-id-schema.ts:
// ^(?!.*(?:ctrl\+.*ctrl\+|shift\+.*shift\+|alt\+.*alt\+|super\+.*super\+))(?:(?:ctrl|shift|alt|super)\+){0,4}(?:[a-z0-9]|<Key values>)$
func isKeyID(s string) bool {
	for _, modifier := range keyIDModifiers {
		prefix := modifier + "+"
		if first := strings.Index(s, prefix); first >= 0 && strings.Contains(s[first+len(prefix):], prefix) {
			return false
		}
	}
	return keyIDMatchesFrom(s, 0)
}

func keyIDMatchesFrom(s string, modifiers int) bool {
	if len(s) == 1 && (s[0] >= 'a' && s[0] <= 'z' || s[0] >= '0' && s[0] <= '9') {
		return true
	}
	if _, ok := keyIDBaseKeys()[s]; ok {
		return true
	}
	if modifiers == 4 {
		return false
	}
	for _, modifier := range keyIDModifiers {
		if rest, ok := strings.CutPrefix(s, modifier+"+"); ok && keyIDMatchesFrom(rest, modifiers+1) {
			return true
		}
	}
	return false
}
