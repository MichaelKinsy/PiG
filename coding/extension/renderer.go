package extension

import "github.com/MichaelKinsy/PiG/coding/extension/markdowntransform"

// MarkdownMessageType identifies the transcript message being transformed for display. It is
// [markdowntransform.MarkdownMessageType], shared with the tui message components.
type MarkdownMessageType = markdowntransform.MarkdownMessageType

const (
	MarkdownMessageUser              = markdowntransform.MarkdownMessageUser
	MarkdownMessageAssistant         = markdowntransform.MarkdownMessageAssistant
	MarkdownMessageAssistantThinking = markdowntransform.MarkdownMessageAssistantThinking
	MarkdownMessageCustom            = markdowntransform.MarkdownMessageCustom
	MarkdownMessageSummary           = markdowntransform.MarkdownMessageSummary
	MarkdownMessageChangelog         = markdowntransform.MarkdownMessageChangelog
)

// MarkdownTransformerOptions mirrors Pi's MarkdownTransformerOptions. It is [markdowntransform.MarkdownTransformerOptions].
type MarkdownTransformerOptions = markdowntransform.MarkdownTransformerOptions

// DefaultMarkdownMessageTypes are the surfaces a transformer runs on unless its registration lists others.
func DefaultMarkdownMessageTypes() []MarkdownMessageType {
	return markdowntransform.DefaultMarkdownMessageTypes()
}

// MarkdownTransformerRegistration is a registered Markdown transformer and the surfaces it runs on (types.ts MarkdownTransformerRegistration).
// MessageTypes is Pi's ReadonlySet: a Go set of the listed types.
type MarkdownTransformerRegistration struct {
	Transformer  MarkdownTransformer
	MessageTypes map[MarkdownMessageType]struct{}
}

// NewMarkdownTransformerRegistration is the registration registerMarkdownTransformer(transformer, options) stores (loader.ts:365):
// the set of messageTypes, or of [DefaultMarkdownMessageTypes] when messageTypes is nil (options?.messageTypes ?? DEFAULT_MARKDOWN_MESSAGE_TYPES).
// A non-nil empty list runs the transformer nowhere.
func NewMarkdownTransformerRegistration(transformer MarkdownTransformer, messageTypes []MarkdownMessageType) *MarkdownTransformerRegistration {
	if messageTypes == nil {
		messageTypes = DefaultMarkdownMessageTypes()
	}
	types := make(map[MarkdownMessageType]struct{}, len(messageTypes))
	for _, messageType := range messageTypes {
		types[messageType] = struct{}{}
	}
	return &MarkdownTransformerRegistration{Transformer: transformer, MessageTypes: types}
}

// MarkdownTransformContext mirrors upstream MarkdownTransformContext. It is [markdowntransform.MarkdownTransformContext].
type MarkdownTransformContext = markdowntransform.MarkdownTransformContext

// MarkdownTransformer performs a synchronous display-only Markdown rewrite. It is [markdowntransform.MarkdownTransformer].
type MarkdownTransformer = markdowntransform.MarkdownTransformer

// MessageRenderOptions is passed to a [MessageRenderer].
type MessageRenderOptions struct {
	Expanded bool `json:"expanded"`
	// OutputPad is the horizontal padding configured by the outputPad setting.
	OutputPad int `json:"outputPad"`
}

// MessageRenderer mirrors upstream MessageRenderer<T>. Renders a custom
// session message into a TUI [Component], or returns nil to fall back to
// the default renderer.
//
// The Go signature drops the TS generic parameter T: the message's data
// is carried on [CustomMessage] (Content and Details are `any`) and the renderer
// type-asserts as needed. This avoids the compilation explosion of N
// generic instantiations across the host package and matches how
// subprocess extensions see this surface (untyped JSON over the wire).
type MessageRenderer = func(
	message CustomMessage,
	options MessageRenderOptions,
	theme Theme,
) Component

// EntryRenderOptions is passed to an [EntryRenderer]. Mirrors upstream
// EntryRenderOptions.
type EntryRenderOptions struct {
	Expanded bool `json:"expanded"`
}

// EntryRenderer mirrors upstream EntryRenderer<T>. Renders a custom session
// entry (appended via AppendEntry; not sent to the LLM) into a TUI [Component],
// or returns nil to fall back to the default renderer.
//
// As with [MessageRenderer], the Go signature drops the TS generic parameter T:
// the entry's data is carried on [CustomEntry].Data (`any`) and the
// renderer type-asserts as needed.
type EntryRenderer = func(
	entry CustomEntry,
	options EntryRenderOptions,
	theme Theme,
) Component
