package coding

// Ports packages/coding-agent/src/core/agent-session.ts.

import (
	"context"
	"errors"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// SendCustomMessage awaits custom-message delivery and any triggered turn. An explicit false TriggerTurn defers an active turn's append until its tool results are complete.
func (s *Session) SendCustomMessage(ctx context.Context, message extension.CustomMessageRef, options *extension.SendMessageOptions) error {
	return s.sendCustomMessage(ctx, message, options, true, nil)
}

// SendMessage is the bound extension action. The Session owns triggered work and reports failures through its extension error listeners.
func (s *Session) SendMessage(message extension.CustomMessageRef, options *extension.SendMessageOptions) error {
	s.reportRuntimeError("send_message", s.sendCustomMessage(s.backgroundContext(), message, options, false, nil))
	return nil
}

// SendMessageStaged is SendMessage whose idle triggerTurn run preparation and start stage places; a nil stage starts the run as SendMessage does.
func (s *Session) SendMessageStaged(message extension.CustomMessageRef, options *extension.SendMessageOptions, stage *ExtensionRunStage) error {
	s.reportRuntimeError("send_message", s.sendCustomMessage(s.backgroundContext(), message, options, false, stage))
	return nil
}

func (s *Session) sendCustomMessage(ctx context.Context, message extension.CustomMessageRef, options *extension.SendMessageOptions, wait bool, stage *ExtensionRunStage) error {
	content := message.Content
	if content == nil {
		content = []any{}
	}
	fields := map[string]any{"role": agent.RoleCustom, "customType": message.CustomType, "content": content, "timestamp": time.Now().UnixMilli()}
	fields["display"] = message.Display
	if message.Details != nil {
		fields["details"] = message.Details
	}
	app := agent.AgentMessage{Custom: fields}
	var opts extension.SendMessageOptions
	if options != nil {
		opts = *options
	}
	s.pendingBashMu.Lock()
	select {
	case <-s.closeDone:
		s.pendingBashMu.Unlock()
		return errors.New("coding: session is closed")
	default:
	}
	if opts.DeliverAs == extension.DeliverAsNextTurn {
		s.agent.QueueNextTurn(app)
		s.pendingBashMu.Unlock()
		return nil
	}
	if s.IsStreaming() {
		switch {
		case opts.TriggerTurn != nil && !*opts.TriggerTurn:
			s.runState.mu.Lock()
			s.runState.pendingCustom = append(s.runState.pendingCustom, app)
			s.runState.mu.Unlock()
		case opts.DeliverAs == extension.DeliverAsFollowUp:
			s.agent.FollowUp(app)
		default:
			s.agent.Steer(app)
		}
		s.pendingBashMu.Unlock()
		return nil
	}
	if opts.TriggerTurn == nil || !*opts.TriggerTurn {
		err := s.appendCustomMessage(app)
		s.pendingBashMu.Unlock()
		if err == nil {
			s.emitCustomMessage(app)
		}
		return err
	}
	if s.runState.settling.Load() {
		s.runState.mu.Lock()
		s.runState.deferred = append(s.runState.deferred, func() { s.reportRuntimeError("send_message", s.SendCustomMessage(ctx, message, &opts)) })
		s.runState.mu.Unlock()
		s.pendingBashMu.Unlock()
		return nil
	}
	// upstream: agent-session.ts:2339-2344 (sendCustomMessage awaits _startRun({ messageType: "custom", message })). The Session is busy
	// from this call on, so messages sent while the compaction check and the before_agent_start handlers run join the starting run. The
	// extension's sendMessage does not await the run, so SendMessage returns at once.
	start := s.beginRunStartLocked()
	s.pendingBashMu.Unlock()
	if wait {
		return s.startCustomRun(ctx, start, app, nil)
	}
	if stage != nil {
		stage.Prepare(ctx, func(ctx context.Context) {
			s.reportRuntimeError("send_message", ignoreCancellation(s.startCustomRun(ctx, start, app, stage.Start)))
		})
		return nil
	}
	// Pi disposes the runtime with the triggered run still starting and reports nothing for it; a Session closing under the run cancels it the same way.
	if err := s.startExtensionTask(func() {
		s.reportRuntimeError("send_message", ignoreCancellation(s.startCustomRun(ctx, start, app, nil)))
	}); err != nil {
		start.Abandon()
		return err
	}
	return nil
}

// startCustomRun is _startRun for the custom message app: it prepares the run that sendCustomMessage marked busy as start. With a nil
// schedule it then runs it; otherwise it passes schedule the step that starts the run on an extension task.
func (s *Session) startCustomRun(ctx context.Context, start *RunStart, app agent.AgentMessage, schedule func(start func())) error {
	fail := func(err error) error {
		start.Abandon()
		return err
	}
	if err := start.checkCompaction(ctx); err != nil {
		return fail(err)
	}
	prepared, err := s.prepareRun(ctx, extension.CustomMessageTrigger(customMessageOf(app)))
	if err != nil {
		return fail(err)
	}
	prepared.takeNextTurn()
	if schedule == nil {
		return s.beginCustomRun(ctx, start, app, prepared)
	}
	schedule(func() {
		if err := s.startExtensionTask(func() { s.reportRuntimeError("send_message", s.beginCustomRun(ctx, start, app, prepared)) }); err != nil {
			s.reportRuntimeError("send_message", fail(err))
		}
	})
	return nil
}

// beginCustomRun appends the messages held while the run started (agent-session.ts:1845-1848) and runs the prepared custom-message run
// that sendCustomMessage marked busy as start.
func (s *Session) beginCustomRun(ctx context.Context, start *RunStart, app agent.AgentMessage, prepared *PreparedPrompt) error {
	fail := func(err error) error {
		start.Abandon()
		return err
	}
	if _, err := start.FlushHeldMessages(); err != nil {
		return fail(err)
	}
	s.pendingBashMu.Lock()
	select {
	case <-s.closeDone:
		s.pendingBashMu.Unlock()
		return fail(errors.New("coding: session is closed"))
	default:
	}
	starting := append([]agent.AgentMessage{app}, prepared.nextTurn...)
	runCtx, cancel := context.WithCancel(ctx)
	run, err := s.agent.BeginSendMessages(runCtx, starting)
	if err != nil {
		s.pendingBashMu.Unlock()
		cancel()
		start.settleUnclaimed()
		return err
	}
	finish := s.ownStartedRun(cancel)
	s.pendingBashMu.Unlock()
	s.admitRunPrompt(prepared)
	return s.completeCustomMessage(runCtx, finish, run, prepared)
}

// customMessageOf is the extension-facing view of a custom AgentMessage.
func customMessageOf(message agent.AgentMessage) extension.CustomMessage {
	customType, _ := message.Custom["customType"].(string)
	display, _ := message.Custom["display"].(bool)
	timestamp, _ := message.Custom["timestamp"].(int64)
	return extension.CustomMessage{CustomType: customType, Content: message.Custom["content"], Display: display, Details: message.Custom["details"], Timestamp: timestamp}
}

func (s *Session) completeCustomMessage(ctx context.Context, finish context.CancelFunc, run *agent.PromptRun, prepared *PreparedPrompt) error {
	defer s.runDeferredSettledActions()
	defer finish()
	messages, err := run.Run()
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.runPostAgentRuns(ctx, messages, err)
	// The run's prompt options end before the held messages flush, as in runPreparedPrompt.
	s.finishRunPrompt(prepared)
	if flushErr := s.flushPendingBashLocked(); flushErr != nil {
		err = errors.Join(err, flushErr)
	} else {
		err = errors.Join(err, s.flushPendingCustomMessages())
	}
	// agent_settled handlers run without the Session lock (see runPreparedPrompt).
	s.mu.Unlock()
	defer s.mu.Lock()
	s.emitAgentSettled()
	return err
}

// appendCustomMessage runs while admission is excluded by pendingBashMu or while the active Agent is suspended at a completed turn.
func (s *Session) appendCustomMessage(message agent.AgentMessage) error {
	customType, _ := message.Custom["customType"].(string)
	display, _ := message.Custom["display"].(bool)
	if _, err := s.inner.AppendCustomMessageEntry(customType, message.Custom["content"], display, message.Custom["details"]); err != nil {
		return err
	}
	s.refreshContext()
	return nil
}

type publishedSessionEvent struct{ agent.AgentEvent }

func (s *Session) emitCustomMessage(message agent.AgentMessage) {
	for _, event := range []agent.AgentEvent{agent.MessageStartEvent{Message: message}, agent.MessageEndEvent{Message: message}} {
		s.notifyAgentEventListeners(event)
		s.emitOrderedEvent(publishedSessionEvent{event})
	}
}

// DeferCustomMessage holds a custom message an extension sent with triggerTurn false while a run streams, until that turn's tool results are
// complete (agent-session.ts:2351-2356). It reports false when no run is active, so the caller appends the message at once.
func (s *Session) DeferCustomMessage(message agent.AgentMessage) bool {
	s.pendingBashMu.Lock()
	defer s.pendingBashMu.Unlock()
	if !s.IsStreaming() {
		return false
	}
	s.runState.mu.Lock()
	s.runState.pendingCustom = append(s.runState.pendingCustom, message)
	s.runState.mu.Unlock()
	return true
}

// HasDeferredCustomMessages reports whether a custom message waits for the end of its turn.
func (s *Session) HasDeferredCustomMessages() bool {
	s.runState.mu.Lock()
	defer s.runState.mu.Unlock()
	return len(s.runState.pendingCustom) > 0
}

// FlushDeferredCustomMessages appends the custom messages held for the end of a turn.
func (s *Session) FlushDeferredCustomMessages() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushPendingCustomMessages()
}

func (s *Session) flushPendingCustomMessages() error {
	s.runState.mu.Lock()
	pending := s.runState.pendingCustom
	s.runState.pendingCustom = nil
	s.runState.mu.Unlock()
	for _, message := range pending {
		s.pendingBashMu.Lock()
		err := s.appendCustomMessage(message)
		s.pendingBashMu.Unlock()
		if err != nil {
			return err
		}
		s.emitCustomMessage(message)
	}
	return nil
}
