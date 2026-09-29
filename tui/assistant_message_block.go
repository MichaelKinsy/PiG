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

type assistantThinkingRegion struct {
	start, end, run int
	hidden          bool
}

// AssistantMessageBlock renders ordered text/thinking content, terminal diagnostics, and OSC 133 zones for one assistant turn.
// Ports packages/coding-agent/src/modes/interactive/components/assistant-message.ts.
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
	asyncText         *AsyncMarkdownTransform
	asyncThinking     *AsyncMarkdownTransform
	// content retains untrimmed blocks so later deltas preserve boundary whitespace.
	content                     []AssistantSegment
	segments                    []assistantSegment
	thinkingVisibilityOverrides map[int]bool
	thinkingRegions             []assistantThinkingRegion
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
		return NewMarkdown(text)
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
	b.Invalidate()
}

// Dispose releases this block's Markdown generations without allowing a late publication.
func (b *AssistantMessageBlock) Dispose() {
	b.md.Dispose()
	for _, segment := range b.segments {
		segment.md.Dispose()
	}
}

// SetHiddenThinking controls all thinking runs and clears individual click overrides, as upstream setHideThinkingBlock does.
func (b *AssistantMessageBlock) SetHiddenThinking(hidden bool) {
	b.hidden = hidden
	clear(b.thinkingVisibilityOverrides)
	b.Invalidate()
}

// HandleMouse toggles only the rendered thinking run under a left click. Hit testing uses the last frame's row ranges without rendering or copying the message.
func (b *AssistantMessageBlock) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	if event.Type != MouseClick || event.Button != MouseButtonLeft || event.X < 0 || event.X >= event.Width || event.Y < 0 || event.Y >= event.Height {
		return nil
	}
	for _, region := range b.thinkingRegions {
		if event.Y >= region.start && event.Y < region.end {
			if b.thinkingVisibilityOverrides == nil {
				b.thinkingVisibilityOverrides = make(map[int]bool)
			}
			b.thinkingVisibilityOverrides[region.run] = !region.hidden
			b.Invalidate()
			return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true}}
		}
	}
	return nil
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

// SetAsyncMarkdownTransforms installs separately contextualized text and thinking rewrites. Each retained segment owns its replaceable worker generation.
func (b *AssistantMessageBlock) SetAsyncMarkdownTransforms(text, thinking *AsyncMarkdownTransform) {
	b.asyncText, b.asyncThinking = text, thinking
	b.md.AsyncTransform = text
	for _, seg := range b.segments {
		seg.md.AsyncTransform = text
		if seg.thinking {
			seg.md.AsyncTransform = thinking
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

// Render applies both horizontal margins and fills remaining cells. Image rows pass through unchanged. Non-tool-call messages carry OSC 133 zone boundaries. Empty content with no terminal diagnostic has no rows.
func (b *AssistantMessageBlock) Render(width int) []string {
	if width < 1 {
		width = 1
	}
	var out []string
	contentWidth := max(1, width-b.outputPad*2)
	padding := strings.Repeat(" ", b.outputPad)
	padLine := func(line string) string {
		if IsImageLine(line) {
			return line
		}
		line = padding + line + padding
		return line + strings.Repeat(" ", max(0, width-lineDisplayWidth(line)))
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
	out = append(out, b.renderTerminalError(contentWidth, padLine)...)
	if !b.hasToolCalls && len(out) > 0 {
		out[0] = assistantZoneStart + out[0]
		out[len(out)-1] = assistantZoneEnd + assistantZoneFinal + out[len(out)-1]
	}
	return out
}

// renderThinking renders the hidden label or Markdown with the theme's thinking text color and italic default text style.
func (b *AssistantMessageBlock) renderThinking(md *Markdown, hidden bool, contentWidth int, padLine func(string) string) []string {
	if hidden {
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
	b.thinkingRegions = b.thinkingRegions[:0]
	run := 0
	for i, seg := range b.segments {
		if !seg.thinking {
			for _, line := range seg.md.Render(contentWidth) {
				out = append(out, padLine(line))
			}
			continue
		}
		hidden, overridden := b.thinkingVisibilityOverrides[run]
		if !overridden {
			hidden = b.hidden
		}
		start := len(out) + 1 // leading content spacer
		out = append(out, b.renderThinking(seg.md, hidden, contentWidth, padLine)...)
		b.thinkingRegions = append(b.thinkingRegions, assistantThinkingRegion{start: start, end: len(out) + 1, run: run, hidden: hidden})
		run++
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
