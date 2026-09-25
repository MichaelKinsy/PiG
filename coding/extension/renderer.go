package extension

// MarkdownMessageType identifies the transcript message being transformed for
// display. Transformers never change model context or persisted message data.
type MarkdownMessageType string

const (
	MarkdownMessageUser      MarkdownMessageType = "user"
	MarkdownMessageAssistant MarkdownMessageType = "assistant"
	// MarkdownMessageAssistantThinking marks the reasoning/thinking trace of an
	// assistant turn. Mirrors upstream MarkdownTransformContext.messageType
	// "assistant-thinking"; the built-in Mermaid transformer skips it.
	MarkdownMessageAssistantThinking MarkdownMessageType = "assistant-thinking"
)

// MarkdownTransformContext mirrors upstream MarkdownTransformContext.
type MarkdownTransformContext struct {
	MessageType    MarkdownMessageType `json:"messageType"`
	IsStreaming    bool                `json:"isStreaming"`
	AvailableWidth int                 `json:"availableWidth"`
}

// MarkdownTransformer performs a synchronous display-only Markdown rewrite.
type MarkdownTransformer func(markdown string, context MarkdownTransformContext) string

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
// is carried on [CustomMessage] (currently `any`) and the renderer
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
// the entry's data is carried on [CustomEntry] (currently `any`) and the
// renderer type-asserts as needed.
type EntryRenderer = func(
	entry CustomEntry,
	options EntryRenderOptions,
	theme Theme,
) Component
