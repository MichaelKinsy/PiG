package ai

// Ports packages/ai/src/providers/faux.ts

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"time"
)

type FauxDeferredConfig struct {
	PendingFetches float64
	PollAfterMS    *int64
}
type fauxDeferredResponse struct {
	handle         DeferredHandle
	step           FauxResponseStep
	context        TranscriptContext
	options        StreamOptions
	model          *Model
	pendingFetches float64
	cancelled      bool
	final          *FauxResponse
}

// Provider exposes the model-aware provider definition for an explicit Models collection.
func (p *fauxProvider) Provider() *ModelsProvider { return p.definition }
func (p *fauxProvider) DeferredFetchCount() int   { return int(p.state.DeferredFetchCount.Load()) }
func (p *fauxProvider) CancelledDeferred() []DeferredHandle {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]DeferredHandle, len(p.cancelledDeferred))
	for i, handle := range p.cancelledDeferred {
		result[i] = *cloneDeferredHandle(&handle)
	}
	return result
}
func (p *fauxProvider) makeDefinition() *ModelsProvider {
	stream := func(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
		return p.streamModel(ctx, model, transcript, options)
	}
	return CreateProvider(CreateProviderOptions{ID: p.ID(), Auth: ProviderAuth{APIKey: &APIKeyAuth{Name: "Faux", Resolve: func(context.Context, APIKeyAuthInput) (*AuthResult, error) { return &AuthResult{}, nil }}}, Models: p.models, API: &ProviderStreams{Stream: stream, StreamSimple: stream, FetchDeferred: p.fetchDeferred, CancelDeferred: p.cancelDeferred}})
}

func (p *fauxProvider) submitDeferred(model *Model, request TranscriptContext, options StreamOptions, step FauxResponseStep) DeferredHandle {
	handle := DeferredHandle{Provider: modelProviderID(model), ModelID: model.ID, API: model.ProviderMeta.API, ID: fmt.Sprintf("deferred:%d:%d", time.Now().UnixNano(), rand.Uint64())}
	pending := 0.0
	if p.cfg.Deferred != nil {
		pending = math.Max(0, math.Floor(p.cfg.Deferred.PendingFetches))
		if p.cfg.Deferred.PollAfterMS != nil {
			handle.PollAfterMS = new(*p.cfg.Deferred.PollAfterMS)
		}
	}
	p.mu.Lock()
	p.deferredResponses[handle.ID] = &fauxDeferredResponse{handle: handle, step: step, context: request, options: options, model: model, pendingFetches: pending}
	p.mu.Unlock()
	return handle
}

// fetchDeferred ports faux.ts:fetchDeferred. As in stream, the response body is one microtask-started async function; its awaits are turn suspensions in source order.
func (p *fauxProvider) fetchDeferred(ctx context.Context, model *Model, handle DeferredHandle, options DeferredFetchOptions) (*AssistantMessageEventStream, error) {
	p.state.DeferredFetchCount.Add(1)
	options.Deferred = nil
	builder := newObservedProviderBuilder(ctx, p.cfg.API, p.cfg.ProviderID, model.ID)
	builder.partialCopy = (*AssistantMessage).ShallowCopy
	builder.afterPush = p.afterPush
	turn := builder.stream.executor.newTurn()
	go turn.run(func(turn *continuationTurn) {
		defer builder.produceUnder(turn)()
		var hookErr error
		if options.OnResponse != nil {
			hookErr = options.OnResponse(ctx, ProviderResponse{Status: 200, Headers: map[string]string{}}, model)
		}
		suspendContinuation(turn)
		if hookErr != nil {
			builder.fail(StopReasonError, hookErr)
			return
		}
		p.mu.Lock()
		entry := p.deferredResponses[handle.ID]
		if entry == nil || entry.handle.Provider != handle.Provider || entry.handle.ModelID != handle.ModelID || entry.handle.API != handle.API {
			p.mu.Unlock()
			builder.fail(StopReasonError, fmt.Errorf("Unknown faux deferred response: %s", handle.ID))
			return
		}
		if entry.cancelled {
			p.mu.Unlock()
			builder.fail(StopReasonError, fmt.Errorf("Faux deferred response was cancelled: %s", handle.ID))
			return
		}
		if entry.pendingFetches > 0 {
			entry.pendingFetches--
			pending := FauxResponse{Content: []FauxContentBlock{}, StopReason: string(StopReasonDeferred), Deferred: cloneDeferredHandle(&entry.handle), usage: &Usage{}}
			p.mu.Unlock()
			p.streamWithDeltas(turn, ctx, builder, pending)
			return
		}
		final := entry.final
		submissionOptions := entry.options
		submissionOptions.Deferred = nil
		submissionOptions.OnResponse = nil
		submissionOptions.Signal = nil
		p.mu.Unlock()
		if final == nil {
			// The await sits in a try/catch that stores an error message as the final response.
			response, err := p.resolveResponse(turn, entry.step, entry.context, submissionOptions, entry.model)
			if err != nil {
				response = FauxResponse{Content: []FauxContentBlock{}, StopReason: string(StopReasonError), ErrorMessage: err.Error(), usage: &Usage{}}
			}
			p.mu.Lock()
			entry.final = &response
			final = entry.final
			p.mu.Unlock()
		}
		// The resolved message carries the submitting model (cloneMessage(resolved, api, provider, entry.model.id)).
		builder.partial.Model = entry.model.ID
		p.streamWithDeltas(turn, ctx, builder, *final)
	})
	return builder.stream, nil
}

func (p *fauxProvider) cancelDeferred(ctx context.Context, model *Model, handle DeferredHandle, options DeferredCancelOptions) error {
	p.mu.Lock()
	p.cancelledDeferred = append(p.cancelledDeferred, *cloneDeferredHandle(&handle))
	if entry := p.deferredResponses[handle.ID]; entry != nil {
		entry.cancelled = true
	}
	p.mu.Unlock()
	if options.OnResponse != nil {
		return options.OnResponse(ctx, ProviderResponse{Status: 200, Headers: map[string]string{}}, model)
	}
	return nil
}
