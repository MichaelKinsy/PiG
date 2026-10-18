package telemetry_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry/telemetrytest"
)

// Pi: packages/telemetry/src/testing/conformance.ts createTelemetryAdapterConformance (cases at 73, 107, 135, 157, 176, 196, 217, 240, 262, 317, 359, 381, 395, 421, 449) and
// packages/telemetry/test/conformance.test.ts, which registers one test per case under its group in the order the cases appear. A
// runner-independent case is identified by its Group and Name, so both, and their order, are the contract a runner sees. The sixteenth Pi case
// ("passivity", "ignores failed status calls atomically") has no Go form: a Go SpanStatus holds a code and an optional error and has no failing
// read, as the conformance.go header records.
// mutation-checked: swapping Group and Name, renaming a case, and restoring the earlier order of the two "recording" cases each fail it.
func TestConformanceCasesKeepPiGroupsNamesAndOrder(t *testing.T) {
	want := []struct{ group, name string }{
		{"metrics", "records ordered measurements and optional histogram exemplars"},
		{"metrics", "copies inputs and suppresses invalid measurements atomically"},
		{"links", "records initial and late links without changing parents"},
		{"links", "suppresses malformed links atomically and ignores settled sources"},
		{"propagation", "roundtrips parent identity through JSON without retaining the carrier"},
		{"propagation", "unknown and unreadable carriers produce recorded roots"},
		{"propagation", "propagates across fresh adapters and multiple hops"},
		{"callback lifecycle", "admits once synchronously and preserves the result"},
		{"callback lifecycle", "preserves synchronous and asynchronous rejection values"},
		{"status", "uses last explicit status without automatic overwrite"},
		{"recording", "merges attributes and records ordered events"},
		{"recording", "ignores failed attribute calls atomically"},
		{"recording", "makes calls after settlement inert"},
		{"parentage", "records nested and concurrent child relationships"},
		{"passivity", "suppresses unreadable telemetry payload failures"},
	}
	cases := telemetrytest.CreateTelemetryAdapterConformance(func(ctx context.Context) (telemetrytest.TelemetryAdapterFixture, error) {
		return telemetrytest.TelemetryAdapterFixture{}, context.Cause(ctx)
	})
	if len(cases) != len(want) {
		t.Fatalf("%d cases, want %d", len(cases), len(want))
	}
	for i, got := range cases {
		if got.Group != want[i].group || got.Name != want[i].name {
			t.Errorf("case %d = %q / %q, want %q / %q", i, got.Group, got.Name, want[i].group, want[i].name)
		}
		if got.Run == nil {
			t.Errorf("case %d (%s) has no Run", i, got.Name)
		}
	}
}
