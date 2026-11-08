package render

import (
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
)

// MessageType is MarkdownTransformContext.messageType.
type MessageType string

const (
	MessageUser              MessageType = "user"
	MessageAssistant         MessageType = "assistant"
	MessageAssistantThinking MessageType = "assistant-thinking"
)

// MarkdownTransformContext is pi's `MarkdownTransformContext`. p2 adds the instance theme and the visitor's settings: pi's transformers close over
// its global theme and settings manager, p2's are registry entries shared by every instance (section 5.4).
//
// pi: apps/p2/src/runtime/tui-renderers.ts MarkdownTransformContext
type MarkdownTransformContext struct {
	MessageType    MessageType
	IsStreaming    bool
	AvailableWidth int
	Theme          *theme.Theme
	// Settings is nil when the visitor has none.
	Settings *prefs.TuiSettings
}

// MarkdownTransformer is pi's `MarkdownTransformer`: rewrites Markdown source before rendering. It is a func so a panic is the "error keeps the
// current text" case of CreateMarkdownTransform.
type MarkdownTransformer func(markdown string, context MarkdownTransformContext) string

// TransformSource is the `source` argument of createMarkdownTransform.
type TransformSource struct {
	Theme *theme.ThemeRef
	// Settings returns the visitor's current settings (nil when none); may itself be nil.
	Settings func() *prefs.TuiSettings
}

// CreateMarkdownTransform is coding-agent markdown-transform.ts createMarkdownTransform: nil when there is no transformer.
func CreateMarkdownTransform(messageType MessageType, isStreaming bool, transformers []MarkdownTransformer, source TransformSource) func(markdown string, availableWidth int) string {
	if len(transformers) == 0 {
		return nil
	}
	return func(markdown string, availableWidth int) string {
		result := markdown
		for _, transformer := range transformers {
			var settings *prefs.TuiSettings
			if source.Settings != nil {
				settings = source.Settings()
			}
			result = applyTransformer(transformer, result, MarkdownTransformContext{
				MessageType:    messageType,
				IsStreaming:    isStreaming,
				AvailableWidth: availableWidth,
				Theme:          source.Theme.Current(),
				Settings:       settings,
			})
		}
		return result
	}
}

// applyTransformer keeps the current text when the transformer panics (pi: `catch {}`).
func applyTransformer(transformer MarkdownTransformer, markdown string, context MarkdownTransformContext) (result string) {
	result = markdown
	defer func() { _ = recover() }()
	return transformer(markdown, context)
}
