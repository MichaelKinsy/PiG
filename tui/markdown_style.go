package tui

// Ports packages/tui/src/components/markdown.ts (style contexts and invalidation).

import (
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func markdownForeground(open, text string) string {
	if open == "" {
		return text
	}
	return open + text + FgClose(open)
}
func markdownDecoration(open, close, text string) string {
	if text == "" {
		return ""
	}
	return ansiSpan(open, close, text)
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
		Bold:            func(s string) string { return markdownDecoration("\x1b[1m", SGRBoldDimReset, s) },
		Italic:          func(s string) string { return markdownDecoration("\x1b[3m", SGRItalicReset, s) },
		Strikethrough:   func(s string) string { return markdownDecoration("\x1b[9m", SGRStrikeReset, s) },
		Underline:       func(s string) string { return markdownDecoration("\x1b[4m", SGRUnderlineReset, s) },
		HighlightCode:   highlightMarkdownCode,
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
	if m.styleContext != nil {
		return *m.styleContext
	}
	if m.styleOwner != nil {
		return m.styleOwner.defaultInlineStyleContext()
	}
	if !m.hasDefaultStylePrefix {
		m.defaultStylePrefix = markdownStylePrefix(m.applyDefaultStyle)
		m.hasDefaultStylePrefix = true
	}
	return inlineStyleContext{applyText: m.applyDefaultStyle, stylePrefix: m.defaultStylePrefix}
}
func (m *Markdown) childMarkdown(content string) *Markdown {
	child := NewMarkdownWithOptions(content, 0, 0, m.theme, m.defaultTextStyle, &m.options)
	child.Transform = nil
	child.defaultColor, child.defaultColorSet = m.defaultColor, m.defaultColorSet
	child.defaultItalic = m.defaultItalic
	child.styleContext = m.styleContext
	child.styleOwner = m
	child.inlineState = m.lexerState()
	if m.styleOwner != nil {
		child.styleOwner = m.styleOwner
	}
	return child
}
func (m *Markdown) latexEnabled() bool { return m.options.RenderLatex == nil || *m.options.RenderLatex }

func (m *Markdown) renderQuote(content string, lazyUnderlines []int, width int) []string {
	theme := m.markdownTheme()
	quoteStyle := func(text string) string { return theme.Quote(theme.Italic(text)) }
	prefix := markdownStylePrefix(quoteStyle)
	child := m.childMarkdown(content)
	child.styleContext = &inlineStyleContext{applyText: func(s string) string { return s }, stylePrefix: prefix}
	child.lazyUnderlines = lazyUnderlines
	contentWidth := max(1, width-2)
	logical := child.renderContent(content, contentWidth)
	// markdown.ts drops trailing empty quote lines, which renderContent keeps only before a link definition.
	for len(logical) > 0 && logical[len(logical)-1] == "" {
		logical = logical[:len(logical)-1]
	}
	var out []string
	for _, line := range logical {
		if prefix != "" {
			line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+prefix)
		}
		for _, wrapped := range widthx.WrapTextWithAnsi(quoteStyle(line), contentWidth) {
			out = append(out, theme.QuoteBorder("│ ")+wrapped)
		}
	}
	return out
}
