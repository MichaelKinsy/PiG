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
		MaxRetryDelayMs:       a.opts.MaxRetryDelayMs,
	}
	convertToLlm, toolExecution := a.opts.ConvertToLlm, a.opts.ToolExecution
	finishTurn, prepareRequest, prepareNextTurn := a.opts.FinishTurn, a.opts.PrepareRequest, a.nextTurnHook()
	a.stateMu.RUnlock()
	return AgentLoopConfig{
		Model:                model,
		StreamOptions:        streamOptions,
		ConvertToLlm:         convertToLlm,
		TransformLLMMessages: a.opts.TransformLLMMessages,
		TransformContext:     a.transformProviderContext,
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
		BeforeToolCall:      a.BeforeToolCall,
		AfterToolCall:       a.AfterToolCall,
		BeforeToolCallHooks: a.opts.BeforeToolCallHooks,
		AfterToolCallHooks:  a.opts.AfterToolCallHooks,
		PrepareToolResult:   a.opts.PrepareToolResult,
		MaxTurns:            a.opts.MaxTurns,
		SessionFile:         a.opts.SessionFile,
	}
}

// transformProviderContext applies the context transform installed when a request starts.
func (a *Agent) transformProviderContext(ctx context.Context, messages []AgentMessage) ([]AgentMessage, error) {
	if a.transformContext == nil {
		return messages, nil
	}
	return a.transformContext(ctx, messages)
}

// loopHost connects a run to this agent: events reduce into agent state, tools and the stream function are read when a request needs them, and a SetModel or SetThinkingLevel call since the last request takes effect on the next one.
// revision is the stateRevision of the model and thinking level cfg carries.
func (a *Agent) loopHost(cfg AgentLoopConfig, revision uint64) *loopHost {
	// upstream: agent.ts:460 createContextSnapshot copies state.tools when the run starts; a later SetTools reaches the run through a context update.
	tools := a.toolList()
	return &loopHost{
		cfg:            cfg,
		agentLifecycle: true,
		sink:           func(event AgentEvent) error { a.emit(event); return nil },
		tools:          func() []AgentTool { return tools },
		streamFn: func() StreamFn {
			a.stateMu.RLock()
			defer a.stateMu.RUnlock()
			if a.opts.StreamFn != nil {
				return a.opts.StreamFn
			}
			return a.opts.DefaultStreamFn
		},
		refreshModel: func() (*ai.Model, ai.ModelThinkingLevel, bool) {
			a.stateMu.RLock()
			defer a.stateMu.RUnlock()
			if a.stateRevision == revision {
				return nil, "", false
			}
			revision = a.stateRevision
			return a.opts.Model, a.opts.ThinkingLevel, true
		},
		requestOptions: func(options *ai.StreamOptions) {
			options.OnPayload = a.beforeProviderHook
			options.TransformHeaders = a.transformHeaders
		},
		timings: a.timings,
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
	host := a.loopHost(cfg, a.stateRevision)
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
			r.refreshModel()
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

// refreshModel picks up SetModel/SetThinkingLevel calls made since the last
// request, the way upstream's session hooks read agent.state before each one.
func (r *loopRun) refreshModel() {
	if r.h.refreshModel == nil {
		return
	}
	if model, thinking, changed := r.h.refreshModel(); changed {
		r.model, r.thinking = model, thinking
	}
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
	} else {
		llmMsgs = ConvertToLLM(NormalizeMessages(contextMsgs, r.model))
	}
	if transform := h.cfg.TransformLLMMessages; transform != nil {
		llmMsgs = transform(llmMsgs)
	}
	if h.cfg.SystemPromptOverride != nil {
		if prompt := h.cfg.SystemPromptOverride(); prompt != nil {
			llmMsgs = projectSystemPrompt(llmMsgs, *prompt)
		}
	}

	transcript := ai.NormalizeContext(ai.Context{Messages: llmMsgs})
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
	streamOpts.Thinking = r.thinking.ReasoningOption()
	streamOpts.IsReasoning = r.model.Capabilities.MaxThinking != ""
	streamOpts.ModelCost = r.model.CostRates()
	if h.requestOptions != nil {
		h.requestOptions(&streamOpts)
	}
	// Upstream buildBaseOptions always sends model.maxTokens, clamped to the
	// context the request leaves.
	if limit := r.model.Capabilities.MaxOutputTokens; limit > 0 && streamOpts.MaxTokens == 0 {
		streamOpts.MaxTokens = ai.ClampMaxTokensToContext(r.model, transcript, limit)
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
			// A failed setup is timed like Pi's lazyStream error: the request start is its timestamp and the stream's own clock gives durationMs.
			// upstream: packages/ai/src/api/lazy.ts createSetupErrorMessage; packages/ai/src/utils/event-stream.ts #time
			Timestamp:     requestStarted.UnixMilli(),
			DurationMs:    new(max(int64(0), int64(math.Floor(float64(time.Since(requestStarted))/float64(time.Millisecond)+0.5)))),
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
