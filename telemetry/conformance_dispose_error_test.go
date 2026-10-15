package telemetry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry"
	"github.com/MichaelKinsy/PiG/telemetry/telemetrytest"
)

// packages/telemetry/src/testing/conformance.ts:19-24 createCase `await using fixture = await factory(); await test(fixture)`: when the case body passes, a
// rejection from the fixture's asyncDispose is the rejection of run(), so a fixture that cannot release its adapter fails the case rather than passing it.
// mutation-checked: the mutant "dispose error ignored when the case passed" fails it.
func TestConformanceCaseFailsWhenAPassingCaseCannotDisposeItsFixture(t *testing.T) {
	disposeFailure := errors.New("dispose failed")
	disposed := 0
	passing := telemetrytest.CreateTelemetryAdapterConformance(func(context.Context) (telemetrytest.TelemetryAdapterFixture, error) {
		recorder := telemetry.NewInMemoryTelemetryContext()
		return telemetrytest.TelemetryAdapterFixture{
			Context:  recorder,
			GetSpans: func(context.Context) ([]telemetry.RecordedTelemetrySpan, error) { return recorder.GetSpans(), nil },
			Dispose:  func(context.Context) error { disposed++; return disposeFailure },
		}, nil
	})
	// The rejection is the dispose error alone: a case body that failed as well would join its own error (SuppressedError in Pi).
	if err := passing[0].Run(t.Context()); !errors.Is(err, disposeFailure) || err.Error() != disposeFailure.Error() {
		t.Fatalf("a passing case whose fixture fails to dispose returned %v, want the dispose error", err)
	}
	if disposed != 1 {
		t.Fatalf("dispose ran %d times, want once", disposed)
	}
}
