// pi: packages/durable/src/harness/types.ts
// Ports packages/durable/test/harness-telemetry-options.test.ts.
//
// Upstream's expectTypeOf and @ts-expect-error assertions check the option's type. The Go port type-checks each snippet
// against the compiled packages, as types_upstream_test.go does. TypeScript's `Partial<TelemetryPropagation &
// TelemetryMetrics>` lets an adapter define any subset of the members; Go has no partial interface, so the capabilities
// are the interfaces telemetry.TelemetryPropagation and telemetry.TelemetryMetrics, found by type assertion.

package harness

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Pi source: packages/durable/src/harness/types.ts
// mutation-checked: dropping the HarnessOptions.Telemetry field fails it
func TestHarnessOptionsTelemetryAcceptsOneAdapterWithOptionalPropagationAndMetricsCapabilities(t *testing.T) {
	adapter := telemetry.NewInMemoryTelemetryContext()
	full := HarnessOptions{Telemetry: adapter}
	contextOnly := HarnessOptions{Telemetry: telemetry.NoopTelemetryContext}
	var omitted HarnessOptions
	if full.Telemetry != telemetry.TelemetryContext(adapter) {
		t.Fatal("Telemetry is not the adapter it was given")
	}
	if contextOnly.Telemetry != telemetry.NoopTelemetryContext {
		t.Fatal("Telemetry is not the no-op context it was given")
	}
	if omitted.Telemetry != nil {
		t.Fatal("an omitted Telemetry is not nil")
	}
	// The adapter's capabilities are found on the one value; a context-only adapter has none.
	if _, ok := full.Telemetry.(telemetry.TelemetryPropagation); !ok {
		t.Fatal("the full adapter does not offer propagation")
	}
	if _, ok := full.Telemetry.(telemetry.TelemetryMetrics); !ok {
		t.Fatal("the full adapter does not offer metrics")
	}
	if _, ok := contextOnly.Telemetry.(telemetry.TelemetryPropagation); ok {
		t.Fatal("the no-op context offers propagation")
	}
	if _, ok := contextOnly.Telemetry.(telemetry.TelemetryMetrics); ok {
		t.Fatal("the no-op context offers metrics")
	}

	imports := map[string]string{
		"h":   "github.com/MichaelKinsy/PiG/durable/harness",
		"tel": "github.com/MichaelKinsy/PiG/telemetry",
	}
	testenv.ExpectCompiles(t, "context-only, full and omitted adapters", imports, `
_ = h.HarnessOptions{Telemetry: tel.NoopTelemetryContext}
_ = h.HarnessOptions{Telemetry: tel.NewInMemoryTelemetryContext()}
_ = h.HarnessOptions{}`)
	// Upstream's `{ counter }` is an object without startSpan; the Go form is a value that is not a TelemetryContext.
	testenv.ExpectTypeError(t, "telemetry requires the span-starting context capability", imports, `
_ = h.HarnessOptions{Telemetry: struct{ Counter func() }{}}`)
}
