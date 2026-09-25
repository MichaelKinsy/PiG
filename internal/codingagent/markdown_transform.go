package codingagent

import "github.com/MichaelKinsy/PiG/coding/extension"

// Markdown transform application, ported from upstream
// modes/interactive/components/markdown-transform.ts. A display-only pipeline:
// each transformer rewrites the markdown string in turn, and a transformer that
// panics is skipped so one bad transform never blanks the message.

// createMarkdownTransform returns the transform closure a Markdown component
// applies at its render width. Mirrors upstream createMarkdownTransform.
func createMarkdownTransform(
	messageType extension.MarkdownMessageType,
	isStreaming bool,
	transformers []extension.MarkdownTransformer,
) func(markdown string, availableWidth int) string {
	return func(markdown string, availableWidth int) string {
		ctx := extension.MarkdownTransformContext{
			MessageType:    messageType,
			IsStreaming:    isStreaming,
			AvailableWidth: availableWidth,
		}
		return applyMarkdownTransformers(markdown, ctx, transformers)
	}
}

func applyMarkdownTransformers(
	markdown string,
	ctx extension.MarkdownTransformContext,
	transformers []extension.MarkdownTransformer,
) string {
	transformed := markdown
	for _, transformer := range transformers {
		transformed = applyOne(transformed, ctx, transformer)
	}
	return transformed
}

// applyOne runs one transformer, keeping the current markdown if it panics
// (upstream catches and continues to the next transformer).
func applyOne(markdown string, ctx extension.MarkdownTransformContext, transformer extension.MarkdownTransformer) (out string) {
	out = markdown
	defer func() { _ = recover() }() // upstream: coding-agent/src/modes/interactive/components/markdown-transform.ts:transformedMarkdown
	out = transformer(markdown, ctx)
	return out
}
