// Ports packages/coding-agent/src/core/agent-session.ts
package coding

import (
	"context"
	"errors"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// PreparedPrompt holds one prompt's preflight result. It does not reserve the agent or mutate another prompt's pending messages while an extension awaits input.
type PreparedPrompt struct {
	session      *Session
	sessionID    string
	content      []ai.UserContentBlock
	messages     []extension.CustomMessageRef
	systemPrompt *string
}

// PreparePrompt awaits before_agent_start without holding the Session run lock. Hosts normalize images and admit the result after that await, so another prompt or queue operation may proceed while a handler is suspended.
func (s *Session) PreparePrompt(ctx context.Context, content []ai.UserContentBlock) (*PreparedPrompt, error) {
	p := &PreparedPrompt{session: s, sessionID: s.ID(), content: append([]ai.UserContentBlock(nil), content...)}
	select {
	case <-s.closeDone:
		return nil, errors.New("coding: session is closed")
	default:
	}
	runner := s.currentRunner()
	if runner == nil || !runner.HasHandlers("before_agent_start") {
		return p, nil
	}
	var text string
	var images []extension.ImageContent
	for _, block := range content {
		if block, ok := block.(ai.TextContent); ok {
			text = block.Text
			break
		}
	}
	for _, block := range content {
		if block, ok := block.(ai.ImageContent); ok {
			images = append(images, block)
		}
	}
	result, err := runner.EmitBeforeAgentStart(ctx, text, images, s.systemPrompt(), extension.BuildSystemPromptOptions{})
	if err != nil {
		return nil, err
	}
	if result != nil {
		p.systemPrompt = result.SystemPrompt
		p.messages = result.Messages
	}
	return p, nil
}

// NormalizeImages applies the current model's image profile after before_agent_start has completed.
func (p *PreparedPrompt) NormalizeImages() {
	p.content = icodingagent.NormalizePromptContent(p.content, p.session.services.SettingsManager().GetImageAutoResize(), p.session.agent.Model())
}

// PreparedPromptRun owns an admitted Session prompt across its first event and the remainder of execution.
type PreparedPromptRun struct {
	session  *Session
	prompt   *PreparedPrompt
	agentRun *agent.PromptRun
	ctx      context.Context
	finish   context.CancelFunc
	err      error
}

// Start dispatches agent_start without waiting for the Provider. Run must follow exactly once, even after Start fails.
func (r *PreparedPromptRun) Start() error {
	if r.agentRun == nil {
		return nil
	}
	return r.agentRun.Start(r.ctx)
}

// Run completes execution, recovery and settlement and releases the admitted run.
func (r *PreparedPromptRun) Run() ([]agent.AgentMessage, error) {
	if r.err != nil {
		r.session.emitAgentSettledNotification()
		r.session.runDeferredSettledActions()
		return nil, r.err
	}
	defer r.finish()
	messages, err := r.session.runPreparedPrompt(r.ctx, r.prompt, r.agentRun.Run)
	r.session.runDeferredSettledActions()
	return messages, err
}

// BeginPreparedPrompt commits preflight and claims the agent before returning its run. A busy agent rejects execution after acceptance; it is not serialized behind the old run.
func (s *Session) BeginPreparedPrompt(ctx context.Context, p *PreparedPrompt) (*PreparedPromptRun, error) {
	if p == nil || p.session != s {
		return nil, errors.New("coding: prompt preparation belongs to another session")
	}
	if p.sessionID != s.ID() {
		return nil, context.Canceled
	}
	run, err := s.agent.BeginSendContent(p.content)
	if err != nil {
		return &PreparedPromptRun{session: s, err: err}, nil
	}
	if p.systemPrompt != nil {
		s.agent.SetSystemPrompt(*p.systemPrompt)
	}
	s.QueueAgentStartMessages(p.messages)
	runCtx, finish := s.beginAgentRun(ctx)
	return &PreparedPromptRun{session: s, prompt: p, agentRun: run, ctx: runCtx, finish: finish}, nil
}

func (s *Session) runPreparedPrompt(ctx context.Context, p *PreparedPrompt, run func(context.Context) ([]agent.AgentMessage, error)) ([]agent.AgentMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.flushPendingBashLocked()
	messages, err := run(ctx)
	messages, err = s.runPostAgentRuns(ctx, messages, err)
	if p.systemPrompt != nil {
		s.agent.ClearSystemPrompt()
	}
	s.emitAgentSettled()
	return messages, err
}
