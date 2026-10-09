package telemetry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry/telemetrytest"
)

type factoryContextKey struct{}

// packages/telemetry/src/testing/types.ts:11 (TelemetryAdapterFixtureFactory) as called by conformance.ts:19-24: the
// factory runs once per case, receives the case's context, and its error is the case's error with no fixture to dispose.
func TestTelemetryAdapterFixtureFactoryIsCalledOncePerCaseWithTheCaseContext(t *testing.T) {
	var seen []any
	var factory telemetrytest.TelemetryAdapterFixtureFactory = func(ctx context.Context) (telemetrytest.TelemetryAdapterFixture, error) {
		seen = append(seen, ctx.Value(factoryContextKey{}))
		return telemetrytest.TelemetryAdapterFixture{}, errors.New("factory failed")
	}
	conformance := telemetrytest.CreateTelemetryAdapterConformance(factory)
	ctx := context.WithValue(t.Context(), factoryContextKey{}, "case-context")
	for i := range 2 {
		if err := conformance[i].Run(ctx); err == nil || err.Error() != "factory failed" {
			t.Fatalf("case %d error = %v, want the factory's", i, err)
		}
	}
	if len(seen) != 2 || seen[0] != "case-context" || seen[1] != "case-context" {
		t.Fatalf("factory calls = %v, want one per case with the run context", seen)
	}
}
