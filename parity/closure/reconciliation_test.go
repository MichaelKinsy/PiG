package closure

import (
	"slices"
	"strings"
	"testing"
)

// affectedTestGraph hosts three behaviors: alpha and beta each carry a bound
// test via an assertion; gamma carries none (a missing edge).
func affectedTestGraph() *Graph {
	records := map[string]Record{
		"behavior:alpha":  &Behavior{Kind: KindBehavior, ID: "behavior:alpha"},
		"behavior:beta":   &Behavior{Kind: KindBehavior, ID: "behavior:beta"},
		"behavior:gamma":  &Behavior{Kind: KindBehavior, ID: "behavior:gamma"},
		"test:alpha":      &Test{Kind: KindTest, ID: "test:alpha"},
		"test:beta":       &Test{Kind: KindTest, ID: "test:beta"},
		"test:gamma":      &Test{Kind: KindTest, ID: "test:gamma"},
		"assertion:alpha": &Assertion{Kind: KindAssertion, ID: "assertion:alpha", TestID: "test:alpha", BehaviorID: "behavior:alpha"},
		"assertion:beta":  &Assertion{Kind: KindAssertion, ID: "assertion:beta", TestID: "test:beta", BehaviorID: "behavior:beta"},
	}
	return &Graph{Records: records}
}

func TestSelectAffectedTestsScopesToBoundTests(t *testing.T) {
	graph := affectedTestGraph()
	got, err := SelectAffectedTests(graph, []string{"behavior:alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Conservative || !slices.Equal(got.TestIDs, []string{"test:alpha"}) {
		t.Fatalf("selection = %#v, want scoped test:alpha", got)
	}
	// Union across two changed behaviors, deterministic order.
	got, err = SelectAffectedTests(graph, []string{"behavior:beta", "behavior:alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Conservative || !slices.Equal(got.TestIDs, []string{"test:alpha", "test:beta"}) {
		t.Fatalf("union selection = %#v", got)
	}
}

func TestSelectAffectedTestsFallsBackToFullSuiteOnMissingEdge(t *testing.T) {
	graph := affectedTestGraph()
	// gamma has no bound test, so scoping is impossible; run the whole suite.
	got, err := SelectAffectedTests(graph, []string{"behavior:alpha", "behavior:gamma"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Conservative || !slices.Equal(got.TestIDs, []string{"test:alpha", "test:beta", "test:gamma"}) {
		t.Fatalf("selection = %#v, want conservative full suite", got)
	}
}

func TestSelectAffectedTestsEmptyChangeSelectsNothing(t *testing.T) {
	graph := affectedTestGraph()
	got, err := SelectAffectedTests(graph, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Conservative || len(got.TestIDs) != 0 {
		t.Fatalf("empty change selection = %#v, want nothing", got)
	}
}

func TestSelectAffectedTestsRejectsUnknownBehavior(t *testing.T) {
	graph := affectedTestGraph()
	if _, err := SelectAffectedTests(graph, []string{"behavior:missing"}); err == nil || !strings.Contains(err.Error(), "unknown behavior behavior:missing") {
		t.Fatalf("unknown behavior error = %v", err)
	}
}

func TestVerdictDriftIsEmptyOnDeterministicRebuild(t *testing.T) {
	records := provedFixture(t)
	first, err := Build(records)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Build(provedFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if drift := VerdictDrift(first, second); len(drift) != 0 {
		t.Fatalf("deterministic rebuild drifted: %v", drift)
	}
}

func TestVerdictDriftDetectsStateAndPresenceChanges(t *testing.T) {
	proved, err := Build(provedFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	// Strip the evidence so the same obligation is open instead of proven.
	open := make([]Record, 0)
	for _, record := range provedFixture(t) {
		switch record.(type) {
		case *EvidenceRun, *ExecutionWitness, *EvidenceAttestation:
			continue
		}
		open = append(open, record)
	}
	openGraph, err := Build(open)
	if err != nil {
		t.Fatal(err)
	}
	drift := VerdictDrift(proved, openGraph)
	if !slices.Contains(drift, "obligation:result") {
		t.Fatalf("drift = %v, want obligation:result (proven vs open)", drift)
	}
}
