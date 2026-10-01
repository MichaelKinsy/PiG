package agent

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// agentLoopConfig is the per-run configuration the loop reads. Mirrors the
// AgentLoopConfig that upstream Agent.createLoopConfig builds for each run
// (packages/agent/src/agent.ts): hooks are captured when the run starts, and
// the queue getters are the only way the loop reads steering and follow-up
// messages.
type agentLoopConfig struct {
	agentStarted        bool
	getSteeringMessages func() []AgentMessage
	getFollowUpMessages func() []AgentMessage
	finishTurn          FinishTurn
	prepareRequest      PrepareRequest
	prepareNextTurn     PrepareNextTurn
	toolExecution       ToolExecutionMode
}

// createLoopConfig mirrors upstream Agent.createLoopConfig. With
// skipInitialSteeringPoll the first steering poll returns nothing: Continue
// already drained the steering message that seeds the run.
func (a *Agent) createLoopConfig(skipInitialSteeringPoll bool) agentLoopConfig {
	skip := skipInitialSteeringPoll
	return agentLoopConfig{
		getSteeringMessages: func() []AgentMessage {
			if skip {
				skip = false
				return nil
			}
			return a.steeringQueue.Drain()
		},
		getFollowUpMessages: a.followUpQueue.Drain,
		finishTurn:          a.opts.FinishTurn,
		prepareRequest:      a.opts.PrepareRequest,
		prepareNextTurn:     a.opts.PrepareNextTurn,
		toolExecution:       a.opts.ToolExecution,
	}
}

// loopRun holds the state of one runLoop invocation. context is upstream's
// currentContext.messages: PrepareNextTurn and PrepareRequest may replace it
// without replacing the agent transcript (a.messages), as upstream keeps the
// loop context apart from Agent.state.messages.
type loopRun struct {
	a             *Agent
	ctx           context.Context
	cfg           agentLoopConfig
	context       []AgentMessage
	newMessages   []AgentMessage
	model         *ai.Model
	thinking      ai.ThinkingLevel
	stateRevision uint64
	turnIndex     int
	// toolResultsEndRun is set when the run legitimately ends on tool results:
	// the whole batch asked to terminate, or FinishTurn ended the run.
	toolResultsEndRun bool
}

// Ports packages/agent/src/agent-loop.ts.
// runAgentLoopContinue validates a low-level continuation before emitting events.
func (a *Agent) runAgentLoopContinue(ctx context.Context, cfg agentLoopConfig) ([]AgentMessage, error) {
	if len(a.messages) == 0 {
		return a.messages, errors.New("Cannot continue: no messages in context")
	}
	if a.messages[len(a.messages)-1].Assistant != nil {
		return a.messages, errors.New("Cannot continue from message role: assistant")
	}
	return a.runLoop(ctx, cfg, nil)
}

// runLoop is the main agent loop shared by Send and Continue. Mirrors upstream
// runAgentLoop/runAgentLoopContinue plus runLoop (packages/agent/src/agent-loop.ts).
// The prompt batch enters provider context immediately and public state only
// when each message_end is emitted.
func (a *Agent) runLoop(ctx context.Context, cfg agentLoopConfig, prompts []AgentMessage) ([]AgentMessage, error) {
	// Defense for every runLoop entry (Send and Continue): never stream, emit
	// lifecycle events, or dereference the model when none is usable.
	if err := a.ensureModel(); err != nil {
		return a.messages, err
	}
	a.stateMu.Lock()
	if !cfg.agentStarted {
		a.errorMessage = ""
	}
	run := &loopRun{
		a:             a,
		ctx:           ctx,
		cfg:           cfg,
		context:       slices.Concat(a.messages, prompts),
		newMessages:   slices.Clone(prompts),
		model:         a.opts.Model,
		thinking:      a.opts.ThinkingLevel,
		stateRevision: a.stateRevision,
	}
	a.stateMu.Unlock()
	err, failure := catchRunFailure(func() error {
		if !cfg.agentStarted {
			a.emit(AgentStartEvent{})
		}
		a.emit(TurnStartEvent{TurnIndex: 0, Timestamp: time.Now()})
		for _, msg := range prompts {
			// message_start carries its own copy: a message_end replacement is
			// applied in place to the message the transcript and message_end share.
			a.emit(MessageStartEvent{Message: startEventMessage(msg)})
			a.emit(MessageEndEvent{Message: msg})
		}
		return run.loop()
	})
	// Awaited callback failures enter the same failed-turn lifecycle as persistence and provider-preparation failures.
	if failure != nil {
		return a.messages, a.handleRunFailure(failure, ctx.Err() != nil, run.model)
	}
	// Agent.abort settles its prompt after recording the aborted response.
	// Caller-owned context cancellation still returns its error to Go callers.
	if signal, ok := ctx.(*runDeadlineContext); ok && signal.parent.Err() == nil && errors.Is(err, context.Canceled) {
		err = nil
	}
	return a.messages, run.checkRunEnd(err)
}

// checkRunEnd enforces that a run never ends silently mid-task. Upstream
// always answers tool results unless the batch terminated or FinishTurn ended
// the run; a run that otherwise ends on a tool result would leave the session
// idle with no response and no error, so it reports ErrToolResultsUnanswered.
func (r *loopRun) checkRunEnd(err error) error {
	if err == nil && r.ctx.Err() == nil && !r.toolResultsEndRun && endsOnToolResult(r.a.messages) {
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
	pendingMessages := r.cfg.getSteeringMessages()
	for {
		hasMoreToolCalls := true
		for hasMoreToolCalls || len(pendingMessages) > 0 {
			if limit := r.a.opts.MaxTurns; limit > 0 && r.turnIndex >= limit {
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
				if err != nil {
					// Agent.runWithLifecycle handles a rejected preparation as a failed assistant turn.
					failure := r.requestError(err)
					r.appendAssistant(failure)
					r.emitTurnEnd(failure, nil)
					r.a.emit(AgentEndEvent{Messages: []AgentMessage{{Assistant: failure}}})
					return nil
				}
				// Preparation can be long-running (for example, compaction). Pick
				// up steering queued while it ran, but only when the earlier poll
				// returned nothing, so one-at-a-time mode delivers one message.
				if len(pendingMessages) == 0 {
					pendingMessages = r.cfg.getSteeringMessages()
				}
				r.a.emit(TurnStartEvent{TurnIndex: r.turnIndex, Timestamp: time.Now()})
			}
			r.a.timings.StartTurn()
			for _, msg := range r.a.declareToolChanges(r.context, slices.Concat(preparedMessages, pendingMessages)) {
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
			pendingMessages = r.cfg.getSteeringMessages()
			if hasMoreToolCalls || len(pendingMessages) > 0 {
				explicitContinuation = false
			}
		}

		// Agent would stop here. Check for follow-up messages.
		if followUps := r.cfg.getFollowUpMessages(); len(followUps) > 0 {
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
			batch = r.executeToolCalls(toolCalls)
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
	r.a.stateMu.RLock()
	defer r.a.stateMu.RUnlock()
	if r.a.stateRevision != r.stateRevision {
		r.model = r.a.opts.Model
		r.thinking = r.a.opts.ThinkingLevel
		r.stateRevision = r.a.stateRevision
	}
}

func (r *loopRun) turnContext(assistant *AssistantMessage, toolResults []ToolResultMessage) AgentTurnContext {
	return AgentTurnContext{
		Message:     assistant,
		ToolResults: toolResults,
		Context:     slices.Clone(r.context),
		NewMessages: slices.Clone(r.newMessages),
	}
}

func (r *loopRun) finishTurn(turn AgentTurnContext) *AgentTurnDecision {
	if r.cfg.finishTurn == nil {
		return nil
	}
	decision, err := r.cfg.finishTurn(r.ctx, turn)
	if err != nil {
		panic(runFailure{err: err})
	}
	return decision
}

// prepareNextTurn applies PrepareNextTurn's replacement state and returns the
// messages it asks to append before the next request.
func (r *loopRun) prepareNextTurn(turn AgentTurnContext) ([]AgentMessage, error) {
	if r.cfg.prepareNextTurn == nil {
		return nil, nil
	}
	update, err := r.cfg.prepareNextTurn(r.ctx, turn)
	if err != nil || update == nil {
		return nil, err
	}
	r.applyUpdate(update.Context, update.Model, update.ThinkingLevel)
	return update.Messages, nil
}

// prepareRequest runs PrepareRequest immediately before the provider request.
func (r *loopRun) prepareRequest() {
	if r.cfg.prepareRequest == nil {
		return
	}
	thinking := r.thinking
	if thinking == "" {
		thinking = ai.ThinkingOff
	}
	update, err := r.cfg.prepareRequest(r.ctx, PrepareRequestContext{
		Context:       slices.Clone(r.context),
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

func (r *loopRun) applyUpdate(context []AgentMessage, model *ai.Model, thinking *ai.ThinkingLevel) {
	if context != nil {
		r.context = slices.Clone(context)
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
	r.a.emit(MessageStartEvent{Message: startEventMessage(msg)})
	r.context = append(r.context, msg)
	r.newMessages = append(r.newMessages, msg)
	r.a.emit(MessageEndEvent{Message: msg})
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
	r.a.timings.EndTurn()
	r.a.emit(TurnEndEvent{TurnIndex: r.turnIndex, Message: AgentMessage{Assistant: assistant}, ToolResults: toolResults})
	r.turnIndex++
}

func (r *loopRun) endRun() {
	r.a.emit(AgentEndEvent{Messages: slices.Clone(r.newMessages)})
}

// streamWithProvider is the default StreamFn: the model's own provider.
func streamWithProvider(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	return model.Provider.Stream(ctx, transcript, options)
}

// streamAssistantResponse transforms the loop context, converts it for the
// provider, and streams one assistant response. Mirrors upstream
// streamAssistantResponse. A provider that fails before streaming yields an
// error assistant message, as upstream stream functions encode failures in
// the stream.
func (r *loopRun) streamAssistantResponse() (*AssistantMessage, []pendingToolCall, error) {
	a := r.a
	contextMsgs := r.context
	if a.transformContext != nil {
		var err error
		contextMsgs, err = a.transformContext(r.ctx, contextMsgs)
		if err != nil {
			message := r.requestError(err)
			return message, nil, err
		}
	}
	var llmMsgs []ai.Message
	if convert := a.opts.ConvertToLlm; convert != nil {
		var err error
		llmMsgs, err = convert(contextMsgs)
		if err != nil {
			panic(runFailure{err: err})
		}
	} else {
		llmMsgs = ConvertToLLM(contextMsgs, r.model)
	}
	if transform := a.opts.TransformLLMMessages; transform != nil {
		llmMsgs = transform(llmMsgs)
	}
	if prompt := a.systemPromptOverride(); prompt != nil {
		llmMsgs = projectSystemPrompt(llmMsgs, *prompt)
	}

	transcript := ai.NormalizeContext(ai.Context{Messages: llmMsgs})
	streamOpts := ai.StreamOptions{
		Thinking:        r.thinking,
		ThinkingBudgets: a.opts.ThinkingBudgets,
		IsReasoning:     r.model.Capabilities.MaxThinking != "",
		ModelCost:       r.model.CostRates(),
		SessionID:       a.opts.SessionID,
		Transport:       a.opts.Transport,
		OnPayload:       a.beforeProviderHook,
		// upstream: agent.ts:475 forwards onProviderStreamEvent to every request.
		OnProviderStreamEvent: a.opts.OnProviderStreamEvent,
		TransformHeaders:      a.transformHeaders,
	}
	// Upstream buildBaseOptions always sends model.maxTokens, clamped to the
	// context the request leaves.
	if limit := r.model.Capabilities.MaxOutputTokens; limit > 0 {
		streamOpts.MaxTokens = ai.ClampMaxTokensToContext(r.model, transcript, limit)
	}

	a.stateMu.RLock()
	streamFn := a.opts.StreamFn
	if streamFn == nil {
		streamFn = a.opts.DefaultStreamFn
	}
	a.stateMu.RUnlock()
	if streamFn == nil {
		streamFn = streamWithProvider
	}
	var message *AssistantMessage
	var calls []pendingToolCall
	err := ai.RunStreamContinuation(r.ctx, func(observation *ai.StreamObservation) error {
		var err error
		message, calls, err = r.requestStream(observation, streamFn, transcript, streamOpts)
		return err
	})
	return message, calls, err
}

// upstream: packages/agent/src/agent-loop.ts:402-413 keeps stream creation and iterator adoption on one continuation, including the await of a synchronously returned stream.
func (r *loopRun) requestStream(observation *ai.StreamObservation, streamFn StreamFn, transcript ai.TranscriptContext, streamOpts ai.StreamOptions) (*AssistantMessage, []pendingToolCall, error) {
	a := r.a
	stream, err := streamFn(r.ctx, r.model, transcript, streamOpts)
	observation.Yield()
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
			Role:          RoleAssistant,
			Content:       []ai.AssistantContentBlock{ai.TextContent{Text: ""}},
			StopReason:    stopReason,
			ErrorMessage:  err.Error(),
			Timestamp:     time.Now().UnixMilli(),
			Provider:      providerID,
			ModelID:       r.model.ID,
			ThinkingLevel: recordedThinkingLevel(r.thinking),
		}
		a.emit(MessageStartEvent{Message: AgentMessage{Assistant: cloneAssistantMessage(errorAssistant)}})
		a.emit(MessageEndEvent{Message: AgentMessage{Assistant: errorAssistant}})
		if stopReason == ai.StopReasonAborted {
			// A local cancellation ends the run like one during streaming.
			return errorAssistant, nil, r.ctx.Err()
		}
		return errorAssistant, nil, nil
	}
	return a.consumeStream(r.ctx, stream, r.model, r.thinking)
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
	r.a.emit(MessageStartEvent{Message: AgentMessage{Assistant: cloneAssistantMessage(message)}})
	r.a.emit(MessageEndEvent{Message: AgentMessage{Assistant: message}})
	return message
}
