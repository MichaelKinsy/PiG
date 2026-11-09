package tui

import (
	"context"

	"github.com/MichaelKinsy/PiG/coding/extension/markdowntransform"
)

// MarkdownSurface is the display-only Markdown transform of a message component whose body Pi renders from a Markdown source with a header of its own
// (custom messages, skill blocks, branch and compaction summaries). The component's owner passes it to the component's constructor, as Pi hands the
// component markdownTransformers, messageId and timestamp (createMarkdownTransform(type, false, markdownTransformers, messageId, timestamp)).
//
// The zero value shows the Markdown as it is. Transform and State are the synchronous rewrite and the state it reads for the Markdown render cache key; a
// nil State is static. Async, when set, is the off-loop rewrite of the same chain (pig additive, D19: subprocess callbacks execute outside the render
// loop) and replaces Transform: its completion marks the component dirty. A surface with neither never polls its body.
type MarkdownSurface struct {
	Transform func(markdown string, availableWidth int) string
	State     func() string
	Async     *AsyncMarkdownTransform
}

// markdownSurface is a MarkdownSurface in a component, with the body it built last so a rebuild releases that body's pending publication.
type markdownSurface struct {
	MarkdownSurface
	body *Markdown
}

// install sets the surface and, for an async one, makes its completions mark owner dirty.
func (s *markdownSurface) install(surface MarkdownSurface, owner *invalidatable) {
	s.MarkdownSurface = surface
	s.Async = completionDirtying(owner, surface.Async)
}

// markdown builds the finished body Markdown. The transformers see content only: the header is put in front of what they return, so a transformer never
// rewrites it (Pi: transform: (markdown, width) => header + transform(markdown, width)). Without a transform the body is header+content as before.
func (s *markdownSurface) markdown(header, content string, theme *MarkdownTheme, style *DefaultTextStyle) *Markdown {
	s.Dispose()
	if s.Transform == nil && s.Async == nil {
		return finishedMarkdown(NewMarkdownWithOptions(header+content, 0, 0, theme, style, nil))
	}
	transform := s.Transform
	var options *MarkdownOptions
	if transform != nil {
		options = &MarkdownOptions{Transform: func(markdown string, availableWidth int) string {
			return header + transform(markdown, availableWidth)
		}}
	}
	md := NewMarkdownWithOptions(content, 0, 0, theme, style, options)
	md.TransformState = s.State
	md.AsyncTransform = withHeader(s.Async, header)
	s.body = md
	return finishedMarkdown(md)
}

// Dispose revokes the pending publication of the body built last.
func (s *markdownSurface) Dispose() {
	if s.body != nil {
		s.body.Dispose()
		s.body = nil
	}
}

// withHeader is async whose rewrite is put behind header, which no transformer sees.
func withHeader(async *AsyncMarkdownTransform, header string) *AsyncMarkdownTransform {
	if async == nil || header == "" {
		return async
	}
	headed := *async
	headed.Prepare = func(markdown string, width int) func(context.Context) string {
		run := async.Prepare(markdown, width)
		return func(ctx context.Context) string { return header + run(ctx) }
	}
	return &headed
}

// transformerSurface is the surface of createMarkdownTransform(messageType, false, markdownTransformers, messageId, timestamp): the zero surface when
// there are no transformers, as Pi's chain of none leaves the Markdown as it is.
func transformerSurface(messageType markdowntransform.MarkdownMessageType, transformers []markdowntransform.MarkdownTransformer, messageID, timestamp string) MarkdownSurface {
	if len(transformers) == 0 {
		return MarkdownSurface{}
	}
	return MarkdownSurface{Transform: markdowntransform.CreateMarkdownTransform(messageType, false, transformers, messageID, timestamp)}
}

// summarySurface is [transformerSurface] for a branch or compaction summary.
func summarySurface(transformers []markdowntransform.MarkdownTransformer, messageID, timestamp string) MarkdownSurface {
	return transformerSurface(markdowntransform.MarkdownMessageSummary, transformers, messageID, timestamp)
}
