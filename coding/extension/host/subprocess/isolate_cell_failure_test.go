//go:build !pig_strip_node_extensions

package subprocess

import (
	"errors"
	"strings"
	"testing"
)

// A packed cell that fails is rebuilt member by member, and only a member that fails in isolation too is reported as a failure. When every
// member survives its retry, the error that ended the packed cell is the only trace of why the plan was not kept: it must reach the
// report of each member that was moved out, or a flaky packed build that the retry hides (a compiler killed by a signal, a lock wait that
// ran out) cannot be told from a placement the planner chose.
func TestIsolateCellFailureReportsTheErrorThatEndedThePackedCell(t *testing.T) {
	rootA := writePackedFactoryModule(t, "example.com/isolate/ra", "report-a", "tool_a")
	rootB := writePackedFactoryModule(t, "example.com/isolate/rb", "report-b", "tool_b")
	configs := []ExtConfig{
		packedFactoryConfig("report-a", rootA, "example.com/isolate/ra", "ra"),
		packedFactoryConfig("report-b", rootB, "example.com/isolate/rb", "rb"),
	}
	cells := PlanCells(configs, nil)
	if len(cells) != 1 || len(cells[0].Extensions) != 2 {
		t.Fatalf("cells = %#v, want both members in one packed cell", cells)
	}
	cell := cells[0]

	ctx, cancel := buildContext(t)
	defer cancel()
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })

	// The compiler's report spans lines; one report line carries it whole.
	packedErr := errors.New("build packed Rust cell " + cell.Key + ": cargo build failed:\n  rustc interrupted by SIGBUS\n")
	outcomes, failures := h.isolateCellFailure(ctx, cell, nil, stageOutcome{}, packedErr)
	if len(failures) != 0 {
		t.Fatalf("failures = %v, want every member to build in isolation", failures)
	}
	if len(outcomes) != 3 {
		t.Fatalf("outcomes = %d, want the packed attempt and one per member", len(outcomes))
	}
	want := "packed cell failed: build packed Rust cell " + cell.Key + ": cargo build failed: rustc interrupted by SIGBUS"
	for i, name := range []string{"report-a", "report-b"} {
		report := outcomes[i+1].report
		if len(report.Extensions) != 1 || report.Extensions[0] != name || report.Strategy != CellStrategyIsolated {
			t.Fatalf("report %d = %#v, want %s in its own process", i, report, name)
		}
		if report.Reason != want {
			t.Errorf("%s: reason = %q, want %q", name, report.Reason, want)
		}
		if strings.ContainsAny(report.Reason, "\r\n") {
			t.Errorf("%s: reason spans lines: %q", name, report.Reason)
		}
	}
}
