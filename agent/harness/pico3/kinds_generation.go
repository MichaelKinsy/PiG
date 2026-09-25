package pico3

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// GenerationHooks are the hook points pi.generation calls.
type GenerationHooks struct {
	// SystemInstructions edits the section draft and may override the tool
	// loadout; a failing handler's edits roll back.
	SystemInstructions func(ctx context.Context, input SystemInstructionsInput, info HookApi) (*SystemInstructionsResult, error)
	// BeforeRequest runs before every request (also on retry and recovery);
	// results chain.
	BeforeRequest func(ctx context.Context, request BeforeRequestInput, info BeforeRequestInfo) (*BeforeRequestInput, error)
	// AfterResponse observes every terminal provider message.
	AfterResponse func(ctx context.Context, message ai.AssistantMessage, info AfterResponseInfo) error
	// OnYield runs on a final answer with nothing queued; the first continue
	// wins and starts a continuation.
	OnYield func(ctx context.Context, answer ai.AssistantMessage, info HookApi) (*YieldResult, error)
}

// BeforeRequestInput is the request a BeforeRequest hook may replace.
type BeforeRequestInput struct {
	Messages []JsonObject
}

// BeforeRequestInfo identifies the request's cutoff.
type BeforeRequestInfo struct {
	HookApi
	Cutoff Id
}

// AfterResponseInfo identifies the attempt.
type AfterResponseInfo struct {
	HookApi
	Attempt int
}

// YieldResult continues the turn with a user message.
type YieldResult struct {
	Continue string
}

func generationHooksOf(handlers any) *GenerationHooks {
	switch typed := handlers.(type) {
	case *GenerationHooks:
		return typed
	case GenerationHooks:
		return &typed
	default:
		return nil
	}
}

// generationPrep is captured once at prepared and carried unchanged except
// the attempt.
type generationPrep struct {
	Phase         string      `json:"phase"`
	Cutoff        Id          `json:"cutoff"`
	System        *Id         `json:"system"`
	Model         ModelRef    `json:"model"`
	ThinkingLevel string      `json:"thinkingLevel"`
	Tools         []string    `json:"tools"`
	Retry         RetryPolicy `json:"retry"`
	Attempt       int         `json:"attempt"`
	UntilMs       *float64    `json:"untilMs,omitempty"`
	LastError     *string     `json:"lastError,omitempty"`
	Handle        JsonObject  `json:"handle,omitempty"`
	PollAt        *float64    `json:"pollAt,omitempty"`
}

func prepOf(checkpoint Checkpoint) generationPrep {
	var prep generationPrep
	_ = decodeInto(checkpoint, &prep)
	return prep
}

// base returns the prep without phase-specific fields.
func (prep generationPrep) base(phase string) generationPrep {
	prep.Phase = phase
	prep.UntilMs, prep.LastError, prep.Handle, prep.PollAt = nil, nil, nil, nil
	return prep
}

func (prep generationPrep) checkpoint() Checkpoint { return storedObject(prep) }

func generationInputs(task Task) []Id { return idList(asObject(task.Input)["inputs"]) }

var generationConfig = &KindConfig{
	Rewindable: []ConfigKey{
		{Key: "model", Optional: true},
		{Key: "thinkingLevel", Default: "off"},
		{Key: "selectedTools", Default: []any{}},
		{Key: "profile", Default: "default"},
	},
	Sticky: []ConfigKey{
		{Key: "retry", Default: JsonObject{"enabled": true, "maxRetries": 3, "baseDelayMs": 2000, "maxAgentDelayMs": 60_000}},
	},
}

// generationKind prepares, requests, and classifies one model turn.
var generationKind = &Kind{
	Name:     "pi.generation",
	Turn:     true,
	Config:   generationConfig,
	Inflight: []string{"requesting"},
	Initial:  generationInitial,
	Phases: map[string]PhaseHandler{
		"prepared":   generationPrepared,
		"requesting": generationRequesting,
		"retrying":   generationRetrying,
		"deferred":   generationDeferred,
	},
	Abort: generationAbort,
}

// generationInitial snapshots S on the line, runs systemInstructions off the
// line, then in one commit retakes S′, retries if it moved, else appends the
// managed entry and checkpoints prepared with the cutoff (§12.5).
func generationInitial(ctx context.Context, task Task, rt *Runtime) (Step, error) {
	taken, err := CommitAs(ctx, rt, func(_ context.Context, tx *Tx, current Task) (takenSnapshot, error) {
		return takeSnapshot(tx, current.ConversationId, rt.Registries.Sections, rt.Registries.Tools)
	})
	if err != nil {
		return Step{}, err
	}
	if taken.snapshot.Settings.Model == nil {
		return failStep(task, "no_model", "no model configured"), nil
	}
	sticky, err := rt.Sticky(ctx, task.ConversationId)
	if err != nil {
		return Step{}, err
	}
	var retry RetryPolicy
	_ = decodeInto(sticky["retry"], &retry)
	var warnings []string
	prepared, err := prepareDraft(ctx, rt, taken.canonical, taken.seed, taken.snapshot.Settings, func(message string) { warnings = append(warnings, message) })
	if err != nil {
		return Step{}, err
	}
	var model ModelRef
	_ = decodeInto(taken.snapshot.Settings.Model, &model)
	thinkingLevel, _ := taken.snapshot.Settings.ThinkingLevel.(string)
	return Step{Build: func(_ context.Context, tx *Tx, current Task) (Transition, error) {
		return commitPrepared(tx, current, rt, taken, prepared, warnings, generationPrep{Phase: "prepared", Model: model, ThinkingLevel: thinkingLevel, Retry: retry})
	}}, nil
}

func commitPrepared(tx *Tx, current Task, rt *Runtime, taken takenSnapshot, prepared preparedDraft, warnings []string, prep generationPrep) (Transition, error) {
	conversationId := current.ConversationId
	again, err := takeSnapshot(tx, conversationId, rt.Registries.Sections, rt.Registries.Tools)
	if err != nil {
		return Transition{}, err
	}
	if !sameSnapshot(again.snapshot, taken.snapshot) {
		return Transition{Retry: true}, nil
	}
	plan, err := planManagedEntry(tx, conversationId, taken.snapshot, taken.canonical, prepared.desired, prepared.tools, rt.Now())
	if err != nil {
		return Transition{}, err
	}
	for _, message := range warnings {
		if err := emit(tx, ViewEvent{"type": "warning", "source": "generation", "message": message}); err != nil {
			return Transition{}, err
		}
	}
	if plan != nil {
		system, err := tx.AppendEntry(conversationId, NewEntry{Kind: "pi.system", Data: plan.data, Model: plan.model, Edits: plan.edits})
		if err != nil {
			return Transition{}, err
		}
		prep.System = &system
		prep.Cutoff = system
	} else {
		newest, err := tx.NewestEntry(conversationId, NewestOptions{})
		if err != nil {
			return Transition{}, err
		}
		if newest == nil {
			return Transition{}, errors.New("generation has no entry to cut off at")
		}
		prep.Cutoff = newest.Id
	}
	if err := resetTurn(tx, conversationId); err != nil {
		return Transition{}, err
	}
	prep.Tools = []string{}
	for _, tool := range prepared.tools {
		prep.Tools = append(prep.Tools, tool.Name)
	}
	return Transition{Checkpoint: prep.checkpoint()}, nil
}

// generationPrepared derives the request from the cutoff (hooks rerun),
// checks overflow, writes the in-flight checkpoint, and calls the provider.
func generationPrepared(ctx context.Context, task Task, rt *Runtime) (Step, error) {
	prep := prepOf(task.Checkpoint)
	model := rt.Models.Resolve(prep.Model)
	if model == nil {
		return failStep(task, "no_model", fmt.Sprintf("model %s/%s unavailable", prep.Model.Provider, prep.Model.ModelId)), nil
	}
	messages, err := deriveRequest(ctx, task, prep, rt)
	if err != nil {
		return Step{}, err
	}
	if estimateTokens(messages, true) > model.Capabilities.ContextWindow-model.Capabilities.MaxOutputTokens {
		return overflowStep(task, rt), nil
	}
	prep.Attempt++
	requesting := prep.base("requesting")
	if _, err := rt.Commit(ctx, func(_ context.Context, tx *Tx, _ Task) (any, error) {
		if err := tx.Checkpoint(requesting.checkpoint()); err != nil {
			return nil, err
		}
		return nil, emit(tx, ViewEvent{"type": "generation.started", "taskId": float64(task.Id), "attempt": prep.Attempt})
	}); err != nil {
		return Step{}, err
	}
	outcome, err := streamGeneration(ctx, prep, model, messages, rt)
	if err != nil {
		return Step{}, err
	}
	if outcome.deferred != nil {
		pollAt := rt.Now() + pollAfter(outcome.deferred)
		deferred := prep.base("deferred")
		deferred.Handle, deferred.PollAt = storedObject(outcome.deferred), &pollAt
		return Step{Build: func(_ context.Context, tx *Tx, _ Task) (Transition, error) {
			if err := emit(tx, ViewEvent{"type": "generation.deferred", "taskId": float64(task.Id), "pollAt": pollAt}); err != nil {
				return Transition{}, err
			}
			return Transition{Checkpoint: deferred.checkpoint()}, nil
		}}, nil
	}
	return classify(ctx, task, prep, *outcome.terminal, rt)
}

func pollAfter(handle *ai.DeferredHandle) float64 {
	if handle.PollAfterMS != nil {
		return float64(*handle.PollAfterMS)
	}
	return 5000
}

// generationRequesting is reached only after a crash: the call may have
// happened, so it counts as a failed attempt and retries per policy.
func generationRequesting(_ context.Context, task Task, rt *Runtime) (Step, error) {
	prep := prepOf(task.Checkpoint)
	decision := retryDecision(prep.Retry, prep.Attempt, nil, rt.Now())
	if !decision.retry {
		return failStep(task, decision.reason, "interrupted"), nil
	}
	return Step{Build: func(_ context.Context, tx *Tx, current Task) (Transition, error) {
		if _, err := tx.AppendEntry(current.ConversationId, NewEntry{Kind: "pi.usage", Data: JsonObject{"attempt": prep.Attempt, "error": "interrupted"}}); err != nil {
			return Transition{}, err
		}
		return retryingTransition(tx, current, prep, decision.untilMs, "interrupted")
	}}, nil
}

func retryingTransition(tx *Tx, current Task, prep generationPrep, untilMs float64, lastError string) (Transition, error) {
	if err := emit(tx, ViewEvent{"type": "generation.retrying", "taskId": float64(current.Id), "attempt": prep.Attempt, "retryAt": untilMs, "error": lastError}); err != nil {
		return Transition{}, err
	}
	retrying := prep.base("retrying")
	retrying.UntilMs, retrying.LastError = &untilMs, &lastError
	return Transition{Checkpoint: retrying.checkpoint()}, nil
}

// generationRetrying waits out the durable backoff and returns to prepared,
// resetting the streaming view.
func generationRetrying(ctx context.Context, task Task, rt *Runtime) (Step, error) {
	prep := prepOf(task.Checkpoint)
	if err := rt.Sleep(ctx, *prep.UntilMs); err != nil {
		return Step{}, err
	}
	return Step{Build: func(_ context.Context, tx *Tx, current Task) (Transition, error) {
		turn, err := turnOf(tx, current.ConversationId)
		if err != nil {
			return Transition{}, err
		}
		delete(turn, "message")
		return Transition{Checkpoint: prep.base("prepared").checkpoint()}, nil
	}}, nil
}

// generationDeferred polls a provider-side deferred response.
func generationDeferred(ctx context.Context, task Task, rt *Runtime) (Step, error) {
	prep := prepOf(task.Checkpoint)
	model := rt.Models.Resolve(prep.Model)
	if model == nil {
		return failStep(task, "no_model", "model disappeared"), nil
	}
	if err := rt.Sleep(ctx, *prep.PollAt); err != nil {
		return Step{}, err
	}
	var handle ai.DeferredHandle
	if err := decodeInto(prep.Handle, &handle); err != nil {
		return Step{}, err
	}
	result, err := rt.Models.FetchDeferred(ctx, model, handle)
	if err != nil {
		return Step{}, err
	}
	if result.Deferred != nil {
		pollAt := rt.Now() + pollAfter(result.Deferred)
		next := prep
		next.Handle, next.PollAt = storedObject(result.Deferred), &pollAt
		return Step{Build: func(_ context.Context, tx *Tx, _ Task) (Transition, error) {
			if err := emit(tx, ViewEvent{"type": "generation.deferred", "taskId": float64(task.Id), "pollAt": pollAt}); err != nil {
				return Transition{}, err
			}
			return Transition{Checkpoint: next.checkpoint()}, nil
		}}, nil
	}
	if result.Message == nil {
		return Step{}, errors.New("deferred poll returned neither a message nor a handle")
	}
	message := *result.Message
	if _, err := rt.Commit(ctx, func(_ context.Context, tx *Tx, current Task) (any, error) {
		turn, err := turnOf(tx, current.ConversationId)
		if err != nil {
			return nil, err
		}
		turn["message"] = storedMessage(message)
		return nil, nil
	}); err != nil {
		return Step{}, err
	}
	return classify(ctx, task, prep.base("prepared"), message, rt)
}

func generationAbort(ctx context.Context, task Task, rt *Runtime) (AbortClosure, error) {
	if Phase(task.Checkpoint) == "deferred" {
		prep := prepOf(task.Checkpoint)
		if model := rt.Models.Resolve(prep.Model); model != nil {
			var handle ai.DeferredHandle
			if decodeInto(prep.Handle, &handle) == nil {
				_ = rt.Models.CancelDeferred(ctx, model, handle)
			}
		}
	}
	attempt := 0
	if task.Checkpoint != nil {
		attempt = prepOf(task.Checkpoint).Attempt
	}
	inputs := generationInputs(task)
	return func(_ context.Context, tx *Tx, current Task) (JsonValue, error) {
		turn, err := turnOf(tx, current.ConversationId)
		if err != nil {
			return nil, err
		}
		result := JsonObject{}
		partial, _ := turn["message"].(map[string]any)
		if partial != nil && len(arr(partial, "content")) > 0 {
			display := cloneObject(partial)
			display["stopReason"] = "aborted"
			assistant, err := tx.AppendEntry(current.ConversationId, NewEntry{Kind: "pi.assistant", Data: JsonObject{"attempt": attempt, "display": display, "reason": "aborted"}})
			if err != nil {
				return nil, err
			}
			result["assistant"] = float64(assistant)
		}
		if err := resetTurn(tx, current.ConversationId); err != nil {
			return nil, err
		}
		if err := tx.ResolveInputs(inputs, InputResolution{Status: InputUnanswered, Reason: "aborted"}); err != nil {
			return nil, err
		}
		return result, emit(tx, ViewEvent{"type": "turn.ended", "inputs": idsJSON(inputs), "status": "unanswered", "reason": "aborted"})
	}, nil
}

// failStep settles the turn's inputs as failed and admits queued triggers at
// the final boundary.
func failStep(task Task, reason, detail string) Step {
	return Step{Done: func(_ context.Context, tx *Tx, current Task) (Completion, error) {
		head, err := tx.NewestEntry(current.ConversationId, NewestOptions{WithHead: true})
		if err != nil {
			return Completion{}, err
		}
		if err := emit(tx, ViewEvent{"type": "generation.failed", "taskId": float64(current.Id), "reason": reason, "detail": detail}); err != nil {
			return Completion{}, err
		}
		if err := settleFailedTurn(tx, current, generationInputs(task), detail, entryIdOf(head)); err != nil {
			return Completion{}, err
		}
		return generationFailure(reason, detail, nil), nil
	}}
}

func generationFailure(reason, detail string, assistant *Id) Completion {
	failure := JsonObject{"reason": reason, "detail": detail}
	if assistant != nil {
		failure["assistant"] = float64(*assistant)
	}
	return Failed(failure)
}

func settleFailedTurn(tx *Tx, current Task, inputs []Id, detail string, headBoundary *Id) error {
	if err := resetTurn(tx, current.ConversationId); err != nil {
		return err
	}
	if err := tx.ResolveInputs(inputs, InputResolution{Status: InputUnanswered, Reason: "failed", Detail: detail}); err != nil {
		return err
	}
	if err := emit(tx, ViewEvent{"type": "turn.ended", "inputs": idsJSON(inputs), "status": "unanswered", "reason": "failed", "detail": detail}); err != nil {
		return err
	}
	boundary, err := tx.Boundary(current.ConversationId, "final", headBoundary)
	if err != nil || len(boundary.Triggers) == 0 {
		return err
	}
	conversationId := current.ConversationId
	if _, err := tx.CreateTaskSpec(TaskSpec{Kind: "pi.generation", ConversationId: &conversationId, Input: JsonObject{"inputs": idsJSON(boundary.Triggers)}}); err != nil {
		return err
	}
	return emit(tx, ViewEvent{"type": "turn.started", "inputs": idsJSON(boundary.Triggers)})
}

// deriveRequest projects the context at the cutoff and runs beforeRequest
// hooks in order.
func deriveRequest(ctx context.Context, task Task, prep generationPrep, rt *Runtime) ([]JsonObject, error) {
	cutoff := prep.Cutoff
	view, err := rt.Context(ctx, task.ConversationId, &cutoff)
	if err != nil {
		return nil, err
	}
	request := BeforeRequestInput{Messages: view.Messages}
	err = rt.Hooks.Each(ctx, func(handlers any, api HookApi) (any, error) {
		hooks := generationHooksOf(handlers)
		if hooks == nil || hooks.BeforeRequest == nil {
			return nil, nil
		}
		result, err := hooks.BeforeRequest(ctx, request, BeforeRequestInfo{HookApi: api, Cutoff: cutoff})
		if result == nil {
			return nil, err
		}
		return result, err
	}, func(value any) bool {
		request = *value.(*BeforeRequestInput)
		return false
	})
	return request.Messages, err
}

type streamOutcome struct {
	terminal *ai.AssistantMessage
	deferred *ai.DeferredHandle
}

type streamItem struct {
	event ai.AssistantMessageEvent
	err   error
}

// generationStream coalesces frames into the turn view, flushing on size or
// time.
type generationStream struct {
	rt             *Runtime
	encoder        *ai.AssistantMessageFrameEncoder
	pending        []ai.AssistantMessageFrame
	pendingBytes   int
	lastFlush      float64
	flushedContent bool
}

func (stream *generationStream) flush(ctx context.Context) error {
	batch := stream.pending
	if len(batch) == 0 {
		return nil
	}
	stream.pending, stream.pendingBytes, stream.lastFlush = nil, 0, stream.rt.Now()
	if slices.ContainsFunc(batch, frameHasDelta) {
		stream.flushedContent = true
	}
	_, err := stream.rt.Commit(ctx, func(_ context.Context, tx *Tx, current Task) (any, error) {
		turn, err := turnOf(tx, current.ConversationId)
		if err != nil {
			return nil, err
		}
		for _, frame := range batch {
			if err := applyFrame(turn, frame); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	return err
}

func frameHasDelta(frame ai.AssistantMessageFrame) bool {
	_, ok := frameDelta(frame)
	return ok
}

func frameDelta(frame ai.AssistantMessageFrame) (string, bool) {
	switch value := frame.(type) {
	case ai.TextDeltaFrame:
		return value.Delta, true
	case ai.ThinkingDeltaFrame:
		return value.Delta, true
	case ai.ToolCallDeltaFrame:
		return value.Delta, true
	default:
		return "", false
	}
}

// push encodes one event; it reports the terminal outcome when the stream
// ends.
func (stream *generationStream) push(ctx context.Context, event ai.AssistantMessageEvent) (*streamOutcome, error) {
	switch value := event.(type) {
	case ai.DoneEvent:
		_, _ = stream.encoder.Encode(event)
		if value.Reason == ai.StopReasonDeferred && value.Message != nil && value.Message.Deferred != nil {
			return &streamOutcome{deferred: value.Message.Deferred}, nil
		}
		return &streamOutcome{terminal: value.Message}, nil
	case ai.ErrorEvent:
		_, _ = stream.encoder.Encode(event)
		return &streamOutcome{terminal: value.Error}, nil
	}
	frame, err := stream.encoder.Encode(event)
	if err != nil || frame == nil {
		return nil, err
	}
	stream.pending = append(stream.pending, frame)
	delta, isDelta := frameDelta(frame)
	if isDelta {
		stream.pendingBytes += utf16Len(delta)
	} else {
		stream.pendingBytes += 64
	}
	if stream.pendingBytes >= 256 || (!stream.flushedContent && isDelta) {
		return nil, stream.flush(ctx)
	}
	return nil, nil
}

// streamGeneration runs the provider stream in an owned producer goroutine
// and races each pull against the flush timer.
func streamGeneration(ctx context.Context, prep generationPrep, model *ai.Model, messages []JsonObject, rt *Runtime) (streamOutcome, error) {
	stream := &generationStream{rt: rt, encoder: &ai.AssistantMessageFrameEncoder{}, lastFlush: rt.Now()}
	producerCtx, stop := context.WithCancel(ctx)
	items := make(chan streamItem)
	produced := make(chan struct{})
	go func() {
		defer close(produced)
		defer close(items)
		for event, err := range rt.Models.Stream(producerCtx, model, RequestOptions{Messages: messages, ThinkingLevel: prep.ThinkingLevel}) {
			select {
			case items <- streamItem{event: snapshotEvent(event), err: err}:
			case <-producerCtx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	outcome, err := stream.consume(ctx, items)
	stop()
	<-produced
	if err != nil {
		if ctx.Err() != nil {
			return streamOutcome{}, err
		}
		outcome = &streamOutcome{terminal: streamFailure(model, err, rt)}
	}
	if flushErr := stream.flush(ctx); flushErr != nil {
		return streamOutcome{}, flushErr
	}
	if outcome == nil {
		return streamOutcome{}, errors.New("stream ended without a terminal message")
	}
	return *outcome, nil
}

func (stream *generationStream) consume(ctx context.Context, items <-chan streamItem) (*streamOutcome, error) {
	for {
		item, open, flushed, err := stream.next(ctx, items)
		if err != nil {
			return nil, err
		}
		if flushed {
			continue
		}
		if !open {
			return nil, nil
		}
		if item.err != nil {
			return nil, item.err
		}
		outcome, err := stream.push(ctx, item.event)
		if err != nil || outcome != nil {
			return outcome, err
		}
	}
}

// next waits for the next event, or flushes pending frames when the flush
// deadline passes first.
func (stream *generationStream) next(ctx context.Context, items <-chan streamItem) (streamItem, bool, bool, error) {
	if len(stream.pending) == 0 {
		select {
		case item, open := <-items:
			return item, open, false, nil
		case <-ctx.Done():
			return streamItem{}, false, false, context.Cause(ctx)
		}
	}
	wait := time.Duration(max(0, stream.lastFlush+100-stream.rt.Now())) * time.Millisecond
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case item, open := <-items:
		return item, open, false, nil
	case <-timer.C:
		return streamItem{}, true, true, stream.flush(ctx)
	case <-ctx.Done():
		return streamItem{}, false, false, context.Cause(ctx)
	}
}

// snapshotEvent copies an event's live partial as it stands when yielded.
// The provider may keep advancing one shared accumulator after yielding; the
// consumer runs on another goroutine, so it encodes the yield-time snapshot,
// which is what a synchronous upstream consumer observes.
func snapshotEvent(event ai.AssistantMessageEvent) ai.AssistantMessageEvent {
	switch value := event.(type) {
	case ai.StartEvent:
		value.Partial = snapshotPartial(value.Partial)
		return value
	case ai.TextStartEvent:
		value.Partial = snapshotPartial(value.Partial)
		return value
	case ai.TextDeltaEvent:
		value.Partial = snapshotPartial(value.Partial)
		return value
	case ai.TextEndEvent:
		value.Partial = snapshotPartial(value.Partial)
		return value
	case ai.ThinkingStartEvent:
		value.Partial = snapshotPartial(value.Partial)
		return value
	case ai.ThinkingDeltaEvent:
		value.Partial = snapshotPartial(value.Partial)
		return value
	case ai.ThinkingEndEvent:
		value.Partial = snapshotPartial(value.Partial)
		return value
	case ai.ToolCallStartEvent:
		value.Partial = snapshotPartial(value.Partial)
		return value
	case ai.ToolCallDeltaEvent:
		value.Partial = snapshotPartial(value.Partial)
		return value
	case ai.ToolCallEndEvent:
		value.Partial = snapshotPartial(value.Partial)
		return value
	default:
		return event
	}
}

func snapshotPartial(partial *ai.AssistantMessage) *ai.AssistantMessage {
	if partial == nil {
		return nil
	}
	copied := *partial
	copied.Content = make([]ai.AssistantContentBlock, len(partial.Content))
	for index, block := range partial.Content {
		if call, ok := block.(ai.ToolCall); ok {
			if arguments, ok := cloneJSON(map[string]any(call.Arguments)).(map[string]any); ok {
				call.Arguments = arguments
			}
			block = call
		}
		copied.Content[index] = block
	}
	return &copied
}

func streamFailure(model *ai.Model, err error, rt *Runtime) *ai.AssistantMessage {
	return &ai.AssistantMessage{
		Content:      []ai.AssistantContentBlock{},
		API:          model.ProviderMeta.API,
		Provider:     model.ProviderMeta.ProviderID,
		Model:        model.ID,
		StopReason:   ai.StopReasonError,
		ErrorMessage: errorString(err),
		Timestamp:    int64(rt.Now()),
	}
}
