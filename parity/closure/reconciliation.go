package closure

import (
	"errors"
	"fmt"
	"slices"
)

// AffectedTests is the result of selecting which tests a set of changed
// behaviors requires. TestIDs is the deterministic set to re-run; Conservative
// is set when an edge gap (a changed behavior with no bound test) forced the
// full suite rather than a scoped selection.
type AffectedTests struct {
	TestIDs      []string
	Conservative bool
}

// SelectAffectedTests chooses the tests to re-run for a set of changed behaviors
// from the assertion edges that bind tests to behaviors. It is conservative by
// construction: if any changed behavior has no bound test, the edge is missing
// and the full test suite is returned rather than a scoped subset, so a
// reconciliation run can never silently skip a test whose coverage the graph
// does not yet record. An empty change set selects nothing.
func SelectAffectedTests(graph *Graph, changedBehaviorIDs []string) (AffectedTests, error) {
	if graph == nil {
		return AffectedTests{}, errors.New("select affected tests: nil graph")
	}
	testsByBehavior := make(map[string]map[string]struct{})
	for _, assertionID := range graph.recordIDs(KindAssertion) {
		assertion := graph.Records[assertionID].(*Assertion)
		bound := testsByBehavior[assertion.BehaviorID]
		if bound == nil {
			bound = make(map[string]struct{})
			testsByBehavior[assertion.BehaviorID] = bound
		}
		bound[assertion.TestID] = struct{}{}
	}

	selected := make(map[string]struct{})
	conservative := false
	for _, behaviorID := range changedBehaviorIDs {
		if _, ok := graph.Records[behaviorID].(*Behavior); !ok {
			return AffectedTests{}, fmt.Errorf("select affected tests: unknown behavior %s", behaviorID)
		}
		bound := testsByBehavior[behaviorID]
		if len(bound) == 0 {
			conservative = true
			continue
		}
		for testID := range bound {
			selected[testID] = struct{}{}
		}
	}

	if conservative {
		return AffectedTests{TestIDs: graph.recordIDs(KindTest), Conservative: true}, nil
	}
	ids := make([]string, 0, len(selected))
	for testID := range selected {
		ids = append(ids, testID)
	}
	slices.Sort(ids)
	return AffectedTests{TestIDs: ids, Conservative: false}, nil
}

// VerdictDrift reports the obligations whose effective verdict differs between
// two graphs, in deterministic obligation order. It is the reconciliation
// primitive: run it between a campaign's incrementally accumulated graph and a
// from-scratch rebuild of the same records, and a non-empty result means the
// incremental path diverged from a full re-derivation. A state or reason change,
// or an obligation present in one graph but not the other, is drift.
func VerdictDrift(before, after *Graph) []string {
	beforeByObligation := verdictsByObligation(before)
	afterByObligation := verdictsByObligation(after)
	seen := make(map[string]struct{}, len(beforeByObligation)+len(afterByObligation))
	for id := range beforeByObligation {
		seen[id] = struct{}{}
	}
	for id := range afterByObligation {
		seen[id] = struct{}{}
	}
	var drifted []string
	for id := range seen {
		left, hasLeft := beforeByObligation[id]
		right, hasRight := afterByObligation[id]
		if hasLeft != hasRight || left.State != right.State || left.Reason != right.Reason {
			drifted = append(drifted, id)
		}
	}
	slices.Sort(drifted)
	return drifted
}

func verdictsByObligation(graph *Graph) map[string]Verdict {
	byObligation := make(map[string]Verdict, len(graph.Verdicts))
	for _, verdict := range graph.EffectiveVerdicts() {
		byObligation[verdict.ObligationID] = verdict
	}
	return byObligation
}
