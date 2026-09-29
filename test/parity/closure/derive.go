package closure

import "slices"

func (g *Graph) deriveVerdicts() {
	for _, id := range sortedRecordIDs(g.Records) {
		obligation, ok := g.Records[id].(*Obligation)
		if !ok {
			continue
		}
		g.Verdicts[id] = g.deriveVerdict(obligation)
	}
}

func (g *Graph) deriveVerdict(obligation *Obligation) Verdict {
	if decision := g.scopedDecision(obligation.ID, "waive", "defer", "designed-out", "accept-divergence"); decision != nil {
		return g.verdict(obligation.ID, VerdictWaived, "reviewed decision", obligation.ID, decision.ID)
	}
	failures := g.failedEvidence(obligation.ID)
	adjudication := g.validAdjudication(obligation.ID, failures)
	if len(failures) > 0 && adjudication == nil {
		supportIDs := []string{obligation.ID}
		for _, failure := range failures {
			supportIDs = append(supportIDs, failure.request.ID, failure.evidence.ID)
		}
		return g.verdict(obligation.ID, VerdictContradicted, "fresh evidence failed", supportIDs...)
	}

	if findings := g.unresolvedAdversaryFindings(obligation.BehaviorID); len(findings) > 0 {
		supportIDs := []string{obligation.ID}
		for _, finding := range findings {
			supportIDs = append(supportIDs, finding.ID)
			if decision := g.scopedDecision(finding.ID, "confirm-agent-finding"); decision != nil {
				supportIDs = append(supportIDs, decision.ID)
			}
		}
		return g.verdict(obligation.ID, VerdictOpen, "unresolved adversary finding", supportIDs...)
	}
	mapping := g.acceptedMapping(obligation.BehaviorID)
	if mapping == nil {
		return g.verdict(obligation.ID, VerdictOpen, "no accepted mapping", obligation.ID)
	}
	if !g.mappingAcceptanceCovers(mapping) {
		return g.verdict(obligation.ID, VerdictOpen, "mapping acceptance does not cover behavior", obligation.ID, mapping.ID)
	}
	if !g.productionTargetsReachable(obligation.BehaviorID, mapping.TargetIDs) {
		return g.verdict(obligation.ID, VerdictOpen, "mapped targets lack production reachability", obligation.ID, mapping.ID)
	}
	assertion, request, evidence, witness, attestation := g.provingEvidence(obligation, mapping)
	if evidence == nil {
		return g.verdict(obligation.ID, VerdictOpen, "no admissible passing evidence", obligation.ID, mapping.ID)
	}
	test := g.Records[assertion.TestID].(*Test)
	mutationRequest, mutationRun := g.mutationEvidence(test.ID, obligation.ID)
	if test.MutationPolicy == "required" && mutationRun == nil {
		if blocking := g.blockingMutationRun(test.ID, obligation.ID); blocking != nil {
			return g.verdict(obligation.ID, VerdictContradicted, "relevant mutant survived or failed for the wrong reason", obligation.ID, mapping.ID, test.ID, blocking.RequestID, blocking.ID)
		}
		return g.verdict(obligation.ID, VerdictOpen, "no admissible mutation evidence", obligation.ID, mapping.ID, test.ID)
	}

	supportIDs := []string{obligation.ID, obligation.BehaviorID, obligation.FacetID, obligation.RuleID, mapping.ID,
		assertion.ID, assertion.TestID, request.ID, evidence.ID, witness.ID, attestation.ID, evidence.SnapshotID}
	if mutationRun != nil {
		supportIDs = append(supportIDs, mutationRequest.ID, mutationRun.ID)
		if attestation := g.mutationAttestation(mutationRun.ID); attestation != nil {
			supportIDs = append(supportIDs, attestation.ID)
		}
		supportIDs = append(supportIDs, mutationRequest.MutantIDs...)
		for _, result := range mutationRun.Results {
			if result.DecisionID != "" {
				supportIDs = append(supportIDs, result.DecisionID)
			}
		}
	}
	supportIDs = append(supportIDs, g.mappingAcceptanceIDs(mapping)...)
	if adjudication != nil {
		supportIDs = append(supportIDs, adjudication.ID)
		for _, failure := range failures {
			supportIDs = append(supportIDs, failure.request.ID, failure.evidence.ID)
		}
	}
	supportIDs = append(supportIDs, obligation.OriginPinIDs...)
	behavior := g.Records[obligation.BehaviorID].(*Behavior)
	supportIDs = append(supportIDs, behavior.OriginPinIDs...)
	supportIDs = append(supportIDs, behavior.FactIDs...)
	for _, targetID := range mapping.TargetIDs {
		supportIDs = append(supportIDs, targetID)
		target := g.Records[targetID].(*Target)
		supportIDs = append(supportIDs, target.SnapshotID)
		supportIDs = append(supportIDs, target.PinIDs...)
		supportIDs = append(supportIDs, target.FactIDs...)
		if reachability := g.productionReachability(obligation.BehaviorID, targetID); reachability != nil {
			supportIDs = append(supportIDs, reachability.ID)
			supportIDs = append(supportIDs, reachability.RootPinIDs...)
		}
	}
	supportIDs = append(supportIDs, test.SnapshotID, test.PinID)
	supportIDs = append(supportIDs, test.FixturePinIDs...)
	for _, group := range evidence.Bindings {
		if group.ObligationID != obligation.ID {
			continue
		}
		for _, binding := range group.Support {
			supportIDs = append(supportIDs, binding.RecordID)
		}
	}
	supportIDs = append(supportIDs, witness.TargetIDs...)
	supportIDs = append(supportIDs, witness.SubjectPinIDs...)
	supportIDs = append(supportIDs, evidence.SubjectPinIDs...)
	return g.verdict(obligation.ID, VerdictProven, "admissible assertion and production execution", supportIDs...)
}

func (g *Graph) provingEvidence(obligation *Obligation, mapping *Mapping) (*Assertion, *EvidenceRequest, *EvidenceRun, *ExecutionWitness, *EvidenceAttestation) {
	facet := g.Records[obligation.FacetID].(*Facet)
	for _, evidenceID := range g.recordIDs(KindEvidenceRun) {
		evidence := g.Records[evidenceID].(*EvidenceRun)
		if evidence.Result != "pass" || !g.evidenceFresh(obligation.ID, evidence) {
			continue
		}
		attestation := g.evidenceAttestation(evidence.ID)
		if attestation == nil {
			continue
		}
		request := g.Records[evidence.RequestID].(*EvidenceRequest)
		if evidence.SnapshotID != request.SnapshotID || !slices.Contains(request.ObligationIDs, obligation.ID) {
			continue
		}
		for _, assertionID := range evidence.AssertionIDs {
			if !slices.Contains(request.AssertionIDs, assertionID) {
				continue
			}
			assertion := g.Records[assertionID].(*Assertion)
			if assertion.BehaviorID != obligation.BehaviorID || assertion.FacetID != obligation.FacetID || !assertionAdmissible(facet.Name, assertion.Class, assertion.Oracle) {
				continue
			}
			for _, witnessID := range evidence.WitnessIDs {
				witness := g.Records[witnessID].(*ExecutionWitness)
				if witnessAdmissible(facet.Name, witness.WitnessType) && containsAll(witness.TargetIDs, mapping.TargetIDs) {
					return assertion, request, evidence, witness, attestation
				}
			}
		}
	}
	return nil, nil, nil, nil, nil
}

func (g *Graph) evidenceAttestation(evidenceID string) *EvidenceAttestation {
	for _, id := range g.recordIDs(KindEvidenceAttestation) {
		attestation := g.Records[id].(*EvidenceAttestation)
		if attestation.EvidenceID == evidenceID {
			return attestation
		}
	}
	return nil
}

type failedEvidenceRun struct {
	evidence *EvidenceRun
	request  *EvidenceRequest
}

func (g *Graph) failedEvidence(obligationID string) []failedEvidenceRun {
	var failures []failedEvidenceRun
	for _, evidenceID := range g.recordIDs(KindEvidenceRun) {
		evidence := g.Records[evidenceID].(*EvidenceRun)
		if evidence.Result != "fail" || g.evidenceAttestation(evidence.ID) == nil || !g.evidenceFresh(obligationID, evidence) {
			continue
		}
		request := g.Records[evidence.RequestID].(*EvidenceRequest)
		if evidence.SnapshotID == request.SnapshotID && slices.Contains(request.ObligationIDs, obligationID) {
			failures = append(failures, failedEvidenceRun{evidence: evidence, request: request})
		}
	}
	return failures
}

func (g *Graph) evidenceFresh(obligationID string, evidence *EvidenceRun) bool {
	snapshot, ok := g.Records[evidence.SnapshotID].(*Snapshot)
	if !ok || evidence.ToolchainHash != snapshot.ToolchainHash || evidence.EnvironmentHash != snapshot.EnvironmentHash {
		return false
	}
	request, ok := g.Records[evidence.RequestID].(*EvidenceRequest)
	if !ok {
		return false
	}
	for _, group := range evidence.Bindings {
		if group.ObligationID == obligationID {
			return slices.Equal(group.Support, g.evidenceBindingsForObligation(request, obligationID))
		}
	}
	return false
}

func (g *Graph) validAdjudication(obligationID string, failures []failedEvidenceRun) *Decision {
	if len(failures) == 0 {
		return nil
	}
	decision := g.scopedDecision(obligationID, "adjudicate")
	if decision == nil {
		return nil
	}
	hashes := make([]string, 0, len(failures))
	for _, failure := range failures {
		hashes = append(hashes, g.RecordHashes[failure.evidence.ID])
	}
	slices.Sort(hashes)
	if !slices.Equal(decision.FailureHashes, hashes) {
		return nil
	}
	return decision
}

func (g *Graph) mappingForBehavior(behaviorID string) *Mapping {
	for _, id := range g.recordIDs(KindMapping) {
		mapping := g.Records[id].(*Mapping)
		if mapping.BehaviorID == behaviorID {
			return mapping
		}
	}
	return nil
}

func (g *Graph) acceptedMapping(behaviorID string) *Mapping {
	for _, id := range g.recordIDs(KindMapping) {
		mapping := g.Records[id].(*Mapping)
		if mapping.BehaviorID == behaviorID && (mapping.Status == "decided" || mapping.Status == "derived") {
			return mapping
		}
	}
	return nil
}

func (g *Graph) mappingAcceptanceCovers(mapping *Mapping) bool {
	if mapping.Status == "derived" {
		return len(g.validateDerivedMapping(mapping)) == 0
	}
	decision, ok := g.Records[mapping.DecisionID].(*Decision)
	return mapping.Status == "decided" && ok && g.decisionCurrent(decision) && decision.DecisionType == "mapping" && slices.Contains(decision.ScopeIDs, mapping.BehaviorID)
}

func (g *Graph) mappingAcceptanceIDs(mapping *Mapping) []string {
	var ids []string
	if mapping.Status == "derived" {
		ids = append([]string{mapping.RuleID}, mapping.FactIDs...)
	} else {
		ids = []string{mapping.DecisionID}
	}
	ids = append(ids, g.agentFindingSupportIDs(mapping.BehaviorID)...)
	slices.Sort(ids)
	return slices.Compact(ids)
}

func (g *Graph) productionTargetsReachable(behaviorID string, targetIDs []string) bool {
	for _, targetID := range targetIDs {
		if g.productionReachability(behaviorID, targetID) == nil {
			return false
		}
	}
	return len(targetIDs) > 0
}

func (g *Graph) productionReachability(behaviorID, targetID string) *Reachability {
	for _, id := range g.recordIDs(KindReachability) {
		reachability := g.Records[id].(*Reachability)
		if reachability.BehaviorID == behaviorID && reachability.TargetID == targetID &&
			(reachability.Class == "prod-reachable" || reachability.Class == "exported") {
			return reachability
		}
	}
	return nil
}

func (g *Graph) scopedDecision(scopeID string, decisionTypes ...string) *Decision {
	for _, id := range g.recordIDs(KindDecision) {
		decision := g.Records[id].(*Decision)
		if g.decisionCurrent(decision) && slices.Contains(decisionTypes, decision.DecisionType) && slices.Contains(decision.ScopeIDs, scopeID) {
			return decision
		}
	}
	return nil
}

func (g *Graph) decisionCurrent(decision *Decision) bool {
	if decision.ValidThroughCommit == "" {
		return true
	}
	commits := make(map[string]struct{})
	for _, scopeID := range decision.ScopeIDs {
		for _, snapshotID := range g.recordSnapshotIDs(scopeID) {
			if snapshot, ok := g.Records[snapshotID].(*Snapshot); ok {
				commits[snapshot.TargetCommit] = struct{}{}
			}
		}
	}
	_, current := commits[decision.ValidThroughCommit]
	return current && len(commits) == 1
}

func (g *Graph) recordSnapshotIDs(recordID string) []string {
	record := g.Records[recordID]
	switch value := record.(type) {
	case *Snapshot:
		return []string{value.ID}
	case *Pin:
		return []string{value.SnapshotID}
	case *Fact:
		return []string{value.SnapshotID}
	case *Hypothesis:
		return []string{value.SnapshotID}
	case *ProvisionalClaim:
		return []string{value.SnapshotID}
	case *Target:
		return []string{value.SnapshotID}
	case *Test:
		return []string{value.SnapshotID}
	case *ExecutionWitness:
		return []string{value.SnapshotID}
	case *EvidenceRequest:
		return []string{value.SnapshotID}
	case *EvidenceRun:
		return []string{value.SnapshotID}
	case *Mutant:
		return []string{value.SnapshotID}
	case *MutationRequest:
		return []string{value.SnapshotID}
	case *MutationRun:
		return []string{value.SnapshotID}
	case *MutationAttestation:
		return g.recordSnapshotIDs(value.RunID)
	case *Behavior:
		return g.pinSnapshotIDs(value.OriginPinIDs)
	case *Obligation:
		return g.recordSnapshotIDs(value.BehaviorID)
	case *Mapping:
		return g.recordSnapshotIDs(value.BehaviorID)
	default:
		return nil
	}
}

func (g *Graph) pinSnapshotIDs(pinIDs []string) []string {
	var snapshotIDs []string
	for _, pinID := range pinIDs {
		if pin, ok := g.Records[pinID].(*Pin); ok {
			snapshotIDs = append(snapshotIDs, pin.SnapshotID)
		}
	}
	slices.Sort(snapshotIDs)
	return slices.Compact(snapshotIDs)
}

func (g *Graph) verdict(obligationID string, state VerdictState, reason string, supportIDs ...string) Verdict {
	hashes := make([]string, 0, len(supportIDs))
	seen := make(map[string]struct{}, len(supportIDs))
	for _, id := range supportIDs {
		hash := g.RecordHashes[id]
		if hash == "" {
			continue
		}
		if _, duplicate := seen[hash]; duplicate {
			continue
		}
		seen[hash] = struct{}{}
		hashes = append(hashes, hash)
	}
	slices.Sort(hashes)
	return Verdict{ObligationID: obligationID, State: state, SupportHashes: hashes, Reason: reason}
}

func (g *Graph) recordIDs(kind Kind) []string {
	var ids []string
	for id, record := range g.Records {
		if record.RecordKind() == kind {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

func assertionAdmissible(facet, class, oracle string) bool {
	if facet == "wire" || facet == "compat" {
		return class == "A3" && (oracle == "source-generated-differential" || oracle == "pinned-source-differential")
	}
	return class == "A2" || class == "A3"
}

func witnessAdmissible(facet, witnessType string) bool {
	switch witnessType {
	case "provider-capture":
		return facet == "wire" || facet == "compat" || facet == "result" || facet == "error" || facet == "order"
	case "go-covered-range":
		return facet == "result" || facet == "error" || facet == "wire" || facet == "compat"
	case "state-transition":
		return facet == "state" || facet == "history" || facet == "restoration" || facet == "order" || facet == "concurrency"
	case "persistence-roundtrip":
		return facet == "persistence" || facet == "restoration"
	case "cancellation-trace":
		return facet == "cancel" || facet == "error" || facet == "shutdown" || facet == "lifetime"
	case "terminal-trace":
		return facet == "input" || facet == "render" || facet == "layout"
	case "event-delivery":
		return facet == "dispatch" || facet == "order"
	case "subprocess-invocation":
		return facet == "dispatch" || facet == "result" || facet == "error"
	case "extension-realization":
		return facet == "realization" || facet == "dispatch" || facet == "lifetime" || facet == "shutdown"
	case "resource-trace":
		return facet == "resource" || facet == "boundedness" || facet == "lifetime" || facet == "shutdown"
	case "typescript-covered-range":
		return facet == "result" || facet == "error"
	default:
		return false
	}
}

func containsAll(have, required []string) bool {
	for _, value := range required {
		if !slices.Contains(have, value) {
			return false
		}
	}
	return len(required) > 0
}
