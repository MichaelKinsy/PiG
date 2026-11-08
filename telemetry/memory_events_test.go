package telemetry_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsclock"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// A settled span stores each event with its own time, relative to the span's start (packages/telemetry/src/memory.ts
// `addEvent` pushes one time per event).
func TestInMemoryStoredSpanKeepsEachEventsOwnTime(t *testing.T) {
	_, restoreWall := jsclock.MockDateNow(1000)
	defer restoreWall()
	clock, restoreClock := jsclock.MockPerformanceNow(100)
	defer restoreClock()
	recorder := telemetry.NewInMemoryTelemetryContext()
	if err := recorder.StartSpan(telemetry.SpanOptions{Name: "events"}, func(span telemetry.TelemetrySpan) error {
		clock.Set(100.25)
		span.AddEvent("first", nil)
		clock.Set(100.75)
		span.AddEvent("second", nil)
		clock.Set(101)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := recorder.FindTraces(telemetry.TraceQuery{})
	if err != nil {
		t.Fatal(err)
	}
	events := result.Traces[0].Spans[0].Events
	if len(events) != 2 || events[0].TimeMs != 0.25 || events[1].TimeMs != 0.75 {
		t.Fatalf("events = %+v, want times 0.25 and 0.75", events)
	}
}
