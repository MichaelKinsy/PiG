package sdk

import (
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

// keyHintGOOS is the platform key hints are written for (Pi reads process.platform); a test sets it to run another platform's cases.
var keyHintGOOS = runtime.GOOS

// KeyTextFormatOptions is pi-coding-agent's KeyTextFormatOptions.
type KeyTextFormatOptions struct {
	// Capitalize upper-cases the first letter of each part ("Ctrl+Enter").
	Capitalize bool
}

func formatKeyPart(goos, part string, capitalize bool) string {
	display := part
	if goos == "darwin" && strings.EqualFold(part, "alt") && jsLowerIsAlt(part) {
		display = "option"
	}
	if !capitalize || display == "" {
		return display
	}
	first, size := utf8.DecodeRuneInString(display)
	if first > 0xFFFF {
		// charAt(0).toUpperCase() upper-cases one UTF-16 unit: a character beyond the Basic Multilingual Plane is half a surrogate pair and stays as it is.
		return display
	}
	if full, ok := fullUppercase[first]; ok {
		return full + display[size:]
	}
	return strings.ToUpper(string(first)) + display[size:]
}

// jsLowerIsAlt is `part.toLowerCase() === "alt"` for the parts EqualFold admits: only ASCII case variants of "alt" qualify.
func jsLowerIsAlt(part string) bool {
	for i := 0; i < len(part); i++ {
		if part[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func formatKeyText(goos, key string, capitalize bool) string {
	slashed := strings.Split(key, "/")
	for i, alternative := range slashed {
		parts := strings.Split(alternative, "+")
		for j, part := range parts {
			parts[j] = formatKeyPart(goos, part, capitalize)
		}
		slashed[i] = strings.Join(parts, "+")
	}
	return strings.Join(slashed, "/")
}

// FormatKeyText is pi-coding-agent's formatKeyText: a key identifier as the interface shows it, "alt" as "option" on macOS.
func FormatKeyText(key string, options ...KeyTextFormatOptions) string {
	return formatKeyText(keyHintGOOS, key, len(options) > 0 && options[0].Capitalize)
}

func formatKeys(goos string, keys []string, capitalize bool) string {
	if len(keys) == 0 {
		return ""
	}
	return formatKeyText(goos, strings.Join(keys, "/"), capitalize)
}

// KeyText is pi-coding-agent's keyText: the keys of a keybinding as the interface shows them, from the global keybindings the host
// published (every tui.* and app.* definition with the user's overrides).
func KeyText(keybinding string) string {
	return formatKeys(keyHintGOOS, tui.GetKeybindings().GetKeys(keybinding), false)
}

// KeyDisplayText is pi-coding-agent's keyDisplayText: [KeyText] with each part capitalized.
func KeyDisplayText(keybinding string) string {
	return formatKeys(keyHintGOOS, tui.GetKeybindings().GetKeys(keybinding), true)
}

// KeyHint is pi-coding-agent's keyHint: the keys of a keybinding in the dim color and description in the muted color of the host's
// theme, as the interface prints a hint such as "esc to interrupt".
func KeyHint(keybinding, description string) string {
	theme := currentHostTheme()
	return theme.Fg("dim", KeyText(keybinding)) + theme.Fg("muted", " "+description)
}

// RawKeyHint is pi-coding-agent's rawKeyHint: a hint for a key identifier that is not a keybinding.
func RawKeyHint(key, description string) string {
	theme := currentHostTheme()
	return theme.Fg("dim", FormatKeyText(key)) + theme.Fg("muted", " "+description)
}

// fullUppercase are the characters whose String.prototype.toUpperCase is several letters (SpecialCasing.txt), which Go's one-to-one
// mapping leaves unchanged.
var fullUppercase = map[rune]string{
	'ß': "SS", 'ŉ': "ʼN", 'ǰ': "J̌", 'ΐ': "Ϊ́", 'ΰ': "Ϋ́", 'և': "ԵՒ", 'ẖ': "H̱", 'ẗ': "T̈", 'ẘ': "W̊", 'ẙ': "Y̊", 'ẚ': "Aʾ",
	'ﬀ': "FF", 'ﬁ': "FI", 'ﬂ': "FL", 'ﬃ': "FFI", 'ﬄ': "FFL", 'ﬅ': "ST", 'ﬆ': "ST",
}
