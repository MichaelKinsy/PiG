package telemetrysqlite

// Ports packages/chord/test/telemetry-sqlite.test.ts.

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/telemetry"
	"github.com/MichaelKinsy/PiG/telemetry/telemetrytest"
)

// sqliteConformanceFixture is upstream's fixture(): getSpans flushes, then reads the committed rows back as normalized
// recorded spans.
func sqliteConformanceFixture(ctx context.Context) (telemetrytest.TelemetryAdapterFixture, error) {
	db, err := openMemoryDatabase()
	if err != nil {
		return telemetrytest.TelemetryAdapterFixture{}, err
	}
	recorder, err := CreateSqliteTelemetry(db, SqliteTelemetryOptions{})
	if err != nil {
		return telemetrytest.TelemetryAdapterFixture{}, errors.Join(err, db.Close())
	}
	return telemetrytest.TelemetryAdapterFixture{
		Context:     recorder,
		Propagation: recorder,
		Metrics:     recorder,
		GetSpans: func(context.Context) ([]telemetry.RecordedTelemetrySpan, error) {
			if err := recorder.Flush(); err != nil {
				return nil, err
			}
			return recordedSpans(db)
		},
		Dispose: func(context.Context) error {
			return errors.Join(recorder.Close(), db.Close())
		},
	}, nil
}

func carrierOf(id, traceID string) telemetry.TelemetryCarrier {
	return telemetry.TelemetryCarrier{"pi-sqlite-span": id, "pi-sqlite-trace": traceID}
}

func recordedSpans(db chordsqlite.SqliteDatabase) ([]telemetry.RecordedTelemetrySpan, error) {
	rows, err := db.All("SELECT rowid, * FROM telemetry_spans")
	if err != nil {
		return nil, err
	}
	var spans []telemetry.RecordedTelemetrySpan
	for _, row := range rows {
		decoded, err := readSpanRow(row)
		if err != nil {
			return nil, err
		}
		stored, err := storedSpan(decoded)
		if err != nil {
			return nil, err
		}
		scope, number, _ := strings.Cut(stored.ID, ":")
		id, err := strconv.Atoi(number)
		if err != nil {
			return nil, err
		}
		recorded := telemetry.RecordedTelemetrySpan{
			ID: id, Context: carrierOf(stored.ID, stored.TraceID), Name: stored.Name, Attributes: integralNumbers(stored.Attributes),
			Events: make([]telemetry.RecordedTelemetryEvent, len(stored.Events)),
			Links:  make([]telemetry.RecordedTelemetryLink, len(stored.Links)), Status: stored.Status, Settled: true,
		}
		if stored.ParentID != nil {
			parentScope, parentNumber, _ := strings.Cut(*stored.ParentID, ":")
			recorded.ParentContext = carrierOf(*stored.ParentID, stored.TraceID)
			if parentScope == scope {
				parent, err := strconv.Atoi(parentNumber)
				if err != nil {
					return nil, err
				}
				recorded.ParentID = &parent
			}
		}
		for i, event := range stored.Events {
			recorded.Events[i] = telemetry.RecordedTelemetryEvent{Name: event.Name, Attributes: integralNumbers(event.Attributes)}
		}
		for i, link := range stored.Links {
			recorded.Links[i] = telemetry.RecordedTelemetryLink{Context: carrierOf(link.SpanID, link.TraceID), Attributes: integralNumbers(link.Attributes)}
		}
		endSequence, err := rowNumber(row, "rowid")
		if err != nil {
			return nil, err
		}
		sequence := int(endSequence) //portlint:allow numbers rowid is a positive integer
		recorded.EndSequence = &sequence
		spans = append(spans, recorded)
	}
	slicesSortSpans(spans)
	return spans, nil
}

// TestSqliteTelemetryConformance ports "SQLite telemetry span and propagation conformance": every adapter case except
// the metrics group.
// Pi source: packages/chord/src/telemetry-sqlite/index.ts
func TestSqliteTelemetryConformance(t *testing.T) {
	cases := telemetrytest.CreateTelemetryAdapterConformance(sqliteConformanceFixture)
	ran := 0
	for _, testCase := range cases {
		if testCase.Group == "metrics" {
			continue
		}
		ran++
		t.Run(testCase.Group+": "+testCase.Name, func(t *testing.T) {
			// upstream: packages/chord/test/telemetry-sqlite.test.ts:104
			if err := testCase.Run(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
	if ran == 0 {
		t.Fatal("conformance suite is empty")
	}
}

type eventRow struct {
	Name       string          `json:"name"`
	TimeMs     float64         `json:"time_ms"`
	Attributes json.RawMessage `json:"attributes"`
}

// Upstream: "does not execute SQL while recording and stores only settled spans".
func TestDoesNotExecuteSQLWhileRecordingAndStoresOnlySettledSpans(t *testing.T) {
	f := newFixture(t, SqliteTelemetryOptions{})
	gate := make(chan struct{})
	entered := make(chan telemetry.TelemetrySpan)
	pending := start(func() error {
		return f.telemetry.StartSpan(telemetry.SpanOptions{Name: "active"}, func(span telemetry.TelemetrySpan) error {
			span.AddEvent("begin", nil)
			f.telemetry.Counter("calls", telemetry.InstrumentOptions{}).Add(1, nil)
			entered <- span
			<-gate
			return nil
		})
	})
	active := <-entered
	if got := f.db.calls.Load(); got != 0 {
		t.Fatalf("transactions while recording = %d, want 0", got)
	}
	f.flush()
	if rows := f.all("SELECT * FROM telemetry_spans"); len(rows) != 0 {
		t.Fatalf("rows for an active span = %v", rows)
	}
	close(gate)
	check(t, pending.wait(t))
	if got := f.db.calls.Load(); got != 1 {
		t.Fatalf("transactions = %d, want 1", got)
	}
	active.AddEvent("late", nil)
	f.flush()
	rows := f.all("SELECT * FROM telemetry_spans")
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want one", rows)
	}
	if rowNumberOf(t, rows[0], "ended_ms") < rowNumberOf(t, rows[0], "started_ms") {
		t.Fatalf("ended_ms %v < started_ms %v", rows[0]["ended_ms"], rows[0]["started_ms"])
	}
	var events []eventRow
	check(t, json.Unmarshal([]byte(rows[0]["events"].(string)), &events))
	if len(events) != 1 || events[0].Name != "begin" || events[0].TimeMs < 0 || string(events[0].Attributes) != "{}" {
		t.Fatalf("events = %s, want [{begin, time_ms, {}}]", rows[0]["events"])
	}
}

// Upstream: "aggregates canonical series by interval and writes partial intervals only on close".
func TestAggregatesCanonicalSeriesByIntervalAndWritesPartialIntervalsOnlyOnClose(t *testing.T) {
	now := mockDate(t, 20_001)
	f := newFixture(t, SqliteTelemetryOptions{})
	unit := "ms"
	metric := f.telemetry.Histogram("duration", telemetry.HistogramOptions{InstrumentOptions: telemetry.InstrumentOptions{Unit: &unit}, Boundaries: []float64{1, 10}})
	original := []string{"original"}
	attributes := telemetry.SpanAttributes{telemetry.Attr("b", original), telemetry.Attr("a", true)}
	metric.Record(2, attributes, nil)
	original[0] = "changed"
	metric.Record(8, telemetry.SpanAttributes{telemetry.Attr("a", true), telemetry.Attr("b", []string{"original"})}, nil)
	f.telemetry.UpDownCounter("active", telemetry.InstrumentOptions{}).Add(-1, nil)
	f.flush()
	if rows := f.all("SELECT * FROM telemetry_metrics"); len(rows) != 0 {
		t.Fatalf("rows before the interval completes = %v", rows)
	}
	now.Set(30_001)
	metric.Record(4, telemetry.SpanAttributes{telemetry.Attr("b", []string{"original"}), telemetry.Attr("a", true)}, nil)
	f.flush()
	rows := f.all("SELECT * FROM telemetry_metrics ORDER BY name")
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want 2", rows)
	}
	want := []map[string]any{
		{"name": "active", "kind": "upDownCounter", "unit": nil, "count": int64(1), "sum": -1.0, "min": -1.0, "max": -1.0},
		{"interval_ms": int64(20_000), "name": "duration", "kind": "histogram", "unit": "ms", "attributes": `{"a":true,"b":["original"]}`, "count": int64(2), "sum": 10.0, "min": 2.0, "max": 8.0},
	}
	for i, expected := range want {
		for column, value := range expected {
			if got := rows[i][column]; got != value {
				t.Fatalf("row %d %s = %#v, want %#v", i, column, got, value)
			}
		}
	}
	check(t, f.telemetry.Close())
	row, err := f.db.Get("SELECT count, sum FROM telemetry_metrics WHERE interval_ms = 30000")
	check(t, err)
	if row["count"] != int64(1) || row["sum"] != 4.0 {
		t.Fatalf("partial interval row = %v, want count 1 sum 4", row)
	}
}

// Upstream: "separates kind, name, unit, and caller attributes without retaining descriptions or boundaries".
func TestSeparatesKindNameUnitAndCallerAttributes(t *testing.T) {
	mockDate(t, 20_001)
	f := newFixture(t, SqliteTelemetryOptions{})
	by := "By"
	first, second := "first", "second"
	f.telemetry.Counter("same", telemetry.InstrumentOptions{}).Add(1, nil)
	f.telemetry.Counter("same", telemetry.InstrumentOptions{Unit: &by}).Add(2, nil)
	f.telemetry.UpDownCounter("same", telemetry.InstrumentOptions{}).Add(3, nil)
	f.telemetry.Counter("other", telemetry.InstrumentOptions{}).Add(4, nil)
	f.telemetry.Counter("same", telemetry.InstrumentOptions{}).Add(5, telemetry.SpanAttributes{telemetry.Attr("method", "read")})
	f.telemetry.Histogram("same", telemetry.HistogramOptions{InstrumentOptions: telemetry.InstrumentOptions{Description: &first}, Boundaries: []float64{1}}).Record(6, nil, nil)
	f.telemetry.Histogram("same", telemetry.HistogramOptions{InstrumentOptions: telemetry.InstrumentOptions{Description: &second}, Boundaries: []float64{2}}).Record(7, nil, nil)
	check(t, f.telemetry.Close())
	rows := f.all("SELECT * FROM telemetry_metrics")
	if len(rows) != 6 {
		t.Fatalf("series = %d, want 6", len(rows))
	}
	for _, row := range rows {
		if row["kind"] == "histogram" && (row["count"] != int64(2) || row["sum"] != 13.0 || row["min"] != 6.0 || row["max"] != 7.0) {
			t.Fatalf("histogram row = %v, want count 2 sum 13 min 6 max 7", row)
		}
	}
}

// Upstream: "strictly bounds queued spans including the in-flight batch and bounds pending metric series".
func TestStrictlyBoundsQueuedSpansIncludingTheInFlightBatch(t *testing.T) {
	mockDate(t, 20_001)
	capacity := 1
	f := newFixture(t, SqliteTelemetryOptions{MaxQueuedSpans: &capacity})
	f.span("first")
	f.telemetry.Counter("first", telemetry.InstrumentOptions{}).Add(1, nil)
	f.telemetry.Counter("first", telemetry.InstrumentOptions{}).Add(2, nil)
	f.telemetry.Counter("overflow", telemetry.InstrumentOptions{}).Add(3, nil)
	gate := newBlocker(t)
	f.db.setHook(gate.hook)
	flushing := start(f.telemetry.Flush)
	gate.waitEntered(t)
	f.span("overflow")
	if got := f.telemetry.DroppedSpans(); got != 1 {
		t.Fatalf("droppedSpans = %d, want 1", got)
	}
	if got := f.telemetry.DroppedMeasurements(); got != 1 {
		t.Fatalf("droppedMeasurements = %d, want 1", got)
	}
	gate.open()
	check(t, flushing.wait(t))
	f.span("after")
	check(t, f.telemetry.Close())
	if got := f.names("SELECT name FROM telemetry_spans ORDER BY rowid"); strings.Join(got, ",") != "first,after" {
		t.Fatalf("spans = %v, want [first after]", got)
	}
	rows := f.all("SELECT name, count, sum FROM telemetry_metrics")
	if len(rows) != 1 || rows[0]["name"] != "first" || rows[0]["count"] != int64(2) || rows[0]["sum"] != 3.0 {
		t.Fatalf("metrics = %v, want [{first 2 3}]", rows)
	}
}

// Upstream: "counts in-flight metric intervals against capacity until commit".
func TestCountsInFlightMetricIntervalsAgainstCapacityUntilCommit(t *testing.T) {
	now := mockDate(t, 20_001)
	capacity := 1
	f := newFixture(t, SqliteTelemetryOptions{MaxQueuedSpans: &capacity})
	f.telemetry.Counter("calls", telemetry.InstrumentOptions{}).Add(1, nil)
	now.Set(30_001)
	gate := newBlocker(t)
	f.db.setHook(gate.hook)
	flushing := start(f.telemetry.Flush)
	gate.waitEntered(t)
	f.telemetry.Counter("calls", telemetry.InstrumentOptions{}).Add(2, nil)
	if got := f.telemetry.DroppedMeasurements(); got != 1 {
		t.Fatalf("droppedMeasurements = %d, want 1", got)
	}
	gate.open()
	check(t, flushing.wait(t))
	f.telemetry.Counter("calls", telemetry.InstrumentOptions{}).Add(3, nil)
	check(t, f.telemetry.Close())
	rows := f.all("SELECT interval_ms, sum FROM telemetry_metrics ORDER BY interval_ms")
	if len(rows) != 2 || rows[0]["interval_ms"] != int64(20_000) || rows[0]["sum"] != 1.0 || rows[1]["interval_ms"] != int64(30_000) || rows[1]["sum"] != 3.0 {
		t.Fatalf("metrics = %v, want 20000:1 and 30000:3", rows)
	}
}

// Upstream: "flushes once per second and retains only rows inside the retention window". The fake timer is the
// fakeTicks seam; each tick is one advanceTimersByTimeAsync(1000).
func TestFlushesOncePerSecondAndRetainsOnlyRowsInsideTheRetentionWindow(t *testing.T) {
	now := mockDate(t, 20_001)
	ticks := newFakeTicks()
	retention := 20_000.0
	f := newFixture(t, ticks.options(SqliteTelemetryOptions{RetentionMs: &retention}))
	f.span("old")
	f.telemetry.Counter("old", telemetry.InstrumentOptions{}).Add(1, nil)
	if !ticks.fire(10 * time.Second) {
		t.Fatal("the background flush did not take the tick")
	}
	waitFor(t, "the old span to be flushed", func() bool { return strings.Join(f.names("SELECT name FROM telemetry_spans"), ",") == "old" })
	now.Set(50_001)
	f.span("recent")
	f.telemetry.Counter("recent", telemetry.InstrumentOptions{}).Add(1, nil)
	if !ticks.fire(10 * time.Second) {
		t.Fatal("the background flush did not take the second tick")
	}
	waitFor(t, "retention to remove the old span", func() bool { return strings.Join(f.names("SELECT name FROM telemetry_spans"), ",") == "recent" })
	if got := f.names("SELECT name FROM telemetry_metrics"); len(got) != 0 {
		t.Fatalf("metrics after retention = %v, want none", got)
	}
	check(t, f.telemetry.Close())
	if got := f.names("SELECT name FROM telemetry_metrics"); strings.Join(got, ",") != "recent" {
		t.Fatalf("metrics after close = %v, want [recent]", got)
	}
}

// Upstream: "serializes concurrent flush and close without losing measurements recorded during writes".
func TestSerializesConcurrentFlushAndCloseWithoutLosingMeasurements(t *testing.T) {
	now := mockDate(t, 20_001)
	f := newFixture(t, SqliteTelemetryOptions{})
	f.telemetry.Counter("calls", telemetry.InstrumentOptions{}).Add(1, nil)
	now.Set(30_001)
	var active, maximum atomic.Int32
	gate := newBlocker(t)
	f.db.setHook(func(callback func(chordsqlite.SqliteExecutor) error, original func(func(chordsqlite.SqliteExecutor) error) error) error {
		current := active.Add(1)
		for {
			seen := maximum.Load()
			if current <= seen || maximum.CompareAndSwap(seen, current) {
				break
			}
		}
		defer active.Add(-1)
		return gate.hook(callback, original)
	})
	first := start(f.telemetry.Flush)
	gate.waitEntered(t)
	f.telemetry.Counter("calls", telemetry.InstrumentOptions{}).Add(2, nil)
	second := start(f.telemetry.Flush)
	closing := start(f.telemetry.Close)
	closingAgain := start(f.telemetry.Close)
	gate.open()
	for _, operation := range []*future{first, second, closing, closingAgain} {
		check(t, operation.wait(t))
	}
	if got := maximum.Load(); got != 1 {
		t.Fatalf("concurrent transactions = %d, want 1", got)
	}
	rows := f.all("SELECT interval_ms, sum FROM telemetry_metrics ORDER BY interval_ms")
	if len(rows) != 2 || rows[0]["interval_ms"] != int64(20_000) || rows[0]["sum"] != 1.0 || rows[1]["interval_ms"] != int64(30_000) || rows[1]["sum"] != 2.0 {
		t.Fatalf("metrics = %v, want 20000:1 and 30000:2", rows)
	}
}

// Upstream: "close does not wait for active business callbacks, ignores late completions, and leaves the database open".
func TestCloseDoesNotWaitForActiveCallbacksAndLeavesTheDatabaseOpen(t *testing.T) {
	f := newFixture(t, SqliteTelemetryOptions{})
	release := make(chan struct{})
	entered := make(chan telemetry.TelemetrySpan)
	pending := start(func() error {
		return f.telemetry.StartSpan(telemetry.SpanOptions{Name: "active"}, func(span telemetry.TelemetrySpan) error {
			entered <- span
			<-release
			return nil
		})
	})
	active := <-entered
	f.span("settled")
	check(t, f.telemetry.Close())
	if got := f.names("SELECT name FROM telemetry_spans"); strings.Join(got, ",") != "settled" {
		t.Fatalf("spans after close = %v, want [settled]", got)
	}
	active.AddEvent("late", nil)
	ran := false
	check(t, active.StartSpan(telemetry.SpanOptions{Name: "late-child"}, func(telemetry.TelemetrySpan) error { ran = true; return nil }))
	if !ran {
		t.Fatal("a child of an active span after close must still run its callback")
	}
	f.telemetry.Counter("late", telemetry.InstrumentOptions{}).Add(1, nil)
	close(release)
	check(t, pending.wait(t))
	f.flush()
	if got := f.names("SELECT name FROM telemetry_spans"); strings.Join(got, ",") != "settled" {
		t.Fatalf("spans after late completion = %v, want [settled]", got)
	}
	if rows := f.all("SELECT * FROM telemetry_metrics"); len(rows) != 0 {
		t.Fatalf("late metrics = %v", rows)
	}
}

// Upstream: "stops permanently after a write failure and reports the identical error without retry".
func TestStopsPermanentlyAfterAWriteFailureAndReportsTheIdenticalError(t *testing.T) {
	f := newFixture(t, SqliteTelemetryOptions{})
	f.span("queued")
	failure := errors.New("write failure")
	f.db.setHook(func(func(chordsqlite.SqliteExecutor) error, func(func(chordsqlite.SqliteExecutor) error) error) error {
		return failure
	})
	if err := f.telemetry.Flush(); err != failure { //nolint:errorlint // identity: the original error, not a wrapper
		t.Fatalf("flush error = %v, want the write failure itself", err)
	}
	result, err := telemetry.StartSpan(f.telemetry, telemetry.SpanOptions{Name: "ignored"}, func(telemetry.TelemetrySpan) (int, error) { return 1, nil })
	if err != nil || result != 1 {
		t.Fatalf("an ignored span returned %d, %v, want 1, nil", result, err)
	}
	f.telemetry.Counter("ignored", telemetry.InstrumentOptions{}).Add(1, nil)
	if err := f.telemetry.Flush(); err != failure { //nolint:errorlint // identity
		t.Fatalf("second flush error = %v, want the write failure itself", err)
	}
	closing := start(f.telemetry.Close)
	closingAgain := start(f.telemetry.Close)
	if err := closing.wait(t); err != failure { //nolint:errorlint // identity
		t.Fatalf("close error = %v, want the write failure itself", err)
	}
	if err := closingAgain.wait(t); err != failure { //nolint:errorlint // identity
		t.Fatalf("second close error = %v, want the write failure itself", err)
	}
	if got := f.db.calls.Load(); got != 1 {
		t.Fatalf("transactions = %d, want 1 (no retry)", got)
	}
	if rows := f.all("SELECT * FROM telemetry_spans"); len(rows) != 0 {
		t.Fatalf("spans = %v, want none", rows)
	}
}

// Upstream: "remembers an undefined background write failure and stops its timer". A Go error is never nil, so the
// remembered failure is a sentinel error; the timer goroutine must take no further tick after the failure.
func TestRemembersABackgroundWriteFailureAndStopsItsTimer(t *testing.T) {
	ticks := newFakeTicks()
	f := newFixture(t, ticks.options(SqliteTelemetryOptions{}))
	failure := errors.New("background write failure")
	f.db.setHook(func(func(chordsqlite.SqliteExecutor) error, func(func(chordsqlite.SqliteExecutor) error) error) error {
		return failure
	})
	if !ticks.fire(10 * time.Second) {
		t.Fatal("the background flush did not take the tick")
	}
	waitFor(t, "the background write", func() bool { return f.db.calls.Load() == 1 })
	if err := f.telemetry.Flush(); err != failure { //nolint:errorlint // identity
		t.Fatalf("flush error = %v, want the background failure", err)
	}
	if err := f.telemetry.Close(); err != failure { //nolint:errorlint // identity
		t.Fatalf("close error = %v, want the background failure", err)
	}
	if ticks.fire(50 * time.Millisecond) {
		t.Fatal("the timer goroutine took a tick after the failure")
	}
	if !ticks.stopped.Load() {
		t.Fatal("the timer was not stopped")
	}
	if got := f.db.calls.Load(); got != 1 {
		t.Fatalf("transactions = %d, want 1", got)
	}
}

// Upstream: "rolls back spans and metric aggregates together on SQL failure".
func TestRollsBackSpansAndMetricAggregatesTogetherOnSQLFailure(t *testing.T) {
	f := newFixture(t, SqliteTelemetryOptions{})
	f.span("rollback")
	f.telemetry.Counter("rollback", telemetry.InstrumentOptions{}).Add(1, nil)
	check(t, f.db.Exec("DROP TABLE telemetry_metrics"))
	if err := f.telemetry.Close(); err == nil {
		t.Fatal("close succeeded, want the SQL failure")
	}
	if rows := f.all("SELECT * FROM telemetry_spans"); len(rows) != 0 {
		t.Fatalf("spans = %v, want rolled back", rows)
	}
}

// Upstream: "reuses migrations, preserves identities across reopen, and merges aggregates from multiple adapters".
func TestReusesMigrationsPreservesIdentitiesAcrossReopenAndMergesAggregates(t *testing.T) {
	mockDate(t, 20_001)
	db := openMemory(t)
	t.Cleanup(func() { check(t, db.Close()) })
	first, err := CreateSqliteTelemetry(db, SqliteTelemetryOptions{})
	check(t, err)
	var carrier telemetry.TelemetryCarrier
	check(t, first.StartSpan(telemetry.SpanOptions{Name: "parent"}, func(span telemetry.TelemetrySpan) error {
		carrier = first.Inject(span)
		return nil
	}))
	first.Counter("shared", telemetry.InstrumentOptions{}).Add(2, nil)
	check(t, first.Close())
	second, err := CreateSqliteTelemetry(db, SqliteTelemetryOptions{})
	check(t, err)
	check(t, second.Extract(carrier).StartSpan(telemetry.SpanOptions{Name: "child"}, func(telemetry.TelemetrySpan) error { return nil }))
	second.Counter("shared", telemetry.InstrumentOptions{}).Add(4, nil)
	check(t, second.Close())
	rows, err := db.All("SELECT * FROM telemetry_spans ORDER BY rowid")
	check(t, err)
	if len(rows) != 2 {
		t.Fatalf("spans = %v, want 2", rows)
	}
	if rows[1]["parent_id"] != rows[0]["id"] || rows[1]["trace_id"] != rows[0]["trace_id"] || rows[0]["trace_id"] != rows[0]["id"] || rows[1]["id"] == rows[0]["id"] {
		t.Fatalf("identities = %v", rows)
	}
	metrics, err := db.All("SELECT count, sum, min, max FROM telemetry_metrics")
	check(t, err)
	if len(metrics) != 1 || metrics[0]["count"] != int64(2) || metrics[0]["sum"] != 6.0 || metrics[0]["min"] != 2.0 || metrics[0]["max"] != 4.0 {
		t.Fatalf("metrics = %v, want one merged row {2 6 2 4}", metrics)
	}
}

// integralNumbers is the fixture's normalization of JSON numbers: the conformance cases record Go ints, and a JSON
// number reads back as a float64, so a whole float64 (and each element of a float64 slice) becomes an int, the one
// JavaScript number they share.
func integralNumbers(attributes telemetry.SpanAttributes) telemetry.SpanAttributes {
	if attributes == nil {
		return nil
	}
	normalized := make(telemetry.SpanAttributes, len(attributes))
	for index, attribute := range attributes {
		value := attribute.Value
		switch typed := value.(type) {
		case float64:
			if typed == math.Trunc(typed) {
				value = int(typed) //portlint:allow numbers guarded by the whole-number check
			}
		case []float64:
			whole := make([]int, len(typed))
			for i, element := range typed {
				if element != math.Trunc(element) {
					whole = nil
					break
				}
				whole[i] = int(element) //portlint:allow numbers guarded by the whole-number check
			}
			if whole != nil {
				value = whole
			}
		}
		normalized[index] = telemetry.Attr(attribute.Name, value)
	}
	return normalized
}
