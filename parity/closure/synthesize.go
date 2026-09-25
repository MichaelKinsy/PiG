package closure

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

// SynthesizeEvidenceRequest derives an EvidenceRequest for a verification-only
// obligation from the graph: the obligation's accepted mapping supplies the
// production targets, and the bound test's Execution recipe supplies the runnable
// command. It is the general, non-authoring counterpart to provider-wire's
// hardcoded request table. A synthesized request is only produced when the
// graph carries everything needed to run and witness the test mechanically;
// otherwise the function fails closed (no recipe, no accepted mapping, no
// admissible assertion) rather than guessing a command.
//
// The recipe covers the common Go case: run one test function in a package with
// a coverage profile, witnessed by the executed production ranges. Obligations
// whose test carries no Execution (differentials, multi-command traces, authored
// recipes) are left to their authored EvidenceRequests and return an error here.
func SynthesizeEvidenceRequest(graph *Graph, obligationID string) (*EvidenceRequest, error) {
	obligation, ok := graph.Records[obligationID].(*Obligation)
	if !ok {
		return nil, fmt.Errorf("synthesize evidence: obligation %s not found", obligationID)
	}
	mapping := graph.acceptedMapping(obligation.BehaviorID)
	if mapping == nil {
		return nil, fmt.Errorf("synthesize evidence: %s has no accepted mapping", obligationID)
	}
	if !graph.mappingAcceptanceCovers(mapping) {
		return nil, fmt.Errorf("synthesize evidence: %s mapping acceptance does not cover behavior", obligationID)
	}
	if !graph.productionTargetsReachable(obligation.BehaviorID, mapping.TargetIDs) {
		return nil, fmt.Errorf("synthesize evidence: %s mapped targets lack production reachability", obligationID)
	}
	test, assertion := graph.runnableAssertion(obligation)
	if test == nil {
		return nil, fmt.Errorf("synthesize evidence: %s has no admissible assertion with a runnable test", obligationID)
	}
	exec := test.Execution
	if exec == nil {
		return nil, fmt.Errorf("synthesize evidence: %s test %s carries no execution recipe", obligationID, test.ID)
	}
	targetIDs, pinIDs, err := witnessTargets(graph, mapping, obligation)
	if err != nil {
		return nil, fmt.Errorf("synthesize evidence: %s: %w", obligationID, err)
	}
	args := []string{"test"}
	if len(exec.BuildTags) > 0 {
		args = append(args, "-tags="+joinComma(exec.BuildTags))
	}
	coverPath := filepath.ToSlash(filepath.Join("tmp", "closure", "evidence-work", "synthesized-"+test.ID+".coverprofile"))
	args = append(args, exec.Package, "-run", "^"+exec.RunName+"$", "-count=1", "-coverprofile="+coverPath)
	command := EvidenceCommand{Name: "go", Args: args}
	return &EvidenceRequest{
		Kind: KindEvidenceRequest, ID: "request:synthesized:" + obligationID, SnapshotID: test.SnapshotID,
		ObligationIDs: []string{obligationID}, AssertionIDs: []string{assertion.ID},
		Commands:    []EvidenceCommand{command},
		Environment: []string{}, TimeoutSeconds: 120,
		Witnesses: []EvidenceWitnessRequest{{
			WitnessType: "go-covered-range", TargetIDs: targetIDs, SubjectPinIDs: pinIDs,
			CommandIndexes: []int{1}, ArtifactPath: coverPath,
		}},
		Durability: 1, Comparator: exec.Comparator,
	}, nil
}

// runnableAssertion returns the obligation's bound test and an admissible
// assertion whose test carries a runnable Execution recipe. The assertion must
// be admissible for the obligation's facet, so a synthesized request can satisfy
// deriveVerdict's admissibility check.
func (g *Graph) runnableAssertion(obligation *Obligation) (*Test, *Assertion) {
	facet, ok := g.Records[obligation.FacetID].(*Facet)
	if !ok {
		return nil, nil
	}
	for _, assertionID := range g.recordIDs(KindAssertion) {
		assertion := g.Records[assertionID].(*Assertion)
		if assertion.BehaviorID != obligation.BehaviorID || assertion.FacetID != obligation.FacetID {
			continue
		}
		if !assertionAdmissible(facet.Name, assertion.Class, assertion.Oracle) {
			continue
		}
		test, ok := g.Records[assertion.TestID].(*Test)
		if !ok || test.Execution == nil {
			continue
		}
		return test, assertion
	}
	return nil, nil
}

// witnessTargets collects the production target and pin IDs the coverage witness
// must cover for the obligation to be proven: every mapped target that the
// obligation's behavior reaches in production, and the pins those targets host.
func witnessTargets(graph *Graph, mapping *Mapping, obligation *Obligation) (targetIDs, pinIDs []string, err error) {
	targetSet := make(map[string]struct{})
	pinSet := make(map[string]struct{})
	for _, targetID := range mapping.TargetIDs {
		if graph.productionReachability(obligation.BehaviorID, targetID) == nil {
			continue
		}
		target, ok := graph.Records[targetID].(*Target)
		if !ok {
			return nil, nil, fmt.Errorf("target %s not found", targetID)
		}
		targetSet[targetID] = struct{}{}
		for _, id := range target.PinIDs {
			pinSet[id] = struct{}{}
		}
	}
	if len(targetSet) == 0 {
		return nil, nil, fmt.Errorf("no production-reachable targets")
	}
	return slices.Sorted(maps.Keys(targetSet)), slices.Sorted(maps.Keys(pinSet)), nil
}

func joinComma(values []string) string {
	if len(values) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString(values[0])
	for _, value := range values[1:] {
		out.WriteString("," + value)
	}
	return out.String()
}
