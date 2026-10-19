package telemetry

// Ports packages/telemetry/src/memory.ts.

import (
	"crypto/rand"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
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
	measurements    []RecordedTelemetryMeasurement
	nextEndSequence int
}

var (
	_ TelemetryContext     = (*InMemoryTelemetryContext)(nil)
	_ TelemetryPropagation = (*InMemoryTelemetryContext)(nil)
	_ TelemetryMetrics     = (*InMemoryTelemetryContext)(nil)
)

const (
	carrierScopeKey = "pi-memory-scope"
	carrierSpanKey  = "pi-memory-span"
	// maxSafeInteger is Number.MAX_SAFE_INTEGER: extraction accepts a span ID only when Number.isSafeInteger holds.
	maxSafeInteger = 1<<53 - 1
)

var (
	scopePattern = lazyregexp.New(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	spanPattern  = lazyregexp.New(`^[1-9][0-9]*$`)
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

func (remote *memoryRemoteParent) memoryCarrier() TelemetryCarrier { return remote.parent.context }

// NewInMemoryTelemetryContext is `new InMemoryTelemetryContext()`: an empty recorder; its zero value is equally ready to use.
func NewInMemoryTelemetryContext() *InMemoryTelemetryContext { return &InMemoryTelemetryContext{} }

type memorySpan struct {
	owner          *InMemoryTelemetryContext
	record         RecordedTelemetrySpan
	explicitStatus bool
	asParent       inMemoryParent
}

func (span *memorySpan) memoryCarrier() TelemetryCarrier { return span.record.Context }

func copyAttributes(attributes SpanAttributes) SpanAttributes {
	copy := SpanAttributes{}
	for name, value := range attributes {
		if value == nil {
			// A nil value is upstream's undefined: it is not recorded and does not overwrite a recorded value.
			continue
		}
		v := reflect.ValueOf(value)
		if v.Kind() == reflect.Slice {
			cloned := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
			reflect.Copy(cloned, v)
			value = cloned.Interface()
		}
		copy[name] = value
	}
	return copy
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
		return NoopTelemetryContext.StartSpan(options, callback)
	}
	links := []RecordedTelemetryLink{}
	for _, link := range options.Links {
		if copied, ok := copyLink(link); ok {
			links = append(links, copied)
		}
	}
	id := len(recorder.spans) + 1
	span := &memorySpan{owner: recorder, record: RecordedTelemetrySpan{
		ID: id, Name: options.Name, Attributes: copyAttributes(options.Attributes),
		Context: TelemetryCarrier{carrierScopeKey: recorder.scopeLocked(), carrierSpanKey: strconv.Itoa(id)},
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

func automaticErrorStatus(failure any) (status SpanStatus) {
	status = SpanStatus{Status: SpanStatusCodeError}
	defer func() {
		// upstream: telemetry/src/memory.ts:automaticErrorStatus
		if recover() != nil {
			// Error inspection is passive.
			status = SpanStatus{Status: SpanStatusCodeError}
		}
	}()
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
}

func (span *memorySpan) SetAttributes(attributes SpanAttributes) {
	span.owner.mu.Lock()
	defer span.owner.mu.Unlock()
	if span.record.Settled {
		return
	}
	maps.Copy(span.record.Attributes, copyAttributes(attributes))
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

// Extract returns a parent whose spans record the carrier's span as their parent context. An unrecognized or malformed
// carrier falls back to this recorder's root context. Only the two carrier keys are read, so a carrier with extra keys
// is accepted and its extras dropped.
func (recorder *InMemoryTelemetryContext) Extract(carrier TelemetryCarrier) TelemetryContext {
	scope, hasScope := carrier[carrierScopeKey]
	span, hasSpan := carrier[carrierSpanKey]
	if !hasScope || !hasSpan || !scopePattern.MatchString(scope) || !spanPattern.MatchString(span) {
		return recorder
	}
	// Number(span) is a safe integer exactly when it fits Number.MAX_SAFE_INTEGER; a longer digit string overflows ParseUint.
	//portlint:allow numbers span matched ^[1-9][0-9]*$, so Number(span) is its value; a longer digit string overflows ParseUint, and Number(span) of one is above Number.MAX_SAFE_INTEGER too
	number, err := strconv.ParseUint(span, 10, 64)
	if err != nil || number > maxSafeInteger {
		return recorder
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	remote := &memoryRemoteParent{owner: recorder, parent: inMemoryParent{context: TelemetryCarrier{carrierScopeKey: scope, carrierSpanKey: span}}}
	if scope == recorder.scopeLocked() && number <= uint64(len(recorder.spans)) {
		remote.parent.localID = new(int(number))
	}
	return remote
}

// measurementRecorder is upstream's createMeasurementRecorder result: the validated instrument metadata and the
// recorder it appends to. An invalid instrument records nothing.
type measurementRecorder struct {
	owner       *InMemoryTelemetryContext
	valid       bool
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
	if kind == TelemetryMetricKindHistogram {
		recorder.boundaries = slices.Clone(options.Boundaries)
	}
	for index, boundary := range recorder.boundaries {
		if math.IsNaN(boundary) || math.IsInf(boundary, 0) || (index > 0 && boundary <= recorder.boundaries[index-1]) {
			return recorder
		}
	}
	recorder.valid = true
	return recorder
}

func (recorder *measurementRecorder) record(value float64, attributes SpanAttributes, context TelemetryContext) {
	if !recorder.valid || math.IsNaN(value) || math.IsInf(value, 0) || (recorder.kind == TelemetryMetricKindCounter && value < 0) {
		return
	}
	measurement := RecordedTelemetryMeasurement{
		Kind: recorder.kind, Name: recorder.name, Description: recorder.description, Unit: recorder.unit, Boundaries: recorder.boundaries,
		Value: value, Attributes: copyAttributes(attributes), Context: maps.Clone(carrierOf(context)),
	}
	recorder.owner.mu.Lock()
	defer recorder.owner.mu.Unlock()
	recorder.owner.measurements = append(recorder.owner.measurements, measurement)
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
	for i, measurement := range recorder.measurements {
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
