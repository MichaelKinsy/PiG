// Package telemetrysqlite ports Pi's @earendil-works/chord/telemetry-sqlite: a portable, buffered SQLite telemetry
// recorder and the TelemetryStore queries over its committed rows. It runs on any chord/sqlite.SqliteDatabase and owns
// no connection.
package telemetrysqlite

// Ports packages/chord/src/telemetry-sqlite/index.ts (Pi main c23b9ec1): a recorder (telemetryingest.Recorder) whose
// batches are written every second, settled spans one row each (events and links inline, times in microseconds) and
// metrics per series per 10 s, with retention; spans and metrics other processes recorded join the same batches
// (Ingest, never overwriting a stored span); and the TelemetryStore reads over it.
//
// Async contract (docs/typescript-to-go-porting.md worksheet):
//   - startSpan awaits its callback: StartSpan blocks and returns the callback's error, settling the span on return,
//     error or panic.
//   - flush/close await the commit: Flush and Close block until the write settles and return its error. Writes are
//     serialized (`while (this.writing) await this.writing`): at most one transaction runs, later callers queue on
//     writeMu. Close is idempotent and every call returns the first call's result; Flush after Close returns Close's
//     result.
//   - the one-second `setInterval(() => void this.flush().catch(() => {}))` is an owned background task: one goroutine
//     started by CreateSqliteTelemetry, stopped by Close or by a write failure, joined by Close, skipping a tick while a
//     write is in flight. Its flush error is not lost: the failure is remembered and Flush and Close return it.
//   - there is no cancellation: upstream has no AbortSignal here, and Close is the shutdown that drains.

import (
	"crypto/rand"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/telemetryingest"
	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/internal/jsclock"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// SqliteTelemetryOptions configures CreateSqliteTelemetry. A nil member is upstream's undefined.
type SqliteTelemetryOptions struct {
	// RetentionMs retains seven days by default. Cleanup runs in the flush transaction.
	RetentionMs *float64
	// MaxQueuedSpans bounds settled spans awaiting commit, including the in-flight batch. Defaults to 10,000. It also
	// bounds pending metric interval/series pairs independently.
	MaxQueuedSpans *int

	// ticks replaces the one-second timer; nil uses time.NewTicker. A test drives the background flush with it, as
	// Pi's tests advance fake timers.
	// pig additive (D104): Go has no global fake timers; the unexported seam stands in for vi.useFakeTimers.
	ticks func() (ticks <-chan time.Time, stop func())
}

const (
	defaultRetentionMs = 7 * 24 * 60 * 60 * 1000
	defaultMaxQueued   = 10_000
	flushInterval      = time.Second
)

// Batch is what a recorder hands its owner at once: settled spans and metric aggregates.
type Batch = telemetryingest.Batch

// closeOperation is upstream's `closing` promise.
type closeOperation struct {
	done chan struct{}
	err  error
}

// SqliteTelemetry is upstream's SqliteTelemetry: a TelemetryContext with metrics and propagation, and a TelemetryStore
// over the committed rows. Readers use committed rows only; Flush first to include newly settled spans.
type SqliteTelemetry struct {
	db        chordsqlite.SqliteDatabase
	retention float64
	recorder  *telemetryingest.Recorder

	// writeMu serializes drains: upstream's `writing` promise.
	writeMu sync.Mutex

	mu      sync.Mutex
	failed  bool
	failure error
	closing *closeOperation

	stopTimer sync.Once
	stop      chan struct{}
	timerDone chan struct{}
}

var (
	_ telemetry.TelemetryContext     = (*SqliteTelemetry)(nil)
	_ telemetry.TelemetryPropagation = (*SqliteTelemetry)(nil)
	_ telemetry.TelemetryMetrics     = (*SqliteTelemetry)(nil)
	_ telemetry.TelemetryStore       = (*SqliteTelemetry)(nil)
)

// CreateSqliteTelemetry is createSqliteTelemetry: a portable, buffered SQLite recorder. Recording never performs SQL;
// flush runs every second. Only settled spans are stored, their times in microseconds (telemetry.TelemetryNow). Metrics
// aggregate count/sum/min/max per ten-second interval. A store of an older version in db is migrated (version 2: span
// times from milliseconds to microseconds, rows kept). A write failure permanently stops recording and is reported by
// Flush and Close.
func CreateSqliteTelemetry(db chordsqlite.SqliteDatabase, options SqliteTelemetryOptions) (*SqliteTelemetry, error) {
	retention := float64(defaultRetentionMs)
	if options.RetentionMs != nil {
		retention = *options.RetentionMs
	}
	capacity := defaultMaxQueued
	if options.MaxQueuedSpans != nil {
		capacity = *options.MaxQueuedSpans
	}
	if err := migrateTelemetryStore(db); err != nil {
		return nil, err
	}
	scope := newScope()
	store := &SqliteTelemetry{
		db: db, retention: retention, recorder: telemetryingest.NewRecorder(func() string { return scope }, capacity),
		stop: make(chan struct{}), timerDone: make(chan struct{}),
	}
	var channel <-chan time.Time
	var stop func()
	if options.ticks != nil {
		channel, stop = options.ticks()
	} else {
		ticker := time.NewTicker(flushInterval)
		channel, stop = ticker.C, ticker.Stop
	}
	//portlint:allow golifetime owned by the recorder: stopped through stop (Close or a write failure) and joined through timerDone in Close
	go store.runTimer(channel, stop)
	return store, nil
}

// runTimer is the owned background flush: it ends when stop is closed, by Close or by a write failure.
func (store *SqliteTelemetry) runTimer(ticks <-chan time.Time, stopTicks func()) {
	defer close(store.timerDone)
	defer stopTicks()
	for {
		select {
		case <-store.stop:
			return
		case <-ticks:
			// `if (!this.writing) void this.flush().catch(() => {})`: a tick during a write does nothing, and a failed
			// write is remembered for Flush and Close rather than reported here.
			if store.writeMu.TryLock() {
				store.mu.Lock()
				closing := store.closing != nil
				store.mu.Unlock()
				if !closing {
					_ = store.drainLocked(false)
				}
				store.writeMu.Unlock()
			}
		}
	}
}

// newScope is crypto.randomUUID(): a random (version 4) UUID.
func newScope() string {
	var id [16]byte
	_, _ = rand.Read(id[:])
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16])
}

func (store *SqliteTelemetry) haltTimer() { store.stopTimer.Do(func() { close(store.stop) }) }

// DroppedSpans counts settled spans dropped because the queue was full.
func (store *SqliteTelemetry) DroppedSpans() int { return store.recorder.DroppedSpans() }

// DroppedMeasurements counts new metric interval/series pairs dropped because the pending set was full.
func (store *SqliteTelemetry) DroppedMeasurements() int { return store.recorder.DroppedMeasurements() }

// Metrics is the adapter's metric capability, itself.
func (store *SqliteTelemetry) Metrics() telemetry.TelemetryMetrics { return store }

// Propagation is the adapter's propagation capability, itself.
func (store *SqliteTelemetry) Propagation() telemetry.TelemetryPropagation { return store }

// StartSpan starts a root span, invokes and waits for callback, and settles the span when it returns or panics.
func (store *SqliteTelemetry) StartSpan(options telemetry.SpanOptions, callback func(telemetry.TelemetrySpan) error) error {
	return store.recorder.StartSpan(options, callback)
}

// Inject returns a carrier for a span or extracted parent of any recorder, and nil for any other context.
func (store *SqliteTelemetry) Inject(context telemetry.TelemetryContext) telemetry.TelemetryCarrier {
	return store.recorder.Inject(context)
}

// Extract returns a parent whose spans continue the carrier's span and trace, or this store's root when either
// identifier is absent. Recognized identifiers are opaque strings.
func (store *SqliteTelemetry) Extract(carrier telemetry.TelemetryCarrier) telemetry.TelemetryContext {
	parent := store.recorder.Extract(carrier)
	if parent == telemetry.TelemetryContext(store.recorder) {
		return store
	}
	return parent
}

// Counter creates a counter; its description is not retained.
func (store *SqliteTelemetry) Counter(name string, options telemetry.InstrumentOptions) telemetry.TelemetryCounter {
	return store.recorder.Counter(name, options)
}

// UpDownCounter creates an up-down counter.
func (store *SqliteTelemetry) UpDownCounter(name string, options telemetry.InstrumentOptions) telemetry.TelemetryUpDownCounter {
	return store.recorder.UpDownCounter(name, options)
}

// Histogram creates a histogram; descriptions and boundaries are not retained and the exemplar context is ignored.
func (store *SqliteTelemetry) Histogram(name string, options telemetry.HistogramOptions) telemetry.TelemetryHistogram {
	return store.recorder.Histogram(name, options)
}

// Ingest queues finished spans and metric rows another process recorded, written with the next flush (a span whose id is
// stored already is kept as it was); within the queue's bound, the rest dropped and counted. It returns how many spans it
// queued. Callers check what they pass: the store writes it as it is.
func (store *SqliteTelemetry) Ingest(batch Batch) int { return store.recorder.Ingest(batch) }

// FindTraces reads committed rows only; Flush first to include newly settled spans.
func (store *SqliteTelemetry) FindTraces(query telemetry.TraceQuery) (telemetry.FindTracesResult, error) {
	return findTraces(store.db, query)
}

// GetTrace reads committed rows only.
func (store *SqliteTelemetry) GetTrace(traceID string) (*telemetry.StoredTrace, error) {
	return getTrace(store.db, traceID)
}

// FindMetrics reads committed rows only.
func (store *SqliteTelemetry) FindMetrics(query telemetry.MetricQuery) ([]telemetry.StoredMetric, error) {
	return findMetrics(store.db, query)
}

// Flush persists settled spans and completed ten-second metric intervals. After Close it returns Close's result.
func (store *SqliteTelemetry) Flush() error {
	store.mu.Lock()
	closing := store.closing
	store.mu.Unlock()
	if closing != nil {
		<-closing.done
		return closing.err
	}
	store.writeMu.Lock()
	defer store.writeMu.Unlock()
	return store.drainLocked(false)
}

// Close stops recording and drains, including partial metric intervals, then joins the background flush. It does not
// wait for active business callbacks and does not close the database. Every call returns the first call's result.
func (store *SqliteTelemetry) Close() error {
	store.mu.Lock()
	closing := store.closing
	first := closing == nil
	if first {
		store.recorder.Stop(false)
		closing = &closeOperation{done: make(chan struct{})}
		store.closing = closing
	}
	store.mu.Unlock()
	if first {
		store.haltTimer()
		store.writeMu.Lock()
		closing.err = store.drainLocked(true)
		store.writeMu.Unlock()
		close(closing.done)
	}
	<-closing.done
	<-store.timerDone
	return closing.err
}

// drainLocked is upstream's drain with writeMu held.
func (store *SqliteTelemetry) drainLocked(partial bool) error {
	store.mu.Lock()
	if store.failed {
		failure := store.failure
		store.mu.Unlock()
		return failure
	}
	store.mu.Unlock()
	batch := store.recorder.Take(partial, -1)
	err := store.write(batch)
	store.recorder.Release()
	if err != nil {
		store.mu.Lock()
		store.failed, store.failure = true, err
		store.mu.Unlock()
		store.recorder.Stop(true)
		store.haltTimer()
		return err
	}
	return nil
}

// micros is ms in whole microseconds, as the store keeps span times (Math.round(ms * 1000)).
func micros(ms float64) int64 {
	//portlint:allow numbers span times are finite and far inside int64 microseconds
	return int64(math.Floor(ms*1000 + 0.5))
}

func (store *SqliteTelemetry) write(batch Batch) error {
	return store.db.Transaction(func(tx chordsqlite.SqliteExecutor) error {
		for _, span := range batch.Spans {
			if err := writeSpan(tx, span); err != nil {
				return err
			}
		}
		for _, stored := range batch.Metrics {
			if err := writeMetric(tx, stored); err != nil {
				return err
			}
		}
		cutoff := jsclock.DateNow() - store.retention
		if err := tx.Run("DELETE FROM telemetry_spans WHERE ended_us < ?", cutoff*1000); err != nil {
			return err
		}
		return tx.Run("DELETE FROM telemetry_metrics WHERE interval_ms < ?", cutoff)
	})
}

// writeSpan writes span, unless a span of its id is stored already (an ingested span never replaces one).
func writeSpan(tx chordsqlite.SqliteExecutor, span telemetry.StoredSpan) error {
	var parentID chordsqlite.SqliteValue
	if span.ParentID != nil {
		parentID = *span.ParentID
	}
	return tx.Run("INSERT OR IGNORE INTO telemetry_spans VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		span.ID, span.TraceID, parentID, span.Name, micros(span.StartedMs), micros(span.EndedMs),
		string(telemetryingest.AppendAttributes(nil, span.Attributes)), string(appendEvents(nil, span.Events)),
		string(appendLinks(nil, span.Links)), string(appendStatus(nil, span.Status)))
}

// writeMetric adds stored to its series' row of its interval.
func writeMetric(tx chordsqlite.SqliteExecutor, stored telemetry.StoredMetric) error {
	var unit chordsqlite.SqliteValue
	if stored.Unit != nil {
		unit = *stored.Unit
	}
	//portlint:allow numbers an interval is a whole multiple of 10,000 and a count a whole number
	interval, count := int64(stored.IntervalMs), int64(stored.Count)
	return tx.Run(`INSERT INTO telemetry_metrics VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(interval_ms, series_key) DO UPDATE SET count = count + excluded.count,
		sum = sum + excluded.sum, min = min(min, excluded.min), max = max(max, excluded.max)`,
		interval, telemetryingest.SeriesKey(stored), stored.Name, string(stored.Kind), unit,
		string(telemetryingest.AppendAttributes(nil, telemetryingest.SortedAttributes(stored.Attributes))), count,
		stored.Sum, stored.Min, stored.Max)
}

// JSON encoders: JSON.stringify of the recorded values, attribute objects in insertion order.

func appendEvents(dst []byte, events []telemetry.StoredSpanEvent) []byte {
	dst = append(dst, '[')
	for i, recorded := range events {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, `{"name":`...)
		dst = jsstring.AppendString(dst, recorded.Name)
		dst = append(dst, `,"time_ms":`...)
		dst = telemetryingest.AppendNumber(dst, recorded.TimeMs)
		dst = append(dst, `,"attributes":`...)
		dst = telemetryingest.AppendAttributes(dst, recorded.Attributes)
		dst = append(dst, '}')
	}
	return append(dst, ']')
}

func appendLinks(dst []byte, links []telemetry.StoredSpanLink) []byte {
	dst = append(dst, '[')
	for i, recorded := range links {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, `{"trace_id":`...)
		dst = jsstring.AppendString(dst, recorded.TraceID)
		dst = append(dst, `,"span_id":`...)
		dst = jsstring.AppendString(dst, recorded.SpanID)
		dst = append(dst, `,"attributes":`...)
		dst = telemetryingest.AppendAttributes(dst, recorded.Attributes)
		dst = append(dst, '}')
	}
	return append(dst, ']')
}

func appendStatus(dst []byte, status telemetry.SpanStatus) []byte {
	dst = append(dst, `{"status":`...)
	dst = jsstring.AppendString(dst, string(status.Status))
	if status.Status == telemetry.SpanStatusCodeError && status.Error != nil {
		dst = append(dst, `,"error":{"name":`...)
		dst = jsstring.AppendString(dst, status.Error.Name)
		dst = append(dst, `,"message":`...)
		dst = jsstring.AppendString(dst, status.Error.Message)
		dst = append(dst, '}')
	}
	return append(dst, '}')
}
