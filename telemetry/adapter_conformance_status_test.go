package telemetry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry"
	"github.com/MichaelKinsy/PiG/telemetry/telemetrytest"
)

// The in-memory adapter passes every conformance case through a typed fixture factory, and records a span's final status: ok on success, an error status carrying the error's message on failure, and the last explicit status when one was set.
// Pi: packages/telemetry/src/testing/conformance.ts:13 (TelemetryAdapterFixtureFactory), packages/telemetry/src/testing/conformance.ts:82 (status), packages/telemetry/src/memory.ts:81 (status)
// mutation-checked: a status that is always ok, or a factory that is not used per case, fails it
func TestInMemoryAdapterPassesConformanceAndRecordsSpanStatus(t *testing.T) {
	var factory telemetrytest.TelemetryAdapterFixtureFactory = func(context.Context) (telemetrytest.TelemetryAdapterFixture, error) {
		recorder := &telemetry.InMemoryTelemetryContext{}
		return telemetrytest.TelemetryAdapterFixture{
			Context:  recorder,
			GetSpans: func(context.Context) ([]telemetry.RecordedTelemetrySpan, error) { return recorder.GetSpans(), nil },
		}, nil
	}
	for _, c := range telemetrytest.CreateTelemetryAdapterConformance(factory) {
		if err := c.Run(t.Context()); err != nil {
			t.Errorf("%s / %s: %v", c.Group, c.Name, err)
		}
	}
	recorder := &telemetry.InMemoryTelemetryContext{}
	failure := errors.New("boom")
	_ = recorder.StartSpan(telemetry.SpanOptions{Name: "ok"}, func(telemetry.TelemetrySpan) error { return nil })
	if err := recorder.StartSpan(telemetry.SpanOptions{Name: "failed"}, func(telemetry.TelemetrySpan) error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("the callback error must propagate: %v", err)
	}
	_ = recorder.StartSpan(telemetry.SpanOptions{Name: "explicit"}, func(span telemetry.TelemetrySpan) error {
		span.SetStatus(telemetry.SpanStatus{Status: telemetry.SpanStatusCodeError, Error: &telemetry.SpanStatusError{Name: "Expected", Message: "first"}})
		span.SetStatus(telemetry.SpanStatus{Status: telemetry.SpanStatusCodeOK})
		return failure
	})
	status := map[string]telemetry.SpanStatus{}
	for _, span := range recorder.GetSpans() {
		status[span.Name] = span.Status
	}
	if status["ok"].Status != telemetry.SpanStatusCodeOK || status["ok"].Error != nil {
		t.Errorf("ok span status = %+v", status["ok"])
	}
	if got := status["failed"]; got.Status != telemetry.SpanStatusCodeError || got.Error == nil || got.Error.Message != "boom" {
		t.Errorf("failed span status = %+v, want an error status with the message", got)
	}
	if got := status["explicit"]; got.Status != telemetry.SpanStatusCodeOK {
		t.Errorf("explicit span status = %+v, want the last explicit status to win over the callback error", got)
	}
}
