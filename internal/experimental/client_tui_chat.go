// Ports packages/coding-agent/src/experimental/client-tui-chat.ts.
package experimental

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

var experimentalToolRenderers = codingagent.CreateAllToolRenderers()

// LiveOf is the pi.live document of a view: the active run, the streaming answer, and running tools. An absent document is an empty one.
func LiveOf(view services.ConversationView) (harness.LiveState, error) {
	var live harness.LiveState
	return live, decodeDocument(view, services.LiveDocKind, &live)
}

// decodeDocument decodes a built-in document of a view into out; an absent document leaves out empty.
func decodeDocument(view services.ConversationView, kind string, out any) error {
	document, present := view.Docs[kind]
	if !present {
		return nil
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, out)
}

// ExperimentalChatView renders the root conversation's durable view for the service-only experimental presentation. Apply and RefreshTheme run on the presentation owner loop. After detaching the view from that loop, Dispose cancels and joins its timers, image conversions and tool-renderer work.
type ExperimentalChatView struct {
	Transcript      *tui.Container
	PendingMessages *tui.Container
	Status          *tui.Container
	ctx             context.Context
	cancel          context.CancelFunc
	requestRender   func()
	runOnMain       func(context.Context, func()) error
	cwd             string
	// tools holds the newest card per call ID; provider call IDs may repeat across turns.
	tools map[string]*codingagent.ToolRendererCard
	// cards holds every card shown, also older ones whose call ID a later turn reused.
	cards []*codingagent.ToolRendererCard
	// streamingCalls holds the call IDs whose cards the streaming answer created; its entry takes them over.
	streamingCalls   map[string]struct{}
	renderedEntryIds []durable.EntryId
	streaming        *tui.AssistantMessageBlock
	indicator        *tui.Loader
	stopIndicator    context.CancelFunc
	statusText       string
	tasks            sync.WaitGroup
	errorsMu         sync.Mutex
	failures         []error
	disposeOnce      sync.Once
	disposeError     error
}

// NewExperimentalChatView creates the native message and tool containers without reading Session history.
func NewExperimentalChatView(ctx context.Context, cwd string, requestRender func(), runOnMain func(context.Context, func()) error) *ExperimentalChatView {
	lifetime, cancel := context.WithCancel(ctx)
	return &ExperimentalChatView{
		Transcript: tui.NewContainer(), PendingMessages: tui.NewContainer(), Status: tui.NewContainer(),
		ctx: lifetime, cancel: cancel, cwd: cwd, requestRender: requestRender, runOnMain: runOnMain,
		tools: make(map[string]*codingagent.ToolRendererCard), streamingCalls: make(map[string]struct{}),
	}
}

func (view *ExperimentalChatView) theme() *tui.Theme { return tui.ActiveTheme() }

// decodeEntryMessage is the entry's first model message, or nil when it has none.
func decodeEntryMessage(entry durable.EntryRecord) (*agent.AgentMessage, error) {
	if len(entry.Model) == 0 {
		return nil, nil
	}
	return agentMessageOf(entry.Model[0])
}

// agentMessageOf converts a pi-ai message to the agent's message through its JSON form.
func agentMessageOf(value any) (*agent.AgentMessage, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var message agent.AgentMessage
	if err := json.Unmarshal(encoded, &message); err != nil {
		return nil, err
	}
	return &message, nil
}

func userContentText(content ai.UserContent) string {
	switch content := content.(type) {
	case ai.UserText:
		return string(content)
	case ai.UserContentBlocks:
		var text strings.Builder
		for _, block := range content {
			if block, ok := block.(ai.TextContent); ok {
				text.WriteString(block.Text)
			}
		}
		return text.String()
	}
	return ""
}

func userMessageText(message agent.AgentMessage) string {
	if message.User == nil {
		return ""
	}
	return userContentText(message.User.Content)
}

// Apply appends new entries, reuses streaming assistant/tool components, replaces the queue, and tracks the status line. Only a changed entry-ID prefix, or a streaming answer whose partial disappeared, rebuilds the retained transcript.
func (view *ExperimentalChatView) Apply(conversation services.ConversationView) error {
	if err := view.ctx.Err(); err != nil {
		return err
	}
	live, err := LiveOf(conversation)
	if err != nil {
		return err
	}
	if err := view.syncTranscript(conversation.Entries); err != nil {
		return err
	}
	var partial *agent.AssistantMessage
	if generation := live.Generation; generation != nil && len(generation.Message) != 0 {
		message, err := agentMessageOf(generation.Message)
		if err != nil {
			return err
		}
		partial = message.Assistant
	}
	// A partial without its entry was dropped, for example by a retry: render the transcript again.
	if partial == nil && view.streaming != nil {
		if err := view.rebuild(conversation.Entries); err != nil {
			return err
		}
	}
	if partial != nil {
		if err := view.syncStreaming(partial); err != nil {
			return err
		}
	}
	for _, slot := range live.Tools {
		if slot.Status == harness.ToolSlotPending {
			continue
		}
		card, err := view.tool(slot.Name, slot.CallId, nil, false, false)
		if err != nil {
			return err
		}
		card.Component.SetArgsComplete()
		if slot.Status != harness.ToolSlotRunning {
			continue
		}
		card.Component.MarkExecutionStarted()
		if slot.Output != nil {
			var details any
			if slot.Details != nil {
				details = *slot.Details
			}
			view.updateResult(slot.CallId, card, agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: *slot.Output}}, Details: details}, true)
		}
	}
	var inbox harness.InboxState
	if err := decodeDocument(conversation, services.InboxDocKind, &inbox); err != nil {
		return err
	}
	if err := view.syncQueue(inbox); err != nil {
		return err
	}
	view.syncStatus(live)
	view.Transcript.Invalidate()
	view.PendingMessages.Invalidate()
	view.Status.Invalidate()
	return nil
}

// RefreshTheme replaces themed components while retaining the supplied view as the single source of transcript state.
func (view *ExperimentalChatView) RefreshTheme(conversation services.ConversationView) error {
	view.stopStatusIndicator()
	view.statusText = ""
	view.Status.Clear()
	if err := view.rebuild(conversation.Entries); err != nil {
		return err
	}
	return view.Apply(conversation)
}

// Dispose joins background work after the caller has detached all view callbacks and rendering. Repeated calls return the same result.
func (view *ExperimentalChatView) Dispose() error {
	view.disposeOnce.Do(func() {
		view.cancel()
		view.discardTools()
		view.tasks.Wait()
		view.errorsMu.Lock()
		view.disposeError = errors.Join(view.failures...)
		view.errorsMu.Unlock()
	})
	return view.disposeError
}

// discardTools finishes every card: a running bash card keeps a timer until it gets a final result.
func (view *ExperimentalChatView) discardTools() {
	cards := view.cards
	view.cards = nil
	view.tools = make(map[string]*codingagent.ToolRendererCard)
	clear(view.streamingCalls)
	for _, card := range cards {
		card.Component.SetResultValue(agent.AgentToolResult{})
		card.Component.ImageBlocks = nil
		card.Component.SetResult("", false, 0)
	}
	view.tasks.Go(func() {
		for _, card := range cards {
			card.Dispose()
		}
	})
}

func (view *ExperimentalChatView) syncQueue(inbox harness.InboxState) error {
	view.PendingMessages.Clear()
	for _, item := range inbox.Items {
		var text string
		if item.Mode == harness.InboxWrite {
			kind, _ := item.Entry["kind"].(string)
			text = "<" + kind + ">"
		} else {
			content, err := decodeInboxContent(item.Content)
			if err != nil {
				return err
			}
			text = collapseClientQueueWhitespace(userContentText(content))
		}
		view.PendingMessages.Add(tui.NewPaddedTruncatedText(view.theme().FgText("muted", "["+string(item.Mode)+"] "+text), 1, 0))
	}
	return nil
}

// decodeInboxContent decodes a queued user input: a string, or an array of text and image blocks.
func decodeInboxContent(content durable.JsonValue) (ai.UserContent, error) {
	var message agent.AgentMessage
	wrapped, err := json.Marshal(map[string]any{"role": "user", "content": content, "timestamp": 0})
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(wrapped, &message); err != nil {
		return nil, err
	}
	if message.User == nil {
		return nil, errors.New("Inbox content is not user content")
	}
	return message.User.Content, nil
}

func collapseClientQueueWhitespace(text string) string {
	var result strings.Builder
	space := false
	for len(text) > 0 {
		char, size := utf8.DecodeRuneInString(text)
		if widthx.IsJSSpace(char) {
			if !space {
				result.WriteByte(' ')
			}
			space = true
		} else {
			result.WriteString(text[:size])
			space = false
		}
		text = text[size:]
	}
	return result.String()
}

// syncStatus shows what the conversation waits for: a retry, a deferred response, a compaction, a running tool, or a plain working run.
func (view *ExperimentalChatView) syncStatus(live harness.LiveState) {
	text := ""
	switch {
	case live.Generation != nil && live.Generation.Retry != nil:
		text = "Retrying (attempt " + itoa(live.Generation.Attempt+1) + "): " + live.Generation.Retry.Error
	case live.Generation != nil && live.Generation.Deferred != nil:
		text = "Waiting for deferred response..."
	case len(live.Compactions) > 0:
		compaction := live.Compactions[0]
		if compaction.Retry != nil {
			text = "Retrying " + string(compaction.Reason) + " compaction (attempt " + itoa(compaction.Attempt+1) + ")..."
		} else {
			text = "Compacting (" + string(compaction.Reason) + ")..."
		}
	case runningToolName(live) != "":
		text = "Running " + runningToolName(live) + "... (esc to abort)"
	case live.Run != nil:
		text = "Working... (esc to abort)"
	}
	if text == view.statusText {
		return
	}
	view.statusText = text
	view.stopStatusIndicator()
	view.Status.Clear()
	if text == "" {
		return
	}
	indicator := tui.NewStyledLoader(view.theme().Accent, view.theme().Muted, text, nil)
	view.indicator = indicator
	view.Status.Add(indicator)
	view.startIndicator(indicator)
}

func runningToolName(live harness.LiveState) string {
	for _, slot := range live.Tools {
		if slot.Status == harness.ToolSlotRunning {
			return slot.Name
		}
	}
	return ""
}

func (view *ExperimentalChatView) stopStatusIndicator() {
	if view.stopIndicator != nil {
		view.stopIndicator()
		view.stopIndicator = nil
	}
	view.indicator = nil
}

func (view *ExperimentalChatView) syncTranscript(entries []durable.EntryRecord) error {
	// Compaction and resets replace the head of the active transcript.
	for i, id := range view.renderedEntryIds {
		if i >= len(entries) || entries[i].Id != id {
			if err := view.rebuild(entries); err != nil {
				return err
			}
			return nil
		}
	}
	for _, entry := range entries[len(view.renderedEntryIds):] {
		if err := view.ctx.Err(); err != nil {
			return err
		}
		if err := view.addEntry(entry); err != nil {
			return err
		}
		view.renderedEntryIds = append(view.renderedEntryIds, entry.Id)
	}
	return nil
}

func (view *ExperimentalChatView) rebuild(entries []durable.EntryRecord) error {
	view.Transcript.Clear()
	view.discardTools()
	view.renderedEntryIds = nil
	view.streaming = nil
	for _, entry := range entries {
		if err := view.ctx.Err(); err != nil {
			return err
		}
		if err := view.addEntry(entry); err != nil {
			return err
		}
		view.renderedEntryIds = append(view.renderedEntryIds, entry.Id)
	}
	return nil
}

func (view *ExperimentalChatView) addEntry(entry durable.EntryRecord) error {
	message, err := decodeEntryMessage(entry)
	if err != nil {
		return err
	}
	switch {
	case entry.Kind == "pi.user" && message != nil && message.User != nil:
		view.Transcript.Add(tui.NewSpacer(1))
		view.Transcript.Add(tui.NewUserMessageBlock(userMessageText(*message)))
	case entry.Kind == "pi.assistant" && message != nil && message.Assistant != nil:
		component := view.streaming
		if component == nil {
			component = tui.NewAssistantMessageBlock(false)
			view.Transcript.Add(component)
		}
		view.streaming = nil
		applyClientAssistant(component, message.Assistant)
		// Only a tool-calling answer runs its calls; an aborted, failed, or truncated one never does.
		ran := message.Assistant.StopReason == ai.StopReasonToolUse
		for _, block := range message.Assistant.Content {
			call, ok := block.(ai.ToolCall)
			if !ok {
				continue
			}
			_, streamed := view.streamingCalls[call.ID]
			if !ran && !streamed {
				continue
			}
			card, err := view.tool(call.Name, call.ID, call.Arguments, true, !streamed)
			if err != nil {
				return err
			}
			card.Component.SetArgsComplete()
			if !ran {
				view.updateResult(call.ID, card, agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Not run: the answer was interrupted."}}, IsError: true}, false)
			}
		}
		clear(view.streamingCalls)
	case entry.Kind == "pi.tool-result" && message != nil && message.ToolResult != nil:
		result := message.ToolResult
		card, err := view.tool(result.ToolName, result.ToolCallID, nil, false, false)
		if err != nil {
			return err
		}
		view.updateResult(result.ToolCallID, card, result.Result(), false)
	case entry.Kind == "pi.compaction":
		view.addText(view.theme().FgText("muted", "[compaction]"))
		if message != nil && message.User != nil {
			view.addText(userMessageText(*message))
		}
	case entry.Kind == "pi.reset":
		view.addText(view.theme().FgText("muted", "[new context]"))
	}
	return nil
}

func applyClientAssistant(component *tui.AssistantMessageBlock, message *agent.AssistantMessage) {
	segments := make([]tui.AssistantSegment, 0, len(message.Content))
	hasToolCalls := false
	for _, block := range message.Content {
		switch content := block.(type) {
		case ai.TextContent:
			segments = append(segments, tui.AssistantSegment{Text: content.Text})
		case ai.ThinkingContent:
			segments = append(segments, tui.AssistantSegment{Thinking: true, Text: content.Thinking})
		case ai.ToolCall:
			segments = append(segments, tui.AssistantSegment{})
			hasToolCalls = true
		}
	}
	component.SetContent(segments)
	component.SetHasToolCalls(hasToolCalls)
	component.SetTerminalError(string(message.StopReason), message.ErrorMessage)
}

func (view *ExperimentalChatView) syncStreaming(message *agent.AssistantMessage) error {
	if view.streaming == nil {
		view.streaming = tui.NewAssistantMessageBlock(false)
		view.Transcript.Add(view.streaming)
	}
	applyClientAssistant(view.streaming, message)
	for _, block := range message.Content {
		if call, ok := block.(ai.ToolCall); ok {
			_, streamed := view.streamingCalls[call.ID]
			if _, err := view.tool(call.Name, call.ID, call.Arguments, true, !streamed); err != nil {
				return err
			}
			view.streamingCalls[call.ID] = struct{}{}
		}
	}
	return nil
}

// tool is the card of a call. hasArgs updates an existing card's arguments; fresh starts a new card for a call ID an earlier turn used.
func (view *ExperimentalChatView) tool(name, id string, args any, hasArgs, fresh bool) (*codingagent.ToolRendererCard, error) {
	if existing := view.tools[id]; existing != nil && !fresh {
		if hasArgs {
			encoded, err := json.Marshal(args)
			if err != nil {
				return nil, err
			}
			existing.Component.UpdateArgs(name, string(encoded))
		}
		return existing, nil
	}
	if args == nil {
		args = map[string]any{}
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	card := codingagent.NewToolRendererCard(view.ctx, name, id, view.cwd, encoded, experimentalToolRenderers[name], view.requestRender)
	view.Transcript.Add(card.Component)
	view.cards = append(view.cards, card)
	view.tools[id] = card
	return card, nil
}

func (view *ExperimentalChatView) updateResult(id string, card *codingagent.ToolRendererCard, result agent.AgentToolResult, partial bool) {
	component := card.Component
	component.SetResultValue(result)
	component.ImageBlocks = nil
	for _, block := range result.Content {
		if image, ok := block.(ai.ImageContent); ok {
			component.ImageBlocks = append(component.ImageBlocks, tui.ImageBlock{Data: image.Data, MIMEType: image.MimeType})
		}
	}
	if partial {
		component.SetStreaming(result.Text())
	} else {
		component.SetResult(result.Text(), result.IsError, 0)
	}
	requests := component.PendingKittyImageConversions()
	for _, request := range requests {
		view.startImageConversion(id, card, request)
	}
}

func (view *ExperimentalChatView) startImageConversion(id string, card *codingagent.ToolRendererCard, request tui.KittyImageConversion) {
	view.tasks.Go(func() {
		if view.ctx.Err() != nil {
			return
		}
		converted := codingagent.ConvertToPng(request.Data, request.MimeType)
		view.recordTaskError(view.runOnMain(view.ctx, func() {
			if view.tools[id] == card && card.Component.ApplyConvertedImage(request, converted) {
				view.requestRender()
			}
		}))
	})
}

func (view *ExperimentalChatView) addText(text string) {
	view.Transcript.Add(tui.NewSpacer(1))
	view.Transcript.Add(tui.NewPaddedText(text, 1, 0, nil))
}

func (view *ExperimentalChatView) recordTaskError(err error) {
	if err != nil && view.ctx.Err() == nil {
		view.errorsMu.Lock()
		view.failures = append(view.failures, err)
		view.errorsMu.Unlock()
	}
}

func (view *ExperimentalChatView) startIndicator(indicator *tui.Loader) {
	ctx, cancel := context.WithCancel(view.ctx)
	view.stopIndicator = cancel
	view.tasks.Go(func() {
		// upstream: packages/tui/src/components/loader.ts:DEFAULT_INTERVAL_MS
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				err := view.runOnMain(ctx, func() {
					if view.indicator == indicator {
						indicator.Tick()
						view.requestRender()
					}
				})
				if err != nil {
					if ctx.Err() == nil {
						view.recordTaskError(err)
					}
					return
				}
			}
		}
	})
}

func itoa(value int) string { return strconv.Itoa(value) }
