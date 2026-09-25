package agent

import (
	"context"
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

// runLoop is the main agent loop shared by Send and Continue. Mirrors upstream
// runAgentLoop/runAgentLoopContinue plus runLoop (packages/agent/src/agent-loop.ts):
// the messages appended at runStart are replayed as the prompt's lifecycle
// events before the loop starts.
func (a *Agent) runLoop(ctx context.Context, cfg agentLoopConfig, runStart int) ([]AgentMessage, error) {
	// Defense for every runLoop entry (Send and Continue): never stream, emit
	// lifecycle events, or dereference the model when none is usable.
	if err := a.ensureModel(); err != nil {
		return a.messages, err
	}
	a.stateMu.RLock()
	run := &loopRun{
		a:             a,
		ctx:           ctx,
		cfg:           cfg,
		context:       slices.Clone(a.messages),
		newMessages:   slices.Clone(a.messages[runStart:]),
		model:         a.opts.Model,
		thinking:      a.opts.ThinkingLevel,
		stateRevision: a.stateRevision,
	}
	a.stateMu.RUnlock()
	err, failure := catchRunFailure(func() error {
		a.emit(AgentStartEvent{})
		a.emit(TurnStartEvent{TurnIndex: 0, Timestamp: time.Now()})
		for _, msg := range a.messages[runStart:] {
			// message_start carries its own copy: a message_end replacement is
			// applied in place to the message the transcript and message_end share.
			a.emit(MessageStartEvent{Message: startEventMessage(msg)})
			a.emit(MessageEndEvent{Message: msg})
		}
		return run.loop()
	})
	// A handler fails only at message_end on this goroutine, after the
	// response or tool batch it records has finished, so no work is in flight.
	if failure != nil {
		return a.messages, a.handleRunFailure(failure, ctx.Err() != nil, run.model)
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
				preparedMessages = r.prepareNextTurn(*lastCompletedTurn)
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
	return r.cfg.finishTurn(r.ctx, turn)
}

// prepareNextTurn applies PrepareNextTurn's replacement state and returns the
// messages it asks to append before the next request.
func (r *loopRun) prepareNextTurn(turn AgentTurnContext) []AgentMessage {
	if r.cfg.prepareNextTurn == nil {
		return nil
	}
	update := r.cfg.prepareNextTurn(r.ctx, turn)
	if update == nil {
		return nil
	}
	r.applyUpdate(update.Context, update.Model, update.ThinkingLevel)
	return update.Messages
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
	update := r.cfg.prepareRequest(r.ctx, PrepareRequestContext{
		Context:       slices.Clone(r.context),
		Model:         r.model,
		ThinkingLevel: thinking,
	})
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
	r.a.appendMessages(msg)
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
	r.a.appendMessages(msg)
	r.context = append(r.context, msg)
	r.newMessages = append(r.newMessages, msg)
}

func (r *loopRun) emitTurnEnd(assistant *AssistantMessage, toolResults []ToolResultMessage) {
	turnDur := r.a.timings.EndTurn()
	r.a.emit(TurnEndEvent{TurnIndex: r.turnIndex, Message: AgentMessage{Assistant: assistant}, ToolResults: toolResults})
	r.a.emit(TimingEvent{Kind: "turn", Duration: turnDur, Snapshot: r.a.timings.Snapshot()})
	r.turnIndex++
}

func (r *loopRun) endRun() {
	r.a.emit(TimingEvent{Kind: "session_end", Snapshot: r.a.timings.Snapshot()})
	r.a.emit(AgentEndEvent{Messages: r.a.agentEndMessages()})
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
	llmMsgs := ConvertToLLM(contextMsgs, r.model)
	if transform := a.opts.TransformLLMMessages; transform != nil {
		llmMsgs = transform(llmMsgs)
	}
	if a.forcedSystemPrompt != nil {
		llmMsgs = projectSystemPrompt(llmMsgs, *a.forcedSystemPrompt)
	}

	transcript := ai.NormalizeContext(ai.Context{Messages: llmMsgs})
	streamOpts := ai.StreamOptions{
		Thinking:         r.thinking,
		ThinkingBudgets:  a.opts.ThinkingBudgets,
		IsReasoning:      r.model.Capabilities.MaxThinking != "",
		ModelCost:        r.model.CostRates(),
		SessionID:        a.opts.SessionID,
		Transport:        a.opts.Transport,
		OnPayload:        a.beforeProviderHook,
		TransformHeaders: a.transformHeaders,
	}
	// Upstream buildBaseOptions always sends model.maxTokens, clamped to the
	// context the request leaves.
	if limit := r.model.Capabilities.MaxOutputTokens; limit > 0 {
		streamOpts.MaxTokens = ai.ClampMaxTokensToContext(r.model, transcript, limit)
	}

	streamFn := a.opts.StreamFn
	if streamFn == nil {
		streamFn = streamWithProvider
	}
	stream, err := streamFn(r.ctx, r.model, transcript, streamOpts)
	if err != nil {
		// Synthesize an error assistant message so the session-level retry
		// loop can classify the error (rate limit, network, etc.) and retry.
		// Mirrors upstream providers, which push an error event carrying
		// stopReason and errorMessage instead of throwing, and report
		// "aborted" when the request's signal was aborted.
		stopReason := ai.StopReasonError
		if r.ctx.Err() != nil {
			stopReason = ai.StopReasonAborted
		}
		errorAssistant := &AssistantMessage{
			Role:         RoleAssistant,
			Content:      []ai.AssistantContentBlock{ai.TextContent{Text: ""}},
			StopReason:   stopReason,
			ErrorMessage: err.Error(),
			Timestamp:    time.Now().UnixMilli(),
			Provider:     r.model.Provider.ID(),
			ModelID:      r.model.ID,
		}
		a.emit(MessageStartEvent{Message: AgentMessage{Assistant: cloneAssistantMessage(errorAssistant)}})
		a.emit(MessageEndEvent{Message: AgentMessage{Assistant: errorAssistant}})
		if stopReason == ai.StopReasonAborted {
			// A local cancellation ends the run like one during streaming.
			return errorAssistant, nil, r.ctx.Err()
		}
		return errorAssistant, nil, nil
	}
	return a.consumeStream(r.ctx, stream, r.model)
}

func (r *loopRun) requestError(err error) *AssistantMessage {
	reason := ai.StopReasonError
	if r.ctx.Err() != nil {
		reason = ai.StopReasonAborted
	}
	message := &AssistantMessage{
		Role: RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: ""}}, StopReason: reason,
		ErrorMessage: err.Error(), Timestamp: time.Now().UnixMilli(), Provider: r.model.Provider.ID(), ModelID: r.model.ID,
	}
	r.a.emit(MessageStartEvent{Message: AgentMessage{Assistant: cloneAssistantMessage(message)}})
	r.a.emit(MessageEndEvent{Message: AgentMessage{Assistant: message}})
	return message
}
