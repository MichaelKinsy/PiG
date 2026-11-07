package extension

import "github.com/MichaelKinsy/PiG/coding/extension/markdowntransform"

// MarkdownMessageType identifies the transcript message being transformed for display. It is
// [markdowntransform.MarkdownMessageType], shared with the tui message components.
type MarkdownMessageType = markdowntransform.MarkdownMessageType

const (
	MarkdownMessageUser              = markdowntransform.MarkdownMessageUser
	MarkdownMessageAssistant         = markdowntransform.MarkdownMessageAssistant
	MarkdownMessageAssistantThinking = markdowntransform.MarkdownMessageAssistantThinking
)

// MarkdownTransformContext mirrors upstream MarkdownTransformContext. It is [markdowntransform.MarkdownTransformContext].
type MarkdownTransformContext = markdowntransform.MarkdownTransformContext

// MarkdownTransformer performs a synchronous display-only Markdown rewrite. It is [markdowntransform.MarkdownTransformer].
type MarkdownTransformer = markdowntransform.MarkdownTransformer

// RenderIdentity is the identity of one transcript message or entry across the renderer calls the transcript makes for it. Pi hands a renderer the
// same message object on every rebuild, so an extension keeps per-message state in a WeakMap keyed by it (a click-toggled card keeps its expansion
// that way). A Go renderer receives its message by value, so the transcript hands the identity along instead: one pointer per displayed message or
// entry, equal on every call for it and distinct from every other. Forced Go mechanic (no object identity for value structs).
//
// The padding keeps the identity out of the tiny allocator, which frees an object only with the 16-byte block it shares, so a cleanup on the identity
// would run late or never.
//
// pig additive (D276)
type RenderIdentity struct{ _ [16]byte }

// NewRenderIdentity returns a new identity, distinct from every other.
func NewRenderIdentity() *RenderIdentity { return new(RenderIdentity) }

// MessageRenderOptions is passed to a [MessageRenderer].
type MessageRenderOptions struct {
	Expanded bool `json:"expanded"`
	// OutputPad is the horizontal padding configured by the outputPad setting.
	OutputPad int `json:"outputPad"`
	// Identity is the message's identity (see [RenderIdentity]); nil for a renderer call that is not for a displayed message (HTML export), whose
	// message then has no state to keep and takes no mouse. It never crosses the wire.
	Identity *RenderIdentity `json:"-"`
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
	// Identity is the entry's identity, as [MessageRenderOptions.Identity] is the message's.
	Identity *RenderIdentity `json:"-"`
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
