package codingagent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

func (m *InteractiveMode) finalizeRunningTools() {
	m.toolMu.Lock()
	for id, start := range m.toolStarts {
		if comp := m.toolByID[id]; comp != nil {
			comp.FinalizeAborted(time.Since(start))
		}
		delete(m.toolStarts, id)
	}
	m.toolMu.Unlock()
}

// handleAgentEvent handles one event from the agent loop for live rendering.
// It runs on the main input goroutine (driven by the inputLoop select on
// m.eventCh), single-threaded with keystroke handling and posted UI tasks, so
// it never races the lock-free TUI component tree. Mirrors upstream's single
// JS event loop where agent events and input are dispatched on one thread.
// Cross-event state (the current assistant block / message / turn index) lives
// on InteractiveMode fields since the handler is now invoked per event.
// setTerminalProgress writes the OSC 9;4 terminal progress indicator.
var setTerminalProgress = tui.SetTerminalProgress

func (m *InteractiveMode) handleAgentEvent(ev agent.AgentEvent) {
	// Each assistant message owns one block updated from authoritative event snapshots.
	if barrier, ok := ev.(eventBarrier); ok {
		// Every earlier event has now been handled on this loop.
		barrier.AcknowledgeEvent()
		return
	}
	switch e := ev.(type) {
	case agent.EntryAppendedEvent:
		m.handleEntryAppended(e.Entry)

	case agent.AgentStartEvent:
		m.toolMu.Lock()
		clear(m.toolByID)
		clear(m.toolStarts)
		clear(m.pendingArgs)
		m.toolMu.Unlock()
		if m.opts.Settings.GetShowTerminalProgress() {
			setTerminalProgress(true)
		}
		// Show the working status in the opted-in editor border.
		m.startWorkingLoader()

	case agent.AgentEndEvent:
		if m.opts.Settings.GetShowTerminalProgress() {
			setTerminalProgress(false)
		}
		// Stop working loader. Mirrors upstream streaming_stopped.
		m.stopWorkingLoader()
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

	case agent.TurnEndEvent:
		m.evTurnIndex = e.TurnIndex + 1
		_ = m.evTurnIndex // used for turn tracking

	case agent.MessageStartEvent:
		if e.Message.User != nil {
			text := strings.TrimSpace(extractAgentMessageText(e.Message))
			if text != "" {
				if !m.chatContainer.IsEmpty() {
					m.appendToChat(tui.NewSpacer(1))
				}
				m.appendToChat(m.newUserMessageBlock(text))
				m.tuiInst.RequestRender()
			}
			// A queued steering/follow-up message that just got injected as a
			// user turn must leave the pending queue immediately, not linger
			// until agent_end. Mirrors upstream message_start(user) →
			// updatePendingMessagesDisplay (interactive-mode.ts:2801).
			m.updatePendingMessagesDisplay()
		}
		if e.Message.Assistant != nil {
			// One block per assistant message: handles thinking + text.
			m.evCurrentBlock = m.newAssistantMessageBlock()
			updateAssistantMessageBlock(m.evCurrentBlock, e.Message.Assistant)
			m.appendToChat(m.evCurrentBlock)
			m.assistantBlocks = append(m.assistantBlocks, m.evCurrentBlock)
			m.tuiInst.RequestRender()
		}

	case agent.MessageUpdateEvent:
		if m.evCurrentBlock != nil && e.Message.Assistant != nil {
			updateAssistantMessageBlock(m.evCurrentBlock, e.Message.Assistant)
			m.tuiInst.RequestRender()
		}
		var toolIndex int
		var toolID, toolName, argsDelta string
		hasToolUpdate := false
		switch event := e.AssistantMessageEvent.(type) {
		case ai.ToolCallStartEvent:
			toolIndex = event.ContentIndex
			hasToolUpdate = true
			if event.Partial != nil && toolIndex < len(event.Partial.Content) {
				if call, ok := event.Partial.Content[toolIndex].(ai.ToolCall); ok {
					toolID, toolName = call.ID, call.Name
				}
			}
		case ai.ToolCallDeltaEvent:
			toolIndex, argsDelta, hasToolUpdate = event.ContentIndex, event.Delta, true
			if event.Partial != nil && toolIndex < len(event.Partial.Content) {
				if call, ok := event.Partial.Content[toolIndex].(ai.ToolCall); ok {
					toolID, toolName = call.ID, call.Name
				}
			}
		case ai.ToolCallEndEvent:
			toolIndex, toolID, toolName, hasToolUpdate = event.ContentIndex, event.ToolCall.ID, event.ToolCall.Name, true
		}
		if !hasToolUpdate {
			break
		}
		if m.evCurrentBlock != nil {
			m.evCurrentBlock.SetHasToolCalls(true)
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
		pta.args.WriteString(argsDelta)

		if pta.id != "" {
			comp := m.toolByID[pta.id]
			if comp == nil {
				argsPreview := tui.HeaderForTool(pta.name, nil, m.opts.CWD)
				comp = tui.NewToolExecutionComponent(pta.name, argsPreview)
				comp.Cwd = m.opts.CWD
				m.applyToolPresentation(comp, pta.id, pta.name, json.RawMessage(pta.args.String()))
				if m.opts.SettingsManager != nil {
					s := m.opts.SettingsManager.Get()
					comp.ShowImages = s.GetShowImages() && !s.BlockImages
					comp.ImageWidthCells = s.GetImageWidthCells()
				}
				if m.toolsExpanded {
					comp.SetExpanded(true)
				}
				m.toolByID[pta.id] = comp
				m.toolOrder = append(m.toolOrder, comp)
				m.toolMu.Unlock()
				m.appendToChat(comp)
			} else {
				m.applyToolPresentation(comp, pta.id, pta.name, json.RawMessage(pta.args.String()))
				comp.UpdateArgs(pta.name, pta.args.String())
				m.toolMu.Unlock()
			}
		} else {
			m.toolMu.Unlock()
		}
		m.tuiInst.RequestRender()

	case agent.MessageEndEvent:
		defer m.refreshFooterContextUsage()
		if m.evCurrentBlock != nil && e.Message.Assistant != nil {
			updateAssistantMessageBlock(m.evCurrentBlock, e.Message.Assistant)
			m.lastAssistantText = strings.TrimSpace(m.evCurrentBlock.Text())
		}
		if e.Message.Assistant != nil {
			if e.Message.Assistant.Usage != nil {
				m.statusLine.SetTurnContextUsage(e.Message.Assistant.Usage)
			}
			if m.showCacheMissNotices() {
				if notice := formatCacheMissNotice(m.currentSession().detectCacheMiss(e.Message.Assistant)); notice != "" {
					m.appendChatBlock(tui.NewText("\033[33m" + notice + "\033[0m"))
				}
			}
			if (e.Message.Assistant.StopReason == "error" || e.Message.Assistant.StopReason == "aborted") && e.Message.Assistant.ErrorMessage != "" {
				statusText, _ := formatProviderErrorForDisplay(string(e.Message.Assistant.StopReason), e.Message.Assistant.ErrorMessage)
				m.statusLine.Flash(statusText, 5*time.Second)
			}
		}
		// Mirrors upstream message_end (interactive-mode.ts:2774-2793):
		// On abort/error → push the error into every pending tool component
		// (the assistant message block's own error is suppressed when
		// hasToolCalls=true, so tools are the only visual feedback).
		// On normal completion → mark args complete (triggers diff rendering).
		isAbortOrError := e.Message.Assistant != nil &&
			(e.Message.Assistant.StopReason == "aborted" || e.Message.Assistant.StopReason == "error")
		m.toolMu.Lock()
		if isAbortOrError {
			errMsg := "Operation aborted"
			if e.Message.Assistant.StopReason == "error" {
				errMsg = "Error"
			}
			if e.Message.Assistant.ErrorMessage != "" {
				errMsg = e.Message.Assistant.ErrorMessage
			}
			for _, comp := range m.toolByID {
				comp.SetResultValue(agent.AgentToolResult{Content: errMsg, IsError: true})
				comp.SetResult(errMsg, true, 0)
			}
			clear(m.toolByID)
			clear(m.toolStarts)
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
		m.evCurrentBlock = nil
		m.tuiInst.CancelPendingRender()

	case agent.ToolExecutionStartEvent:
		// Mark the current assistant block as having tool calls so its
		// abort/error rendering is suppressed (tools show their own).
		// Mirrors upstream assistant-message.ts:128.
		if m.evCurrentBlock != nil {
			m.evCurrentBlock.SetHasToolCalls(true)
		}
		argsPreview := tui.HeaderForTool(e.ToolName, e.Args, m.opts.CWD)
		m.toolMu.Lock()
		comp := m.toolByID[e.ToolCallID]
		if comp != nil {
			// Component was created during streaming: update with
			// final (complete) args and mark execution started.
			comp.Cwd = m.opts.CWD
			comp.ArgsPreview = argsPreview
			comp.SetHeaderArgs(e.Args)
			m.applyToolPresentation(comp, e.ToolCallID, e.ToolName, e.Args)
			if e.ToolLabel != "" {
				comp.Label = e.ToolLabel
			}
			comp.MarkExecutionStarted()
			m.toolStarts[e.ToolCallID] = time.Now()
			m.toolMu.Unlock()
		} else {
			// No streaming component: create fresh (extension tools,
			// or providers that don't emit per-delta tool IDs).
			comp = tui.NewToolExecutionComponent(e.ToolName, argsPreview)
			comp.Cwd = m.opts.CWD
			comp.SetHeaderArgs(e.Args)
			m.applyToolPresentation(comp, e.ToolCallID, e.ToolName, e.Args)
			if e.ToolLabel != "" {
				comp.Label = e.ToolLabel
			}
			if m.opts.SettingsManager != nil {
				s := m.opts.SettingsManager.Get()
				comp.ShowImages = s.GetShowImages() && !s.BlockImages
				comp.ImageWidthCells = s.GetImageWidthCells()
			}
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
		m.tuiInst.Render()

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
			comp.BodyRenderer = makeShellBodyRenderer(e.Content, e.Details, true, nil)
		}
		if comp.HasDefinition() {
			// Upstream hands a partial result to renderResult with isPartial.
			comp.SetResultValue(agent.AgentToolResult{Content: e.Content, Details: e.Details})
		}
		comp.SetStreaming(e.Content)
		m.tuiInst.RequestRender()

	case agent.ToolExecutionEndEvent:
		m.toolMu.Lock()
		comp := m.toolByID[e.ToolCallID]
		delete(m.toolByID, e.ToolCallID)
		start := m.toolStarts[e.ToolCallID]
		delete(m.toolStarts, e.ToolCallID)
		m.toolMu.Unlock()
		debugLog("tool end id=%q name=%q matched=%v outlen=%d", e.ToolCallID, e.ToolName, comp != nil, len(e.Result.Content))
		if comp == nil {
			return
		}
		var elapsed time.Duration
		var took *time.Duration
		if !start.IsZero() {
			elapsed = time.Since(start)
			took = &elapsed
		}
		// Attach a per-tool body renderer so Ctrl+O reveals a diff /
		// line-numbered view instead of raw text.
		comp.BodyRenderer = toolBodyRenderer(e.ToolName, e.Result, took)
		// Wire image blocks from tool results so they render inline.
		// Mirrors upstream tool-execution.ts updateResult → image block handling.
		if len(e.Result.Images) > 0 {
			blocks := make([]tui.ImageBlock, len(e.Result.Images))
			for i, img := range e.Result.Images {
				blocks[i] = tui.ImageBlock{Data: img.Data, MIMEType: img.MimeType}
			}
			comp.ImageBlocks = blocks
		}
		comp.SetResultValue(e.Result)
		comp.SetResult(e.Result.Content, e.Result.IsError, elapsed)
		m.maybeConvertImagesForKitty(comp)
		m.tuiInst.Render()

	case agent.CompactionStartEvent:
		// Central abort routing handles Esc and Ctrl+C during compaction.
		if m.opts.Settings.GetShowTerminalProgress() {
			setTerminalProgress(true)
		}
		m.isCompacting = true
		m.showCompactionStatusIndicator(e.Reason)
		m.tuiInst.Render()

	case agent.CompactionEndEvent:
		// Compaction finished (success, abort, or error).
		// Restore normal state; handle result.
		// Mirrors upstream compaction_end handler (interactive-mode.ts:2795-2833).
		if m.opts.Settings.GetShowTerminalProgress() {
			setTerminalProgress(false)
		}
		m.isCompacting = false
		m.clearStatusIndicator("compaction")

		switch {
		case e.Aborted:
			if e.Reason == "manual" {
				// Pi uses showError for manual cancellation, so the result remains
				// in conversation history instead of disappearing as a footer flash.
				m.appendChatBlock(tui.NewText("\033[31mError: Compaction cancelled\033[0m"))
			} else {
				m.statusLine.Flash("Auto-compaction cancelled", 2*time.Second)
			}
		case e.Summary != "":
			// Successful compaction: rebuild chat from session entries.
			// rebuildChatFromSession walks the JSONL and renders the
			// CompactionSummaryComponent inline at the boundary position.
			// No separate appendToChat needed: would duplicate the chip.
			m.rebuildChatFromSession()
			m.statusLine.Invalidate()
		case e.ErrorMessage != "":
			// Manual compaction errors surface as a persistent transcript line
			// "Error: <message>" (upstream showError); auto/overflow errors are
			// appended without the "Error: " prefix (upstream error-color Text).
			if e.Reason == "manual" {
				m.appendToChat(tui.NewText("\x1b[31mError: " + e.ErrorMessage + "\x1b[0m"))
			} else {
				m.appendToChat(tui.NewText("\x1b[31m" + e.ErrorMessage + "\x1b[0m"))
			}
		}
		// handleAgentEvent runs on the input loop, so queue delivery and any
		// turn-starting handleSubmit stay single-threaded with editor state.
		// Retry recovery steers into the imminent Continue turn; other endings
		// start a new turn from the first queued prompt when idle.
		m.flushCompactionQueue(m.runCtx, !e.WillRetry)
		m.tuiInst.Render()

	case agent.AutoRetryStartEvent:
		m.showRetryStatusIndicator(e.Attempt, e.MaxAttempts, e.DelayMs)
		m.tuiInst.Render()

	case agent.AutoRetryEndEvent:
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
