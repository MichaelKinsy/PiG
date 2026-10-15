package codingagent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui"
)

func (m *InteractiveMode) rebuildChatFromSession() {
	m.rebuildChat(false)
}

// rebuildChat clears and repaints the transcript; populateHistory is renderSessionEntries' option of the same name.
func (m *InteractiveMode) rebuildChat(populateHistory bool) {
	m.disposeArminComponents()
	m.chatContainer.Clear()
	m.tuiInst.ForceFullRender()
	m.renderSessionEntriesWithHistory(populateHistory)
}

// renderSessionEntries renders session messages/entries into the chat
// container without clearing it first. Initial resume retains loaded resources;
// rebuildChatFromSession clears the transcript before calling it.
//
// Mirrors upstream renderInitialMessages (interactive-mode.ts:3097) for
// the append-only case and rebuildChatFromMessages (interactive-mode.ts:3346)
// for the clear-first case (via rebuildChatFromSession).
// parsedEntry caches the WireToMessage conversion for one session entry
// so renderSessionEntries can walk the branch for usage hydration and
// rendering without converting it twice.
type parsedEntry struct {
	entry SessionEntry
	// agent is the entry's message when hasAgent: a message entry that parsed. It is held by value, so the branch slice holds it without an
	// allocation per message.
	agent    agent.AgentMessage
	hasAgent bool
}

// compactionTrimIndex returns the index into a root->leaf branch where
// resume rendering should begin so the view matches the aligned context
// (buildSessionContext): everything before the latest compaction's kept
// tail collapses to that compaction's summary. The kept tail starts at the
// latest compaction's FirstKeptEntryID (which precedes the compaction
// node). When no entry before the compaction has that id, or it is empty,
// nothing is kept and rendering begins at the compaction itself, as
// buildContextEntries compares each entry's id with it (session-manager.ts:499-507).
// Mirrors session.go BuildContext foundFirstKept logic.
func compactionTrimIndex(branch []parsedEntry) int {
	lastCompactionIdx := -1
	for i := range branch {
		if branch[i].entry.Base().Type == "compaction" {
			lastCompactionIdx = i
		}
	}
	if lastCompactionIdx < 0 {
		return 0
	}
	var ce CompactionEntry
	if err := json.Unmarshal(branch[lastCompactionIdx].entry.Raw(), &ce); err != nil || ce.FirstKeptEntryID == "" {
		return lastCompactionIdx
	}
	for i := 0; i < lastCompactionIdx; i++ {
		if branch[i].entry.Base().ID == ce.FirstKeptEntryID {
			return i
		}
	}
	return lastCompactionIdx
}

// contextRenderOrder is branch[start:] in the order Pi paints a restored transcript, session-manager.ts buildContextEntries (:476-508): the latest
// compaction first, then the kept entries that precede it in the file, then the entries after it. start is compactionTrimIndex(branch).
func contextRenderOrder(branch []parsedEntry, start int) []parsedEntry {
	c := -1
	for i := range branch {
		if branch[i].entry.Base().Type == "compaction" {
			c = i
		}
	}
	if c < start {
		return branch[start:]
	}
	out := make([]parsedEntry, 0, len(branch)-start)
	out = append(out, branch[c])
	out = append(out, branch[start:c]...)
	return append(out, branch[c+1:]...)
}

// renderInitialMessages paints the resumed transcript after the loaded resources, without clearing either, and then the project-trust warning.
// Mirrors upstream renderInitialMessages (interactive-mode.ts:4168-4182), which ends with the compaction notice.
func (m *InteractiveMode) renderInitialMessages() {
	if m.opts.ResumePath != "" {
		m.renderSessionEntriesWithHistory(true)
	}
	m.renderProjectTrustWarningIfNeeded()
	m.showSessionCompactedStatus()
}

// showSessionCompactedStatus shows how many times the Session was compacted, counting every compaction entry in the file, not only the active
// branch. Only renderInitialMessages shows it (interactive-mode.ts:4178-4182): rebuilding the transcript after a compaction or a model switch does not.
func (m *InteractiveMode) showSessionCompactedStatus() {
	session := m.currentSession()
	if session == nil {
		return
	}
	count := session.CountEntriesOfType("compaction")
	if count == 0 {
		return
	}
	times := "1 time"
	if count > 1 {
		times = fmt.Sprintf("%d times", count)
	}
	m.showStatus("Session compacted " + times)
}

// repaintInitialMessages clears the transcript and paints the current Session's entries and then the project-trust warning, as upstream's
// `chatContainer.clear(); renderInitialMessages()` does after a Session replacement and a tree navigation (interactive-mode.ts:2004-2005,
// 2201-2209, 5634-5635). rebuildChatFromSession alone is upstream's rebuildChatFromMessages, which paints no warning.
func (m *InteractiveMode) repaintInitialMessages() {
	m.rebuildChat(true)
	m.renderProjectTrustWarningIfNeeded()
	m.showSessionCompactedStatus()
}

// renderSessionEntries refreshes the footer name before painting the current Session, including an unnamed fork. Pi's footer.ts:124-128 reads the current SessionManager name rather than retaining the source name.
func (m *InteractiveMode) renderSessionEntries() {
	m.renderSessionEntriesWithHistory(false)
}

// renderSessionEntriesWithHistory is renderSessionEntries with upstream's populateHistory option: each rendered user message's text also goes to the
// editor history.
func (m *InteractiveMode) renderSessionEntriesWithHistory(populateHistory bool) {
	var entries []SessionEntry
	name := ""
	if session := m.currentSession(); session != nil {
		// The kept range and everything after it render the same from the context branch, and the entries before it hydrate the same state;
		// cache-miss notices scan the whole branch, and a compaction whose kept entry is not on the branch renders the whole branch.
		if m.showCacheMissNotices() || !session.KeptRangeOnBranch() {
			entries = session.GetBranch()
		} else {
			entries = session.GetContextBranch()
		}
		name = session.GetSessionName()
	}
	if m.statusLine != nil {
		m.statusLine.SetName(name)
	}
	m.renderSessionEntryListWithHistory(entries, true, populateHistory)
}

// addUserMessageToChat is addMessageToChat case "user" (interactive-mode.ts:3927-3963), shared by the live message_start path and the
// transcript rebuild: Spacer(1) after the first chat child, a skill block as a collapsible SkillInvocationMessageComponent followed by
// its trailing user message (#5371 spacer), otherwise the user message itself.
func (m *InteractiveMode) addUserMessageToChat(msg agent.AgentMessage, text string) {
	if !m.chatContainer.IsEmpty() {
		m.chatContainer.Add(tui.NewSpacer(1))
	}
	parsed := ParseSkillBlock(text)
	if parsed == nil {
		block := m.newUserMessageBlock(text)
		block.SetImages(userMessageImages(msg))
		m.chatContainer.Add(block)
		return
	}
	comp := tui.NewSkillInvocationMessageComponent(tui.ParsedSkillBlock{
		Name:    parsed.Name,
		Content: parsed.Content,
	}, m.markdownThemeWithSettings(), m.outputPad)
	if m.toolsExpanded {
		comp.SetExpanded(true)
	}
	m.chatContainer.Add(comp)
	if parsed.UserMessage != "" {
		m.chatContainer.Add(tui.NewSpacer(1))
		block := m.newUserMessageBlock(parsed.UserMessage)
		block.SetImages(userMessageImages(msg))
		m.chatContainer.Add(block)
	}
}

func (m *InteractiveMode) renderSessionEntryList(entries []SessionEntry, trim bool) {
	m.renderSessionEntryListWithHistory(entries, trim, false)
}

func (m *InteractiveMode) renderSessionEntryListWithHistory(entries []SessionEntry, trim, populateHistory bool) {
	// Selection coordinates point into the transcript being replaced (upstream interactive-mode.ts renderSessionEntries, #9311).
	if m.altScreen != nil {
		m.altScreen.ResetTextSelection()
	}
	m.previousThinkingDroppedCount = 0
	m.disposeMarkdownBlocks()
	defer m.refreshFooterContextUsage()
	// Reset all component tracking: all are stale after a branch navigation.
	m.toolMu.Lock()
	clear(m.toolByID)
	clear(m.toolFileCalls)
	m.toolOrder = m.toolOrder[:0]
	m.toolStarts = make(map[string]time.Time)
	m.toolMu.Unlock()
	m.bashOrder = m.bashOrder[:0]
	m.assistantBlocks = m.assistantBlocks[:0]
	m.userBlocks = m.userBlocks[:0]
	m.compactionOrder = m.compactionOrder[:0]
	m.branchSummaryOrder = m.branchSummaryOrder[:0]
	m.customMessageOrder = m.customMessageOrder[:0]

	// Hydrate usage and count compactions in one pass over the branch.
	// Previously this did three separate passes (two Entries() copies +
	// one Branch() walk), each converting every message again. For
	// sessions with hundreds of entries, the redundant conversion was the
	// main source of post-compact/post-tree latency.
	if m.statusLine != nil {
		m.statusLine.ResetContextUsage()
	}

	// Pre-parse branch entries once (parsedEntry caches the
	// WireToMessage conversion). Walked twice below (usage/compaction
	// scan, then render) without converting twice.
	branch := make([]parsedEntry, 0, len(entries))
	sess := m.currentSession()
	for _, e := range entries {
		pe := parsedEntry{entry: e}
		switch e.Base().Type {
		case "message":
			decode := asMessage
			if sess != nil {
				decode = sess.messageFor
			}
			if me, ok := decode(e); ok {
				if me.Message.Assistant != nil {
					m.previousThinkingDroppedCount = countDroppedThinkingBlocks(me.Message.Assistant)
					if m.statusLine != nil && me.Message.Assistant.Usage != nil {
						m.statusLine.SetTurnContextUsage(me.Message.Assistant.Usage)
					}
				}
				pe.agent, pe.hasAgent = me.Message.Clone(), true
			}
		case "compaction":
			// The latest summarization usage also sets the context column;
			// token and cost totals come from the session accounting.
			if m.statusLine != nil {
				var ce CompactionEntry
				if json.Unmarshal(e.Raw(), &ce) == nil && ce.Usage != nil {
					m.statusLine.SetTurnContextUsage(ce.Usage)
				}
			}
		case "branch_summary":
			if m.statusLine != nil {
				var bs BranchSummaryEntry
				if json.Unmarshal(e.Raw(), &bs) == nil && bs.Usage != nil {
					m.statusLine.SetTurnContextUsage(bs.Usage)
				}
			}
		}
		branch = append(branch, pe)
	}

	cacheMissNotices := make(map[string]string)
	if m.showCacheMissNotices() {
		// upstream collectCacheMisses(sessionManager.getEntries()): every entry of the file in file order, not only the rendered branch, so a
		// response compares with the request before it in the file, as the live notice does. The branch's messages keep their decoded copies.
		branchMessages := make(map[string]*agent.AssistantMessage, len(branch))
		for _, item := range branch {
			if item.hasAgent {
				branchMessages[item.entry.Base().ID] = item.agent.Assistant
			}
		}
		scanned := entries
		if sess != nil {
			scanned = sess.GetEntries()
		}
		statsEntries := make([]cacheStatsEntry, 0, len(scanned))
		for _, e := range scanned {
			entry := cacheStatsEntry{kind: e.Base().Type}
			switch entry.kind {
			case "message":
				if message, ok := branchMessages[e.Base().ID]; ok {
					entry.message = message
				} else if sess != nil {
					if me, ok := sess.messageFor(e); ok {
						entry.message = me.Message.Assistant
					}
				}
			case "usage":
				if usage, ok := decodeUsageEntry(e.Raw()); ok {
					entry.usage = &usage
				}
			}
			statsEntries = append(statsEntries, entry)
		}
		var prices ModelPriceSource
		if m.opts.ModelRegistry != nil {
			prices = m.opts.ModelRegistry.CacheReadPrice
		}
		misses := collectCacheMisses(statsEntries, prices)
		for _, item := range branch {
			if item.hasAgent && item.agent.Assistant != nil {
				message := item.agent.Assistant
				if message.StopReason != "aborted" && message.StopReason != "error" {
					cacheMissNotices[item.entry.Base().ID] = formatCacheMissNotice(misses[message])
				}
			}
		}
	}

	// Match upstream renderInitialMessages, which renders
	// buildSessionContext().messages: the aligned context where
	// everything before the latest compaction's kept tail collapses to
	// that compaction's summary (interactive-mode.ts:3221; session-manager.ts
	// buildSessionContext). Pig previously walked the full branch and
	// re-rendered the pre-compaction history that compaction summarized
	// away: wrong visually (shows hidden history) and slow (the extra
	// lines inflate the single synchronized-update write, which some
	// terminals process super-linearly).
	//
	// The kept tail begins at the latest compaction's FirstKeptEntryID
	// (which precedes the compaction node), mirroring BuildContext's
	// foundFirstKept logic. An empty FirstKeptEntryID keeps all prior
	// history (renderStart stays 0).
	renderStart := 0
	if trim {
		renderStart = compactionTrimIndex(branch)
	}
	rendered := branch[renderStart:]
	if trim {
		// Pi paints a restored transcript from buildContextEntries, whose first entry is the latest compaction (interactive-mode.ts:4220-4223);
		// only the live compaction_end rebuild appends the new summary last (:3708-3721), and renderCompactionResult does that.
		rendered = contextRenderOrder(branch, renderStart)
	}

	pendingCalls := make(map[string]ai.ToolCall)
	// renderMessage renders one AgentMessage (user, assistant, toolResult, bashExecution)
	// into the chat container with all its sub-components.
	// Extracted so it can be called from both the session-entry path
	// (primary) and the agent-message fallback path.
	renderMessage := func(msg agent.AgentMessage) {
		switch {
		case msg.User != nil:
			if text := extractAgentMessageText(msg); text != "" {
				m.addUserMessageToChat(msg, text)
				if populateHistory && m.editor != nil {
					m.editor.AddToHistory(text)
				}
			}

		case msg.Assistant != nil:
			// Render from the persisted content blocks, as upstream
			// addMessageToChat → AssistantMessageComponent does. The runtime
			// AssistantMessage.Thinking accumulator is not serialized, so it
			// is empty for every message read back from the session file.
			block := m.newAssistantMessageBlock()
			// A message read back never streams.
			block.FinishStreaming()
			updateAssistantMessageBlock(block, msg.Assistant)
			m.chatContainer.Add(block)
			m.assistantBlocks = append(m.assistantBlocks, block)
			// Tool-use blocks.
			for _, b := range msg.Assistant.Content {
				call, ok := b.(ai.ToolCall)
				if !ok {
					continue
				}
				args, _ := jsonNoEscape(call.Arguments)
				comp := m.newToolCard(call.Name, call.ID, json.RawMessage(args))
				comp.BodyRenderer = toolBodyRendererForCall(call, agent.AgentToolResult{}, nil)
				m.applyToolPresentation(comp, call.ID, call.Name, json.RawMessage(args))
				comp.SetExpanded(m.toolsExpanded)
				switch msg.Assistant.StopReason {
				case ai.StopReasonAborted:
					abortedText := m.abortedMessageText()
					result := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: abortedText}}, IsError: true}
					comp.BodyRenderer = toolBodyRendererForCall(call, result, nil)
					comp.SetResultValue(result)
					comp.SetResult(abortedText, true, 0)
					comp.MarkAborted() // pig additive (D91): a frontend shows it cancelled
				case ai.StopReasonError:
					errorMessage := msg.Assistant.ErrorMessage
					if errorMessage == "" {
						errorMessage = "Error"
					}
					result := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: errorMessage}}, IsError: true}
					comp.BodyRenderer = toolBodyRendererForCall(call, result, nil)
					comp.SetResultValue(result)
					comp.SetResult(errorMessage, true, 0)
				default:
					pendingCalls[call.ID] = call
				}
				m.toolMu.Lock()
				m.toolByID[call.ID] = comp
				m.toolOrder = append(m.toolOrder, comp)
				m.toolMu.Unlock()
				m.chatContainer.Add(comp)
			}

		case msg.Role() == agent.RoleBashExecution:
			// Upstream addMessageToChat case "bashExecution" (interactive-mode.ts:3870-3887): a BashExecutionComponent over the
			// command, its output appended, then completed with the exit code, cancelled and truncated flags and full-output path.
			command, _ := msg.Custom["command"].(string)
			excludeFromContext, _ := msg.Custom["excludeFromContext"].(bool)
			block := tui.NewBashExecutionComponent(command, m.tuiInst, excludeFromContext, m.outputPad)
			if output, _ := msg.Custom["output"].(string); output != "" {
				block.AppendOutput(output)
			}
			var exitCode *int
			if code, ok := msg.Custom["exitCode"].(float64); ok {
				value := int(code)
				exitCode = &value
			}
			cancelled, _ := msg.Custom["cancelled"].(bool)
			truncated, _ := msg.Custom["truncated"].(bool)
			fullOutputPath, _ := msg.Custom["fullOutputPath"].(string)
			var truncationResult *tui.TruncationResult
			if truncated {
				truncationResult = &tui.TruncationResult{Truncated: true}
			}
			block.SetComplete(exitCode, cancelled, truncationResult, fullOutputPath)
			m.toolMu.Lock()
			m.bashOrder = append(m.bashOrder, block)
			m.toolMu.Unlock()
			// upstream: interactive-mode.ts:3871,7009,7037 never call setExpanded on a new BashExecutionComponent; it starts collapsed.
			m.chatContainer.Add(block)
		case msg.Role() == agent.RoleCustom:
			// upstream addMessageToChat case "custom" (interactive-mode.ts:3901-3913): only a displayed message is drawn.
			if entry, display := customMessageEntryOf(msg.Custom); display {
				m.appendCustomMessage(entry)
			}
		case msg.Role() == agent.RoleCompactionSummary:
			// upstream case "compactionSummary" (interactive-mode.ts:3915-3924): a message carries no usage, so no cost notice follows.
			summary, _ := msg.Custom["summary"].(string)
			tokensBefore, _ := msg.Custom["tokensBefore"].(float64)
			m.addCompactionSummary(summary, int(tokensBefore), nil)
		case msg.Role() == agent.RoleBranchSummary:
			// upstream case "branchSummary" (interactive-mode.ts:3926-3935).
			summary, _ := msg.Custom["summary"].(string)
			m.addBranchSummary(summary, nil)
		case msg.ToolResult != nil:
			r := msg.ToolResult
			call, pending := pendingCalls[r.ToolCallID]
			if !pending {
				return
			}
			delete(pendingCalls, r.ToolCallID)
			m.toolMu.Lock()
			comp := m.toolByID[r.ToolCallID]
			delete(m.toolByID, r.ToolCallID)
			m.toolMu.Unlock()
			if comp == nil {
				return
			}
			result := r.Result()
			comp.BodyRenderer = toolBodyRendererForCall(call, result, recordedTook(call.Name, r.DurationMs, nil))
			final := ToolExecutionResultOf(result, 0)
			final.IsError = r.IsError
			final.DurationMs = r.DurationMs
			comp.UpdateResult(final, false)
		}
	}

	// Render from pre-parsed branch entries (from the latest compaction
	// boundary onward; see renderStart above).
	for _, pe := range rendered {
		switch pe.entry.Base().Type {
		case "message":
			if pe.hasAgent {
				renderMessage(pe.agent)
				if notice := cacheMissNotices[pe.entry.Base().ID]; notice != "" {
					m.addCacheMissNotice(notice)
				}
			}
		case "compaction":
			var ce CompactionEntry
			if err := json.Unmarshal(pe.entry.Raw(), &ce); err == nil {
				m.addCompactionSummary(ce.Summary, ce.TokensBefore, ce.Usage)
			}
		case "branch_summary":
			var be BranchSummaryEntry
			if err := json.Unmarshal(pe.entry.Raw(), &be); err == nil && be.Summary != "" {
				m.addBranchSummary(be.Summary, be.Usage)
			}
		case "custom_message":
			var cm CustomMessageEntry
			if err := json.Unmarshal(pe.entry.Raw(), &cm); err == nil && cm.Display {
				m.appendCustomMessage(cm)
			}
		case "custom":
			var ce CustomEntry
			if err := json.Unmarshal(pe.entry.Raw(), &ce); err == nil {
				m.addCustomEntryToChat(ce)
			}
		case "usage":
			if usage, ok := decodeUsageEntry(pe.entry.Raw()); ok && usage.Kind == "cache_warm" {
				m.addCacheWarmingUsage(usage)
			}
		}
	}
	if len(branch) == 0 && trim && m.agent != nil {
		// Fallback: no session available: render from agent messages only.
		for _, msg := range m.agent.Messages() {
			renderMessage(msg.Clone())
		}
	}
	m.tuiInst.Render()
}

// addCustomEntryToChat renders a custom session entry (appended via the
// appendEntry host action) using the extension-registered entry renderer, if
// any. Mirrors upstream interactive-mode.ts addCustomEntryToChat: with no
// registered renderer the entry is not shown. The returned proxy shares the
// message renderer's off-loop, single-flight IPC core, so expand toggles and
// width changes re-render without blocking the TUI loop.
func (m *InteractiveMode) addCustomEntryToChat(entry CustomEntry) {
	if m.newRunner == nil {
		return
	}
	renderer := m.newRunner.EntryRenderer(entry.CustomType)
	if renderer == nil {
		return
	}
	component := NewCustomEntryComponent(entry, renderer, m.outputPad)
	component.SetExpanded(m.toolsExpanded)
	if !component.HasContent() {
		return
	}
	// interactive-mode.ts:3834-3840: an entry added during streaming goes before the streaming message.
	if m.evCurrentBlock != nil && m.chatContainer.InsertBefore(m.evCurrentBlock, component) {
		return
	}
	// setAllToolsExpanded reaches the component through chatContainer's expandable children.
	m.chatContainer.Add(component)
}

// appendCustomMessage mounts a CustomMessageComponent for a displayed custom message, as upstream interactive-mode.ts:3861-3872
// does: the registered renderer (if any) is handed to the component, which rebuilds it when expansion or padding changes.
func (m *InteractiveMode) appendCustomMessage(message CustomMessageEntry) {
	var renderer tui.MessageRenderer
	if m.newRunner != nil {
		if registered := m.newRunner.MessageRenderer(message.CustomType); registered != nil {
			customMessage := extension.CustomMessage{
				CustomType: message.CustomType,
				Content:    message.Content,
				Display:    message.Display,
				Details:    message.Details,
				Timestamp:  entryTimestampMs(message.Timestamp),
			}
			renderer = func(_ *tui.CustomMessage, options tui.MessageRenderOptions) tui.Component {
				return registered(customMessage, extension.MessageRenderOptions{Expanded: options.Expanded, OutputPad: options.OutputPad}, nil)
			}
		}
	}
	component := tui.NewCustomMessageComponent(&tui.CustomMessage{CustomType: message.CustomType, Content: message.Content}, renderer, m.markdownThemeWithSettings(), m.outputPad)
	component.SetExpanded(m.toolsExpanded)
	m.customMessageOrder = append(m.customMessageOrder, component)
	m.chatContainer.Add(component)
}

// customMessageEntryOf reads a "custom" role message as the entry appendCustomMessage draws. display is the message's
// display member, which decides whether upstream addMessageToChat case "custom" draws it.
func customMessageEntryOf(custom map[string]any) (entry CustomMessageEntry, display bool) {
	display, _ = custom["display"].(bool)
	customType, _ := custom["customType"].(string)
	// A message built in this process holds an int64 timestamp; one decoded from JSON holds a float64.
	var timestamp int64
	switch value := custom["timestamp"].(type) {
	case int64:
		timestamp = value
	case float64:
		timestamp = int64(value)
	}
	return CustomMessageEntry{
		SessionEntryBase: SessionEntryBase{Type: "custom_message", Timestamp: time.UnixMilli(timestamp).UTC().Format(time.RFC3339Nano)},
		CustomType:       customType, Content: custom["content"], Display: display, Details: custom["details"],
	}, display
}

// addBranchSummary draws a branch summary as upstream addMessageToChat case "branchSummary" (interactive-mode.ts:3926-3935)
// does: a spacer, then the component at the current expansion. A branch_summary entry's usage adds its cost notice.
func (m *InteractiveMode) addBranchSummary(summary string, usage *ai.Usage) {
	component := tui.NewBranchSummaryMessageComponent(tui.BranchSummaryMessage{Summary: summary}, m.markdownThemeWithSettings(), m.outputPad)
	component.SetExpanded(m.toolsExpanded)
	m.branchSummaryOrder = append(m.branchSummaryOrder, component)
	m.chatContainer.Add(tui.NewSpacer(1))
	m.chatContainer.Add(component)
	m.addCompactionCostNotice("branch_summary", usage)
}

// flushCompactionQueue sends all messages that were queued while a compaction
// was in progress. Called at the end of successful CompactionEndEvent handling.
//
// Upstream: flushCompactionQueue in interactive-mode.ts:3561.
// Now uses the agent's steering queue so compaction-queued messages are
// injected between tool batches rather than spawning new turns.
// flushCompactionQueue replays messages queued while compaction was running.
//
// startTurn distinguishes the two call sites:
//   - false (drain at handleSubmit top): a new prompt or an active overflow
//     retry will start a turn, so the queued messages only need to be steered
//     into it.
//   - true (CompactionEndEvent): the agent is idle, so a bare Steer would be
//     dropped (the steering queue is only drained by a running loop). The first
//     non-slash message starts a turn; slash commands dispatch; the remaining
//     messages steer into the started turn. Mirrors upstream flushCompactionQueue
//     (interactive-mode.ts:3825), which uses the first non-extension-command
//     message as session.prompt() and steers the rest.
func (m *InteractiveMode) flushCompactionQueue(ctx context.Context, startTurn bool) {
	if len(m.compactionQueue) == 0 {
		return
	}
	queued := m.compactionQueue
	m.compactionQueue = nil
	// The queue moved to the agent (steered) or a new turn; clear its
	// entries from the pending display so it reflects the live queue.
	defer m.updatePendingMessagesDisplay()

	// Steer only when a run goroutine actually exists to drain the steering
	// queue. turnActive marks exactly that interval; isIdle is reset through a
	// queued runOnMain and so reads stale-false right after a turn ends, which
	// is precisely the state auto-compaction leaves behind when it runs at the
	// end of a turn. Gating on isIdle therefore steered into a queue nothing
	// would drain: the message vanished from the UI, never reached the session,
	// and was lost on restart.
	//
	// When startTurn is false (the handleSubmit drain) the caller has its own
	// turn to steer into. Starting a second concurrent turn while one is running
	// would race the agent loop, so turnActive still suppresses that.
	if !startTurn {
		for _, msg := range queued {
			m.queueCompactionMessageForActiveTurn(msg)
		}
		return
	}
	// A run still settling (the compaction ran inside it) receives each
	// message through upstream's steer()/followUp(), input handlers and
	// expansion included; extension commands run immediately.
	if m.runStreaming() {
		for _, msg := range queued {
			m.deliverCompactionQueued(ctx, msg)
		}
		return
	}

	// A successful or aborted compaction leaves the agent idle. Refresh the
	// abort context so the new turn runs under a live (non-cancelled) context
	// even when compaction was cancelled via Esc.
	if m.abortFn != nil {
		m.abortFn()
	}
	m.abortCtx, m.abortFn = context.WithCancel(m.runCtx)

	firstIdx := -1
	for i, msg := range queued {
		if !m.isExtensionCommand(msg.text) {
			firstIdx = i
			break
		}
	}
	if firstIdx == -1 {
		// All extension commands (interactive-mode.ts:4818-4825): run each; none starts a turn.
		for _, msg := range queued {
			m.handleSubmitWithImages(ctx, msg.text, msg.images)
		}
		return
	}
	// Run the extension commands before the first prompt.
	for _, msg := range queued[:firstIdx] {
		m.handleSubmitWithImages(ctx, msg.text, msg.images)
	}
	// Preserve the queued message's delivery mode. Upstream passes
	// streamingBehavior on the first prompt so a still-settling agent run queues
	// it instead of starting a concurrent turn. turnActive is the same boundary
	// in PiG; the idle path starts a new prompt, while a live run receives the
	// message through its requested queue.
	m.handleSubmitWithImages(ctx, queued[firstIdx].text, queued[firstIdx].images)
	// Remaining messages preserve their queued mode; later extension commands
	// still dispatch immediately (deliverCompactionQueued). A message that finds the run already
	// settled starts the next turn instead of waiting in a drained queue.
	for _, msg := range queued[firstIdx+1:] {
		m.deliverCompactionQueued(ctx, msg)
	}
}

// deliverCompactionQueued hands one message queued during compaction to the
// active run with its queued mode, as upstream's flushCompactionQueue does
// through prompt() for extension commands and steer()/followUp() otherwise.
func (m *InteractiveMode) deliverCompactionQueued(ctx context.Context, msg compactionQueuedMessage) {
	if m.isExtensionCommand(msg.text) {
		m.dispatchSlash(ctx, msg.text)
		return
	}
	m.promptUserInput(ctx, msg.text, msg.images, msg.mode == compactionQueueFollowUp, extension.InputSourceUser, true)
}

// enqueueIfTurnActive runs enqueue and reports true when a run is active (a
// turn, or the agent streaming a run it drains itself), or reports false
// without running it so the caller starts a new turn. queueMu
// is held across the check and enqueue, so settleTurn either sees the queued
// message or has already ended the run, never neither.
func (m *InteractiveMode) enqueueIfTurnActive(enqueue func()) bool {
	m.queueMu.Lock()
	defer m.queueMu.Unlock()
	if !m.runStreaming() {
		return false
	}
	enqueue()
	return true
}

func (m *InteractiveMode) queueCompactionMessageForActiveTurn(msg compactionQueuedMessage) {
	if msg.mode == compactionQueueFollowUp {
		m.followUpMessageWithImages(msg.text, msg.images)
		return
	}
	m.steerMessageWithImages(msg.text, msg.images)
}

// steerMessageWithImages queues a steering message, delivered after the
// current tool batch and before the next model call (upstream _queueSteer). Like Pi's onSubmit (interactive-mode.ts:3378-3383) it shows the
// message in the pending area only, with no status line.
func (m *InteractiveMode) steerMessageWithImages(text string, images []ai.ImageContent) {
	msg := agent.AgentMessage{
		User: &agent.UserMessage{
			Role:      agent.RoleUser,
			Content:   promptContent(text, images),
			Timestamp: time.Now().UnixMilli(),
		},
	}
	m.agent.Steer(msg)
	m.updatePendingMessagesDisplay()
}

// followUpMessageWithImages queues a follow-up message, delivered once the
// run has no more tool calls or steering messages (upstream _queueFollowUp). Like Pi's handleFollowUp (interactive-mode.ts:4493-4498) it
// shows the message in the pending area only, with no status line.
func (m *InteractiveMode) followUpMessageWithImages(text string, images []ai.ImageContent) {
	msg := agent.AgentMessage{
		User: &agent.UserMessage{
			Role:      agent.RoleUser,
			Content:   promptContent(text, images),
			Timestamp: time.Now().UnixMilli(),
		},
	}
	m.agent.FollowUp(msg)
	m.updatePendingMessagesDisplay()
}

// updatePendingMessagesDisplay populates the persistent pending-messages
// container with one line per queued steering/follow-up message and a
// dequeue hint. Mirrors upstream interactive-mode.ts:3730-3746.
func (m *InteractiveMode) updatePendingMessagesDisplay() {
	if m.pendingMessagesContainer == nil || m.agent == nil {
		return
	}
	m.pendingMessagesContainer.Clear()
	steering, followUps := m.agent.PendingMessages()

	// Merge messages typed during an in-flight /compact so the display and
	// the Alt+Up dequeue path operate on the same set of queued messages.
	steeringTexts, followUpTexts := collectQueuedTexts(steering, followUps, m.compactionQueue)

	if len(steeringTexts) == 0 && len(followUpTexts) == 0 {
		m.tuiInst.Render()
		return
	}
	theme := tui.ActiveTheme()
	m.pendingMessagesContainer.Add(tui.NewSpacer(1))
	// Upstream wraps each line in TruncatedText(text, 1, 0): a queued
	// message renders as a single truncated line, never wrapped.
	for _, text := range steeringTexts {
		m.pendingMessagesContainer.Add(tui.NewTruncatedText(theme.Fg("dim", "Steering: "+text), 1, 0))
	}
	for _, text := range followUpTexts {
		m.pendingMessagesContainer.Add(tui.NewTruncatedText(theme.Fg("dim", "Follow-up: "+text), 1, 0))
	}
	// upstream: interactive-mode.ts:4736 getAppKeyDisplay is keyDisplayText: every key bound to the action, capitalized and joined by "/".
	hint := tui.ActionKeyDisplayText("app.message.dequeue")
	m.pendingMessagesContainer.Add(tui.NewTruncatedText(theme.Fg("dim", "↳ "+hint+" to edit all queued messages"), 1, 0))
	m.tuiInst.Render()
}

// extractAgentMessageText is getUserMessageText (interactive-mode.ts): the text blocks of a user message joined with "".
func extractAgentMessageText(msg agent.AgentMessage) string {
	if msg.User == nil {
		return ""
	}
	var text strings.Builder
	for _, c := range msg.ContentBlocks() {
		if tc, ok := c.(ai.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	return text.String()
}

// userMessageImages are the image blocks of a user message with data and a
// MIME type, in order, or nil. Pi's UserMessageComponent draws none; a
// frontend session may show them with the message (pig additive, D91).
func userMessageImages(msg agent.AgentMessage) []tui.ImageBlock {
	if msg.User == nil {
		return nil
	}
	blocks, _ := msg.User.Content.(ai.UserContentBlocks)
	var images []tui.ImageBlock
	for _, block := range blocks {
		if img, ok := block.(ai.ImageContent); ok && img.Data != "" && img.MimeType != "" {
			images = append(images, tui.ImageBlock{Data: img.Data, MIMEType: img.MimeType})
		}
	}
	return images
}

// collectQueuedTexts merges the agent's steering/follow-up queues with the
// messages typed during an in-flight compaction (m.compactionQueue), preserving
// upstream ordering: session queue first, then compaction-queued messages, each
// kept in its own steering/follow-up bucket. Mirrors upstream getAllQueuedMessages
// (interactive-mode.ts:3763-3772). Pure so the merge is unit-testable.
//
// Pi lists only typed input: its session records the text of each user message it queues (agent-session.ts:2191-2210,
// _queueSteer/_queueFollowUp), while an extension's custom message goes to the agent's queue alone
// (sendCustomMessage, :2246-2266). A queued custom message therefore has no row and is not restored to the editor.
func collectQueuedTexts(steering, followUps []agent.AgentMessage, compaction []compactionQueuedMessage) (steeringTexts, followUpTexts []string) {
	steeringTexts = make([]string, 0, len(steering)+len(compaction))
	for _, msg := range steering {
		if msg.User != nil {
			steeringTexts = append(steeringTexts, extractAgentMessageText(msg))
		}
	}
	followUpTexts = make([]string, 0, len(followUps)+len(compaction))
	for _, msg := range followUps {
		if msg.User != nil {
			followUpTexts = append(followUpTexts, extractAgentMessageText(msg))
		}
	}
	for _, msg := range compaction {
		if msg.mode == compactionQueueFollowUp {
			followUpTexts = append(followUpTexts, msg.text)
		} else {
			steeringTexts = append(steeringTexts, msg.text)
		}
	}
	return steeringTexts, followUpTexts
}

// jsonNoEscape is JSON.stringify(v) for display strings.
func jsonNoEscape(v any) (string, error) {
	encoded, err := jsstring.MarshalJSON(v)
	if err != nil {
		return "{}", err
	}
	return string(encoded), nil
}

func (m *InteractiveMode) refreshFooterContextUsage() {
	if m.statusLine == nil || m.opts.ContextUsage == nil {
		return
	}
	tokens, window := m.opts.ContextUsage()
	m.statusLine.SetContextUsage(tokens, window)
}

// applyOutputPad updates every transcript block that carries the horizontal padding in place, in the chat and in the pending-message area, then
// requests a render (interactive-mode.ts onOutputPadChange). The transcript is not rebuilt.
func (m *InteractiveMode) applyOutputPad() {
	type outputPadded interface{ SetOutputPad(int) }
	apply := func(component tui.Component) {
		if padded, ok := component.(outputPadded); ok {
			padded.SetOutputPad(m.outputPad)
		}
	}
	for _, container := range []*tui.Container{m.chatContainer, m.pendingMessagesContainer} {
		if container == nil {
			continue
		}
		for _, child := range container.Children() {
			apply(child)
		}
	}
	m.pendingBashBlocksMu.Lock()
	pending := slices.Clone(m.pendingBashBlocks)
	m.pendingBashBlocksMu.Unlock()
	for _, block := range pending {
		apply(block)
	}
	m.tuiInst.RequestRender()
}
