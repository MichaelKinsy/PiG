package telemetrysqlite

// Ports packages/chord/test/telemetry-sqlite.test.ts (the slice 9 additions) and telemetry-sqlite-node.test.ts at Pi main
// c23b9ec1: span times in whole microseconds, a version 1 store migrated, ingested spans and metric rows, and the
// read-only store.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/telemetryingest"
	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
	"github.com/MichaelKinsy/PiG/internal/jsclock"
	"github.com/MichaelKinsy/PiG/telemetry"
)

func TestOrdersSpansStartedInTheSameMillisecondAsTheyStarted(t *testing.T) {
	mockDate(t, 20_001)
	f := newFixture(t, SqliteTelemetryOptions{})
	check(t, f.telemetry.StartSpan(telemetry.SpanOptions{Name: "root"}, func(span telemetry.TelemetrySpan) error {
		for index := range 11 {
			if err := span.StartSpan(telemetry.SpanOptions{Name: fmt.Sprintf("child %d", index)}, func(telemetry.TelemetrySpan) error { return nil }); err != nil {
				return err
			}
		}
		return nil
	}))
	f.flush()
	name := "root"
	result, err := f.telemetry.FindTraces(telemetry.TraceQuery{Name: &name})
	check(t, err)
	want := []string{"root"}
	for index := range 11 {
		want = append(want, fmt.Sprintf("child %d", index))
	}
	var got []string
	for _, span := range result.Traces[0].Spans {
		got = append(got, span.Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v", got)
	}
}

func TestStoresSpanTimesInWholeMicrosecondsReadBackAsMilliseconds(t *testing.T) {
	mockDate(t, 20_001)
	clock, restore := jsclock.MockPerformanceNow(500)
	t.Cleanup(restore)
	f := newFixture(t, SqliteTelemetryOptions{})
	check(t, f.telemetry.StartSpan(telemetry.SpanOptions{Name: "root"}, func(span telemetry.TelemetrySpan) error {
		clock.Set(500.125)
		return span.StartSpan(telemetry.SpanOptions{Name: "child"}, func(telemetry.TelemetrySpan) error {
			clock.Set(500.5)
			span.AddEvent("half", nil)
			return nil
		})
	}))
	f.flush()
	rows := f.all("SELECT name, started_us, ended_us, events FROM telemetry_spans ORDER BY started_us")
	want := []struct {
		name           string
		started, ended float64
		events         string
	}{
		{"root", 20_001_000, 20_001_500, `[{"name":"half","time_ms":0.5,"attributes":{}}]`},
		{"child", 20_001_125, 20_001_500, "[]"},
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %v", rows)
	}
	for i, row := range rows {
		if row["name"] != want[i].name || rowNumberOf(t, row, "started_us") != want[i].started || rowNumberOf(t, row, "ended_us") != want[i].ended || row["events"] != want[i].events {
			t.Fatalf("row %d = %v, want %+v", i, row, want[i])
		}
	}
	startMin, startMax := 20_001.0, 20_001.0
	result, err := f.telemetry.FindTraces(telemetry.TraceQuery{StartMin: &startMin, StartMax: &startMax})
	check(t, err)
	spans := result.Traces[0].Spans
	if spans[0].StartedMs != 20_001 || spans[0].EndedMs != 20_001.5 || spans[1].StartedMs != 20_001.125 || spans[1].EndedMs != 20_001.5 {
		t.Fatalf("spans = %+v", spans)
	}
}

func TestMigratesAStoreOfVersion1ToMicrosecondsItsRowsKept(t *testing.T) {
	db := openMemory(t)
	t.Cleanup(func() { _ = db.Close() })
	// Version 1 as it shipped: its tables and one span.
	for _, statement := range []string{
		`CREATE TABLE telemetry_schema (singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
			version INTEGER NOT NULL CHECK (version >= 0)) STRICT`,
		"INSERT INTO telemetry_schema VALUES (1, 1)",
		`CREATE TABLE telemetry_spans (id TEXT PRIMARY KEY, trace_id TEXT NOT NULL, parent_id TEXT,
			name TEXT, started_ms REAL, ended_ms REAL NOT NULL, attributes TEXT, events TEXT, links TEXT, status TEXT)`,
		"CREATE INDEX telemetry_spans_started_ms ON telemetry_spans(started_ms)",
		`CREATE TABLE telemetry_metrics (interval_ms INTEGER, series_key TEXT, name TEXT, kind TEXT,
			unit TEXT, attributes TEXT, count INTEGER, sum REAL, min REAL, max REAL, PRIMARY KEY(interval_ms, series_key))`,
		`INSERT INTO telemetry_spans VALUES ('old:1', 'old:1', NULL, 'old', 1000, 1002.5, '{}', '[]', '[]', '{"status":"ok"}')`,
	} {
		check(t, db.Exec(statement))
	}
	recorder, err := CreateSqliteTelemetry(db, SqliteTelemetryOptions{})
	check(t, err)
	version, err := db.Get("SELECT version FROM telemetry_schema")
	check(t, err)
	if rowNumberOf(t, version, "version") != 2 {
		t.Fatalf("version = %v", version)
	}
	rows, err := db.All("SELECT started_us, ended_us FROM telemetry_spans")
	check(t, err)
	if len(rows) != 1 || rowNumberOf(t, rows[0], "started_us") != 1_000_000 || rowNumberOf(t, rows[0], "ended_us") != 1_002_500 {
		t.Fatalf("rows = %v", rows)
	}
	trace, err := recorder.GetTrace("old:1")
	check(t, err)
	if trace == nil || trace.Spans[0].StartedMs != 1000 || trace.Spans[0].EndedMs != 1002.5 {
		t.Fatalf("trace = %+v", trace)
	}
	check(t, recorder.Close())
}

func TestWritesIngestedSpansAndMetricRowsWithItsOwnNeverReplacingAStoredSpan(t *testing.T) {
	mockDate(t, 20_001)
	f := newFixture(t, SqliteTelemetryOptions{})
	f.span("own")
	f.flush()
	name := "own"
	found, err := f.telemetry.FindTraces(telemetry.TraceQuery{Name: &name})
	check(t, err)
	own := found.Traces[0].Spans[0]
	span := own
	span.ID, span.TraceID, span.Name, span.StartedMs, span.EndedMs = "w:1", "w:1", "worker", 1000.5, 1001.25
	impostor := own
	impostor.Name = "impostor"
	metric := telemetry.StoredMetric{
		IntervalMs: 10_000, Name: "hits", Kind: telemetry.TelemetryMetricKindCounter,
		Attributes: telemetry.SpanAttributes{telemetry.Attr("b", 1.0), telemetry.Attr("a", 2.0)}, Count: 2, Sum: 6, Min: 2, Max: 4,
	}
	if queued := f.telemetry.Ingest(Batch{Spans: []telemetry.StoredSpan{span, impostor}, Metrics: []telemetry.StoredMetric{metric}}); queued != 2 {
		t.Fatalf("queued = %d", queued)
	}
	f.flush()
	worker, err := f.telemetry.GetTrace("w:1")
	check(t, err)
	if worker == nil || !reflect.DeepEqual(worker.Spans, []telemetry.StoredSpan{span}) {
		t.Fatalf("worker trace = %+v, want %+v", worker, span)
	}
	stored, err := f.telemetry.GetTrace(own.TraceID)
	check(t, err)
	if len(stored.Spans) != 1 || stored.Spans[0].Name != "own" {
		t.Fatalf("own trace = %+v", stored)
	}
	metrics, err := f.telemetry.FindMetrics(telemetry.MetricQuery{Name: "hits"})
	check(t, err)
	if len(metrics) != 1 || metrics[0].Count != 2 || metrics[0].Sum != 6 || metrics[0].Attributes[0].Name != "a" || metrics[0].Attributes[1].Name != "b" {
		t.Fatalf("metrics = %+v", metrics)
	}
	_ = telemetryingest.IntervalMs
}

func recorderOn(t *testing.T, file string) (*SqliteTelemetry, func()) {
	t.Helper()
	db, err := node.OpenNodeSqliteDatabase(file, node.NodeSqliteDatabaseOptions{})
	check(t, err)
	recorder, err := CreateSqliteTelemetry(db, SqliteTelemetryOptions{})
	check(t, err)
	return recorder, func() {
		check(t, recorder.Close())
		check(t, db.Close())
	}
}

func TestReadOnlyStoreReadsWhatRecordersWroteWhileOneStillRecordsAndNeverWritesTheFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "telemetry.sqlite")
	writing, closeWriting := recorderOn(t, file)
	check(t, writing.StartSpan(telemetry.SpanOptions{Name: "first"}, func(span telemetry.TelemetrySpan) error {
		return span.StartSpan(telemetry.SpanOptions{Name: "child"}, func(telemetry.TelemetrySpan) error { return nil })
	}))
	writing.Metrics().Counter("hits", telemetry.InstrumentOptions{}).Add(2, nil)
	check(t, writing.Flush())
	store, err := OpenTelemetryStore(file)
	check(t, err)
	first := "first"
	found, err := store.FindTraces(telemetry.TraceQuery{Name: &first})
	check(t, err)
	if len(found.Traces) != 1 || len(found.Traces[0].Spans) != 2 || found.Traces[0].Spans[0].Name != "first" || found.Traces[0].Spans[1].Name != "child" {
		t.Fatalf("traces = %+v", found.Traces)
	}
	byID, err := store.GetTrace(found.Traces[0].TraceID)
	check(t, err)
	if !reflect.DeepEqual(*byID, found.Traces[0]) {
		t.Fatalf("GetTrace = %+v", byID)
	}
	// The recorder goes on: the reader sees each flush.
	check(t, writing.StartSpan(telemetry.SpanOptions{Name: "second"}, func(telemetry.TelemetrySpan) error { return nil }))
	closeWriting()
	all, err := store.FindTraces(telemetry.TraceQuery{})
	check(t, err)
	if len(all.Traces) != 2 || all.Traces[0].Spans[0].Name != "second" || all.Traces[1].Spans[0].Name != "first" {
		t.Fatalf("all = %+v", all.Traces)
	}
	metrics, err := store.FindMetrics(telemetry.MetricQuery{Name: "hits"})
	check(t, err)
	if len(metrics) != 1 || metrics[0].Sum != 2 {
		t.Fatalf("metrics = %+v", metrics)
	}
	before, err := os.ReadFile(file)
	check(t, err)
	_, err = store.FindTraces(telemetry.TraceQuery{})
	check(t, err)
	check(t, store.Close())
	after, err := os.ReadFile(file)
	check(t, err)
	if !bytes.Equal(before, after) {
		t.Fatal("the read-only store wrote the file")
	}
	if _, err := store.FindTraces(telemetry.TraceQuery{}); err == nil {
		t.Fatal("a read after Close succeeded")
	}
}

func TestReadOnlyStoreRefusesAMissingFileAFileWithoutAStoreAndAStoreOfAnotherVersion(t *testing.T) {
	dir := t.TempDir()
	if store, err := OpenTelemetryStore(filepath.Join(dir, "missing.sqlite")); err == nil {
		_ = store.Close()
		t.Fatal("a missing file opened")
	}
	other := filepath.Join(dir, "other.sqlite")
	db, err := node.OpenNodeSqliteDatabase(other, node.NodeSqliteDatabaseOptions{})
	check(t, err)
	check(t, db.Exec("CREATE TABLE notes (id INTEGER)"))
	check(t, db.Close())
	if _, err := OpenTelemetryStore(other); err == nil || !strings.Contains(err.Error(), "not a telemetry store") {
		t.Fatalf("other = %v", err)
	}
	old := filepath.Join(dir, "old.sqlite")
	_, closeOld := recorderOn(t, old)
	closeOld()
	for _, version := range []struct {
		set     int
		message string
	}{{1, "telemetry store version 1, not 2"}, {3, "telemetry store version 3 is newer than this reader's 2"}} {
		changed, err := node.OpenNodeSqliteDatabase(old, node.NodeSqliteDatabaseOptions{})
		check(t, err)
		check(t, changed.Run("UPDATE telemetry_schema SET version = ?", chordsqlite.SqliteValue(int64(version.set))))
		check(t, changed.Close())
		if _, err := OpenTelemetryStore(old); err == nil || !strings.Contains(err.Error(), version.message) {
			t.Fatalf("version %d: %v", version.set, err)
		}
	}
}
