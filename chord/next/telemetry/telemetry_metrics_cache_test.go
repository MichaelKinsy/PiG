package telemetry_test

import (
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	chordtelemetry "github.com/MichaelKinsy/PiG/chord/next/telemetry"
	pitelemetry "github.com/MichaelKinsy/PiG/telemetry"
)

// Ports telemetry.test.ts `describe("metrics")` > `it("keeps no adapter alive (its instruments are cached by its
// metrics object, weakly)")`: an instrument made over an adapter's metrics must not keep the adapter reachable.
func TestMetricsKeepsNoAdapterAliveItsInstrumentsAreCachedByItsMetricsObjectWeakly(t *testing.T) {
	adapter := pitelemetry.NewInMemoryTelemetryContext()
	ref := weak.Make(adapter)
	chordtelemetry.Metrics(adapter.Metrics()).Counter("chord.query.runs").Add(1, nil)
	adapter = nil
	nexttest.ExpectCollected(t, ref)
}
