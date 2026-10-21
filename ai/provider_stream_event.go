package ai

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Ports the onProviderStreamEvent call sites of packages/ai/src/api/*.ts.

// providerEventModel is the model an OnProviderStreamEvent observer receives: the selected model when the provider was configured from one, otherwise the identity the provider knows.
func providerEventModel(selected *Model, api API, providerID, id string) *Model {
	if selected != nil {
		return selected
	}
	return &Model{ID: id, ProviderMeta: ProviderMetadata{ProviderID: providerID, API: api}}
}

// outputAPI is the api of the assistant message a stream builds: the selected model's api, as every api module's createOutput and the lazy
// setup error copy `api: model.api` (mistral-conversations.ts:221, openai-completions.ts:316, anthropic-messages.ts:585, lazy.ts:8). The
// module's own api applies when no model was selected or the model names none.
func outputAPI(selected *Model, module API) API {
	if selected != nil && selected.ProviderMeta.API != "" {
		return selected.ProviderMeta.API
	}
	return module
}

// setProviderEventObserver binds opts.OnProviderStreamEvent to the request. Without the option, observeProviderEvent does nothing.
func (builder *assistantStreamBuilder) setProviderEventObserver(opts StreamOptions, model *Model) {
	if opts.OnProviderStreamEvent == nil {
		return
	}
	ctx := builder.ctx
	builder.providerEventOption = opts.OnProviderStreamEvent
	builder.providerEventModel = model
	builder.providerEventWanted = opts.ProviderStreamEventWanted
	builder.providerEvent = func(data any) error { return opts.OnProviderStreamEvent(ctx, data, model) }
}

// observeProviderEvent hands the parsed JSON form of one wire event to the observer before the adapter normalizes it. The observer's error ends
// the request. Upstream awaits the callback at every call site, so a successful call is one microtask hop even when no observer is set.
func (builder *assistantStreamBuilder) observeProviderEvent(raw []byte) error {
	if builder.providerEvent == nil {
		builder.awaitProviderEvent()
		return nil
	}
	// An observer with nothing to do would return nil, so a wire event that is certain to parse is not parsed for it.
	if builder.providerEventWanted != nil && jsonParsesAsValue(raw) && !builder.awaitProviderEventUnlessWanted() {
		return nil
	}
	builder.settleDeferred()
	return builder.observeProviderEventJSON(raw)
}

// observeValidProviderEvent is observeProviderEvent for the text of a wire event that the adapter's decoder already accepted with json.Valid, so deciding whether an unwanted observer may skip it does not validate it again.
func (builder *assistantStreamBuilder) observeValidProviderEvent(text string) error {
	if builder.providerEvent == nil {
		builder.awaitProviderEvent()
		return nil
	}
	if builder.providerEventWanted != nil && validJSONParsesAsValue(text) && !builder.awaitProviderEventUnlessWanted() {
		return nil
	}
	builder.settleDeferred()
	return builder.observeProviderEventJSON([]byte(text))
}

// observeDecodedProviderEvent is observeValidProviderEvent for a wire event whose decode already answered validJSONParsesAsValue of its text as parsesAsValue, so an unwanted observer skips it without a scan.
func (builder *assistantStreamBuilder) observeDecodedProviderEvent(text string, parsesAsValue bool) error {
	if builder.providerEvent == nil {
		builder.awaitProviderEvent()
		return nil
	}
	if builder.providerEventWanted != nil && parsesAsValue && !builder.awaitProviderEventUnlessWanted() {
		return nil
	}
	builder.settleDeferred()
	return builder.observeProviderEventJSON([]byte(text))
}

func (builder *assistantStreamBuilder) observeProviderEventJSON(raw []byte) error {
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return err
	}
	return builder.observeProviderEventData(data)
}

// jsonParsesAsValue reports that json.Unmarshal of raw into an any succeeds. A false answer only means it is not certain: raw is not valid JSON, or it holds a number with an exponent or of more than 300 characters, which may overflow a float64.
func jsonParsesAsValue(raw []byte) bool {
	return json.Valid(raw) && validJSONParsesAsValue(raw)
}

// recordScanHook, when a test sets it, is told of each pass over a wire record's bytes that is not the decode that reads the record: json.Valid in the stream decoder (recordScanValid) and validJSONParsesAsValue for an event observer (recordScanObserver), with the record's length. It is nil in production, so a pass costs one nil check beside the scan of the record's bytes.
var recordScanHook func(pass recordScanPass, bytes int)

type recordScanPass uint8

const (
	recordScanValid recordScanPass = iota
	recordScanObserver
)

// jsonValidRecord is json.Valid of a wire record the stream decoder checks.
func jsonValidRecord(data []byte) bool {
	if recordScanHook != nil {
		recordScanHook(recordScanValid, len(data))
	}
	return json.Valid(data)
}

// validJSONParsesAsValue is jsonParsesAsValue for raw that json.Valid accepts: false when it holds a number with an exponent or of more than 300 characters.
func validJSONParsesAsValue[T string | []byte](raw T) bool {
	if recordScanHook != nil {
		recordScanHook(recordScanObserver, len(raw))
	}
	inString := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inString {
			switch c {
			case '\\':
				i++
			case '"':
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
		case c == '-' || c >= '0' && c <= '9':
			start := i
			for i+1 < len(raw) && strings.IndexByte("0123456789.eE+-", raw[i+1]) >= 0 {
				i++
				if raw[i] == 'e' || raw[i] == 'E' {
					return false
				}
			}
			if i+1-start > 300 {
				return false
			}
		}
	}
	return true
}

// observeProviderEventData is observeProviderEvent for an adapter whose parsed event is not JSON, such as an SDK union member.
func (builder *assistantStreamBuilder) observeProviderEventData(data any) error {
	if builder.providerEvent != nil {
		builder.settleDeferred()
		if err := builder.providerEvent(data); err != nil {
			return err
		}
	}
	builder.awaitProviderEvent()
	return nil
}

// awaitProviderEvent is the microtask hop of `await options?.onProviderStreamEvent?.(...)` when no callback runs. The awaits the response turn deferred are made first, in the same row.
func (builder *assistantStreamBuilder) awaitProviderEvent() {
	if builder.turn != nil {
		suspendContinuation(builder.turn)
	}
}

// awaitProviderEventUnlessWanted asks the observer whether it wants the wire event where the observer would be called, after the awaits the response turn deferred, and unless it does makes the observer's hop in the same row. It reports the answer; the caller observes a wanted event.
func (builder *assistantStreamBuilder) awaitProviderEventUnlessWanted() bool {
	if builder.turn == nil {
		return builder.providerEventWanted()
	}
	return builder.turn.awaitResolvedUnless(builder.providerEventWanted)
}

// settleDeferred makes the awaits the response turn deferred (deferContinuation) before an observer runs.
func (builder *assistantStreamBuilder) settleDeferred() {
	if builder.turn != nil {
		builder.turn.settleDeferred()
	}
}

// observeCodexFrame observes one raw Codex frame before mapCodexEvents maps it. parseWebSocket (openai-codex-responses.ts:1331-1345) never queues a frame that JSON.parse rejects or a null value, whose `type` read throws, so neither reaches the observer; the mapper reports them.
func (builder *assistantStreamBuilder) observeCodexFrame(frame []byte) error {
	if builder.providerEvent == nil || !json.Valid(frame) || string(bytes.TrimSpace(frame)) == "null" {
		return nil
	}
	return builder.observeProviderEvent(frame)
}
