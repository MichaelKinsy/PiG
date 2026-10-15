package telemetry_test

// Pins the fixture contract of packages/telemetry/src/testing/types.ts:5-6 (TelemetryAdapterFixture extends
// AsyncDisposable) as consumed by createCase in packages/telemetry/src/testing/conformance.ts:19-24
// (`await using fixture = await factory()`): each case owns one fresh fixture, disposes it after the case body
// whether or not the case passed, and a factory failure ends the case before any dispose.

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry"
	"github.com/MichaelKinsy/PiG/telemetry/telemetrytest"
)

// Pi source: packages/telemetry/src/memory.ts
// mutation-checked: zeroing the results of InMemoryTelemetryContext.GetSpans fails it
func TestConformanceFixtureIsOwnedAndDisposedByEachCase(t *testing.T) {
	var events []string
	built := 0
	var factory telemetrytest.TelemetryAdapterFixtureFactory = func(context.Context) (telemetrytest.TelemetryAdapterFixture, error) {
		built++
		recorder := telemetry.NewInMemoryTelemetryContext()
		events = append(events, "build")
		return telemetrytest.TelemetryAdapterFixture{
			Context: recorder,
			GetSpans: func(context.Context) ([]telemetry.RecordedTelemetrySpan, error) {
				events = append(events, "read")
				return recorder.GetSpans(), nil
			},
			Dispose: func(context.Context) error {
				events = append(events, "dispose")
				return nil
			},
		}, nil
	}
	conformance := telemetrytest.CreateTelemetryAdapterConformance(factory)
	if err := conformance[0].Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"build", "read", "dispose"}; !slices.Equal(events, want) {
		t.Fatalf("events = %v, want %v: the fixture is built once, read by the case, then disposed last", events, want)
	}
	events = nil
	if err := conformance[1].Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if events[0] != "build" || events[len(events)-1] != "dispose" || built != 2 {
		t.Fatalf("second case events = %v built = %d, want one fresh fixture per case", events, built)
	}
}

// mutation-checked: dropping the reads and writes of TelemetryAdapterConformanceCase.Run, TelemetryAdapterFixture.Context, TelemetryAdapterFixture.GetSpans fails it
func TestConformanceFixtureDisposesAfterAFailingCaseAndJoinsTheDisposeError(t *testing.T) {
	disposed := 0
	disposeFailure := errors.New("dispose failed")
	broken := telemetrytest.CreateTelemetryAdapterConformance(func(context.Context) (telemetrytest.TelemetryAdapterFixture, error) {
		recorder := telemetry.NewInMemoryTelemetryContext()
		return telemetrytest.TelemetryAdapterFixture{
			Context: telemetry.NoopTelemetryContext, // records nothing, so the case's span lookup fails
			GetSpans: func(context.Context) ([]telemetry.RecordedTelemetrySpan, error) {
				return recorder.GetSpans(), nil
			},
			Dispose: func(context.Context) error { disposed++; return disposeFailure },
		}, nil
	})
	err := broken[0].Run(t.Context())
	if disposed != 1 {
		t.Fatalf("dispose ran %d times, want once even though the case failed", disposed)
	}
	if err == nil || !errors.Is(err, disposeFailure) {
		t.Fatalf("err = %v, want the case failure joined with the dispose error", err)
	}
	if err.Error() == disposeFailure.Error() {
		t.Fatalf("err = %v: the case failure is missing from the joined error", err)
	}

	factoryFailure := errors.New("factory failed")
	disposed = 0
	failing := telemetrytest.CreateTelemetryAdapterConformance(func(context.Context) (telemetrytest.TelemetryAdapterFixture, error) {
		return telemetrytest.TelemetryAdapterFixture{Dispose: func(context.Context) error { disposed++; return nil }}, factoryFailure
	})
	if err := failing[0].Run(t.Context()); err != factoryFailure { //nolint:errorlint // the factory error is returned as is
		t.Fatalf("err = %v, want the factory error", err)
	}
	if disposed != 0 {
		t.Fatalf("dispose ran %d times for a fixture the factory failed to build", disposed)
	}
}

// upstream: packages/telemetry/src/testing/types.ts TelemetryAdapterFixtureFactory: each call returns a fresh fixture, so two cases never share a recorder.
func TestFixtureFactoryReturnsAnIsolatedFixtureForEachCall(t *testing.T) {
	var factory telemetrytest.TelemetryAdapterFixtureFactory = func(context.Context) (telemetrytest.TelemetryAdapterFixture, error) {
		recorder := telemetry.NewInMemoryTelemetryContext()
		return telemetrytest.TelemetryAdapterFixture{Context: recorder, GetSpans: func(context.Context) ([]telemetry.RecordedTelemetrySpan, error) { return recorder.GetSpans(), nil }}, nil
	}
	first, err := factory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second, err := factory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if first.Context == second.Context {
		t.Fatal("factory returned the same adapter twice")
	}
	failure := errors.New("no adapter")
	var failing telemetrytest.TelemetryAdapterFixtureFactory = func(context.Context) (telemetrytest.TelemetryAdapterFixture, error) {
		return telemetrytest.TelemetryAdapterFixture{}, failure
	}
	cases := telemetrytest.CreateTelemetryAdapterConformance(failing)
	if err := cases[0].Run(t.Context()); !errors.Is(err, failure) {
		t.Fatalf("a failing factory must fail the case with its error, got %v", err)
	}
}
