// Package telemetrysqlite ports Pi's @earendil-works/chord/telemetry-sqlite: a portable, buffered SQLite telemetry
// recorder and the TelemetryStore queries over its committed rows. It runs on any chord/sqlite.SqliteDatabase and owns
// no connection.
package telemetrysqlite

// Ports packages/chord/src/telemetry-sqlite/index.ts.
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
	"reflect"
	"slices"
	"strconv"
	"sync"
	"time"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/internal/jsclock"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
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
	intervalMs         = 10_000
	flushInterval      = time.Second
	carrierKey         = "pi-sqlite-span"
	traceKey           = "pi-sqlite-trace"
)

type identity struct{ id, traceID string }

// identified is implemented by every context this package hands out, a span or an extracted parent: upstream's
// module-level `identities` WeakMap, shared by every recorder, so a span of one recorder is a link target and an
// injectable context of any other.
type identified interface{ sqliteIdentity() identity }

type link struct {
	traceID, spanID string
	attributes      telemetry.SpanAttributes
}

type event struct {
	name       string
	timeMs     float64
	attributes telemetry.SpanAttributes
}

type metric struct {
	interval      float64
	key, name     string
	kind          telemetry.TelemetryMetricKind
	unit          *string
	attributes    string
	count         int64
	sum, min, max float64
}

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
	capacity  int
	scope     string

	// writeMu serializes drains: upstream's `writing` promise.
	writeMu sync.Mutex

	mu                  sync.Mutex
	sequence            int
	spans               [][]chordsqlite.SqliteValue
	measurements        map[string]*metric
	measurementOrder    []string
	inFlightMeasurement int
	droppedSpans        int
	droppedMeasurements int
	stopped             bool
	failed              bool
	failure             error
	closing             *closeOperation

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
// flush runs every second. Only settled spans are stored. Metrics aggregate count/sum/min/max per ten-second interval.
// A write failure permanently stops recording and is reported by Flush and Close.
func CreateSqliteTelemetry(db chordsqlite.SqliteDatabase, options SqliteTelemetryOptions) (*SqliteTelemetry, error) {
	retention := float64(defaultRetentionMs)
	if options.RetentionMs != nil {
		retention = *options.RetentionMs
	}
	capacity := defaultMaxQueued
	if options.MaxQueuedSpans != nil {
		capacity = *options.MaxQueuedSpans
	}
	err := chordsqlite.ApplySqliteMigrations(db, []chordsqlite.SqliteMigration{{
		Version: 1,
		Statements: []string{
			`CREATE TABLE telemetry_spans (id TEXT PRIMARY KEY, trace_id TEXT NOT NULL, parent_id TEXT, name TEXT, started_ms REAL,
			ended_ms REAL NOT NULL, attributes TEXT, events TEXT, links TEXT, status TEXT)`,
			"CREATE INDEX telemetry_spans_ended_ms ON telemetry_spans(ended_ms)",
			"CREATE INDEX telemetry_spans_trace_started ON telemetry_spans(trace_id, started_ms)",
			"CREATE INDEX telemetry_spans_started_ms ON telemetry_spans(started_ms)",
			`CREATE TABLE telemetry_metrics (interval_ms INTEGER, series_key TEXT, name TEXT, kind TEXT,
			unit TEXT, attributes TEXT, count INTEGER, sum REAL, min REAL, max REAL, PRIMARY KEY(interval_ms, series_key))`,
		},
	}}, chordsqlite.ApplySqliteMigrationsOptions{Table: "telemetry_schema"})
	if err != nil {
		return nil, err
	}
	recorder := &SqliteTelemetry{
		db: db, retention: retention, capacity: capacity, scope: newScope(),
		measurements: map[string]*metric{}, stop: make(chan struct{}), timerDone: make(chan struct{}),
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
	go recorder.runTimer(channel, stop)
	return recorder, nil
}

// runTimer is the owned background flush: it ends when stop is closed, by Close or by a write failure.
func (recorder *SqliteTelemetry) runTimer(ticks <-chan time.Time, stopTicks func()) {
	defer close(recorder.timerDone)
	defer stopTicks()
	for {
		select {
		case <-recorder.stop:
			return
		case <-ticks:
			// `if (!this.writing) void this.flush().catch(() => {})`: a tick during a write does nothing, and a failed
			// write is remembered for Flush and Close rather than reported here.
			if recorder.writeMu.TryLock() {
				recorder.mu.Lock()
				closing := recorder.closing != nil
				recorder.mu.Unlock()
				if !closing {
					_ = recorder.drainLocked(false)
				}
				recorder.writeMu.Unlock()
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

func (recorder *SqliteTelemetry) haltTimer() { recorder.stopTimer.Do(func() { close(recorder.stop) }) }

// DroppedSpans counts settled spans dropped because the queue was full.
func (recorder *SqliteTelemetry) DroppedSpans() int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.droppedSpans
}

// DroppedMeasurements counts new metric interval/series pairs dropped because the pending set was full.
func (recorder *SqliteTelemetry) DroppedMeasurements() int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.droppedMeasurements
}

// Metrics is the adapter's metric capability, itself.
func (recorder *SqliteTelemetry) Metrics() telemetry.TelemetryMetrics { return recorder }

// Propagation is the adapter's propagation capability, itself.
func (recorder *SqliteTelemetry) Propagation() telemetry.TelemetryPropagation { return recorder }

// StartSpan starts a root span, invokes and waits for callback, and settles the span when it returns or panics.
func (recorder *SqliteTelemetry) StartSpan(options telemetry.SpanOptions, callback func(telemetry.TelemetrySpan) error) error {
	return recorder.start(nil, options, callback)
}

// Inject returns a carrier for a span or extracted parent of any SQLite recorder, and nil for any other context.
func (recorder *SqliteTelemetry) Inject(context telemetry.TelemetryContext) telemetry.TelemetryCarrier {
	owner, ok := context.(identified)
	if !ok {
		return nil
	}
	identity := owner.sqliteIdentity()
	return telemetry.TelemetryCarrier{carrierKey: identity.id, traceKey: identity.traceID}
}

// Extract returns a parent whose spans continue the carrier's span and trace, or this recorder's root when either
// identifier is absent. Recognized identifiers are opaque strings.
func (recorder *SqliteTelemetry) Extract(carrier telemetry.TelemetryCarrier) telemetry.TelemetryContext {
	id, hasID := carrier[carrierKey]
	traceID, hasTrace := carrier[traceKey]
	if !hasID || !hasTrace {
		return recorder
	}
	return &remoteParent{owner: recorder, identity: identity{id: id, traceID: traceID}}
}

// remoteParent is the parent Extract returns: it only starts spans.
type remoteParent struct {
	owner    *SqliteTelemetry
	identity identity
}

func (parent *remoteParent) StartSpan(options telemetry.SpanOptions, callback func(telemetry.TelemetrySpan) error) error {
	return parent.owner.start(&parent.identity, options, callback)
}
func (parent *remoteParent) Metrics() telemetry.TelemetryMetrics         { return parent.owner }
func (parent *remoteParent) Propagation() telemetry.TelemetryPropagation { return parent.owner }
func (parent *remoteParent) sqliteIdentity() identity                    { return parent.identity }

// inertSpan is what a callback receives once the recorder is stopped or its parent settled: it records nothing and
// keeps the recorder's capabilities, as upstream's `inert`.
type inertSpan struct{ owner *SqliteTelemetry }

func (span inertSpan) StartSpan(_ telemetry.SpanOptions, callback func(telemetry.TelemetrySpan) error) error {
	return callback(span)
}
func (span inertSpan) AddLink(telemetry.SpanLink)                  {}
func (span inertSpan) AddEvent(string, telemetry.SpanAttributes)   {}
func (span inertSpan) SetAttributes(telemetry.SpanAttributes)      {}
func (span inertSpan) SetStatus(telemetry.SpanStatus)              {}
func (span inertSpan) Metrics() telemetry.TelemetryMetrics         { return span.owner }
func (span inertSpan) Propagation() telemetry.TelemetryPropagation { return span.owner }

type sqliteSpan struct {
	owner      *SqliteTelemetry
	identity   identity
	parentID   *string
	name       string
	started    float64
	clock      float64
	attributes telemetry.SpanAttributes
	events     []event
	links      []link
	status     telemetry.SpanStatus
	explicit   bool
	settled    bool
}

func (span *sqliteSpan) sqliteIdentity() identity                    { return span.identity }
func (span *sqliteSpan) Metrics() telemetry.TelemetryMetrics         { return span.owner }
func (span *sqliteSpan) Propagation() telemetry.TelemetryPropagation { return span.owner }

func (span *sqliteSpan) StartSpan(options telemetry.SpanOptions, callback func(telemetry.TelemetrySpan) error) error {
	span.owner.mu.Lock()
	settled := span.settled
	span.owner.mu.Unlock()
	if settled {
		return callback(inertSpan{span.owner})
	}
	return span.owner.start(&span.identity, options, callback)
}

// record is upstream's `record`: the action runs only while the span is unsettled and the recorder running.
func (span *sqliteSpan) record(action func()) {
	span.owner.mu.Lock()
	defer span.owner.mu.Unlock()
	if !span.settled && !span.owner.stopped {
		action()
	}
}

func (span *sqliteSpan) AddLink(spanLink telemetry.SpanLink) {
	copied, ok := copyLink(spanLink)
	span.record(func() {
		if ok {
			span.links = append(span.links, copied)
		}
	})
}

func (span *sqliteSpan) AddEvent(name string, attributes telemetry.SpanAttributes) {
	elapsed := jsclock.PerformanceNow() - span.clock
	copied := copyAttributes(attributes)
	span.record(func() { span.events = append(span.events, event{name: name, timeMs: elapsed, attributes: copied}) })
}

func (span *sqliteSpan) SetAttributes(attributes telemetry.SpanAttributes) {
	copied := copyAttributes(attributes)
	span.record(func() { span.attributes = span.attributes.Merge(copied) })
}

func (span *sqliteSpan) SetStatus(status telemetry.SpanStatus) {
	span.record(func() {
		span.status = telemetry.SpanStatus{Status: status.Status}
		if status.Status == telemetry.SpanStatusCodeError && status.Error != nil {
			span.status.Error = &telemetry.SpanStatusError{Name: status.Error.Name, Message: status.Error.Message}
		}
		span.explicit = true
	})
}

// copyAttributes is upstream's copyAttributes: Object.fromEntries of the defined entries with array values copied. A
// name written twice keeps its first position and takes its last value, as an object literal does, so a later nil
// (undefined) value removes the name.
func copyAttributes(attributes telemetry.SpanAttributes) telemetry.SpanAttributes {
	copied := make(telemetry.SpanAttributes, 0, len(attributes))
	undefined := false
	for _, attribute := range attributes {
		if attribute.Value == nil {
			undefined = true
		}
		copied = copied.Set(attribute.Name, cloneAttributeValue(attribute.Value))
	}
	if undefined {
		copied = slices.DeleteFunc(copied, func(attribute telemetry.SpanAttribute) bool { return attribute.Value == nil })
	}
	return copied
}

// cloneAttributeValue copies an array value, as upstream's `[...value]`.
func cloneAttributeValue(value telemetry.AttributeValue) telemetry.AttributeValue {
	if reflected := reflect.ValueOf(value); reflected.Kind() == reflect.Slice {
		cloned := reflect.MakeSlice(reflected.Type(), reflected.Len(), reflected.Len())
		reflect.Copy(cloned, reflected)
		return cloned.Interface()
	}
	return value
}

// copyLink is upstream's copyLink: a link whose target has no recorded identity is dropped.
func copyLink(spanLink telemetry.SpanLink) (link, bool) {
	owner, ok := spanLink.Context.(identified)
	if !ok {
		return link{}, false
	}
	identity := owner.sqliteIdentity()
	return link{traceID: identity.traceID, spanID: identity.id, attributes: copyAttributes(spanLink.Attributes)}, true
}

// automaticErrorStatus is the status of a callback that failed without setting one: the failure's `error.name` and
// message, or a bare error for a failure that is not an error.
func automaticErrorStatus(failure any) telemetry.SpanStatus {
	status := telemetry.SpanStatus{Status: telemetry.SpanStatusCodeError}
	if err, ok := failure.(error); ok {
		name := "Error"
		if named, ok := err.(interface{ Name() string }); ok {
			name = named.Name()
		}
		status.Error = &telemetry.SpanStatusError{Name: name, Message: err.Error()}
	}
	return status
}

func (recorder *SqliteTelemetry) start(parent *identity, options telemetry.SpanOptions, callback func(telemetry.TelemetrySpan) error) (err error) {
	recorder.mu.Lock()
	if recorder.stopped {
		recorder.mu.Unlock()
		return callback(inertSpan{recorder})
	}
	var links []link
	for _, spanLink := range options.Links {
		if copied, ok := copyLink(spanLink); ok {
			links = append(links, copied)
		}
	}
	recorder.sequence++
	id := recorder.scope + ":" + strconv.Itoa(recorder.sequence)
	span := &sqliteSpan{
		owner: recorder, identity: identity{id: id, traceID: id}, name: options.Name, started: jsclock.DateNow(),
		clock: jsclock.PerformanceNow(), attributes: copyAttributes(options.Attributes), links: links,
		status: telemetry.SpanStatus{Status: telemetry.SpanStatusCodeOK},
	}
	if parent != nil {
		span.identity.traceID = parent.traceID
		span.parentID = &parent.id
	}
	recorder.mu.Unlock()
	defer func() {
		failure := recover()
		failed := failure != nil || err != nil
		if failure == nil && err != nil {
			failure = err
		}
		span.settle(failed, failure)
		if failure != nil && err == nil {
			panic(failure)
		}
	}()
	return callback(span)
}

func (span *sqliteSpan) settle(failed bool, failure any) {
	recorder := span.owner
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if failed && !span.explicit {
		span.status = automaticErrorStatus(failure)
	}
	span.settled = true
	if recorder.stopped {
		return
	}
	if len(recorder.spans) >= recorder.capacity {
		recorder.droppedSpans++
		return
	}
	var parentID chordsqlite.SqliteValue
	if span.parentID != nil {
		parentID = *span.parentID
	}
	recorder.spans = append(recorder.spans, []chordsqlite.SqliteValue{
		span.identity.id, span.identity.traceID, parentID, span.name, span.started,
		span.started + jsclock.PerformanceNow() - span.clock,
		string(appendAttributes(nil, span.attributes)), string(appendEvents(nil, span.events)),
		string(appendLinks(nil, span.links)), string(appendStatus(nil, span.status)),
	})
}

// measurement is upstream's `measurement`: a recording folds into the pending (interval, series) aggregate, or is
// dropped and counted when a new pair would exceed the bound.
func (recorder *SqliteTelemetry) measurement(kind telemetry.TelemetryMetricKind, name string, options telemetry.InstrumentOptions) func(float64, telemetry.SpanAttributes) {
	var unit *string
	if options.Unit != nil {
		unit = new(*options.Unit)
	}
	return func(value float64, attributes telemetry.SpanAttributes) {
		recorder.mu.Lock()
		defer recorder.mu.Unlock()
		if recorder.stopped {
			return
		}
		encoded := string(appendCanonicalAttributes(nil, copyAttributes(attributes)))
		key := string(appendSeriesKey(nil, name, kind, unit, encoded))
		interval := math.Floor(jsclock.DateNow()/intervalMs) * intervalMs
		pendingKey := strconv.FormatFloat(interval, 'f', -1, 64) + ":" + key
		previous := recorder.measurements[pendingKey]
		if previous == nil {
			if len(recorder.measurements)+recorder.inFlightMeasurement >= recorder.capacity {
				recorder.droppedMeasurements++
				return
			}
			recorder.measurements[pendingKey] = &metric{
				interval: interval, key: key, name: name, kind: kind, unit: unit, attributes: encoded, count: 1,
				sum: value, min: value, max: value,
			}
			recorder.measurementOrder = append(recorder.measurementOrder, pendingKey)
			return
		}
		previous.count++
		previous.sum += value
		previous.min = math.Min(previous.min, value)
		previous.max = math.Max(previous.max, value)
	}
}

type sqliteCounter struct {
	add func(float64, telemetry.SpanAttributes)
}

func (counter sqliteCounter) Add(value float64, attributes telemetry.SpanAttributes) {
	counter.add(value, attributes)
}

type sqliteHistogram struct {
	record func(float64, telemetry.SpanAttributes)
}

func (histogram sqliteHistogram) Record(value float64, attributes telemetry.SpanAttributes, _ telemetry.TelemetryContext) {
	histogram.record(value, attributes)
}

// Counter creates a counter; its description is not retained.
func (recorder *SqliteTelemetry) Counter(name string, options telemetry.InstrumentOptions) telemetry.TelemetryCounter {
	return sqliteCounter{recorder.measurement(telemetry.TelemetryMetricKindCounter, name, options)}
}

// UpDownCounter creates an up-down counter.
func (recorder *SqliteTelemetry) UpDownCounter(name string, options telemetry.InstrumentOptions) telemetry.TelemetryUpDownCounter {
	return sqliteCounter{recorder.measurement(telemetry.TelemetryMetricKindUpDownCounter, name, options)}
}

// Histogram creates a histogram; descriptions and boundaries are not retained and the exemplar context is ignored.
func (recorder *SqliteTelemetry) Histogram(name string, options telemetry.HistogramOptions) telemetry.TelemetryHistogram {
	return sqliteHistogram{recorder.measurement(telemetry.TelemetryMetricKindHistogram, name, options.InstrumentOptions)}
}

// FindTraces reads committed rows only; Flush first to include newly settled spans.
func (recorder *SqliteTelemetry) FindTraces(query telemetry.TraceQuery) (telemetry.FindTracesResult, error) {
	return findTraces(recorder.db, query)
}

// GetTrace reads committed rows only.
func (recorder *SqliteTelemetry) GetTrace(traceID string) (*telemetry.StoredTrace, error) {
	return getTrace(recorder.db, traceID)
}

// FindMetrics reads committed rows only.
func (recorder *SqliteTelemetry) FindMetrics(query telemetry.MetricQuery) ([]telemetry.StoredMetric, error) {
	return findMetrics(recorder.db, query)
}

// Flush persists settled spans and completed ten-second metric intervals. After Close it returns Close's result.
func (recorder *SqliteTelemetry) Flush() error {
	recorder.mu.Lock()
	closing := recorder.closing
	recorder.mu.Unlock()
	if closing != nil {
		<-closing.done
		return closing.err
	}
	recorder.writeMu.Lock()
	defer recorder.writeMu.Unlock()
	return recorder.drainLocked(false)
}

// Close stops recording and drains, including partial metric intervals, then joins the background flush. It does not
// wait for active business callbacks and does not close the database. Every call returns the first call's result.
func (recorder *SqliteTelemetry) Close() error {
	recorder.mu.Lock()
	closing := recorder.closing
	first := closing == nil
	if first {
		recorder.stopped = true
		closing = &closeOperation{done: make(chan struct{})}
		recorder.closing = closing
	}
	recorder.mu.Unlock()
	if first {
		recorder.haltTimer()
		recorder.writeMu.Lock()
		closing.err = recorder.drainLocked(true)
		recorder.writeMu.Unlock()
		close(closing.done)
	}
	<-closing.done
	<-recorder.timerDone
	return closing.err
}

// drainLocked is upstream's drain with writeMu held.
func (recorder *SqliteTelemetry) drainLocked(partial bool) error {
	recorder.mu.Lock()
	if recorder.failed {
		failure := recorder.failure
		recorder.mu.Unlock()
		return failure
	}
	spans := slices.Clone(recorder.spans)
	var metrics []*metric
	completed := math.Floor(jsclock.DateNow()/intervalMs) * intervalMs
	remaining := recorder.measurementOrder[:0:0]
	for _, key := range recorder.measurementOrder {
		pending := recorder.measurements[key]
		if !partial && pending.interval >= completed {
			remaining = append(remaining, key)
			continue
		}
		metrics = append(metrics, pending)
		delete(recorder.measurements, key)
	}
	recorder.measurementOrder = remaining
	recorder.inFlightMeasurement = len(metrics)
	recorder.mu.Unlock()

	err := recorder.write(spans, metrics)

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.inFlightMeasurement = 0
	if err != nil {
		recorder.failed, recorder.stopped = true, true
		recorder.failure = err
		recorder.spans = nil
		recorder.measurements = map[string]*metric{}
		recorder.measurementOrder = nil
		recorder.haltTimer()
		return err
	}
	recorder.spans = recorder.spans[len(spans):]
	return nil
}

func (recorder *SqliteTelemetry) write(spans [][]chordsqlite.SqliteValue, metrics []*metric) error {
	return recorder.db.Transaction(func(tx chordsqlite.SqliteExecutor) error {
		for _, span := range spans {
			if err := tx.Run("INSERT INTO telemetry_spans VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", span...); err != nil {
				return err
			}
		}
		for _, pending := range metrics {
			var unit chordsqlite.SqliteValue
			if pending.unit != nil {
				unit = *pending.unit
			}
			//portlint:allow numbers interval is a whole multiple of 10,000 from Math.floor
			interval := int64(pending.interval)
			if err := tx.Run(`INSERT INTO telemetry_metrics VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(interval_ms, series_key) DO UPDATE SET count = count + excluded.count,
				sum = sum + excluded.sum, min = min(min, excluded.min), max = max(max, excluded.max)`,
				interval, pending.key, pending.name, string(pending.kind), unit, pending.attributes, pending.count,
				pending.sum, pending.min, pending.max); err != nil {
				return err
			}
		}
		cutoff := jsclock.DateNow() - recorder.retention
		if err := tx.Run("DELETE FROM telemetry_spans WHERE ended_ms < ?", cutoff); err != nil {
			return err
		}
		return tx.Run("DELETE FROM telemetry_metrics WHERE interval_ms < ?", cutoff)
	})
}

// JSON encoders: JSON.stringify of the recorded values, attribute objects in insertion order.

func appendString(dst []byte, text string) []byte { return jsstring.AppendString(dst, text) }

func appendValue(dst []byte, value telemetry.AttributeValue) []byte {
	switch typed := value.(type) {
	case nil:
		return append(dst, "null"...)
	case string:
		return appendString(dst, typed)
	case bool:
		return strconv.AppendBool(dst, typed)
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Slice, reflect.Array:
		dst = append(dst, '[')
		for i := range reflected.Len() {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = appendValue(dst, reflected.Index(i).Interface())
		}
		return append(dst, ']')
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return append(dst, jsnumber.String(float64(reflected.Int()))...)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return append(dst, jsnumber.String(float64(reflected.Uint()))...)
	case reflect.Float32, reflect.Float64:
		return appendNumber(dst, reflected.Float())
	}
	encoded, err := jsstring.AppendMarshalJSON(dst, value)
	if err != nil {
		return append(dst, "null"...)
	}
	return encoded
}

// appendNumber is JSON.stringify of a number: a non-finite value is null.
func appendNumber(dst []byte, value float64) []byte {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return append(dst, "null"...)
	}
	return append(dst, jsnumber.String(value)...)
}

// appendAttributes is JSON.stringify of an attribute object: members in insertion order.
func appendAttributes(dst []byte, attributes telemetry.SpanAttributes) []byte {
	dst = append(dst, '{')
	for i, attribute := range attributes {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = appendString(dst, attribute.Name)
		dst = append(dst, ':')
		dst = appendValue(dst, attribute.Value)
	}
	return append(dst, '}')
}

// appendCanonicalAttributes is the metric series' attribute object: `Object.fromEntries(Object.keys(copied).sort()
// .map(key => [key, copied[key]]))`, the keys sorted by UTF-16 code unit and then listed as an object lists them
// (array-index names first, ascending).
func appendCanonicalAttributes(dst []byte, attributes telemetry.SpanAttributes) []byte {
	sorted := slices.Clone(attributes)
	slices.SortFunc(sorted, func(a, b telemetry.SpanAttribute) int {
		return slices.Compare(jsstring.ToUTF16(a.Name), jsstring.ToUTF16(b.Name))
	})
	var canonical telemetry.SpanAttributes
	for _, attribute := range sorted {
		canonical = canonical.Set(attribute.Name, attribute.Value)
	}
	return appendAttributes(dst, canonical)
}

func appendEvents(dst []byte, events []event) []byte {
	dst = append(dst, '[')
	for i, recorded := range events {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, `{"name":`...)
		dst = appendString(dst, recorded.name)
		dst = append(dst, `,"time_ms":`...)
		dst = appendNumber(dst, recorded.timeMs)
		dst = append(dst, `,"attributes":`...)
		dst = appendAttributes(dst, recorded.attributes)
		dst = append(dst, '}')
	}
	return append(dst, ']')
}

func appendLinks(dst []byte, links []link) []byte {
	dst = append(dst, '[')
	for i, recorded := range links {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, `{"trace_id":`...)
		dst = appendString(dst, recorded.traceID)
		dst = append(dst, `,"span_id":`...)
		dst = appendString(dst, recorded.spanID)
		dst = append(dst, `,"attributes":`...)
		dst = appendAttributes(dst, recorded.attributes)
		dst = append(dst, '}')
	}
	return append(dst, ']')
}

func appendStatus(dst []byte, status telemetry.SpanStatus) []byte {
	dst = append(dst, `{"status":`...)
	dst = appendString(dst, string(status.Status))
	if status.Status == telemetry.SpanStatusCodeError && status.Error != nil {
		dst = append(dst, `,"error":{"name":`...)
		dst = appendString(dst, status.Error.Name)
		dst = append(dst, `,"message":`...)
		dst = appendString(dst, status.Error.Message)
		dst = append(dst, '}')
	}
	return append(dst, '}')
}

// appendSeriesKey is JSON.stringify([name, kind, unit, encoded]).
func appendSeriesKey(dst []byte, name string, kind telemetry.TelemetryMetricKind, unit *string, encoded string) []byte {
	dst = append(dst, '[')
	dst = appendString(dst, name)
	dst = append(dst, ',')
	dst = appendString(dst, string(kind))
	dst = append(dst, ',')
	if unit == nil {
		dst = append(dst, "null"...)
	} else {
		dst = appendString(dst, *unit)
	}
	dst = append(dst, ',')
	dst = appendString(dst, encoded)
	return append(dst, ']')
}
