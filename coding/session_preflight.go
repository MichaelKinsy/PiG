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
	// run is the run's resolved before_agent_start result. Its options build the run's prompt (agent-session.ts:1747-1748); SelectedTools is an explicit loadout edit, and nil keeps the live active tools.
	run icodingagent.BeforeAgentStartRun
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
	options := *s.GetSystemPromptOptions()
	sections := ai.OrderedSections{}
	if options.Sections != nil {
		sections = cloneSystemSections(*options.Sections)
	}
	options.Sections = &sections
	var result *extension.BeforeAgentStartCombinedResult
	if runner != nil && runner.HasHandlers("before_agent_start") {
		var err error
		result, err = runner.EmitBeforeAgentStart(ctx, text, images, s.systemPrompt(), options)
		if err != nil {
			return nil, err
		}
	}
	// Interactive mode applies the same rule to its own prompt and loadout.
	run, err := icodingagent.ResolveBeforeAgentStartRun(options, result)
	if err != nil {
		// agent-session.ts:1409-1419 admits the loadout before buildSystemPromptSections rejects an invalid section name.
		s.applyPromptToolLoadout(s.admittedToolNames(run.SelectedTools))
		return nil, err
	}
	p.run, p.systemPrompt, p.messages = run, run.SystemPrompt, run.Messages
	return p, nil
}

// NormalizeImages applies the current model's image profile after before_agent_start has completed.
func (p *PreparedPrompt) NormalizeImages() {
	p.content = icodingagent.NormalizePromptContent(p.content, p.session.services.SettingsManager().GetImageAutoResize(), p.session.agent.Model(), p.session.processImage)
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
	return r.agentRun.Start()
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
	// Keep claim and streaming publication atomic with the shell queue decision.
	runCtx, cancel := context.WithCancel(ctx)
	s.pendingBashMu.Lock()
	s.runState.active.Store(true)
	run, err := s.agent.BeginSendContent(runCtx, p.content)
	if err != nil {
		s.pendingBashMu.Unlock()
		cancel()
		return &PreparedPromptRun{session: s, err: err}, nil
	}
	finish := s.ownAgentRun(cancel)
	s.pendingBashMu.Unlock()
	s.applyPromptToolLoadout(s.admittedToolNames(p.run.SelectedTools))
	if p.systemPrompt != nil {
		s.agent.SetSystemPrompt(*p.systemPrompt)
	}
	s.runPrompt.Store(&p.run)
	s.QueueAgentStartMessages(p.messages)
	return &PreparedPromptRun{session: s, prompt: p, agentRun: run, ctx: runCtx, finish: finish}, nil
}

// admittedToolNames returns an explicit selectedTools edit, or the live active tools when handlers left the list unchanged (agent-session.ts:1714).
func (s *Session) admittedToolNames(selected []string) []string {
	if selected == nil {
		return s.ActiveToolNames()
	}
	return selected
}

// applyPromptToolLoadout makes an edited selectedTools list the executable and provider loadout before the run declares its tools. Like agent-session.ts:1409-1415 it leaves the base prompt options that before_agent_start receives unchanged. Each prompt applies the whole loadout, so hidden tools stay inactive and prepareLoadout hooks run again.
//
// upstream: agent-session.ts:1661-1665,2020 (_preparePromptAndToolLoadout calls _applyToolLoadout)
func (s *Session) applyPromptToolLoadout(names []string) {
	s.loadout.applyMu.Lock()
	defer s.loadout.applyMu.Unlock()
	s.toolRegistryMu.Lock()
	defer s.toolRegistryMu.Unlock()
	s.agent.SetTools(s.applyToolLoadout(names))
}

func (s *Session) runPreparedPrompt(ctx context.Context, p *PreparedPrompt, run func() ([]agent.AgentMessage, error)) ([]agent.AgentMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	messages, err := func() ([]agent.AgentMessage, error) {
		s.mu.Unlock()
		defer s.mu.Lock()
		return run()
	}()
	messages, err = s.runPostAgentRuns(ctx, messages, err)
	if flushErr := s.flushPendingBashLocked(); flushErr != nil {
		err = errors.Join(err, flushErr)
	} else {
		err = errors.Join(err, s.flushPendingCustomMessages())
	}
	if p.systemPrompt != nil {
		s.agent.ClearSystemPrompt()
	}
	s.runPrompt.Store(nil)
	s.emitAgentSettled()
	return messages, err
}
