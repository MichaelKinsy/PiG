package telemetry_test

// packages/telemetry/src/memory.ts:285-286 declares `readonly metrics: TelemetryMetrics = this` and
// `readonly propagation: TelemetryPropagation = this`; memory.ts:172-173 and :194-195 give every child span and every extracted
// parent the same two capabilities. packages/telemetry/test/store-conformance.ts:130-192 reads them through `context.propagation`
// and `context.metrics`.

import (
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry"
)

// TestInMemoryRecorderIsItsOwnMetricsAndPropagationCapability checks memory.ts:285-286: the recorder's metrics and propagation
// members are the recorder itself, and a span, a nested span and an extracted parent report the recorder as theirs
// (memory.ts:172-173, :194-195).
func TestInMemoryRecorderIsItsOwnMetricsAndPropagationCapability(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	if recorder.Metrics() != telemetry.TelemetryMetrics(recorder) {
		t.Fatal("Metrics is not the recorder, as memory.ts:285 `metrics = this`")
	}
	if recorder.Propagation() != telemetry.TelemetryPropagation(recorder) {
		t.Fatal("Propagation is not the recorder, as memory.ts:286 `propagation = this`")
	}
	var carrier telemetry.TelemetryCarrier
	if err := recorder.StartSpan(telemetry.SpanOptions{Name: "outer"}, func(outer telemetry.TelemetrySpan) error {
		carrier = recorder.Propagation().Inject(outer)
		if outer.Metrics() != recorder.Metrics() || outer.Propagation() != recorder.Propagation() {
			t.Error("a span's capabilities are not the recorder's")
		}
		return outer.StartSpan(telemetry.SpanOptions{Name: "inner"}, func(inner telemetry.TelemetrySpan) error {
			if inner.Metrics() != recorder.Metrics() || inner.Propagation() != recorder.Propagation() {
				t.Error("a nested span's capabilities are not the recorder's")
			}
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	remote := recorder.Propagation().Extract(carrier)
	if remote == nil {
		t.Fatal("Extract returned no parent for the carrier Inject produced")
	}
	if remote.Metrics() != recorder.Metrics() || remote.Propagation() != recorder.Propagation() {
		t.Fatal("an extracted parent's capabilities are not the recorder's (memory.ts:194-195)")
	}
	recorder.Metrics().Counter("calls", telemetry.InstrumentOptions{}).Add(1, nil)
	if got := recorder.GetMetrics(); len(got) != 1 || got[0].Name != "calls" || got[0].Value != 1 {
		t.Fatalf("a counter made through Metrics() was not recorded: %+v", got)
	}
}
