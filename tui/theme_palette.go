package tui

import (
	"fmt"
	"maps"
	"strings"
)

// faintOpening is the SGR 2 the palette of a faint foreground token ends with (GetFgAnsi); the theme stores the token without it and draws it through dimTokens.
const faintOpening = "\x1b[2m"

// NewThemeFromANSIPalette is the inverse of [Theme.ANSIPalette]: the theme whose Fg and Bg draw with the given opening escape sequences. A subprocess extension
// holds the host's theme as such a palette, and a helper the host answers for it (highlightCode) must draw with the theme the extension holds, not with the
// one the host has active. The theme has no JSON source, so [Theme.WithColorMode] returns it unchanged. A palette that lacks a token the interface draws with
// is an error.
func NewThemeFromANSIPalette(name string, foregrounds, backgrounds map[string]string, mode TerminalColorMode) (theme *Theme, err error) {
	t := &Theme{
		Name:      name,
		mode:      mode,
		fgAnsi:    make(map[string]string, len(foregrounds)),
		bgAnsi:    make(map[string]string, len(backgrounds)),
		dimTokens: map[string]bool{},
	}
	for token, ansi := range foregrounds {
		if base, faint := strings.CutSuffix(ansi, faintOpening); faint {
			t.fgAnsi[token] = base
			t.dimTokens[token] = true
		} else {
			t.fgAnsi[token] = ansi
		}
	}
	maps.Copy(t.bgAnsi, backgrounds)
	defer func() {
		if recovered := recover(); recovered != nil {
			theme, err = nil, fmt.Errorf("theme palette: %v", recovered)
		}
	}()
	t.populateFields()
	return t, nil
}

// HighlightCodeFor is highlightCode of pi-coding-agent's theme.ts, or getMarkdownTheme().highlightCode when markdown is set (a failed highlight then takes the
// mdCodeBlock color too), drawn with th rather than the active theme.
func HighlightCodeFor(th *Theme, code, lang string, markdown bool) []string {
	if th == ActiveTheme() {
		return highlightCodeLines(code, lang, markdown)
	}
	return highlightCodeUncached(highlightRegistry(), code, lang, markdown, th)
}
