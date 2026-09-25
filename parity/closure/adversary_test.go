package closure

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestAC77ClosureRejectsFalseProof(t *testing.T) {
	t.Run("citation laundering", func(t *testing.T) {
		records := correspondenceMappingFixture(t)
		finding := adversaryHypothesis(t)
		var value agentProposalValue
		if err := json.Unmarshal(finding.Value, &value); err != nil {
			t.Fatal(err)
		}
		value.Citations[0].SourceHash = HashBytes([]byte("laundered citation"))
		finding.Value, _ = json.Marshal(value)
		records = append(records, finding)
		if _, err := Build(records); err == nil || !strings.Contains(err.Error(), "citations do not bind") {
			t.Fatalf("laundered citation error = %v", err)
		}
	})

	t.Run("missing facet", func(t *testing.T) {
		records := provedFixture(t)
		for _, record := range records {
			if assertion, ok := record.(*Assertion); ok {
				assertion.FacetID = "facet:error"
			}
		}
		records = append(records, &Facet{Kind: KindFacet, ID: "facet:error", Name: "error"})
		if _, err := Build(records); err == nil || !strings.Contains(err.Error(), "binds no requested obligation") {
			t.Fatalf("missing facet error = %v", err)
		}
	})

	t.Run("stale pin", func(t *testing.T) {
		records := provedFixture(t)
		for _, record := range records {
			if pin, ok := record.(*Pin); ok && pin.ID == "pin:target" {
				pin.BodyHash = HashBytes([]byte("stale target"))
			}
		}
		graph, err := Build(records)
		if err != nil {
			t.Fatal(err)
		}
		if verdict := graph.Verdicts["obligation:result"]; verdict.State != VerdictOpen || verdict.Reason != "no admissible passing evidence" {
			t.Fatalf("stale pin verdict = %#v", verdict)
		}
	})

	t.Run("forged executor record", func(t *testing.T) {
		_, err := DecodeJSONL(strings.NewReader(`{"kind":"mutation-run","id":"mutation-run:forged"}` + "\n"))
		if err == nil || !strings.Contains(err.Error(), "executor-imported only") {
			t.Fatalf("forged mutation run error = %v", err)
		}
	})

	t.Run("hidden failure", func(t *testing.T) {
		records, run := mutationFixture(t, false)
		run.Commands[0].Error = "test process could not start"
		run.Results[0].FailureHash = mutationStdoutFailureHash(run.Commands[0].StdoutHash)
		run.Results[0].FailureTest = ""
		run.ID, _ = mutationRunContentID(run)
		rebindMutationAttestation(t, records, run)
		graph, err := Build(records)
		if err != nil {
			t.Fatal(err)
		}
		if verdict := graph.Verdicts["obligation:result"]; verdict.State != VerdictContradicted || verdict.Reason != "relevant mutant survived or failed for the wrong reason" {
			t.Fatalf("hidden failure verdict = %#v", verdict)
		}
	})

	t.Run("self agreeing wire fixture", func(t *testing.T) {
		records := provedFixture(t)
		for _, record := range records {
			switch value := record.(type) {
			case *Facet:
				value.ID, value.Name = "facet:wire", "wire"
			case *Obligation:
				value.FacetID = "facet:wire"
			case *Assertion:
				value.FacetID, value.Class, value.Oracle = "facet:wire", "A3", "authored-self-agreement"
			}
		}
		refreshEvidenceBindings(t, records)
		graph, err := Build(records)
		if err != nil {
			t.Fatal(err)
		}
		if verdict := graph.Verdicts["obligation:result"]; verdict.State != VerdictOpen {
			t.Fatalf("self-agreeing fixture verdict = %#v", verdict)
		}
	})

	t.Run("uncovered branch", func(t *testing.T) {
		records, _ := mutationFixture(t, false)
		records = slices.DeleteFunc(records, func(record Record) bool {
			return record.RecordKind() == KindMutationRun || record.RecordKind() == KindMutationAttestation
		})
		graph, err := Build(records)
		if err != nil {
			t.Fatal(err)
		}
		if verdict := graph.Verdicts["obligation:result"]; verdict.State != VerdictOpen || verdict.Reason != "no admissible mutation evidence" {
			t.Fatalf("uncovered branch verdict = %#v", verdict)
		}
	})

	t.Run("inadmissible normalization", func(t *testing.T) {
		records := provedFixture(t)
		for _, record := range records {
			if request, ok := record.(*EvidenceRequest); ok {
				request.Comparator = "output-normalized-equal"
			}
		}
		if _, err := Build(records); err == nil || !strings.Contains(err.Error(), "invalid comparator") {
			t.Fatalf("normalization error = %v", err)
		}
	})

	t.Run("expired decision", func(t *testing.T) {
		records := provedFixture(t)
		for _, record := range records {
			if decision, ok := record.(*Decision); ok && decision.DecisionType == "mapping" {
				decision.ValidThroughCommit = strings.Repeat("c", 40)
			}
		}
		graph, err := Build(records)
		if err != nil {
			t.Fatal(err)
		}
		if verdict := graph.Verdicts["obligation:result"]; verdict.State != VerdictOpen || verdict.Reason != "mapping acceptance does not cover behavior" {
			t.Fatalf("expired decision verdict = %#v", verdict)
		}
	})
}
