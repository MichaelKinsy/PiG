package telemetry

// Ports packages/telemetry/src/memory.ts.

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/jsclock"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// RecordedTelemetryEvent is a detached span event snapshot.
type RecordedTelemetryEvent struct {
	Name       string
	Attributes SpanAttributes
}

// RecordedTelemetryLink is a detached span link snapshot: the target's carrier and the link's copied attributes.
type RecordedTelemetryLink struct {
	Context    TelemetryCarrier
	Attributes SpanAttributes
}

// RecordedTelemetrySpan is a detached snapshot in span-start order. ParentID is the local parent's ID, nil for roots and
// for parents owned by another recorder. Context is the span's carrier; ParentContext is the parent's carrier, nil
// (upstream's null) for a root.
type RecordedTelemetrySpan struct {
	ID            int
	ParentID      *int
	Context       TelemetryCarrier
	ParentContext TelemetryCarrier
	Links         []RecordedTelemetryLink
	Name          string
	Attributes    SpanAttributes
	Events        []RecordedTelemetryEvent
	Status        SpanStatus
	Settled       bool
	EndSequence   *int
}

// RecordedTelemetryMeasurement is a detached measurement snapshot in recording order. Description, Unit and Boundaries
// are nil (upstream's absent members) when the instrument declared none; Context is the exemplar's carrier, nil
// (upstream's null) when the measurement had no recognized exemplar.
type RecordedTelemetryMeasurement struct {
	Kind        TelemetryMetricKind
	Name        string
	Description *string
	Unit        *string
	Boundaries  []float64
	Value       float64
	Attributes  SpanAttributes
	Context     TelemetryCarrier
}

// InMemoryTelemetryContext records spans and measurements without an external backend. It implements TelemetryContext,
// TelemetryPropagation and TelemetryMetrics. Create a fresh instance to isolate tests or independent recording scopes
// (memory.ts:190-197).
type InMemoryTelemetryContext struct {
	mu              sync.Mutex
	scope           string
	spans           []*memorySpan
	measurements    []timedMeasurement
	nextEndSequence int
}

var (
	_ TelemetryContext     = (*InMemoryTelemetryContext)(nil)
	_ TelemetryPropagation = (*InMemoryTelemetryContext)(nil)
	_ TelemetryMetrics     = (*InMemoryTelemetryContext)(nil)
	_ TelemetryStore       = (*InMemoryTelemetryContext)(nil)
)

const (
	carrierScopeKey = "pi-memory-scope"
	carrierSpanKey  = "pi-memory-span"
	carrierTraceKey = "pi-memory-trace"
)

// scopeLocked is the recorder's unique scope, created on first use so the zero value stays ready to use. It is
// crypto.randomUUID(): a random (version 4) UUID.
func (recorder *InMemoryTelemetryContext) scopeLocked() string {
	if recorder.scope == "" {
		var id [16]byte
		_, _ = rand.Read(id[:])
		id[6] = id[6]&0x0f | 0x40
		id[8] = id[8]&0x3f | 0x80
		recorder.scope = fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16])
	}
	return recorder.scope
}

// timedMeasurement is a measurement with the start of the 10 s interval it was recorded in.
type timedMeasurement struct {
	intervalMs  float64
	measurement RecordedTelemetryMeasurement
}

// carrierID is upstream's carrierId: the global span ID of a carrier.
func carrierID(carrier TelemetryCarrier) string {
	return carrier[carrierScopeKey] + ":" + carrier[carrierSpanKey]
}

// carrierOwner is implemented by every context the in-memory recorder hands out, a span or an extracted parent. It is
// the Go form of upstream's module-level contextCarriers WeakMap: identity survives settlement without exposing backend
// IDs on the generic context contract, and it is shared by every recorder, so a span of one recorder is a link target
// and an injectable context of any other.
type carrierOwner interface{ memoryCarrier() TelemetryCarrier }

// carrierOf is contextCarriers.get(context): nil for a nil, no-op, root or foreign context.
func carrierOf(context TelemetryContext) TelemetryCarrier {
	if owner, ok := context.(carrierOwner); ok {
		return owner.memoryCarrier()
	}
	return nil
}

// inMemoryParent is upstream's InMemoryParent: the parent's carrier, its local span ID when this recorder owns it, and
// the span itself when the parent is a span of this recorder (an extracted parent has none and never settles).
type inMemoryParent struct {
	context TelemetryCarrier
	localID *int
	span    *memorySpan
}

// memoryRemoteParent is the parent Extract returns: it only starts spans.
type memoryRemoteParent struct {
	owner  *InMemoryTelemetryContext
	parent inMemoryParent
}

func (remote *memoryRemoteParent) StartSpan(options SpanOptions, callback func(TelemetrySpan) error) error {
	return remote.owner.start(&remote.parent, options, callback)
}

func (remote *memoryRemoteParent) Metrics() TelemetryMetrics         { return remote.owner }
func (remote *memoryRemoteParent) Propagation() TelemetryPropagation { return remote.owner }
func (remote *memoryRemoteParent) memoryCarrier() TelemetryCarrier   { return remote.parent.context }

// NewInMemoryTelemetryContext is `new InMemoryTelemetryContext()`: an empty recorder; its zero value is equally ready to use.
func NewInMemoryTelemetryContext() *InMemoryTelemetryContext { return &InMemoryTelemetryContext{} }

type memorySpan struct {
	owner          *InMemoryTelemetryContext
	record         RecordedTelemetrySpan
	startedMs      float64
	clock          float64
	endedMs        float64
	eventTimes     []float64
	explicitStatus bool
	asParent       inMemoryParent
}

func (span *memorySpan) Metrics() TelemetryMetrics         { return span.owner }
func (span *memorySpan) Propagation() TelemetryPropagation { return span.owner }
func (span *memorySpan) memoryCarrier() TelemetryCarrier   { return span.record.Context }

// copyAttributes is upstream's copyAttributes: a detached bag in insertion order, without undefined (nil) values and
// with each name once, a repeated name keeping its first position and taking its last value as an object literal does,
// so a later undefined value removes the name.
func copyAttributes(attributes SpanAttributes) SpanAttributes {
	copied := make(SpanAttributes, 0, len(attributes))
	undefined := false
	for _, attribute := range attributes {
		if attribute.Value == nil {
			undefined = true
		}
		copied = copied.Set(attribute.Name, cloneAttributeValue(attribute.Value))
	}
	if undefined {
		copied = slices.DeleteFunc(copied, func(attribute SpanAttribute) bool { return attribute.Value == nil })
	}
	return copied
}

// cloneAttributeValue copies an array value, as upstream's `[...value]`.
func cloneAttributeValue(value AttributeValue) AttributeValue {
	switch typed := value.(type) {
	case string, bool, int, int64, float64:
		return value
	case []string:
		return slices.Clone(typed)
	case []float64:
		return slices.Clone(typed)
	case []bool:
		return slices.Clone(typed)
	}
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Slice {
		cloned := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		reflect.Copy(cloned, v)
		return cloned.Interface()
	}
	return value
}

// copyLink is upstream's copyLink: a link whose target has no recorded identity is dropped.
func copyLink(link SpanLink) (RecordedTelemetryLink, bool) {
	context := carrierOf(link.Context)
	if context == nil {
		return RecordedTelemetryLink{}, false
	}
	return RecordedTelemetryLink{Context: maps.Clone(context), Attributes: copyAttributes(link.Attributes)}, true
}

func copyStatus(status SpanStatus) SpanStatus {
	if status.Status == SpanStatusCodeOK {
		return SpanStatus{Status: SpanStatusCodeOK}
	}
	if status.Error != nil {
		status.Error = new(*status.Error)
	}
	return status
}

// StartSpan invokes and waits for callback, recording its settlement even when it returns an error or panics.
func (recorder *InMemoryTelemetryContext) StartSpan(options SpanOptions, callback func(TelemetrySpan) error) error {
	return recorder.start(nil, options, callback)
}

func (recorder *InMemoryTelemetryContext) start(parent *inMemoryParent, options SpanOptions, callback func(TelemetrySpan) error) (err error) {
	recorder.mu.Lock()
	if parent != nil && parent.span != nil && parent.span.record.Settled {
		recorder.mu.Unlock()
		return (&inertSpan{owner: recorder}).StartSpan(options, callback)
	}
	links := []RecordedTelemetryLink{}
	for _, link := range options.Links {
		if copied, ok := copyLink(link); ok {
			links = append(links, copied)
		}
	}
	id := len(recorder.spans) + 1
	scope := recorder.scopeLocked()
	trace := scope + ":" + strconv.Itoa(id)
	if parent != nil {
		if inherited, ok := parent.context[carrierTraceKey]; ok {
			trace = inherited
		}
	}
	span := &memorySpan{owner: recorder, startedMs: jsclock.DateNow(), clock: jsclock.PerformanceNow(), record: RecordedTelemetrySpan{
		ID: id, Name: options.Name, Attributes: copyAttributes(options.Attributes),
		Context: TelemetryCarrier{carrierScopeKey: scope, carrierSpanKey: strconv.Itoa(id), carrierTraceKey: trace},
		Links:   links, Events: []RecordedTelemetryEvent{}, Status: SpanStatus{Status: SpanStatusCodeOK},
	}}
	span.asParent = inMemoryParent{context: span.record.Context, localID: &span.record.ID, span: span}
	if parent != nil {
		if parent.localID != nil {
			span.record.ParentID = new(*parent.localID)
		}
		span.record.ParentContext = maps.Clone(parent.context)
	}
	recorder.spans = append(recorder.spans, span)
	recorder.mu.Unlock()
	defer func() {
		if failure := recover(); failure != nil {
			span.settle(true, failure)
			panic(failure)
		}
		span.settle(err != nil, err)
	}()
	return callback(span)
}

// inertSpan is upstream's startInertSpan span: a no-op span that keeps the recorder's capabilities, started when the
// parent has settled.
type inertSpan struct{ owner *InMemoryTelemetryContext }

func (span *inertSpan) Metrics() TelemetryMetrics         { return span.owner }
func (span *inertSpan) Propagation() TelemetryPropagation { return span.owner }
func (span *inertSpan) StartSpan(_ SpanOptions, callback func(TelemetrySpan) error) error {
	return callback(&inertSpan{owner: span.owner})
}
func (*inertSpan) AddLink(SpanLink)                {}
func (*inertSpan) AddEvent(string, SpanAttributes) {}
func (*inertSpan) SetAttributes(SpanAttributes)    {}
func (*inertSpan) SetStatus(SpanStatus)            {}

// automaticErrorStatus is the status of a span whose callback failed without an explicit status: the thrown value's own
// `error.name` and message for an error, otherwise a bare error.
func automaticErrorStatus(failure any) SpanStatus {
	status := SpanStatus{Status: SpanStatusCodeError}
	if err, ok := failure.(error); ok {
		// The thrown value's own `error.name`: its Name method when it has one, otherwise "Error"; upstream reads no cause chain.
		name := "Error"
		if named, ok := err.(interface{ Name() string }); ok {
			name = named.Name()
		}
		status.Error = &SpanStatusError{Name: name, Message: err.Error()}
	}
	return status
}

func (span *memorySpan) settle(failed bool, failure any) {
	span.owner.mu.Lock()
	defer span.owner.mu.Unlock()
	if span.record.Settled {
		return
	}
	if failed && !span.explicitStatus {
		span.record.Status = automaticErrorStatus(failure)
	}
	span.record.Settled = true
	span.endedMs = span.startedMs + jsclock.PerformanceNow() - span.clock
	span.owner.nextEndSequence++
	span.record.EndSequence = new(span.owner.nextEndSequence)
}

func (span *memorySpan) StartSpan(options SpanOptions, callback func(TelemetrySpan) error) error {
	return span.owner.start(&span.asParent, options, callback)
}

func (span *memorySpan) AddLink(link SpanLink) {
	span.owner.mu.Lock()
	defer span.owner.mu.Unlock()
	if span.record.Settled {
		return
	}
	if copied, ok := copyLink(link); ok {
		span.record.Links = append(span.record.Links, copied)
	}
}

func (span *memorySpan) AddEvent(name string, attributes SpanAttributes) {
	span.owner.mu.Lock()
	defer span.owner.mu.Unlock()
	if span.record.Settled {
		return
	}
	span.record.Events = append(span.record.Events, RecordedTelemetryEvent{Name: name, Attributes: copyAttributes(attributes)})
	span.eventTimes = append(span.eventTimes, jsclock.PerformanceNow()-span.clock)
	span.eventTimes = append(span.eventTimes, jsclock.PerformanceNow()-span.clock)
}

func (span *memorySpan) SetAttributes(attributes SpanAttributes) {
	span.owner.mu.Lock()
	defer span.owner.mu.Unlock()
	if span.record.Settled {
		return
	}
	for _, attribute := range copyAttributes(attributes) {
		span.record.Attributes = span.record.Attributes.Set(attribute.Name, attribute.Value)
	}
}

func (span *memorySpan) SetStatus(status SpanStatus) {
	span.owner.mu.Lock()
	defer span.owner.mu.Unlock()
	if span.record.Settled {
		return
	}
	span.record.Status = copyStatus(status)
	span.explicitStatus = true
}

// GetSpans returns detached snapshots in span-start order.
func (recorder *InMemoryTelemetryContext) GetSpans() []RecordedTelemetrySpan {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	result := make([]RecordedTelemetrySpan, len(recorder.spans))
	for i, span := range recorder.spans {
		value := span.record
		if value.ParentID != nil {
			value.ParentID = new(*value.ParentID)
		}
		if value.EndSequence != nil {
			value.EndSequence = new(*value.EndSequence)
		}
		value.Context = maps.Clone(value.Context)
		value.ParentContext = maps.Clone(value.ParentContext)
		value.Links = make([]RecordedTelemetryLink, len(span.record.Links))
		for j, link := range span.record.Links {
			value.Links[j] = RecordedTelemetryLink{Context: maps.Clone(link.Context), Attributes: copyAttributes(link.Attributes)}
		}
		value.Attributes = copyAttributes(value.Attributes)
		value.Status = copyStatus(value.Status)
		value.Events = make([]RecordedTelemetryEvent, len(span.record.Events))
		for j, event := range span.record.Events {
			value.Events[j] = RecordedTelemetryEvent{Name: event.Name, Attributes: copyAttributes(event.Attributes)}
		}
		result[i] = value
	}
	return result
}

// Inject returns a detached carrier for a span or extracted parent of any in-memory recorder, including a settled span,
// and nil for any other context: the root recorder, a no-op context or an unsupported one.
func (recorder *InMemoryTelemetryContext) Inject(context TelemetryContext) TelemetryCarrier {
	return maps.Clone(carrierOf(context))
}

// Extract returns a parent whose spans record the carrier's span as their parent context and continue its trace. An
// absent or incomplete carrier falls back to this recorder's root context. Only the three carrier keys are read, so a
// carrier with extra keys is accepted and its extras dropped; identities are opaque strings.
func (recorder *InMemoryTelemetryContext) Extract(carrier TelemetryCarrier) TelemetryContext {
	scope, hasScope := carrier[carrierScopeKey]
	span, hasSpan := carrier[carrierSpanKey]
	trace, hasTrace := carrier[carrierTraceKey]
	if !hasScope || !hasSpan || !hasTrace {
		return recorder
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	remote := &memoryRemoteParent{owner: recorder, parent: inMemoryParent{context: TelemetryCarrier{carrierScopeKey: scope, carrierSpanKey: span, carrierTraceKey: trace}}}
	if scope == recorder.scopeLocked() {
		// localId = Number(span). A span that is not an integer (NaN, a fraction) names no local span, so Go leaves the parent unset where Pi keeps the number.
		if number := jsnumber.Parse(span); number == math.Trunc(number) && math.Abs(number) <= 1<<53 {
			id := int(number) //portlint:allow numbers number is a finite integer (NaN fails the Trunc equality) within 2^53, so the conversion is exact
			remote.parent.localID = &id
		}
	}
	return remote
}

// measurementRecorder is upstream's createMeasurementRecorder result: the instrument metadata and the recorder it
// appends to. Typed inputs are trusted, so every measurement is recorded.
type measurementRecorder struct {
	owner       *InMemoryTelemetryContext
	kind        TelemetryMetricKind
	name        string
	description *string
	unit        *string
	boundaries  []float64
}

func newMeasurementRecorder(owner *InMemoryTelemetryContext, kind TelemetryMetricKind, name string, options HistogramOptions) *measurementRecorder {
	recorder := &measurementRecorder{owner: owner, kind: kind, name: name}
	if options.Description != nil {
		recorder.description = new(*options.Description)
	}
	if options.Unit != nil {
		recorder.unit = new(*options.Unit)
	}
	if kind == TelemetryMetricKindHistogram && options.Boundaries != nil {
		recorder.boundaries = slices.Clone(options.Boundaries)
	}
	return recorder
}

func (recorder *measurementRecorder) record(value float64, attributes SpanAttributes, context TelemetryContext) {
	measurement := RecordedTelemetryMeasurement{
		Kind: recorder.kind, Name: recorder.name, Description: recorder.description, Unit: recorder.unit, Boundaries: recorder.boundaries,
		Value: value, Attributes: copyAttributes(attributes), Context: maps.Clone(carrierOf(context)),
	}
	interval := math.Floor(jsclock.DateNow()/10_000) * 10_000
	recorder.owner.mu.Lock()
	defer recorder.owner.mu.Unlock()
	recorder.owner.measurements = append(recorder.owner.measurements, timedMeasurement{intervalMs: interval, measurement: measurement})
}

type memoryCounter struct{ recorder *measurementRecorder }

func (counter memoryCounter) Add(value float64, attributes SpanAttributes) {
	counter.recorder.record(value, attributes, nil)
}

type memoryHistogram struct{ recorder *measurementRecorder }

func (histogram memoryHistogram) Record(value float64, attributes SpanAttributes, context TelemetryContext) {
	histogram.recorder.record(value, attributes, context)
}

// Counter creates a counter that records non-negative finite increments. Metadata is copied when the instrument is
// created, and an instrument whose metadata is invalid records nothing.
func (recorder *InMemoryTelemetryContext) Counter(name string, options InstrumentOptions) TelemetryCounter {
	return memoryCounter{newMeasurementRecorder(recorder, TelemetryMetricKindCounter, name, HistogramOptions{InstrumentOptions: options})}
}

// UpDownCounter creates a counter that records finite signed increments.
func (recorder *InMemoryTelemetryContext) UpDownCounter(name string, options InstrumentOptions) TelemetryUpDownCounter {
	return memoryCounter{newMeasurementRecorder(recorder, TelemetryMetricKindUpDownCounter, name, HistogramOptions{InstrumentOptions: options})}
}

// Histogram creates a histogram that records finite signed values. Invalid boundaries (non-finite or not strictly
// increasing) leave an instrument that records nothing.
func (recorder *InMemoryTelemetryContext) Histogram(name string, options HistogramOptions) TelemetryHistogram {
	return memoryHistogram{newMeasurementRecorder(recorder, TelemetryMetricKindHistogram, name, options)}
}

// GetMetrics returns detached measurements in recording order, without aggregation.
func (recorder *InMemoryTelemetryContext) GetMetrics() []RecordedTelemetryMeasurement {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	result := make([]RecordedTelemetryMeasurement, len(recorder.measurements))
	for i, timed := range recorder.measurements {
		measurement := timed.measurement
		if measurement.Description != nil {
			measurement.Description = new(*measurement.Description)
		}
		if measurement.Unit != nil {
			measurement.Unit = new(*measurement.Unit)
		}
		measurement.Boundaries = slices.Clone(measurement.Boundaries)
		measurement.Attributes = copyAttributes(measurement.Attributes)
		measurement.Context = maps.Clone(measurement.Context)
		result[i] = measurement
	}
	return result
}

// Metrics is the recorder's own metrics capability, upstream's `metrics = this`.
func (recorder *InMemoryTelemetryContext) Metrics() TelemetryMetrics { return recorder }

// Propagation is the recorder's own propagation capability, upstream's `propagation = this`.
func (recorder *InMemoryTelemetryContext) Propagation() TelemetryPropagation { return recorder }

// storedSpansLocked is upstream's storedSpans: settled spans only, ordered by start time and then span ID.
func (recorder *InMemoryTelemetryContext) storedSpansLocked() []StoredSpan {
	var stored []StoredSpan
	for _, span := range recorder.spans {
		if !span.record.Settled {
			continue
		}
		value := StoredSpan{
			ID: carrierID(span.record.Context), TraceID: span.record.Context[carrierTraceKey], Name: span.record.Name,
			StartedMs: span.startedMs, EndedMs: span.endedMs, Attributes: copyAttributes(span.record.Attributes),
			Events: []StoredSpanEvent{}, Links: []StoredSpanLink{}, Status: copyStatus(span.record.Status),
		}
		if span.record.ParentContext != nil {
			value.ParentID = new(carrierID(span.record.ParentContext))
		}
		for index, event := range span.record.Events {
			value.Events = append(value.Events, StoredSpanEvent{Name: event.Name, TimeMs: span.eventTimes[index], Attributes: copyAttributes(event.Attributes)})
		}
		for _, link := range span.record.Links {
			value.Links = append(value.Links, StoredSpanLink{TraceID: link.Context[carrierTraceKey], SpanID: carrierID(link.Context), Attributes: copyAttributes(link.Attributes)})
		}
		stored = append(stored, value)
	}
	sort.SliceStable(stored, func(left, right int) bool {
		if stored[left].StartedMs != stored[right].StartedMs {
			return stored[left].StartedMs < stored[right].StartedMs
		}
		return stored[left].ID < stored[right].ID
	})
	return stored
}

// GetTrace returns the settled spans of one trace, or nil when it has none.
func (recorder *InMemoryTelemetryContext) GetTrace(traceID string) (*StoredTrace, error) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	var spans []StoredSpan
	for _, span := range recorder.storedSpansLocked() {
		if span.TraceID == traceID {
			spans = append(spans, span)
		}
	}
	if len(spans) == 0 {
		return nil, nil
	}
	return &StoredTrace{TraceID: traceID, Spans: spans}, nil
}

// attributeEqual is upstream's `===` on a string, number or boolean: numbers are equal by value whatever their Go type.
func attributeEqual(left, right AttributeValue) bool {
	leftNumber, leftIsNumber := numberOf(left)
	rightNumber, rightIsNumber := numberOf(right)
	if leftIsNumber || rightIsNumber {
		return leftIsNumber && rightIsNumber && leftNumber == rightNumber
	}
	return left == right
}

func numberOf(value AttributeValue) (float64, bool) {
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(reflected.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(reflected.Uint()), true
	case reflect.Float32, reflect.Float64:
		return reflected.Float(), true
	default:
		return 0, false
	}
}

// attributesMatch is upstream's `Object.entries(query).every(([key, value]) => attributes[key] === value)`.
func attributesMatch(query map[string]AttributeValue, attributes SpanAttributes) bool {
	for key, want := range query {
		got, ok := attributes.Get(key)
		if !ok || !attributeEqual(got, want) {
			return false
		}
	}
	return true
}

// FindTraces filters settled traces by their earliest span's name and start, by any error span and by attributes that
// all match one span, newest first, and pages them with an opaque cursor.
func (recorder *InMemoryTelemetryContext) FindTraces(query TraceQuery) (FindTracesResult, error) {
	limit := 20
	if query.Limit != nil {
		limit = *query.Limit
	}
	var after struct {
		set     bool
		started float64
		traceID string
	}
	if query.Cursor != nil {
		var decoded [2]json.RawMessage
		if err := json.Unmarshal([]byte(*query.Cursor), &decoded); err != nil {
			return FindTracesResult{}, err
		}
		if err := json.Unmarshal(decoded[0], &after.started); err != nil {
			return FindTracesResult{}, err
		}
		if err := json.Unmarshal(decoded[1], &after.traceID); err != nil {
			return FindTracesResult{}, err
		}
		after.set = true
	}
	recorder.mu.Lock()
	grouped := map[string]*StoredTrace{}
	var traces []*StoredTrace
	for _, span := range recorder.storedSpansLocked() {
		trace, ok := grouped[span.TraceID]
		if !ok {
			trace = &StoredTrace{TraceID: span.TraceID}
			grouped[span.TraceID] = trace
			traces = append(traces, trace)
		}
		trace.Spans = append(trace.Spans, span)
	}
	recorder.mu.Unlock()
	var matches []*StoredTrace
	for _, trace := range traces {
		first := trace.Spans[0]
		if query.Name != nil && first.Name != *query.Name {
			continue
		}
		if query.StartMin != nil && first.StartedMs < *query.StartMin || query.StartMax != nil && first.StartedMs > *query.StartMax {
			continue
		}
		if after.set && !(first.StartedMs < after.started || first.StartedMs == after.started && trace.TraceID > after.traceID) {
			continue
		}
		if query.Errors && !slices.ContainsFunc(trace.Spans, func(span StoredSpan) bool { return span.Status.Status == SpanStatusCodeError }) {
			continue
		}
		if query.Attributes != nil && !slices.ContainsFunc(trace.Spans, func(span StoredSpan) bool { return attributesMatch(query.Attributes, span.Attributes) }) {
			continue
		}
		matches = append(matches, trace)
	}
	sort.SliceStable(matches, func(left, right int) bool {
		if matches[left].Spans[0].StartedMs != matches[right].Spans[0].StartedMs {
			return matches[left].Spans[0].StartedMs > matches[right].Spans[0].StartedMs
		}
		return matches[left].TraceID < matches[right].TraceID
	})
	page := matches
	if len(page) > limit {
		page = page[:limit]
	}
	result := FindTracesResult{Traces: make([]StoredTrace, len(page))}
	for index, trace := range page {
		result.Traces[index] = *trace
	}
	if len(matches) > limit {
		last := page[len(page)-1]
		cursor, err := jsstring.MarshalJSON([]any{last.Spans[0].StartedMs, last.TraceID})
		if err != nil {
			return FindTracesResult{}, err
		}
		result.Cursor = new(string(cursor))
	}
	return result, nil
}

// FindMetrics reads raw measurements as count-one rows, without changing GetMetrics snapshots.
func (recorder *InMemoryTelemetryContext) FindMetrics(query MetricQuery) ([]StoredMetric, error) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	rows := []StoredMetric{}
	for _, timed := range recorder.measurements {
		measurement := timed.measurement
		if measurement.Name != query.Name ||
			query.StartMin != nil && timed.intervalMs < *query.StartMin ||
			query.StartMax != nil && timed.intervalMs > *query.StartMax ||
			!attributesMatch(query.Attributes, measurement.Attributes) {
			continue
		}
		row := StoredMetric{
			IntervalMs: timed.intervalMs, Name: measurement.Name, Kind: measurement.Kind,
			Attributes: copyAttributes(measurement.Attributes), Count: 1, Sum: measurement.Value, Min: measurement.Value, Max: measurement.Value,
		}
		if measurement.Unit != nil {
			row.Unit = new(*measurement.Unit)
		}
		rows = append(rows, row)
	}
	return rows, nil
}
