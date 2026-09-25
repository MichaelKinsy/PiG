// kitty_csi.go: Kitty CSI-u keyboard protocol decoder.
//
// The Kitty keyboard protocol encodes keystrokes as:
//   \x1b[<codepoint>;<modifier>u       (CSI-u format)
//   \x1b[1;<modifier><A-H>             (modified cursor keys)
//   \x1b[27;<modifier>;<codepoint>~    (xterm modifyOtherKeys)
//
// Modifier bits (1-indexed: modifier = 1 + bitfield):
//   Shift=1, Alt=2, Ctrl=4, Super=8, Hyper=16, Meta=32
//
// This decoder handles common modifier+key combinations that the
// hardcoded switch in classifyKey doesn't cover.
//
// Upstream reference: packages/tui/src/keys.ts (1400 LOC).

package codingagent

import (
	"strconv"
	"strings"
)

// kittyModShift is the Kitty modifier bit for Shift.
const kittyModShift = 1

// kittyModAlt is the Kitty modifier bit for Alt/Option.
const kittyModAlt = 2

// kittyModCtrl is the Kitty modifier bit for Ctrl.
const kittyModCtrl = 4

// csiFieldBase parses the numeric value before any ':' sub-parameter in a
// Kitty CSI field. Kitty encodes the codepoint field as
// base[:shifted[:base-layout]] (flag 4, alternate keys) and the modifier field
// as modifier[:event-type] (flag 2, event types); Pig's extendedKeyInit
// negotiates both (\x1b[>7u). We match on the base codepoint and the base
// modifier and ignore those sub-parameters; the base-layout alternate is
// honored earlier, by KeybindingsManager.Resolve. Key-release events (event-type 3)
// are filtered earlier by IsKeyRelease, so only press/repeat reach here.
func csiFieldBase(field string) (int, bool) {
	if i := strings.IndexByte(field, ':'); i >= 0 {
		field = field[:i]
	}
	n, err := strconv.Atoi(field)
	return n, err == nil
}

// hasKittyCSIuFraming reports whether data has Kitty CSI-u delimiters. The
// shared TUI matcher validates the fields before matching them.
func hasKittyCSIuFraming(data string) bool {
	return strings.HasPrefix(data, "\x1b[") && strings.HasSuffix(data, "u")
}

// classifyCSIu decodes Kitty CSI-u sequences and maps them to keyActions.
// Returns actionInsert if the sequence is not recognized or not a CSI-u.
func classifyCSIu(data string) keyAction {
	// CSI-u format: \x1b[<codepoint>[:alt][;<modifier>[:event]]u
	if hasKittyCSIuFraming(data) {
		body := data[2 : len(data)-1] // strip \x1b[ and u
		parts := strings.SplitN(body, ";", 2)
		codepoint, ok1 := csiFieldBase(parts[0])
		if !ok1 {
			return actionInsert
		}
		// The modifier field is optional in CSI-u. When absent (e.g. \x1b[27u
		// for an unmodified Escape, which the Kitty disambiguate flag emits) it
		// defaults to 1 (no modifier). Requiring the field dropped bare Escape
		// under the Kitty protocol, breaking single Esc and the double-Esc tree
		// shortcut.
		modifier := 1
		if len(parts) == 2 {
			m, ok2 := csiFieldBase(parts[1])
			if !ok2 {
				return actionInsert
			}
			modifier = m
		}
		// modifier is 1-indexed: subtract 1 to get bitfield
		return mapCSIuKey(codepoint, modifier-1)
	}

	// xterm modifyOtherKeys: \x1b[27;<modifier>;<codepoint>~
	if strings.HasPrefix(data, "\x1b[27;") && strings.HasSuffix(data, "~") {
		body := data[5 : len(data)-1] // strip \x1b[27; and ~
		parts := strings.SplitN(body, ";", 2)
		if len(parts) != 2 {
			return actionInsert
		}
		modifier, ok1 := csiFieldBase(parts[0])
		codepoint, ok2 := csiFieldBase(parts[1])
		if !ok1 || !ok2 {
			return actionInsert
		}
		return mapCSIuKey(codepoint, modifier-1)
	}

	return actionInsert
}

// mapCSIuKey maps a codepoint + modifier bitfield to the editor keys
// classifyKey routes itself: Enter submits and Shift+Enter inserts a
// newline. App actions are not decoded here: upstream runs one only when
// keybindings.matches accepts the key, and KeybindingsManager already
// matches every Kitty and modifyOtherKeys encoding, so a key whose action was
// rebound (by the user or by the Windows/WSL default column) goes to the
// editor.
func mapCSIuKey(codepoint, modBits int) keyAction {
	ctrl := modBits&kittyModCtrl != 0
	alt := modBits&kittyModAlt != 0
	shift := modBits&kittyModShift != 0

	switch {
	case codepoint == 13 && !ctrl && !alt && !shift:
		return actionSubmit
	case codepoint == 13 && !ctrl && !alt && shift:
		return actionNewline
	}
	return actionInsert
}
