package tui

import "strings"

// AssistantMessageBlock renders ordered text/thinking content and terminal diagnostics for one assistant turn.
type AssistantMessageBlock struct {
	invalidatable
	thinking          string
	text              string
	hidden            bool // whether thinking trace is hidden
	stopReason        string
	errorMessage      string
	hasToolCalls      bool // skip error/abort rendering when tools handle their own
	outputPad         int
	md                *Markdown
	thinkingTransform func(string, int) string
	// content retains untrimmed blocks so later deltas preserve boundary whitespace.
	content  []AssistantSegment
	segments []assistantSegment
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

// NewAssistantMessageBlock creates an empty block. Pass hiddenThinking=true when
// the user has toggled thinking visibility off (Ctrl+T).
func NewAssistantMessageBlock(hiddenThinking bool) *AssistantMessageBlock {
	return &AssistantMessageBlock{
		hidden:    hiddenThinking,
		outputPad: 1,
		md:        NewMarkdown(""),
	}
}

// SetOutputPad changes the horizontal content padding.
func (b *AssistantMessageBlock) SetOutputPad(padding int) {
	b.outputPad = max(0, min(1, padding))
	b.Invalidate()
}

// SetThinkingDelta appends to the current thinking block, or starts one after text.
func (b *AssistantMessageBlock) SetThinkingDelta(delta string) {
	b.appendDelta(true, delta)
}

// SetTextDelta appends to the current text block, or starts one after thinking.
func (b *AssistantMessageBlock) SetTextDelta(delta string) {
	b.appendDelta(false, delta)
}

func (b *AssistantMessageBlock) appendDelta(thinking bool, delta string) {
	if len(b.content) == 0 || b.content[len(b.content)-1].Thinking != thinking {
		b.content = append(b.content, AssistantSegment{Thinking: thinking})
	}
	b.content[len(b.content)-1].Text += delta
	b.SetContent(b.content)
}

// SetContent replaces text/thinking content in message order for streaming or redraw. Blocks are trimmed, empty ones skipped, and consecutive thinking blocks form one run joined by a blank line. An empty text segment preserves an invisible boundary such as a tool call.
func (b *AssistantMessageBlock) SetContent(content []AssistantSegment) {
	b.content = append(b.content[:0], content...)
	b.segments = b.segments[:0]
	var thinking, text []string
	for i := 0; i < len(content); i++ {
		if !content[i].Thinking {
			text = append(text, content[i].Text)
			if trimmed := strings.TrimSpace(content[i].Text); trimmed != "" {
				md := NewMarkdown(trimmed)
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
			md := NewMarkdown(joined)
			md.defaultItalic = true
			md.Transform = b.thinkingTransform
			md.TransformState = b.md.TransformState
			b.segments = append(b.segments, assistantSegment{thinking: true, md: md})
			thinking = append(thinking, joined)
		}
	}
	b.thinking = strings.Join(thinking, "\n\n")
	b.text = strings.Join(text, "")
	b.md.Content = b.text
	b.md.Invalidate()
	b.Invalidate()
}

// SetHiddenThinking controls whether the thinking trace renders as full text
// or as the "Thinking..." indicator. Mirrors upstream setHideThinkingBlock.
func (b *AssistantMessageBlock) SetHiddenThinking(hidden bool) {
	b.hidden = hidden
	b.Invalidate()
}

// SetMarkdownTransform installs a display-only rewrite applied to the text
// section at its render width, before markdown parsing. Mirrors upstream
// MarkdownOptions.transform threaded through createMarkdownTransform in
// assistant-message.ts:112. Used for the built-in Mermaid transformer.
func (b *AssistantMessageBlock) SetMarkdownTransform(fn func(markdown string, width int) string) {
	b.md.Transform = fn
	b.md.Invalidate()
	for _, seg := range b.segments {
		if !seg.thinking && seg.md != nil {
			seg.md.Transform = fn
			seg.md.Invalidate()
		}
	}
	b.Invalidate()
}

// SetThinkingMarkdownTransform installs the display-only rewrite for visible thinking, separate from the assistant-text transform context. Hidden thinking does not invoke it.
func (b *AssistantMessageBlock) SetThinkingMarkdownTransform(fn func(markdown string, width int) string) {
	b.thinkingTransform = fn
	for _, seg := range b.segments {
		if seg.thinking && seg.md != nil {
			seg.md.Transform = fn
			seg.md.Invalidate()
		}
	}
	b.Invalidate()
}

// SetMarkdownTransformState declares the external state the installed transform
// reads, so a change to it re-renders instead of serving the cached lines.
// Required whenever the transform is not a pure function of (markdown, width).
func (b *AssistantMessageBlock) SetMarkdownTransformState(fn func() string) {
	b.md.TransformState = fn
	b.md.Invalidate()
	for _, seg := range b.segments {
		if seg.md != nil {
			seg.md.TransformState = fn
			seg.md.Invalidate()
		}
	}
	b.Invalidate()
}

// Thinking returns the accumulated thinking content.
func (b *AssistantMessageBlock) Thinking() string { return b.thinking }

// Text returns concatenated untrimmed text blocks, without display transformations.
func (b *AssistantMessageBlock) Text() string { return b.text }

// SetHasToolCalls records that the assistant message contains tool calls.
// When true, the abort/error section is suppressed: tool execution
// components show their own error state. Mirrors upstream
// assistant-message.ts:128: `if (!hasToolCalls) { ... }`.
func (b *AssistantMessageBlock) SetHasToolCalls(v bool) {
	b.hasToolCalls = v
	b.Invalidate()
}

// SetTerminalError records length/error/abort state after partial assistant content. An empty error message renders "Unknown error"; tool calls suppress abort/error but not length diagnostics.
func (b *AssistantMessageBlock) SetTerminalError(stopReason, errorMessage string) {
	b.stopReason = stopReason
	b.errorMessage = errorMessage
	b.Invalidate()
}

// Render implements Component. Returns nil when thinking, text, and terminal
// error state are all empty.
func (b *AssistantMessageBlock) Render(width int) []string {
	if width < 1 {
		width = 1
	}
	var out []string
	contentWidth := max(1, width-b.outputPad*2)
	padding := strings.Repeat(" ", b.outputPad)
	padLine := func(line string) string {
		return padding + line
	}

	// Leading spacer when there is visible content.
	// Mirrors upstream AssistantMessageComponent.updateContent():
	//   if (hasVisibleContent) this.contentContainer.addChild(new Spacer(1))
	// This blank line separates assistant text from preceding tool blocks
	// and user messages: the key visual rhythm of the chat layout.
	if len(b.segments) > 0 {
		out = append(out, "")
	}

	out = append(out, b.renderSegments(contentWidth, padLine)...)
	return append(out, b.renderTerminalError(contentWidth, padLine)...)
}

// renderThinking renders the hidden label or Markdown with the theme's thinking text color and italic default text style.
func (b *AssistantMessageBlock) renderThinking(md *Markdown, contentWidth int, padLine func(string) string) []string {
	if b.hidden {
		return []string{padLine("\x1b[3m" + ActiveTheme().ThinkingText + thinkingHiddenLabel + SGRFgReset + SGRItalicReset)}
	}
	md.SetDefaultColor(ActiveTheme().ThinkingText)
	var out []string
	for _, line := range md.Render(contentWidth) {
		out = append(out, padLine(line))
	}
	return out
}

// renderSegments renders SetContent's ordered content. Upstream adds a spacer
// after a thinking run only when visible content follows it.
func (b *AssistantMessageBlock) renderSegments(contentWidth int, padLine func(string) string) []string {
	var out []string
	for i, seg := range b.segments {
		if !seg.thinking {
			for _, line := range seg.md.Render(contentWidth) {
				out = append(out, padLine(line))
			}
			continue
		}
		out = append(out, b.renderThinking(seg.md, contentWidth, padLine)...)
		if i+1 < len(b.segments) {
			out = append(out, "")
		}
	}
	return out
}

// hasTerminalError reports whether a length/error/abort line renders.
func (b *AssistantMessageBlock) hasTerminalError() bool {
	return b.stopReason == "length" || (!b.hasToolCalls && (b.stopReason == "error" || b.stopReason == "aborted"))
}

// renderTerminalError renders the length/error/abort section after content.
func (b *AssistantMessageBlock) renderTerminalError(contentWidth int, padLine func(string) string) []string {
	if !b.hasTerminalError() {
		return nil
	}
	out := []string{""}
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
	errText := ActiveTheme().Error + prefix + err + SGRFgReset
	for _, line := range wrapText(errText, contentWidth) {
		out = append(out, padLine(line))
	}
	return out
}
