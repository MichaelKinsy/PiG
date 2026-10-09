package tui

import (
	"runtime"
	"strings"
	"sync/atomic"
)

// appKeyTextResolver is atomic because components resolve key text where they are built, which for extension views is off
// the UI loop, while a keybindings reload installs a new resolver.
var appKeyTextResolver atomic.Pointer[func(action string) string]

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
			if capitalize {
				displayPart = jsUpperFirstUnit(displayPart)
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
	if resolver == nil {
		appKeyTextResolver.Store(nil)
		return
	}
	appKeyTextResolver.Store(&resolver)
}

// AppKeyText formats the resolved keys for an app-level keybinding, falling
// back to the provided default raw key text when no resolver is installed.
func AppKeyText(action, fallback string) string {
	if resolver := appKeyTextResolver.Load(); resolver != nil {
		if text := strings.TrimSpace((*resolver)(action)); text != "" {
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

// ActionKeyText formats every key the registry binds to action, joined by "/", or "" when none is bound. Mirrors upstream
// keyText (coding-agent keybinding-hints.ts:34); ActionKeyDisplayText is keyDisplayText, its capitalized form.
func ActionKeyText(action string) string {
	keys := GetTUIKeybindings().GetKeys(action)
	if len(keys) == 0 {
		return ""
	}
	return FormatKeyText(strings.Join(keys, "/"), false)
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

// KeyHint formats a display key and description with foreground-only resets, preserving enclosing text styles.
func KeyHint(key, description string) string {
	t := ActiveTheme()
	return t.Fg("dim", key) + t.Fg("muted", " "+description)
}

// RawKeyHint formats a raw key string without going through a keybinding registry.
// Mirrors upstream's rawKeyHint which skips the keybinding lookup.
func RawKeyHint(key, description string) string {
	return KeyHint(FormatKeyText(key, false), description)
}
