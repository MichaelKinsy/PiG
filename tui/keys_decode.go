package tui

// keys_decode.go: printable key decoding helpers for TUI components.
//
// Ports the printable CSI-u / modifyOtherKeys decoding slice from
// packages/tui/src/keys.ts used by pi-tui's Input component.

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

const (
	kittyModifierShift = 1
	kittyModifierAlt   = 2
	kittyModifierCtrl  = 4
	kittyModifierSuper = 8
	kittyLockMask      = 64 + 128
)

const kittyPrintableAllowedModifiers = kittyModifierShift | kittyLockMask

var kittyCSIURegex = regexp.MustCompile(`^\x1b\[(\d+)(?::(\d*))?(?::(\d+))?(?:;(\d+))?(?::(\d+))?u$`)
var modifyOtherKeysRegex = regexp.MustCompile(`^\x1b\[27;(\d+);(\d+)~$`)

var kittyFunctionalKeyEquivalents = map[int]int{
	57399: 48, // KP_0 -> 0
	57400: 49, // KP_1 -> 1
	57401: 50,
	57402: 51,
	57403: 52,
	57404: 53,
	57405: 54,
	57406: 55,
	57407: 56,
	57408: 57,
	57409: 46, // .
	57410: 47, // /
	57411: 42, // *
	57412: 45, // -
	57413: 43, // +
	57415: 61, // =
	57416: 44, // ,
	57417: -4, // left
	57418: -3, // right
	57419: -1, // up
	57420: -2, // down
	57421: -12,
	57422: -13,
	57423: -14,
	57424: -15,
	57425: -11,
	57426: -10,
}

func normalizeKittyFunctionalCodepoint(codepoint int) int {
	if mapped, ok := kittyFunctionalKeyEquivalents[codepoint]; ok {
		return mapped
	}
	return codepoint
}

func parseOptionalInt(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// DecodeKittyPrintable decodes a printable Kitty CSI-u sequence into the
// corresponding character. Mirrors upstream decodeKittyPrintable().
func DecodeKittyPrintable(data string) (string, bool) {
	match := kittyCSIURegex.FindStringSubmatch(data)
	if match == nil {
		return "", false
	}
	codepoint, err := strconv.Atoi(match[1])
	if err != nil {
		return "", false
	}
	shiftedKey, hasShiftedKey := parseOptionalInt(match[2])
	modValue := 1
	if match[4] != "" {
		parsed, err := strconv.Atoi(match[4])
		if err != nil {
			return "", false
		}
		modValue = parsed
	}
	modifier := modValue - 1
	if modifier&^kittyPrintableAllowedModifiers != 0 {
		return "", false
	}
	if modifier&(kittyModifierAlt|kittyModifierCtrl) != 0 {
		return "", false
	}
	effectiveCodepoint := codepoint
	if modifier&kittyModifierShift != 0 {
		if hasShiftedKey {
			effectiveCodepoint = shiftedKey
		} else if codepoint >= 'a' && codepoint <= 'z' {
			// Shift+letter is layout-independent: uppercase the base when the
			// terminal reports the base codepoint without a shifted alternate
			// (e.g. \x1b[97;2u for Shift+a). Otherwise capital letters are lost.
			effectiveCodepoint = codepoint - 32
		}
	}
	effectiveCodepoint = normalizeKittyFunctionalCodepoint(effectiveCodepoint)
	// Upstream's String.fromCodePoint throws above U+10FFFF, which it maps to
	// undefined.
	if effectiveCodepoint < 32 || effectiveCodepoint > unicode.MaxRune {
		return "", false
	}
	return string(rune(effectiveCodepoint)), true
}

type parsedModifyOtherKeysSequence struct {
	codepoint int
	modifier  int
}

func parseModifyOtherKeysSequence(data string) (parsedModifyOtherKeysSequence, bool) {
	match := modifyOtherKeysRegex.FindStringSubmatch(data)
	if match == nil {
		return parsedModifyOtherKeysSequence{}, false
	}
	modValue, err := strconv.Atoi(match[1])
	if err != nil {
		return parsedModifyOtherKeysSequence{}, false
	}
	codepoint, err := strconv.Atoi(match[2])
	if err != nil {
		return parsedModifyOtherKeysSequence{}, false
	}
	return parsedModifyOtherKeysSequence{codepoint: codepoint, modifier: modValue - 1}, true
}

func decodeModifyOtherKeysPrintable(data string) (string, bool) {
	parsed, ok := parseModifyOtherKeysSequence(data)
	if !ok {
		return "", false
	}
	modifier := parsed.modifier &^ kittyLockMask
	if modifier&^kittyModifierShift != 0 {
		return "", false
	}
	if parsed.codepoint < 32 || parsed.codepoint > unicode.MaxRune {
		return "", false
	}
	return string(rune(parsed.codepoint)), true
}

// DecodePrintableKey decodes printable terminal sequences from either Kitty
// CSI-u or xterm modifyOtherKeys formats.
func DecodePrintableKey(data string) (string, bool) {
	if s, ok := DecodeKittyPrintable(data); ok {
		return s, true
	}
	return decodeModifyOtherKeysPrintable(data)
}

// ShouldDeliverKey reports whether a raw input chunk may be handed to a focused
// component's HandleInput. Kitty key releases are dropped unless the component
// implements KeyReleaseReceiver and opts in.
//
// This is the single decision point for focused-component key delivery, and
// every such handoff must route through it. A component that acts on a release
// fires each keystroke twice, because extendedKeyInit pushes \x1b[>7u, whose
// flag 2 makes the terminal report press, repeat, and release. Scattering the
// check across dispatch sites is what let extension dialogs move a selector
// cursor two rows per arrow press: the filter existed, but an earlier return
// bypassed it.
//
// Raw-input consumers are a different contract and must not use this: terminal
// input listeners, alt-screen viewport handling, and extension shortcut
// listeners all see unfiltered input by design, matching upstream's
// addInputListener and handleViewportInput.
//
// Mirrors upstream tui.ts:887 (isKeyRelease(data) && !wantsKeyRelease -> drop).
func ShouldDeliverKey(component Component, data string) bool {
	if !IsKeyRelease(data) {
		return true
	}
	receiver, ok := component.(KeyReleaseReceiver)
	return ok && receiver.WantsKeyRelease()
}

// IsKeyRelease reports whether a raw input chunk is a Kitty key-release event
// (flag 2, ":3" variants). Bracketed-paste content is never treated as a
// release even when it contains ":3" byte patterns. Mirrors upstream isKeyRelease.
//
// Prefer ShouldDeliverKey when routing input to a focused component; call this
// directly only from a raw-input consumer.
func IsKeyRelease(data string) bool {
	if strings.Contains(data, "\x1b[200~") {
		return false
	}
	return strings.Contains(data, ":3u") ||
		strings.Contains(data, ":3~") ||
		strings.Contains(data, ":3A") ||
		strings.Contains(data, ":3B") ||
		strings.Contains(data, ":3C") ||
		strings.Contains(data, ":3D") ||
		strings.Contains(data, ":3H") ||
		strings.Contains(data, ":3F")
}
