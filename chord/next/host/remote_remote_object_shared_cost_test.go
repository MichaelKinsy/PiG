package host

import (
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry"
)

// pi: packages/chord/src/remote/remote-object.ts

// Ports the describe "cost" of 'references sharing one wire hold (decision "References share one wire hold")' of
// packages/chord/test/remote/remote-object.test.ts (Pi 47e9d74f9 + 90ed3e3b1).

func TestReferencesSharingOneWireHoldCost(t *testing.T) {
	// remote-object.test.ts: references sharing one wire hold › cost › two references, one hold: one encode per commit
	t.Run("two references, one hold: one encode per commit", func(t *testing.T) {
		meter := telemetry.NewInMemoryTelemetryContext()
		hosts := linked(t, func(h FacetHost) error { _, err := serveNotes(h, nil); return err }, linkedOptions{Telemetry: meter})
		get := func() *Reference {
			ref, err := hosts.Client.Get(background, Notes, rhNotes("S1"), GetOptions{})
			must(t, err)
			return ref
		}
		a := get()
		b := get()
		settleLinked(hosts)
		encodes := func() float64 {
			sum := 0.0
			for _, measurement := range meter.GetMetrics() {
				if measurement.Name == "chord.link.encodes" {
					sum += measurement.Value
				}
			}
			return sum
		}
		before := encodes()
		_, err := a.Call("add", jsonObject("text", "x"), background)
		must(t, err)
		_, err = b.Call("add", jsonObject("text", "y"), background)
		must(t, err)
		settleLinked(hosts)
		if more := encodes() - before; more != 2 {
			t.Fatalf("encodes of two commits = %v, want 2", more)
		}
		rhAwaitValue(t, a, "count", float64(2))
		if counts := []any{memberValue(t, a, "count"), memberValue(t, b, "count")}; counts[0] != float64(2) || counts[1] != float64(2) {
			t.Fatalf("counts = %v, want [2 2]", counts)
		}
	})
}
