package ai

// Ports packages/ai/src/providers/faux.ts createFauxCore.

import (
	"context"
	"slices"
)

// FauxCore is the object faux.ts createFauxCore returns (faux.ts:454-702): the faux provider's api and provider names, its models, the functions that serve them
// (stream, streamSimple, fetchDeferred, cancelDeferred, getModel), the call counters, and the response queue. fauxProvider and registerFauxProvider are built on it;
// here they are [NewFauxProvider] and [RegisterFauxProvider], whose handle shares this core's queue and state. Like upstream's object, every function is a field a
// caller keeps and calls on its own.
type FauxCore struct {
	// API is `api` (faux.ts:455): the requested API name, else "faux:<time>:<random>".
	API string
	// Provider is `provider` (faux.ts:456): the provider id, "faux" unless one was requested.
	Provider string
	// Models is `models` (faux.ts:477-492): the faux models, in definition order, with the core's api and provider.
	Models []*Model
	// Stream is `stream` (faux.ts:508-571): it shifts the next queued response and streams it for the model, or ends the stream with an error message when none is queued.
	Stream ModelsStreamFunction
	// StreamSimple is `streamSimple` (faux.ts:573-574): stream, which takes the same options.
	StreamSimple ModelsStreamFunction
	// FetchDeferred is `fetchDeferred` (faux.ts:576-638): it answers a fetch of a deferred submission, the original handle while fetches remain pending, then the scripted response.
	FetchDeferred func(ctx context.Context, model *Model, handle DeferredHandle, options DeferredFetchOptions) (*AssistantMessageEventStream, error)
	// CancelDeferred is `cancelDeferred` (faux.ts:640-650): it records the handle as cancelled, so a later fetch of it fails.
	CancelDeferred func(ctx context.Context, model *Model, handle DeferredHandle, options DeferredCancelOptions) error
	// GetModel is `getModel` (faux.ts:652-659): the first model, or the model with the id, nil for an unknown id.
	GetModel func(id ...string) *Model
	// State is `state` (faux.ts:665): the call, deferred fetch and cancelled deferred counters.
	State *FauxProviderState
	// SetResponses is `setResponses` (faux.ts:667-669): the queue becomes a copy of responses.
	SetResponses func(responses []FauxResponseStep)
	// AppendResponses is `appendResponses` (faux.ts:670-672): responses join the end of the queue.
	AppendResponses func(responses []FauxResponseStep)
	// GetPendingResponseCount is `getPendingResponseCount` (faux.ts:673-675): the number of queued responses.
	GetPendingResponseCount func() int

	handle *FauxProviderHandle
}

// CreateFauxCore is faux.ts createFauxCore(options): it builds the faux provider's core. A nil option field takes upstream's default.
func CreateFauxCore(options RegisterFauxProviderOptions) *FauxCore {
	handle := NewFauxProvider(options)
	return &FauxCore{
		API:                     string(handle.cfg.API),
		Provider:                handle.cfg.ProviderID,
		Models:                  slices.Clone(handle.models),
		Stream:                  handle.streamModel,
		StreamSimple:            handle.streamModel,
		FetchDeferred:           handle.fetchDeferred,
		CancelDeferred:          handle.cancelDeferred,
		GetModel:                handle.GetModel,
		State:                   handle.State(),
		SetResponses:            handle.SetResponses,
		AppendResponses:         handle.AppendResponses,
		GetPendingResponseCount: handle.PendingResponseCount,
		handle:                  handle,
	}
}
