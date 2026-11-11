package telemetryingest_test

// pi: packages/chord/src/telemetry-ingest/recorder.ts

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/telemetryingest"
	"github.com/MichaelKinsy/PiG/internal/jsclock"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/test/telemetry-ingest/recorder.test.ts at Pi main c23b9ec1.

func noop(telemetry.TelemetrySpan) error { return nil }

func names(spans []telemetry.StoredSpan) []string {
	var result []string
	for _, span := range spans {
		result = append(result, span.Name)
	}
	return result
}

func TestRecorderKeepsSettledSpansOnlyUnderTheScopeItReadsAtItsFirstSpan(t *testing.T) {
	scope := "early"
	recorder := telemetryingest.NewRecorder(func() string { return scope }, 100)
	scope = "browser:inc"
	gate := make(chan struct{})
	opened := make(chan struct{})
	done := make(chan error)
	go func() {
		done <- recorder.StartSpan(telemetry.SpanOptions{Name: "open"}, func(telemetry.TelemetrySpan) error {
			close(opened)
			<-gate
			return nil
		})
	}()
	<-opened
	var carrier telemetry.TelemetryCarrier
	err := recorder.StartSpan(telemetry.SpanOptions{Name: "root"}, func(span telemetry.TelemetrySpan) error {
		carrier = recorder.Inject(span)
		return span.StartSpan(telemetry.SpanOptions{Name: "child"}, noop)
	})
	if err != nil {
		t.Fatal(err)
	}
	spans := recorder.Take(false, -1).Spans
	if len(spans) != 2 || spans[0].ID != "browser:inc:3" || *spans[0].ParentID != "browser:inc:2" || spans[0].Name != "child" ||
		spans[1].ID != "browser:inc:2" || spans[1].ParentID != nil || spans[1].Name != "root" {
		t.Fatalf("spans = %+v", spans)
	}
	if carrier[telemetryingest.SpanKey] != "browser:inc:2" || carrier[telemetryingest.TraceKey] != "browser:inc:2" || len(carrier) != 2 {
		t.Fatalf("carrier = %v", carrier)
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := names(recorder.Take(false, -1).Spans); !slices.Equal(got, []string{"open"}) {
		t.Fatalf("late = %v", got)
	}
}

func TestRecorderAggregatesMetricsPerSeriesAndTenSecondsTakingOnlyIntervalsThatAreOverUnlessPartial(t *testing.T) {
	now, restore := jsclock.MockDateNow(20_001)
	defer restore()
	recorder := telemetryingest.NewRecorder(func() string { return "s" }, 100)
	counter := recorder.Counter("hits", telemetry.InstrumentOptions{})
	counter.Add(2, telemetry.SpanAttributes{telemetry.Attr("b", 1.0), telemetry.Attr("a", true)})
	counter.Add(3, telemetry.SpanAttributes{telemetry.Attr("a", true), telemetry.Attr("b", 1.0)})
	if metrics := recorder.Take(false, -1).Metrics; len(metrics) != 0 {
		t.Fatalf("early = %+v", metrics)
	}
	now.Set(30_001)
	metrics := recorder.Take(false, -1).Metrics
	if len(metrics) != 1 {
		t.Fatalf("metrics = %+v", metrics)
	}
	got := metrics[0]
	if got.IntervalMs != 20_000 || got.Name != "hits" || got.Kind != telemetry.TelemetryMetricKindCounter || got.Count != 2 || got.Sum != 5 ||
		got.Min != 2 || got.Max != 3 || got.Attributes[0].Name != "a" || got.Attributes[1].Name != "b" {
		t.Fatalf("metric = %+v", got)
	}
	counter.Add(1, nil)
	partial := recorder.Take(true, -1).Metrics
	if len(partial) != 1 || partial[0].IntervalMs != 30_000 || partial[0].Sum != 1 {
		t.Fatalf("partial = %+v", partial)
	}
}

func TestRecorderCountsATakenBatchAgainstItsBoundUntilReleased(t *testing.T) {
	recorder := telemetryingest.NewRecorder(func() string { return "s" }, 2)
	for _, name := range []string{"a", "b"} {
		_ = recorder.StartSpan(telemetry.SpanOptions{Name: name}, noop)
	}
	taken := recorder.Take(false, 1)
	if got := names(taken.Spans); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("taken = %v", got)
	}
	_ = recorder.StartSpan(telemetry.SpanOptions{Name: "c"}, noop)
	if recorder.DroppedSpans() != 1 {
		t.Fatalf("dropped = %d", recorder.DroppedSpans())
	}
	recorder.Release()
	_ = recorder.StartSpan(telemetry.SpanOptions{Name: "d"}, noop)
	if got := names(recorder.Take(false, -1).Spans); !slices.Equal(got, []string{"b", "d"}) {
		t.Fatalf("rest = %v", got)
	}
}

func TestRecorderQueuesIngestedSpansAndMergesIngestedMetricRowsWithinItsBounds(t *testing.T) {
	recorder := telemetryingest.NewRecorder(func() string { return "s" }, 1)
	span := telemetry.StoredSpan{ID: "w:1", TraceID: "w:1", Name: "x", StartedMs: 1, EndedMs: 2, Status: telemetry.SpanStatus{Status: telemetry.SpanStatusCodeOK}}
	other := span
	other.ID = "w:2"
	metric := telemetry.StoredMetric{Name: "m", Kind: telemetry.TelemetryMetricKindCounter, Count: 1, Sum: 2, Min: 2, Max: 2}
	queued := recorder.Ingest(telemetryingest.TelemetryBatch{Spans: []telemetry.StoredSpan{span, other}, Metrics: []telemetry.StoredMetric{metric, metric}})
	if queued != 1 || recorder.DroppedSpans() != 1 {
		t.Fatalf("queued %d dropped %d", queued, recorder.DroppedSpans())
	}
	batch := recorder.Take(true, -1)
	if len(batch.Spans) != 1 || batch.Spans[0].ID != "w:1" || len(batch.Metrics) != 1 || batch.Metrics[0].Count != 2 || batch.Metrics[0].Sum != 4 {
		t.Fatalf("batch = %+v", batch)
	}
}

func TestRecorderRecordsNothingOnceStoppedAndADiscardingStopDropsWhatWaits(t *testing.T) {
	recorder := telemetryingest.NewRecorder(func() string { return "s" }, 10)
	_ = recorder.StartSpan(telemetry.SpanOptions{Name: "kept"}, noop)
	recorder.Stop(false)
	_ = recorder.StartSpan(telemetry.SpanOptions{Name: "late"}, noop)
	recorder.Counter("late", telemetry.InstrumentOptions{}).Add(1, nil)
	batch := recorder.Take(true, -1)
	if got := names(batch.Spans); !slices.Equal(got, []string{"kept"}) || len(batch.Metrics) != 0 {
		t.Fatalf("batch = %+v", batch)
	}
	discarding := telemetryingest.NewRecorder(func() string { return "s" }, 10)
	_ = discarding.StartSpan(telemetry.SpanOptions{Name: "gone"}, noop)
	discarding.Stop(true)
	if batch := discarding.Take(true, -1); len(batch.Spans) != 0 || len(batch.Metrics) != 0 {
		t.Fatalf("batch = %+v", batch)
	}
}
