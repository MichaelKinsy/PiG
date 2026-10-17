package codingagent

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// promptUserInput has two dispatch strategies, and they differ only in who runs
// the input handlers.
//
// PiG dispatches input handlers synchronously from promptUserInput, which the
// owner loop calls from its Enter handling. That made a handler part of the
// loop, and a handler that opens a dialog and waits for an answer was then
// waiting for the loop that was waiting for the handler: the terminal stopped
// responding to every key with nothing on screen to explain why. Extension
// commands (interactive_input.go, `go nr.ExecuteCommand`) and before_agent_start
// (startTurn's run goroutine) are already dispatched off the loop for the same
// reason; input handlers were the last path that was not.
//
// So when an extension has an `input` handler, the handlers run on their own
// goroutine and the rest of the path is applied on the owner loop when they
// return. When no extension has one, nothing can suspend the dispatch, and the
// work stays on the loop: several of the loop's other guarantees are stated in
// terms of a submission being fully dispatched before the loop reads the next
// keystroke (queued startup prompts each start their own turn), and a hop
// through another goroutine for work that cannot block would give those up for
// nothing.

// inputPreflight is one submission as it waits for, or runs, its input handlers.
type inputPreflight struct {
	ctx    context.Context
	text   string
	images []ai.ImageContent
	// followUp and expand are the caller's delivery and expansion choices.
	followUp bool
	expand   bool
	// behavior is the streamingBehavior the input event carries. It is sampled
	// once, at submission, and delivery is decided again when the handlers
	// return, as upstream does after awaiting them.
	behavior string
	// streaming records whether a run was active at submission. It decides the
	// delivery the user asked for: text typed while working steers or follows up,
	// text typed while idle starts a turn.
	streaming bool
	source    extension.InputSource
	// session is the Session the submission was typed into. Upstream's prompt
	// stays bound to the AgentSession it was called on, so a result that
	// returns after /new, /resume or /fork replaced it is dropped.
	session InteractiveSessionHandle
}

// heldPrompt is one idle submission waiting for the active run to settle.
type heldPrompt struct {
	ctx     context.Context
	text    string
	images  []ai.ImageContent
	session InteractiveSessionHandle
}

// inputPreflightResult is what the handlers returned for one submission.
type inputPreflightResult struct {
	text    string
	images  []ai.ImageContent
	handled bool
	err     error
}

// inputHandlersRegistered reports whether any loaded extension handles the input
// event, which is what decides the dispatch strategy.
func (m *InteractiveMode) inputHandlersRegistered() bool {
	return m.newRunner != nil && m.newRunner.HasHandlers(EventInput)
}

// submitPrompt takes the next submission whose handlers run off the loop.
//
// The owner loop owns this state. The mutex is not for the loop's benefit: it is
// what lets a reader outside the loop ask whether a dispatch is still in flight,
// which is a question tests ask and shutdown can ask, without reading a field the
// loop writes.
func (m *InteractiveMode) submitPrompt(submission inputPreflight) {
	m.preflightMu.Lock()
	m.preflightQueue = append(m.preflightQueue, submission)
	m.preflightMu.Unlock()
	m.dispatchNextPreflight()
}

// dispatchNextPreflight starts the next queued submission's input handlers.
//
// One dispatch runs at a time. Two concurrent dispatches would let two input
// events reach the extensions in an order the user did not type them in, and
// would let a second submission's delivery be decided against a turn the first
// submission has not started yet. The RPC path serialises the same boundary on
// its FIFO executor (coding/cli/rpc_admission.go).
func (m *InteractiveMode) dispatchNextPreflight() {
	m.preflightMu.Lock()
	if m.preflight != nil || len(m.preflightQueue) == 0 {
		m.preflightMu.Unlock()
		return
	}
	next := m.preflightQueue[0]
	m.preflightQueue = m.preflightQueue[1:]
	m.preflight = &next
	m.preflightMu.Unlock()

	// Resolve the dispatch target here, on the owner loop: reload and session
	// replacement reassign m.newRunner and m.opts.SessionHandle on the loop, so
	// the worker must not read them.
	run := m.inputHandlerDispatcher()
	go func() {
		text, images, handled, err := run(next.ctx, next.text, next.images, next.source, next.behavior)
		// Everything the handlers produced is host state: expansion, queueing and
		// the turn itself all belong to the owner loop. The result is posted there
		// rather than applied here, and postToMain drops it once the session ends,
		// which is the rule every other worker follows.
		m.runOnMain(m.runCtx, func() {
			m.finishPreflight(next, inputPreflightResult{text: text, images: images, handled: handled, err: err})
		})
	}()
}

// inputHandlerDispatcher captures the current input-handler entry point. It
// runs on the owner loop.
func (m *InteractiveMode) inputHandlerDispatcher() func(context.Context, string, []ai.ImageContent, extension.InputSource, string) (string, []ai.ImageContent, bool, error) {
	if session := m.opts.SessionHandle; session != nil {
		return session.RunInputHandlers
	}
	runner := m.newRunner
	return func(ctx context.Context, text string, images []ai.ImageContent, source extension.InputSource, behavior string) (string, []ai.ImageContent, bool, error) {
		return RunInputHandlers(ctx, runner, text, images, source, behavior)
	}
}

// endPreflightDispatch clears the in-flight submission and starts the next one.
func (m *InteractiveMode) endPreflightDispatch() {
	m.preflightMu.Lock()
	m.preflight = nil
	m.preflightMu.Unlock()
	m.dispatchNextPreflight()
}

// preflightPending reports whether a submission's input handlers are still
// running, or another is queued behind it.
func (m *InteractiveMode) preflightPending() bool {
	m.preflightMu.Lock()
	defer m.preflightMu.Unlock()
	return m.preflight != nil || len(m.preflightQueue) > 0
}

// finishPreflight applies one submission's handler result on the owner loop and
// starts the next queued submission. It is also the synchronous path's whole
// body, so both strategies deliver a result the same way.
func (m *InteractiveMode) finishPreflight(submission inputPreflight, result inputPreflightResult) {
	defer m.endPreflightDispatch()
	switch {
	case submission.session != m.opts.SessionHandle:
		// The Session was replaced while the handlers ran; the result belonged
		// to the old one.
	case result.err != nil:
		// Upstream prompt() rejects and the input is not sent; the error is
		// shown.
		m.showError(result.err.Error())
	case !result.handled:
		text := result.text
		if submission.expand {
			// Upstream expansion order: _expandSkillCommand, then
			// expandPromptTemplate on its result.
			if expanded, ok := m.expandSkillCommand(text); ok {
				text = expanded
			}
			if expanded, ok := ExpandPromptTemplate(text, m.promptTemplates); ok {
				text = expanded
			}
		}
		// Upstream rechecks streaming after the input await (agent-session.ts
		// `_queueUserInput`): a run that started while the handlers ran receives
		// text the user typed while working as a steer or follow-up.
		if submission.streaming && m.enqueueIfTurnActive(func() {
			if submission.followUp {
				m.followUpMessageWithImages(text, result.images)
			} else {
				m.steerMessageWithImages(text, result.images)
			}
		}) {
			break
		}
		// Text typed while idle never joins another run. Upstream holds it in
		// pendingUserInputs (interactive-mode.ts onSubmit) and its main loop
		// prompts each entry only after the previous prompt settled
		// (getUserInput / session.prompt), so it waits here, behind any text
		// already held, and runs as its own prompt.
		if m.runStreaming() || len(m.heldPrompts) > 0 {
			m.heldPrompts = append(m.heldPrompts, heldPrompt{ctx: submission.ctx, text: text, images: result.images, session: submission.session})
			m.runHeldPrompts()
			break
		}
		m.runPromptTurnWithImages(submission.ctx, text, result.images)
	}
}

// runHeldPrompts starts the oldest held prompt when no run is active, or waits
// off the loop for the active run to settle and tries again. It runs on the
// owner loop.
func (m *InteractiveMode) runHeldPrompts() {
	if m.heldWaiting || len(m.heldPrompts) == 0 {
		return
	}
	if m.runStreaming() {
		m.heldWaiting = true
		ctx := m.runCtx
		if ctx == nil {
			ctx = context.Background()
		}
		go func() {
			if m.waitForIdle(ctx) != nil {
				return
			}
			m.runOnMain(ctx, func() {
				m.heldWaiting = false
				m.runHeldPrompts()
			})
		}()
		return
	}
	next := m.heldPrompts[0]
	m.heldPrompts[0] = heldPrompt{}
	m.heldPrompts = m.heldPrompts[1:]
	if next.session == m.opts.SessionHandle {
		m.runPromptTurnWithImages(next.ctx, next.text, next.images)
	}
	m.runHeldPrompts()
}
