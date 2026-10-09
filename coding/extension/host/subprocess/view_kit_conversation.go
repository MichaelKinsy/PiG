package subprocess

// view_kit_conversation.go: the component kit's conversation kinds (D107,
// docs/plan/extension-component-kit.md §2.1). Each renders with the Go port
// PiG's main transcript draws the same Pi component with, and reports the
// nodes the main transcript reports for it to a D91 frontend.

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

// ConversationRenderers are the coding agent's parts of the conversation
// kinds, which the host cannot import.
type ConversationRenderers struct {
	// NewToolCard builds the tool card the main transcript draws for a
	// tool-execution node's constructor arguments; toolDefinition is
	// ViewToolDefinitionBuiltin or ViewToolDefinitionEmpty. requestRender
	// is called, from any goroutine, after the card changed on its own.
	// dispose cancels and joins the card's background work.
	NewToolCard func(ctx context.Context, toolName, toolCallID, cwd string, args json.RawMessage, toolDefinition string, requestRender func()) (card *tui.ToolExecutionComponent, dispose func())
	// ToolDiff is the diff a finished call's result carries, as the main
	// transcript reports it to a frontend.
	ToolDiff func(toolName string, arguments map[string]any, result any) *frontend.Diff
}

var conversationRenderers atomic.Pointer[ConversationRenderers]

// SetConversationRenderers installs the coding agent's tool cards. Without
// them a view with a tool-execution node is invalid.
func SetConversationRenderers(renderers ConversationRenderers) {
	conversationRenderers.Store(&renderers)
}

// viewStopReasons are upstream StopReason's values.
var viewStopReasons = []string{"stop", "length", "toolUse", "error", "aborted"}

// viewConversation is a conversation node of the frame, whose transcript
// nodes FrontendView reports.
type viewConversation struct {
	component tui.Component
	node      *frontend.ViewNode
	streaming bool
}

// checkConversation validates the conversation kinds' fields.
func (c *viewChecker) checkConversation(n *ViewNode) {
	if p := n.OutputPad; p != nil && *p != 0 && *p != 1 {
		c.fail("%s outputPad %d is not 0 or 1", n.Kind, *p)
	}
	switch n.Kind {
	case ViewKindAssistantMessage:
		m := n.AssistantMessage
		if m == nil {
			return
		}
		if len(m.Content) > viewMaxItems {
			c.fail("assistant-message has more than %d content blocks", viewMaxItems)
		}
		for _, block := range m.Content {
			if !slices.Contains([]string{"text", "thinking", "toolCall"}, block.Type) {
				c.fail("assistant-message content block type %q is not text, thinking or toolCall", block.Type)
			}
		}
		if m.StopReason != "" && !slices.Contains(viewStopReasons, m.StopReason) {
			c.fail("unknown stopReason %q", m.StopReason)
		}
	case ViewKindToolExecution:
		if n.ToolName == "" {
			c.fail("tool-execution needs a toolName")
		}
		if n.ToolDefinition != "" && n.ToolDefinition != ViewToolDefinitionBuiltin && n.ToolDefinition != ViewToolDefinitionEmpty {
			c.fail("unknown toolDefinition %q", n.ToolDefinition)
		}
		if n.ImageWidthCells != nil && *n.ImageWidthCells < 1 {
			c.fail("tool-execution imageWidthCells %d is under 1", *n.ImageWidthCells)
		}
		if conversationRenderers.Load() == nil {
			c.fail("tool-execution needs the coding agent's tool cards")
		}
		r := n.Result
		if r == nil {
			return
		}
		if len(r.Content) > viewMaxItems {
			c.fail("tool result has more than %d content blocks", viewMaxItems)
		}
		for _, block := range r.Content {
			switch block.Type {
			case "text":
			case "image":
				if block.Ref == "" || block.MimeType == "" {
					c.fail("tool result image needs ref and mimeType")
				}
				c.refs = append(c.refs, block.Ref)
			default:
				c.fail("tool result content block type %q is not text or image", block.Type)
			}
		}
	}
}

// conversationInstance returns the kept instance of an id'd node whose
// constructor key is ctor and that reuse accepts given the node its sender
// sent last, or a fresh one. A node without an id is fresh every frame, as
// `new …Component` is in Pi; its component goes to dispose with the frame.
func (b *viewBuilder) conversationInstance(n *ViewNode, ctor []byte, reuse func(sent *ViewNode) bool) (inst *viewInstance, fresh bool) {
	if n.ID != "" {
		if inst = b.s.instances[n.ID]; inst != nil && inst.kind == n.Kind && bytes.Equal(inst.ctorKey, ctor) && (reuse == nil || reuse(inst.sent)) {
			inst.used = true
			return inst, false
		}
		if inst != nil && inst.dispose != nil {
			b.disposers = append(b.disposers, inst.dispose)
		}
	}
	inst = &viewInstance{id: n.ID, kind: n.Kind, ctorKey: ctor, used: true}
	if n.ID != "" {
		b.s.instances[n.ID] = inst
	}
	return inst, true
}

// sent records n as the values its sender sent last.
func (inst *viewInstance) remember(n *ViewNode) {
	sent := *n
	inst.sent = &sent
}

func (b *viewBuilder) userMessage(n *ViewNode, out *frontend.ViewNode) tui.Component {
	block := tui.NewUserMessageComponent(n.Text, nil, intOr(n.OutputPad, 1), nil)
	out.Text = n.Text
	b.f.conversations = append(b.f.conversations, &viewConversation{component: block, node: out})
	return block
}

func (b *viewBuilder) assistantMessage(n *ViewNode, out *frontend.ViewNode) tui.Component {
	inst, fresh := b.conversationInstance(n, nil, nil)
	if fresh {
		inst.assistant = tui.NewAssistantMessageComponent(nil, n.HideThinkingBlock, nil, "", nil, nil)
	}
	block, sent := inst.assistant, inst.sent
	if !fresh && sent.HideThinkingBlock != n.HideThinkingBlock {
		block.SetHideThinkingBlock(n.HideThinkingBlock)
	}
	label := "Thinking..."
	if n.HiddenThinkingLabel != nil {
		label = *n.HiddenThinkingLabel
	}
	if fresh || !equalStringPtr(sent.HiddenThinkingLabel, n.HiddenThinkingLabel) {
		block.SetHiddenThinkingLabel(label)
	}
	if fresh || intOr(sent.OutputPad, 1) != intOr(n.OutputPad, 1) {
		block.SetOutputPad(intOr(n.OutputPad, 1))
	}
	if fresh || !equalAssistantMessage(sent.AssistantMessage, n.AssistantMessage) || sent.IsStreaming != n.IsStreaming {
		updateAssistantContent(block, n.AssistantMessage)
	}
	inst.remember(n)
	b.f.conversations = append(b.f.conversations, &viewConversation{component: block, node: out, streaming: n.IsStreaming})
	return block
}

// updateAssistantContent is upstream updateContent(message): text and
// thinking blocks in order, a tool call as an invisible boundary that also
// leaves abort and error lines to the tool cards, and the stop reason.
func updateAssistantContent(block *tui.AssistantMessageComponent, message *ViewAssistantMessage) {
	if message == nil {
		block.SetContent(nil)
		block.SetHasToolCalls(false)
		block.SetTerminalError("", "")
		return
	}
	segments := make([]tui.AssistantSegment, 0, len(message.Content))
	hasToolCalls := false
	for _, content := range message.Content {
		switch content.Type {
		case "text":
			segments = append(segments, tui.AssistantSegment{Text: content.Text})
		case "thinking":
			segments = append(segments, tui.AssistantSegment{Thinking: true, Text: content.Thinking})
		case "toolCall":
			hasToolCalls = true
			segments = append(segments, tui.AssistantSegment{})
		}
	}
	stopReason := message.StopReason
	// pig additive (D107): the kit wire's stopReason defaults to "stop"
	// (spec §2.1 table); a node describes a finished message, not a stream.
	if stopReason == "" {
		stopReason = "stop"
	}
	block.SetContent(segments)
	block.SetHasToolCalls(hasToolCalls)
	block.SetTerminalError(stopReason, message.ErrorMessage)
}

func equalStringPtr(a, b *string) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}

func equalAssistantMessage(a, b *ViewAssistantMessage) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || (a.StopReason == b.StopReason && a.ErrorMessage == b.ErrorMessage && slices.Equal(a.Content, b.Content))
}

func (b *viewBuilder) toolExecution(n *ViewNode, out *frontend.ViewNode) tui.Component {
	renderers := conversationRenderers.Load()
	definition := n.ToolDefinition
	if definition == "" {
		definition = ViewToolDefinitionBuiltin
	}
	args := n.Args
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	ctor, _ := json.Marshal([]string{n.ToolName, n.ToolCallID, definition, n.Cwd})
	// Upstream's executionStarted and argsComplete only become true; a
	// sender that turns one back off describes a new component.
	inst, fresh := b.conversationInstance(n, ctor, func(sent *ViewNode) bool {
		return (!sent.ExecutionStarted || n.ExecutionStarted) && (!sent.ArgsComplete || n.ArgsComplete)
	})
	if fresh {
		s := b.s
		inst.tool, inst.dispose = renderers.NewToolCard(context.Background(), n.ToolName, n.ToolCallID, n.Cwd, args, definition, s.requestCardRender)
		if inst.id == "" {
			b.disposers = append(b.disposers, inst.dispose)
		}
	}
	card, sent := inst.tool, inst.sent
	if !fresh && !bytes.Equal(sent.Args, n.Args) {
		card.SetDefinitionArgs(args)
	}
	if fresh || !bytes.Equal(sent.Args, n.Args) {
		// The header and arguments a frontend's tool node shows, as the
		// main transcript records them.
		card.ArgsPreview = tui.HeaderForTool(n.ToolName, args, n.Cwd)
		card.SetHeaderArgs(args)
	}
	if n.ArgsComplete && (fresh || !sent.ArgsComplete) {
		card.SetArgsComplete()
	}
	if n.ExecutionStarted && (fresh || !sent.ExecutionStarted) {
		card.MarkExecutionStarted()
	}
	showImages := n.ShowImages == nil || *n.ShowImages
	if fresh && !showImages || !fresh && showImages != (sent.ShowImages == nil || *sent.ShowImages) {
		card.SetShowImages(showImages)
	}
	if width := intOr(n.ImageWidthCells, 60); fresh && width != 60 || !fresh && width != intOr(sent.ImageWidthCells, 60) {
		card.SetImageWidthCells(width)
	}
	partial := n.IsPartial == nil || *n.IsPartial
	if n.Result != nil && (fresh || !equalToolResult(sent.Result, n.Result) || partial != (sent.IsPartial == nil || *sent.IsPartial)) {
		b.updateToolResult(card, n.Result, partial)
	}
	if fresh && n.Expanded || !fresh && n.Expanded != sent.Expanded {
		card.SetExpanded(n.Expanded)
	}
	inst.remember(n)
	b.f.conversations = append(b.f.conversations, &viewConversation{component: card, node: out})
	return card
}

// updateToolResult is upstream updateResult(result, isPartial).
func (b *viewBuilder) updateToolResult(card *tui.ToolExecutionComponent, result *ViewToolResult, partial bool) {
	value := agent.AgentToolResult{IsError: result.IsError}
	if len(result.Details) > 0 {
		var details any
		if json.Unmarshal(result.Details, &details) == nil {
			value.Details = details
		}
	}
	card.ImageBlocks = nil
	for _, block := range result.Content {
		switch block.Type {
		case "text":
			value.Content = append(value.Content, ai.TextContent{Text: block.Text})
		case "image":
			img := b.f.images[block.Ref]
			value.Content = append(value.Content, ai.ImageContent{Data: img.b64, MimeType: block.MimeType})
			card.ImageBlocks = append(card.ImageBlocks, tui.ImageBlock{Data: img.b64, MIMEType: block.MimeType})
		}
	}
	card.SetResultValue(value)
	if partial {
		card.SetStreaming(value.Text())
	} else {
		card.SetResult(value.Text(), value.IsError, 0)
	}
}

func equalToolResult(a, b *ViewToolResult) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || (a.IsError == b.IsError && bytes.Equal(a.Details, b.Details) && slices.Equal(a.Content, b.Content))
}

func (b *viewBuilder) bashExecution(n *ViewNode, out *frontend.ViewNode) tui.Component {
	ctor, _ := json.Marshal([]any{n.Command, n.ExcludeFromContext})
	// Output only grows and a completed command does not run again; a
	// sender that sends otherwise describes a new component.
	inst, fresh := b.conversationInstance(n, ctor, func(sent *ViewNode) bool {
		return strings.HasPrefix(n.Output, sent.Output) && (sent.Complete == nil || n.Complete != nil)
	})
	if fresh {
		// The kit drives the spinner from the frame's Frame and has no outputPad
		// field yet, so the block takes Pi's default of 1 (F7).
		inst.bash = tui.NewBashExecutionComponent(n.Command, nil, n.ExcludeFromContext, 1)
	}
	block, sent := inst.bash, inst.sent
	appended := n.Output
	if !fresh {
		appended = n.Output[len(sent.Output):]
	}
	if appended != "" {
		block.AppendOutput(appended)
	}
	if fresh && n.Expanded || !fresh && n.Expanded != sent.Expanded {
		block.SetExpanded(n.Expanded)
	}
	if c := n.Complete; c != nil && (fresh || sent.Complete == nil || !equalBashComplete(*sent.Complete, *c)) {
		// Pi's setComplete without a truncation result keeps the output the block shows.
		block.SetCompleteWithOutput(c.ExitCode, c.Cancelled, c.Truncated, block.GetOutput(), c.FullOutputPath)
	}
	if loader := block.Loader(); loader != nil {
		if n.Frame != nil && len(loader.Frames) > 0 && (fresh || sent.Frame == nil || *sent.Frame != *n.Frame) {
			loader.Frame = *n.Frame % len(loader.Frames)
		}
		// upstream: packages/tui/src/components/loader.ts DEFAULT_INTERVAL_MS
		b.f.loaders = append(b.f.loaders, &viewLoader{loader: loader, interval: 80 * time.Millisecond})
	}
	inst.remember(n)
	return block
}

func equalBashComplete(a, b ViewBashComplete) bool {
	return equalIntPtr(a.ExitCode, b.ExitCode) && a.Cancelled == b.Cancelled && a.Truncated == b.Truncated && a.FullOutputPath == b.FullOutputPath
}

func equalIntPtr(a, b *int) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}

func (b *viewBuilder) diff(n *ViewNode, out *frontend.ViewNode) tui.Component {
	out.PaddingX, out.PaddingY = intOr(n.PaddingX, 0), intOr(n.PaddingY, 0)
	out.Diff = &frontend.Diff{Path: n.FilePath, Text: n.Diff}
	return tui.NewPaddedText(tui.RenderDiff(n.Diff), out.PaddingX, out.PaddingY, nil)
}

// requestCardRender is a tool card's requestRender: it may run on the card's
// own goroutines, and inside a render under s.mu, so it takes no lock.
func (s *viewSurface) requestCardRender() {
	s.cardChanged.Store(true)
	if s.repaint != nil {
		s.repaint()
	}
}

// refreshConversationsLocked reports each conversation node's transcript
// nodes at the width it was laid out at.
func (f *viewFrame) refreshConversationsLocked() {
	var toolDiff func(string, map[string]any, any) *frontend.Diff
	if renderers := conversationRenderers.Load(); renderers != nil {
		toolDiff = renderers.ToolDiff
	}
	for _, c := range f.conversations {
		c.node.Transcript = tui.TranscriptNodes(c.component, c.node.Width, c.streaming, toolDiff)
	}
}
