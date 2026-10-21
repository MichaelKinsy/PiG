package telemetry

// Ports packages/telemetry/src/index.ts.
//
// The schema helpers are type-level in TypeScript: the conditional and mapped types that infer exact start, end and
// event attributes, reject undeclared names, and reject duplicate span names across schemas have no Go form, and
// upstream performs no runtime schema validation. Go keeps the serializable schema data and the runtime starter.
// stubgen:omit InferRequiredAndOptionalAttributes
// stubgen:omit InferStartAttributes
// stubgen:omit InferOptionalAttributes
// stubgen:omit ExactTelemetryAttributes
// stubgen:omit InferEventAttributes
// stubgen:omit TelemetrySchemaSpanName
// stubgen:omit TelemetrySchemaSpanStartAttributes
// stubgen:omit TelemetrySchemaSpanEndAttributes
// stubgen:omit TelemetrySchemaSpanEventName
// stubgen:omit TelemetrySchemaSpanEventAttributes
// stubgen:omit SchemaTelemetrySpan
// stubgen:omit TelemetrySchemaSpanUnion
//
// The runner-independent conformance cases of src/testing live in telemetry/telemetrytest, as Go keeps test support in
// its own package.
// stubgen:omit CreateTelemetryAdapterConformance
// stubgen:omit TelemetryAdapterFixture
// stubgen:omit TelemetryAdapterFixture.GetSpans
// stubgen:omit TelemetryAdapterFixture.GetMetrics
// stubgen:omit TelemetryAdapterFixtureFactory
// stubgen:omit TelemetryAdapterConformanceCase
// stubgen:omit TelemetryAdapterConformanceCase.Run

// TelemetryAttributeType names the value type of a schema attribute.
type TelemetryAttributeType string

const (
	TelemetryAttributeTypeString       TelemetryAttributeType = "string"
	TelemetryAttributeTypeNumber       TelemetryAttributeType = "number"
	TelemetryAttributeTypeBoolean      TelemetryAttributeType = "boolean"
	TelemetryAttributeTypeStringArray  TelemetryAttributeType = "string[]"
	TelemetryAttributeTypeNumberArray  TelemetryAttributeType = "number[]"
	TelemetryAttributeTypeBooleanArray TelemetryAttributeType = "boolean[]"
)

// TelemetryAttributeCardinality classifies how many distinct values an attribute takes.
type TelemetryAttributeCardinality string

const (
	TelemetryAttributeCardinalityLow  TelemetryAttributeCardinality = "low"
	TelemetryAttributeCardinalityHigh TelemetryAttributeCardinality = "high"
)

// TelemetryAttributeMetadata documents one schema attribute.
type TelemetryAttributeMetadata struct {
	Description string                        `json:"description"`
	Sensitive   *bool                         `json:"sensitive,omitempty"`
	Cardinality TelemetryAttributeCardinality `json:"cardinality,omitempty"`
}

// TelemetryAttributeDefinition declares one attribute's type and optional closed value set and examples.
//
// Upstream's `values`, `elementValues` and `examples` take the element type of `type` ("string" takes readonly string[],
// "number[]" takes readonly number[] for elementValues and readonly (readonly number[])[] for examples). Go has no sum of
// slice types, so each arm is a field of its own element type; the one that applies to Type is the one read and written, and
// serializes under upstream's member name. A nil slice is an absent member and an empty slice serializes as [], as
// JSON.stringify writes the schema object.
type TelemetryAttributeDefinition struct {
	TelemetryAttributeMetadata
	Type TelemetryAttributeType

	// Values and Examples are the members of a "string" attribute.
	Values   []string
	Examples []string
	// NumberValues and NumberExamples are the members of a "number" attribute.
	NumberValues   []float64
	NumberExamples []float64
	// BooleanValues and BooleanExamples are the members of a "boolean" attribute.
	BooleanValues   []bool
	BooleanExamples []bool
	// ElementValues and ArrayExamples are the members of a "string[]" attribute.
	ElementValues []string
	ArrayExamples [][]string
	// NumberElementValues and NumberArrayExamples are the members of a "number[]" attribute.
	NumberElementValues []float64
	NumberArrayExamples [][]float64
	// BooleanElementValues and BooleanArrayExamples are the members of a "boolean[]" attribute.
	BooleanElementValues []bool
	BooleanArrayExamples [][]bool
}

// TelemetryStartAttributeDefinition is an attribute supplied when a span starts.
type TelemetryStartAttributeDefinition struct {
	TelemetryAttributeDefinition
	Required bool
}

// TelemetryEventAttributeDefinition is an attribute of a span event; it has the start-attribute shape.
type TelemetryEventAttributeDefinition = TelemetryStartAttributeDefinition

// TelemetryEventDefinition declares one span event.
type TelemetryEventDefinition struct {
	Description string                                       `json:"description"`
	Attributes  map[string]TelemetryEventAttributeDefinition `json:"attributes"`
}

// TelemetryParentKind selects which parents a span accepts.
type TelemetryParentKind string

const (
	TelemetryParentKindAny            TelemetryParentKind = "any"
	TelemetryParentKindRootOrExternal TelemetryParentKind = "root_or_external"
	TelemetryParentKindSpans          TelemetryParentKind = "spans"
)

// TelemetryParentDefinition declares a span's allowed parents. Spans applies to TelemetryParentKindSpans; a nil slice is
// absent and an empty slice serializes as [].
type TelemetryParentDefinition struct {
	Kind  TelemetryParentKind `json:"kind"`
	Spans []string            `json:"spans,omitzero"`
}

// TelemetrySpanStatusDefinition documents a span's default status and when it reports an error.
type TelemetrySpanStatusDefinition struct {
	Default   SpanStatusCode `json:"default"`
	ErrorWhen string         `json:"errorWhen"`
}

// TelemetrySpanDefinition declares one span name. A nil Events is absent and an empty map serializes as {}.
type TelemetrySpanDefinition struct {
	Description     string                                       `json:"description"`
	Parents         TelemetryParentDefinition                    `json:"parents"`
	StartAttributes map[string]TelemetryStartAttributeDefinition `json:"startAttributes"`
	EndAttributes   map[string]TelemetryAttributeDefinition      `json:"endAttributes"`
	Events          map[string]TelemetryEventDefinition          `json:"events,omitzero"`
	Status          TelemetrySpanStatusDefinition                `json:"status"`
}

// TelemetrySchemaDefinition is serializable telemetry schema data. A nil Metrics is absent and an empty map serializes
// as {}.
type TelemetrySchemaDefinition struct {
	Version int                                  `json:"version"`
	Spans   map[string]TelemetrySpanDefinition   `json:"spans"`
	Metrics map[string]TelemetryMetricDefinition `json:"metrics,omitzero"`
}

// DefineTelemetrySchema returns schema unchanged. It is the identity helper for serializable telemetry schema data.
func DefineTelemetrySchema(schema *TelemetrySchemaDefinition) *TelemetrySchemaDefinition {
	return schema
}

// TypedSpanStarter starts a span by schema span name under one explicit parent context. The callback receives the
// span and a starter bound to that span for its children.
type TypedSpanStarter func(name string, attributes SpanAttributes, callback func(span TelemetrySpan, startChildSpan TypedSpanStarter) error) error

// StartTypedSpan starts a schema span through starter and returns the callback's result, as an upstream TypedSpanStarter call resolves
// to it. A rejected callback returns its error and the zero value. The child starter passed to callback is bound to the new span.
func StartTypedSpan[R any](starter TypedSpanStarter, name string, attributes SpanAttributes, callback func(span TelemetrySpan, startChildSpan TypedSpanStarter) (R, error)) (R, error) {
	var result R
	err := starter(name, attributes, func(span TelemetrySpan, startChildSpan TypedSpanStarter) error {
		value, err := callback(span, startChildSpan)
		result = value
		return err
	})
	if err != nil {
		var zero R
		return zero, err
	}
	return result, nil
}

func bindTypedSpanStarter(telemetryContext TelemetryContext) TypedSpanStarter {
	return func(name string, attributes SpanAttributes, callback func(TelemetrySpan, TypedSpanStarter) error) error {
		return telemetryContext.StartSpan(SpanOptions{Name: name, Attributes: attributes}, func(span TelemetrySpan) error {
			return callback(span, bindTypedSpanStarter(span))
		})
	}
}

// CreateTypedSpanStarter binds an explicit parent context to the combined span vocabulary of one or more schemas.
// The schemas are not read: as upstream, no runtime schema validation is performed.
// At least one schema is required, as upstream's non-empty TelemetrySchemaTuple.
func CreateTypedSpanStarter(telemetryContext TelemetryContext, _ *TelemetrySchemaDefinition, _ ...*TelemetrySchemaDefinition) TypedSpanStarter {
	return bindTypedSpanStarter(telemetryContext)
}
