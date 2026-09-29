// Ports packages/coding-agent/src/experimental/client-tui-chat.ts.
package experimental

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

var experimentalToolRenderers = codingagent.CreateAllToolRenderers()

// ExperimentalChatView is a snapshot-driven transcript. Apply and RefreshTheme run on the presentation owner loop. After detaching the view from that loop, Dispose cancels and joins its timers, image conversions and tool-renderer work.
type ExperimentalChatView struct {
	Transcript       *tui.Container
	PendingMessages  *tui.Container
	Status           *tui.Container
	ctx              context.Context
	cancel           context.CancelFunc
	requestRender    func()
	runOnMain        func(context.Context, func()) error
	cwd              string
	tools            map[string]*codingagent.ToolRendererCard
	renderedEntryIds []string
	streaming        *tui.AssistantMessageBlock
	indicator        *tui.Loader
	stopIndicator    context.CancelFunc
	working          bool
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
		tools: make(map[string]*codingagent.ToolRendererCard),
	}
}

func (view *ExperimentalChatView) theme() *tui.Theme { return tui.ActiveTheme() }

func userMessageText(message agent.AgentMessage) string {
	if message.User == nil {
		return ""
	}
	switch content := message.User.Content.(type) {
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

// Apply appends new entries, reuses streaming assistant/tool components, replaces queues, and tracks operation liveness. Only a changed entry-ID prefix rebases the retained transcript.
func (view *ExperimentalChatView) Apply(snapshot *agentharness.LaneSnapshot) error {
	if err := view.ctx.Err(); err != nil {
		return err
	}
	if snapshot == nil {
		return errors.New("Transcript has no initialized snapshot")
	}
	if err := view.syncTranscript(snapshot.Transcript); err != nil {
		return err
	}
	if operation := snapshot.Operation; operation != nil {
		if err := view.syncStreaming(operation.StreamingMessage); err != nil {
			return err
		}
		for _, tool := range operation.RunningTools {
			card, err := view.tool(tool.ToolName, tool.ToolCallID, tool.Args, true)
			if err != nil {
				return err
			}
			if tool.Status == "running" {
				card.Component.MarkExecutionStarted()
			}
			if tool.Result != nil {
				view.updateResult(tool.ToolCallID, card, agent.AgentToolResult{Content: tool.Result.Content, Details: tool.Result.Details, IsError: tool.Status != "running" && tool.IsError}, tool.Status == "running")
			}
		}
	}
	view.syncQueues(snapshot.Queues)
	view.setWorking(snapshot.Operation != nil)
	view.Transcript.Invalidate()
	view.PendingMessages.Invalidate()
	view.Status.Invalidate()
	return nil
}

// RefreshTheme replaces themed components while retaining the supplied snapshot as the single source of transcript state.
func (view *ExperimentalChatView) RefreshTheme(snapshot *agentharness.LaneSnapshot) error {
	view.setWorking(false)
	view.Transcript.Clear()
	view.PendingMessages.Clear()
	view.Status.Clear()
	view.retireTools()
	view.renderedEntryIds = nil
	view.streaming = nil
	return view.Apply(snapshot)
}

// Dispose joins background work after the caller has detached all view callbacks and rendering. Repeated calls return the same result.
func (view *ExperimentalChatView) Dispose() error {
	view.disposeOnce.Do(func() {
		view.cancel()
		for _, card := range view.tools {
			card.Dispose()
		}
		view.tasks.Wait()
		view.errorsMu.Lock()
		view.disposeError = errors.Join(view.failures...)
		view.errorsMu.Unlock()
	})
	return view.disposeError
}

func (view *ExperimentalChatView) retireTools() {
	retired := view.tools
	view.tools = make(map[string]*codingagent.ToolRendererCard)
	view.tasks.Go(func() {
		for _, card := range retired {
			card.Dispose()
		}
	})
}

func (view *ExperimentalChatView) syncQueues(queues []agentharness.LaneQueuedItem) {
	view.PendingMessages.Clear()
	for _, item := range queues {
		text := "<" + item.CustomType + ">"
		if item.Type == "message" {
			text = collapseClientQueueWhitespace(userMessageText(item.Message))
		}
		view.PendingMessages.Add(tui.NewPaddedTruncatedText(view.theme().FgText("muted", "["+item.Kind+"] "+text), 1, 0))
	}
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

func (view *ExperimentalChatView) syncTranscript(transcript []session.Entry) error {
	for i, id := range view.renderedEntryIds {
		if i >= len(transcript) || transcript[i].ID != id {
			view.Transcript.Clear()
			view.retireTools()
			view.renderedEntryIds = nil
			view.streaming = nil
			break
		}
	}
	for _, entry := range transcript[len(view.renderedEntryIds):] {
		if err := view.ctx.Err(); err != nil {
			return err
		}
		if err := view.addEntry(entry); err != nil {
			return err
		}
		view.renderedEntryIds = append(view.renderedEntryIds, entry.ID)
	}
	return nil
}

func (view *ExperimentalChatView) addEntry(entry session.Entry) error {
	switch entry.Type {
	case session.EntryTypeCompaction:
		view.addText(view.theme().FgText("muted", fmt.Sprintf("[compaction] compacted from %d tokens", entry.TokensBefore)))
		for _, message := range entry.RetainedTail {
			if err := view.ctx.Err(); err != nil {
				return err
			}
			if err := view.addMessage(message); err != nil {
				return err
			}
		}
	case session.EntryTypeBranchSummary:
		view.addText(view.theme().FgText("muted", "[branch summary]"))
		view.addText(entry.Summary)
	case session.EntryTypeCustom:
		view.addText(view.theme().FgText("muted", "["+entry.CustomType+"]"))
	default:
		return view.addMessage(entry.Message)
	}
	return nil
}

func (view *ExperimentalChatView) addMessage(message agent.AgentMessage) error {
	switch {
	case message.User != nil:
		view.Transcript.Add(tui.NewSpacer(1))
		view.Transcript.Add(tui.NewUserMessageBlock(userMessageText(message)))
	case message.Assistant != nil:
		component := view.streaming
		if component == nil {
			component = tui.NewAssistantMessageBlock(false)
			view.Transcript.Add(component)
		}
		view.streaming = nil
		applyClientAssistant(component, message.Assistant)
		for _, block := range message.Assistant.Content {
			if call, ok := block.(ai.ToolCall); ok {
				card, err := view.tool(call.Name, call.ID, call.Arguments, true)
				if err != nil {
					return err
				}
				card.Component.SetArgsComplete()
			}
		}
	case message.ToolResult != nil:
		result := message.ToolResult
		card, err := view.tool(result.ToolName, result.ToolCallID, nil, false)
		if err != nil {
			return err
		}
		view.updateResult(result.ToolCallID, card, agent.AgentToolResult{Content: result.Content, Details: result.Details, IsError: result.IsError}, false)
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
	if message == nil {
		return nil
	}
	if view.streaming == nil {
		view.streaming = tui.NewAssistantMessageBlock(false)
		view.Transcript.Add(view.streaming)
	}
	applyClientAssistant(view.streaming, message)
	for _, block := range message.Content {
		if call, ok := block.(ai.ToolCall); ok {
			if _, err := view.tool(call.Name, call.ID, call.Arguments, true); err != nil {
				return err
			}
		}
	}
	return nil
}

func (view *ExperimentalChatView) tool(name, id string, args any, supplied bool) (*codingagent.ToolRendererCard, error) {
	if existing := view.tools[id]; existing != nil {
		if supplied {
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

func (view *ExperimentalChatView) setWorking(working bool) {
	if view.working == working {
		return
	}
	view.working = working
	if view.stopIndicator != nil {
		view.stopIndicator()
		view.stopIndicator = nil
	}
	view.indicator = nil
	view.Status.Clear()
	if !working {
		return
	}
	indicator := tui.NewStyledLoader(view.theme().Accent, view.theme().Muted, "Working... (esc to abort)", nil)
	view.indicator = indicator
	view.Status.Add(indicator)
	view.startIndicator(indicator)
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
