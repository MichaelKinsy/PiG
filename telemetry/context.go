// Package telemetry provides backend-neutral span carriers, propagation, links, metrics and in-memory recording.
// Ports packages/telemetry/src/index.ts and packages/telemetry/src/noop.ts.
package telemetry

// AttributeValue is a string, number, boolean, or slice of those values.
type AttributeValue = any

// SpanAttributes carries named telemetry values. A nil value is upstream's undefined: recorders omit it.
type SpanAttributes map[string]AttributeValue

// TelemetryCarrier is opaque, JSON-serializable, adapter-owned propagation data. A nil carrier is upstream's undefined:
// Inject returns it for a context with nothing to propagate and Extract accepts it as an absent carrier.
type TelemetryCarrier map[string]string

// TelemetryPropagation is the optional transport capability implemented by the application's telemetry adapter.
// Both methods are synchronous, passive and never panic. Inject returns a detached carrier, or nil for roots, no-op
// contexts and contexts the adapter does not recognize. Extract returns a receiver-owned parent and falls back to the
// adapter's root for an absent, invalid, unknown or unreadable carrier.
type TelemetryPropagation interface {
	Inject(TelemetryContext) TelemetryCarrier
	Extract(TelemetryCarrier) TelemetryContext
}

// SpanLink relates a span to the work Context names without changing the span's parent. A nil Attributes is upstream's
// absent attributes.
type SpanLink struct {
	Context    TelemetryContext
	Attributes SpanAttributes
}

// SpanOptions supplies a span's name, initial attributes and initial links.
type SpanOptions struct {
	Name       string
	Attributes SpanAttributes
	Links      []SpanLink
}

// SpanStatusCode is the final status of a span.
type SpanStatusCode string

const (
	SpanStatusCodeOK    SpanStatusCode = "ok"
	SpanStatusCodeError SpanStatusCode = "error"
)

// SpanStatusError describes an optional error status.
type SpanStatusError struct{ Name, Message string }

// SpanStatus records success or failure and optional error details.
type SpanStatus struct {
	Status SpanStatusCode
	Error  *SpanStatusError
}

// TelemetryContext starts child spans and waits for their callbacks.
type TelemetryContext interface {
	StartSpan(SpanOptions, func(TelemetrySpan) error) error
}

// StartSpan starts a span from telemetryContext and returns the callback's result, as upstream startSpan<T> resolves to it. A rejected
// callback returns its error and the zero value. Go interface methods cannot be generic, so the typed result travels through this
// function over TelemetryContext.StartSpan.
func StartSpan[T any](telemetryContext TelemetryContext, options SpanOptions, callback func(TelemetrySpan) (T, error)) (T, error) {
	var result T
	err := telemetryContext.StartSpan(options, func(span TelemetrySpan) error {
		value, err := callback(span)
		result = value
		return err
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return result, nil
}

// TelemetrySpan records attributes, events and status while active.
type TelemetrySpan interface {
	TelemetryContext
	AddLink(SpanLink)
	AddEvent(string, SpanAttributes)
	SetAttributes(SpanAttributes)
	SetStatus(SpanStatus)
}

type noopTelemetrySpan struct{}

func (noopTelemetrySpan) StartSpan(_ SpanOptions, callback func(TelemetrySpan) error) error {
	return callback(NoopTelemetryContext)
}
func (noopTelemetrySpan) AddLink(SpanLink)                {}
func (noopTelemetrySpan) AddEvent(string, SpanAttributes) {}
func (noopTelemetrySpan) SetAttributes(SpanAttributes)    {}
func (noopTelemetrySpan) SetStatus(SpanStatus)            {}

// NoopTelemetryContext is the shared parent used without a recording backend.
var NoopTelemetryContext TelemetrySpan = noopTelemetrySpan{}

type noopTelemetryPropagation struct{}

func (noopTelemetryPropagation) Inject(TelemetryContext) TelemetryCarrier { return nil }
func (noopTelemetryPropagation) Extract(TelemetryCarrier) TelemetryContext {
	return NoopTelemetryContext
}

// NoopTelemetryPropagation is the shared propagation capability that neither inspects nor retains its inputs: Inject
// returns nil and Extract returns NoopTelemetryContext.
var NoopTelemetryPropagation TelemetryPropagation = noopTelemetryPropagation{}

type noopTelemetryCounter struct{}

func (noopTelemetryCounter) Add(float64, SpanAttributes) {}

type noopTelemetryHistogram struct{}

func (noopTelemetryHistogram) Record(float64, SpanAttributes, TelemetryContext) {}

type noopTelemetryMetrics struct{}

func (noopTelemetryMetrics) Counter(string, InstrumentOptions) TelemetryCounter {
	return noopTelemetryCounter{}
}
func (noopTelemetryMetrics) UpDownCounter(string, InstrumentOptions) TelemetryUpDownCounter {
	return noopTelemetryCounter{}
}
func (noopTelemetryMetrics) Histogram(string, HistogramOptions) TelemetryHistogram {
	return noopTelemetryHistogram{}
}

// NoopTelemetryMetrics is the shared metric capability whose instruments never inspect or retain metadata,
// measurements or contexts. Each factory returns the same shared instrument.
var NoopTelemetryMetrics TelemetryMetrics = noopTelemetryMetrics{}
