package telemetry

// Ports the StoredSpan, StoredTrace, TraceQuery, MetricQuery, StoredMetric and TelemetryStore declarations of
// packages/telemetry/src/index.ts.
//
// Upstream's `Promise<T>` store reads are blocking Go calls returning the value and an error. An optional member is a
// pointer or a nil slice or map, as elsewhere in this package; a pointer to a zero value is a defined zero.

// StoredSpanEvent is an event of a settled span. TimeMs is relative to the span's start.
type StoredSpanEvent struct {
	Name       string
	TimeMs     float64
	Attributes SpanAttributes
}

// StoredSpanLink names the span a settled span links to. A nil Attributes is upstream's absent attributes.
type StoredSpanLink struct {
	TraceID    string
	SpanID     string
	Attributes SpanAttributes
}

// StoredSpan is a settled span snapshot, independent of the recording adapter. A nil ParentID is upstream's absent
// parentId.
type StoredSpan struct {
	ID         string
	TraceID    string
	ParentID   *string
	Name       string
	StartedMs  float64
	EndedMs    float64
	Attributes SpanAttributes
	Events     []StoredSpanEvent
	Links      []StoredSpanLink
	Status     SpanStatus
}

// StoredTrace is the settled spans of one trace.
type StoredTrace struct {
	TraceID string
	Spans   []StoredSpan
}

// TraceQuery filters FindTraces. Every nil member is upstream's absent member.
type TraceQuery struct {
	// Name matches the earliest stored span's name, with the span ID breaking timestamp ties.
	Name *string
	// Attributes holds string, number or boolean values; all equalities must match one span, which may be anywhere in
	// the trace.
	Attributes map[string]AttributeValue
	// Errors, when true, keeps traces with an error span. Upstream's `errors?: true` admits only true; false is absent.
	Errors bool
	// StartMin and StartMax are inclusive bounds on the earliest stored span's start time.
	StartMin *float64
	StartMax *float64
	// Limit is a positive page size; nil defaults to 20.
	Limit *int
	// Cursor is the opaque continuation from the previous page; the same filters are retained.
	Cursor *string
}

// MetricQuery filters FindMetrics.
type MetricQuery struct {
	Name string
	// StartMin and StartMax are inclusive bounds on interval start time.
	StartMin   *float64
	StartMax   *float64
	Attributes map[string]AttributeValue
}

// StoredMetric is one interval's measurements of a series. Unit is nil when the instrument declared none.
type StoredMetric struct {
	IntervalMs float64
	Name       string
	Kind       TelemetryMetricKind
	Unit       *string
	Attributes SpanAttributes
	Count      float64
	Sum        float64
	Min        float64
	Max        float64
}

// FindTracesResult is upstream's `{ traces, cursor? }`: a nil Cursor is the last page.
type FindTracesResult struct {
	Traces []StoredTrace
	Cursor *string
}

// TelemetryStore reads settled traces without depending on a storage backend. Upstream's three Promise methods are
// blocking calls: each returns when the read has settled, and a rejection is the returned error.
type TelemetryStore interface {
	// FindTraces returns traces, newest earliest-span start first, with trace ID order among equal starts, and a cursor
	// when more remain. A zero TraceQuery is upstream's omitted query.
	FindTraces(query TraceQuery) (FindTracesResult, error)
	// GetTrace returns nil, upstream's undefined, for an unknown or unsettled trace.
	GetTrace(traceID string) (*StoredTrace, error)
	FindMetrics(query MetricQuery) ([]StoredMetric, error)
}
