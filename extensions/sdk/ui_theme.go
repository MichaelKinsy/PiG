// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package sdk

import (
	"encoding/json"
	"fmt"
)

// UITheme is the host's active theme as extensions see it (upstream
// ctx.ui.theme): its name, the file a custom theme was loaded from, and the
// escape sequences the host resolved for each color token. The host
// replicates it with the state snapshot and every theme change; get the
// current one with [Context.UITheme].
//
// Methods port upstream Theme (modes/interactive/theme/theme.ts) over that
// palette, as the Node runtime's ThemeShim does. The zero value styles text
// with modifiers only, like a runtime that has not yet received a palette.
type UITheme struct {
	// Name is the theme's name.
	Name string
	// SourcePath is upstream Theme.sourcePath: the file a custom theme was
	// loaded from, empty for a built-in theme.
	SourcePath string

	foregrounds map[string]string
	backgrounds map[string]string
	// noModifiers is the host's modifiers=false: its chalk draws no bold,
	// italic, underline, inverse or strikethrough.
	noModifiers bool
	mode        string
}

// themePalette is the wire shape of the host's theme palette.
type themePalette struct {
	Name        any            `json:"name"`
	SourcePath  any            `json:"sourcePath"`
	Foregrounds map[string]any `json:"foregrounds"`
	Backgrounds map[string]any `json:"backgrounds"`
	Modifiers   *bool          `json:"modifiers"`
	Mode        any            `json:"mode"`
}

// decodeUITheme builds a UITheme from a host palette, as ThemeShim.setPalette
// does. ok is false when raw is not a JSON object.
func decodeUITheme(raw json.RawMessage) (UITheme, bool) {
	var probe any
	if len(raw) == 0 || json.Unmarshal(raw, &probe) != nil {
		return UITheme{}, false
	}
	if _, isObject := probe.(map[string]any); !isObject {
		return UITheme{}, false
	}
	var palette themePalette
	_ = json.Unmarshal(raw, &palette)
	theme := UITheme{
		foregrounds: ansiTokens(palette.Foregrounds),
		backgrounds: ansiTokens(palette.Backgrounds),
		noModifiers: palette.Modifiers != nil && !*palette.Modifiers,
		mode:        "truecolor",
	}
	if name, ok := palette.Name.(string); ok {
		theme.Name = name
	}
	if path, ok := palette.SourcePath.(string); ok {
		theme.SourcePath = path
	}
	if mode, ok := palette.Mode.(string); ok && mode == "256color" {
		theme.mode = mode
	}
	return theme, true
}

// ansiTokens keeps the tokens whose escape sequence is a non-empty string; any
// other value resolves as an unknown token.
func ansiTokens(values map[string]any) map[string]string {
	tokens := make(map[string]string, len(values))
	for token, value := range values {
		if ansi, ok := value.(string); ok && ansi != "" {
			tokens[token] = ansi
		}
	}
	return tokens
}

// Fg colors text with the foreground of token and resets only the
// foreground. An unknown token leaves text uncolored.
func (t UITheme) Fg(token, text string) string {
	open := t.foregrounds[token]
	if open == "" {
		return text
	}
	return open + text + "\x1b[39m"
}

// Bg colors text with the background of token and resets only the
// background. An unknown token leaves text uncolored.
func (t UITheme) Bg(token, text string) string {
	open := t.backgrounds[token]
	if open == "" {
		return text
	}
	return open + text + "\x1b[49m"
}

func (t UITheme) style(open, closing, text string) string {
	if t.noModifiers {
		return text
	}
	return open + text + closing
}

// Bold draws text bold.
func (t UITheme) Bold(text string) string { return t.style("\x1b[1m", "\x1b[22m", text) }

// Italic draws text italic.
func (t UITheme) Italic(text string) string { return t.style("\x1b[3m", "\x1b[23m", text) }

// Underline draws text underlined.
func (t UITheme) Underline(text string) string { return t.style("\x1b[4m", "\x1b[24m", text) }

// Inverse draws text with foreground and background swapped.
func (t UITheme) Inverse(text string) string { return t.style("\x1b[7m", "\x1b[27m", text) }

// Strikethrough draws text struck through.
func (t UITheme) Strikethrough(text string) string { return t.style("\x1b[9m", "\x1b[29m", text) }

// GetFgAnsi returns the foreground escape sequence of token, or upstream's
// "Unknown theme color" error.
func (t UITheme) GetFgAnsi(token string) (string, error) {
	ansi := t.foregrounds[token]
	if ansi == "" {
		return "", fmt.Errorf("Unknown theme color: %s", token)
	}
	return ansi, nil
}

// GetBgAnsi returns the background escape sequence of token, or upstream's
// "Unknown theme background color" error.
func (t UITheme) GetBgAnsi(token string) (string, error) {
	ansi := t.backgrounds[token]
	if ansi == "" {
		return "", fmt.Errorf("Unknown theme background color: %s", token)
	}
	return ansi, nil
}

// GetColorMode returns the host terminal's color mode: "truecolor" or
// "256color".
func (t UITheme) GetColorMode() string {
	if t.mode == "" {
		return "truecolor"
	}
	return t.mode
}

// thinkingBorderTokens maps a thinking level to its border color token, as
// upstream Theme.getThinkingBorderColor does.
var thinkingBorderTokens = map[string]string{
	"off":     "thinkingOff",
	"minimal": "thinkingMinimal",
	"low":     "thinkingLow",
	"medium":  "thinkingMedium",
	"high":    "thinkingHigh",
	"xhigh":   "thinkingXhigh",
	"max":     "thinkingMax",
}

// GetThinkingBorderColor returns the editor border colorizer for a thinking
// level. An unknown level uses the "off" color.
func (t UITheme) GetThinkingBorderColor(level string) func(string) string {
	token, ok := thinkingBorderTokens[level]
	if !ok {
		token = "thinkingOff"
	}
	return func(text string) string { return t.Fg(token, text) }
}

// GetBashModeBorderColor returns the editor border colorizer for bash mode.
func (t UITheme) GetBashModeBorderColor() func(string) string {
	return func(text string) string { return t.Fg("bashMode", text) }
}

// UITheme returns a snapshot of the host's active theme (upstream
// ctx.ui.theme), replicated from the state snapshot and theme changes.
func (c Context) UITheme() UITheme {
	c.ext.mu.RLock()
	defer c.ext.mu.RUnlock()
	// A palette is replaced whole and never mutated, so the snapshot can
	// share its maps.
	return c.ext.uiTheme
}
