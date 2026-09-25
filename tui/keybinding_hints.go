package tui

import (
	"runtime"
	"strings"
)

var appKeyTextResolver func(action string) string

// FormatKeyText formats a raw key string for UI display.
// It mirrors upstream formatKeyText by splitting alternate combos on "/"
// and combo parts on "+", mapping alt → option on macOS, and optionally
// capitalizing each part.
func FormatKeyText(key string, capitalize bool) string {
	variants := strings.Split(key, "/")
	for i, variant := range variants {
		parts := strings.Split(variant, "+")
		for j, part := range parts {
			displayPart := part
			if runtime.GOOS == "darwin" && strings.EqualFold(part, "alt") {
				displayPart = "option"
			}
			if capitalize && displayPart != "" {
				displayPart = strings.ToUpper(displayPart[:1]) + displayPart[1:]
			}
			parts[j] = displayPart
		}
		variants[i] = strings.Join(parts, "+")
	}
	return strings.Join(variants, "/")
}

// KeyDisplayText formats a raw key string in display form.
func KeyDisplayText(key string) string {
	return FormatKeyText(key, true)
}

// SetAppKeyTextResolver installs a resolver for app-level keybinding IDs
// (for example, `app.tree.foldOrUp`) used by TUI components that can't
// import the codingagent package directly.
func SetAppKeyTextResolver(resolver func(action string) string) {
	appKeyTextResolver = resolver
}

// AppKeyText formats the resolved keys for an app-level keybinding, falling
// back to the provided default raw key text when no resolver is installed.
func AppKeyText(action, fallback string) string {
	if appKeyTextResolver != nil {
		if text := strings.TrimSpace(appKeyTextResolver(action)); text != "" {
			return text
		}
	}
	return FormatKeyText(fallback, false)
}

// ActionKeyDisplayText formats every key the registry binds to action,
// capitalized and joined by "/", or "" when none is bound. Mirrors upstream
// keyDisplayText (coding-agent keybinding-hints.ts).
func ActionKeyDisplayText(action string) string {
	keys := GetTUIKeybindings().GetKeys(action)
	if len(keys) == 0 {
		return ""
	}
	return FormatKeyText(strings.Join(keys, "/"), true)
}

// ActionKeyDisplayTextOr is ActionKeyDisplayText for an action that may not be
// registered (app actions outside the interactive mode, such as in component
// tests), falling back to the upstream default keys.
func ActionKeyDisplayTextOr(action, fallback string) string {
	if GetTUIKeybindings().HasBinding(action) {
		return ActionKeyDisplayText(action)
	}
	return FormatKeyText(fallback, true)
}

// KeyHint formats a keybinding hint: dim key + muted description.
// Example: KeyHint("Esc", "cancel") → "\x1b[…Esc\x1b[0m \x1b[…cancel\x1b[0m"
func KeyHint(key, description string) string {
	t := ActiveTheme()
	return t.Dim + key + "\x1b[0m" + t.Muted + " " + description + "\x1b[0m"
}

// RawKeyHint formats a raw key string without going through a keybinding registry.
// Mirrors upstream's rawKeyHint which skips the keybinding lookup.
func RawKeyHint(key, description string) string {
	return KeyHint(FormatKeyText(key, false), description)
}
