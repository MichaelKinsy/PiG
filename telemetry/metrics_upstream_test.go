package telemetry_test

// Ports packages/telemetry/test/metrics.test.ts.
//
// The expectTypeOf and @ts-expect-error assertions check TypeScript inference: counter names restricted to counters,
// histogram names to histograms, unknown names, required and exact measurement attributes, closed-set values, variables
// with unknown attributes, duplicate metric names across schemas, and high-cardinality attributes on metrics. They have no
// Go form, and upstream performs no runtime validation for them (see the header of metrics.go). Object.isFrozen has no Go
// form either: the shared no-op instruments are stateless values. An unreadable Proxy argument has no Go form: a Go options
// struct or attribute bag cannot fail to read. The Go forms of those cases are the values whose methods panic and a
// context whose methods panic.

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry"
)

// panickingValue is the Go form of upstream's unreadable payload: every method that could inspect it panics.
type panickingValue struct{}

func (panickingValue) String() string               { panic("read") }
func (panickingValue) MarshalJSON() ([]byte, error) { panic("read") }

// panickingContext is the Go form of upstream's unreadable context: calling any method on it panics.
type panickingContext struct{}

func (panickingContext) StartSpan(telemetry.SpanOptions, func(telemetry.TelemetrySpan) error) error {
	panic("read")
}

func metricAttribute(attributeType telemetry.TelemetryAttributeType, description string, required bool, values ...telemetry.AttributeValue) telemetry.TelemetryMetricAttributeDefinition {
	definition := telemetry.TelemetryMetricAttributeDefinition{Required: required}
	definition.Type = attributeType
	definition.Description = description
	definition.Values = values
	return definition
}

func trafficSchema() *telemetry.TelemetrySchemaDefinition {
	direction := metricAttribute(telemetry.TelemetryAttributeTypeString, "Direction", true, "in", "out")
	direction.Cardinality = telemetry.TelemetryAttributeCardinalityLow
	return telemetry.DefineTelemetrySchema(&telemetry.TelemetrySchemaDefinition{
		Version: 1,
		Spans:   map[string]telemetry.TelemetrySpanDefinition{},
		Metrics: map[string]telemetry.TelemetryMetricDefinition{
			"network.bytes": {
				Kind: telemetry.TelemetryMetricKindCounter, Description: "Encoded bytes", Unit: new("By"),
				Attributes: map[string]telemetry.TelemetryMetricAttributeDefinition{"direction": direction},
			},
			"network.active": {Kind: telemetry.TelemetryMetricKindUpDownCounter, Description: "Open links", Attributes: map[string]telemetry.TelemetryMetricAttributeDefinition{}},
		},
	})
}

func latencySchema() *telemetry.TelemetrySchemaDefinition {
	return telemetry.DefineTelemetrySchema(&telemetry.TelemetrySchemaDefinition{
		Version: 1,
		Spans:   map[string]telemetry.TelemetrySpanDefinition{},
		Metrics: map[string]telemetry.TelemetryMetricDefinition{
			"call.duration": {
				Kind: telemetry.TelemetryMetricKindHistogram, Description: "Call duration", Unit: new("ms"), Boundaries: []float64{1, 10, 100},
				Attributes: map[string]telemetry.TelemetryMetricAttributeDefinition{"method": metricAttribute(telemetry.TelemetryAttributeTypeString, "Method", false)},
			},
		},
	})
}

// TestMetricsUpstream ports packages/telemetry/test/metrics.test.ts. Each subtest names one upstream case.
// Pi source: packages/telemetry/src/metrics.ts, packages/telemetry/src/memory.ts, packages/telemetry/src/noop.ts
func TestMetricsUpstream(t *testing.T) {
	t.Run("metrics › binds schema names, kinds, metadata, and exact measurement attributes", func(t *testing.T) {
		// upstream: packages/telemetry/test/metrics.test.ts:62
		adapter := telemetry.NewInMemoryTelemetryContext()
		traffic, latency := trafficSchema(), latencySchema()
		metrics := telemetry.CreateTypedMetrics(adapter, traffic, latency)
		metrics.Counter("network.bytes").Add(128, telemetry.SpanAttributes{"direction": "out"})
		metrics.UpDownCounter("network.active").Add(1, nil)
		metrics.UpDownCounter("network.active").Add(-1, nil)
		if err := adapter.StartSpan(telemetry.SpanOptions{Name: "request"}, func(span telemetry.TelemetrySpan) error {
			metrics.Histogram("call.duration").Record(12, telemetry.SpanAttributes{"method": "read"}, span)
			metrics.Histogram("call.duration").Record(13, nil, nil)
			metrics.Histogram("call.duration").Record(14, nil, span)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		measurements := adapter.GetMetrics()
		if len(measurements) != 6 {
			t.Fatalf("%d measurements, want 6", len(measurements))
		}
		counter := measurements[0]
		if counter.Kind != telemetry.TelemetryMetricKindCounter || counter.Name != "network.bytes" || counter.Value != 128 ||
			counter.Description == nil || *counter.Description != "Encoded bytes" || counter.Unit == nil || *counter.Unit != "By" ||
			!reflect.DeepEqual(counter.Attributes, telemetry.SpanAttributes{"direction": "out"}) {
			t.Fatalf("counter measurement = %+v", counter)
		}
		span := adapter.GetSpans()[0]
		histogram := measurements[3]
		if histogram.Kind != telemetry.TelemetryMetricKindHistogram || !reflect.DeepEqual(histogram.Boundaries, []float64{1, 10, 100}) || !reflect.DeepEqual(histogram.Context, span.Context) {
			t.Fatalf("histogram measurement = %+v, want boundaries [1 10 100] and the request span as exemplar", histogram)
		}
		if measurements[4].Context != nil {
			t.Fatalf("a measurement without a context records exemplar %v, want null", measurements[4].Context)
		}
		if !reflect.DeepEqual(measurements[5].Context, span.Context) {
			t.Fatalf("an undefined attribute bag with a context records exemplar %v, want %v", measurements[5].Context, span.Context)
		}
		encoded, err := json.Marshal(traffic)
		if err != nil {
			t.Fatalf("schema must serialize: %v", err)
		}
		const want = `{"version":1,"spans":{},"metrics":{"network.active":{"description":"Open links","attributes":{},"kind":"upDownCounter"},` +
			`"network.bytes":{"description":"Encoded bytes","unit":"By","attributes":{"direction":{"description":"Direction","cardinality":"low",` +
			`"type":"string","values":["in","out"],"required":true}},"kind":"counter"}}}`
		if string(encoded) != want {
			t.Fatalf("serialized schema:\n got %s\nwant %s", encoded, want)
		}
	})

	t.Run("metrics › requires histogram attributes when the schema declares them", func(t *testing.T) {
		// upstream: packages/telemetry/test/metrics.test.ts:121
		schema := telemetry.DefineTelemetrySchema(&telemetry.TelemetrySchemaDefinition{
			Version: 1,
			Spans:   map[string]telemetry.TelemetrySpanDefinition{},
			Metrics: map[string]telemetry.TelemetryMetricDefinition{"required": {
				Kind: telemetry.TelemetryMetricKindHistogram, Description: "Required tag",
				Attributes: map[string]telemetry.TelemetryMetricAttributeDefinition{"cache": metricAttribute(telemetry.TelemetryAttributeTypeBoolean, "Cache hit", true)},
			}},
		})
		adapter := telemetry.NewInMemoryTelemetryContext()
		telemetry.CreateTypedMetrics(adapter, schema).Histogram("required").Record(1, telemetry.SpanAttributes{"cache": true}, nil)
		measurements := adapter.GetMetrics()
		if len(measurements) != 1 || measurements[0].Name != "required" || measurements[0].Value != 1 || !reflect.DeepEqual(measurements[0].Attributes, telemetry.SpanAttributes{"cache": true}) ||
			measurements[0].Description == nil || *measurements[0].Description != "Required tag" || measurements[0].Unit != nil || measurements[0].Boundaries != nil {
			t.Fatalf("measurements = %+v", measurements)
		}
	})

	t.Run("metrics › returns detached measurement snapshots including exemplars and boundaries", func(t *testing.T) {
		// upstream: packages/telemetry/test/metrics.test.ts:148
		adapter := telemetry.NewInMemoryTelemetryContext()
		histogram := adapter.Histogram("duration", telemetry.HistogramOptions{Boundaries: []float64{1, 2}})
		if err := adapter.StartSpan(telemetry.SpanOptions{Name: "request"}, func(span telemetry.TelemetrySpan) error {
			histogram.Record(1, telemetry.SpanAttributes{"tags": []string{"original"}}, span)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		measurement := adapter.GetMetrics()[0]
		if measurement.Context == nil {
			t.Fatal("Expected measurement context")
		}
		// A Go slice aliases its backing array, so each element is mutated in place as well as the slice replaced.
		measurement.Boundaries[0] = 99
		measurement.Boundaries = append(measurement.Boundaries, 3)
		measurement.Attributes["tags"].([]string)[0] = "mutated"
		measurement.Attributes["tags"] = []string{"mutated"}
		measurement.Context["pi-memory-span"] = "999"
		again := adapter.GetMetrics()[0]
		if !reflect.DeepEqual(again.Boundaries, []float64{1, 2}) || !reflect.DeepEqual(again.Attributes, telemetry.SpanAttributes{"tags": []string{"original"}}) || !reflect.DeepEqual(again.Context, adapter.GetSpans()[0].Context) {
			t.Fatalf("measurement = %+v, want boundaries [1 2], tags [original] and the request span as exemplar", again)
		}
	})

	t.Run("metrics › ignores malformed metadata, nonfinite values, and unreadable attributes", func(t *testing.T) {
		// upstream: packages/telemetry/test/metrics.test.ts:164
		adapter := telemetry.NewInMemoryTelemetryContext()
		// Upstream's first two calls pass unreadable option bags, which have no Go form: a Go options struct cannot fail to read.
		for _, boundaries := range [][]float64{{1, 1}, {2, 1}, {math.NaN()}, {math.Inf(1)}} {
			adapter.Histogram("bad", telemetry.HistogramOptions{Boundaries: boundaries}).Record(1, nil, nil)
		}
		for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
			adapter.Counter("bad", telemetry.InstrumentOptions{}).Add(value, nil)
			adapter.UpDownCounter("bad", telemetry.InstrumentOptions{}).Add(value, nil)
			adapter.Histogram("bad", telemetry.HistogramOptions{}).Record(value, nil, nil)
		}
		adapter.Counter("bad", telemetry.InstrumentOptions{}).Add(-1, nil)
		// Upstream's last invalid call passes an unreadable attribute bag. A Go bag cannot fail to read, and the recorder stores
		// a value it never inspects, so that call is not ported; the recorder passivity of an unreadable value is the
		// "metrics" conformance case.
		if measurements := adapter.GetMetrics(); len(measurements) != 0 {
			t.Fatalf("measurements = %+v, want none", measurements)
		}
		adapter.Histogram("valid", telemetry.HistogramOptions{}).Record(-1, telemetry.SpanAttributes{}, telemetry.NoopTelemetryContext)
		if measurements := adapter.GetMetrics(); len(measurements) != 1 || measurements[0].Context != nil {
			t.Fatalf("measurements = %+v, want one measurement without an exemplar", measurements)
		}
	})

	t.Run("metrics › shares frozen no-op instruments without inspecting arguments", func(t *testing.T) {
		// upstream: packages/telemetry/test/metrics.test.ts:182
		options := telemetry.HistogramOptions{InstrumentOptions: telemetry.InstrumentOptions{Unit: new("By")}}
		attributes := telemetry.SpanAttributes{"secret": panickingValue{}}
		counter := telemetry.NoopTelemetryMetrics.Counter("bytes", options.InstrumentOptions)
		active := telemetry.NoopTelemetryMetrics.UpDownCounter("active", options.InstrumentOptions)
		histogram := telemetry.NoopTelemetryMetrics.Histogram("duration", options)
		if counter != telemetry.NoopTelemetryMetrics.Counter("other", telemetry.InstrumentOptions{}) {
			t.Fatal("the no-op counters differ: the instrument is shared")
		}
		if active != telemetry.NoopTelemetryMetrics.UpDownCounter("other", telemetry.InstrumentOptions{}) || histogram != telemetry.NoopTelemetryMetrics.Histogram("other", telemetry.HistogramOptions{}) {
			t.Fatal("the no-op up-down counters or histograms differ: the instrument is shared")
		}
		counter.Add(1, attributes)
		active.Add(-1, attributes)
		histogram.Record(1, attributes, panickingContext{})
	})
}

// A metric name no schema declares creates an instrument without schema metadata, as upstream's Map.get yields undefined for it, and a name declared by two
// schemas takes the last schema's definition, as Map.set does. Upstream rejects the duplicate at compile time only.
// upstream: packages/telemetry/src/metrics.ts createTypedMetrics
func TestCreateTypedMetricsReadsSchemaMetadataForDeclaredNamesOnly(t *testing.T) {
	adapter := telemetry.NewInMemoryTelemetryContext()
	first := telemetry.DefineTelemetrySchema(&telemetry.TelemetrySchemaDefinition{Version: 1, Spans: map[string]telemetry.TelemetrySpanDefinition{}, Metrics: map[string]telemetry.TelemetryMetricDefinition{
		"shared": {Kind: telemetry.TelemetryMetricKindCounter, Description: "first", Unit: new("a"), Attributes: map[string]telemetry.TelemetryMetricAttributeDefinition{}},
	}})
	second := telemetry.DefineTelemetrySchema(&telemetry.TelemetrySchemaDefinition{Version: 1, Spans: map[string]telemetry.TelemetrySpanDefinition{}, Metrics: map[string]telemetry.TelemetryMetricDefinition{
		"shared": {Kind: telemetry.TelemetryMetricKindCounter, Description: "second", Attributes: map[string]telemetry.TelemetryMetricAttributeDefinition{}},
	}})
	noMetrics := telemetry.DefineTelemetrySchema(&telemetry.TelemetrySchemaDefinition{Version: 1, Spans: map[string]telemetry.TelemetrySpanDefinition{}})
	metrics := telemetry.CreateTypedMetrics(adapter, noMetrics, first, second)
	metrics.Counter("shared").Add(1, nil)
	metrics.Counter("undeclared").Add(2, nil)
	measurements := adapter.GetMetrics()
	if len(measurements) != 2 {
		t.Fatalf("measurements = %+v", measurements)
	}
	if measurements[0].Description == nil || *measurements[0].Description != "second" || measurements[0].Unit != nil {
		t.Fatalf("shared metric = %+v, want the last schema's description and no unit", measurements[0])
	}
	if measurements[1].Description != nil || measurements[1].Unit != nil {
		t.Fatalf("undeclared metric = %+v, want no metadata", measurements[1])
	}
	encoded, err := json.Marshal(noMetrics)
	if err != nil || string(encoded) != `{"version":1,"spans":{}}` {
		t.Fatalf("a schema without metrics serializes as %s, %v; want the metrics member absent", encoded, err)
	}
}
