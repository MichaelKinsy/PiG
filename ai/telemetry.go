package ai

// Ports packages/ai/src/telemetry.ts.
//
// Pi's typed-schema helpers are type-level: SchemaTelemetrySpan and the span-name literal types have no Go form, so the
// request span is the plain telemetry.TelemetrySpan and the typed starter and typed metrics are the runtime helpers of
// package telemetry.
//
// Async contract of observeModelRequest (telemetry.ts:84-86, `void start(...)`): the span's callback is a Promise that
// settles when the observer's result is observed, and nobody awaits it. Go starts the callback on a launched goroutine
// that blocks until onResult runs (or the adapter's StartSpan returns), and the observer waits only until the callback
// has run so that its span is set before the first event, as Pi's synchronous adapters guarantee. Ordering: events and
// the result are observed on the forwarding turn, after the outer stream's push/end; the span ends after the result
// is observed, on the launched goroutine. Errors: a failed StartSpan leaves the no-op span in place and records
// nothing. Cancellation: the observer has no cancellation of its own; it ends with the stream's terminal result, which
// the forwarding always reaches.

import (
	"reflect"
	"runtime"
	"sync"
	"weak"

	"github.com/MichaelKinsy/PiG/internal/jsclock"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/telemetry"
)

var (
	aiProvider = telemetry.TelemetryStartAttributeDefinition{
		TelemetryAttributeDefinition: telemetry.TelemetryAttributeDefinition{
			TelemetryAttributeMetadata: telemetry.TelemetryAttributeMetadata{Description: "Provider identifier."},
			Type:                       telemetry.TelemetryAttributeTypeString,
		},
		Required: true,
	}
	aiModel = telemetry.TelemetryStartAttributeDefinition{
		TelemetryAttributeDefinition: telemetry.TelemetryAttributeDefinition{
			TelemetryAttributeMetadata: telemetry.TelemetryAttributeMetadata{Description: "Requested model identifier."},
			Type:                       telemetry.TelemetryAttributeTypeString,
		},
		Required: true,
	}
	aiStop = telemetry.TelemetryAttributeDefinition{
		TelemetryAttributeMetadata: telemetry.TelemetryAttributeMetadata{Description: "Normalized stop reason."},
		Type:                       telemetry.TelemetryAttributeTypeString,
		Values:                     []string{"pending", "stop", "length", "toolUse", "error", "aborted", "deferred"},
	}
)

func aiAttribute(valueType telemetry.TelemetryAttributeType, description string, required bool) telemetry.TelemetryStartAttributeDefinition {
	return telemetry.TelemetryStartAttributeDefinition{
		TelemetryAttributeDefinition: telemetry.TelemetryAttributeDefinition{
			TelemetryAttributeMetadata: telemetry.TelemetryAttributeMetadata{Description: description},
			Type:                       valueType,
		},
		Required: required,
	}
}

func aiEndAttribute(description string) telemetry.TelemetryAttributeDefinition {
	return telemetry.TelemetryAttributeDefinition{
		TelemetryAttributeMetadata: telemetry.TelemetryAttributeMetadata{Description: description},
		Type:                       telemetry.TelemetryAttributeTypeNumber,
	}
}

func aiRequestSpan(description string) telemetry.TelemetrySpanDefinition {
	blockType := aiAttribute(telemetry.TelemetryAttributeTypeString, "Block kind.", true)
	blockType.Values = []string{"text", "thinking", "tool_call"}
	responseID := telemetry.TelemetryAttributeDefinition{
		TelemetryAttributeMetadata: telemetry.TelemetryAttributeMetadata{Description: "Provider response identifier.", Cardinality: telemetry.TelemetryAttributeCardinalityHigh},
		Type:                       telemetry.TelemetryAttributeTypeString,
	}
	return telemetry.TelemetrySpanDefinition{
		Description: description,
		Parents:     telemetry.TelemetryParentDefinition{Kind: telemetry.TelemetryParentKindAny},
		StartAttributes: map[string]telemetry.TelemetryStartAttributeDefinition{
			"provider":  aiProvider,
			"api":       aiAttribute(telemetry.TelemetryAttributeTypeString, "API identifier.", true),
			"model":     aiModel,
			"reasoning": aiAttribute(telemetry.TelemetryAttributeTypeString, "Scalar requested reasoning level.", false),
		},
		EndAttributes: map[string]telemetry.TelemetryAttributeDefinition{
			"stop":              aiStop,
			"usage.input":       aiEndAttribute("Uncached input tokens."),
			"usage.output":      aiEndAttribute("Output tokens."),
			"usage.cache_read":  aiEndAttribute("Cache-read tokens."),
			"usage.cache_write": aiEndAttribute("Cache-write tokens."),
			"cost.total":        aiEndAttribute("Total request cost in USD."),
			"response.id":       responseID,
		},
		Events: map[string]telemetry.TelemetryEventDefinition{
			"pi.ai.first_chunk": {Description: "First normalized stream event delivered.", Attributes: map[string]telemetry.TelemetryEventAttributeDefinition{}},
			"pi.ai.block": {
				Description: "A completed content block; content is never recorded.",
				Attributes: map[string]telemetry.TelemetryEventAttributeDefinition{
					"type":  blockType,
					"index": aiAttribute(telemetry.TelemetryAttributeTypeNumber, "Content block index.", true),
					"chars": aiAttribute(telemetry.TelemetryAttributeTypeNumber, "UTF-16 character count.", true),
					"tool":  aiAttribute(telemetry.TelemetryAttributeTypeString, "Tool name, for tool-call blocks only.", false),
				},
			},
		},
		Status: telemetry.TelemetrySpanStatusDefinition{
			Default:   telemetry.SpanStatusCodeOK,
			ErrorWhen: "Stop reason is error: provider_error with at most 500 error-message characters. Aborted stays ok.",
		},
	}
}

func aiMetricStop() telemetry.TelemetryMetricAttributeDefinition {
	stop := aiAttribute(telemetry.TelemetryAttributeTypeString, aiStop.Description, true)
	stop.Values = aiStop.Values
	return stop
}

// AITelemetrySchema is Pi's AI_TELEMETRY_SCHEMA: Pi-owned request metadata, bounded error status, events, and
// low-cardinality metrics. The schema is built on first use, not at package init, so a process that never reads it pays nothing;
// every call returns the same schema.
var AITelemetrySchema = sync.OnceValue(func() *telemetry.TelemetrySchemaDefinition {
	return telemetry.DefineTelemetrySchema(&telemetry.TelemetrySchemaDefinition{
		Version: 1,
		Spans: map[string]telemetry.TelemetrySpanDefinition{
			"pi.ai.request": aiRequestSpan("One logical model request, including authentication and provider setup."),
			"pi.ai.poll":    aiRequestSpan("One deferred-response poll, not the deferred request's lifetime."),
		},
		Metrics: map[string]telemetry.TelemetryMetricDefinition{
			"pi.ai.request.duration": {
				Kind:        telemetry.TelemetryMetricKindHistogram,
				Description: "Logical request or poll duration.",
				Unit:        "ms",
				Attributes:  map[string]telemetry.TelemetryMetricAttributeDefinition{"provider": aiProvider, "model": aiModel, "stop": aiMetricStop()},
			},
			"pi.ai.first_chunk.duration": {
				Kind:        telemetry.TelemetryMetricKindHistogram,
				Description: "Time to the first normalized stream event.",
				Unit:        "ms",
				Attributes:  map[string]telemetry.TelemetryMetricAttributeDefinition{"provider": aiProvider, "model": aiModel},
			},
			"pi.ai.tokens": {
				Kind:        telemetry.TelemetryMetricKindCounter,
				Description: "Reported model token usage; pending and deferred polls are excluded.",
				Unit:        "token",
				Attributes: map[string]telemetry.TelemetryMetricAttributeDefinition{
					"provider": aiProvider,
					"model":    aiModel,
					"kind": func() telemetry.TelemetryMetricAttributeDefinition {
						kind := aiAttribute(telemetry.TelemetryAttributeTypeString, "Token usage category.", true)
						kind.Values = []string{"input", "output", "cache_read", "cache_write"}
						return kind
					}(),
				},
			},
			"pi.ai.cost": {
				Kind:        telemetry.TelemetryMetricKindCounter,
				Description: "Reported model cost; pending and deferred polls are excluded.",
				Unit:        "USD",
				Attributes:  map[string]telemetry.TelemetryMetricAttributeDefinition{"provider": aiProvider, "model": aiModel},
			},
		},
	})
})

type aiInstrumentSet struct {
	requestDuration, firstChunkDuration telemetry.TelemetryHistogram
	tokens, cost                        telemetry.TelemetryCounter
}

func createAIInstruments(adapter telemetry.TelemetryMetrics) *aiInstrumentSet {
	metrics := telemetry.CreateTypedMetrics(adapter, AITelemetrySchema())
	return &aiInstrumentSet{
		requestDuration:    metrics.Histogram("pi.ai.request.duration"),
		firstChunkDuration: metrics.Histogram("pi.ai.first_chunk.duration"),
		tokens:             metrics.Counter("pi.ai.tokens"),
		cost:               metrics.Counter("pi.ai.cost"),
	}
}

// aiInstruments caches Pi's instrumentCache WeakMap<TelemetryMetrics, instruments>: instruments are created once per
// metrics object.
//
// A pointer adapter is keyed by a weak pointer to its allocation, and runtime.AddCleanup drops the entry once the
// adapter is collected. Go has no ephemeron: the entry's instruments reference the adapter strongly when the adapter's
// own instruments do (the in-memory recorder's do), and then the adapter stays reachable through its entry, where Pi's
// WeakMap value does not pin its key. An adapter whose instruments do not reference it is released with its entry.
// A non-pointer adapter has no allocation to weaken, so it is keyed by value for the process lifetime; one whose
// dynamic type is not comparable cannot key a map and creates its instruments per request.
var aiInstruments = struct {
	sync.Mutex
	weak  map[weak.Pointer[byte]]*aiInstrumentSet
	value map[telemetry.TelemetryMetrics]*aiInstrumentSet
}{}

func aiInstrumentsFor(adapter telemetry.TelemetryMetrics) *aiInstrumentSet {
	reflected := reflect.ValueOf(adapter)
	if reflected.Kind() == reflect.Pointer && !reflected.IsNil() && reflected.Type().Elem().Size() != 0 {
		address := (*byte)(reflected.UnsafePointer())
		key := weak.Make(address)
		aiInstruments.Lock()
		defer aiInstruments.Unlock()
		if cached, ok := aiInstruments.weak[key]; ok {
			return cached
		}
		instruments := createAIInstruments(adapter)
		if aiInstruments.weak == nil {
			aiInstruments.weak = map[weak.Pointer[byte]]*aiInstrumentSet{}
		}
		aiInstruments.weak[key] = instruments
		runtime.AddCleanup(address, func(key weak.Pointer[byte]) {
			aiInstruments.Lock()
			delete(aiInstruments.weak, key)
			aiInstruments.Unlock()
		}, key)
		return instruments
	}
	if !reflected.IsValid() || !reflected.Type().Comparable() {
		return createAIInstruments(adapter)
	}
	aiInstruments.Lock()
	defer aiInstruments.Unlock()
	if cached, ok := aiInstruments.value[adapter]; ok {
		return cached
	}
	instruments := createAIInstruments(adapter)
	if aiInstruments.value == nil {
		aiInstruments.value = map[telemetry.TelemetryMetrics]*aiInstrumentSet{}
	}
	aiInstruments.value[adapter] = instruments
	return instruments
}

// modelRequestObservation is the state observeModelRequest closes over.
type modelRequestObservation struct {
	started    float64
	attributes telemetry.SpanAttributes
	poll       bool
	instr      *aiInstrumentSet
	span       telemetry.TelemetrySpan
	finished   chan struct{}
	first      bool
	toolChars  map[int]int
}

// observeModelRequest is Pi's observeModelRequest. launch runs the background span callback, as Pi's unawaited start(...) call; a nil observer is returned
// for the no-op span, which has nothing to record (Pi's observer does the same work against no-op capabilities).
// options nil is Pi's omitted options.
func observeModelRequest(requestedModel *Model, options *StreamOptions, poll bool, launch func(func())) *LazyStreamObserver {
	var parent telemetry.TelemetrySpan
	if options != nil {
		parent = options.Telemetry
	}
	if parent == nil || parent == telemetry.NoopTelemetryContext {
		return nil
	}
	o := &modelRequestObservation{
		started:    jsclock.PerformanceNow(),
		attributes: telemetry.SpanAttributes{telemetry.Attr("provider", modelProviderID(requestedModel)), telemetry.Attr("model", requestedModel.ID)},
		poll:       poll,
		span:       telemetry.NoopTelemetryContext,
		finished:   make(chan struct{}),
		first:      true,
	}
	adapter := parent.Metrics()
	if adapter == nil {
		adapter = telemetry.NoopTelemetryMetrics
	}
	o.instr = aiInstrumentsFor(adapter)
	startAttributes := o.attributes.Merge(telemetry.SpanAttributes{telemetry.Attr("api", string(requestedModel.ProviderMeta.API))})
	if options.Thinking != "" {
		startAttributes = startAttributes.Set("reasoning", string(options.Thinking))
	}
	name := "pi.ai.request"
	if poll {
		name = "pi.ai.poll"
	}
	start := telemetry.CreateTypedSpanStarter(parent, AITelemetrySchema())
	ready := make(chan struct{})
	var readyOnce sync.Once
	markReady := func() { readyOnce.Do(func() { close(ready) }) }
	launch(func() {
		defer markReady()
		_ = start(name, startAttributes, func(span telemetry.TelemetrySpan, _ telemetry.TypedSpanStarter) error {
			o.span = span
			markReady()
			<-o.finished
			return nil
		})
	})
	<-ready
	return &LazyStreamObserver{OnEvent: o.onEvent, OnResult: o.onResult}
}

func (o *modelRequestObservation) onEvent(event AssistantMessageEvent) {
	if o.first {
		o.first = false
		o.span.AddEvent("pi.ai.first_chunk", nil)
		o.instr.firstChunkDuration.Record(jsclock.PerformanceNow()-o.started, o.attributes, o.span)
	}
	switch event := event.(type) {
	case ToolCallDeltaEvent:
		if o.toolChars == nil {
			o.toolChars = map[int]int{}
		}
		o.toolChars[event.ContentIndex] += jsstring.Length(event.Delta)
	case TextEndEvent:
		o.span.AddEvent("pi.ai.block", telemetry.SpanAttributes{telemetry.Attr("type", "text"), telemetry.Attr("index", event.ContentIndex), telemetry.Attr("chars", jsstring.Length(event.Content))})
	case ThinkingEndEvent:
		o.span.AddEvent("pi.ai.block", telemetry.SpanAttributes{telemetry.Attr("type", "thinking"), telemetry.Attr("index", event.ContentIndex), telemetry.Attr("chars", jsstring.Length(event.Content))})
	case ToolCallEndEvent:
		chars := o.toolChars[event.ContentIndex]
		delete(o.toolChars, event.ContentIndex)
		o.span.AddEvent("pi.ai.block", telemetry.SpanAttributes{telemetry.Attr("type", "tool_call"), telemetry.Attr("index", event.ContentIndex), telemetry.Attr("chars", chars), telemetry.Attr("tool", event.ToolCall.Name)})
	}
}

// metricAttributes is `{ ...attributes, [name]: value }`: the provider and model, then the extra attribute.
func (o *modelRequestObservation) metricAttributes(name string, value string) telemetry.SpanAttributes {
	return append(o.attributes[:2:2], telemetry.Attr(name, value))
}

func (o *modelRequestObservation) onResult(message *AssistantMessage) {
	if message != nil {
		attributes := telemetry.SpanAttributes{
			telemetry.Attr("stop", string(message.StopReason)),
			telemetry.Attr("usage.input", message.Usage.Input),
			telemetry.Attr("usage.output", message.Usage.Output),
			telemetry.Attr("usage.cache_read", message.Usage.CacheRead),
			telemetry.Attr("usage.cache_write", message.Usage.CacheWrite),
			telemetry.Attr("cost.total", message.Usage.Cost.Total),
		}
		if message.ResponseID != "" {
			attributes = append(attributes, telemetry.Attr("response.id", message.ResponseID))
		}
		o.span.SetAttributes(attributes)
		if message.StopReason == StopReasonError {
			o.span.SetStatus(telemetry.SpanStatus{Status: telemetry.SpanStatusCodeError, Error: &telemetry.SpanStatusError{Name: "provider_error", Message: jsstring.Slice(message.ErrorMessage, 0, 500)}})
		}
		o.instr.requestDuration.Record(jsclock.PerformanceNow()-o.started, o.metricAttributes("stop", string(message.StopReason)), o.span)
		if !o.poll || (message.StopReason != StopReasonPending && message.StopReason != StopReasonDeferred) {
			o.instr.tokens.Add(float64(message.Usage.Input), o.metricAttributes("kind", "input"))
			o.instr.tokens.Add(float64(message.Usage.Output), o.metricAttributes("kind", "output"))
			o.instr.tokens.Add(float64(message.Usage.CacheRead), o.metricAttributes("kind", "cache_read"))
			o.instr.tokens.Add(float64(message.Usage.CacheWrite), o.metricAttributes("kind", "cache_write"))
			o.instr.cost.Add(message.Usage.Cost.Total, o.attributes)
		}
	}
	//portlint:allow doubleclose newLazyObservation reports the terminal result once, so onResult and this close run once
	close(o.finished)
}
