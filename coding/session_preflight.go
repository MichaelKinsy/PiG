// Ports packages/coding-agent/src/core/agent-session.ts
package coding

import (
	"context"
	"errors"
	"time"

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
	// nextTurn holds the pending nextTurn messages the preparation took for the run. A nextTurn message queued after that waits for the next run.
	nextTurn []agent.AgentMessage
	// run is the run's resolved before_agent_start result. Its options build the run's prompt (agent-session.ts:1747-1748); SelectedTools is an explicit loadout edit, and nil keeps the live active tools.
	run icodingagent.BeforeAgentStartRun
}

// PreparePrompt awaits before_agent_start for the user prompt whose RunStart keeps the Session busy (agent-session.ts:1885-1936
// _prepareRun). Hosts normalize images and admit the result after that await; a message or queued prompt sent while a handler is
// suspended joins the starting run.
func (s *Session) PreparePrompt(ctx context.Context, content []ai.UserContentBlock) (*PreparedPrompt, error) {
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
	p, err := s.prepareRun(ctx, extension.UserPromptTrigger(text, images))
	if p != nil {
		p.content = append([]ai.UserContentBlock(nil), content...)
	}
	return p, err
}

// prepareRun awaits before_agent_start for the message that starts a run (agent-session.ts:1885-1936 _prepareRun) without holding the
// Session lock, so a handler's own Session calls and queue operations proceed while it is suspended.
func (s *Session) prepareRun(ctx context.Context, trigger extension.BeforeAgentStartTrigger) (*PreparedPrompt, error) {
	p := &PreparedPrompt{session: s, sessionID: s.ID()}
	select {
	case <-s.closeDone:
		return nil, errors.New("coding: session is closed")
	default:
	}
	runner := s.currentRunner()
	options := *s.GetSystemPromptOptions()
	sections := ai.OrderedSections{}
	if options.Sections != nil {
		sections = cloneSystemSections(*options.Sections)
	}
	options.Sections = &sections
	var result *extension.BeforeAgentStartCombinedResult
	if runner != nil && runner.HasHandlers("before_agent_start") {
		var err error
		result, err = runner.EmitBeforeAgentStart(ctx, trigger, options)
		if err != nil {
			return nil, err
		}
	}
	// Interactive mode applies the same rule to its own prompt and loadout.
	run, err := icodingagent.ResolveBeforeAgentStartRun(options, result)
	// _preparePromptAndToolLoadout replays the transcript's system messages before it builds the run's sections (agent-session.ts:1745-1748), so its TypeError comes first.
	if replayErr := s.agent.CurrentSystemMessageError(); replayErr != nil {
		err = replayErr
	}
	if err != nil {
		// agent-session.ts:1409-1419 admits the loadout before buildSystemPromptSections rejects an invalid section name.
		s.applyPromptToolLoadout(s.admittedToolNames(run.SelectedTools))
		return nil, err
	}
	p.run, p.systemPrompt, p.messages = run, run.SystemPrompt, run.Messages
	return p, nil
}

// NormalizeImages applies the image profile of the limits model (the physical model of the latest virtual-selection response, else the selected model) after before_agent_start has completed (agent-session.ts:2037). It is the last step of the preparation, so it also takes the pending nextTurn messages for the run, as _prepareRun does after it normalizes the images (agent-session.ts:1914-1925). The held messages are flushed after that (:1852-1853), so a nextTurn message queued while they are emitted, such as one a Session listener sends, waits for the next run.
func (p *PreparedPrompt) NormalizeImages() {
	p.content = icodingagent.NormalizePromptContent(p.content, p.session.SettingsManager().GetImageAutoResize(), p.session.limitsModel(), p.session.processImage)
	p.takeNextTurn()
}

// takeNextTurn takes the pending nextTurn messages for the run whose preparation ends (agent-session.ts:1924-1925).
func (p *PreparedPrompt) takeNextTurn() {
	p.nextTurn = p.session.agent.TakePendingNextTurn()
}

// startingMessages is the starting batch of a user prompt's run: the user message, then the nextTurn messages the preparation took (agent-session.ts:1909-1925). The Agent's prepare-prompt hook appends the handlers' messages and the system patch.
func (p *PreparedPrompt) startingMessages() []agent.AgentMessage {
	user := agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: append(ai.UserContentBlocks(nil), p.content...), Timestamp: time.Now().UnixMilli()}}
	return append([]agent.AgentMessage{user}, p.nextTurn...)
}

// PreparedPromptRun owns an admitted Session prompt across its first event and the remainder of execution.
type PreparedPromptRun struct {
	session  *Session
	start    *RunStart
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
		r.start.settleUnclaimed()
		return nil, r.err
	}
	defer r.finish()
	messages, err := r.session.runPreparedPrompt(r.ctx, r.prompt, r.agentRun.Run)
	r.session.runDeferredSettledActions()
	return messages, err
}

// BeginPreparedPrompt claims the agent for the prompt that start prepared, after its held messages were flushed (agent-session.ts:1857
// agent.prompt). An error abandons start. A busy agent rejects the run after acceptance; it is not serialized behind the old run.
func (s *Session) BeginPreparedPrompt(ctx context.Context, start *RunStart, p *PreparedPrompt) (*PreparedPromptRun, error) {
	if p == nil || p.session != s || start.session != s {
		start.Abandon()
		return nil, errors.New("coding: prompt preparation belongs to another session")
	}
	if p.sessionID != s.ID() {
		start.Abandon()
		return nil, context.Canceled
	}
	// Keep claim and streaming publication atomic with the shell queue decision.
	runCtx, cancel := context.WithCancel(ctx)
	s.pendingBashMu.Lock()
	s.runState.active.Store(true)
	run, err := s.agent.BeginSendMessages(runCtx, p.startingMessages())
	if err != nil {
		s.pendingBashMu.Unlock()
		cancel()
		return &PreparedPromptRun{session: s, start: start, err: err}, nil
	}
	finish := s.ownStartedRun(cancel)
	s.pendingBashMu.Unlock()
	s.admitRunPrompt(p)
	return &PreparedPromptRun{session: s, start: start, prompt: p, agentRun: run, ctx: runCtx, finish: finish}, nil
}

// admitRunPrompt makes the prepared run's tool loadout, system prompt, run options and handler messages current for the run that was just claimed.
func (s *Session) admitRunPrompt(p *PreparedPrompt) {
	s.applyPromptToolLoadout(s.admittedToolNames(p.run.SelectedTools))
	if p.systemPrompt != nil {
		s.agent.SetSystemPrompt(*p.systemPrompt)
	}
	s.runPrompt.Store(&p.run)
	s.QueueAgentStartMessages(p.messages)
}

// finishRunPrompt ends the prepared run's system prompt and run options.
func (s *Session) finishRunPrompt(p *PreparedPrompt) {
	if p.systemPrompt != nil {
		s.agent.ClearSystemPrompt()
	}
	s.runPrompt.Store(nil)
}

// SetRunPrompt publishes the before_agent_start run of a prompt that interactive mode prepared itself, so the Session's
// request hooks record the run's prompt sections in the transcript as they do for a prompt the Session prepared
// (agent-session.ts:1724 builds the run's options for every mode). Nil ends the run. The Session refreshes the options
// before each later turn.
func (s *Session) SetRunPrompt(run *icodingagent.BeforeAgentStartRun) {
	s.runPrompt.Store(run)
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
	// The run's prompt options end before the held bash and custom messages flush, so their handlers read the base prompt (agent-session.ts:1874-1881).
	s.finishRunPrompt(p)
	if flushErr := s.flushPendingBashLocked(); flushErr != nil {
		err = errors.Join(err, flushErr)
	} else {
		err = errors.Join(err, s.flushPendingCustomMessages())
	}
	// agent_settled handlers run without the Session lock: Pi awaits them while the Session stays usable, so a handler that awaits ctx.compact()
	// or another Session operation must not wait on a lock held for the very dispatch that awaits it.
	s.mu.Unlock()
	defer s.mu.Lock()
	s.emitAgentSettled()
	return messages, err
}
