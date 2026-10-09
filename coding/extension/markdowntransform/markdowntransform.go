// Package markdowntransform holds the display-only Markdown transformer contract and its chain. It is a dependency-free
// leaf, so both the extension API (which re-exports these types) and the tui message components that take transformers
// share one definition.
//
// Ports packages/coding-agent/src/modes/interactive/components/markdown-transform.ts
package markdowntransform

import "context"

// MarkdownMessageType identifies the transcript message being transformed for display. Transformers never change model
// context or persisted message data.
type MarkdownMessageType string

const (
	MarkdownMessageUser      MarkdownMessageType = "user"
	MarkdownMessageAssistant MarkdownMessageType = "assistant"
	// MarkdownMessageAssistantThinking marks the reasoning/thinking trace of an assistant turn. Mirrors upstream
	// MarkdownTransformContext.messageType "assistant-thinking"; the built-in Mermaid transformer skips it.
	MarkdownMessageAssistantThinking MarkdownMessageType = "assistant-thinking"
)

// MarkdownTransformContext mirrors upstream MarkdownTransformContext.
type MarkdownTransformContext struct {
	// Context owns the off-loop host generation; it is not part of Pi's wire context.
	Context        context.Context     `json:"-"`
	MessageType    MarkdownMessageType `json:"messageType"`
	IsStreaming    bool                `json:"isStreaming"`
	AvailableWidth int                 `json:"availableWidth"`
}

// MarkdownTransformer performs a synchronous display-only Markdown rewrite.
type MarkdownTransformer func(markdown string, context MarkdownTransformContext) string

// CreateMarkdownTransform returns the transform a Markdown component applies at its available width: each transformer
// rewrites the Markdown in turn, and one that panics is skipped with the current Markdown kept. It is Pi's
// createMarkdownTransform(messageType, isStreaming, transformers).
func CreateMarkdownTransform(messageType MarkdownMessageType, isStreaming bool, transformers []MarkdownTransformer) func(markdown string, availableWidth int) string {
	return func(markdown string, availableWidth int) string {
		ctx := MarkdownTransformContext{MessageType: messageType, IsStreaming: isStreaming, AvailableWidth: availableWidth}
		return ApplyMarkdownTransformers(markdown, ctx, transformers)
	}
}

// ApplyMarkdownTransformers runs transformers in order on markdown. A transformer that panics keeps the current Markdown,
// and a done ctx.Context stops the chain with the Markdown transformed so far.
func ApplyMarkdownTransformers(markdown string, ctx MarkdownTransformContext, transformers []MarkdownTransformer) string {
	transformed := markdown
	for _, transformer := range transformers {
		if ctx.Context != nil && ctx.Context.Err() != nil {
			return transformed
		}
		transformed = applyOne(transformed, ctx, transformer)
	}
	return transformed
}

// applyOne runs one transformer, keeping the current markdown if it panics (upstream catches and continues to the next
// transformer).
func applyOne(markdown string, ctx MarkdownTransformContext, transformer MarkdownTransformer) (out string) {
	out = markdown
	defer func() { _ = recover() }() // upstream: coding-agent/src/modes/interactive/components/markdown-transform.ts:transformedMarkdown
	out = transformer(markdown, ctx)
	return out
}
