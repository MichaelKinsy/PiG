package telemetry_test

// upstream: packages/telemetry/src/testing/types.ts:11 (TelemetryAdapterFixtureFactory) consumed by
// packages/telemetry/test/conformance.test.ts: the in-memory recorder passes every case built from one factory.

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry"
	"github.com/MichaelKinsy/PiG/telemetry/telemetrytest"
)

func inMemoryFixtureFactory(_ context.Context, adapter func(*telemetry.InMemoryTelemetryContext) telemetry.TelemetryContext) (telemetrytest.TelemetryAdapterFixture, error) {
	recorder := &telemetry.InMemoryTelemetryContext{}
	return telemetrytest.TelemetryAdapterFixture{
		Context:  adapter(recorder),
		GetSpans: func(context.Context) ([]telemetry.RecordedTelemetrySpan, error) { return recorder.GetSpans(), nil },
	}, nil
}

func TestTelemetryAdapterFixtureFactoryBuildsAFreshFixturePerCase(t *testing.T) {
	var factory telemetrytest.TelemetryAdapterFixtureFactory = func(ctx context.Context) (telemetrytest.TelemetryAdapterFixture, error) {
		return inMemoryFixtureFactory(ctx, func(recorder *telemetry.InMemoryTelemetryContext) telemetry.TelemetryContext { return recorder })
	}
	cases := telemetrytest.CreateTelemetryAdapterConformance(factory)
	if len(cases) == 0 {
		t.Fatal("no conformance cases")
	}
	for _, conformanceCase := range cases {
		t.Run(conformanceCase.Group+"/"+conformanceCase.Name, func(t *testing.T) {
			if err := conformanceCase.Run(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTelemetryAdapterFixtureFactoryOfARecordingFreeAdapterFailsTheCases(t *testing.T) {
	var factory telemetrytest.TelemetryAdapterFixtureFactory = func(ctx context.Context) (telemetrytest.TelemetryAdapterFixture, error) {
		return inMemoryFixtureFactory(ctx, func(*telemetry.InMemoryTelemetryContext) telemetry.TelemetryContext {
			return telemetry.NoopTelemetryContext
		})
	}
	failed := 0
	for _, conformanceCase := range telemetrytest.CreateTelemetryAdapterConformance(factory) {
		if conformanceCase.Run(t.Context()) != nil {
			failed++
		}
	}
	if failed == 0 {
		t.Fatal("a context that records nothing passed every conformance case")
	}
}
