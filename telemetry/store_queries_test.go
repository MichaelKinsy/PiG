package telemetry_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsclock"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// recordTrace settles one root span at the mocked wall-clock time and returns its trace ID, with the carrier fields the in-memory
// recorder injects as the span's identity (memory.ts: `${scope}:${span}`).
func recordTrace(t *testing.T, recorder *telemetry.InMemoryTelemetryContext, clock *jsclock.Mock, at float64, name string, attributes telemetry.SpanAttributes, body func(span telemetry.TelemetrySpan) error) string {
	t.Helper()
	clock.Set(at)
	var id string
	if err := recorder.StartSpan(telemetry.SpanOptions{Name: name, Attributes: attributes}, func(span telemetry.TelemetrySpan) error {
		carrier := recorder.Inject(span)
		id = carrier["pi-memory-scope"] + ":" + carrier["pi-memory-span"]
		if body != nil {
			return body(span)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func traceIDs(traces []telemetry.StoredTrace) []string {
	ids := make([]string, len(traces))
	for i, trace := range traces {
		ids[i] = trace.TraceID
	}
	return ids
}

// store.test.ts conformance "orders traces newest first and pages them", "filters by name, time, errors and attributes", and GetTrace's
// undefined for an unknown trace, asserted through the TelemetryStore the recorder is.
func TestInMemoryFindTracesFiltersOrdersAndPagesSettledTraces(t *testing.T) {
	clock, restore := jsclock.MockDateNow(0)
	defer restore()
	recorder := telemetry.NewInMemoryTelemetryContext()
	var store telemetry.TelemetryStore = recorder

	oldest := recordTrace(t, recorder, clock, 1000, "a", telemetry.SpanAttributes{telemetry.Attr("kind", "x")}, nil)
	failed := recordTrace(t, recorder, clock, 2000, "b", nil, func(span telemetry.TelemetrySpan) error {
		span.SetStatus(telemetry.SpanStatus{Status: telemetry.SpanStatusCodeError, Error: &telemetry.SpanStatusError{Name: "Boom", Message: "failed"}})
		return nil
	})
	tieOne := recordTrace(t, recorder, clock, 3000, "a", telemetry.SpanAttributes{telemetry.Attr("n", 1)}, nil)
	tieTwo := recordTrace(t, recorder, clock, 3000, "a", nil, nil)
	nested := recordTrace(t, recorder, clock, 4000, "c", nil, func(span telemetry.TelemetrySpan) error {
		return span.StartSpan(telemetry.SpanOptions{Name: "child", Attributes: telemetry.SpanAttributes{telemetry.Attr("route", "/x")}}, func(telemetry.TelemetrySpan) error { return nil })
	})
	ties := []string{tieOne, tieTwo}
	slices.Sort(ties)

	find := func(query telemetry.TraceQuery) telemetry.FindTracesResult {
		t.Helper()
		result, err := store.FindTraces(query)
		if err != nil {
			t.Fatalf("FindTraces(%+v): %v", query, err)
		}
		return result
	}
	check := func(what string, query telemetry.TraceQuery, want ...string) {
		t.Helper()
		if got := traceIDs(find(query).Traces); !slices.Equal(got, want) {
			t.Errorf("%s: traces = %v, want %v", what, got, want)
		}
	}

	check("newest first, equal starts by trace ID", telemetry.TraceQuery{}, nested, ties[0], ties[1], failed, oldest)
	check("name of the earliest span", telemetry.TraceQuery{Name: new("a")}, ties[0], ties[1], oldest)
	check("child span names do not name the trace", telemetry.TraceQuery{Name: new("child")})
	check("inclusive lower start bound", telemetry.TraceQuery{StartMin: new(3000.0)}, nested, ties[0], ties[1])
	check("inclusive upper start bound", telemetry.TraceQuery{StartMax: new(2000.0)}, failed, oldest)
	check("start window", telemetry.TraceQuery{StartMin: new(2000.0), StartMax: new(3000.0)}, ties[0], ties[1], failed)
	check("errors keeps traces with an error span", telemetry.TraceQuery{Errors: true}, failed)
	check("an attribute on a child span matches its trace", telemetry.TraceQuery{Attributes: map[string]telemetry.AttributeValue{"route": "/x"}}, nested)
	check("numbers compare by value whatever their Go type", telemetry.TraceQuery{Attributes: map[string]telemetry.AttributeValue{"n": float64(1)}}, tieOne)
	check("a string never equals a number", telemetry.TraceQuery{Attributes: map[string]telemetry.AttributeValue{"n": "1"}})
	check("every attribute must match one span", telemetry.TraceQuery{Attributes: map[string]telemetry.AttributeValue{"kind": "x", "n": 1}})
	check("filters combine", telemetry.TraceQuery{Name: new("a"), StartMin: new(2000.0), Attributes: map[string]telemetry.AttributeValue{"n": 1}}, tieOne)

	first := find(telemetry.TraceQuery{Limit: new(2)})
	if got := traceIDs(first.Traces); !slices.Equal(got, []string{nested, ties[0]}) || first.Cursor == nil {
		t.Fatalf("first page = %v cursor %v, want [%s %s] and a cursor", got, first.Cursor, nested, ties[0])
	}
	second := find(telemetry.TraceQuery{Limit: new(2), Cursor: first.Cursor})
	if got := traceIDs(second.Traces); !slices.Equal(got, []string{ties[1], failed}) || second.Cursor == nil {
		t.Fatalf("second page = %v cursor %v, want [%s %s] and a cursor", got, second.Cursor, ties[1], failed)
	}
	last := find(telemetry.TraceQuery{Limit: new(2), Cursor: second.Cursor})
	if got := traceIDs(last.Traces); !slices.Equal(got, []string{oldest}) || last.Cursor != nil {
		t.Fatalf("last page = %v cursor %v, want [%s] and no cursor", got, last.Cursor, oldest)
	}
	if exact := find(telemetry.TraceQuery{Limit: new(5)}); len(exact.Traces) != 5 || exact.Cursor != nil {
		t.Fatalf("a page that holds every trace has no cursor: %d traces, cursor %v", len(exact.Traces), exact.Cursor)
	}
	filtered := find(telemetry.TraceQuery{Name: new("a"), Limit: new(1)})
	if got := traceIDs(filtered.Traces); !slices.Equal(got, []string{ties[0]}) || filtered.Cursor == nil {
		t.Fatalf("filtered first page = %v cursor %v", got, filtered.Cursor)
	}
	if rest := find(telemetry.TraceQuery{Name: new("a"), Limit: new(5), Cursor: filtered.Cursor}); !slices.Equal(traceIDs(rest.Traces), []string{ties[1], oldest}) {
		t.Fatalf("filtered continuation = %v, want the same filters retained", traceIDs(rest.Traces))
	}
	if _, err := store.FindTraces(telemetry.TraceQuery{Cursor: new("not a cursor")}); err == nil {
		t.Fatal("a cursor that is not one the store issued must be an error")
	}

	trace, err := store.GetTrace(nested)
	if err != nil || trace == nil || trace.TraceID != nested || len(trace.Spans) != 2 || trace.Spans[0].Name != "c" || trace.Spans[1].Name != "child" {
		t.Fatalf("GetTrace(nested) = %+v, %v, want the root and child spans in start order", trace, err)
	}
	if missing, err := store.GetTrace("no-such-trace"); err != nil || missing != nil {
		t.Fatalf("GetTrace of an unknown trace = %+v, %v, want nil, nil", missing, err)
	}
}

// store.test.ts: FindMetrics returns each raw measurement as a count-one row at the start of its ten-second interval, filtered by name,
// inclusive interval bounds and attributes, in recording order.
func TestInMemoryFindMetricsFiltersByNameIntervalAndAttributes(t *testing.T) {
	clock, restore := jsclock.MockDateNow(5_000)
	defer restore()
	recorder := telemetry.NewInMemoryTelemetryContext()
	var store telemetry.TelemetryStore = recorder
	hits := recorder.Counter("hits", telemetry.InstrumentOptions{Unit: new("1")})
	hits.Add(2, telemetry.SpanAttributes{telemetry.Attr("route", "/a")})
	clock.Set(20_001)
	hits.Add(3, telemetry.SpanAttributes{telemetry.Attr("route", "/b")})
	recorder.Counter("misses", telemetry.InstrumentOptions{}).Add(7, nil)
	clock.Set(39_999)
	hits.Add(4, telemetry.SpanAttributes{telemetry.Attr("route", "/a"), telemetry.Attr("code", 200)})

	rows := func(query telemetry.MetricQuery) [][4]float64 {
		t.Helper()
		found, err := store.FindMetrics(query)
		if err != nil {
			t.Fatalf("FindMetrics(%+v): %v", query, err)
		}
		var got [][4]float64
		for _, row := range found {
			if row.Name != query.Name || row.Kind != telemetry.TelemetryMetricKindCounter || row.Count != 1 || row.Min != row.Sum || row.Max != row.Sum {
				t.Fatalf("row %+v is not a count-one counter row of %q", row, query.Name)
			}
			got = append(got, [4]float64{row.IntervalMs, row.Sum, row.Count, float64(len(row.Attributes))})
		}
		return got
	}
	check := func(what string, query telemetry.MetricQuery, want [][4]float64) {
		t.Helper()
		if got := rows(query); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: rows = %v, want %v", what, got, want)
		}
	}
	check("name only, in recording order", telemetry.MetricQuery{Name: "hits"}, [][4]float64{{0, 2, 1, 1}, {20_000, 3, 1, 1}, {30_000, 4, 1, 2}})
	check("another name", telemetry.MetricQuery{Name: "misses"}, [][4]float64{{20_000, 7, 1, 0}})
	check("unknown name", telemetry.MetricQuery{Name: "none"}, nil)
	check("inclusive lower interval bound", telemetry.MetricQuery{Name: "hits", StartMin: new(20_000.0)}, [][4]float64{{20_000, 3, 1, 1}, {30_000, 4, 1, 2}})
	check("inclusive upper interval bound", telemetry.MetricQuery{Name: "hits", StartMax: new(20_000.0)}, [][4]float64{{0, 2, 1, 1}, {20_000, 3, 1, 1}})
	check("a bound inside an interval excludes it", telemetry.MetricQuery{Name: "hits", StartMin: new(20_001.0)}, [][4]float64{{30_000, 4, 1, 2}})
	check("attribute equality", telemetry.MetricQuery{Name: "hits", Attributes: map[string]telemetry.AttributeValue{"route": "/a"}}, [][4]float64{{0, 2, 1, 1}, {30_000, 4, 1, 2}})
	check("number attribute compares by value", telemetry.MetricQuery{Name: "hits", Attributes: map[string]telemetry.AttributeValue{"code": float64(200)}}, [][4]float64{{30_000, 4, 1, 2}})
	check("an absent attribute matches nothing", telemetry.MetricQuery{Name: "hits", Attributes: map[string]telemetry.AttributeValue{"code": 404}}, nil)

	found, err := store.FindMetrics(telemetry.MetricQuery{Name: "hits"})
	if err != nil || len(found) != 3 || found[0].Unit == nil || *found[0].Unit != "1" {
		t.Fatalf("FindMetrics unit = %+v, %v, want the instrument's unit", found, err)
	}
	if none, _ := store.FindMetrics(telemetry.MetricQuery{Name: "misses"}); len(none) != 1 || none[0].Unit != nil {
		t.Fatalf("an instrument without a unit reports none: %+v", none)
	}
}
