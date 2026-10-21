// Package telemetry provides backend-neutral span carriers, propagation, links, metrics and in-memory recording.
// Ports packages/telemetry/src/index.ts and packages/telemetry/src/noop.ts.
//
// The adapter contract trusts typed inputs: malformed input behavior is unspecified, as upstream states.
package telemetry

import "github.com/MichaelKinsy/PiG/internal/jsstring"

// AttributeValue is a string, number, boolean, or slice of those values.
type AttributeValue = any

// SpanAttribute is one named telemetry value. A nil Value is upstream's undefined: recorders omit it.
type SpanAttribute struct {
	Name  string
	Value AttributeValue
}

// Attr is a SpanAttribute: SpanAttributes{Attr("name", value)}.
func Attr(name string, value AttributeValue) SpanAttribute {
	return SpanAttribute{Name: name, Value: value}
}

// SpanAttributes carries named telemetry values in insertion order, as a JavaScript object's own string keys do, so
// the order an attribute bag is written in is the order recorders, stores and renderers report it. A name written
// again keeps its position (see Set); recorders store a bag with each name once and without undefined (nil) values.
type SpanAttributes []SpanAttribute

// Get is `attributes[name]` with presence: the value of the first attribute named name.
func (attributes SpanAttributes) Get(name string) (AttributeValue, bool) {
	for _, attribute := range attributes {
		if attribute.Name == name {
			return attribute.Value, true
		}
	}
	return nil, false
}

// Value is `attributes[name]`: the value of the first attribute named name, nil (upstream's undefined) when absent.
func (attributes SpanAttributes) Value(name string) AttributeValue {
	value, _ := attributes.Get(name)
	return value
}

// Set is the assignment `attributes[name] = value`: an existing name keeps its position and takes value, and a new name
// is appended, except that a name which is a canonical array index ("0", "17", never "01" or "-1") is placed among the
// leading index names in ascending numeric order, as JavaScript lists an object's integer-like keys first. It writes
// into the receiver's backing array like append does, so the caller keeps the result.
func (attributes SpanAttributes) Set(name string, value AttributeValue) SpanAttributes {
	for index := range attributes {
		if attributes[index].Name == name {
			attributes[index].Value = value
			return attributes
		}
	}
	attribute := SpanAttribute{Name: name, Value: value}
	position := len(attributes)
	if number, ok := jsstring.ArrayIndex(name); ok {
		position = 0
		for position < len(attributes) {
			existing, ok := jsstring.ArrayIndex(attributes[position].Name)
			if !ok || existing > number {
				break
			}
			position++
		}
	}
	attributes = append(attributes, SpanAttribute{})
	copy(attributes[position+1:], attributes[position:])
	attributes[position] = attribute
	return attributes
}

// MarshalJSON is JSON.stringify of the object: the attributes as members in insertion order. A nil bag is null.
func (attributes SpanAttributes) MarshalJSON() ([]byte, error) {
	if attributes == nil {
		return []byte("null"), nil
	}
	encoded := []byte{'{'}
	for index, attribute := range attributes {
		if index > 0 {
			encoded = append(encoded, ',')
		}
		encoded = append(jsstring.AppendString(encoded, attribute.Name), ':')
		var err error
		if encoded, err = jsstring.AppendMarshalJSON(encoded, attribute.Value); err != nil {
			return nil, err
		}
	}
	return append(encoded, '}'), nil
}

// Merge is the object spread `{ ...attributes, ...other }`: the attributes of other overwrite in place or append, and a
// nil value of other is upstream's undefined, which is not written. The receiver is not modified.
func (attributes SpanAttributes) Merge(other SpanAttributes) SpanAttributes {
	merged := make(SpanAttributes, len(attributes), len(attributes)+len(other))
	copy(merged, attributes)
	for _, attribute := range other {
		if attribute.Value != nil {
			merged = merged.Set(attribute.Name, attribute.Value)
		}
	}
	return merged
}

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

// TelemetryContext starts child spans and waits for their callbacks. Metrics and Propagation are the adapter's optional
// capabilities, upstream's readonly `metrics` and `propagation` members: they stay available on child spans and
// extracted parents, and a nil result is an absent capability.
type TelemetryContext interface {
	Metrics() TelemetryMetrics
	Propagation() TelemetryPropagation
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

func (noopTelemetrySpan) Metrics() TelemetryMetrics         { return NoopTelemetryMetrics }
func (noopTelemetrySpan) Propagation() TelemetryPropagation { return NoopTelemetryPropagation }
func (noopTelemetrySpan) StartSpan(_ SpanOptions, callback func(TelemetrySpan) error) error {
	return callback(NoopTelemetryContext)
}
func (noopTelemetrySpan) AddLink(SpanLink)                {}
func (noopTelemetrySpan) AddEvent(string, SpanAttributes) {}
func (noopTelemetrySpan) SetAttributes(SpanAttributes)    {}
func (noopTelemetrySpan) SetStatus(SpanStatus)            {}

// NoopTelemetryContext is the shared parent used without a recording backend. It is a span, so a Context's telemetry
// parent is always a span.
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
