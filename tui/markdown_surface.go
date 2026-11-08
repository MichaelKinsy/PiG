package tui

// pig divergence (D313): Pi eebd5d90 passes the Markdown transformers to these message components; Pi 1.5.0 renders their body untransformed.
// markdownSurface is the display-only Markdown transform of a message component whose body Pi renders from a Markdown source with a header of its own
// (custom messages, skill blocks, branch and compaction summaries). The component's owner installs the transform and the state it reads, as Pi hands the
// component markdownTransformers, messageId and timestamp (createMarkdownTransform(type, false, markdownTransformers, messageId, timestamp)).
type markdownSurface struct {
	transform func(markdown string, availableWidth int) string
	state     func() string
}

// markdown builds the finished body Markdown. The transformers see content only: the header is put in front of what they return, so a transformer never
// rewrites it (Pi: transform: (markdown, width) => header + transform(markdown, width)). Without a transform the body is header+content as before.
func (s *markdownSurface) markdown(header, content string, theme *MarkdownTheme, style *DefaultTextStyle) *Markdown {
	if s.transform == nil {
		return finishedMarkdown(NewMarkdownWithOptions(header+content, 0, 0, theme, style, nil))
	}
	transform := s.transform
	md := NewMarkdownWithOptions(content, 0, 0, theme, style, &MarkdownOptions{Transform: func(markdown string, availableWidth int) string {
		return header + transform(markdown, availableWidth)
	}})
	md.TransformState = s.state
	return finishedMarkdown(md)
}
