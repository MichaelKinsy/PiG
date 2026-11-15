package telemetry_test

import (
	"testing"

	chordtelemetry "github.com/MichaelKinsy/PiG/chord/next/telemetry"
)

// Go-only: slice 14's additions to telemetry.ts (Pi has no test of them): the three query counters the store counts, and the
// event `chord.admit` (attribute `outcome`) that the relay, hold and activation spans can carry.
func TestSchemaDeclaresSlice14sQueryCountersAndAdmitEvent(t *testing.T) {
	for _, name := range []string{"chord.query.runs", "chord.query.probes", "chord.query.skips", "chord.query.rows"} {
		if _, ok := chordtelemetry.Schema.Metrics[name]; !ok {
			t.Errorf("metric %s is not declared", name)
		}
	}
	for _, span := range []string{"chord.relay", "chord.hold", "chord.activate"} {
		event, ok := chordtelemetry.Schema.Spans[span].Events["chord.admit"]
		if !ok {
			t.Errorf("%s does not declare the event chord.admit", span)
			continue
		}
		if _, ok := event.Attributes["outcome"]; !ok {
			t.Errorf("chord.admit on %s declares no outcome attribute", span)
		}
	}
}
