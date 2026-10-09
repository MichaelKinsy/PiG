package ai

// Ports packages/ai/src/api/lazy.ts
// Ports packages/ai/src/types.ts

import (
	"context"
	"fmt"
)

// LazyAPICapabilities declares optional methods without loading the implementation.
type LazyAPICapabilities struct {
	FetchDeferred  bool
	CancelDeferred bool
}

// LazyStream returns before setup runs. Setup starts in the first promise reaction after the call, as the continuation of an async function whose first statement awaits; use LazyStreamSync when Pi's setup has no await before its provider call. The producer owns cancellation and its final event/result; forwarding never replaces an established terminal value with an iterator cancellation.
func LazyStream(ctx context.Context, model *Model, setup func(context.Context) (*AssistantMessageEventStream, error)) *AssistantMessageEventStream {
	return startLazyStream(ctx, model, setup, func(body func()) { go body() })
}

// LazyStreamSync is LazyStream for a setup that never awaits before it returns its stream. Like an async function without an await, setup runs to completion in the caller's synchronous prefix, before LazyStreamSync returns, and only the forwarding reaction is queued (packages/ai/src/api/lazy.ts:48-50). A caller that already owns the shared continuation queue runs setup inline; any other caller first acquires the queue.
func LazyStreamSync(ctx context.Context, model *Model, setup func(context.Context) (*AssistantMessageEventStream, error)) *AssistantMessageEventStream {
	return startLazyStreamSync(ctx, model, setup, func(body func()) { go body() })
}

type lazySetupResult struct {
	stream *AssistantMessageEventStream
	err    error
}

// startLazyStream models lazyStream(model, async () => { await x; ... }): the reserved turn is the await continuation that runs setup, and the setup promise settles when it returns.
func startLazyStream(ctx context.Context, model *Model, setup func(context.Context) (*AssistantMessageEventStream, error), launch func(func())) *AssistantMessageEventStream {
	ctx, executor := withContinuationExecutor(ctx)
	outer := NewAssistantMessageEventStream()
	outer.executor = executor
	turn := executor.newTurn()
	launch(func() {
		turn.run(func(turn *continuationTurn) {
			inner, err := setup(context.WithValue(ctx, continuationTurnKey{}, turn))
			settled := newContinuationPromise[lazySetupResult](executor)
			settled.resolve(lazySetupResult{stream: inner, err: err})
			prepared := awaitContinuation(turn, settled)
			forwardLazySetup(ctx, turn, executor, outer, model, prepared.stream, prepared.err)
		})
	})
	return outer
}

// startLazyStreamSync models lazyStream(model, async () => { ...no await... }): setup completes in the caller's tick, and `setup().then(forwardStream)` registers its reaction on an already fulfilled promise before lazyStream returns.
func startLazyStreamSync(ctx context.Context, model *Model, setup func(context.Context) (*AssistantMessageEventStream, error), launch func(func())) *AssistantMessageEventStream {
	ctx, executor := withContinuationExecutor(ctx)
	outer := NewAssistantMessageEventStream()
	outer.executor = executor
	var prepared lazySetupResult
	var forward *continuationTurn
	// The forwarding reaction is registered while the setup turn still runs, so it queues behind every reaction setup already queued and ahead of any that a released producer queues later. A caller that does not own the queue must not register it after its acquired turn releases: the release drains a buffered producer, and the goroutine's later registration would land behind that whole response.
	run := func(ctx context.Context) {
		prepared.stream, prepared.err = setup(ctx)
		settled := newContinuationPromise[lazySetupResult](executor)
		settled.resolve(prepared)
		forward = executor.newDeferredTurn()
		settled.onResolved(func(lazySetupResult) { forward.grant() })
	}
	if owned := ownedContinuationTurn(ctx, executor); owned != nil {
		run(context.WithValue(ctx, continuationTurnKey{}, owned))
	} else {
		executor.run(func(turn *continuationTurn) { run(context.WithValue(ctx, continuationTurnKey{}, turn)) })
	}
	launch(func() {
		forward.run(func(turn *continuationTurn) {
			forwardLazySetup(ctx, turn, executor, outer, model, prepared.stream, prepared.err)
		})
	})
	return outer
}

// ownedContinuationTurn returns the turn currently executing the caller, or nil when the caller does not own the queue. A callback owns it when its context carries the running turn or the running observation.
func ownedContinuationTurn(ctx context.Context, executor *continuationExecutor) *continuationTurn {
	executor.mu.Lock()
	active := executor.active
	executor.mu.Unlock()
	if active == nil {
		return nil
	}
	if turn, _ := ctx.Value(continuationTurnKey{}).(*continuationTurn); turn == active {
		return active
	}
	if observation := StreamObservationFromContext(ctx); observation != nil && observation.turn == active {
		return active
	}
	return nil
}

// forwardLazySetup is lazy.ts:48-60. A rejected setup reaches the catch handler two reactions after it settles: the .then passthrough, then the handler.
func forwardLazySetup(ctx context.Context, turn *continuationTurn, executor *continuationExecutor, outer *AssistantMessageEventStream, model *Model, inner *AssistantMessageEventStream, err error) {
	if err == nil && inner == nil {
		err = fmt.Errorf("API returned no event stream")
	}
	if err == nil {
		observation := context.WithValue(context.WithoutCancel(ctx), continuationTurnKey{}, turn)
		for event := range inner.events(observation, false) {
			if err = outer.Push(event); err != nil {
				break
			}
		}
		if err == nil {
			outer.End(awaitContinuation(turn, inner.resultContinuation(executor)))
			return
		}
	} else {
		passthrough := newContinuationPromise[struct{}](executor)
		passthrough.resolve(struct{}{})
		awaitContinuation(turn, passthrough)
	}
	message := &AssistantMessage{Content: []AssistantContentBlock{}, StopReason: StopReasonError, ErrorMessage: err.Error(), Timestamp: outer.startedAt}
	if model != nil {
		message.API = model.ProviderMeta.API
		message.Provider = modelProviderID(model)
		message.Model = model.ID
	}
	_ = outer.Push(ErrorEvent{Reason: StopReasonError, Error: message})
}
func (stream *AssistantMessageEventStream) isTerminated() bool {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.terminal
}
func modelProviderID(model *Model) string {
	if model.ProviderMeta.ProviderID != "" {
		return model.ProviderMeta.ProviderID
	}
	if model.Provider != nil {
		return model.Provider.ID()
	}
	return ""
}

// LazyAPI invokes the loader per operation, as Pi does. Module caching belongs to the loader; unsupported deferred callbacks stay nil.
func LazyAPI(load func(context.Context) (*ProviderStreams, error), capabilities LazyAPICapabilities) *ProviderStreams {
	wrap := func(simple bool) ModelsStreamFunction {
		return func(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
			return LazyStream(ctx, model, func(ctx context.Context) (*AssistantMessageEventStream, error) {
				api, err := load(ctx)
				if err != nil {
					return nil, err
				}
				if simple {
					return api.StreamSimple(ctx, model, transcript, options)
				}
				return api.Stream(ctx, model, transcript, options)
			}), nil
		}
	}
	api := &ProviderStreams{Stream: wrap(false), StreamSimple: wrap(true)}
	if capabilities.FetchDeferred {
		api.FetchDeferred = func(ctx context.Context, model *Model, handle DeferredHandle, options DeferredFetchOptions) (*AssistantMessageEventStream, error) {
			return LazyStream(ctx, model, func(ctx context.Context) (*AssistantMessageEventStream, error) {
				loaded, err := load(ctx)
				if err != nil {
					return nil, err
				}
				if loaded.FetchDeferred == nil {
					return nil, fmt.Errorf("API does not support deferred responses")
				}
				return loaded.FetchDeferred(ctx, model, handle, options)
			}), nil
		}
	}
	if capabilities.CancelDeferred {
		api.CancelDeferred = func(ctx context.Context, model *Model, handle DeferredHandle, options DeferredCancelOptions) error {
			loaded, err := load(ctx)
			if err != nil {
				return err
			}
			if loaded.CancelDeferred == nil {
				return fmt.Errorf("API cannot cancel deferred responses")
			}
			return loaded.CancelDeferred(ctx, model, handle, options)
		}
	}
	return api
}
