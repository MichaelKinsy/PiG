// Code generated from tui/keys_decode.go by tui/sdk_tui_copy_test.go; DO NOT EDIT.

package engine

// keys_decode.go: printable key decoding helpers for TUI components.
//
// Ports the printable CSI-u / modifyOtherKeys decoding slice from
// packages/tui/src/keys.ts used by pi-tui's Input component.

import (
	"strings"
	"unicode"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui/internal/lazyregexp"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui/internal/jsstring"
)

const (
	kittyModifierShift = 1
	kittyModifierAlt   = 2
	kittyModifierCtrl  = 4
	kittyModifierSuper = 8
	kittyLockMask      = 64 + 128
)

const kittyPrintableAllowedModifiers = kittyModifierShift | kittyLockMask

var kittyCSIURegex = lazyregexp.New(`^\x1b\[(\d+)(?::(\d*))?(?::(\d+))?(?:;(\d+))?(?::(\d+))?u$`)
var modifyOtherKeysRegex = lazyregexp.New(`^\x1b\[27;(\d+);(\d+)~$`)

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

// DecodeKittyPrintable decodes a printable Kitty CSI-u sequence into the
// corresponding character. Mirrors upstream decodeKittyPrintable().
func DecodeKittyPrintable(data string) (string, bool) {
	match := kittyCSIURegex.FindStringSubmatch(data)
	if match == nil {
		return "", false
	}
	codepoint := keyCodepoint(keyNumber(match[1]))
	shiftedKey, hasShiftedKey := 0, match[2] != ""
	if hasShiftedKey {
		shiftedKey = keyCodepoint(keyNumber(match[2]))
	}
	modValue := 1.0
	if match[4] != "" {
		modValue = keyNumber(match[4])
	}
	modifier := jsInt32(modValue - 1)
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
		}
	}
	effectiveCodepoint = normalizeKittyFunctionalCodepoint(effectiveCodepoint)
	return printableCodepoint(effectiveCodepoint)
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
	return parsedModifyOtherKeysSequence{codepoint: keyCodepoint(keyNumber(match[2])), modifier: jsInt32(keyNumber(match[1]) - 1)}, true
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
	return printableCodepoint(parsed.codepoint)
}

// String.fromCodePoint accepts UTF-16 surrogate values; only out-of-range values and control characters are rejected by printable decoding.
func printableCodepoint(codepoint int) (string, bool) {
	if codepoint < 32 || codepoint > unicode.MaxRune {
		return "", false
	}
	if codepoint >= 0xd800 && codepoint <= 0xdfff {
		return jsstring.FromUTF16([]uint16{uint16(codepoint)}), true
	}
	return string(rune(codepoint)), true
}

// DecodePrintableKey decodes printable terminal sequences from either Kitty
// CSI-u or xterm modifyOtherKeys formats.
func DecodePrintableKey(data string) (string, bool) {
	if s, ok := DecodeKittyPrintable(data); ok {
		return s, true
	}
	return decodeModifyOtherKeysPrintable(data)
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

// KeyID is upstream's KeyId (keys.ts:152): a key name such as "enter", "ctrl+c" or "ctrl+shift+f5". Upstream's template-literal union is
// open here; use the Key helper to build identifiers.
type KeyID = string

// KeyEventType is the Kitty key event kind of an input sequence (keys.ts:505). Upstream's parser records it only in write-only module
// state (keys.ts:521 _lastEventType); IsKeyRepeat and IsKeyRelease answer the observable questions.
type KeyEventType string

// The KeyEventType values.
const (
	KeyEventPress   KeyEventType = "press"
	KeyEventRepeat  KeyEventType = "repeat"
	KeyEventRelease KeyEventType = "release"
)

// IsKeyRepeat reports whether a raw input chunk is a Kitty key-repeat event (flag 2, ":2" variants). Bracketed-paste content is never treated as a repeat even when it contains ":2" byte patterns. Mirrors upstream isKeyRepeat.
func IsKeyRepeat(data string) bool {
	if strings.Contains(data, "\x1b[200~") {
		return false
	}
	return strings.Contains(data, ":2u") ||
		strings.Contains(data, ":2~") ||
		strings.Contains(data, ":2A") ||
		strings.Contains(data, ":2B") ||
		strings.Contains(data, ":2C") ||
		strings.Contains(data, ":2D") ||
		strings.Contains(data, ":2H") ||
		strings.Contains(data, ":2F")
}
