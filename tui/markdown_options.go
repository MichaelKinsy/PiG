package tui

// Ports packages/tui/src/components/markdown.ts (constructor options and text replacement).

// MarkdownTheme supplies the styling functions used by Markdown. It mirrors packages/tui/src/components/markdown.ts.
type MarkdownTheme struct {
	Heading, Link, LinkUrl, Code, CodeBlock, CodeBlockBorder func(string) string
	Quote, QuoteBorder, Hr, ListBullet                       func(string) string
	Bold, Italic, Strikethrough, Underline                   func(string) string
	HighlightCode                                            func(code, lang string) []string
	CodeBlockIndent                                          *string
}

// DefaultTextStyle decorates ordinary Markdown text; background is applied after layout and padding.
type DefaultTextStyle struct {
	Color, BgColor                         func(string) string
	Bold, Italic, Strikethrough, Underline bool
}

// MarkdownOptions controls source preservation, display transforms and math rendering. Nil RenderLatex selects the upstream default, true.
type MarkdownOptions struct {
	PreserveOrderedListMarkers bool
	PreserveBackslashEscapes   bool
	Transform                  func(markdown string, availableWidth int) string
	RenderLatex                *bool
}

// NewMarkdownWithOptions maps the full upstream constructor. NewMarkdown supplies its normal unpadded, active-theme defaults without a Go overload.
func NewMarkdownWithOptions(content string, paddingX, paddingY int, theme *MarkdownTheme, style *DefaultTextStyle, options *MarkdownOptions) *Markdown {
	m := &Markdown{Content: content, paddingX: paddingX, paddingY: paddingY, theme: theme, defaultTextStyle: style}
	if options != nil {
		m.options = *options
		m.Transform = options.Transform
		if options.RenderLatex != nil {
			m.options.RenderLatex = new(*options.RenderLatex)
		}
	}
	return m
}

// SetText replaces source text and invalidates its rendered output.
func (m *Markdown) SetText(text string) { m.Content = text; m.Invalidate() }
