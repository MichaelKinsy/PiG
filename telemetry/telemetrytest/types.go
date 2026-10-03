// Package telemetrytest holds runner-independent conformance cases for telemetry.TelemetryContext adapters.
package telemetrytest

// Ports packages/telemetry/src/testing/types.ts.
// Ports packages/telemetry/src/testing/index.ts.

import (
	"context"

	"github.com/MichaelKinsy/PiG/telemetry"
)

// TelemetryAdapterFixture is a fresh adapter instance and normalized snapshot reader owned by one conformance case.
// Dispose ports Symbol.asyncDispose; a nil Dispose releases nothing.
type TelemetryAdapterFixture struct {
	Context  telemetry.TelemetryContext
	GetSpans func(context.Context) ([]telemetry.RecordedTelemetrySpan, error)
	Dispose  func(context.Context) error
}

// TelemetryAdapterFixtureFactory creates an isolated adapter fixture for one conformance case.
type TelemetryAdapterFixtureFactory func(context.Context) (TelemetryAdapterFixture, error)

// TelemetryAdapterConformanceCase is a runner-independent conformance case that can be registered with any test
// framework. Run returns the first failed check.
type TelemetryAdapterConformanceCase struct {
	Group string
	Name  string
	Run   func(context.Context) error
}
