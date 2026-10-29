package tui

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
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
	// mu serializes every access to the block's fields and the children
	// updateContent hands to contentContainer: extension view builders,
	// repaint callbacks and MouseRegion clicks mutate the block off the owner
	// goroutine (D107 conversation renderer), and an unsynchronized
	// updateContent pair let one caller's clear() write {0x0, 0x0} nil
	// Components into the child list another caller was handing to SetChildren
	// (the renderChildLocked nil-dispatch panic). Lock order is fixed
	// b.mu -> b.Container.mu -> contentContainer.mu: Render and updateContent
	// acquire container locks while holding b.mu, and container paths
	// (HandleMouse, render) never acquire b.mu while holding a container lock.
	mu               sync.Mutex
	contentContainer *Container
	// snapMu guards only the thinking and text fields. The getters read them
	// under snapMu.RLock instead of b.mu because a Markdown render under b.mu
	// can invoke a TransformState closure that calls Text or Thinking back on
	// this block, and sync.Mutex is not reentrant. Lock order is fixed
	// b.mu -> snapMu: the only writer is setContentLocked, which already holds
	// b.mu when it takes snapMu, and no code takes b.mu while holding snapMu.
	snapMu            sync.RWMutex
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
	// isStreaming is atomic: the markdown transform closure reads it while a
	// render holds b.mu, and sync.Mutex is not reentrant.
	isStreaming atomic.Bool
	// finished reports that FinishStreaming ran: the block's Markdown keeps no streaming memos.
	finished bool
	// previousSegments is the scratch copy SetContent takes of segments.
	previousSegments []assistantSegment
	// childScratch is the scratch list updateContent hands to the content
	// container; b.mu serializes updateContent, so the slice is single-writer
	// and SetChildren copies it before the next update reuses it.
	childScratch []Component
	// spacers, thinkingRuns and terminalError are the children updateContent keeps across updates (see updateContent).
	spacers      []*Spacer
	thinkingRuns []thinkingRunRegion
	// mountedRuns is the number of thinking runs the last updateContent
	// mounted into the content container; b.thinkingRuns keeps stale entries
	// past it, and their click callbacks must not toggle a run index that no
	// longer belongs to them.
	mountedRuns       int
	terminalError     []Component
	terminalErrorLine string
	terminalErrorPad  int
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
func NewAssistantMessageComponent(message *AssistantMessage, hideThinkingBlock bool, markdownTheme *MarkdownTheme, hiddenThinkingLabel string, outputPad *int, markdownTransformers []MarkdownTransformer, messageID, timestamp string) *AssistantMessageComponent {
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
		b.SetMarkdownTransform(createMarkdownTransform("assistant", func() bool { return b.isStreaming.Load() }, markdownTransformers, messageID, timestamp))
		b.SetThinkingMarkdownTransform(createMarkdownTransform("assistant-thinking", func() bool { return b.isStreaming.Load() }, markdownTransformers, messageID, timestamp))
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
	// MessageID and Timestamp are the persisted entry's, empty for a live message.
	MessageID string
	Timestamp string
}

// MarkdownTransformer is upstream's MarkdownTransformer: it returns the rewritten markdown, or ok=false for a result that is
// not a string (upstream's undefined), which keeps the markdown unchanged.
type MarkdownTransformer func(markdown string, context MarkdownTransformContext) (transformed string, ok bool)

// createMarkdownTransform is markdown-transform.ts createMarkdownTransform: each transformer sees the previous one's output; a
// panic (upstream's thrown exception) or a non-string result keeps the current markdown and continues with the next transformer.
func createMarkdownTransform(messageType string, isStreaming func() bool, transformers []MarkdownTransformer, messageID, timestamp string) func(string, int) string {
	return func(markdown string, width int) string {
		context := MarkdownTransformContext{MessageType: messageType, IsStreaming: isStreaming(), AvailableWidth: width, MessageID: messageID, Timestamp: timestamp}
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
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastMessage = &message
	if len(isStreaming) > 0 {
		b.isStreaming.Store(isStreaming[0])
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
	b.setContentLocked(segments)
}

// FinishStreaming tells the block that its message is complete: its Markdown drops the memos that only speed up the
// render of the next streamed delta. The rows are unchanged. A hidden thinking run shows its label, so its Markdown does
// not render again and drops them now (Markdown.FinishStreaming keeps them for an undrawn text's next render).
func (b *AssistantMessageComponent) FinishStreaming() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.finished = true
	run := 0
	for _, segment := range b.segments {
		segment.md.FinishStreaming()
		if segment.thinking {
			if b.thinkingRunHidden(run) {
				segment.md.dropStreamingMemos()
			}
			run++
		}
	}
}

// thinkingRunHidden reports whether thinking run index shows the hidden-thinking label: its click override, else Ctrl+T.
func (b *AssistantMessageComponent) thinkingRunHidden(index int) bool {
	if hidden, overridden := b.thinkingVisibilityOverrides[index]; overridden {
		return hidden
	}
	return b.hidden
}

// IsStreaming reports the isStreaming flag of the last UpdateContent.
func (b *AssistantMessageComponent) IsStreaming() bool { return b.isStreaming.Load() }

// SetHiddenThinkingLabel sets the label that stands in for a hidden thinking run (assistant-message.ts setHiddenThinkingLabel).
func (b *AssistantMessageComponent) SetHiddenThinkingLabel(label string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.hiddenLabel = label
	b.updateContent()
}

// SetOutputPad changes the horizontal content padding.
func (b *AssistantMessageComponent) SetOutputPad(padding int) {
	b.mu.Lock()
	defer b.mu.Unlock()
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
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.content) == 0 || b.content[len(b.content)-1].Thinking != thinking {
		b.content = append(b.content, AssistantSegment{Thinking: thinking})
	}
	b.content[len(b.content)-1].Text += delta
	b.setContentLocked(b.content)
}

// SetContent replaces text/thinking content in message order for streaming or redraw. Blocks are trimmed, empty ones skipped, and consecutive thinking blocks form one run joined by a blank line. An empty text segment preserves an invisible boundary such as a tool call.
func (b *AssistantMessageComponent) SetContent(content []AssistantSegment) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.setContentLocked(content)
}

// setContentLocked is SetContent under b.mu.
func (b *AssistantMessageComponent) setContentLocked(content []AssistantSegment) {
	b.content = append(b.content[:0], content...)
	b.previousSegments = append(b.previousSegments[:0], b.segments...)
	previous := b.previousSegments
	b.segments = b.segments[:0]
	markdown := func(text string, thinking bool) *Markdown {
		i := len(b.segments)
		if i < len(previous) && previous[i].thinking == thinking {
			md := previous[i].md
			md.SetText(text)
			return md
		}
		md := b.newMarkdown(text)
		if b.finished {
			md.FinishStreaming()
		}
		return md
	}
	var thinking, text []string
	for i := 0; i < len(content); i++ {
		if !content[i].Thinking {
			text = append(text, content[i].Text)
			if trimmed := jsstring.Trim(content[i].Text); trimmed != "" {
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
			if trimmed := jsstring.Trim(content[i].Text); trimmed != "" {
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
	clear(previous) // the scratch copy must not keep disposed Markdown alive
	// Writers hold b.mu, then snapMu; Thinking and Text take only snapMu.
	b.snapMu.Lock()
	b.thinking = strings.Join(thinking, "\n\n")
	b.text = strings.Join(text, "")
	b.snapMu.Unlock()
	b.md.SetText(b.text)
	b.updateContent()
}

// Dispose releases this block's Markdown generations without allowing a late publication.
func (b *AssistantMessageComponent) Dispose() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.md.Dispose()
	for _, segment := range b.segments {
		segment.md.Dispose()
	}
}

// SetHideThinkingBlock controls all thinking runs and clears individual click overrides (assistant-message.ts setHideThinkingBlock).
func (b *AssistantMessageComponent) SetHideThinkingBlock(hidden bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.hidden = hidden
	clear(b.thinkingVisibilityOverrides)
	b.updateContent()
}

// SetMarkdownTransform installs a display-only rewrite applied to the text
// section at its render width, before markdown parsing. Mirrors upstream
// MarkdownOptions.transform threaded through createMarkdownTransform in
// assistant-message.ts:112. Used for the built-in Mermaid transformer.
// fn runs at render time while the render holds b.mu, so it may call only
// read-only getters such as Text, Thinking and IsStreaming; calling a
// mutator of this block deadlocks.
func (b *AssistantMessageComponent) SetMarkdownTransform(fn func(markdown string, width int) string) {
	b.mu.Lock()
	defer b.mu.Unlock()
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
// fn runs under b.mu at render time, with the same constraint as SetMarkdownTransform.
func (b *AssistantMessageComponent) SetThinkingMarkdownTransform(fn func(markdown string, width int) string) {
	b.mu.Lock()
	defer b.mu.Unlock()
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
// fn runs under b.mu at render time, with the same constraint as SetMarkdownTransform.
func (b *AssistantMessageComponent) SetMarkdownTransformState(fn func() string) {
	b.mu.Lock()
	defer b.mu.Unlock()
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
// Each transform's Prepare runs under b.mu at render time, with the same constraint as SetMarkdownTransform.
func (b *AssistantMessageComponent) SetAsyncMarkdownTransforms(text, thinking *AsyncMarkdownTransform) {
	b.mu.Lock()
	defer b.mu.Unlock()
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

// Thinking returns the accumulated thinking content. It reads under
// snapMu.RLock rather than b.mu because a Markdown TransformState callback may
// run while a render holds b.mu (sync.Mutex is not reentrant); the only
// writer already holds b.mu when it takes snapMu, so a callback cannot
// interleave with a write.
func (b *AssistantMessageComponent) Thinking() string {
	b.snapMu.RLock()
	defer b.snapMu.RUnlock()
	return b.thinking
}

// Text returns concatenated untrimmed text blocks, without display
// transformations; snapMu.RLock for the same reason as Thinking.
func (b *AssistantMessageComponent) Text() string {
	b.snapMu.RLock()
	defer b.snapMu.RUnlock()
	return b.text
}

// SetHasToolCalls records that the assistant message contains tool calls.
// When true, the abort/error section is suppressed: tool execution
// components show their own error state. Mirrors upstream
// assistant-message.ts:128: `if (!hasToolCalls) { ... }`.
func (b *AssistantMessageComponent) SetHasToolCalls(v bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.hasToolCalls == v {
		return // every tool-call argument delta reports it; nothing it decides changed
	}
	b.hasToolCalls = v
	b.updateContent()
}

// SetTerminalError records length/error/abort state after partial assistant content. An empty error message renders "Unknown error"; tool calls suppress abort/error but not length diagnostics.
func (b *AssistantMessageComponent) SetTerminalError(stopReason, errorMessage string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopReason = stopReason
	b.errorMessage = errorMessage
	b.updateContent()
}

// Invalidate rebuilds the content so theme-baked colors follow the active theme (assistant-message.ts invalidate).
func (b *AssistantMessageComponent) Invalidate() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Container.Invalidate()
	b.updateContent()
}

// Render adds the OSC 133 zone boundaries around non-tool-call messages. Empty content with no terminal diagnostic has no rows.
func (b *AssistantMessageComponent) Render(width int) []string {
	if width < 1 {
		width = 1
	}
	// b.mu serializes the render against the mutators off the owner goroutine;
	// it precedes every container lock (see the mu field comment).
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.renderCopy(width)
	if b.hasToolCalls || len(out) == 0 {
		return out
	}
	out[0] = assistantZoneStart + out[0]
	out[len(out)-1] = assistantZoneEnd + assistantZoneFinal + out[len(out)-1]
	return out
}

// updateContent rebuilds the content container from the retained Markdown segments and the terminal state (assistant-message.ts updateContent).
// The spacers, the thinking runs' mouse regions and the diagnostic lines are kept across updates while what they show is unchanged, so a
// streamed delta neither allocates them again nor makes the container render them again. b.mu must be held.
func (b *AssistantMessageComponent) updateContent() {
	children := b.childScratch[:0]
	if len(b.segments) > 0 {
		children = append(children, b.spacer(0))
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
		hidden := b.thinkingRunHidden(run)
		if hidden && b.finished {
			seg.md.dropStreamingMemos() // a run hidden after FinishStreaming does not render its Markdown again
		}
		children = append(children, b.thinkingRun(run, seg.md, hidden))
		run++
		// Upstream adds a spacer after a thinking run only when visible content follows it.
		if i+1 < len(b.segments) {
			children = append(children, b.spacer(run))
		}
	}
	children = append(children, b.terminalErrorChildren()...)
	b.mountedRuns = run
	// SetChildren copies the slice, so handing it the scratch buffer keeps no
	// alias the next update could mutate through.
	b.contentContainer.SetChildren(children...)
	clear(children)
	b.childScratch = children[:0]
	b.Container.Invalidate()
}

// HandleMouse replaces the inherited Container.HandleMouse with the same hit
// contract — the event's self-reported bounds decide the layout width and the
// forwarded coordinates — while b.mu serializes the layout read and any
// measurement against the block's mutators: measuring renders each child's
// Markdown, and off-lock that would race the mutators and the content
// container's own layout writes.
//
// The lock splits the call in two. Under b.mu the call copies the rendered
// child layout when the cache matches the event width, or measures the
// children at event.Width; the dispatch then runs unlocked, because the
// thinking-run click callback re-enters b.mu to toggle the run and sync.Mutex
// is not reentrant. A snapshot taken under a released b.mu can describe
// children updateContent replaced since; those are handled the way upstream
// handles any layout drift — the click may land on a child that no longer
// draws, and a stale thinking region's callback validates its run under b.mu
// before toggling.
func (b *AssistantMessageComponent) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	if event.Y < 0 || event.Y >= event.Height {
		return nil
	}
	children := func() []mouseChild {
		b.mu.Lock()
		defer b.mu.Unlock()
		var children []mouseChild
		b.contentContainer.mu.RLock()
		if b.contentContainer.layoutValid && b.contentContainer.layoutWidth == event.Width {
			children = make([]mouseChild, len(b.contentContainer.builtKids))
			top := 0
			for i, child := range b.contentContainer.builtKids {
				children[i] = mouseChild{component: child, height: b.contentContainer.builtEnds[i] - top}
				top = b.contentContainer.builtEnds[i]
			}
		}
		components := append([]Component(nil), b.contentContainer.children...)
		b.contentContainer.mu.RUnlock()
		if children == nil {
			// Stale or width-mismatched layout cache: measure at the event's
			// own width, as Container.HandleMouse does, while b.mu still
			// serializes the render against the mutators.
			children = measureMouseChildren(components, event.Width)
		}
		return children
	}()
	// dispatchToStackedChild may invoke the thinking-run click callback,
	// which takes b.mu; it must run unlocked.
	return dispatchToStackedChild(children, event, event.X, event.Y, event.Width)
}

// spacer is the block's i-th one-line spacer: 0 leads the content, i > 0 follows thinking run i-1.
func (b *AssistantMessageComponent) spacer(i int) *Spacer {
	for len(b.spacers) <= i {
		b.spacers = append(b.spacers, NewSpacer(1))
	}
	return b.spacers[i]
}

// thinkingRunRegion is the clickable component of one thinking run, kept while the run shows the same component.
type thinkingRunRegion struct {
	md         *Markdown
	hidden     bool
	hiddenText string
	outputPad  int
	region     *MouseRegion
}

// thinkingRun is the mouse region of thinking run index: its Markdown, or the hidden-thinking label when hidden. A click toggles the
// run's visibility. b.mu must be held.
func (b *AssistantMessageComponent) thinkingRun(index int, md *Markdown, hidden bool) *MouseRegion {
	for len(b.thinkingRuns) <= index {
		b.thinkingRuns = append(b.thinkingRuns, thinkingRunRegion{})
	}
	kept := &b.thinkingRuns[index]
	hiddenText := ""
	if hidden {
		hiddenText = "\x1b[3m" + ActiveTheme().ThinkingText + b.hiddenLabel + FgClose(ActiveTheme().ThinkingText) + SGRItalicReset
	} else {
		md.SetDefaultColor(ActiveTheme().ThinkingText)
	}
	if kept.region != nil && kept.md == md && kept.hidden == hidden && kept.hiddenText == hiddenText && kept.outputPad == b.outputPad {
		return kept.region
	}
	var thinking Component = md
	if hidden {
		thinking = NewPaddedText(hiddenText, b.outputPad, 0, nil)
	}
	runIndex := index
	var region *MouseRegion
	region = NewMouseRegion(thinking, func(event TuiMouseEvent) *TuiMouseEventResult {
		if event.Type != MouseClick || event.Button != MouseButtonLeft {
			return nil
		}
		// The click can arrive on a goroutine that is not holding b.mu; take it
		// for the same mutation window every other entry serializes through.
		b.mu.Lock()
		defer b.mu.Unlock()
		// The region this callback belongs to may be stale: a later update can
		// have replaced thinkingRuns[runIndex].region or shrunk the run list.
		// Only a click on the region currently mounted at runIndex toggles it;
		// a stale one reports the click handled without touching state, the
		// same outcome the miss branch produces.
		if runIndex < b.mountedRuns && runIndex < len(b.thinkingRuns) && b.thinkingRuns[runIndex].region == region {
			if b.thinkingVisibilityOverrides == nil {
				b.thinkingVisibilityOverrides = make(map[int]bool)
			}
			b.thinkingVisibilityOverrides[runIndex] = !hidden
			b.updateContent()
		}
		return &TuiMouseEventResult{Handled: true}
	})
	*kept = thinkingRunRegion{md: md, hidden: hidden, hiddenText: hiddenText, outputPad: b.outputPad, region: region}
	return kept.region
}

// hasTerminalError reports whether a length/error/abort line renders.
func (b *AssistantMessageComponent) hasTerminalError() bool {
	return b.stopReason == "length" || (!b.hasToolCalls && (b.stopReason == "error" || b.stopReason == "aborted"))
}

// terminalErrorChildren is the length/error/abort section after content, kept while its line is unchanged.
func (b *AssistantMessageComponent) terminalErrorChildren() []Component {
	if !b.hasTerminalError() {
		return nil
	}
	text := b.terminalErrorText()
	if b.terminalError == nil || b.terminalErrorLine != text || b.terminalErrorPad != b.outputPad {
		b.terminalError = []Component{NewSpacer(1), NewPaddedText(text, b.outputPad, 0, nil)}
		b.terminalErrorLine, b.terminalErrorPad = text, b.outputPad
	}
	return b.terminalError
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
