package closure

import (
	"bytes"
	"fmt"
	"strings"
)

const FoundationDashboardDataset = "foundation"

var foundationDatasets = []string{
	"semantic-interface",
	SemanticMappingDataset,
	SemanticDeltaDataset,
	"go-interface",
	"interface-recommendation",
	"cli-interface",
	"behavior-input",
	InputRenderMappingDataset,
	AsyncContractDataset,
	ChangedSourceDataset,
	BehaviorContractDataset,
	FormatOwnershipDataset,
	"family",
	"scenario",
	PortMapDataset,
	CoverageDataset,
	DivergenceDashboardDataset,
}

type foundationReportRow struct {
	scope  string
	items  int
	counts map[VerdictState]int
	ready  bool
	reason string
}

func renderFoundationDashboard(graph *Graph) ([]byte, error) {
	subjects := make(map[string]map[string]struct{}, len(foundationDatasets))
	for _, dataset := range foundationDatasets {
		subjects[dataset] = make(map[string]struct{})
	}
	for _, recordID := range graph.recordIDs(KindFact) {
		fact := graph.Records[recordID].(*Fact)
		dataset, ok := strings.CutPrefix(fact.FactType, "denominator:")
		if !ok || fact.SubjectID == "<metadata>" || subjects[dataset] == nil {
			continue
		}
		subjects[dataset][fact.SubjectID] = struct{}{}
	}
	for _, recordID := range graph.recordIDs(KindHypothesis) {
		hypothesis := graph.Records[recordID].(*Hypothesis)
		if subjects[hypothesis.HypothesisType] != nil {
			subjects[hypothesis.HypothesisType][hypothesis.SubjectID] = struct{}{}
		}
	}
	provisional := make(map[string]int, len(foundationDatasets))
	claimSubjects := make(map[string]map[string]struct{}, len(foundationDatasets))
	for _, recordID := range graph.recordIDs(KindProvisionalClaim) {
		claim := graph.Records[recordID].(*ProvisionalClaim)
		remainder, ok := strings.CutPrefix(claim.ID, "provisional:denominator:")
		if !ok {
			continue
		}
		dataset, _, ok := strings.Cut(remainder, ":")
		if !ok || subjects[dataset] == nil {
			return nil, fmt.Errorf("report %s claim %s has an unknown dataset", FoundationDashboardDataset, claim.ID)
		}
		if _, exists := subjects[dataset][claim.SubjectID]; !exists {
			return nil, fmt.Errorf("report %s claim %s has no denominator row", FoundationDashboardDataset, claim.ID)
		}
		if claimSubjects[dataset] == nil {
			claimSubjects[dataset] = make(map[string]struct{})
		}
		if _, duplicate := claimSubjects[dataset][claim.SubjectID]; duplicate {
			return nil, fmt.Errorf("report %s has duplicate claim subject %s in %s", FoundationDashboardDataset, claim.SubjectID, dataset)
		}
		claimSubjects[dataset][claim.SubjectID] = struct{}{}
		provisional[dataset]++
	}
	rows := make([]foundationReportRow, 0, len(foundationDatasets)+1)
	for _, dataset := range foundationDatasets {
		items := len(subjects[dataset])
		counts := foundationStateCounts()
		counts[VerdictProvisional] = provisional[dataset]
		counts[VerdictOpen] = items - provisional[dataset]
		reason := "imported denominator pending closure bindings"
		if items == 0 {
			reason = "imported denominator is missing"
		}
		rows = append(rows, foundationReportRow{scope: dataset, items: items, counts: counts, reason: reason})
	}
	obligationCounts := foundationStateCounts()
	for _, verdict := range graph.EffectiveVerdicts() {
		obligationCounts[verdict.State]++
	}
	obligationItems := len(graph.Verdicts)
	obligationReady := obligationItems > 0 && obligationCounts[VerdictOpen] == 0 && obligationCounts[VerdictProvisional] == 0 && obligationCounts[VerdictContradicted] == 0 && obligationCounts[VerdictProven]+obligationCounts[VerdictWaived] == obligationItems
	obligationReason := "derived obligation verdicts"
	if obligationItems == 0 {
		obligationReason = "no generated obligations"
	}
	rows = append(rows, foundationReportRow{scope: "obligations", items: obligationItems, counts: obligationCounts, ready: obligationReady, reason: obligationReason})

	var output bytes.Buffer
	output.WriteString("| scope | items | proven | waived | provisional | open | contradicted | ready | reason |\n")
	output.WriteString("|---|---:|---:|---:|---:|---:|---:|---|---|\n")
	for _, row := range rows {
		ready := "no"
		if row.ready {
			ready = "yes"
		}
		fmt.Fprintf(&output, "| %s | %d | %d | %d | %d | %d | %d | %s | %s |\n",
			row.scope, row.items, row.counts[VerdictProven], row.counts[VerdictWaived],
			row.counts[VerdictProvisional], row.counts[VerdictOpen], row.counts[VerdictContradicted],
			ready, row.reason)
	}
	return output.Bytes(), nil
}

func foundationStateCounts() map[VerdictState]int {
	return map[VerdictState]int{
		VerdictProven: 0, VerdictWaived: 0, VerdictProvisional: 0,
		VerdictOpen: 0, VerdictContradicted: 0,
	}
}
