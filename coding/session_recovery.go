package coding

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
)

// messageIdentity returns the pointer that identifies one in-memory message,
// or nil for messages without one (custom messages).
func messageIdentity(message agent.AgentMessage) any {
	switch {
	case message.System != nil:
		return message.System
	case message.User != nil:
		return message.User
	case message.Assistant != nil:
		return message.Assistant
	case message.ToolResult != nil:
		return message.ToolResult
	}
	return nil
}

// rememberMessageEntry records the Session entry that persisted message.
func (s *Session) rememberMessageEntry(message agent.AgentMessage, entryID string) {
	key := messageIdentity(message)
	if key == nil || entryID == "" {
		return
	}
	s.entryIDsMu.Lock()
	if s.entryIDsByMessage == nil {
		s.entryIDsByMessage = make(map[any]string)
	}
	s.entryIDsByMessage[key] = entryID
	s.entryIDsMu.Unlock()
}

// refreshContext replaces the agent's finalized transcript with the canonical
// Session projection and re-indexes each projected message by its source
// entry (agent-session.ts _refreshFinalizedContext). The caller serializes
// with the agent loop.
func (s *Session) refreshContext() {
	s.refreshProjectedContext(true)
}

// refreshReplacementContext installs only the incoming Session's canonical
// projection. An outgoing instruction baseline belongs to the old Session and
// must not cross a resume or switch boundary.
func (s *Session) refreshReplacementContext() {
	s.refreshProjectedContext(false)
}

func (s *Session) refreshProjectedContext(preserveInstructionBaseline bool) {
	projection := s.inner.BuildSessionProjection()
	ids := make(map[any]string, len(projection.Messages))
	for _, entry := range projection.Entries {
		for _, message := range entry.Messages {
			if key := messageIdentity(message); key != nil {
				ids[key] = entry.SourceEntry.Base.ID
			}
		}
	}
	s.entryIDsMu.Lock()
	// Projection creates new message objects. Preserve the observed turn's IDs only until post-run recovery consumes it.
	observed := append([]agent.AgentMessage{{Assistant: s.lastAssistantMessage}}, s.lastAssistantToolResults...)
	for _, message := range observed {
		key := messageIdentity(message)
		if id, found := s.entryIDsByMessage[key]; key != nil && found {
			ids[key] = id
		}
	}
	s.entryIDsByMessage = ids
	s.entryIDsMu.Unlock()
	if preserveInstructionBaseline {
		restoreAgentMessages(s.agent, projection.Messages)
		return
	}
	s.agent.SetMessages(projection.Messages)
}

// RefreshContext refreshes the agent's finalized transcript from the canonical
// Session projection (agent-session.ts refreshContext). Call it after
// appending entries to Inner() directly.
func (s *Session) RefreshContext() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshContext()
}

// findPersistedMessageEntryID resolves the Session entry that persisted an
// in-memory message (agent-session.ts _findPersistedMessageEntryId). A
// message not indexed at persistence or refresh time resolves through its
// position in the agent transcript, which follows the projection.
func (s *Session) findPersistedMessageEntryID(message agent.AgentMessage) (string, bool) {
	key := messageIdentity(message)
	if key == nil {
		return "", false
	}
	s.entryIDsMu.Lock()
	id, found := s.entryIDsByMessage[key]
	s.entryIDsMu.Unlock()
	if found {
		return id, true
	}
	if message.ToolResult != nil {
		projection := s.inner.BuildSessionProjection()
		for _, entry := range slices.Backward(projection.Entries) {
			for _, projected := range entry.Messages {
				if projected.ToolResult != nil && projected.ToolResult.ToolCallID == message.ToolResult.ToolCallID {
					s.rememberMessageEntry(message, entry.SourceEntry.Base.ID)
					return entry.SourceEntry.Base.ID, true
				}
			}
		}
	}
	messageIndex := slices.IndexFunc(s.agent.Messages(), func(candidate agent.AgentMessage) bool {
		return messageIdentity(candidate) == key
	})
	if messageIndex < 0 {
		return "", false
	}
	projection := s.inner.BuildSessionProjection()
	// Restoring a transcript without a system entry prepends the current
	// baseline. It has no persisted entry and shifts projected positions.
	messages := s.agent.Messages()
	if len(messages) > 0 && messages[0].System != nil && (len(projection.Messages) == 0 || projection.Messages[0].System == nil) {
		messageIndex--
	}
	if messageIndex < 0 {
		return "", false
	}
	projectedIndex := 0
	for _, entry := range projection.Entries {
		for _, projected := range entry.Messages {
			if projectedIndex == messageIndex {
				if projected.Role() != message.Role() {
					return "", false
				}
				s.rememberMessageEntry(message, entry.SourceEntry.Base.ID)
				return entry.SourceEntry.Base.ID, true
			}
			projectedIndex++
		}
	}
	return "", false
}

// isInAgentTranscript reports whether message is one of the agent's current
// in-memory messages.
func (s *Session) isInAgentTranscript(message agent.AgentMessage) bool {
	key := messageIdentity(message)
	return key != nil && slices.ContainsFunc(s.agent.Messages(), func(candidate agent.AgentMessage) bool {
		return messageIdentity(candidate) == key
	})
}

var errUnresolvedRecoveryTarget = errors.New("Cannot persist recovery omission because a projected message has no source entry")

// omitRecoveryAttempt durably omits a failed final attempt and its tool
// results from model context with context_edit entries, keeping them in raw
// history (agent-session.ts _omitRecoveryAttempt).
func (s *Session) omitRecoveryAttempt(message *agent.AssistantMessage, toolResults []agent.AgentMessage) error {
	targets := append([]agent.AgentMessage{{Assistant: message}}, toolResults...)
	targetIDs := make([]string, len(targets))
	for i, target := range targets {
		id, found := s.findPersistedMessageEntryID(target)
		if !found && s.isInAgentTranscript(target) {
			return errUnresolvedRecoveryTarget
		}
		targetIDs[i] = id
	}
	for _, targetID := range targetIDs {
		if targetID == "" {
			continue
		}
		editID, err := s.inner.AppendContextEdit(targetID, nil)
		if err != nil {
			return err
		}
		if entry, ok := s.inner.EntryByID(editID); ok {
			s.emitOrderedEvent(agent.EntryAppendedEvent{Entry: entry.Raw()})
		}
	}
	s.refreshContext()
	return nil
}

// runPostAgentRuns repeats post-run handling and continuations until the run settles (agent-session.ts _runAgentPrompt loop). Session abort and caller cancellation both stop continuation. The caller holds s.mu.
func (s *Session) runPostAgentRuns(ctx context.Context, messages []agent.AgentMessage, runErr error) ([]agent.AgentMessage, error) {
	return s.runPostAgentRunsWith(ctx, messages, runErr, func(ctx context.Context) ([]agent.AgentMessage, error) {
		s.mu.Unlock()
		defer s.mu.Lock()
		return s.agent.Continue(ctx)
	})
}

// runPostAgentRunsWith is runPostAgentRuns with the continuation supplied by
// the caller, which decides whether the Session lock is held across it.
func (s *Session) runPostAgentRunsWith(ctx context.Context, messages []agent.AgentMessage, runErr error, resume func(context.Context) ([]agent.AgentMessage, error)) ([]agent.AgentMessage, error) {
	for !s.agentRunAborted(ctx) {
		continueRun, err := s.handlePostAgentRun(ctx)
		if err != nil {
			return messages, err
		}
		if !continueRun && !s.agentRunAborted(ctx) {
			continueRun, err = s.runBeforeSettleBoundary(ctx)
			if err != nil {
				return messages, err
			}
		}
		if !continueRun || s.agentRunAborted(ctx) {
			break
		}
		messages, runErr = resume(ctx)
	}
	if s.agentRunAborted(ctx) {
		s.finishCancelledRetry()
	}
	return messages, runErr
}

// handlePostAgentRun applies retry, recovery, and compaction to one finished
// low-level run and reports whether the agent should continue
// (agent-session.ts _handlePostAgentRun).
func (s *Session) handlePostAgentRun(ctx context.Context) (bool, error) {
	message, toolResults := s.lastAssistantMessage, s.lastAssistantToolResults
	s.lastAssistantMessage, s.lastAssistantToolResults = nil, nil
	if s.agentRunAborted(ctx) {
		s.finishCancelledRetry()
		return false, nil
	}
	if message == nil {
		return s.agent.HasQueuedMessages(), nil
	}
	if icodingagent.IsRetryableError(message, s.contextWindowFor(message)) {
		retrying, err := s.prepareRetry(ctx, message)
		if err != nil {
			return false, err
		}
		if retrying {
			s.failedResponse.Store(message)
			if s.agentRunAborted(ctx) {
				s.finishCancelledRetry()
			}
			return !s.agentRunAborted(ctx), nil
		}
	}
	if s.agentRunAborted(ctx) {
		s.finishCancelledRetry()
		return false, nil
	}
	if message.StopReason == ai.StopReasonError {
		if attempt := s.retryAttempt.Swap(0); attempt > 0 {
			s.emitEvent(agent.AutoRetryEndEvent{Success: false, Attempt: int(attempt), FinalError: message.ErrorMessage})
		}
	}
	compacted, err := s.checkCompaction(ctx, message, true, toolResults)
	if err != nil {
		return false, err
	}
	if compacted {
		return !s.agentRunAborted(ctx), nil
	}
	return !s.agentRunAborted(ctx) && s.agent.HasQueuedMessages(), nil
}

// prepareRetry schedules one automatic retry of a retryable error: it emits
// auto_retry_start, omits the failed attempt, and waits out the backoff
// (agent-session.ts _prepareRetry). It reports false when retries are
// disabled or exhausted, or when the wait is cancelled.
func (s *Session) prepareRetry(ctx context.Context, message *agent.AssistantMessage) (bool, error) {
	s.retryMu.Lock()
	schedule := s.retrySchedule
	s.retryMu.Unlock()
	wait, err := s.retryPrefix(schedule, func() (*sessionRetryWait, error) {
		cfg := s.services.SettingsManager().GetRetrySettings()
		if !cfg.Enabled || s.agentRunAborted(ctx) {
			return nil, nil
		}
		attempt := int(s.retryAttempt.Add(1))
		if attempt > cfg.MaxRetries {
			s.retryAttempt.Add(-1)
			return nil, nil
		}
		delayMs := ai.RetryDelayMs(cfg.BaseDelayMs, &cfg.MaxDelayMs, attempt)
		errorMessage := message.ErrorMessage
		if errorMessage == "" {
			errorMessage = "Unknown error"
		}
		retryCtx, cancel := context.WithCancel(ctx)
		wait := &sessionRetryWait{session: s, ctx: retryCtx, cancel: cancel, schedule: schedule, done: make(chan retryWaitResult, 1)}
		// Publish the cancellation owner before another Go goroutine can observe auto_retry_start. The queued host prefix still emits start and omission in Pi's order.
		s.retryMu.Lock()
		s.retryCancel = wait
		s.retryMu.Unlock()
		s.emitEvent(agent.AutoRetryStartEvent{Attempt: attempt, MaxAttempts: cfg.MaxRetries, DelayMs: delayMs, ErrorMessage: errorMessage})
		if err := s.omitRecoveryAttempt(message, nil); err != nil {
			cancel()
			s.retryMu.Lock()
			if s.retryCancel == wait {
				s.retryCancel = nil
			}
			s.retryMu.Unlock()
			return nil, err
		}
		wait.timer = time.NewTimer(time.Duration(delayMs) * time.Millisecond)
		return wait, nil
	})
	if err != nil || wait == nil {
		return false, err
	}
	defer wait.cancel()
	defer wait.timer.Stop()
	s.mu.Unlock()
	retry, err := wait.await()
	s.mu.Lock()
	return retry, err
}

// finishCancelledRetry ends an in-progress retry sequence after an abort.
func (s *Session) finishCancelledRetry() {
	if attempt := s.retryAttempt.Swap(0); attempt > 0 {
		s.emitEvent(agent.AutoRetryEndEvent{Success: false, Attempt: int(attempt), FinalError: "Retry cancelled"})
	}
}

// assistantEndNote records the agent-goroutine decisions for one assistant
// message_end: whether the run would retry it if it ends the run, and the
// attempt count of a retry sequence it ended successfully (zero if none).
type assistantEndNote struct {
	willRetry  bool
	retryEnded int32
}

// noteAssistantMessageEnd applies Pi's assistant message_end bookkeeping: a
// response that is neither an error nor a length stop re-arms overflow
// recovery, and a non-error response ends an automatic retry sequence. It runs
// on the agent goroutine when the message persists, before post-run handling
// can commit another retry.
func (s *Session) noteAssistantMessageEnd(message *agent.AssistantMessage) assistantEndNote {
	note := assistantEndNote{willRetry: s.willRetryAfterAgentEnd(message)}
	if message.StopReason != ai.StopReasonError && message.StopReason != ai.StopReasonLength {
		s.overflowRecoveryAttempted.Store(false)
	}
	if message.StopReason != ai.StopReasonError {
		note.retryEnded = s.retryAttempt.Swap(0)
	}
	return note
}

func (s *Session) pushAssistantEnd(note assistantEndNote) {
	s.assistantEndsMu.Lock()
	s.assistantEnds = append(s.assistantEnds, note)
	s.assistantEndsMu.Unlock()
}

// popAssistantEnd returns the note for the next forwarded assistant
// message_end. Messages that never persisted have no note.
func (s *Session) popAssistantEnd() assistantEndNote {
	s.assistantEndsMu.Lock()
	defer s.assistantEndsMu.Unlock()
	if len(s.assistantEnds) == 0 {
		return assistantEndNote{}
	}
	note := s.assistantEnds[0]
	s.assistantEnds = s.assistantEnds[1:]
	return note
}

// contextWindowFor is the context window that applies to message: the window of the model that produced it, else of the selected model.
//
// upstream: agent-session.ts:3613 (_isRetryableError)
func (s *Session) contextWindowFor(message *agent.AssistantMessage) int {
	model := s.modelForMessage(message)
	if model == nil {
		model = s.Model()
	}
	if model == nil {
		return 0
	}
	return model.Capabilities.ContextWindow
}

func (s *Session) contextWindow() int {
	model := s.Model()
	if model == nil {
		return 0
	}
	return model.Capabilities.ContextWindow
}

func (s *Session) currentBranch() []icodingagent.SessionEntry {
	if leaf := s.inner.LeafID(); leaf != nil {
		return s.inner.Branch(*leaf)
	}
	return nil
}

// compactionSettings returns the compaction settings for the current model,
// with its compaction.modelOverrides entry applied (upstream
// getCompactionSettings(this.model)).
func (s *Session) compactionSettings() (compaction.CompactionSettings, error) {
	provider, modelID := "", ""
	if model := s.Model(); model != nil {
		provider, modelID = providerID(model), model.ID
	}
	cfg, err := s.services.SettingsManager().GetModelCompactionSettings(provider, modelID)
	if err != nil {
		return compaction.CompactionSettings{}, err
	}
	return compaction.CompactionSettings{
		Enabled:          cfg.Enabled,
		ReserveTokens:    cfg.ReserveTokens,
		KeepRecentTokens: cfg.KeepRecentTokens,
	}, nil
}

func estimateMessagesTokens(messages []agent.AgentMessage) int {
	tokens := 0
	for _, message := range messages {
		tokens += compaction.EstimateTokens(message)
	}
	return tokens
}
