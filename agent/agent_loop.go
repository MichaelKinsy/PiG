package agent

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// createLoopConfig mirrors upstream Agent.createLoopConfig. With
// skipInitialSteeringPoll the first steering poll returns nothing: Continue
// already drained the steering message that seeds the run. Hooks are captured
// when the run starts; the queue getters are the only way the loop reads
// steering and follow-up messages.
func (a *Agent) createLoopConfig(skipInitialSteeringPoll bool) AgentLoopConfig {
	skip := skipInitialSteeringPoll
	// The run captures the assignable properties once, under the lock their setters take.
	a.stateMu.RLock()
	model, thinking := a.opts.Model, a.opts.ThinkingLevel
	streamOptions := ai.StreamOptions{
		Thinking:              thinking.ReasoningOption(),
		ThinkingBudgets:       a.opts.ThinkingBudgets,
		SessionID:             a.opts.SessionID,
		Transport:             a.opts.Transport,
		OnProviderStreamEvent: a.opts.OnProviderStreamEvent,
		OnResponse:            a.opts.OnResponse,
		OnPayload:             a.beforeProviderHook,
		MaxRetryDelayMs:       a.opts.MaxRetryDelayMs,
	}
	convertToLlm, toolExecution := a.opts.ConvertToLlm, a.opts.ToolExecution
	transformContext := a.transformContext
	beforeToolCallHooks, afterToolCallHooks := slices.Clone(a.opts.BeforeToolCallHooks), slices.Clone(a.opts.AfterToolCallHooks)
	beforeToolCall, afterToolCall := a.BeforeToolCall, a.AfterToolCall
	finishTurn, prepareRequest, prepareNextTurn := a.opts.FinishTurn, a.opts.PrepareRequest, a.nextTurnHook()
	transformLLMMessages, prepareToolResult, sessionFile := a.opts.TransformLLMMessages, a.opts.PrepareToolResult, a.opts.SessionFile
	a.stateMu.RUnlock()
	if prepareNextTurn != nil {
		// agent.ts:484-492 builds the closure only when a hook is set now, then reads both properties on every call: an assignment during the run redirects or clears what later turns call.
		prepareNextTurn = func(ctx context.Context, turn PrepareNextTurnContext) (*AgentLoopTurnUpdate, error) {
			a.stateMu.RLock()
			current := a.nextTurnHook()
			a.stateMu.RUnlock()
			if current == nil {
				return nil, nil
			}
			return current(ctx, turn)
		}
	}
	return AgentLoopConfig{
		Model:                model,
		StreamOptions:        streamOptions,
		ConvertToLlm:         convertToLlm,
		TransformLLMMessages: transformLLMMessages,
		TransformContext:     transformContext,
		GetAPIKey:            a.GetAPIKeyFunc(),
		SystemPromptOverride: a.systemPromptOverride,
		GetSteeringMessages: func() []AgentMessage {
			if skip {
				skip = false
				return nil
			}
			return a.steeringQueue.Drain()
		},
		GetFollowUpMessages: a.followUpQueue.Drain,
		FinishTurn:          finishTurn,
		PrepareRequest:      prepareRequest,
		PrepareNextTurn:     prepareNextTurn,
		ToolExecution:       toolExecution,
		BeforeToolCall:      beforeToolCall,
		AfterToolCall:       afterToolCall,
		BeforeToolCallHooks: beforeToolCallHooks,
		AfterToolCallHooks:  afterToolCallHooks,
		PrepareToolResult:   prepareToolResult,
		MaxTurns:            a.opts.MaxTurns,
		SessionFile:         sessionFile,
	}
}

// loopHost connects a run to this agent: events reduce into agent state, the stream function is fixed when the run starts (agent.ts:443,455 pass this.streamFunction to runAgentLoop once), tools are read when a request needs them, and a SetModel or SetThinkingLevel call during the run applies to the next run.
// The model and thinking level are fixed by cfg when the run starts (agent.ts:471-472). The caller holds stateMu.
func (a *Agent) loopHost(cfg AgentLoopConfig) *loopHost {
	// upstream: agent.ts:460 createContextSnapshot copies state.tools when the run starts; a later SetTools reaches the run through a context update.
	tools := a.toolList()
	runStreamFn := a.opts.StreamFn
	if runStreamFn == nil {
		runStreamFn = a.opts.DefaultStreamFn
	}
	return &loopHost{
		cfg:            cfg,
		agentLifecycle: true,
		validated:      a.validated,
		llm:            &a.llm,
		sink:           func(event AgentEvent) error { a.emit(event); return nil },
		tools:          func() []AgentTool { return tools },
		streamFn:       func() StreamFn { return runStreamFn },
		requestOptions: func(options *ai.StreamOptions) {
			a.stateMu.RLock()
			options.TransformHeaders = a.transformHeaders
			a.stateMu.RUnlock()
		},
		timings:                a.timings,
		toolUpdatesObserved:    a.toolUpdatesObserved,
		messageUpdatesObserved: a.messageUpdatesObserved,
		unobservedUpdate:       a.reduceUnobservedUpdate,
	}
}

// loopRun holds the state of one loop invocation. context is upstream's
// currentContext.messages: PrepareNextTurn and PrepareRequest may replace it
// without replacing the agent transcript, as upstream keeps the
// transcript separate.
type loopRun struct {
	h           *loopHost
	ctx         context.Context
	context     []AgentMessage
	newMessages []AgentMessage
	model       *ai.Model
	thinking    ai.ModelThinkingLevel
	turnIndex   int
	// toolResultsEndRun is set when the run legitimately ends on tool results:
	// the whole batch asked to terminate, or FinishTurn ended the run.
	toolResultsEndRun bool
}

func newLoopRun(ctx context.Context, host *loopHost, messages, newMessages []AgentMessage) *loopRun {
	return &loopRun{h: host, ctx: ctx, context: messages, newMessages: newMessages, model: host.cfg.Model, thinking: ai.ModelThinkingLevel(host.cfg.Thinking)}
}

// Ports packages/agent/src/agent-loop.ts.
// runAgentLoopContinue validates a low-level continuation before emitting events.
func (a *Agent) runAgentLoopContinue(ctx context.Context, cfg AgentLoopConfig) ([]AgentMessage, error) {
	if err := validateContinuation(a.messages); err != nil {
		return a.messages, err
	}
	return a.runLoop(ctx, cfg, nil)
}

// runLoop is the main agent loop shared by Send and Continue. Mirrors upstream
// runAgentLoop/runAgentLoopContinue plus runLoop (packages/agent/src/agent-loop.ts)
// with Agent.runWithLifecycle around it.
// The prompt batch enters provider context immediately and public state only
// when each message_end is emitted.
func (a *Agent) runLoop(ctx context.Context, cfg AgentLoopConfig, prompts []AgentMessage) ([]AgentMessage, error) {
	// Defense for every runLoop entry (Send and Continue): never stream, emit
	// lifecycle events, or dereference the model when none is usable.
	if err := a.ensureModel(); err != nil {
		return a.messages, err
	}
	a.stateMu.Lock()
	if !cfg.agentStarted {
		a.errorMessage = ""
	}
	cfg.Model, cfg.Thinking = a.opts.Model, a.opts.ThinkingLevel.ReasoningOption()
	host := a.loopHost(cfg)
	run := newLoopRun(ctx, host, slices.Concat(a.messages, prompts), slices.Clone(prompts))
	a.stateMu.Unlock()
	err, failure := run.execute(prompts, nil)
	// Awaited callback failures enter the same failed-turn lifecycle as persistence and provider-preparation failures.
	if failure != nil {
		return a.messages, a.handleRunFailure(failure, ctx.Err() != nil, run.model)
	}
	// Agent.abort settles its prompt after recording the aborted response.
	// Caller-owned context cancellation still returns its error to Go callers.
	if signal, ok := ctx.(*runDeadlineContext); ok && signal.parent.Err() == nil && errors.Is(err, context.Canceled) {
		err = nil
	}
	return a.messages, run.checkRunEnd(a.messages, err)
}

// checkRunEnd enforces that a run never ends silently mid-task. Upstream
// always answers tool results unless the batch terminated or FinishTurn ended
// the run; a run that otherwise ends on a tool result would leave the session
// idle with no response and no error, so it reports ErrToolResultsUnanswered.
func (r *loopRun) checkRunEnd(transcript []AgentMessage, err error) error {
	if err == nil && r.ctx.Err() == nil && !r.toolResultsEndRun && endsOnToolResult(transcript) {
		return ErrToolResultsUnanswered
	}
	return err
}

// endsOnToolResult reports whether the transcript's last message is a tool
// result.
func endsOnToolResult(messages []AgentMessage) bool {
	return len(messages) > 0 && messages[len(messages)-1].ToolResult != nil
}

// loop mirrors upstream runLoop: the inner loop processes tool calls and
// steering messages, the outer loop follow-up messages and FinishTurn
// continuation requests.
func (r *loopRun) loop() error {
	var lastCompletedTurn *AgentTurnContext
	explicitContinuation := false
	// Check for steering messages at start (user may have typed while waiting).
	pendingMessages := r.h.steering()
	for {
		hasMoreToolCalls := true
		for hasMoreToolCalls || len(pendingMessages) > 0 {
			if limit := r.h.cfg.MaxTurns; limit > 0 && r.turnIndex >= limit {
				// A pig-only safety cap. Stopping here leaves tool results or
				// queued messages unanswered, so it must not look like a
				// finished task.
				r.endRun()
				return fmt.Errorf("%w (%d turns)", ErrMaxTurnsReached, limit)
			}
			var preparedMessages []AgentMessage
			if lastCompletedTurn != nil {
				var err error
				preparedMessages, err = r.prepareNextTurn(*lastCompletedTurn)
				if err != nil && !r.h.agentLifecycle {
					panic(runFailure{err: err})
				}
				if err != nil {
					// Agent.runWithLifecycle handles a rejected preparation as a failed assistant turn.
					failure := r.requestError(err)
					r.appendAssistant(failure)
					r.emitTurnEnd(failure, nil)
					r.h.emit(AgentEndEvent{Messages: []AgentMessage{{Assistant: failure}}})
					return nil
				}
				// Preparation can be long-running (for example, compaction). Pick
				// up steering queued while it ran, but only when the earlier poll
				// returned nothing, so one-at-a-time mode delivers one message.
				if len(pendingMessages) == 0 {
					pendingMessages = r.h.steering()
				}
				r.h.emit(TurnStartEvent{TurnIndex: r.turnIndex, Timestamp: time.Now()})
			}
			r.h.timings.StartTurn()
			for _, msg := range r.h.declareToolChanges(r.context, slices.Concat(preparedMessages, pendingMessages)) {
				r.appendMessage(msg)
			}
			r.prepareRequest()

			turn, done, err := r.runTurn()
			if done {
				return err
			}
			lastCompletedTurn = &turn.context
			if turn.decision != nil && turn.decision.Action == AgentTurnEnd {
				r.toolResultsEndRun = true
				r.endRun()
				return nil
			}
			hasMoreToolCalls = turn.hasMoreToolCalls
			r.toolResultsEndRun = turn.terminated
			explicitContinuation = turn.decision != nil && turn.decision.Action == AgentTurnContinue
			pendingMessages = r.h.steering()
			if hasMoreToolCalls || len(pendingMessages) > 0 {
				explicitContinuation = false
			}
		}

		// Agent would stop here. Check for follow-up messages.
		if followUps := r.h.followUps(); len(followUps) > 0 {
			explicitContinuation = false
			pendingMessages = followUps
			continue
		}
		// No natural request was selected, so fulfill the continuation decision
		// with one context-only turn.
		if explicitContinuation {
			explicitContinuation = false
			continue
		}
		break
	}
	r.endRun()
	return nil
}

// completedTurn is the outcome of one assistant response plus its tool batch.
type completedTurn struct {
	context          AgentTurnContext
	decision         *AgentTurnDecision
	hasMoreToolCalls bool
	terminated       bool // the tool batch asked to terminate the run
}

// runTurn streams one assistant response, executes its tool calls, runs
// FinishTurn, and emits TurnEndEvent. done reports that the run already ended
// (hard exit on an error or aborted response).
func (r *loopRun) runTurn() (completedTurn, bool, error) {
	assistant, toolCalls, err := r.streamAssistantResponse()
	if err != nil {
		// Local cancellation finalizes the same aborted turn as a provider's
		// terminal aborted response. consumeStream already emitted message_end.
		r.appendAssistant(assistant)
		r.finishTurn(r.turnContext(assistant, nil))
		r.emitTurnEnd(assistant, nil)
		r.endRun()
		return completedTurn{}, true, err
	}
	r.appendAssistant(assistant)

	if assistant.StopReason == ai.StopReasonError || assistant.StopReason == ai.StopReasonAborted {
		// The decision is ignored: error and aborted responses are hard exits.
		r.finishTurn(r.turnContext(assistant, nil))
		r.emitTurnEnd(assistant, nil)
		r.endRun()
		return completedTurn{}, true, nil
	}

	var toolResults []ToolResultMessage
	hasMoreToolCalls, terminated := false, false
	if len(toolCalls) > 0 {
		// A "length" stop means the output was cut off by the token limit, so
		// every tool call may carry truncated arguments. Fail them all instead
		// of executing potentially broken calls.
		var batch executedToolCallBatch
		if assistant.StopReason == ai.StopReasonLength {
			batch = r.failToolCallsFromTruncatedMessage(toolCalls)
		} else {
			batch = r.executeToolCalls(assistant, toolCalls)
		}
		toolResults = batch.messages
		hasMoreToolCalls = !batch.terminate
		terminated = batch.terminate
	}

	turn := r.turnContext(assistant, toolResults)
	decision := r.finishTurn(turn)
	r.emitTurnEnd(assistant, toolResults)
	return completedTurn{context: turn, decision: decision, hasMoreToolCalls: hasMoreToolCalls, terminated: terminated}, false, nil
}

func (r *loopRun) turnContext(assistant *AssistantMessage, toolResults []ToolResultMessage) AgentTurnContext {
	return AgentTurnContext{
		Message:     assistant,
		ToolResults: toolResults,
		Context:     AgentContext{Messages: slices.Clone(r.context), Tools: r.h.tools()},
		NewMessages: slices.Clone(r.newMessages),
	}
}

func (r *loopRun) finishTurn(turn AgentTurnContext) *AgentTurnDecision {
	if r.h.cfg.FinishTurn == nil {
		return nil
	}
	decision, err := r.h.cfg.FinishTurn(r.ctx, turn)
	if err != nil {
		panic(runFailure{err: err})
	}
	return decision
}

// prepareNextTurn applies PrepareNextTurn's replacement state and returns the
// messages it asks to append before the next request.
func (r *loopRun) prepareNextTurn(turn AgentTurnContext) ([]AgentMessage, error) {
	if r.h.cfg.PrepareNextTurn == nil {
		return nil, nil
	}
	update, err := r.h.cfg.PrepareNextTurn(r.ctx, turn)
	if err != nil || update == nil {
		return nil, err
	}
	r.applyUpdate(update.Context, update.Model, update.ThinkingLevel)
	return update.Messages, nil
}

// prepareRequest runs PrepareRequest immediately before the provider request.
func (r *loopRun) prepareRequest() {
	if r.h.cfg.PrepareRequest == nil {
		return
	}
	thinking := r.thinking
	if thinking == "" {
		thinking = ai.ThinkingOff
	}
	update, err := r.h.cfg.PrepareRequest(r.ctx, PrepareRequestContext{
		Context:       AgentContext{Messages: slices.Clone(r.context), Tools: r.h.tools()},
		Model:         r.model,
		ThinkingLevel: thinking,
	})
	if err != nil {
		panic(runFailure{err: err})
	}
	if update != nil {
		r.applyUpdate(update.Context, update.Model, update.ThinkingLevel)
	}
}

func (r *loopRun) applyUpdate(context *AgentContext, model *ai.Model, thinking *ai.ModelThinkingLevel) {
	if context != nil {
		r.context = slices.Clone(context.Messages)
		// upstream: the executable tools are context.tools for the rest of the run, so tool calls resolve against them.
		tools := slices.Clone(context.Tools)
		r.h.tools = func() []AgentTool { return tools }
	}
	if model != nil {
		r.model = model
	}
	if thinking != nil {
		r.thinking = *thinking
	}
}

// appendMessage emits a message's lifecycle and adds it to the transcript, the
// loop context, and the run's new messages.
func (r *loopRun) appendMessage(msg AgentMessage) {
	r.h.emit(MessageStartEvent{Message: startEventMessage(msg)})
	r.context = append(r.context, msg)
	r.newMessages = append(r.newMessages, msg)
	r.h.emit(MessageEndEvent{Message: msg})
}

// startEventMessage gives message_start its own envelope: a message_end
// replacement overwrites the message the transcript and message_end share,
// and must not reach a message_start a listener may still be reading.
func startEventMessage(msg AgentMessage) AgentMessage {
	out := msg
	switch {
	case msg.System != nil:
		out.System = new(*msg.System)
	case msg.User != nil:
		out.User = new(*msg.User)
	case msg.Assistant != nil:
		out.Assistant = new(*msg.Assistant)
	case msg.ToolResult != nil:
		out.ToolResult = new(*msg.ToolResult)
	case msg.Custom != nil:
		out.Custom = maps.Clone(msg.Custom)
	}
	return out
}

// appendAssistant records a streamed assistant message; consumeStream already
// emitted its lifecycle events.
func (r *loopRun) appendAssistant(assistant *AssistantMessage) {
	msg := AgentMessage{Assistant: assistant}
	r.context = append(r.context, msg)
	r.newMessages = append(r.newMessages, msg)
}

func (r *loopRun) emitTurnEnd(assistant *AssistantMessage, toolResults []ToolResultMessage) {
	r.h.timings.EndTurn()
	r.h.emit(TurnEndEvent{TurnIndex: r.turnIndex, Message: AgentMessage{Assistant: assistant}, ToolResults: toolResults})
	r.turnIndex++
}

func (r *loopRun) endRun() {
	r.h.emit(AgentEndEvent{Messages: slices.Clone(r.newMessages)})
}

// streamAssistantResponse transforms the loop context, converts it for the
// provider, and streams one assistant response. Mirrors upstream
// streamAssistantResponse. A provider that fails before streaming yields an
// error assistant message, as upstream stream functions encode failures in
// the stream.
func (r *loopRun) streamAssistantResponse() (*AssistantMessage, []pendingToolCall, error) {
	h := r.h
	contextMsgs := r.context
	if h.cfg.TransformContext != nil {
		var err error
		contextMsgs, err = h.cfg.TransformContext(r.ctx, contextMsgs)
		if err != nil && !h.agentLifecycle {
			panic(runFailure{err: err})
		}
		if err != nil {
			message := r.requestError(err)
			return message, nil, err
		}
	}
	var llmMsgs []ai.Message
	if convert := h.cfg.ConvertToLlm; convert != nil {
		var err error
		llmMsgs, err = convert(contextMsgs)
		if err != nil {
			panic(runFailure{err: err})
		}
	} else if h.llm != nil {
		llmMsgs = h.llm.convert(contextMsgs)
	} else {
		llmMsgs = ConvertToLLM(contextMsgs)
	}
	if transform := h.cfg.TransformLLMMessages; transform != nil {
		llmMsgs = transform(llmMsgs)
	}
	// pig divergence (D137): the request keeps at most the model's inputLimits.images.maxPerRequest images; upstream sends every image (Pi #10162).
	llmMsgs = limitImagesPerRequest(llmMsgs, r.model)
	if h.cfg.SystemPromptOverride != nil {
		if prompt := h.cfg.SystemPromptOverride(); prompt != nil {
			llmMsgs = projectSystemPrompt(llmMsgs, *prompt)
		}
	}

	transcript := ai.NormalizeContext(ai.Context{Messages: llmMsgs, Frozen: true, Validated: h.validated})
	streamOpts := h.cfg.StreamOptions
	if h.cfg.GetAPIKey != nil {
		// upstream: packages/agent/src/agent-loop.ts:streamAssistantResponse awaits getApiKey without a catch, so its rejection fails the run.
		key, err := h.cfg.GetAPIKey(r.model.ProviderID())
		if err != nil {
			panic(runFailure{err: err})
		}
		if key != "" {
			streamOpts.APIKey = key
		}
	}
	// pig divergence (D48): a real provider repairs orphaned, repeated and out-of-order tool results; the stream function still receives Pi's messages.
	streamOpts.RepairToolFlow = true
	streamOpts.Thinking = r.thinking.ReasoningOption()
	streamOpts.IsReasoning = r.model.Capabilities.MaxThinking != ""
	streamOpts.ModelCost = r.model.CostRates()
	if h.requestOptions != nil {
		h.requestOptions(&streamOpts)
	}

	streamFn := h.streamFn()
	var message *AssistantMessage
	var calls []pendingToolCall
	var requestFailure error
	err := ai.RunStreamContinuation(r.ctx, func(observation *ai.StreamObservation) error {
		var err error
		message, calls, requestFailure, err = r.requestStream(observation, streamFn, transcript, streamOpts)
		return err
	})
	if requestFailure != nil {
		panic(runFailure{err: requestFailure})
	}
	return message, calls, err
}

// upstream: packages/agent/src/agent-loop.ts:402-413 keeps stream creation and iterator adoption on one continuation, including the await of a synchronously returned stream.
// A standalone run reports a stream function's error as requestFailure, as upstream runAgentLoop rejects when its streamFn throws.
func (r *loopRun) requestStream(observation *ai.StreamObservation, streamFn StreamFn, transcript ai.TranscriptContext, streamOpts ai.StreamOptions) (message *AssistantMessage, calls []pendingToolCall, requestFailure, err error) {
	h := r.h
	requestStarted := time.Now()
	// lazy.ts reads the clock for a setup error's timestamp, then the stream it creates reads its own start: two reads.
	streamStarted := time.Now()
	stream, err := streamFn(r.ctx, r.model, transcript, streamOpts)
	observation.Yield()
	if err != nil && !h.agentLifecycle {
		return nil, nil, err, nil
	}
	if err != nil {
		// Synthesize an error assistant message so the session-level retry
		// loop can classify the error (rate limit, network, etc.) and retry.
		// Mirrors upstream providers, which push an error event carrying
		// stopReason and errorMessage instead of throwing, and report
		// "aborted" when the request's signal was aborted. Its result
		// records the requested thinkingLevel like any other final
		// response (upstream agent-loop.ts:409; ai/src/api/lazy.ts:52-58
		// turns a request-setup failure into an error event).
		stopReason := ai.StopReasonError
		if r.ctx.Err() != nil {
			stopReason = ai.StopReasonAborted
		}
		providerID := r.model.ProviderMeta.ProviderID
		if r.model.Provider != nil {
			providerID = r.model.Provider.ID()
		}
		errorAssistant := &AssistantMessage{
			Role:         RoleAssistant,
			Content:      []ai.AssistantContentBlock{ai.TextContent{Text: ""}},
			StopReason:   stopReason,
			ErrorMessage: err.Error(),
			// createSetupErrorMessage builds errorMessage before timestamp.
			ErrorBeforeTimestamp: true,
			// A failed setup is timed like Pi's lazyStream error: the request start is its timestamp and the stream's own clock gives durationMs.
			// upstream: packages/ai/src/api/lazy.ts createSetupErrorMessage; packages/ai/src/utils/event-stream.ts #time
			Timestamp:     requestStarted.UnixMilli(),
			DurationMs:    setupErrorDuration(requestStarted, streamStarted, time.Now()),
			API:           r.model.ProviderMeta.API,
			Provider:      providerID,
			ModelID:       r.model.ID,
			ThinkingLevel: recordedThinkingLevel(r.thinking),
		}
		h.emit(MessageStartEvent{Message: AgentMessage{Assistant: cloneAssistantMessage(errorAssistant)}})
		h.emit(MessageEndEvent{Message: AgentMessage{Assistant: errorAssistant}})
		if stopReason == ai.StopReasonAborted {
			// A local cancellation ends the run like one during streaming.
			return errorAssistant, nil, nil, r.ctx.Err()
		}
		return errorAssistant, nil, nil, nil
	}
	message, calls, err = h.consumeStream(r.ctx, stream, r.model, r.thinking)
	return message, calls, nil, err
}

// setupErrorDuration is event-stream.ts #time for a request-setup error. lazy.ts:50-51 stamps the error with a first
// Date.now() and the stream records a second one as its start, and #time leaves a message untimed when its timestamp is
// before the stream's start: a millisecond that ticks between the two reads drops durationMs. Otherwise it is the
// milliseconds since the stream's start, rounded like Math.round.
func setupErrorDuration(requestStarted, streamStarted, now time.Time) *int64 {
	if requestStarted.UnixMilli() < streamStarted.UnixMilli() {
		return nil
	}
	//portlint:allow numbers a non-negative whole-nanosecond count over 1e6 is never within 1e-6 of a .5 boundary, so floor(x+0.5) is Math.round (event-stream.ts:128)
	return new(max(int64(0), int64(math.Floor(float64(now.Sub(streamStarted))/float64(time.Millisecond)+0.5))))
}

func (r *loopRun) requestError(err error) *AssistantMessage {
	reason := ai.StopReasonError
	if r.ctx.Err() != nil {
		reason = ai.StopReasonAborted
	}
	providerID := r.model.ProviderMeta.ProviderID
	if r.model.Provider != nil {
		providerID = r.model.Provider.ID()
	}
	message := &AssistantMessage{
		Role: RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: ""}}, StopReason: reason,
		API: r.model.ProviderMeta.API, Usage: &ai.Usage{},
		ErrorMessage: err.Error(), Timestamp: time.Now().UnixMilli(), Provider: providerID, ModelID: r.model.ID,
	}
	r.h.emit(MessageStartEvent{Message: AgentMessage{Assistant: cloneAssistantMessage(message)}})
	r.h.emit(MessageEndEvent{Message: AgentMessage{Assistant: message}})
	return message
}
