package telemetry

// Ports packages/telemetry/src/metrics.ts.
//
// The conditional and mapped types of metrics.ts (MetricNames, MetricByName, DuplicateMetricNames, MetricAttributes,
// MeasurementArguments, SchemaTelemetryCounter, SchemaTelemetryHistogram and the generic parameters of
// TypedTelemetryMetrics) restrict instrument names to their declared kind, require the declared attributes, reject
// undeclared attributes and closed-set values, and reject duplicate metric names across schemas at compile time. They
// have no Go form and, as upstream, createTypedMetrics validates nothing at runtime: the typed instruments are the
// adapter's instruments, so Go returns TelemetryCounter and TelemetryHistogram where upstream's types narrow them. A
// duplicate metric name across schemas resolves to the last schema's definition, as upstream's Map.set does.
// TelemetryMetricAttributeDefinition's `cardinality?: "low"` has no Go form either (a Go field cannot admit one string
// constant), so a "high" cardinality compiles and is not rejected, exactly as upstream does not reject it at runtime.
// stubgen:omit SchemaTelemetryCounter
// stubgen:omit SchemaTelemetryHistogram

import "maps"

// InstrumentOptions describes an instrument. A nil Description or Unit is upstream's undefined, which a recorder omits;
// a pointer to the empty string is a defined empty string.
type InstrumentOptions struct {
	Description *string
	Unit        *string
}

// HistogramOptions describes a histogram. Boundaries are finite, strictly increasing bucket hints; a nil slice is
// upstream's undefined and an empty slice a defined empty boundary list.
type HistogramOptions struct {
	InstrumentOptions
	Boundaries []float64
}

// TelemetryCounter is a monotonic instrument: it accepts non-negative finite increments.
type TelemetryCounter interface {
	Add(value float64, attributes SpanAttributes)
}

// TelemetryUpDownCounter is an instrument that accepts finite signed increments. Upstream declares it structurally
// identical to TelemetryCounter, so Go names the same method set instead of declaring a duplicate interface.
type TelemetryUpDownCounter = TelemetryCounter

// TelemetryHistogram records finite signed values. A nil context is upstream's omitted context; otherwise the
// measurement is correlated with that context, called an exemplar.
type TelemetryHistogram interface {
	Record(value float64, attributes SpanAttributes, context TelemetryContext)
}

// TelemetryMetrics is the optional adapter capability that creates instruments. Factories and recording methods are
// synchronous, passive and never panic.
type TelemetryMetrics interface {
	Counter(name string, options InstrumentOptions) TelemetryCounter
	UpDownCounter(name string, options InstrumentOptions) TelemetryUpDownCounter
	Histogram(name string, options HistogramOptions) TelemetryHistogram
}

// TelemetryMetricAttributeDefinition is a start-attribute definition whose cardinality, when annotated, is low.
type TelemetryMetricAttributeDefinition = TelemetryStartAttributeDefinition

// TelemetryMetricKind names the instrument a metric definition declares.
type TelemetryMetricKind string

const (
	TelemetryMetricKindCounter       TelemetryMetricKind = "counter"
	TelemetryMetricKindUpDownCounter TelemetryMetricKind = "upDownCounter"
	TelemetryMetricKindHistogram     TelemetryMetricKind = "histogram"
)

// TelemetryMetricDefinition is serializable instrument metadata: its kind, description, optional unit and
// required/optional low-cardinality attributes. Only histograms may declare Boundaries; a nil slice is absent and an
// empty slice serializes as [].
type TelemetryMetricDefinition struct {
	Description string                                        `json:"description"`
	Unit        *string                                       `json:"unit,omitempty"`
	Attributes  map[string]TelemetryMetricAttributeDefinition `json:"attributes"`
	Kind        TelemetryMetricKind                           `json:"kind"`
	Boundaries  []float64                                     `json:"boundaries,omitzero"`
}

// TypedTelemetryMetrics creates the instruments of one or more schemas, passing each instrument its schema metadata.
type TypedTelemetryMetrics struct {
	metrics     TelemetryMetrics
	definitions map[string]TelemetryMetricDefinition
}

// CreateTypedMetrics binds instrument names, kinds and metadata to one or more schemas. The schemas' metric definitions
// are read and retained, as upstream does; no attribute validation is performed. At least one schema is required, as
// upstream's non-empty tuple.
func CreateTypedMetrics(metrics TelemetryMetrics, schema *TelemetrySchemaDefinition, schemas ...*TelemetrySchemaDefinition) *TypedTelemetryMetrics {
	definitions := map[string]TelemetryMetricDefinition{}
	for _, candidate := range append([]*TelemetrySchemaDefinition{schema}, schemas...) {
		maps.Copy(definitions, candidate.Metrics)
	}
	return &TypedTelemetryMetrics{metrics: metrics, definitions: definitions}
}

func (typed *TypedTelemetryMetrics) options(name string) InstrumentOptions {
	definition, ok := typed.definitions[name]
	if !ok {
		return InstrumentOptions{}
	}
	return InstrumentOptions{Description: &definition.Description, Unit: definition.Unit}
}

// Counter returns the adapter's counter for name with the schema's description and unit.
func (typed *TypedTelemetryMetrics) Counter(name string) TelemetryCounter {
	return typed.metrics.Counter(name, typed.options(name))
}

// UpDownCounter returns the adapter's up-down counter for name with the schema's description and unit.
func (typed *TypedTelemetryMetrics) UpDownCounter(name string) TelemetryUpDownCounter {
	return typed.metrics.UpDownCounter(name, typed.options(name))
}

// Histogram returns the adapter's histogram for name with the schema's description, unit and boundaries.
func (typed *TypedTelemetryMetrics) Histogram(name string) TelemetryHistogram {
	return typed.metrics.Histogram(name, HistogramOptions{InstrumentOptions: typed.options(name), Boundaries: typed.definitions[name].Boundaries})
}
