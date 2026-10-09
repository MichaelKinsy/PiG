package ai

import (
	"encoding/json"
)

// Ports the onProviderStreamEvent call sites of packages/ai/src/api/*.ts.

// providerEventModel is the model an OnProviderStreamEvent observer receives: the selected model when the provider was configured from one, otherwise the identity the provider knows.
func providerEventModel(selected *Model, api API, providerID, id string) *Model {
	if selected != nil {
		return selected
	}
	return &Model{ID: id, ProviderMeta: ProviderMetadata{ProviderID: providerID, API: api}}
}

// setProviderEventObserver binds opts.OnProviderStreamEvent to the request. Without the option, observeProviderEvent does nothing.
func (builder *assistantStreamBuilder) setProviderEventObserver(opts StreamOptions, model *Model) {
	if opts.OnProviderStreamEvent == nil {
		return
	}
	ctx := builder.ctx
	builder.providerEventOption = opts.OnProviderStreamEvent
	builder.providerEventModel = model
	builder.providerEvent = func(data any) error { return opts.OnProviderStreamEvent(ctx, data, model) }
}

// observeProviderEvent hands the parsed JSON form of one wire event to the observer before the adapter normalizes it. The observer's error ends
// the request. Upstream awaits the callback at every call site, so a successful call is one microtask hop even when no observer is set.
func (builder *assistantStreamBuilder) observeProviderEvent(raw []byte) error {
	if builder.providerEvent == nil {
		builder.awaitProviderEvent()
		return nil
	}
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return err
	}
	return builder.observeProviderEventData(data)
}

// observeProviderEventData is observeProviderEvent for an adapter whose parsed event is not JSON, such as an SDK union member.
func (builder *assistantStreamBuilder) observeProviderEventData(data any) error {
	if builder.providerEvent != nil {
		if err := builder.providerEvent(data); err != nil {
			return err
		}
	}
	builder.awaitProviderEvent()
	return nil
}

// awaitProviderEvent is the microtask hop of `await options?.onProviderStreamEvent?.(...)` when no callback runs.
func (builder *assistantStreamBuilder) awaitProviderEvent() {
	if builder.turn != nil {
		suspendContinuation(builder.turn)
	}
}

// observeCodexFrame observes one raw Codex frame before mapCodexEvents maps it. Frames that are not JSON are left to the mapper, which reports them.
func (builder *assistantStreamBuilder) observeCodexFrame(frame []byte) error {
	if builder.providerEvent == nil || !json.Valid(frame) {
		return nil
	}
	return builder.observeProviderEvent(frame)
}
