package tui

import (
	"os"
	"strings"
)

var parsedLegacySequenceKeyIDs = map[string]string{
	"\x1bOA":   "up",
	"\x1bOB":   "down",
	"\x1bOC":   "right",
	"\x1bOD":   "left",
	"\x1bOH":   "home",
	"\x1bOF":   "end",
	"\x1b[E":   "clear",
	"\x1bOE":   "clear",
	"\x1bOe":   "ctrl+clear",
	"\x1b[e":   "shift+clear",
	"\x1b[2~":  "insert",
	"\x1b[2$":  "shift+insert",
	"\x1b[2^":  "ctrl+insert",
	"\x1b[3$":  "shift+delete",
	"\x1b[3^":  "ctrl+delete",
	"\x1b[[5~": "pageUp",
	"\x1b[[6~": "pageDown",
	"\x1b[a":   "shift+up",
	"\x1b[b":   "shift+down",
	"\x1b[c":   "shift+right",
	"\x1b[d":   "shift+left",
	"\x1bOa":   "ctrl+up",
	"\x1bOb":   "ctrl+down",
	"\x1bOc":   "ctrl+right",
	"\x1bOd":   "ctrl+left",
	"\x1b[5$":  "shift+pageUp",
	"\x1b[6$":  "shift+pageDown",
	"\x1b[7$":  "shift+home",
	"\x1b[8$":  "shift+end",
	"\x1b[5^":  "ctrl+pageUp",
	"\x1b[6^":  "ctrl+pageDown",
	"\x1b[7^":  "ctrl+home",
	"\x1b[8^":  "ctrl+end",
	"\x1bOP":   "f1",
	"\x1bOQ":   "f2",
	"\x1bOR":   "f3",
	"\x1bOS":   "f4",
	"\x1b[11~": "f1",
	"\x1b[12~": "f2",
	"\x1b[13~": "f3",
	"\x1b[14~": "f4",
	"\x1b[[A":  "f1",
	"\x1b[[B":  "f2",
	"\x1b[[C":  "f3",
	"\x1b[[D":  "f4",
	"\x1b[[E":  "f5",
	"\x1b[15~": "f5",
	"\x1b[17~": "f6",
	"\x1b[18~": "f7",
	"\x1b[19~": "f8",
	"\x1b[20~": "f9",
	"\x1b[21~": "f10",
	"\x1b[23~": "f11",
	"\x1b[24~": "f12",
	"\x1bb":    "alt+left",
	"\x1bf":    "alt+right",
	"\x1bp":    "alt+up",
	"\x1bn":    "alt+down",
}

func formatParsedKey(codepoint, modifier int, baseLayoutKey int, hasBaseLayoutKey bool) (string, bool) {
	normalizedCodepoint := normalizeKittyFunctionalCodepoint(codepoint)
	identityCodepoint := normalizeShiftedLetterIdentityCodepoint(normalizedCodepoint, modifier)
	isLatinLetter := identityCodepoint >= 'a' && identityCodepoint <= 'z'
	isDigit := identityCodepoint >= '0' && identityCodepoint <= '9'
	effectiveCodepoint := identityCodepoint
	if !isLatinLetter && !isDigit && !isSymbolCodepoint(identityCodepoint) && hasBaseLayoutKey {
		effectiveCodepoint = baseLayoutKey
	}

	keyName, ok := parsedKeyName(effectiveCodepoint)
	if !ok {
		return "", false
	}
	return formatKeyNameWithModifiers(keyName, modifier)
}

func parsedKeyName(codepoint int) (string, bool) {
	switch codepoint {
	case cpEscape:
		return "escape", true
	case cpTab:
		return "tab", true
	case cpEnter, cpKPEnter:
		return "enter", true
	case cpSpace:
		return "space", true
	case cpBackspace:
		return "backspace", true
	case cpDelete:
		return "delete", true
	case cpInsert:
		return "insert", true
	case cpHome:
		return "home", true
	case cpEnd:
		return "end", true
	case cpPageUp:
		return "pageUp", true
	case cpPageDown:
		return "pageDown", true
	case cpArrowUp:
		return "up", true
	case cpArrowDown:
		return "down", true
	case cpArrowLeft:
		return "left", true
	case cpArrowRight:
		return "right", true
	}
	if codepoint >= '0' && codepoint <= 'z' && (codepoint <= '9' || codepoint >= 'a') {
		return string(rune(codepoint)), true
	}
	if isSymbolCodepoint(codepoint) {
		return string(rune(uint16(codepoint))), true
	}
	return "", false
}

func formatKeyNameWithModifiers(keyName string, modifier int) (string, bool) {
	effectiveModifier := modifier &^ lockMask
	if effectiveModifier & ^(modShift|modCtrl|modAlt|modSuper) != 0 {
		return "", false
	}
	modifiers := make([]string, 0, 4)
	if effectiveModifier&modShift != 0 {
		modifiers = append(modifiers, "shift")
	}
	if effectiveModifier&modCtrl != 0 {
		modifiers = append(modifiers, "ctrl")
	}
	if effectiveModifier&modAlt != 0 {
		modifiers = append(modifiers, "alt")
	}
	if effectiveModifier&modSuper != 0 {
		modifiers = append(modifiers, "super")
	}
	if len(modifiers) == 0 {
		return keyName, true
	}
	return strings.Join(modifiers, "+") + "+" + keyName, true
}

// ParseKey parses terminal input into Pi's canonical key identifier. The bool is
// false when the input is not one recognized key.
func ParseKey(data string) (string, bool) {
	if kitty := parseCSIu(data); kitty != nil {
		return formatParsedKey(kitty.codepoint, kitty.modifier, kitty.baseLayoutKey, kitty.hasBaseLayoutKey)
	}
	if modified := parseModifyOtherKeys(data); modified != nil {
		return formatParsedKey(modified.codepoint, modified.modifier, 0, false)
	}
	if modified := parseModifiedArrow(data); modified != nil {
		return formatParsedKey(modified.codepoint, modified.modifier, 0, false)
	}
	if modified := parseModifiedFunc(data); modified != nil {
		return formatParsedKey(modified.codepoint, modified.modifier, 0, false)
	}

	if IsKittyProtocolActive() && (data == "\x1b\r" || data == "\n") {
		return "shift+enter", true
	}
	if keyID, ok := parsedLegacySequenceKeyIDs[data]; ok {
		return keyID, true
	}

	switch data {
	case "\x1b":
		return "escape", true
	case "\x1c":
		return "ctrl+\\", true
	case "\x1d":
		return "ctrl+]", true
	case "\x1f":
		return "ctrl+-", true
	case "\x1b\x1b":
		return "ctrl+alt+[", true
	case "\x1b\x1c":
		return "ctrl+alt+\\", true
	case "\x1b\x1d":
		return "ctrl+alt+]", true
	case "\x1b\x1f":
		return "ctrl+alt+-", true
	case "\t":
		return "tab", true
	case "\r", "\x1bOM":
		return "enter", true
	case "\n":
		if !IsKittyProtocolActive() {
			return "enter", true
		}
	case "\x00":
		return "ctrl+space", true
	case " ":
		return "space", true
	case "\x7f":
		return "backspace", true
	case "\x08":
		if isLocalWindowsTerminalSession() {
			return "ctrl+backspace", true
		}
		return "backspace", true
	case "\x1b[Z":
		return "shift+tab", true
	case "\x1b\r":
		if !IsKittyProtocolActive() {
			return "alt+enter", true
		}
	case "\x1b ":
		if !IsKittyProtocolActive() {
			return "alt+space", true
		}
	case "\x1b\x7f", "\x1b\x08":
		return "alt+backspace", true
	case "\x1bB":
		if !IsKittyProtocolActive() {
			return "alt+left", true
		}
	case "\x1bF":
		if !IsKittyProtocolActive() {
			return "alt+right", true
		}
	case "\x1b[A":
		return "up", true
	case "\x1b[B":
		return "down", true
	case "\x1b[C":
		return "right", true
	case "\x1b[D":
		return "left", true
	case "\x1b[H":
		return "home", true
	case "\x1b[F":
		return "end", true
	case "\x1b[3~":
		return "delete", true
	case "\x1b[5~":
		return "pageUp", true
	case "\x1b[6~":
		return "pageDown", true
	}

	if !IsKittyProtocolActive() && len(data) == 2 && data[0] == '\x1b' {
		codepoint := data[1]
		if codepoint >= 1 && codepoint <= 26 {
			return "ctrl+alt+" + string(rune(codepoint+96)), true
		}
		if codepoint >= 'a' && codepoint <= 'z' || codepoint >= '0' && codepoint <= '9' || isSymbolKey(codepoint) {
			return "alt+" + string(rune(codepoint)), true
		}
	}
	if len(data) == 1 {
		codepoint := data[0]
		if codepoint >= 1 && codepoint <= 26 {
			return "ctrl+" + string(rune(codepoint+96)), true
		}
		if codepoint >= 32 && codepoint <= 126 {
			return data, true
		}
	}
	return "", false
}

func matchesRawBackspace(data string, expectedModifier int) bool {
	if data == "\x7f" {
		return expectedModifier == 0
	}
	if data != "\x08" {
		return false
	}
	if isLocalWindowsTerminalSession() {
		return expectedModifier == modCtrl
	}
	return expectedModifier == 0
}

func isLocalWindowsTerminalSession() bool {
	return os.Getenv("WT_SESSION") != "" && os.Getenv("SSH_CONNECTION") == "" && os.Getenv("SSH_CLIENT") == "" && os.Getenv("SSH_TTY") == ""
}
