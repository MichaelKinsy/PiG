package codingagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

func (m *InteractiveMode) rebuildChatFromSession() {
	m.chatContainer.Clear()
	m.tuiInst.ForceFullRender()
	m.renderSessionEntries()
}

// renderSessionEntries renders session messages/entries into the chat
// container without clearing it first. Initial resume retains loaded resources;
// rebuildChatFromSession clears the transcript before calling it.
//
// Mirrors upstream renderInitialMessages (interactive-mode.ts:3097) for
// the append-only case and rebuildChatFromMessages (interactive-mode.ts:3346)
// for the clear-first case (via rebuildChatFromSession).
// parsedEntry caches the expensive AsMessage()/WireToMessage conversion
// for one session entry so renderSessionEntries can walk the branch for
// usage hydration and rendering without re-unmarshaling raw JSONL.
type parsedEntry struct {
	entry SessionEntry
	msg   *MessageEntry       // nil unless type=message and parse ok
	agent *agent.AgentMessage // nil unless type=message and parse ok
}

// compactionTrimIndex returns the index into a root->leaf branch where
// resume rendering should begin so the view matches the aligned context
// (buildSessionContext): everything before the latest compaction's kept
// tail collapses to that compaction's summary. The kept tail starts at the
// latest compaction's FirstKeptEntryID (which precedes the compaction
// node); an empty/absent FirstKeptEntryID keeps all prior history (0).
// Mirrors session.go BuildContext foundFirstKept logic.
func compactionTrimIndex(branch []parsedEntry) int {
	lastCompactionIdx := -1
	for i := range branch {
		if branch[i].entry.Base.Type == "compaction" {
			lastCompactionIdx = i
		}
	}
	if lastCompactionIdx < 0 {
		return 0
	}
	var ce CompactionEntry
	if err := json.Unmarshal(branch[lastCompactionIdx].entry.Raw(), &ce); err != nil || ce.FirstKeptEntryID == "" {
		return 0
	}
	for i := 0; i < lastCompactionIdx; i++ {
		if branch[i].entry.Base.ID == ce.FirstKeptEntryID {
			return i
		}
	}
	return 0
}

func (m *InteractiveMode) renderSessionEntries() {
	defer m.refreshFooterContextUsage()
	// Reset all component tracking: all are stale after a branch navigation.
	m.toolMu.Lock()
	clear(m.toolByID)
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
	// one Branch() walk), each calling AsMessage() which re-unmarshals
	// the raw JSON. For sessions with hundreds of entries, the redundant
	// unmarshal was the main source of post-compact/post-tree latency.
	if m.statusLine != nil {
		m.statusLine.ResetContextUsage()
	}

	// Pre-parse branch entries once (parsedEntry caches the expensive
	// AsMessage()/WireToMessage conversion). Walked twice below
	// (usage/compaction scan, then render) without re-unmarshaling.
	var branch []parsedEntry
	var compactionCount int

	if m.currentSession() != nil {
		leafID := m.currentSession().LeafID()
		if leafID != nil {
			sess := m.currentSession()
			for _, e := range sess.Branch(*leafID) {
				pe := parsedEntry{entry: e}
				switch e.Base.Type {
				case "message":
					if me, ok := sess.messageFor(e); ok {
						pe.msg = &me
						if m.statusLine != nil && me.Message.Assistant != nil && me.Message.Assistant.Usage != nil {
							m.statusLine.SetTurnContextUsage(me.Message.Assistant.Usage)
						}
						message := me.Message.Clone()
						pe.agent = &message
					}
				case "compaction":
					compactionCount++
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
		}
	}

	if m.statusLine != nil && compactionCount > 0 {
		times := "1 time"
		if compactionCount > 1 {
			times = fmt.Sprintf("%d times", compactionCount)
		}
		m.statusLine.Flash(fmt.Sprintf("Session compacted %s", times), 3*time.Second)
	}

	cacheMissNotices := make(map[string]string)
	if m.showCacheMissNotices() {
		var previous *previousCacheRequest
		for _, item := range branch {
			switch item.entry.Base.Type {
			case "compaction", "branch_summary":
				previous = nil
			case "usage":
				if usage, ok := decodeUsageEntry(item.entry.Raw()); ok && usage.Kind == "cache_warm" {
					previous = cacheWarmPreviousRequest(usage.Provider, usage.Model, &usage.Usage, usage.Timestamp, previous)
				}
			case "message":
				if item.agent == nil || item.agent.Assistant == nil {
					continue
				}
				message := item.agent.Assistant
				if message.StopReason != "aborted" && message.StopReason != "error" {
					cacheMissNotices[item.entry.Base.ID] = formatCacheMissNotice(detectMiss(previous, message))
				}
				if next := asPreviousCacheRequest(message, previous != nil && previous.reportedCache); next != nil {
					previous = next
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
	renderStart := compactionTrimIndex(branch)

	pendingCalls := make(map[string]ai.ToolCall)
	// renderMessage renders one AgentMessage (user, assistant, toolResult)
	// into the chat container with all its sub-components.
	// Extracted so it can be called from both the session-entry path
	// (primary) and the agent-message fallback path.
	renderMessage := func(msg agent.AgentMessage) {
		switch {
		case msg.User != nil:
			var sb strings.Builder
			for _, block := range msg.User.Content {
				if tc, ok := block.(ai.TextContent); ok {
					sb.WriteString(tc.Text)
				}
			}
			if text := strings.TrimSpace(sb.String()); text != "" {
				// Upstream addMessageToChat case "user" spaces every user
				// message after the first chat child with Spacer(1); the
				// block's own vertical padding supplies the rest.
				if !m.chatContainer.IsEmpty() {
					m.chatContainer.Add(tui.NewSpacer(1))
				}
				// Parse skill blocks in user messages.
				// Mirrors upstream addMessageToChat case "user" which calls
				// parseSkillBlock(textContent) → SkillInvocationMessageComponent.
				if parsed := ParseSkillBlock(text); parsed != nil {
					comp := tui.NewSkillInvocationMessage(tui.ParsedSkillBlock{
						Name:    parsed.Name,
						Content: parsed.Content,
					})
					if m.toolsExpanded {
						comp.SetExpanded(true)
					}
					m.chatContainer.Add(comp)
					// Render trailing user message if present. Upstream #5371
					// (interactive-mode.ts) inserts a Spacer(1) between the skill
					// block and the user message.
					if parsed.UserMessage != "" {
						m.chatContainer.Add(tui.NewSpacer(1))
						m.chatContainer.Add(m.newUserMessageBlock(parsed.UserMessage))
					}
				} else {
					m.chatContainer.Add(m.newUserMessageBlock(text))
				}
			}

		case msg.Assistant != nil:
			// Render from the persisted content blocks, as upstream
			// addMessageToChat → AssistantMessageComponent does. The runtime
			// AssistantMessage.Thinking accumulator is not serialized, so it
			// is empty for every message read back from the session file.
			block := m.newAssistantMessageBlock()
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
				argsPreview := tui.HeaderForTool(call.Name, json.RawMessage(args), m.opts.CWD)
				comp := tui.NewToolExecutionComponent(call.Name, argsPreview)
				comp.BodyRenderer = toolBodyRendererForCall(call, agent.AgentToolResult{})
				comp.Cwd = m.opts.CWD
				comp.SetHeaderArgs(json.RawMessage(args))
				m.applyToolPresentation(comp, call.ID, call.Name, json.RawMessage(args))
				comp.SetExpanded(m.toolsExpanded)
				switch msg.Assistant.StopReason {
				case ai.StopReasonAborted:
					comp.BodyRenderer = toolBodyRendererForCall(call, agent.AgentToolResult{Content: "Operation aborted", IsError: true})
					comp.SetResultValue(agent.AgentToolResult{Content: "Operation aborted", IsError: true})
					comp.SetResult("Operation aborted", true, 0)
				case ai.StopReasonError:
					errorMessage := msg.Assistant.ErrorMessage
					if errorMessage == "" {
						errorMessage = "Error"
					}
					comp.BodyRenderer = toolBodyRendererForCall(call, agent.AgentToolResult{Content: errorMessage, IsError: true})
					comp.SetResultValue(agent.AgentToolResult{Content: errorMessage, IsError: true})
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
			images := r.Images()
			result := agent.AgentToolResult{Content: r.Text(), Details: r.Details, IsError: r.IsError, Images: images}
			comp.BodyRenderer = toolBodyRendererForCall(call, result)
			if len(images) > 0 {
				blocks := make([]tui.ImageBlock, len(images))
				for i, img := range images {
					blocks[i] = tui.ImageBlock{Data: img.Data, MIMEType: img.MimeType}
				}
				comp.ImageBlocks = blocks
			}
			comp.SetResultValue(result)
			comp.SetResult(r.Text(), r.IsError, 0)
			m.maybeConvertImagesForKitty(comp)
		}
	}

	// Render from pre-parsed branch entries (from the latest compaction
	// boundary onward; see renderStart above).
	for _, pe := range branch[renderStart:] {
		switch pe.entry.Base.Type {
		case "message":
			if pe.agent != nil {
				renderMessage(*pe.agent)
				if notice := cacheMissNotices[pe.entry.Base.ID]; notice != "" {
					m.chatContainer.Add(tui.NewText("\033[33m" + notice + "\033[0m"))
				}
			}
		case "compaction":
			// Render CompactionSummaryComponent at the boundary position.
			var ce CompactionEntry
			if err := json.Unmarshal(pe.entry.Raw(), &ce); err == nil && ce.Summary != "" {
				comp := tui.NewCompactionSummaryComponent(ce.Summary, ce.TokensBefore)
				if m.toolsExpanded {
					comp.SetExpanded(true)
				}
				m.compactionOrder = append(m.compactionOrder, comp)
				m.chatContainer.Add(tui.NewSpacer(1))
				m.chatContainer.Add(comp)
			}
		case "branch_summary":
			var be BranchSummaryEntry
			if err := json.Unmarshal(pe.entry.Raw(), &be); err == nil && be.Summary != "" {
				comp := tui.NewBranchSummaryComponent(be.Summary)
				if m.toolsExpanded {
					comp.SetExpanded(true)
				}
				m.branchSummaryOrder = append(m.branchSummaryOrder, comp)
				m.chatContainer.Add(tui.NewText(""))
				m.chatContainer.Add(comp)
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
	if len(branch) == 0 {
		// Fallback: no session available: render from agent messages only.
		for _, msg := range m.agent.Messages() {
			renderMessage(msg)
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
	component := renderer(entry, extension.EntryRenderOptions{Expanded: m.toolsExpanded}, nil)
	rendered, ok := component.(tui.Component)
	if !ok {
		return
	}
	// Host owns transcript spacing: one blank line above the entry, mirroring
	// upstream CustomEntryComponent's Spacer(1) (same as compaction/branch cases).
	m.chatContainer.Add(tui.NewSpacer(1))
	// Track expandable proxies (the subprocess renderer path) so Ctrl+O toggles
	// re-render them; a bare component still renders, just without expand tracking.
	if expandable, ok := rendered.(expandableCustomMessageComponent); ok {
		m.customMessageOrder = append(m.customMessageOrder, expandable)
	}
	m.chatContainer.Add(rendered)
}

func (m *InteractiveMode) appendCustomMessage(message CustomMessageEntry) {
	content := tui.CustomMessageText(&tui.CustomMessage{
		CustomType: message.CustomType,
		Content:    message.Content,
	})
	fallback := tui.NewCustomMessageComponent(message.CustomType, content)
	fallback.SetOutputPad(m.outputPad)
	fallback.SetExpanded(m.toolsExpanded)
	if m.newRunner != nil {
		if renderer := m.newRunner.MessageRenderer(message.CustomType); renderer != nil {
			customMessage := extension.CustomMessageRef{
				CustomType: message.CustomType,
				Content:    message.Content,
				Display:    message.Display,
				Details:    message.Details,
			}
			component := renderer(customMessage, extension.MessageRenderOptions{Expanded: m.toolsExpanded, OutputPad: m.outputPad}, nil)
			if rendered, ok := component.(tui.Component); ok && rendered != nil {
				if expandable, ok := rendered.(interface {
					expandableCustomMessageComponent
					SetOutputPad(int)
				}); ok {
					m.customMessageOrder = append(m.customMessageOrder, expandable)
					m.chatContainer.Add(expandable)
					return
				}
				wrapper := &rerenderingCustomMessageComponent{
					renderer: renderer, message: customMessage, expanded: m.toolsExpanded, outputPad: m.outputPad,
					component: rendered, fallback: fallback,
				}
				m.customMessageOrder = append(m.customMessageOrder, wrapper)
				m.chatContainer.Add(wrapper)
				return
			}
			wrapper := &rerenderingCustomMessageComponent{
				renderer: renderer, message: customMessage, expanded: m.toolsExpanded, outputPad: m.outputPad,
				component: fallback, fallback: fallback,
			}
			m.customMessageOrder = append(m.customMessageOrder, wrapper)
			m.chatContainer.Add(wrapper)
			return
		}
	}
	m.customMessageOrder = append(m.customMessageOrder, fallback)
	m.chatContainer.Add(fallback)
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
		if !strings.HasPrefix(msg.text, "/") {
			firstIdx = i
			break
		}
	}
	if firstIdx == -1 {
		// All slash commands: dispatch each; none starts a turn.
		for _, msg := range queued {
			m.handleSubmitWithImages(ctx, msg.text, msg.images)
		}
		return
	}
	// Dispatch any slash commands before the first prompt.
	for _, msg := range queued[:firstIdx] {
		m.handleSubmitWithImages(ctx, msg.text, msg.images)
	}
	// Preserve the queued message's delivery mode. Upstream passes
	// streamingBehavior on the first prompt so a still-settling agent run queues
	// it instead of starting a concurrent turn. turnActive is the same boundary
	// in PiG; the idle path starts a new prompt, while a live run receives the
	// message through its requested queue.
	m.handleSubmitWithImages(ctx, queued[firstIdx].text, queued[firstIdx].images)
	// Remaining messages preserve their queued mode; later slash commands
	// still dispatch immediately. A message that finds the run already
	// settled starts the next turn instead of waiting in a drained queue.
	for _, msg := range queued[firstIdx+1:] {
		if strings.HasPrefix(msg.text, "/") {
			m.handleSubmitWithImages(ctx, msg.text, msg.images)
			continue
		}
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
// current tool batch and before the next model call (upstream _queueSteer).
func (m *InteractiveMode) steerMessageWithImages(text string, images []ai.ImageContent) {
	msg := agent.AgentMessage{
		User: &agent.UserMessage{
			Role:      agent.RoleUser,
			Content:   promptContent(text, images),
			Timestamp: time.Now().UnixMilli(),
		},
	}
	m.agent.Steer(msg)
	if m.statusLine != nil {
		m.statusLine.Flash("Queued steering message", 2*time.Second)
	}
	m.updatePendingMessagesDisplay()
}

// followUpMessageWithImages queues a follow-up message, delivered once the
// run has no more tool calls or steering messages (upstream _queueFollowUp).
func (m *InteractiveMode) followUpMessageWithImages(text string, images []ai.ImageContent) {
	msg := agent.AgentMessage{
		User: &agent.UserMessage{
			Role:      agent.RoleUser,
			Content:   promptContent(text, images),
			Timestamp: time.Now().UnixMilli(),
		},
	}
	m.agent.FollowUp(msg)
	if m.statusLine != nil {
		m.statusLine.Flash("Queued follow-up message", 2*time.Second)
	}
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
	dim := tui.ActiveTheme().Muted
	reset := "\x1b[0m"
	m.pendingMessagesContainer.Add(tui.NewSpacer(1))
	// Upstream wraps each line in TruncatedText(text, 1, 0): a queued
	// message renders as a single truncated line, never wrapped.
	for _, text := range steeringTexts {
		m.pendingMessagesContainer.Add(tui.NewTruncatedText(dim+"Steering: "+text+reset, 1))
	}
	for _, text := range followUpTexts {
		m.pendingMessagesContainer.Add(tui.NewTruncatedText(dim+"Follow-up: "+text+reset, 1))
	}
	hint := m.keybindings.DisplayFor("app.message.dequeue")
	m.pendingMessagesContainer.Add(tui.NewTruncatedText(dim+"↳ "+hint+" to edit all queued messages"+reset, 1))
	m.tuiInst.Render()
}

// extractAgentMessageText pulls the first text content from an AgentMessage.
func extractAgentMessageText(msg agent.AgentMessage) string {
	if msg.User != nil {
		for _, c := range msg.User.Content {
			if tc, ok := c.(ai.TextContent); ok {
				return tc.Text
			}
		}
	}
	return ""
}

// collectQueuedTexts merges the agent's steering/follow-up queues with the
// messages typed during an in-flight compaction (m.compactionQueue), preserving
// upstream ordering: session queue first, then compaction-queued messages, each
// kept in its own steering/follow-up bucket. Mirrors upstream getAllQueuedMessages
// (interactive-mode.ts:3763-3772). Pure so the merge is unit-testable.
func collectQueuedTexts(steering, followUps []agent.AgentMessage, compaction []compactionQueuedMessage) (steeringTexts, followUpTexts []string) {
	steeringTexts = make([]string, 0, len(steering)+len(compaction))
	for _, msg := range steering {
		steeringTexts = append(steeringTexts, extractAgentMessageText(msg))
	}
	followUpTexts = make([]string, 0, len(followUps)+len(compaction))
	for _, msg := range followUps {
		followUpTexts = append(followUpTexts, extractAgentMessageText(msg))
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

// jsonNoEscape serialises v to JSON without HTML-escaping (for display strings).
func jsonNoEscape(v any) (string, error) {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "{}", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

func (m *InteractiveMode) refreshFooterContextUsage() {
	if m.statusLine == nil || m.opts.ContextUsage == nil {
		return
	}
	tokens, window := m.opts.ContextUsage()
	m.statusLine.SetContextUsage(tokens, window)
}
