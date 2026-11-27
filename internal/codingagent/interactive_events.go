package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts (handleEvent).

import (
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsonopts"
	"github.com/MichaelKinsy/PiG/tui"
)

type reasoningDropDiagnostic struct {
	provider    string
	droppedType string
	noun        [2]string
}

var reasoningDropDiagnostics = map[string]*reasoningDropDiagnostic{
	"anthropic_input_transformations": {
		provider:    "Anthropic",
		droppedType: "thinking_dropped",
		noun:        [2]string{"thinking block", "thinking blocks"},
	},
	"openai_input_transformations": {
		provider:    "OpenAI",
		droppedType: "reasoning",
		noun:        [2]string{"reasoning item", "reasoning items"},
	},
}

type droppedReasoningState struct {
	diagnostic *reasoningDropDiagnostic
	count      int
}

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts (getDroppedReasoning).
// Returns the matching diagnostic kind, its dropped count, and the index of the diagnostic in message.Diagnostics
// without allocating, so the unchanged path stays zero-allocation.
func countDroppedReasoning(message *agent.AssistantMessage) (droppedReasoningState, int) {
	if message == nil {
		return droppedReasoningState{}, -1
	}
	for i, diagnostic := range message.Diagnostics {
		kind := reasoningDropDiagnostics[diagnostic.Type]
		if kind == nil {
			continue
		}
		count := 0
		switch transformations := diagnostic.Details["transformations"].(type) {
		case []any:
			for _, value := range transformations {
				if transformation, ok := value.(map[string]any); ok && transformation["type"] == kind.droppedType {
					count++
				}
			}
		case []map[string]any:
			for _, transformation := range transformations {
				if transformation["type"] == kind.droppedType {
					count++
				}
			}
		default:
			continue
		}
		if count > 0 {
			return droppedReasoningState{diagnostic: kind, count: count}, i
		}
	}
	return droppedReasoningState{}, -1
}

func collectDroppedReasoningReasons(diagnostic ai.AssistantMessageDiagnostic, droppedType string) []string {
	var seen map[string]struct{}
	var reasons []string
	add := func(transformation map[string]any) {
		if transformation["type"] != droppedType {
			return
		}
		reason, ok := transformation["reason"].(string)
		if !ok {
			return
		}
		if seen == nil {
			seen = make(map[string]struct{})
		}
		if _, dup := seen[reason]; !dup {
			seen[reason] = struct{}{}
			reasons = append(reasons, reason)
		}
	}
	switch transformations := diagnostic.Details["transformations"].(type) {
	case []any:
		for _, value := range transformations {
			if transformation, ok := value.(map[string]any); ok {
				add(transformation)
			}
		}
	case []map[string]any:
		for _, transformation := range transformations {
			add(transformation)
		}
	}
	return reasons
}

// maybeShowThinkingDropNotice compares with the preceding displayed response. Session persistence can overtake queued UI events, so the owner loop keeps this state and branch rebuilds initialize it in their existing history walk.
func (m *InteractiveMode) maybeShowThinkingDropNotice(message *agent.AssistantMessage) {
	if !m.showCacheMissNotices() {
		return
	}
	dropped, diagIdx := countDroppedReasoning(message)
	if dropped.count == 0 {
		return
	}
	if m.previousDroppedReasoning.diagnostic == dropped.diagnostic && dropped.count <= m.previousDroppedReasoning.count {
		return
	}
	noun := dropped.diagnostic.noun[1]
	if dropped.count == 1 {
		noun = dropped.diagnostic.noun[0]
	}
	reasons := ""
	if list := collectDroppedReasoningReasons(message.Diagnostics[diagIdx], dropped.diagnostic.droppedType); len(list) > 0 {
		reasons = ": " + strings.Join(list, ", ")
	}
	text := fmt.Sprintf("%s dropped %d %s%s (details in session)", dropped.diagnostic.provider, dropped.count, noun, reasons)
	m.chatContainer.Add(tui.NewSpacer(1))
	m.chatContainer.Add(themedNotice("warning", text, 1))
}

func (m *InteractiveMode) finalizeRunningTools() {
	m.toolMu.Lock()
	for id, start := range m.toolStarts {
		if comp := m.toolByID[id]; comp != nil {
			comp.FinalizeAborted(time.Since(start))
		}
		delete(m.toolStarts, id)
		delete(m.toolFileCalls, id)
	}
	m.toolMu.Unlock()
}

// fileCallMembers is the members of a read or write call's arguments the file renderers read (toolBodyRendererForCall).
type fileCallMembers struct {
	FilePath fileCallMember `json:"file_path"`
	Path     fileCallMember `json:"path"`
	Content  fileCallMember `json:"content"`
}

// fileCallMember is what the renderers ask of one member: whether it is absent (missing or null), a string, or a value of another
// type. A value of another type is skipped, not built. A repeated member's last value, null included, is the one kept.
type fileCallMember struct {
	kind jsontext.Kind // 0 for a missing member
	text string        // the value of a string member
}

func (member *fileCallMember) UnmarshalJSONFrom(decoder *jsontext.Decoder) error {
	kind := decoder.PeekKind()
	if kind != '"' {
		*member = fileCallMember{kind: kind}
		return decoder.SkipValue()
	}
	token, err := decoder.ReadToken()
	if err != nil {
		return err
	}
	*member = fileCallMember{kind: kind, text: token.String()}
	return nil
}

// otherFileCallArgument stands for a member the model sent as a boolean, a number, an array or an object.
type otherFileCallArgument struct{}

// fileCallArguments reads from a read or write call's arguments the three members the file renderers use and nothing else: a string
// member as its string, a missing or null one as no member, and one of any other type as otherFileCallArgument. Text that is not one
// JSON object gives no members. Member names match exactly, as upstream's property reads on event.args do.
func fileCallArguments(args json.RawMessage) ai.JsonObject {
	var members fileCallMembers
	if err := jsonv2.Unmarshal(args, &members, jsonopts.Parse); err != nil {
		return nil
	}
	arguments := make(ai.JsonObject, 3)
	for _, member := range [...]struct {
		name  string
		value *fileCallMember
	}{{"file_path", &members.FilePath}, {"path", &members.Path}, {"content", &members.Content}} {
		switch member.value.kind {
		case 0, 'n':
		case '"':
			arguments[member.name] = member.value.text
		default:
			arguments[member.name] = otherFileCallArgument{}
		}
	}
	return arguments
}

// handleAgentEvent handles one event from the agent loop for live rendering.
// It runs on the main input goroutine (driven by the inputLoop select on
// m.eventCh), single-threaded with keystroke handling and posted UI tasks, so
// it never races the lock-free TUI component tree. Mirrors upstream's single
// JS event loop where agent events and input are dispatched on one thread.
// Cross-event state (the current assistant block / message / turn index) lives
// on InteractiveMode fields since the handler is now invoked per event.

// handleAgentEvent takes one item of the Session's Events on the owner loop.
//
// Pi's frame is a timer callback (packages/tui/src/tui.ts:1033-1090 requestRender), so it cannot begin between two events that Pi's loop handles in one task: the events of a response the provider delivered in one run, and the message_end, tool_execution_start, turn_end and agent_end the loop emits behind them without awaiting anything (packages/agent/src/agent-loop.ts:262-263, 296-299, 329). The Session marks the bounds of such a task (eventRegionMarker). The loop takes the frame timer and the events from one select, so between the markers it only collects the events, and applies them all in the iteration that takes the closing marker: a frame that falls due meanwhile draws the state before the task, as Pi's timer does when it fires before the task, and the frame after it draws the whole task.
//
// No frame, dialog or input waits for a marker. A producer that waits inside a task delays that task's events only, and a task that closes early is drawn as it was without markers. A task of one message_update, the chunk of a provider that streams, arrives without markers and is applied as it arrives (coding.Session, sessionEventRegions).
//
// Applying a task's events at its close, rather than as they arrive with only the frame timer kept out of the task, makes the task one change of the mode's state: every paint sees all of it or none, the paint after a keystroke and the one a dialog asks for by name included, and a task whose end went unreported would hold back its own events, never the screen. What is collected is kept small (collectEvent).
func (m *InteractiveMode) handleAgentEvent(ev agent.AgentEvent) {
	if m.tuiTornDown {
		// Pi's stop() unsubscribes the mode from its Session before it stops the TUI (interactive-mode.ts stop: this.unsubscribe()), so the events of a run that ends under the quit's dispose reach the extensions and the session file but not the mode, and nothing of the run is drawn after the stop. The Session's flush still waits for its acknowledgement.
		if barrier, ok := ev.(eventBarrier); ok {
			barrier.AcknowledgeEvent()
		}
		return
	}
	if barrier, ok := ev.(eventBarrier); ok {
		if marker, ok := ev.(eventRegionMarker); ok {
			if open, bounds := marker.EventRegion(); bounds {
				m.setEventRegion(open)
				return
			}
		}
		// Every earlier event has now been handled on this loop.
		m.applyEventRegion()
		barrier.AcknowledgeEvent()
		return
	}
	if m.eventRegionOpen {
		m.collectEvent(ev)
		switch ev.(type) {
		case agent.AgentEndEvent, agent.AgentSettledEvent:
			// The run is over, so its task is, whatever became of the closing marker.
			m.setEventRegion(false)
		}
		return
	}
	m.applyAgentEvent(ev)
}

// setEventRegion opens a task's region, or closes it and applies the events collected in it.
func (m *InteractiveMode) setEventRegion(open bool) {
	m.eventRegionOpen = open
	if !open {
		m.applyEventRegion()
	}
}

// The values of InteractiveMode.eventRegionUpdate: the part of the message that the message_update collected last reports.
const (
	// eventRegionNoUpdate: the event collected last is no message_update, or none is collected.
	eventRegionNoUpdate = iota
	// eventRegionMessageUpdate: it reports no tool call.
	eventRegionMessageUpdate
	// eventRegionToolCallUpdate plus a content index: it reports the tool call of that block.
	eventRegionToolCallUpdate
)

// collectEvent keeps ev for the close of the task that runs.
//
// A message_update that follows another of the same part of the message takes its place. Pi applies every message_update of a task before the frame that follows the task, and each is applied from what it carries alone (interactive-mode.ts handleEvent message_update: the message, and for a tool call its block's arguments), so the later of two for one part leaves what both would have left: the message block is set from the later message, and the card of the tool call at one content index is created once and given the later arguments. A response that arrives in one piece is one task of hundreds of updates; held until the close they would be so many messages kept alive for the collector to trace and so many blocks set for one frame. Updates for different tool calls, and the events between them, keep their order.
func (m *InteractiveMode) collectEvent(ev agent.AgentEvent) {
	part := eventRegionNoUpdate
	if update, ok := ev.(agent.MessageUpdateEvent); ok {
		part = eventRegionMessageUpdate
		switch event := update.AssistantMessageEvent.(type) {
		case ai.ToolCallStartEvent:
			part = toolCallUpdatePart(event.ContentIndex)
		case ai.ToolCallDeltaEvent:
			part = toolCallUpdatePart(event.ContentIndex)
		case ai.ToolCallEndEvent:
			part = toolCallUpdatePart(event.ContentIndex)
		}
		if part != eventRegionNoUpdate && part == m.eventRegionUpdate {
			m.eventRegion[len(m.eventRegion)-1] = ev
			return
		}
	}
	m.eventRegionUpdate = part
	m.eventRegion = append(m.eventRegion, ev)
}

// toolCallUpdatePart is the eventRegionUpdate value of a message_update that reports the tool call of the block at index. An index that is no block's names no part, so such an update is kept beside its neighbours.
func toolCallUpdatePart(index int) int {
	if index < 0 {
		return eventRegionNoUpdate
	}
	return eventRegionToolCallUpdate + index
}

// applyEventRegion applies the collected events in the order they arrived, in this one iteration of the loop.
func (m *InteractiveMode) applyEventRegion() {
	events := m.eventRegion
	m.eventRegion, m.eventRegionUpdate = m.eventRegion[:0], eventRegionNoUpdate
	for i, ev := range events {
		events[i] = nil
		m.applyAgentEvent(ev)
	}
}

// applyAgentEvent updates the mode for one Session event.
func (m *InteractiveMode) applyAgentEvent(ev agent.AgentEvent) {
	// Each assistant message owns one block updated from authoritative event snapshots.
	m.programStatusReporter().HandleEvent(ev)
	// The session file, and with it the resume command, appears when the first prompt is written, which
	// happens after its message_end reaches listeners. The reply's message_start or the run's end sees it.
	switch ev.(type) {
	case agent.MessageStartEvent, agent.AgentSettledEvent:
		m.programResume.ReportIfNone()
	}
	switch e := ev.(type) {
	case agent.SessionInfoChangedEvent:
		m.setTerminalTitle(tui.BuildTerminalTitle(e.Name, m.opts.CWD))
		m.statusLine.SetName(e.Name)
		m.tuiInst.RequestRender()

	case agent.EntryAppendedEvent:
		m.handleEntryAppended(e.Entry)

	case agent.AgentStartEvent:
		m.toolMu.Lock()
		clear(m.toolByID)
		clear(m.toolStarts)
		clear(m.toolFileCalls)
		clear(m.pendingArgs)
		m.toolMu.Unlock()
		// Pi restores the main escape handler here; the terminal progress and the working status start at turn_start.
		m.retryEscapeHandler = false

	case agent.AgentEndEvent:
		if m.opts.Settings.GetShowTerminalProgress() {
			m.setTerminalProgress(false)
		}
		m.clearStatusIndicator("working")
		// Pi removes a message still streaming when the run ends (interactive-mode.ts agent_end).
		if m.evCurrentBlock != nil {
			m.chatContainer.Remove(m.evCurrentBlock)
			m.assistantBlocks = slices.DeleteFunc(m.assistantBlocks, func(block *tui.AssistantMessageComponent) bool { return block == m.evCurrentBlock })
			m.evCurrentBlock = nil
		}
		// Finalize any tool still mid-execution when the turn ended (an
		// aborted run emits no ToolExecutionEnd, so its component would stay
		// ToolStateRunning and its live "Elapsed X.Xs" footer would keep
		// recomputing time.Since(start) on every later render: forcing a
		// repaint per keystroke/agent chunk and breaking scrollback). Events
		// are ordered, so any toolStarts entry left here never received its
		// End and is genuinely stale.
		m.finalizeRunningTools()
		m.toolMu.Lock()
		clear(m.toolByID)
		clear(m.pendingArgs)
		m.toolMu.Unlock()
		m.tuiInst.RequestRender()

	case agent.TurnStartEvent:
		if m.opts.Settings.GetShowTerminalProgress() {
			m.setTerminalProgress(true)
		}
		if m.workingVisible {
			if m.activeStatusIndicator == nil || m.activeStatusIndicator.Kind != "working" {
				m.startWorkingLoader()
			}
		} else {
			m.clearStatusIndicator("")
		}
		m.tuiInst.RequestRender()

	case agent.QueueUpdateEvent:
		m.updatePendingMessagesDisplay()
		m.tuiInst.RequestRender()

	case agent.ThinkingLevelChangedEvent:
		m.refreshThinkingLevel()
		m.tuiInst.RequestRender()

	case agent.BashExecutionUpdateEvent:
		// The bash execution callback renders the command's output.

	case agent.AgentSettledEvent:
		// Move user bash output shown while the agent works into the chat when the run settles.
		m.flushPendingBashBlocks()
		m.checkShutdownRequested()

	case agent.TurnEndEvent:
		m.evTurnIndex = e.TurnIndex + 1
		_ = m.evTurnIndex // used for turn tracking

	case agent.MessageStartEvent:
		if e.Message.User != nil {
			text := extractAgentMessageText(e.Message)
			if text != "" {
				m.addUserMessageToChat(e.Message, text, messageMeta{})
				m.tuiInst.RequestRender()
			}
			// A queued steering/follow-up message that just got injected as a
			// user turn must leave the pending queue immediately, not linger
			// until agent_end. Mirrors upstream message_start(user) →
			// updatePendingMessagesDisplay (interactive-mode.ts:2801).
			m.updatePendingMessagesDisplay()
		}
		if custom := e.Message.Custom; custom != nil {
			// upstream: interactive-mode.ts handleEvent message_start renders every displayed custom message through addMessageToChat,
			// whichever path delivered it (before_agent_start handler messages, queued steer/follow-up, nextTurn, a triggerTurn start).
			if entry, display := customMessageEntryOf(custom); display {
				m.appendCustomMessage(entry, messageMeta{})
				m.tuiInst.RequestRender()
			}
		}
		if e.Message.Assistant != nil {
			// One block per assistant message: handles thinking + text.
			m.evCurrentBlock = m.newAssistantMessageBlock(messageMeta{})
			updateAssistantMessageBlock(m.evCurrentBlock, e.Message.Assistant)
			m.appendToChat(m.evCurrentBlock)
			m.assistantBlocks = append(m.assistantBlocks, m.evCurrentBlock)
			m.tuiInst.RequestRender()
		}

	case agent.MessageUpdateEvent:
		// Pi's whole message_update handler runs only while a message streams.
		if m.evCurrentBlock == nil || e.Message.Assistant == nil {
			break
		}
		updateAssistantMessageBlock(m.evCurrentBlock, e.Message.Assistant)
		// pig additive (D625): while the adaptive policy paces the frames, a tool call delta that leaves its card's lines as the last frame drew them asks for no frame (unchanged, below): the frame would send the terminal nothing. Under Pi's interval every update asks for one here and one below, as Pi's handler does.
		_, toolDelta := e.AssistantMessageEvent.(ai.ToolCallDeltaEvent)
		skippable := toolDelta && m.framePacingInForce()
		if !skippable {
			m.tuiInst.RequestRender()
		}
		unchanged := false
		var toolIndex int
		var toolID, toolName string
		var endCall ai.ToolCall
		hasEnd := false
		hasToolUpdate := false
		switch event := e.AssistantMessageEvent.(type) {
		case ai.ToolCallStartEvent:
			toolIndex = event.ContentIndex
			hasToolUpdate = true
			// Only the block's identity is read; observing the whole partial would copy every block's arguments for each delta.
			if id, name, isToolCall, _ := event.Partial.ObserveToolCallIdentity(toolIndex); isToolCall {
				toolID, toolName = id, name
			}
		case ai.ToolCallDeltaEvent:
			toolIndex, hasToolUpdate = event.ContentIndex, true
			// Only the block's identity is read; observing the whole partial would copy every block's arguments for each delta.
			if id, name, isToolCall, _ := event.Partial.ObserveToolCallIdentity(toolIndex); isToolCall {
				toolID, toolName = id, name
			}
		case ai.ToolCallEndEvent:
			toolIndex, toolID, toolName, hasToolUpdate = event.ContentIndex, event.ToolCall.ID, event.ToolCall.Name, true
			endCall, hasEnd = event.ToolCall, true
		}
		if !hasToolUpdate {
			break
		}
		if m.evCurrentBlock != nil {
			m.evCurrentBlock.SetHasToolCalls(true)
		}
		// upstream: interactive-mode.ts:3529-3553 hands the card content.arguments, the message's partial arguments, on
		// every message_update. The provider's resumable streaming JSON parser has already read this delta into them, and
		// the card encodes them only when it draws, so a delta costs the same however long the arguments are.
		streamed, hasStreamed := streamedToolCall(e.Message.Assistant, toolIndex)
		if !hasStreamed && hasEnd {
			streamed, hasStreamed = endCall, true
		}

		m.toolMu.Lock()
		pta, ok := m.pendingArgs[toolIndex]
		if !ok {
			pta = &pendingToolArg{}
			m.pendingArgs[toolIndex] = pta
		}
		if toolID != "" {
			pta.id = toolID
		}
		if toolName != "" {
			pta.name = toolName
		}

		if pta.id != "" {
			comp := m.toolByID[pta.id]
			created := comp == nil
			if created {
				comp = m.newToolCard(pta.name, pta.id, nil)
			}
			m.applyToolPresentation(comp, pta.id, pta.name, nil)
			if hasStreamed {
				comp.SetStreamedArgs(streamedToolCallArgs{call: streamed})
			}
			if created {
				if m.toolsExpanded {
					comp.SetExpanded(true)
				}
				m.toolByID[pta.id] = comp
				m.toolOrder = append(m.toolOrder, comp)
				m.toolMu.Unlock()
				m.appendToChat(comp)
			} else {
				m.toolMu.Unlock()
				unchanged = skippable && hasStreamed && comp.CallShowsNoChange()
			}
		} else {
			m.toolMu.Unlock()
		}
		if !unchanged {
			m.tuiInst.RequestRender()
		}

	case agent.MessageEndEvent:
		// Pi ends message_end for a user message without rendering or touching the pending tools (interactive-mode.ts handleEvent).
		if e.Message.User != nil {
			// pig: the footer holds a stored context estimate (Pi's footer recomputes it on every render), so the user message_end refreshes it here: the prompt counts as soon as the message is recorded.
			m.refreshFooterContextUsage()
			break
		}
		defer m.refreshFooterContextUsage()
		if e.Message.Assistant != nil && e.Message.Assistant.Usage != nil {
			m.statusLine.SetTurnContextUsage(e.Message.Assistant.Usage)
		}
		// Pi finishes a message only while one streams; any other message_end just requests a frame.
		if m.evCurrentBlock == nil || e.Message.Assistant == nil {
			m.tuiInst.RequestRender()
			break
		}
		// An aborted message already carries the text the mode's Session listener gave it before the Session persisted and published it (handleSessionEvent).
		finished := e.Message.Assistant
		updateAssistantMessageBlock(m.evCurrentBlock, finished)
		// Mirrors upstream message_end (interactive-mode.ts:2774-2793):
		// On abort/error → push the error into every pending tool component
		// (the assistant message block's own error is suppressed when
		// hasToolCalls=true, so tools are the only visual feedback).
		// On normal completion → mark args complete (triggers diff rendering).
		isAbortOrError := e.Message.Assistant.StopReason == "aborted" || e.Message.Assistant.StopReason == "error"
		m.toolMu.Lock()
		if isAbortOrError {
			errMsg := finished.ErrorMessage
			if finished.StopReason != ai.StopReasonAborted && errMsg == "" {
				errMsg = "Error"
			}
			for _, comp := range m.toolByID {
				comp.SetResultValue(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: errMsg}}, IsError: true})
				comp.SetResult(errMsg, true, 0)
				if e.Message.Assistant.StopReason == "aborted" {
					comp.MarkAborted() // pig additive (D91): a frontend shows it cancelled
				}
			}
			clear(m.toolByID)
			clear(m.toolStarts)
			clear(m.toolFileCalls)
		} else {
			for _, pta := range m.pendingArgs {
				if pta.id != "" {
					if comp := m.toolByID[pta.id]; comp != nil {
						comp.SetArgsComplete()
					}
				}
			}
		}
		// Clear pendingArgs for next message.
		clear(m.pendingArgs)
		m.toolMu.Unlock()
		if isAbortOrError {
			m.maybeSuggestBugReport(e.Message.Assistant)
		} else {
			m.maybeShowThinkingDropNotice(e.Message.Assistant)
			if m.showCacheMissNotices() {
				if notice := formatCacheMissNotice(m.currentSession().detectCacheMiss(e.Message.Assistant)); notice != "" {
					m.addCacheMissNotice(notice)
				}
			}
		}
		m.previousDroppedReasoning, _ = countDroppedReasoning(e.Message.Assistant)
		m.evCurrentBlock.FinishStreaming() // its last delta arrived; keep no memos for the next one
		m.evCurrentBlock = nil
		m.tuiInst.RequestRender()

	case agent.ToolExecutionStartEvent:
		// A call another tool made through ctx.executeTool(), for example from a codemode script, is shown inside its
		// parent's row. Its update and end events find no card.
		// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:3501-3503
		if e.ParentToolCallID != "" {
			return
		}
		// Mark the current assistant block as having tool calls so its
		// abort/error rendering is suppressed (tools show their own).
		// Mirrors upstream assistant-message.ts:128.
		if m.evCurrentBlock != nil {
			m.evCurrentBlock.SetHasToolCalls(true)
		}
		argsPreview := tui.HeaderForTool(e.ToolName, e.Args, m.opts.CWD)
		m.toolMu.Lock()
		if e.ToolName == "read" || e.ToolName == "write" {
			if m.toolFileCalls == nil {
				m.toolFileCalls = make(map[string]ai.ToolCall)
			}
			m.toolFileCalls[e.ToolCallID] = ai.ToolCall{Name: e.ToolName, Arguments: fileCallArguments(e.Args)}
		}
		comp := m.toolByID[e.ToolCallID]
		if comp != nil {
			// Component was created during streaming: update with
			// final (complete) args and mark execution started.
			comp.Cwd = m.opts.CWD
			comp.ArgsPreview = argsPreview
			comp.SetHeaderArgs(e.Args)
			m.applyToolPresentation(comp, e.ToolCallID, e.ToolName, e.Args)
			comp.MarkExecutionStarted()
			m.toolStarts[e.ToolCallID] = time.Now()
			m.toolMu.Unlock()
		} else {
			// No streaming component: create fresh (extension tools,
			// or providers that don't emit per-delta tool IDs).
			comp = m.newToolCard(e.ToolName, e.ToolCallID, e.Args)
			m.applyToolPresentation(comp, e.ToolCallID, e.ToolName, e.Args)
			if m.toolsExpanded {
				comp.SetExpanded(true)
			}
			comp.MarkExecutionStarted()
			m.toolByID[e.ToolCallID] = comp
			m.toolStarts[e.ToolCallID] = time.Now()
			m.toolOrder = append(m.toolOrder, comp)
			m.toolMu.Unlock()
			m.appendToChat(comp)
		}
		// upstream: tool-execution.ts markExecutionStarted runs updateDisplay, the card's renderers, at the event, so a renderer's
		// clock starts here; interactive-mode.ts:3754 then requests the frame, which the throttle draws.
		comp.Render(m.tuiInst.Width())
		m.tuiInst.RequestRender()

	case agent.ToolExecutionUpdateEvent:
		// Live streaming update from a long-running tool (e.g. bash).
		// Replace the visible body with the latest snapshot while keeping
		// the state running. The component shows the live tail by default;
		// an explicit Ctrl+O choice remains sticky across updates.
		m.toolMu.Lock()
		comp := m.toolByID[e.ToolCallID]
		m.toolMu.Unlock()
		if comp == nil {
			return
		}
		if tui.IsShellTool(e.ToolName) {
			// Shell tools render partial results through the shared shell
			// renderer (upstream renderers/bash.ts, isPartial).
			comp.BodyRenderer = makeShellBodyRenderer(e.PartialResult.Text(), e.PartialResult.Details, true, nil)
		}
		// Upstream hands a partial result to updateResult as `{...event.partialResult, isError: false}`.
		// upstream: modes/interactive/interactive-mode.ts:3527-3533
		partial := e.PartialResult
		partial.IsError = false
		comp.UpdateResult(ToolExecutionResultOf(partial, 0), true)
		m.tuiInst.RequestRender()

	case agent.ToolExecutionEndEvent:
		// Pi checks before it looks up the component (interactive-mode.ts tool_execution_end).
		if e.IsError {
			m.maybeShowInstallChangeWarning()
		}
		m.toolMu.Lock()
		comp := m.toolByID[e.ToolCallID]
		delete(m.toolByID, e.ToolCallID)
		start := m.toolStarts[e.ToolCallID]
		delete(m.toolStarts, e.ToolCallID)
		call, hasFileCall := m.toolFileCalls[e.ToolCallID]
		delete(m.toolFileCalls, e.ToolCallID)
		m.toolMu.Unlock()
		debugLog("tool end id=%q name=%q matched=%v outlen=%d", e.ToolCallID, e.ToolName, comp != nil, len(e.Result.Text()))
		if comp == nil {
			return
		}
		// upstream: interactive-mode.ts:3539 hands the component `{ ...event.result, isError: event.isError }`.
		result := e.Result
		result.IsError = e.IsError
		var elapsed time.Duration
		var took *time.Duration
		if !start.IsZero() {
			elapsed = time.Since(start)
			took = &elapsed
		}
		// Attach a per-tool body renderer so Ctrl+O reveals a diff /
		// line-numbered view instead of raw text. Body renderers read no structured content, and the card keeps them as long as it is shown.
		took = recordedTook(e.ToolName, e.DurationMs, took)
		bodyResult := result
		bodyResult.StructuredContent = nil
		comp.BodyRenderer = toolBodyRenderer(e.ToolName, bodyResult, took)
		if hasFileCall {
			// File renderers own their call arguments; wire results carry no private preview state.
			comp.BodyRenderer = toolBodyRendererForCall(call, bodyResult, took)
		}
		// upstream: tool-execution.ts:187 updateResult takes the content blocks, so images render inline.
		final := ToolExecutionResultOf(result, elapsed)
		final.Result = retainToolResult(e.ToolName, result)
		final.DurationMs = e.DurationMs
		comp.UpdateResult(final, false)
		// upstream: tool-execution.ts updateResult runs updateDisplay at the event; interactive-mode.ts:3773 then requests the
		// frame, which the throttle draws.
		comp.Render(m.tuiInst.Width())
		m.tuiInst.RequestRender()

	case agent.CompactionStartEvent:
		// Central abort routing handles Esc and Ctrl+C during compaction.
		if m.opts.Settings.GetShowTerminalProgress() {
			m.setTerminalProgress(true)
		}
		m.isCompacting = true
		m.showCompactionStatusIndicator(e.Reason)
		m.tuiInst.Render()

	case agent.CompactionEndEvent:
		// Compaction finished (success, abort, or error).
		// Restore normal state; handle result.
		// Mirrors upstream compaction_end handler (interactive-mode.ts:2795-2833).
		if m.opts.Settings.GetShowTerminalProgress() {
			m.setTerminalProgress(false)
		}
		m.isCompacting = false
		m.clearStatusIndicator("compaction")
		m.compactionStartedAt = time.Time{} // pig divergence (D652): the next compaction dates itself

		switch {
		case e.Aborted:
			if e.Reason == "manual" {
				// Pi uses showError for manual cancellation, so the result remains
				// in conversation history instead of disappearing as a footer flash.
				m.showError("Compaction cancelled")
			} else {
				m.showStatus("Auto-compaction cancelled")
			}
		case e.HasResult:
			var entries []SessionEntry
			if session := m.currentSession(); session != nil {
				entries = session.BuildContextEntries()
			}
			m.renderCompactionResult(e, entries)
			m.statusLine.Invalidate()
		case e.ErrorMessage != "":
			// Manual compaction errors surface as a persistent transcript line
			// "Error: <message>" (upstream showError); auto/overflow errors are
			// appended without the "Error: " prefix (upstream error-color Text).
			if e.Reason == "manual" {
				m.showError(e.ErrorMessage)
			} else {
				m.appendChatBlock(themedNotice("error", e.ErrorMessage, 1))
			}
		}
		// handleAgentEvent runs on the input loop, so queue delivery and any
		// turn-starting handleSubmit stay single-threaded with editor state.
		// Retry recovery steers into the imminent Continue turn; other endings
		// start a new turn from the first queued prompt when idle.
		m.flushCompactionQueue(m.runCtx, e.WillRetry)
		m.tuiInst.Render()

	case agent.AutoRetryStartEvent:
		// pig additive (D654): a silent retry shows no status line and no final error, and Escape still cancels its delay, as in Pi.
		if DefaultRetrySilent() {
			m.clearStatusIndicator("")
			m.retryCountdownStop = func() {}
			m.retryEscapeHandler = true
			break
		}
		m.showRetryStatusIndicator(e.Attempt, e.MaxAttempts, e.DelayMs)
		// Escape cancels the retry delay until agent_start or auto_retry_end.
		m.retryEscapeHandler = true
		m.tuiInst.Render()

	case agent.AutoRetryEndEvent:
		if DefaultRetrySilent() {
			m.retryCountdownStop = nil
			m.retryEscapeHandler = false
			break
		}
		m.clearStatusIndicator("retry")
		// Mirrors upstream auto_retry_end: only a final failure, including a
		// cancelled retry, is shown, as a persistent transcript error.
		if !e.Success {
			finalError := e.FinalError
			if finalError == "" {
				finalError = "Unknown error"
			}
			m.showError(fmt.Sprintf("Retry failed after %d attempts: %s", e.Attempt, finalError))
		}
		m.tuiInst.Render()

	case agent.SummarizationRetryScheduledEvent:
		m.showError(e.ErrorMessage)
		m.showRetryStatusIndicator(e.Attempt, e.MaxAttempts, e.DelayMs)
		m.tuiInst.Render()

	case agent.SummarizationRetryAttemptStartEvent:
		m.clearStatusIndicator("retry")
		if e.Source == "branchSummary" {
			m.showBranchSummaryStatusIndicator()
		} else {
			m.showCompactionStatusIndicator(e.Reason)
		}
		m.tuiInst.Render()

	case agent.SummarizationRetryFinishedEvent:
		m.clearStatusIndicator("retry")
		m.tuiInst.Render()
	}
}

// handleModelPicker drives the model selector overlay (Ctrl+L or
// onTerminalResize re-applies the editor's max-visible cap and re-renders.
// Called by the platform resize handler (SIGWINCH on unix, size poll on
// Windows). The work mutates the editor and renders the tree, both owned by
// the main input loop, so it is posted there rather than run on the handler
// goroutine, which would race keystroke handling on the editor's
// unsynchronized state. applyEditorMaxVisible derives from Height(), so a
// coalesced/dropped post is re-applied by the next render. Mirrors upstream's
// single-event-loop resize.

// abortedMessageText is the text the tool cards of an aborted message show when a session is rendered from its entries, naming the automatic retries running now.
// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:4212-4218 (renderSessionContext).
func (m *InteractiveMode) abortedMessageText() string {
	attempt := 0
	if m.opts.SessionHandle != nil {
		attempt = m.opts.SessionHandle.RetryAttempt()
	}
	return abortedMessageForRetries(attempt)
}

// abortedMessageForRetries is the errorMessage Pi's interactive mode gives an aborted message.
// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:3690-3695 and :4213-4218.
func abortedMessageForRetries(attempt int) string {
	if attempt <= 0 {
		return "Operation aborted"
	}
	if attempt == 1 {
		return "Aborted after 1 retry attempt"
	}
	return fmt.Sprintf("Aborted after %d retry attempts", attempt)
}

// toolExecutionOptions are the image settings every tool card is constructed with (interactive-mode.ts:3516, 3594, 3997 pass
// showImages and imageWidthCells from the settings manager; blockImages does not hide them). Without a settings manager the card keeps its defaults.
func (m *InteractiveMode) toolExecutionOptions() tui.ToolExecutionOptions {
	pad := m.outputPad
	if m.opts.SettingsManager == nil {
		return tui.ToolExecutionOptions{OutputPad: &pad}
	}
	s := m.opts.SettingsManager.Get()
	show, width := s.GetShowImages(), s.GetImageWidthCells()
	return tui.ToolExecutionOptions{ShowImages: &show, ImageWidthCells: &width, OutputPad: &pad}
}

// toolResultUpdate is a tool result as tool-execution.ts updateResult takes it: its content blocks, error state and structured result.
func toolResultUpdate(result agent.AgentToolResult) tui.ToolResultUpdate {
	content := make([]tui.ToolResultContent, 0, len(result.Content))
	for _, block := range result.Content {
		switch value := block.(type) {
		case ai.TextContent:
			content = append(content, tui.ToolResultContent{Type: "text", Text: value.Text})
		case ai.ImageContent:
			content = append(content, tui.ToolResultContent{Type: "image", Data: value.Data, MimeType: value.MimeType})
		}
	}
	return tui.ToolResultUpdate{Content: content, Details: result.Details, IsError: result.IsError, Result: result}
}

// streamedToolCall is the tool call at index of a streaming assistant message, whose arguments are what the provider's
// streaming JSON parser has read of them so far (upstream message_update content.arguments).
func streamedToolCall(message *agent.AssistantMessage, index int) (ai.ToolCall, bool) {
	if message == nil || index < 0 || index >= len(message.Content) {
		return ai.ToolCall{}, false
	}
	call, ok := message.Content[index].(ai.ToolCall)
	return call, ok
}

// streamedToolCallArgs is the partial arguments of a streaming tool call as a tool card draws them.
type streamedToolCallArgs struct {
	call ai.ToolCall
}

// Object is the argument object the provider's streaming parser left in the call, read in place. Each update of the call
// holds a new object, so the card's copy of an earlier one never changes under it.
func (args streamedToolCallArgs) Object() map[string]any { return args.call.Arguments }

// JSON writes the arguments in the member order the model sent them, as Pi's renderers enumerate the parsed object. It
// goes through ArgumentMembers rather than ArgumentsJSON, which keeps every object it encodes for later requests: a
// partial object is drawn once and never sent.
func (args streamedToolCallArgs) JSON() json.RawMessage {
	if args.call.Arguments == nil {
		// A value that is not an object, or none yet: ArgumentsJSON keeps no encoding for either.
		raw, err := args.call.ArgumentsJSON()
		if err != nil {
			return nil
		}
		return raw
	}
	members, err := args.call.ArgumentMembers()
	if err != nil {
		return nil
	}
	size := 2
	for _, member := range members {
		size += len(member.Key) + len(member.JSON) + 4
	}
	raw := make([]byte, 0, size)
	raw = append(raw, '{')
	for i, member := range members {
		if i > 0 {
			raw = append(raw, ',')
		}
		key, err := json.Marshal(member.Key)
		if err != nil {
			return nil
		}
		raw = append(raw, key...)
		raw = append(raw, ':')
		raw = append(raw, member.JSON...)
	}
	return append(raw, '}')
}
