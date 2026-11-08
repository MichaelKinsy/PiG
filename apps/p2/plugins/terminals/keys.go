package terminals

import (
	"regexp"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/terminals/ghostty"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports apps/p2/plugins/terminals/keys.ts. tmux key names (`send-keys`) as key presses for libghostty's encoder (terminal
// host): `Enter`, `C-c`, `M-x`, `S-Up`, `F5`, `^D`. The host encodes them in the terminal's current modes (application
// cursor keys, the kitty protocol), as a browser's key presses are.

type namedKey struct {
	code, key string
	shift     bool
}

// namedKeys are tmux's names for keys without a character (key-string.c), by `KeyboardEvent.code`.
var namedKeys = func() map[string]namedKey {
	keys := map[string]namedKey{
		"Enter":  {code: "Enter", key: "Enter"},
		"Escape": {code: "Escape", key: "Escape"},
		"Tab":    {code: "Tab", key: "Tab"},
		"BTab":   {code: "Tab", key: "Tab", shift: true},
		"BSpace": {code: "Backspace", key: "Backspace"},
		"Space":  {code: "Space", key: " "},
		"Up":     {code: "ArrowUp", key: "ArrowUp"},
		"Down":   {code: "ArrowDown", key: "ArrowDown"},
		"Left":   {code: "ArrowLeft", key: "ArrowLeft"},
		"Right":  {code: "ArrowRight", key: "ArrowRight"},
		"Home":   {code: "Home", key: "Home"},
		"End":    {code: "End", key: "End"},
		"PageUp": {code: "PageUp", key: "PageUp"}, "PgUp": {code: "PageUp", key: "PageUp"}, "PPage": {code: "PageUp", key: "PageUp"},
		"PageDown": {code: "PageDown", key: "PageDown"}, "PgDn": {code: "PageDown", key: "PageDown"}, "NPage": {code: "PageDown", key: "PageDown"},
		"IC": {code: "Insert", key: "Insert"}, "Insert": {code: "Insert", key: "Insert"},
		"DC": {code: "Delete", key: "Delete"}, "Delete": {code: "Delete", key: "Delete"},
	}
	for index := 1; index <= 12; index++ {
		name := "F" + itoa(index)
		keys[name] = namedKey{code: name, key: name}
	}
	return keys
}()

func itoa(value int) string {
	if value >= 10 {
		return string(rune('0'+value/10)) + string(rune('0'+value%10))
	}
	return string(rune('0' + value))
}

// punctuationKeys are the physical keys of the punctuation characters (a US layout), so Ctrl and Alt combine with them
// as typed.
var punctuationKeys = map[string]string{
	"`": "Backquote", "\\": "Backslash", "[": "BracketLeft", "]": "BracketRight", ",": "Comma", "=": "Equal",
	"-": "Minus", ".": "Period", "'": "Quote", ";": "Semicolon", "/": "Slash",
}

var (
	modifierPrefix = regexp.MustCompile(`^([CMS])-.`)
	lowerLetter    = regexp.MustCompile(`^[a-z]$`)
	digit          = regexp.MustCompile(`^[0-9]$`)
)

// TmuxKey is the key press a tmux key name stands for; false when it names no key (tmux then sends it as text).
func TmuxKey(name string) (ghostty.KeyInput, bool) {
	rest := name
	var ctrl, alt, shiftMod bool
	// Modifier prefixes in any order: `C-M-x`; `^x` is Ctrl too.
	for {
		if prefix := modifierPrefix.FindStringSubmatch(rest); prefix != nil {
			switch prefix[1] {
			case "C":
				ctrl = true
			case "M":
				alt = true
			default:
				shiftMod = true
			}
			rest = rest[2:]
			continue
		}
		if jsstring.Length(rest) == 2 && strings.HasPrefix(rest, "^") {
			ctrl = true
			rest = rest[1:]
		}
		break
	}
	// C-[ is Escape in tmux (and every terminal); libghostty sends it as a distinct key.
	if ctrl && rest == "[" {
		ctrl = false
		rest = "Escape"
	}
	if named, ok := namedKeys[rest]; ok {
		return ghostty.KeyInput{Code: named.code, Key: named.key, Shift: named.shift || shiftMod, Ctrl: ctrl, Alt: alt}, true
	}
	// A single character only names a key with a modifier: plain `a` is text.
	if jsstring.Length(rest) != 1 || rest == name {
		return ghostty.KeyInput{}, false
	}
	lower := strings.ToLower(rest)
	var code string
	switch {
	case lowerLetter.MatchString(lower):
		code = "Key" + strings.ToUpper(lower)
	case digit.MatchString(rest):
		code = "Digit" + rest
	case punctuationKeys[rest] != "":
		code = punctuationKeys[rest]
	case rest == " ":
		code = "Space"
	}
	// tmux's C-A is C-a: Ctrl ignores case. An uppercase letter otherwise means Shift.
	shift := shiftMod || (!ctrl && rest != lower)
	key := lower
	if shift {
		key = strings.ToUpper(rest)
	}
	return ghostty.KeyInput{Code: code, Key: key, Shift: shift, Ctrl: ctrl, Alt: alt}, true
}
