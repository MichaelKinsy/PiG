package telemetry_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry/telemetrytest"
)

// Pi: packages/telemetry/src/testing/conformance.ts createTelemetryAdapterConformance and
// packages/telemetry/test/conformance.test.ts, which registers one test per case under its group in the order the cases appear. A
// runner-independent case is identified by its Group and Name, so both, and their order, are the contract a runner sees. Upstream's malformed-input
// cases are removed: the adapter contract trusts typed inputs.
// mutation-checked: swapping Group and Name, renaming a case, and restoring the earlier order of the two "recording" cases each fail it.
func TestConformanceCasesKeepPiGroupsNamesAndOrder(t *testing.T) {
	want := []struct{ group, name string }{
		{"capabilities", "carries adapter capabilities through contexts and spans"},
		{"metrics", "records ordered measurements and optional histogram exemplars"},
		{"metrics", "copies instrument metadata and measurement attributes"},
		{"links", "records initial and late links without changing parents"},
		{"links", "ignores links added after source settlement"},
		{"propagation", "roundtrips parent identity through JSON without retaining the carrier"},
		{"propagation", "absent and unknown carriers produce recorded roots"},
		{"propagation", "propagates across fresh adapters and multiple hops"},
		{"callback lifecycle", "admits once synchronously and preserves the result"},
		{"callback lifecycle", "preserves synchronous and asynchronous rejection values"},
		{"status", "uses last explicit status without automatic overwrite"},
		{"recording", "merges attributes and records ordered events"},
		{"recording", "makes calls after settlement inert"},
		{"parentage", "records nested and concurrent child relationships"},
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
