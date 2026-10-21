package telemetry_test

// Ports packages/telemetry/test/store.test.ts.
//
// The shared store conformance cases run as subtests through telemetrytest. vi.spyOn(performance, "now") is
// jsclock.MockPerformanceNow, and vi.spyOn(Date, "now") is jsclock.MockDateNow.

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsclock"
	"github.com/MichaelKinsy/PiG/telemetry"
	"github.com/MichaelKinsy/PiG/telemetry/telemetrytest"
)

func TestInMemoryTelemetryStoreConformance(t *testing.T) {
	for _, testCase := range telemetrytest.CreateTelemetryStoreConformance("in-memory telemetry store conformance", func(context.Context) (telemetrytest.StoreFixture, error) {
		recorder := telemetry.NewInMemoryTelemetryContext()
		return telemetrytest.StoreFixture{Context: recorder, Store: recorder}, nil
	}) {
		t.Run(testCase.Group+" › "+testCase.Name, func(t *testing.T) {
			if err := testCase.Run(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInMemoryStoreSnapshots(t *testing.T) {
	t.Run("records wall-clock boundaries and relative event times without changing normalized event snapshots", func(t *testing.T) {
		_, restoreDate := jsclock.MockDateNow(1000)
		defer restoreDate()
		clock, restoreClock := jsclock.MockPerformanceNow(100)
		defer restoreClock()
		recorder := telemetry.NewInMemoryTelemetryContext()
		var id string
		if err := recorder.StartSpan(telemetry.SpanOptions{Name: "timed"}, func(span telemetry.TelemetrySpan) error {
			carrier := recorder.Inject(span)
			id = carrier["pi-memory-scope"] + ":" + carrier["pi-memory-span"]
			clock.Set(110)
			span.AddEvent("event", telemetry.SpanAttributes{telemetry.Attr("count", 1)})
			clock.Set(125)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		var store telemetry.TelemetryStore = recorder // the recorder is the store, as the fixture of store.test.ts:9-13 passes it
		trace, err := store.GetTrace(id)
		if err != nil {
			t.Fatal(err)
		}
		want := &telemetry.StoredTrace{TraceID: id, Spans: []telemetry.StoredSpan{{
			ID: id, TraceID: id, Name: "timed", StartedMs: 1000, EndedMs: 1025,
			Attributes: telemetry.SpanAttributes{},
			Events:     []telemetry.StoredSpanEvent{{Name: "event", TimeMs: 10, Attributes: telemetry.SpanAttributes{telemetry.Attr("count", 1)}}},
			Links:      []telemetry.StoredSpanLink{},
			Status:     telemetry.SpanStatus{Status: telemetry.SpanStatusCodeOK},
		}}}
		if !reflect.DeepEqual(trace, want) {
			t.Fatalf("GetTrace = %+v, want %+v", trace, want)
		}
		if events := recorder.GetSpans()[0].Events; !reflect.DeepEqual(events, []telemetry.RecordedTelemetryEvent{{Name: "event", Attributes: telemetry.SpanAttributes{telemetry.Attr("count", 1)}}}) {
			t.Fatalf("GetSpans events = %+v", events)
		}
	})

	t.Run("maps raw metric measurements to count-one rows without exposing private timing in getMetrics", func(t *testing.T) {
		_, restore := jsclock.MockDateNow(20_001)
		defer restore()
		recorder := telemetry.NewInMemoryTelemetryContext()
		recorder.Counter("calls", telemetry.InstrumentOptions{}).Add(2, nil)
		recorder.Counter("calls", telemetry.InstrumentOptions{}).Add(3, nil)
		var store telemetry.TelemetryStore = recorder
		rows, err := store.FindMetrics(telemetry.MetricQuery{Name: "calls"})
		if err != nil {
			t.Fatal(err)
		}
		var got [][3]float64
		for _, row := range rows {
			got = append(got, [3]float64{row.IntervalMs, row.Count, row.Sum})
		}
		if want := [][3]float64{{20_000, 1, 2}, {20_000, 1, 3}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("rows = %v, want %v", got, want)
		}
		want := []telemetry.RecordedTelemetryMeasurement{
			{Kind: telemetry.TelemetryMetricKindCounter, Name: "calls", Value: 2, Attributes: telemetry.SpanAttributes{}},
			{Kind: telemetry.TelemetryMetricKindCounter, Name: "calls", Value: 3, Attributes: telemetry.SpanAttributes{}},
		}
		if got := recorder.GetMetrics(); !reflect.DeepEqual(got, want) {
			t.Fatalf("GetMetrics = %+v, want %+v", got, want)
		}
	})

	t.Run("detaches link attributes and error details", func(t *testing.T) {
		recorder := telemetry.NewInMemoryTelemetryContext()
		var target telemetry.TelemetrySpan
		if err := recorder.StartSpan(telemetry.SpanOptions{Name: "target"}, func(span telemetry.TelemetrySpan) error {
			target = span
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := recorder.StartSpan(telemetry.SpanOptions{Name: "source", Links: []telemetry.SpanLink{{Context: target, Attributes: telemetry.SpanAttributes{telemetry.Attr("tags", []string{"original"})}}}}, func(span telemetry.TelemetrySpan) error {
			span.SetStatus(telemetry.SpanStatus{Status: telemetry.SpanStatusCodeError, Error: &telemetry.SpanStatusError{Name: "Expected", Message: "original"}})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		var store telemetry.TelemetryStore = recorder
		found, err := store.FindTraces(telemetry.TraceQuery{Name: new("source")})
		if err != nil || len(found.Traces) != 1 {
			t.Fatalf("FindTraces = %+v, %v", found, err)
		}
		trace := found.Traces[0]
		span := trace.Spans[0]
		span.Links[0].Attributes.Set("tags", []string{"changed"})
		if span.Status.Error != nil {
			span.Status.Error.Message = "changed"
		}
		fresh, err := store.GetTrace(trace.TraceID)
		if err != nil || fresh == nil {
			t.Fatalf("GetTrace = %v, %v", fresh, err)
		}
		if got := fresh.Spans[0].Links[0].Attributes.Value("tags"); !reflect.DeepEqual(got, []string{"original"}) {
			t.Fatalf("link tags = %v", got)
		}
		if want := (telemetry.SpanStatus{Status: telemetry.SpanStatusCodeError, Error: &telemetry.SpanStatusError{Name: "Expected", Message: "original"}}); !reflect.DeepEqual(fresh.Spans[0].Status, want) {
			t.Fatalf("status = %+v, want %+v", fresh.Spans[0].Status, want)
		}
	})
}

// The in-memory store reports a measurement's attributes in insertion order; a store that aggregates a series may
// report them in a canonical order, so the shared conformance cases compare metric attribute names only.
func TestInMemoryMetricRowsKeepAttributeInsertionOrder(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	recorder.Counter("ordered", telemetry.InstrumentOptions{}).Add(1, telemetry.SpanAttributes{telemetry.Attr("metric.z", 0), telemetry.Attr("metric.a", 1)})
	rows, err := recorder.FindMetrics(telemetry.MetricQuery{Name: "ordered"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("FindMetrics = %v, %v", rows, err)
	}
	if got := attributeNames(rows[0].Attributes); !reflect.DeepEqual(got, []string{"metric.z", "metric.a"}) {
		t.Fatalf("metric attribute order = %v", got)
	}
}
