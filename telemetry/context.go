// Package telemetry provides backend-neutral span carriers and in-memory recording.
// Ports packages/telemetry/src/index.ts and packages/telemetry/src/noop.ts.
package telemetry

// AttributeValue is a string, number, boolean, or slice of those values.
type AttributeValue = any

// SpanAttributes carries named telemetry values.
type SpanAttributes map[string]AttributeValue

// SpanOptions supplies a span's name and initial attributes.
type SpanOptions struct {
	Name       string
	Attributes SpanAttributes
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

// TelemetrySpan records attributes, events and status while active.
type TelemetrySpan interface {
	TelemetryContext
	AddEvent(string, SpanAttributes)
	SetAttributes(SpanAttributes)
	SetStatus(SpanStatus)
}

type noopTelemetrySpan struct{}

func (noopTelemetrySpan) StartSpan(_ SpanOptions, callback func(TelemetrySpan) error) error {
	return callback(NoopTelemetryContext)
}
func (noopTelemetrySpan) AddEvent(string, SpanAttributes) {}
func (noopTelemetrySpan) SetAttributes(SpanAttributes)    {}
func (noopTelemetrySpan) SetStatus(SpanStatus)            {}

// NoopTelemetryContext is the shared parent used without a recording backend.
var NoopTelemetryContext TelemetrySpan = noopTelemetrySpan{}
