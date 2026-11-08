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
	// pig divergence (D313): Pi eebd5d90 lets a transformer opt into the custom, summary and changelog surfaces; Pi 1.5.0 has only the three transcript types.
	// MarkdownMessageCustom marks custom messages without a message renderer and the content of expanded skill blocks.
	MarkdownMessageCustom MarkdownMessageType = "custom"
	// MarkdownMessageSummary marks the summary of an expanded branch or compaction summary, without Pi's header.
	MarkdownMessageSummary MarkdownMessageType = "summary"
	// MarkdownMessageChangelog marks Pi's changelog ("What's New" and /changelog), the "Extension Updates" notice and the release note of the "Update Available" notice.
	MarkdownMessageChangelog MarkdownMessageType = "changelog"
)

// DefaultMarkdownMessageTypes are the surfaces a transformer runs on unless its registration lists others: the transcript messages.
func DefaultMarkdownMessageTypes() []MarkdownMessageType {
	return []MarkdownMessageType{MarkdownMessageUser, MarkdownMessageAssistant, MarkdownMessageAssistantThinking}
}

// MarkdownTransformerOptions mirrors Pi's MarkdownTransformerOptions.
type MarkdownTransformerOptions struct {
	// MessageTypes lists the surfaces the transformer runs on. Nil selects [DefaultMarkdownMessageTypes]; a non-nil empty list runs it nowhere.
	MessageTypes []MarkdownMessageType
}

// MarkdownTransformContext mirrors upstream MarkdownTransformContext.
type MarkdownTransformContext struct {
	// Context owns the off-loop host generation; it is not part of Pi's wire context.
	Context context.Context `json:"-"`
	// Retry is nil where nothing re-renders a message; otherwise a host proxy calls its RequestRetry, off the UI loop, once an abandoned transform can be asked again (D56). It is not part of Pi's wire context.
	Retry          RetryRequester      `json:"-"`
	MessageType    MarkdownMessageType `json:"messageType"`
	IsStreaming    bool                `json:"isStreaming"`
	AvailableWidth int                 `json:"availableWidth"`
	// MessageID is the persisted session-entry id. It is empty (Pi's undefined) for a live message or live rendering, and set when rendering from persisted session entries.
	MessageID string `json:"messageId,omitempty"`
	// Timestamp is the persisted session-entry ISO timestamp, with the same availability as MessageID.
	Timestamp string `json:"timestamp,omitempty"`
}

// RetryRequester marks the message being transformed as changed, so its next frame transforms it again.
type RetryRequester interface{ RequestRetry() }

// MarkdownTransformer performs a synchronous display-only Markdown rewrite.
type MarkdownTransformer func(markdown string, context MarkdownTransformContext) string

// CreateMarkdownTransform returns the transform a Markdown component applies at its available width: each transformer
// rewrites the Markdown in turn, and one that panics is skipped with the current Markdown kept. It is Pi's
// createMarkdownTransform(messageType, isStreaming, transformers, messageId, timestamp): messageID and timestamp are the persisted
// entry's, or empty for a live message.
func CreateMarkdownTransform(messageType MarkdownMessageType, isStreaming bool, transformers []MarkdownTransformer, messageID, timestamp string) func(markdown string, availableWidth int) string {
	return func(markdown string, availableWidth int) string {
		ctx := MarkdownTransformContext{MessageType: messageType, IsStreaming: isStreaming, AvailableWidth: availableWidth, MessageID: messageID, Timestamp: timestamp}
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
