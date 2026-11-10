package telemetryingest

// Ports packages/chord/src/telemetry-ingest/recorder.ts (Pi main c23b9ec1). Chord's telemetry recorder (§29): an adapter
// keeping settled spans and metric aggregates (per series per 10 s) in a bounded queue (past it dropped and counted),
// until its owner takes them as a batch: the SQLite store writes its batches to its file, a worker sends them to the
// server's telemetry object. Its carriers name a span by id and trace id; every recorder of a process reads every
// other's (one carrier format).
//
// Forced Go differences (language mechanics): `startSpan` awaits its callback by blocking, and settles the span when the callback
// returns, fails or panics (the panic continues). The identities WeakMap of upstream is the `identified` interface every
// span and extracted parent of a Recorder implements (Go has no weak map over interface values). A span's id counter and
// queue bounds are guarded by one mutex; take/release/ingest/stop are safe from any goroutine.

import (
	"math"
	"reflect"
	"slices"
	"strconv"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/jsclock"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// TelemetryBatch is what a recorder hands its owner at once: settled spans and metric aggregates.
type TelemetryBatch struct {
	Spans   []telemetry.StoredSpan
	Metrics []telemetry.StoredMetric
}

// identity is a span's identity in carriers and links: its id and its trace's.
type identity struct{ id, traceID string }

// identified is implemented by every span and extracted parent a Recorder hands out: upstream's module-level
// `identities` WeakMap, shared by every recorder, so a span of one recorder is a link target and an injectable context
// of any other.
type identified interface{ recorderIdentity() identity }

const (
	// SpanKey and TraceKey are the carrier's keys: a span's id and its trace's (the SQLite store's since slice 9:
	// stored job carriers use them).
	SpanKey  = "pi-sqlite-span"
	TraceKey = "pi-sqlite-trace"
	// IntervalMs is the metric interval.
	IntervalMs = 10_000
)

type metric struct {
	telemetry.StoredMetric
	key string
}

// Recorder records spans and metrics in memory for its owner, who takes them in batches (Take, then Release once they are
// written or given up). Span ids are `<scope>:<n>`, scope read at the first span (a page's names its host's incarnation,
// made after the recorder). Capacity bounds the queued spans, a taken batch's included, and separately the pending
// metric series.
type Recorder struct {
	scope    func() string
	capacity int

	mu                   sync.Mutex
	prefix               *string
	sequence             int
	spans                []telemetry.StoredSpan
	inFlight             int
	measurements         map[string]*metric
	order                []string
	inFlightMeasurements int
	droppedSpans         int
	droppedMeasurements  int
	stopped              bool
	facade               interface {
		telemetry.TelemetryMetrics
		telemetry.TelemetryPropagation
	}
}

var (
	_ telemetry.TelemetryContext     = (*Recorder)(nil)
	_ telemetry.TelemetryPropagation = (*Recorder)(nil)
	_ telemetry.TelemetryMetrics     = (*Recorder)(nil)
)

// NewRecorder makes a recorder whose span ids sit under scope() (read at the first span) and whose queues hold capacity.
func NewRecorder(scope func() string, capacity int) *Recorder {
	return &Recorder{scope: scope, capacity: capacity, measurements: map[string]*metric{}}
}

// Metrics is the adapter's metric capability, itself.
func (recorder *Recorder) Metrics() telemetry.TelemetryMetrics {
	if recorder.facade != nil {
		return recorder.facade
	}
	return recorder
}

// Propagation is the adapter's propagation capability, itself.
func (recorder *Recorder) Propagation() telemetry.TelemetryPropagation {
	if recorder.facade != nil {
		return recorder.facade
	}
	return recorder
}

// Facade makes the capabilities its spans hand out (their Metrics and Propagation) those of facade, an adapter built on
// this recorder (the SQLite store): a span of the store carries the store. Set once, before the first span.
func (recorder *Recorder) Facade(facade interface {
	telemetry.TelemetryMetrics
	telemetry.TelemetryPropagation
}) {
	recorder.facade = facade
}

// Stopped reports whether it stopped: later spans are inert, later measurements ignored.
func (recorder *Recorder) Stopped() bool {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.stopped
}

// DroppedSpans counts settled spans dropped because the queue was full.
func (recorder *Recorder) DroppedSpans() int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.droppedSpans
}

// DroppedMeasurements counts new metric interval/series pairs dropped because the pending set was full.
func (recorder *Recorder) DroppedMeasurements() int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.droppedMeasurements
}

// StartSpan starts a root span, invokes and waits for callback, and settles the span when it returns or panics.
func (recorder *Recorder) StartSpan(options telemetry.SpanOptions, callback func(telemetry.TelemetrySpan) error) error {
	return recorder.start(nil, options, callback)
}

// Inject returns a carrier for a span or extracted parent of any recorder, and nil for any other context.
func (recorder *Recorder) Inject(context telemetry.TelemetryContext) telemetry.TelemetryCarrier {
	owner, ok := context.(identified)
	if !ok {
		return nil
	}
	identity := owner.recorderIdentity()
	return telemetry.TelemetryCarrier{SpanKey: identity.id, TraceKey: identity.traceID}
}

// Extract returns a parent whose spans continue the carrier's span and trace, or this recorder's root when either
// identifier is absent. Carriers come from other processes: only their two strings are read.
func (recorder *Recorder) Extract(carrier telemetry.TelemetryCarrier) telemetry.TelemetryContext {
	id, hasID := carrier[SpanKey]
	traceID, hasTrace := carrier[TraceKey]
	if !hasID || !hasTrace {
		return recorder
	}
	return &remoteParent{owner: recorder, identity: identity{id: id, traceID: traceID}}
}

type remoteParent struct {
	owner    *Recorder
	identity identity
}

func (parent *remoteParent) StartSpan(options telemetry.SpanOptions, callback func(telemetry.TelemetrySpan) error) error {
	return parent.owner.start(&parent.identity, options, callback)
}
func (parent *remoteParent) Metrics() telemetry.TelemetryMetrics { return parent.owner.Metrics() }
func (parent *remoteParent) Propagation() telemetry.TelemetryPropagation {
	return parent.owner.Propagation()
}
func (parent *remoteParent) recorderIdentity() identity { return parent.identity }

// inertSpan is what a stopped recorder or a settled span hands out: it records nothing and keeps the recorder's
// capabilities.
type inertSpan struct{ owner *Recorder }

func (span inertSpan) StartSpan(_ telemetry.SpanOptions, callback func(telemetry.TelemetrySpan) error) error {
	return callback(inertSpan{span.owner})
}
func (inertSpan) AddLink(telemetry.SpanLink)                       {}
func (inertSpan) AddEvent(string, telemetry.SpanAttributes)        {}
func (inertSpan) SetAttributes(telemetry.SpanAttributes)           {}
func (inertSpan) SetStatus(telemetry.SpanStatus)                   {}
func (span inertSpan) Metrics() telemetry.TelemetryMetrics         { return span.owner.Metrics() }
func (span inertSpan) Propagation() telemetry.TelemetryPropagation { return span.owner.Propagation() }

type link struct {
	traceID, spanID string
	attributes      telemetry.SpanAttributes
}

type event struct {
	name       string
	timeMs     float64
	attributes telemetry.SpanAttributes
}

type recorderSpan struct {
	owner      *Recorder
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

func (span *recorderSpan) recorderIdentity() identity          { return span.identity }
func (span *recorderSpan) Metrics() telemetry.TelemetryMetrics { return span.owner.Metrics() }
func (span *recorderSpan) Propagation() telemetry.TelemetryPropagation {
	return span.owner.Propagation()
}

func (span *recorderSpan) StartSpan(options telemetry.SpanOptions, callback func(telemetry.TelemetrySpan) error) error {
	span.owner.mu.Lock()
	settled := span.settled
	span.owner.mu.Unlock()
	if settled {
		return callback(inertSpan{span.owner})
	}
	return span.owner.start(&span.identity, options, callback)
}

// record runs action only while the span is unsettled and the recorder running.
func (span *recorderSpan) record(action func()) {
	span.owner.mu.Lock()
	defer span.owner.mu.Unlock()
	if !span.settled && !span.owner.stopped {
		action()
	}
}

func (span *recorderSpan) AddLink(spanLink telemetry.SpanLink) {
	copied, ok := copyLink(spanLink)
	span.record(func() {
		if ok {
			span.links = append(span.links, copied)
		}
	})
}

func (span *recorderSpan) AddEvent(name string, attributes telemetry.SpanAttributes) {
	elapsed := telemetry.Microseconds(jsclock.PerformanceNow() - span.clock)
	copied := CopyAttributes(attributes)
	span.record(func() { span.events = append(span.events, event{name: name, timeMs: elapsed, attributes: copied}) })
}

func (span *recorderSpan) SetAttributes(attributes telemetry.SpanAttributes) {
	copied := CopyAttributes(attributes)
	span.record(func() { span.attributes = span.attributes.Merge(copied) })
}

func (span *recorderSpan) SetStatus(status telemetry.SpanStatus) {
	span.record(func() {
		span.status = telemetry.SpanStatus{Status: status.Status}
		if status.Status == telemetry.SpanStatusCodeError && status.Error != nil {
			span.status.Error = &telemetry.SpanStatusError{Name: status.Error.Name, Message: status.Error.Message}
		}
		span.explicit = true
	})
}

// CopyAttributes is a detached bag without undefined (nil) values: a name written twice keeps its first position and
// takes its last value as an object literal does, so a later nil value removes the name; array values are copied.
func CopyAttributes(attributes telemetry.SpanAttributes) telemetry.SpanAttributes {
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

func cloneAttributeValue(value telemetry.AttributeValue) telemetry.AttributeValue {
	if reflected := reflect.ValueOf(value); reflected.Kind() == reflect.Slice {
		cloned := reflect.MakeSlice(reflected.Type(), reflected.Len(), reflected.Len())
		reflect.Copy(cloned, reflected)
		return cloned.Interface()
	}
	return value
}

// copyLink drops a link whose target has no recorded identity.
func copyLink(spanLink telemetry.SpanLink) (link, bool) {
	owner, ok := spanLink.Context.(identified)
	if !ok {
		return link{}, false
	}
	identity := owner.recorderIdentity()
	return link{traceID: identity.traceID, spanID: identity.id, attributes: CopyAttributes(spanLink.Attributes)}, true
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

func (recorder *Recorder) start(parent *identity, options telemetry.SpanOptions, callback func(telemetry.TelemetrySpan) error) (err error) {
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
	if recorder.prefix == nil {
		prefix := recorder.scope()
		recorder.prefix = &prefix
	}
	recorder.sequence++
	id := *recorder.prefix + ":" + strconv.Itoa(recorder.sequence)
	// The start on the clock pi-telemetry's adapters share; the end and events by the monotonic clock from it.
	span := &recorderSpan{
		owner: recorder, identity: identity{id: id, traceID: id}, name: options.Name, started: telemetry.TelemetryNow(),
		clock: jsclock.PerformanceNow(), attributes: CopyAttributes(options.Attributes), links: links,
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

func (span *recorderSpan) settle(failed bool, failure any) {
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
	if len(recorder.spans)+recorder.inFlight >= recorder.capacity {
		recorder.droppedSpans++
		return
	}
	events := make([]telemetry.StoredSpanEvent, len(span.events))
	for i, recorded := range span.events {
		events[i] = telemetry.StoredSpanEvent{Name: recorded.name, TimeMs: recorded.timeMs, Attributes: recorded.attributes}
	}
	links := make([]telemetry.StoredSpanLink, len(span.links))
	for i, recorded := range span.links {
		attributes := recorded.attributes
		links[i] = telemetry.StoredSpanLink{TraceID: recorded.traceID, SpanID: recorded.spanID, Attributes: attributes}
	}
	recorder.spans = append(recorder.spans, telemetry.StoredSpan{
		ID: span.identity.id, TraceID: span.identity.traceID, ParentID: span.parentID, Name: span.name,
		StartedMs: span.started, EndedMs: telemetry.Microseconds(span.started + jsclock.PerformanceNow() - span.clock),
		Attributes: span.attributes, Events: events, Links: links, Status: span.status,
	})
}

type counter struct {
	add func(float64, telemetry.SpanAttributes)
}

func (c counter) Add(value float64, attributes telemetry.SpanAttributes) { c.add(value, attributes) }

type histogram struct {
	record func(float64, telemetry.SpanAttributes)
}

func (h histogram) Record(value float64, attributes telemetry.SpanAttributes, _ telemetry.TelemetryContext) {
	h.record(value, attributes)
}

// Counter creates a counter; its description is not retained.
func (recorder *Recorder) Counter(name string, options telemetry.InstrumentOptions) telemetry.TelemetryCounter {
	return counter{recorder.measurement(telemetry.TelemetryMetricKindCounter, name, options)}
}

// UpDownCounter creates an up-down counter.
func (recorder *Recorder) UpDownCounter(name string, options telemetry.InstrumentOptions) telemetry.TelemetryUpDownCounter {
	return counter{recorder.measurement(telemetry.TelemetryMetricKindUpDownCounter, name, options)}
}

// Histogram creates a histogram; descriptions and boundaries are not retained and the exemplar context is ignored.
func (recorder *Recorder) Histogram(name string, options telemetry.HistogramOptions) telemetry.TelemetryHistogram {
	return histogram{recorder.measurement(telemetry.TelemetryMetricKindHistogram, name, options.InstrumentOptions)}
}

func (recorder *Recorder) measurement(kind telemetry.TelemetryMetricKind, name string, options telemetry.InstrumentOptions) func(float64, telemetry.SpanAttributes) {
	var unit *string
	if options.Unit != nil {
		unit = new(*options.Unit)
	}
	return func(value float64, attributes telemetry.SpanAttributes) {
		recorder.merge(telemetry.StoredMetric{
			IntervalMs: intervalOf(jsclock.DateNow()), Name: name, Kind: kind, Unit: unit,
			Attributes: SortedAttributes(CopyAttributes(attributes)), Count: 1, Sum: value, Min: value, Max: value,
		})
	}
}

// merge adds a metric to its series' pending interval (a new series within the capacity, else dropped and counted).
func (recorder *Recorder) merge(stored telemetry.StoredMetric) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.stopped {
		return
	}
	key := SeriesKey(stored)
	pendingKey := strconv.FormatFloat(stored.IntervalMs, 'f', -1, 64) + ":" + key
	previous := recorder.measurements[pendingKey]
	if previous == nil {
		if len(recorder.measurements)+recorder.inFlightMeasurements >= recorder.capacity {
			recorder.droppedMeasurements++
			return
		}
		recorder.measurements[pendingKey] = &metric{StoredMetric: stored, key: key}
		recorder.order = append(recorder.order, pendingKey)
		return
	}
	previous.Count += stored.Count
	previous.Sum += stored.Sum
	previous.Min = math.Min(previous.Min, stored.Min)
	previous.Max = math.Max(previous.Max, stored.Max)
}

// Take takes the queued spans (at most limit, the oldest; a negative limit takes all) and the metric intervals that are
// over (every one with partial); the spans keep counting against the capacity until Release.
func (recorder *Recorder) Take(partial bool, limit int) TelemetryBatch {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	spans := recorder.spans
	if limit >= 0 && len(spans) > limit {
		recorder.spans = slices.Clone(spans[limit:])
		spans = spans[:limit]
	} else {
		recorder.spans = nil
	}
	recorder.inFlight = len(spans)
	var metrics []telemetry.StoredMetric
	current := intervalOf(jsclock.DateNow())
	remaining := recorder.order[:0:0]
	for _, key := range recorder.order {
		pending := recorder.measurements[key]
		if !partial && pending.IntervalMs >= current {
			remaining = append(remaining, key)
			continue
		}
		metrics = append(metrics, pending.StoredMetric)
		delete(recorder.measurements, key)
	}
	recorder.order = remaining
	recorder.inFlightMeasurements = len(metrics)
	return TelemetryBatch{Spans: spans, Metrics: metrics}
}

// Release says the batch taken last was written or given up: its capacity is free again.
func (recorder *Recorder) Release() {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.inFlight = 0
	recorder.inFlightMeasurements = 0
}

// QueuedSpans is how many settled spans wait to be taken.
func (recorder *Recorder) QueuedSpans() int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return len(recorder.spans)
}

// Pending reports whether spans, or metric intervals that are over (all of them with partial), wait to be taken.
func (recorder *Recorder) Pending(partial bool) bool {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.spans) > 0 {
		return true
	}
	current := intervalOf(jsclock.DateNow())
	for _, pending := range recorder.measurements {
		if partial || pending.IntervalMs < current {
			return true
		}
	}
	return false
}

// Ingest queues spans and metric rows another process recorded, within the capacity; it returns how many spans it queued
// (the rest dropped and counted).
func (recorder *Recorder) Ingest(batch TelemetryBatch) int {
	recorder.mu.Lock()
	if recorder.stopped {
		recorder.mu.Unlock()
		return 0
	}
	queued := 0
	for _, span := range batch.Spans {
		if len(recorder.spans)+recorder.inFlight >= recorder.capacity {
			recorder.droppedSpans++
			continue
		}
		recorder.spans = append(recorder.spans, span)
		queued++
	}
	recorder.mu.Unlock()
	for _, stored := range batch.Metrics {
		recorder.merge(stored)
	}
	return queued
}

// Stop stops recording; with discard, what waits is dropped too (a failed store).
func (recorder *Recorder) Stop(discard bool) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.stopped = true
	if discard {
		recorder.spans = nil
		recorder.measurements = map[string]*metric{}
		recorder.order = nil
	}
}

func intervalOf(time float64) float64 { return math.Floor(time/IntervalMs) * IntervalMs }

// SeriesKey is a metric's series: its name, kind, unit and attributes (sorted by key), as JSON.
func SeriesKey(stored telemetry.StoredMetric) string {
	encoded := string(AppendAttributes(nil, SortedAttributes(stored.Attributes)))
	dst := []byte{'['}
	dst = jsstring.AppendString(dst, stored.Name)
	dst = append(dst, ',')
	dst = jsstring.AppendString(dst, string(stored.Kind))
	dst = append(dst, ',')
	if stored.Unit == nil {
		dst = append(dst, "null"...)
	} else {
		dst = jsstring.AppendString(dst, *stored.Unit)
	}
	dst = append(dst, ',')
	dst = jsstring.AppendString(dst, encoded)
	return string(append(dst, ']'))
}

// SortedAttributes is attributes with their keys in order (a series' attributes as stored): the keys sorted by UTF-16
// code unit and then listed as an object lists them (array-index names first, ascending).
func SortedAttributes(attributes telemetry.SpanAttributes) telemetry.SpanAttributes {
	sorted := slices.Clone(attributes)
	slices.SortFunc(sorted, func(a, b telemetry.SpanAttribute) int {
		return slices.Compare(jsstring.ToUTF16(a.Name), jsstring.ToUTF16(b.Name))
	})
	var canonical telemetry.SpanAttributes
	for _, attribute := range sorted {
		canonical = canonical.Set(attribute.Name, attribute.Value)
	}
	return canonical
}

// AppendNumber is JSON.stringify of a number: a non-finite value is null.
func AppendNumber(dst []byte, value float64) []byte {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return append(dst, "null"...)
	}
	return append(dst, jsnumber.String(value)...)
}

func appendValue(dst []byte, value telemetry.AttributeValue) []byte {
	switch typed := value.(type) {
	case nil:
		return append(dst, "null"...)
	case string:
		return jsstring.AppendString(dst, typed)
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
		return AppendNumber(dst, reflected.Float())
	}
	encoded, err := jsstring.AppendMarshalJSON(dst, value)
	if err != nil {
		return append(dst, "null"...)
	}
	return encoded
}

// AppendAttributes is JSON.stringify of an attribute object: members in insertion order.
func AppendAttributes(dst []byte, attributes telemetry.SpanAttributes) []byte {
	dst = append(dst, '{')
	for i, attribute := range attributes {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = jsstring.AppendString(dst, attribute.Name)
		dst = append(dst, ':')
		dst = appendValue(dst, attribute.Value)
	}
	return append(dst, '}')
}
