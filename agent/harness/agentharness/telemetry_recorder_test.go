package agentharness

import (
	"maps"
	"sync"

	"github.com/MichaelKinsy/PiG/agent/harness"
)

// recordingTelemetry is a local in-memory recorder mirroring the parent/child
// bookkeeping of upstream InMemoryTelemetryContext for tests.
type recordingTelemetry struct {
	mu    sync.Mutex
	spans []*recordedSpan
}

type recordedSpan struct {
	recorder   *recordingTelemetry
	id         int
	parentID   int // 0 is the root
	name       string
	attributes harness.SpanAttributes
	status     *harness.SpanStatus
}

func (recorder *recordingTelemetry) start(parentID int, options harness.SpanOptions, callback func(span harness.TelemetrySpan) error) error {
	recorder.mu.Lock()
	span := &recordedSpan{recorder: recorder, id: len(recorder.spans) + 1, parentID: parentID, name: options.Name, attributes: harness.SpanAttributes{}}
	maps.Copy(span.attributes, options.Attributes)
	recorder.spans = append(recorder.spans, span)
	recorder.mu.Unlock()
	return callback(span)
}

func (recorder *recordingTelemetry) StartSpan(options harness.SpanOptions, callback func(span harness.TelemetrySpan) error) error {
	return recorder.start(0, options, callback)
}

func (recorder *recordingTelemetry) find(name string) *recordedSpan {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	for _, span := range recorder.spans {
		if span.name == name {
			return span
		}
	}
	return nil
}

func (recorder *recordingTelemetry) all(name string) []*recordedSpan {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	var spans []*recordedSpan
	for _, span := range recorder.spans {
		if span.name == name {
			spans = append(spans, span)
		}
	}
	return spans
}

func (span *recordedSpan) StartSpan(options harness.SpanOptions, callback func(span harness.TelemetrySpan) error) error {
	return span.recorder.start(span.id, options, callback)
}

func (span *recordedSpan) AddEvent(string, harness.SpanAttributes) {}

func (span *recordedSpan) SetAttributes(attributes harness.SpanAttributes) {
	span.recorder.mu.Lock()
	defer span.recorder.mu.Unlock()
	maps.Copy(span.attributes, attributes)
}

func (span *recordedSpan) SetStatus(status harness.SpanStatus) {
	span.recorder.mu.Lock()
	defer span.recorder.mu.Unlock()
	span.status = &status
}
