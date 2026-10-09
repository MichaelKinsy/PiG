package tui

// Ports packages/coding-agent/src/modes/interactive/theme/theme.ts
// (getMarkdownTheme, getSelectListTheme, getSettingsListTheme). Upstream binds
// them to the global theme; these take the theme explicitly so a host can
// render with a per-view theme.

// SelectListThemeFor is theme.ts getSelectListTheme bound to th rather than the active theme
// ([GetSelectListTheme]).
func SelectListThemeFor(th *Theme) SelectListTheme {
	accent := func(text string) string { return th.Fg("accent", text) }
	muted := func(text string) string { return th.Fg("muted", text) }
	return SelectListTheme{
		SelectedPrefix: accent,
		SelectedText:   accent,
		Description:    muted,
		ScrollInfo:     muted,
		NoMatch:        muted,
	}
}

// SettingsListThemeFor is theme.ts getSettingsListTheme bound to th rather than the active
// theme ([GetSettingsListTheme]).
func SettingsListThemeFor(th *Theme) SettingsListTheme {
	dim := func(text string) string { return th.Fg("dim", text) }
	return SettingsListTheme{
		Label: func(text string, selected bool) string {
			if selected {
				return th.Fg("accent", text)
			}
			return text
		},
		Value: func(text string, selected bool) string {
			if selected {
				return th.Fg("accent", text)
			}
			return th.Fg("muted", text)
		},
		Description: dim,
		Cursor:      th.Fg("accent", "→ "),
		Hint:        dim,
	}
}

// MarkdownThemeFor is theme.ts getMarkdownTheme bound to th rather than the active theme
// ([GetMarkdownTheme]).
func MarkdownThemeFor(th *Theme) MarkdownTheme {
	return markdownThemeReading(func() *Theme { return th }, func(code, lang string) []string {
		if th == ActiveTheme() {
			return highlightMarkdownCode(code, lang)
		}
		// The highlight memo holds the active theme's colors only.
		return highlightCodeUncached(highlightRegistry(), code, lang, true, th)
	})
}

// markdownThemeReading builds the Markdown theme reading its theme from theme on
// every style call, so the default theme follows theme changes.
func markdownThemeReading(theme func() *Theme, highlightCode func(code, lang string) []string) MarkdownTheme {
	return MarkdownTheme{
		Heading:         func(s string) string { return markdownForeground(theme().MDHeading, s) },
		Link:            func(s string) string { return markdownForeground(theme().MDLink, s) },
		LinkUrl:         func(s string) string { return markdownForeground(theme().MDLinkUrl, s) },
		Code:            func(s string) string { return markdownForeground(theme().MDCode, s) },
		CodeBlock:       func(s string) string { return markdownForeground(theme().MDCodeBlock, s) },
		CodeBlockBorder: func(s string) string { return markdownForeground(theme().MDCodeBlockBorder, s) },
		Quote:           func(s string) string { return markdownForeground(theme().MDQuote, s) },
		QuoteBorder:     func(s string) string { return markdownForeground(theme().MDQuoteBorder, s) },
		Hr:              func(s string) string { return markdownForeground(theme().MDHr, s) },
		ListBullet:      func(s string) string { return markdownForeground(theme().MDListBullet, s) },
		Bold:            func(s string) string { return markdownDecoration("\x1b[1m", SGRBoldDimReset, s) },
		Italic:          func(s string) string { return markdownDecoration("\x1b[3m", SGRItalicReset, s) },
		Strikethrough:   func(s string) string { return markdownDecoration("\x1b[9m", SGRStrikeReset, s) },
		Underline:       func(s string) string { return markdownDecoration("\x1b[4m", SGRUnderlineReset, s) },
		HighlightCode:   highlightCode,
	}
}
