// Ports packages/coding-agent/src/core/agent-session.ts.

package coding

import (
	"bytes"
	"context"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
)

// installAgentBoundaryHooks runs actionable turn_end before the Agent chooses its next turn. An earlier explicit end decision wins; an invalid extension continuation does not suppress natural tool or queue scheduling.
func (s *Session) installAgentBoundaryHooks() {
	s.lastActivityOutcome = extension.AgentActivityCompleted
	previous := s.agent.FinishTurnHook()
	s.agent.SetFinishTurn(func(ctx context.Context, turn agent.AgentTurnContext) (*agent.AgentTurnDecision, error) {
		s.boundaryDispatchedMessage = turn.Message
		continued, err := s.dispatchTurnEndBoundary(ctx, agent.TurnEndEvent{Message: agent.AgentMessage{Assistant: turn.Message}, ToolResults: turn.ToolResults})
		if err != nil {
			return nil, err
		}
		var decision *agent.AgentTurnDecision
		if previous != nil {
			decision, err = previous(ctx, turn)
			if err != nil {
				return nil, err
			}
		}
		if decision != nil && decision.Action == agent.AgentTurnEnd {
			return decision, nil
		}
		if continued || decision != nil && decision.Action == agent.AgentTurnContinue {
			return &agent.AgentTurnDecision{Action: agent.AgentTurnContinue}, nil
		}
		return nil, nil
	})
}

// handleAgentEvent is the Session's awaited Agent subscription. Queue consumption notifies listeners before message_start handlers. Synthetic failed turns have no FinishTurn dispatch, so their boundary runs here before public listeners.
func (s *Session) handleAgentEvent(ctx context.Context, event agent.AgentEvent) error {
	switch event := event.(type) {
	case agent.MessageStartEvent:
		if event.Message.User != nil {
			s.overflowRecoveryAttempted.Store(false)
			if update, removed := s.consumeQueuedMessage(event.Message); removed {
				s.notifyAgentEventListeners(update)
				s.publishQueueEvent(update)
			}
		}
	case agent.AgentStartEvent:
		s.boundaryTurnIndex = 0
	case agent.TurnEndEvent:
		if event.Message.Assistant != nil && event.Message.Assistant != s.boundaryDispatchedMessage {
			if _, err := s.dispatchTurnEndBoundary(ctx, event); err != nil {
				return err
			}
		}
		s.boundaryDispatchedMessage = nil
		s.boundaryTurnIndex++
	}
	if _, turnEnd := event.(agent.TurnEndEvent); !turnEnd {
		s.extensionEventHook(event)
	}
	s.emitSynchronousSessionEvent(event, true)
	var persistErr error
	if end, ok := event.(agent.MessageEndEvent); ok {
		persistErr = s.persistMessage(end.Message)
	}
	// Publish after persistence but before pending custom messages. Later Agent subscribers observe the completed Session transaction.
	s.emitOrderedEvent(publishedSessionEvent{event})
	if persistErr != nil {
		return persistErr
	}
	if end, ok := event.(agent.TurnEndEvent); ok {
		s.lastAssistantToolResults = make([]agent.AgentMessage, len(end.ToolResults))
		for i := range end.ToolResults {
			s.lastAssistantToolResults[i] = agent.AgentMessage{ToolResult: &end.ToolResults[i]}
		}
		return s.flushPendingCustomMessages()
	}
	return nil
}

func (s *Session) dispatchTurnEndBoundary(ctx context.Context, event agent.TurnEndEvent) (bool, error) {
	s.lastActivityOutcome = extension.AgentActivityCompleted
	if message := event.Message.Assistant; message != nil {
		switch message.StopReason {
		case ai.StopReasonError:
			s.lastActivityOutcome = extension.AgentActivityError
		case ai.StopReasonAborted:
			s.lastActivityOutcome = extension.AgentActivityAborted
		}
	}
	runner := s.currentRunner()
	if runner == nil || !runner.HasHandlers(icodingagent.EventTurnEnd) {
		return false, nil
	}
	event = s.turnEndWithEntryIDs(event)
	s.lastAssistantToolResults = make([]agent.AgentMessage, len(event.ToolResults))
	for i := range event.ToolResults {
		s.lastAssistantToolResults[i] = agent.AgentMessage{ToolResult: &event.ToolResults[i]}
		s.rememberMessageEntry(s.lastAssistantToolResults[i], event.ToolResultEntryIDs[i])
	}
	if event.MessageEntryID == "" {
		runner.EmitError(&extension.ExtensionError{ExtensionPath: "<boundary>", Event: icodingagent.EventTurnEnd, Error: "turn_end could not resolve the persisted assistant entry ID"})
		return false, nil
	}
	results := make([]extension.ToolResultMessage, len(event.ToolResults))
	for i := range event.ToolResults {
		results[i] = event.ToolResults[i]
	}
	ids := make([]string, 0, len(event.ToolResultEntryIDs))
	for _, id := range event.ToolResultEntryIDs {
		if id != "" {
			ids = append(ids, id)
		}
	}
	base := extension.TurnEndEvent{Type: icodingagent.EventTurnEnd, TurnIndex: s.boundaryTurnIndex, Message: event.Message, ToolResults: results, MessageEntryID: event.MessageEntryID, ToolResultEntryIds: ids, BoundaryState: &extension.BoundaryState{Outcome: s.lastActivityOutcome}}
	// Upstream emitBoundary takes no abort signal, so an aborted run's turns
	// still reach turn_end handlers.
	result, err := runner.EmitBoundary(context.WithoutCancel(ctx), base, func(drafts []extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
		return s.buildBoundaryContext(drafts, icodingagent.EventTurnEnd)
	})
	if err != nil {
		return false, err
	}
	if err := s.commitBoundaryDrafts(result.Entries); err != nil {
		return false, err
	}
	final, err := s.buildBoundaryContext(nil, icodingagent.EventTurnEnd)
	if err != nil {
		return false, err
	}
	if result.Continue && !final.CanContinue {
		s.reportInvalidBoundaryContinuation(icodingagent.EventTurnEnd)
		return false, nil
	}
	return result.Continue, nil
}

func (s *Session) runBeforeSettleBoundary(ctx context.Context) (bool, error) {
	runner := s.currentRunner()
	if runner == nil || !runner.HasHandlers(icodingagent.EventAgentBeforeSettle) {
		return s.agent.HasQueuedMessages(), nil
	}
	s.runState.mu.Lock()
	if s.runState.abortRequested.Load() {
		s.runState.mu.Unlock()
		return false, nil
	}
	s.runState.isBeforeSettle = true
	s.runState.mu.Unlock()
	defer func() { s.runState.mu.Lock(); s.runState.isBeforeSettle = false; s.runState.mu.Unlock() }()
	base := &extension.AgentBeforeSettleEvent{Type: icodingagent.EventAgentBeforeSettle, BoundaryState: extension.BoundaryState{Outcome: s.lastActivityOutcome}}
	result, err := runner.EmitBoundary(ctx, base, func(drafts []extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
		return s.buildBoundaryContext(drafts, icodingagent.EventAgentBeforeSettle)
	})
	if err != nil {
		return false, err
	}
	if err := s.commitBoundaryDrafts(result.Entries); err != nil {
		return false, err
	}
	if err := s.flushPendingCustomMessages(); err != nil {
		return false, err
	}
	final, err := s.buildBoundaryContext(nil, icodingagent.EventAgentBeforeSettle)
	if err != nil {
		return false, err
	}
	if s.runState.abortRequested.Load() {
		return false, nil
	}
	shouldContinue := result.Continue || s.agent.HasQueuedMessages()
	if shouldContinue && !final.CanContinue {
		if result.Continue {
			s.reportInvalidBoundaryContinuation(icodingagent.EventAgentBeforeSettle)
		}
		return false, nil
	}
	return shouldContinue, nil
}

func (s *Session) reportInvalidBoundaryContinuation(event string) {
	s.currentRunner().EmitError(&extension.ExtensionError{ExtensionPath: "<boundary>", Event: event, Error: event + " requested continuation without runnable model context"})
}

func (s *Session) commitBoundaryDrafts(drafts []extension.SessionBoundaryDraft) error {
	appended, err := applyBoundaryDrafts(s.inner, drafts)
	if err != nil {
		return err
	}
	s.refreshContext()
	for _, entry := range appended {
		s.emitOrderedEventSync(agent.EntryAppendedEvent{Entry: entry.Raw()})
	}
	return nil
}

func (s *Session) createBoundaryPreviewManager(drafts []extension.SessionBoundaryDraft) (*icodingagent.Session, error) {
	header := s.inner.Header()
	preview := icodingagent.NewSession(header.ID, header.CWD)
	for _, entry := range s.currentBranch() {
		if err := preview.AppendEntry(entry); err != nil {
			return nil, err
		}
	}
	if _, err := applyBoundaryDrafts(preview, drafts); err != nil {
		return nil, err
	}
	return preview, nil
}

func applyBoundaryDrafts(manager *icodingagent.Session, drafts []extension.SessionBoundaryDraft) ([]icodingagent.SessionEntry, error) {
	appended := make([]icodingagent.SessionEntry, 0, len(drafts))
	for _, draft := range drafts {
		var id string
		var err error
		switch draft.Type {
		case "custom":
			id, err = manager.AppendCustomEntry(draft.CustomType, draft.Data)
		case "custom_message":
			id, err = manager.AppendCustomMessage(draft.CustomType, draft.Content, draft.Display, draft.Details)
		case "context_edit":
			var replacement *icodingagent.ContextEditReplacement
			if raw := bytes.TrimSpace(draft.Replacement); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
				replacement = &icodingagent.ContextEditReplacement{}
				if err = json.Unmarshal(raw, replacement); err != nil {
					return nil, fmt.Errorf("context_edit replacement: %w", err)
				}
			}
			id, err = manager.AppendContextEdit(draft.TargetID, replacement)
		case "compaction":
			firstKept := ""
			if draft.FirstKeptEntryID != nil {
				firstKept = *draft.FirstKeptEntryID
			}
			tokensBefore := compaction.EstimateProjectedContextTokens(manager.BuildSessionProjection(), manager.GetBranch()).Tokens
			id, err = manager.AppendCompaction(draft.Summary, firstKept, tokensBefore, draft.Details, true, draft.Usage)
		default:
			return nil, fmt.Errorf("unsupported session boundary draft type %q", draft.Type)
		}
		if err != nil {
			return nil, err
		}
		entry, found := manager.EntryByID(id)
		if !found {
			return nil, fmt.Errorf("boundary entry %s was not appended", id)
		}
		appended = append(appended, entry)
	}
	return appended, nil
}

func (s *Session) buildBoundaryContext(drafts []extension.SessionBoundaryDraft, boundary string) (extension.BoundaryContextPreview, error) {
	preview, err := s.createBoundaryPreviewManager(drafts)
	if err != nil {
		return extension.BoundaryContextPreview{}, err
	}
	projection := preview.BuildSessionProjection()
	entries := make([]extension.ProjectedSessionEntry, len(projection.Entries))
	for i, projected := range projection.Entries {
		messages := make([]extension.AgentMessage, len(projected.Messages))
		for j := range projected.Messages {
			messages[j] = projected.Messages[j]
		}
		var source any
		if err := json.Unmarshal(projected.SourceEntry.Raw(), &source); err != nil {
			return extension.BoundaryContextPreview{}, err
		}
		entries[i] = extension.ProjectedSessionEntry{SourceEntry: source, Messages: messages}
	}
	contextMessages := make([]extension.AgentMessage, len(projection.Messages))
	for i := range projection.Messages {
		contextMessages[i] = projection.Messages[i]
	}
	llm := agent.ConvertToLLM(projection.Messages, s.agent.Model())
	llmMessages := make([]any, len(llm))
	hasNonSystem, finalAssistant := false, false
	for i, message := range llm {
		llmMessages[i] = message
		_, system := message.(ai.SystemMessage)
		hasNonSystem = hasNonSystem || !system
		_, finalAssistant = message.(ai.AssistantMessage)
	}
	s.runState.mu.Lock()
	custom := slices.Clone(s.runState.pendingCustom)
	s.runState.mu.Unlock()
	pending := slices.Concat(s.agent.PeekQueuedMessages(), custom)
	pendingMessages := make([]extension.AgentMessage, len(pending))
	for i := range pending {
		pendingMessages[i] = pending[i]
	}
	canContinue := hasNonSystem && !finalAssistant || len(custom) > 0 || (boundary == icodingagent.EventTurnEnd || finalAssistant) && s.agent.HasQueuedMessages()
	return extension.BoundaryContextPreview{ContextEntries: entries, ContextMessages: contextMessages, LLMMessages: llmMessages, PendingMessages: pendingMessages, CanContinue: canContinue}, nil
}
