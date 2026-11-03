package tui

// Ports packages/tui/src/components/markdown.ts (style contexts and invalidation).

import (
	"strings"
)

func markdownForeground(open, text string) string {
	if open == "" {
		return text
	}
	return open + text + FgClose(open)
}
func markdownDecoration(open, close, text string) string {
	if text == "" || chalkModifiersOff.Load() {
		return text
	}
	return ansiSpan(open, close, text)
}

// markdownBold, markdownItalic and markdownStrikethrough are the emphasis styles of every built-in markdown theme. The themes share these functions, so the one-pass span renderer recognizes them by function identity.
func markdownBold(s string) string { return markdownDecoration("\x1b[1m", SGRBoldDimReset, s) }

func markdownItalic(s string) string { return markdownDecoration("\x1b[3m", SGRItalicReset, s) }

func markdownStrikethrough(s string) string {
	return markdownDecoration("\x1b[9m", SGRStrikeReset, s)
}

// GetMarkdownTheme returns the markdown theme that styles each span with the active theme's markdown tokens (upstream theme.ts getMarkdownTheme). Every function reads ActiveTheme when it runs, so a theme switch restyles components that hold the result.
func GetMarkdownTheme() MarkdownTheme {
	return MarkdownTheme{
		Heading:         func(s string) string { return markdownForeground(ActiveTheme().MDHeading, s) },
		Link:            func(s string) string { return markdownForeground(ActiveTheme().MDLink, s) },
		LinkUrl:         func(s string) string { return markdownForeground(ActiveTheme().MDLinkUrl, s) },
		Code:            func(s string) string { return markdownForeground(ActiveTheme().MDCode, s) },
		CodeBlock:       func(s string) string { return markdownForeground(ActiveTheme().MDCodeBlock, s) },
		CodeBlockBorder: func(s string) string { return markdownForeground(ActiveTheme().MDCodeBlockBorder, s) },
		Quote:           func(s string) string { return markdownForeground(ActiveTheme().MDQuote, s) },
		QuoteBorder:     func(s string) string { return markdownForeground(ActiveTheme().MDQuoteBorder, s) },
		Hr:              func(s string) string { return markdownForeground(ActiveTheme().MDHr, s) },
		ListBullet:      func(s string) string { return markdownForeground(ActiveTheme().MDListBullet, s) },
		Bold:            markdownBold,
		Italic:          markdownItalic,
		Strikethrough:   markdownStrikethrough,
		Underline:       func(s string) string { return markdownDecoration("\x1b[4m", SGRUnderlineReset, s) },
		HighlightCode:   HighlightCode,
	}
}

// activeMarkdownTheme is GetMarkdownTheme built once. Its functions read the active theme when called, so one value serves every render.
var activeMarkdownTheme = GetMarkdownTheme()

func (m *Markdown) markdownTheme() MarkdownTheme {
	if m.theme != nil {
		return *m.theme
	}
	return activeMarkdownTheme
}
func markdownStylePrefix(style func(string) string) string {
	styled := style("\x00")
	before, _, ok := strings.Cut(styled, "\x00")
	if !ok {
		return ""
	}
	return before
}
func (m *Markdown) defaultInlineStyleContext() inlineStyleContext {
	if !m.hasDefaultStylePrefix {
		m.defaultStylePrefix = markdownStylePrefix(m.applyDefaultStyle)
		m.hasDefaultStylePrefix = true
	}
	return inlineStyleContext{applyText: m.applyDefaultStyle, stylePrefix: m.defaultStylePrefix, identity: m.defaultStyleIsIdentity()}
}

// defaultStyleIsIdentity reports whether applyDefaultStyle leaves text unchanged.
func (m *Markdown) defaultStyleIsIdentity() bool {
	style := m.defaultTextStyle
	colored := style != nil && style.Color != nil
	if m.defaultColorSet {
		colored = m.defaultColor != ""
	}
	return !colored && !m.defaultItalic && (style == nil || !style.Bold && !style.Italic && !style.Strikethrough && !style.Underline)
}
func (m *Markdown) latexEnabled() bool { return m.options.RenderLatex == nil || *m.options.RenderLatex }
