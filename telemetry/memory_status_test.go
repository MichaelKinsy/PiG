package telemetry_test

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry"
)

// packages/telemetry/src/memory.ts:114 a recorded span starts with status ok; memory.ts:96 a failing callback sets an error
// status carrying the error's name and message unless setStatus (memory.ts:160) already set one; memory.ts:214 getSpans
// returns a copy of each status.
func TestInMemoryRecordedSpanStatus(t *testing.T) {
	recorder := &telemetry.InMemoryTelemetryContext{}
	failure := errors.New("boom")
	_ = recorder.StartSpan(telemetry.SpanOptions{Name: "ok"}, func(telemetry.TelemetrySpan) error { return nil })
	if err := recorder.StartSpan(telemetry.SpanOptions{Name: "failed"}, func(telemetry.TelemetrySpan) error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("StartSpan = %v, want the callback's error", err)
	}
	_ = recorder.StartSpan(telemetry.SpanOptions{Name: "explicit"}, func(span telemetry.TelemetrySpan) error {
		span.SetStatus(telemetry.SpanStatus{Status: telemetry.SpanStatusCodeError, Error: &telemetry.SpanStatusError{Name: "RateLimit", Message: "slow down"}})
		return failure
	})

	spans := recorder.GetSpans()
	if len(spans) != 3 {
		t.Fatalf("spans = %+v", spans)
	}
	status := func(index int) (telemetry.SpanStatusCode, string, string) {
		got := spans[index].Status
		if got.Error == nil {
			return got.Status, "", ""
		}
		return got.Status, got.Error.Name, got.Error.Message
	}
	if code, name, _ := status(0); code != telemetry.SpanStatusCodeOK || name != "" {
		t.Errorf("ok span status = %+v", spans[0].Status)
	}
	if code, name, message := status(1); code != telemetry.SpanStatusCodeError || name != "Error" || message != "boom" {
		t.Errorf("failed span status = %+v, want error Error/boom", spans[1].Status)
	}
	if code, name, message := status(2); code != telemetry.SpanStatusCodeError || name != "RateLimit" || message != "slow down" {
		t.Errorf("explicit span status = %+v, want the status set before the failure", spans[2].Status)
	}
	spans[2].Status.Error.Message = "changed"
	if again := recorder.GetSpans(); again[2].Status.Error.Message != "slow down" {
		t.Errorf("a returned status aliases the recorder: %+v", again[2].Status)
	}
}
