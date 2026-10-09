package telemetry

import (
	"errors"
	"testing"
)

// packages/telemetry/src/memory.ts:22,38,71-86 (RecordedTelemetrySpan.status): a recorded span is ok by default, an
// explicit SetStatus wins over a failing callback, a failure without an explicit status records the error's name and
// message, and the snapshot's status is a copy.
func TestRecordedTelemetrySpanStatusFollowsDefaultExplicitAndAutomaticErrors(t *testing.T) {
	recorder := &InMemoryTelemetryContext{}
	failure := errors.New("boom")
	_ = recorder.StartSpan(SpanOptions{Name: "ok"}, func(TelemetrySpan) error { return nil })
	_ = recorder.StartSpan(SpanOptions{Name: "failed"}, func(TelemetrySpan) error { return failure })
	_ = recorder.StartSpan(SpanOptions{Name: "explicit"}, func(span TelemetrySpan) error {
		span.SetStatus(SpanStatus{Status: SpanStatusCodeError, Error: &SpanStatusError{Name: "Custom", Message: "mine"}})
		return failure
	})
	spans := recorder.GetSpans()
	if len(spans) != 3 {
		t.Fatalf("spans = %d, want 3", len(spans))
	}
	if got := spans[0].Status; got.Status != SpanStatusCodeOK || got.Error != nil {
		t.Errorf("ok span status = %+v", got)
	}
	if got := spans[1].Status; got.Status != SpanStatusCodeError || got.Error == nil || got.Error.Name != "Error" || got.Error.Message != "boom" {
		t.Errorf("failed span status = %+v", got)
	}
	if got := spans[2].Status; got.Status != SpanStatusCodeError || got.Error == nil || got.Error.Name != "Custom" || got.Error.Message != "mine" {
		t.Errorf("explicit span status = %+v", got)
	}
	spans[2].Status.Error.Message = "changed"
	if got := recorder.GetSpans()[2].Status.Error.Message; got != "mine" {
		t.Errorf("snapshot status is shared with the recorder: %q", got)
	}
}
