package tui

import (
	"slices"
	"strings"
)

const (
	assistantZoneStart = "\x1b]133;A\x07"
	assistantZoneEnd   = "\x1b]133;B\x07"
	assistantZoneFinal = "\x1b]133;C\x07"
)

// AssistantMessageComponent renders ordered text/thinking content, terminal diagnostics, and OSC 133 zones for one assistant turn.
// Ports packages/coding-agent/src/modes/interactive/components/assistant-message.ts.
type AssistantMessageComponent struct {
	Container
	contentContainer  *Container
	thinking          string
	text              string
	hidden            bool // whether thinking trace is hidden
	hiddenLabel       string
	stopReason        string
	errorMessage      string
	hasToolCalls      bool // skip error/abort rendering when tools handle their own
	outputPad         int
	md                *Markdown
	thinkingTransform func(string, int) string
	asyncText         *AsyncMarkdownTransform
	asyncThinking     *AsyncMarkdownTransform
	// content retains untrimmed blocks so later deltas preserve boundary whitespace.
	content                     []AssistantSegment
	segments                    []assistantSegment
	thinkingVisibilityOverrides map[int]bool
	markdownTheme               *MarkdownTheme
	lastMessage                 *AssistantMessage
	isStreaming                 bool
}

func (b *AssistantMessageComponent) newMarkdown(text string) *Markdown {
	return NewMarkdownWithOptions(text, 0, 0, b.markdownTheme, nil, nil)
}

// AssistantSegment is one text or thinking content block of a complete
// assistant message, in message order.
type AssistantSegment struct {
	Thinking bool
	Text     string
}

type assistantSegment struct {
	thinking bool
	md       *Markdown
}

// AssistantContentBlock is one content block of an AssistantMessage: Type "text" carries Text, "thinking" carries Thinking, and
// any other type (upstream "toolCall") is an invisible boundary between thinking runs.
type AssistantContentBlock struct {
	Type     string
	Text     string
	Thinking string
}

// AssistantMessage is the message argument of upstream assistant-message.ts updateContent: the ordered content blocks and the
// terminal stop reason ("length", "aborted", "error", ...) with its error message. tui cannot import the ai package, so it owns this copy.
type AssistantMessage struct {
	Content      []AssistantContentBlock
	StopReason   string
	ErrorMessage string
}

// NewAssistantMessageComponent is upstream's constructor (assistant-message.ts:26-49): a block that renders message when it is
// non-nil, with the thinking blocks hidden when hideThinkingBlock is true (Ctrl+T). A nil markdownTheme is the active theme's
// (getMarkdownTheme()), an empty hiddenThinkingLabel is "Thinking...", and a nil outputPad is 1 (any given value is kept, as in
// assistant-message.ts; only the settings layer limits it to 0 or 1). markdownTransformers rewrite the text and the visible thinking of the block, in order, with the
// block's streaming flag (markdown-transform.ts createMarkdownTransform); a host with a live transformer list uses
// SetMarkdownTransform and SetThinkingMarkdownTransform instead.
func NewAssistantMessageComponent(message *AssistantMessage, hideThinkingBlock bool, markdownTheme *MarkdownTheme, hiddenThinkingLabel string, outputPad *int, markdownTransformers []MarkdownTransformer) *AssistantMessageComponent {
	b := &AssistantMessageComponent{
		hidden:           hideThinkingBlock,
		hiddenLabel:      thinkingHiddenLabel,
		outputPad:        1,
		contentContainer: NewContainer(),
	}
	b.markdownTheme = markdownTheme
	if hiddenThinkingLabel != "" {
		b.hiddenLabel = hiddenThinkingLabel
	}
	if outputPad != nil {
		b.outputPad = max(0, *outputPad)
	}
	b.md = b.newMarkdown("")
	b.Add(b.contentContainer)
	if len(markdownTransformers) > 0 {
		b.SetMarkdownTransform(createMarkdownTransform("assistant", func() bool { return b.isStreaming }, markdownTransformers))
		b.SetThinkingMarkdownTransform(createMarkdownTransform("assistant-thinking", func() bool { return b.isStreaming }, markdownTransformers))
	}
	if message != nil {
		b.UpdateContent(*message)
	}
	return b
}

// MarkdownTransformContext is upstream's MarkdownTransformContext: the kind of message text being rewritten ("assistant" or
// "assistant-thinking" here), whether the block is streaming, and the width the text renders at.
type MarkdownTransformContext struct {
	MessageType    string
	IsStreaming    bool
	AvailableWidth int
}

// MarkdownTransformer is upstream's MarkdownTransformer: it returns the rewritten markdown, or ok=false for a result that is
// not a string (upstream's undefined), which keeps the markdown unchanged.
type MarkdownTransformer func(markdown string, context MarkdownTransformContext) (transformed string, ok bool)

// createMarkdownTransform is markdown-transform.ts createMarkdownTransform: each transformer sees the previous one's output; a
// panic (upstream's thrown exception) or a non-string result keeps the current markdown and continues with the next transformer.
func createMarkdownTransform(messageType string, isStreaming func() bool, transformers []MarkdownTransformer) func(string, int) string {
	return func(markdown string, width int) string {
		context := MarkdownTransformContext{MessageType: messageType, IsStreaming: isStreaming(), AvailableWidth: width}
		current := markdown
		for _, transformer := range transformers {
			func() {
				// upstream: packages/coding-agent/src/modes/interactive/components/markdown-transform.ts:applyMarkdownTransformers
				defer func() { _ = recover() }() // upstream try/catch: keep the current Markdown and continue with the next transformer
				if transformed, ok := transformer(current, context); ok {
					current = transformed
				}
			}()
		}
		return current
	}
}

// UpdateContent renders message, upstream assistant-message.ts:91 updateContent(message, isStreaming = this.isStreaming): the text
// and thinking blocks in order, consecutive thinking blocks as one run, tool calls as invisible boundaries, and the
// length/aborted/error diagnostic after the content (suppressed for aborted/error when the message has tool calls). The optional
// isStreaming replaces the retained flag; IsStreaming reports it.
func (b *AssistantMessageComponent) UpdateContent(message AssistantMessage, isStreaming ...bool) {
	b.lastMessage = &message
	if len(isStreaming) > 0 {
		b.isStreaming = isStreaming[0]
	}
	segments := make([]AssistantSegment, 0, len(message.Content))
	hasToolCalls := false
	for _, block := range message.Content {
		switch block.Type {
		case "text":
			segments = append(segments, AssistantSegment{Text: block.Text})
		case "thinking":
			segments = append(segments, AssistantSegment{Thinking: true, Text: block.Thinking})
		case "toolCall":
			hasToolCalls = true
			segments = append(segments, AssistantSegment{})
		}
	}
	b.hasToolCalls = hasToolCalls
	b.stopReason = message.StopReason
	b.errorMessage = message.ErrorMessage
	b.SetContent(segments)
}

// IsStreaming reports the isStreaming flag of the last UpdateContent.
func (b *AssistantMessageComponent) IsStreaming() bool { return b.isStreaming }

// SetHiddenThinkingLabel sets the label that stands in for a hidden thinking run (assistant-message.ts setHiddenThinkingLabel).
func (b *AssistantMessageComponent) SetHiddenThinkingLabel(label string) {
	b.hiddenLabel = label
	b.updateContent()
}

// SetOutputPad changes the horizontal content padding.
func (b *AssistantMessageComponent) SetOutputPad(padding int) {
	b.outputPad = max(0, padding)
	b.updateContent()
}

// SetThinkingDelta appends to the current thinking block, or starts one after text.
func (b *AssistantMessageComponent) SetThinkingDelta(delta string) {
	b.appendDelta(true, delta)
}

// SetTextDelta appends to the current text block, or starts one after thinking.
func (b *AssistantMessageComponent) SetTextDelta(delta string) {
	b.appendDelta(false, delta)
}

func (b *AssistantMessageComponent) appendDelta(thinking bool, delta string) {
	if len(b.content) == 0 || b.content[len(b.content)-1].Thinking != thinking {
		b.content = append(b.content, AssistantSegment{Thinking: thinking})
	}
	b.content[len(b.content)-1].Text += delta
	b.SetContent(b.content)
}

// SetContent replaces text/thinking content in message order for streaming or redraw. Blocks are trimmed, empty ones skipped, and consecutive thinking blocks form one run joined by a blank line. An empty text segment preserves an invisible boundary such as a tool call.
func (b *AssistantMessageComponent) SetContent(content []AssistantSegment) {
	b.content = append(b.content[:0], content...)
	previous := slices.Clone(b.segments)
	b.segments = b.segments[:0]
	markdown := func(text string, thinking bool) *Markdown {
		i := len(b.segments)
		if i < len(previous) && previous[i].thinking == thinking {
			md := previous[i].md
			md.Content = text
			md.Invalidate()
			return md
		}
		return b.newMarkdown(text)
	}
	var thinking, text []string
	for i := 0; i < len(content); i++ {
		if !content[i].Thinking {
			text = append(text, content[i].Text)
			if trimmed := strings.TrimSpace(content[i].Text); trimmed != "" {
				md := markdown(trimmed, false)
				md.AsyncTransform = b.asyncText
				md.Transform = b.md.Transform
				md.TransformState = b.md.TransformState
				b.segments = append(b.segments, assistantSegment{md: md})
			}
			continue
		}
		var run []string
		for ; i < len(content) && content[i].Thinking; i++ {
			if trimmed := strings.TrimSpace(content[i].Text); trimmed != "" {
				run = append(run, trimmed)
			}
		}
		i--
		if len(run) > 0 {
			joined := strings.Join(run, "\n\n")
			md := markdown(joined, true)
			md.AsyncTransform = b.asyncThinking
			md.defaultItalic = true
			md.Transform = b.thinkingTransform
			md.TransformState = b.md.TransformState
			b.segments = append(b.segments, assistantSegment{thinking: true, md: md})
			thinking = append(thinking, joined)
		}
	}
	for i, old := range previous {
		if i >= len(b.segments) || b.segments[i].md != old.md {
			old.md.Dispose()
		}
	}
	b.thinking = strings.Join(thinking, "\n\n")
	b.text = strings.Join(text, "")
	b.md.Content = b.text
	b.md.Invalidate()
	b.updateContent()
}

// Dispose releases this block's Markdown generations without allowing a late publication.
func (b *AssistantMessageComponent) Dispose() {
	b.md.Dispose()
	for _, segment := range b.segments {
		segment.md.Dispose()
	}
}

// SetHideThinkingBlock controls all thinking runs and clears individual click overrides (assistant-message.ts setHideThinkingBlock).
func (b *AssistantMessageComponent) SetHideThinkingBlock(hidden bool) {
	b.hidden = hidden
	clear(b.thinkingVisibilityOverrides)
	b.updateContent()
}

// SetMarkdownTransform installs a display-only rewrite applied to the text
// section at its render width, before markdown parsing. Mirrors upstream
// MarkdownOptions.transform threaded through createMarkdownTransform in
// assistant-message.ts:112. Used for the built-in Mermaid transformer.
func (b *AssistantMessageComponent) SetMarkdownTransform(fn func(markdown string, width int) string) {
	b.md.Transform = fn
	b.md.Invalidate()
	for _, seg := range b.segments {
		if !seg.thinking && seg.md != nil {
			seg.md.Transform = fn
			seg.md.Invalidate()
		}
	}
	b.updateContent()
}

// SetThinkingMarkdownTransform installs the display-only rewrite for visible thinking, separate from the assistant-text transform context. Hidden thinking does not invoke it.
func (b *AssistantMessageComponent) SetThinkingMarkdownTransform(fn func(markdown string, width int) string) {
	b.thinkingTransform = fn
	for _, seg := range b.segments {
		if seg.thinking && seg.md != nil {
			seg.md.Transform = fn
			seg.md.Invalidate()
		}
	}
	b.updateContent()
}

// SetMarkdownTransformState declares the external state the installed transform
// reads, so a change to it re-renders instead of serving the cached lines.
// Required whenever the transform is not a pure function of (markdown, width).
func (b *AssistantMessageComponent) SetMarkdownTransformState(fn func() string) {
	b.md.TransformState = fn
	b.md.Invalidate()
	for _, seg := range b.segments {
		if seg.md != nil {
			seg.md.TransformState = fn
			seg.md.Invalidate()
		}
	}
	b.updateContent()
}

// SetAsyncMarkdownTransforms installs separately contextualized text and thinking rewrites. Each retained segment owns its replaceable worker generation.
func (b *AssistantMessageComponent) SetAsyncMarkdownTransforms(text, thinking *AsyncMarkdownTransform) {
	b.asyncText, b.asyncThinking = text, thinking
	b.md.AsyncTransform = text
	for _, seg := range b.segments {
		seg.md.AsyncTransform = text
		if seg.thinking {
			seg.md.AsyncTransform = thinking
		}
	}
	b.updateContent()
}

// Thinking returns the accumulated thinking content.
func (b *AssistantMessageComponent) Thinking() string { return b.thinking }

// Text returns concatenated untrimmed text blocks, without display transformations.
func (b *AssistantMessageComponent) Text() string { return b.text }

// SetHasToolCalls records that the assistant message contains tool calls.
// When true, the abort/error section is suppressed: tool execution
// components show their own error state. Mirrors upstream
// assistant-message.ts:128: `if (!hasToolCalls) { ... }`.
func (b *AssistantMessageComponent) SetHasToolCalls(v bool) {
	b.hasToolCalls = v
	b.updateContent()
}

// SetTerminalError records length/error/abort state after partial assistant content. An empty error message renders "Unknown error"; tool calls suppress abort/error but not length diagnostics.
func (b *AssistantMessageComponent) SetTerminalError(stopReason, errorMessage string) {
	b.stopReason = stopReason
	b.errorMessage = errorMessage
	b.updateContent()
}

// Invalidate rebuilds the content so theme-baked colors follow the active theme (assistant-message.ts invalidate).
func (b *AssistantMessageComponent) Invalidate() {
	b.Container.Invalidate()
	b.updateContent()
}

// Render adds the OSC 133 zone boundaries around non-tool-call messages. Empty content with no terminal diagnostic has no rows.
func (b *AssistantMessageComponent) Render(width int) []string {
	if width < 1 {
		width = 1
	}
	out := slices.Clone(b.renderBorrowed(width))
	if b.hasToolCalls || len(out) == 0 {
		return out
	}
	out[0] = assistantZoneStart + out[0]
	out[len(out)-1] = assistantZoneEnd + assistantZoneFinal + out[len(out)-1]
	return out
}

// updateContent rebuilds the content container from the retained Markdown segments and the terminal state (assistant-message.ts updateContent).
func (b *AssistantMessageComponent) updateContent() {
	var children []Component
	if len(b.segments) > 0 {
		children = append(children, NewSpacer(1))
	}
	run := 0
	for i, seg := range b.segments {
		if seg.md.paddingX != b.outputPad {
			seg.md.paddingX = b.outputPad
			seg.md.Invalidate()
		}
		if !seg.thinking {
			children = append(children, seg.md)
			continue
		}
		hidden, overridden := b.thinkingVisibilityOverrides[run]
		if !overridden {
			hidden = b.hidden
		}
		var thinking Component
		if hidden {
			thinking = NewPaddedText("\x1b[3m"+ActiveTheme().ThinkingText+b.hiddenLabel+FgClose(ActiveTheme().ThinkingText)+SGRItalicReset, b.outputPad, 0, nil)
		} else {
			seg.md.SetDefaultColor(ActiveTheme().ThinkingText)
			thinking = seg.md
		}
		runIndex, wasHidden := run, hidden
		children = append(children, NewMouseRegion(thinking, func(event TuiMouseEvent) *TuiMouseEventResult {
			if event.Type != MouseClick || event.Button != MouseButtonLeft {
				return nil
			}
			if b.thinkingVisibilityOverrides == nil {
				b.thinkingVisibilityOverrides = make(map[int]bool)
			}
			b.thinkingVisibilityOverrides[runIndex] = !wasHidden
			b.updateContent()
			return &TuiMouseEventResult{Handled: true}
		}))
		run++
		// Upstream adds a spacer after a thinking run only when visible content follows it.
		if i+1 < len(b.segments) {
			children = append(children, NewSpacer(1))
		}
	}
	children = append(children, b.terminalErrorChildren()...)
	b.contentContainer.SetChildren(children...)
	b.Container.Invalidate()
}

// hasTerminalError reports whether a length/error/abort line renders.
func (b *AssistantMessageComponent) hasTerminalError() bool {
	return b.stopReason == "length" || (!b.hasToolCalls && (b.stopReason == "error" || b.stopReason == "aborted"))
}

// terminalErrorChildren builds the length/error/abort section after content.
func (b *AssistantMessageComponent) terminalErrorChildren() []Component {
	if !b.hasTerminalError() {
		return nil
	}
	return []Component{NewSpacer(1), NewPaddedText(b.terminalErrorText(), b.outputPad, 0, nil)}
}

// terminalErrorText is the styled length/error/abort line.
func (b *AssistantMessageComponent) terminalErrorText() string {
	err := b.errorMessage
	prefix := "Error: "
	switch {
	case b.stopReason == "length":
		err = "Response was truncated before completion."
		prefix = ""
	case b.stopReason == "aborted":
		// Upstream always shows "Operation aborted" unless there's a
		// meaningful provider error (assistant-message.ts:129-133).
		if err == "" || err == "Request was aborted" {
			err = "Operation aborted"
		}
	case err == "":
		err = "Unknown error"
	}
	if b.stopReason == "aborted" {
		prefix = ""
	}
	return ActiveTheme().Error + prefix + err + SGRFgReset
}
