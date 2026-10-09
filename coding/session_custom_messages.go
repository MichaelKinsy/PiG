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
	return s.sendCustomMessage(ctx, message, options, true)
}

// SendMessage is the bound extension action. The Session owns triggered work and reports failures through its extension error listeners.
func (s *Session) SendMessage(message extension.CustomMessageRef, options *extension.SendMessageOptions) error {
	s.reportRuntimeError("send_message", s.sendCustomMessage(s.backgroundContext(), message, options, false))
	return nil
}

func (s *Session) sendCustomMessage(ctx context.Context, message extension.CustomMessageRef, options *extension.SendMessageOptions, wait bool) error {
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
	runCtx, cancel := context.WithCancel(ctx)
	run, err := s.agent.BeginSendMessages(runCtx, []agent.AgentMessage{app})
	if err != nil {
		s.pendingBashMu.Unlock()
		cancel()
		return err
	}
	finish := s.ownAgentRun(cancel)
	if wait {
		s.pendingBashMu.Unlock()
		return s.completeCustomMessage(runCtx, finish, run)
	}
	started := make(chan struct{})
	if err := s.startExtensionTask(func() {
		<-started
		s.reportRuntimeError("send_message", s.completeCustomMessage(runCtx, finish, run))
	}); err != nil {
		s.pendingBashMu.Unlock()
		cancel()
		_ = s.completeCustomMessage(runCtx, finish, run)
		return err
	}
	s.pendingBashMu.Unlock()
	_ = run.Start()
	close(started)
	return nil
}

func (s *Session) completeCustomMessage(ctx context.Context, finish context.CancelFunc, run *agent.PromptRun) error {
	defer s.runDeferredSettledActions()
	defer finish()
	messages, err := run.Run()
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.runPostAgentRuns(ctx, messages, err)
	if flushErr := s.flushPendingBashLocked(); flushErr != nil {
		err = errors.Join(err, flushErr)
	} else {
		err = errors.Join(err, s.flushPendingCustomMessages())
	}
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
