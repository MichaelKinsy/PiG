package telemetry

// Ports packages/telemetry/src/memory.ts.

import (
	"maps"
	"reflect"
	"sync"
)

// RecordedTelemetryEvent is a detached span event snapshot.
type RecordedTelemetryEvent struct {
	Name       string
	Attributes SpanAttributes
}

// RecordedTelemetrySpan is a detached snapshot in span-start order.
type RecordedTelemetrySpan struct {
	ID          int
	ParentID    *int
	Name        string
	Attributes  SpanAttributes
	Events      []RecordedTelemetryEvent
	Status      SpanStatus
	Settled     bool
	EndSequence *int
}

// InMemoryTelemetryContext records spans without an external backend. Its zero value is ready to use.
type InMemoryTelemetryContext struct {
	mu              sync.Mutex
	spans           []*memorySpan
	nextEndSequence int
}

type memorySpan struct {
	owner          *InMemoryTelemetryContext
	record         RecordedTelemetrySpan
	explicitStatus bool
}

func copyAttributes(attributes SpanAttributes) SpanAttributes {
	copy := SpanAttributes{}
	for name, value := range attributes {
		v := reflect.ValueOf(value)
		if v.IsValid() && v.Kind() == reflect.Slice {
			cloned := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
			reflect.Copy(cloned, v)
			value = cloned.Interface()
		}
		copy[name] = value
	}
	return copy
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

func (recorder *InMemoryTelemetryContext) start(parent *memorySpan, options SpanOptions, callback func(TelemetrySpan) error) (err error) {
	recorder.mu.Lock()
	if parent != nil && parent.record.Settled {
		recorder.mu.Unlock()
		return NoopTelemetryContext.StartSpan(options, callback)
	}
	span := &memorySpan{owner: recorder, record: RecordedTelemetrySpan{
		ID: len(recorder.spans) + 1, Name: options.Name, Attributes: copyAttributes(options.Attributes),
		Events: []RecordedTelemetryEvent{}, Status: SpanStatus{Status: SpanStatusCodeOK},
	}}
	if parent != nil {
		span.record.ParentID = new(parent.record.ID)
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
		if recover() != nil {
			// Upstream memory.ts:automaticErrorStatus treats error inspection as passive.
			status = SpanStatus{Status: SpanStatusCodeError}
		}
	}()
	if err, ok := failure.(error); ok {
		status.Error = &SpanStatusError{Name: "Error", Message: err.Error()}
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
	return span.owner.start(span, options, callback)
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
