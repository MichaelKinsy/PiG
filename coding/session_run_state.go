package coding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// errAgentAlreadyProcessing is upstream prompt()'s error for a message sent
// while a run is active without a delivery mode.
var errAgentAlreadyProcessing = errors.New("Agent is already processing. Specify streamingBehavior ('steer' or 'followUp') to queue the message.")

// errPromptDuringCompaction is upstream prompt()'s error while compaction runs.
var errPromptDuringCompaction = errors.New("Cannot submit a prompt while compaction is in progress. Wait for compaction to finish and retry.")

// runtimeErrorPath is the ExtensionError path upstream uses for failures of
// session actions an extension started (agent-session.ts "<runtime>").
const runtimeErrorPath = "<runtime>"

// sessionRunState tracks the Session's agent run the way upstream
// AgentSession does with _isAgentRunActive, _agentRunAbortRequested,
// _isEmittingAgentSettled, _deferredSettledActions, and the idle-wait
// promise. Every mode reads streaming and idle state from here, and abort
// reaches the active run whichever caller started it.
type sessionRunState struct {
	active   atomic.Bool
	settling atomic.Bool

	mu         sync.Mutex
	generation uint64
	cancel     context.CancelFunc // cancels the active run
	idle       chan struct{}      // closed when the session may have become idle
	deferred   []func()           // prompts sent while agent_settled was dispatched

	// Runs an extension starts while the session is idle are owned here:
	// Close cancels them and waits for them to return.
	background   sync.WaitGroup
	bgCtx        context.Context
	bgCancel     context.CancelFunc
	shutdownOnce sync.Once
}

// IsStreaming reports whether an agent run, including its retries,
// compaction, and queued continuations, is active (upstream isStreaming).
func (s *Session) IsStreaming() bool { return s.runState.active.Load() }

// IsIdle reports whether the session has no active run and no compaction in
// flight (upstream isIdle).
func (s *Session) IsIdle() bool { return !s.IsStreaming() && !s.IsCompacting() }

// HasPendingMessages reports whether steering or follow-up messages are
// queued (upstream hasPendingMessages: pendingMessageCount > 0).
func (s *Session) HasPendingMessages() bool { return s.PendingMessageCount() > 0 }

// WaitForIdle blocks until the session is idle, ctx ends, or the session
// closes (upstream waitForIdle).
func (s *Session) WaitForIdle(ctx context.Context) error {
	for {
		s.runState.mu.Lock()
		if s.IsIdle() {
			s.runState.mu.Unlock()
			return nil
		}
		if s.runState.idle == nil {
			s.runState.idle = make(chan struct{})
		}
		idle := s.runState.idle
		s.runState.mu.Unlock()
		select {
		case <-idle:
		case <-ctx.Done():
			return ctx.Err()
		case <-s.closeDone:
			return nil
		}
	}
}

// notifyIdleWaiters wakes WaitForIdle callers to re-check the idle state.
func (s *Session) notifyIdleWaiters() {
	s.runState.mu.Lock()
	if s.runState.idle != nil {
		close(s.runState.idle)
		s.runState.idle = nil
	}
	s.runState.mu.Unlock()
}

// RequestAbort cancels the active run, retry wait, compaction, and branch
// summary without waiting. Extensions abort this way: upstream's ctx.abort()
// does not await the run it stops.
func (s *Session) RequestAbort() {
	s.runState.mu.Lock()
	cancel := s.runState.cancel
	s.runState.mu.Unlock()
	s.AbortRetry()
	s.AbortCompaction()
	s.AbortBranchSummary()
	if cancel != nil {
		cancel()
	}
}

// Abort cancels the active run, whoever started it, and waits until the
// session is idle (upstream abort).
func (s *Session) Abort(ctx context.Context) error {
	s.RequestAbort()
	return s.WaitForIdle(ctx)
}

// beginAgentRun marks the run active and returns its cancellable context and
// owned cleanup (upstream _runAgentPrompt start). A stale run's cleanup cannot
// erase a newer run's cancellation.
func (s *Session) beginAgentRun(ctx context.Context) (context.Context, context.CancelFunc) {
	runCtx, cancel := context.WithCancel(ctx)
	s.runState.mu.Lock()
	s.runState.generation++
	generation := s.runState.generation
	s.runState.cancel = cancel
	s.runState.active.Store(true)
	s.runState.mu.Unlock()
	select {
	case <-s.closeDone:
		cancel()
	default:
	}
	return runCtx, func() {
		cancel()
		s.finishAgentRun(generation)
	}
}

func (s *Session) finishAgentRun(generation uint64) {
	s.runState.mu.Lock()
	if s.runState.generation != generation || !s.runState.active.Load() {
		s.runState.mu.Unlock()
		return
	}
	s.runState.cancel = nil
	s.runState.active.Store(false)
	idle := s.runState.idle
	s.runState.idle = nil
	s.runState.mu.Unlock()
	if idle != nil {
		close(idle)
	}
}

// endAgentRun ends a run whose caller owns agent_settled (RunAgentPrompt).
func (s *Session) endAgentRun(end context.CancelFunc) { end() }

// emitAgentSettled ends the run and emits agent_settled (upstream
// _emitAgentSettled). The run is inactive before extensions see the event,
// and a prompt an agent_settled handler sends is deferred until dispatch
// finishes. With extension handlers the call waits for their dispatch, as
// upstream awaits them. Notifying the cache warmer is upstream
// _emitAgentSettled's first step (this._cacheWarmer?.onAgentSettled()),
// ahead of the event dispatch itself.
func (s *Session) emitAgentSettled() {
	s.OnAgentSettled()
	s.runState.mu.Lock()
	s.runState.cancel = nil
	s.runState.mu.Unlock()
	s.runState.active.Store(false)
	if !s.hasAgentLoopHandlers() {
		s.emitOrderedEvent(agent.AgentSettledEvent{})
		return
	}
	s.runState.settling.Store(true)
	s.emitOrderedEventSync(agent.AgentSettledEvent{})
	s.runState.settling.Store(false)
}

// runDeferredSettledActions runs the prompts deferred during agent_settled,
// in order, and then wakes idle waiters (upstream _emitAgentSettled tail).
func (s *Session) runDeferredSettledActions() {
	for {
		s.runState.mu.Lock()
		deferred := s.runState.deferred
		s.runState.deferred = nil
		s.runState.mu.Unlock()
		if len(deferred) == 0 {
			break
		}
		for _, action := range deferred {
			action()
		}
	}
	s.notifyIdleWaiters()
}

// agentLoopExtensionEvents are the events the session dispatches to
// extensions for a run.
var agentLoopExtensionEvents = []string{
	icodingagent.EventAgentStart, icodingagent.EventAgentEnd, icodingagent.EventAgentSettled,
	icodingagent.EventTurnStart, icodingagent.EventTurnEnd,
	icodingagent.EventMessageStart, icodingagent.EventMessageUpdate, icodingagent.EventMessageEnd,
	icodingagent.EventToolExecutionStart, icodingagent.EventToolExecutionUpdate, icodingagent.EventToolExecutionEnd,
}

// hasAgentLoopHandlers reports whether an extension handles any run event.
func (s *Session) hasAgentLoopHandlers() bool {
	runner := s.currentRunner()
	if runner == nil {
		return false
	}
	return slices.ContainsFunc(agentLoopExtensionEvents, runner.HasHandlers)
}

// SendUserMessage delivers an extension's user message (upstream
// sendUserMessage → prompt with source "extension" and no template
// expansion). content is a string or an array of text and image blocks. It
// returns only content and option errors; like upstream, the prompt itself
// runs asynchronously and its failures are reported as extension errors.
func (s *Session) SendUserMessage(content any, deliverAs extension.DeliverAs) error {
	text, images, err := extensionUserMessageContent(content)
	if err != nil {
		return fmt.Errorf("sendUserMessage: %w", err)
	}
	switch deliverAs {
	case "", extension.DeliverAsSteer, extension.DeliverAsFollowUp:
	default:
		return fmt.Errorf("sendUserMessage: unsupported deliverAs %q", deliverAs)
	}
	if s.runState.settling.Load() {
		s.runState.mu.Lock()
		s.runState.deferred = append(s.runState.deferred, func() {
			s.reportRuntimeError("send_user_message", s.promptFromExtension(text, images, deliverAs, true))
		})
		s.runState.mu.Unlock()
		return nil
	}
	s.reportRuntimeError("send_user_message", s.promptFromExtension(text, images, deliverAs, false))
	return nil
}

// promptFromExtension is upstream prompt() for an extension message: input
// handlers, then queueing during a run or a new run. wait runs a new run on
// the calling goroutine; otherwise it runs in the background.
func (s *Session) promptFromExtension(text string, images []ai.ImageContent, deliverAs extension.DeliverAs, wait bool) error {
	if s.IsCompacting() {
		return errPromptDuringCompaction
	}
	text, images, handled, err := s.RunInputHandlers(s.backgroundContext(), text, images, extension.InputSourceExtension, string(deliverAs))
	if err != nil || handled {
		return err
	}
	if s.IsStreaming() {
		switch deliverAs {
		case extension.DeliverAsFollowUp:
			s.FollowUp(text, images)
		case extension.DeliverAsSteer:
			s.Steer(text, images)
		default:
			return errAgentAlreadyProcessing
		}
		return nil
	}
	content := BuildUserContent(text, images)
	if wait {
		_, err := s.SendContent(s.backgroundContext(), content)
		return ignoreCancellation(err)
	}
	ctx := s.backgroundContext()
	s.runState.background.Go(func() {
		_, err := s.SendContent(ctx, content)
		s.reportRuntimeError("send_user_message", ignoreCancellation(err))
	})
	return nil
}

// RunInputHandlers runs the extension input handlers for user input and
// returns the text and images to use, or handled when an extension consumed
// it (upstream _runInputHandlers). behavior ("steer" or "followUp") reaches
// the handlers only while a run is active, as upstream passes it only when
// streaming. A transform without images keeps the original images.
func (s *Session) RunInputHandlers(ctx context.Context, text string, images []ai.ImageContent, source extension.InputSource, behavior string) (string, []ai.ImageContent, bool, error) {
	if !s.IsStreaming() {
		behavior = ""
	}
	return icodingagent.RunInputHandlers(ctx, s.currentRunner(), text, images, source, behavior)
}

// backgroundContext is the lifetime of runs the session starts itself.
func (s *Session) backgroundContext() context.Context {
	s.runState.mu.Lock()
	defer s.runState.mu.Unlock()
	if s.runState.bgCtx == nil {
		s.runState.bgCtx, s.runState.bgCancel = context.WithCancel(context.Background())
	}
	return s.runState.bgCtx
}

// shutdownRuns cancels the runs the session started itself and waits for
// them. Close calls it after the event funnel closes, so none can block.
func (s *Session) shutdownRuns() {
	s.runState.shutdownOnce.Do(func() {
		s.runState.mu.Lock()
		if s.runState.bgCancel != nil {
			s.runState.bgCancel()
		} else {
			s.runState.bgCtx, s.runState.bgCancel = context.WithCancel(context.Background())
			s.runState.bgCancel()
		}
		s.runState.mu.Unlock()
		s.runState.background.Wait()
	})
}

// reportRuntimeError reports a failed extension-started action through the
// extension error listeners, as upstream's runner.emitError with
// extensionPath "<runtime>".
func (s *Session) reportRuntimeError(event string, err error) {
	if err == nil {
		return
	}
	if runner := s.currentRunner(); runner != nil {
		runner.EmitError(&extension.ExtensionError{ExtensionPath: runtimeErrorPath, Event: event, Error: err.Error()})
	}
}

func ignoreCancellation(err error) error {
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// extensionUserMessageContent normalizes sendUserMessage content: a string,
// or text and image blocks whose texts join with "\n" (upstream
// sendUserMessage).
func extensionUserMessageContent(raw any) (string, []ai.ImageContent, error) {
	var blocks []ai.UserContentBlock
	switch value := raw.(type) {
	case string:
		return value, nil, nil
	case ai.UserText:
		return string(value), nil, nil
	case ai.UserContentBlocks:
		blocks = value
	case []ai.UserContentBlock:
		blocks = value
	default:
		encoded, err := json.Marshal(raw)
		if err != nil {
			return "", nil, fmt.Errorf("user content must be a string or array: %w", err)
		}
		var wire []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Data     string `json:"data"`
			MimeType string `json:"mimeType"`
		}
		if err := json.Unmarshal(encoded, &wire); err != nil {
			return "", nil, fmt.Errorf("user content must be a string or array")
		}
		for i, block := range wire {
			switch block.Type {
			case "text":
				blocks = append(blocks, ai.TextContent{Text: block.Text})
			case "image":
				blocks = append(blocks, ai.ImageContent{Data: block.Data, MimeType: block.MimeType})
			default:
				return "", nil, fmt.Errorf("user content[%d] has unsupported type %q", i, block.Type)
			}
		}
	}
	var texts []string
	var images []ai.ImageContent
	for _, block := range blocks {
		switch block := block.(type) {
		case ai.TextContent:
			texts = append(texts, block.Text)
		case ai.ImageContent:
			images = append(images, block)
		}
	}
	return strings.Join(texts, "\n"), images, nil
}
