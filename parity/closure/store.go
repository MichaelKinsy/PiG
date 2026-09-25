package closure

import (
	"bytes"
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	_ "modernc.org/sqlite"
)

const storeSchema = `
CREATE TABLE records (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    content_hash TEXT NOT NULL UNIQUE,
    body BLOB NOT NULL
) STRICT;
CREATE TABLE verdicts (
    obligation_id TEXT PRIMARY KEY,
    state TEXT NOT NULL,
    reason TEXT NOT NULL
) STRICT;
CREATE TABLE verdict_support (
    obligation_id TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    PRIMARY KEY (obligation_id, content_hash)
) STRICT;
`

func RebuildStore(ctx context.Context, path string, graph *Graph) error {
	if graph == nil {
		return fmt.Errorf("rebuild closure store: graph is nil")
	}
	if path == "" {
		return fmt.Errorf("rebuild closure store: path is empty")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("rebuild closure store: create directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("rebuild closure store: create temporary database: %w", err)
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("rebuild closure store: close temporary database: %w", err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		return fmt.Errorf("rebuild closure store: prepare temporary database: %w", err)
	}
	defer func() { _ = os.Remove(temporaryPath) }()

	database, err := openStore(temporaryPath)
	if err != nil {
		return err
	}
	if err := writeGraph(ctx, database, graph); err != nil {
		_ = database.Close()
		return err
	}
	if err := database.Close(); err != nil {
		return fmt.Errorf("rebuild closure store: close database: %w", err)
	}
	if err := publishStore(temporaryPath, path); err != nil {
		return fmt.Errorf("rebuild closure store: publish database: %w", err)
	}
	return nil
}

func marshalWorkUnits(units []WorkUnit) ([]byte, error) {
	type unitEntry struct {
		ID                  string   `json:"id"`
		SnapshotID          string   `json:"snapshotId"`
		BehaviorID          string   `json:"behaviorId"`
		ObligationIDs       []string `json:"obligationIds"`
		WritePaths          []string `json:"writePaths"`
		TestPaths           []string `json:"testPaths"`
		FixturePaths        []string `json:"fixturePaths"`
		SemanticBoundaryIDs []string `json:"semanticBoundaryIds"`
	}
	entries := make([]unitEntry, 0, len(units))
	for _, unit := range units {
		entries = append(entries, unitEntry{
			ID: unit.ID, SnapshotID: unit.SnapshotID, BehaviorID: unit.BehaviorID,
			ObligationIDs: nonNilStrings(unit.ObligationIDs), WritePaths: nonNilStrings(unit.WritePaths),
			TestPaths: nonNilStrings(unit.TestPaths), FixturePaths: nonNilStrings(unit.FixturePaths),
			SemanticBoundaryIDs: nonNilStrings(unit.SemanticBoundaryIDs),
		})
	}
	encoded, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func indexWorkUnits(units []WorkUnit) map[string]WorkUnit {
	byID := make(map[string]WorkUnit, len(units))
	for _, unit := range units {
		byID[unit.ID] = unit
	}
	return byID
}

// GrantLeaseInStore grants one derived work unit to a holder and persists the
// resulting lease record. It reads the current active set from the graph, so the
// overlap-admission gate rejects a grant that would collide with a lease already
// held. The operation assumes serialized invocation: the campaign controller
// serializes production/evidence writes, so no two grants race the active set.
func GrantLeaseInStore(ctx context.Context, path, workUnitID, holder string) ([]byte, error) {
	database, err := openStore(path)
	if err != nil {
		return nil, err
	}
	records, err := readRecords(ctx, database)
	if closeErr := database.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	graph, err := Build(records)
	if err != nil {
		return nil, err
	}
	units, err := PlanWorkUnits(graph)
	if err != nil {
		return nil, err
	}
	lease, err := GrantLease(graph, ActiveLeases(graph), indexWorkUnits(units), workUnitID, holder)
	if err != nil {
		return nil, err
	}
	updated := append(slices.Clone(records), &lease)
	rebuilt, err := Build(updated)
	if err != nil {
		return nil, err
	}
	if err := RebuildStore(ctx, path, rebuilt); err != nil {
		return nil, err
	}
	return marshalLeaseRecord(lease)
}

// IntegrateLeaseInStore lands one active lease: it rejects a stale base (whose
// support moved since the grant), flips the lease active→integrated so it stops
// contending, and recomputes delta briefs for the leases that remain active. The
// briefs are empty until an integration also changes obligation support; landing
// proven evidence is a separate operation, so nothing here mutates a verdict.
func IntegrateLeaseInStore(ctx context.Context, path, leaseID string) ([]byte, error) {
	database, err := openStore(path)
	if err != nil {
		return nil, err
	}
	records, err := readRecords(ctx, database)
	if closeErr := database.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	before, err := Build(records)
	if err != nil {
		return nil, err
	}
	record, exists := before.Records[leaseID]
	target, isLease := record.(*Lease)
	if !exists || !isLease {
		return nil, fmt.Errorf("integrate lease: unknown lease %s", leaseID)
	}
	if target.State != LeaseActive {
		return nil, fmt.Errorf("integrate lease: lease %s is not active", leaseID)
	}
	units, err := PlanWorkUnits(before)
	if err != nil {
		return nil, err
	}
	unitByID := indexWorkUnits(units)
	if err := IntegrateLease(before, *target, unitByID); err != nil {
		return nil, err
	}
	updated := make([]Record, 0, len(records))
	for _, existing := range records {
		if lease, ok := existing.(*Lease); ok && lease.ID == leaseID {
			integrated := *lease
			integrated.State = LeaseIntegrated
			updated = append(updated, &integrated)
			continue
		}
		updated = append(updated, existing)
	}
	after, err := Build(updated)
	if err != nil {
		return nil, err
	}
	briefs, err := DeltaBriefs(before, after, ActiveLeases(after), unitByID)
	if err != nil {
		return nil, err
	}
	if err := RebuildStore(ctx, path, after); err != nil {
		return nil, err
	}
	return marshalIntegration(leaseID, briefs)
}

func marshalLeaseRecord(lease Lease) ([]byte, error) {
	encoded, err := json.MarshalIndent(lease, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func marshalIntegration(leaseID string, briefs []DeltaBrief) ([]byte, error) {
	type briefEntry struct {
		LeaseID        string `json:"leaseId"`
		WorkUnitID     string `json:"workUnitId"`
		OldFingerprint string `json:"oldFingerprint"`
		NewFingerprint string `json:"newFingerprint"`
	}
	entries := make([]briefEntry, 0, len(briefs))
	for _, brief := range briefs {
		entries = append(entries, briefEntry(brief))
	}
	encoded, err := json.MarshalIndent(struct {
		LeaseID     string       `json:"leaseId"`
		State       LeaseState   `json:"state"`
		DeltaBriefs []briefEntry `json:"deltaBriefs"`
	}{LeaseID: leaseID, State: LeaseIntegrated, DeltaBriefs: entries}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

// PlanUnits returns the deterministic parallel work units derived from the
// closure graph in the disposable store. It is read-only: it exposes the
// planner's output for a campaign controller to lease, and it neither grants
// leases nor mutates the graph.
func PlanUnits(ctx context.Context, path string) ([]byte, error) {
	database, err := openStore(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = database.Close() }()
	records, err := readRecords(ctx, database)
	if err != nil {
		return nil, err
	}
	graph, err := Build(records)
	if err != nil {
		return nil, err
	}
	units, err := PlanWorkUnits(graph)
	if err != nil {
		return nil, err
	}
	return marshalWorkUnits(units)
}

func ReadStatus(ctx context.Context, path string) ([]byte, error) {
	database, err := openStore(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = database.Close() }()
	verdicts, err := readEffectiveVerdicts(ctx, database)
	if err != nil {
		return nil, err
	}
	claims, err := readProvisionalClaims(ctx, database)
	if err != nil {
		return nil, err
	}
	return RenderStatus(verdicts, claims...)
}

// ReadEvidenceRequest returns one canonical evidence request from the
// disposable store. The record is revalidated with the complete graph before
// it is exposed, so a malformed or dangling request cannot cross the Porter
// process boundary.
func ReadEvidenceRequest(ctx context.Context, path, requestID string) ([]byte, error) {
	database, err := openStore(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = database.Close() }()
	records, err := readRecords(ctx, database)
	if err != nil {
		return nil, err
	}
	graph, err := Build(records)
	if err != nil {
		return nil, err
	}
	record, ok := graph.Records[requestID]
	if !ok {
		return nil, fmt.Errorf("evidence request %s not found", requestID)
	}
	if record.RecordKind() != KindEvidenceRequest {
		return nil, fmt.Errorf("record %s is %s, want %s", requestID, record.RecordKind(), KindEvidenceRequest)
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode evidence request %s: %w", requestID, err)
	}
	return append(encoded, '\n'), nil
}

func ReadMutationRequest(ctx context.Context, path, requestID string) ([]byte, error) {
	database, err := openStore(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = database.Close() }()
	records, err := readRecords(ctx, database)
	if err != nil {
		return nil, err
	}
	graph, err := Build(records)
	if err != nil {
		return nil, err
	}
	record, ok := graph.Records[requestID]
	if !ok {
		return nil, fmt.Errorf("mutation request %s not found", requestID)
	}
	if record.RecordKind() != KindMutationRequest {
		return nil, fmt.Errorf("record %s is %s, want %s", requestID, record.RecordKind(), KindMutationRequest)
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode mutation request %s: %w", requestID, err)
	}
	return append(encoded, '\n'), nil
}

type AgentWorkScope struct {
	EvidenceIDs  []string
	FixturePaths []string
	TestPaths    []string
}

func ReadAgentWorkScope(ctx context.Context, path, snapshotID string, obligationIDs []string) (AgentWorkScope, error) {
	database, err := openStore(path)
	if err != nil {
		return AgentWorkScope{}, err
	}
	defer func() { _ = database.Close() }()
	records, err := readRecords(ctx, database)
	if err != nil {
		return AgentWorkScope{}, err
	}
	graph, err := Build(records)
	if err != nil {
		return AgentWorkScope{}, err
	}
	if _, ok := graph.Records[snapshotID].(*Snapshot); !ok {
		return AgentWorkScope{}, fmt.Errorf("agent work snapshot %s not found", snapshotID)
	}
	obligations := make(map[string]*Obligation, len(obligationIDs))
	for _, obligationID := range obligationIDs {
		obligation, ok := graph.Records[obligationID].(*Obligation)
		if !ok {
			return AgentWorkScope{}, fmt.Errorf("agent work obligation %s not found", obligationID)
		}
		obligations[obligationID] = obligation
	}
	evidenceSet := make(map[string]struct{})
	testSet := make(map[string]struct{})
	fixtureSet := make(map[string]struct{})
	for _, assertionID := range graph.recordIDs(KindAssertion) {
		assertion := graph.Records[assertionID].(*Assertion)
		matched := false
		for _, obligation := range obligations {
			if assertion.BehaviorID == obligation.BehaviorID && assertion.FacetID == obligation.FacetID {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		test := graph.Records[assertion.TestID].(*Test)
		if test.SnapshotID != snapshotID {
			continue
		}
		if pin, ok := graph.Records[test.PinID].(*Pin); ok {
			testSet[pin.Path] = struct{}{}
		}
		for _, pinID := range test.FixturePinIDs {
			if pin, ok := graph.Records[pinID].(*Pin); ok {
				fixtureSet[pin.Path] = struct{}{}
				testSet[pin.Path] = struct{}{}
			}
		}
	}
	for _, evidenceID := range graph.recordIDs(KindEvidenceRun) {
		evidence := graph.Records[evidenceID].(*EvidenceRun)
		if evidence.SnapshotID != snapshotID {
			continue
		}
		for _, binding := range evidence.Bindings {
			if _, ok := obligations[binding.ObligationID]; ok {
				evidenceSet[evidence.ID] = struct{}{}
				break
			}
		}
	}
	return AgentWorkScope{EvidenceIDs: slices.Sorted(maps.Keys(evidenceSet)), FixturePaths: slices.Sorted(maps.Keys(fixtureSet)), TestPaths: slices.Sorted(maps.Keys(testSet))}, nil
}

func MutationReadiness(ctx context.Context, path string) ([]byte, error) {
	database, err := openStore(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = database.Close() }()
	records, err := readRecords(ctx, database)
	if err != nil {
		return nil, err
	}
	graph, err := Build(records)
	if err != nil {
		return nil, err
	}
	type readinessEntry struct {
		ObligationID string `json:"obligationId"`
		TestID       string `json:"testId"`
		RequestID    string `json:"requestId,omitempty"`
		RunID        string `json:"runId,omitempty"`
		State        string `json:"state"`
	}
	entries := []readinessEntry{}
	seen := make(map[string]struct{})
	for _, testID := range graph.recordIDs(KindTest) {
		test := graph.Records[testID].(*Test)
		if test.MutationPolicy != "required" {
			continue
		}
		for _, assertionID := range graph.recordIDs(KindAssertion) {
			assertion := graph.Records[assertionID].(*Assertion)
			if assertion.TestID != test.ID {
				continue
			}
			for _, obligationID := range graph.recordIDs(KindObligation) {
				obligation := graph.Records[obligationID].(*Obligation)
				if obligation.BehaviorID != assertion.BehaviorID || obligation.FacetID != assertion.FacetID {
					continue
				}
				key := obligation.ID + "\x00" + test.ID
				if _, duplicate := seen[key]; duplicate {
					continue
				}
				seen[key] = struct{}{}
				request, run := graph.mutationEvidence(test.ID, obligation.ID)
				if run != nil {
					entries = append(entries, readinessEntry{ObligationID: obligation.ID, TestID: test.ID, RequestID: request.ID, RunID: run.ID, State: "ready"})
					continue
				}
				requestID := ""
				runID := ""
				state := "missing-request"
				for _, candidateID := range graph.recordIDs(KindMutationRequest) {
					candidate := graph.Records[candidateID].(*MutationRequest)
					if candidate.TestID == test.ID && candidate.ObligationID == obligation.ID {
						requestID = candidate.ID
						state = "missing-run"
						for _, candidateRunID := range graph.recordIDs(KindMutationRun) {
							candidateRun := graph.Records[candidateRunID].(*MutationRun)
							if candidateRun.RequestID != candidate.ID {
								continue
							}
							runID = candidateRun.ID
							if graph.mutationRunFresh(candidate, candidateRun) && !graph.mutationRunKillsAll(candidate, candidateRun) {
								state = "surviving-run"
								break
							}
							state = "stale-run"
						}
						if state == "surviving-run" {
							break
						}
					}
				}
				entries = append(entries, readinessEntry{ObligationID: obligation.ID, TestID: test.ID, RequestID: requestID, RunID: runID, State: state})
			}
		}
	}
	encoded, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func Explain(ctx context.Context, path, obligationID string) ([]byte, error) {
	database, err := openStore(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = database.Close() }()
	verdicts, err := readEffectiveVerdicts(ctx, database)
	if err != nil {
		return nil, err
	}
	for _, verdict := range verdicts {
		if verdict.ObligationID != obligationID {
			continue
		}
		var output strings.Builder
		fmt.Fprintf(&output, "obligation\t%s\nstate\t%s\nreason\t%s\n", verdict.ObligationID, verdict.State, verdict.Reason)
		for _, support := range verdict.SupportHashes {
			fmt.Fprintf(&output, "support\t%s\n", support)
		}
		return []byte(output.String()), nil
	}
	return nil, fmt.Errorf("explain closure obligation: %s not found", obligationID)
}

func Frontier(ctx context.Context, path, recordID string) ([]byte, error) {
	database, err := openStore(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = database.Close() }()
	records, err := readRecords(ctx, database)
	if err != nil {
		return nil, err
	}
	graph, err := Build(records)
	if err != nil {
		return nil, err
	}
	hash := graph.RecordHashes[recordID]
	if hash == "" {
		return nil, fmt.Errorf("frontier closure record: %s not found", recordID)
	}
	var output strings.Builder
	output.WriteString("change\tobligation\tstate\timpact-path\n")
	for _, verdict := range graph.EffectiveVerdicts() {
		if slices.Contains(verdict.SupportHashes, hash) {
			fmt.Fprintf(&output, "%s\t%s\t%s\t%s -> %s\n", recordID, verdict.ObligationID, verdict.State, recordID, verdict.ObligationID)
		}
	}
	return []byte(output.String()), nil
}

func WhyOpen(ctx context.Context, path string) ([]byte, error) {
	database, err := openStore(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = database.Close() }()
	verdicts, err := readEffectiveVerdicts(ctx, database)
	if err != nil {
		return nil, err
	}
	var output strings.Builder
	output.WriteString("obligation\tstate\treason\n")
	for _, verdict := range verdicts {
		if verdict.State == VerdictProven || verdict.State == VerdictWaived {
			continue
		}
		fmt.Fprintf(&output, "%s\t%s\t%s\n", verdict.ObligationID, verdict.State, verdict.Reason)
	}
	claims, err := readProvisionalClaims(ctx, database)
	if err != nil {
		return nil, err
	}
	for _, claim := range claims {
		fmt.Fprintf(&output, "%s\t%s\timported status %s\n", claim.SubjectID, VerdictProvisional, claim.Status)
	}
	return []byte(output.String()), nil
}

func EvidenceReadiness(ctx context.Context, path string) ([]byte, error) {
	database, err := openStore(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = database.Close() }()
	records, err := readRecords(ctx, database)
	if err != nil {
		return nil, err
	}
	graph, err := Build(records)
	if err != nil {
		return nil, err
	}
	var output strings.Builder
	output.WriteString("obligation\trequest\tassertion\tstatus\n")
	for _, obligationID := range graph.recordIDs(KindObligation) {
		obligation := graph.Records[obligationID].(*Obligation)
		facet := graph.Records[obligation.FacetID].(*Facet)
		requestID, assertionID, witnessReady := graph.admissibleRequestedProof(obligation, facet)
		status := "ready"
		if assertionID == "" {
			status = "no-admissible-assertion"
		} else if !witnessReady {
			status = "no-admissible-witness"
		}
		fmt.Fprintf(&output, "%s\t%s\t%s\t%s\n", obligationID, requestID, assertionID, status)
	}
	return []byte(output.String()), nil
}

func (g *Graph) admissibleRequestedProof(obligation *Obligation, facet *Facet) (string, string, bool) {
	var candidateRequestID, candidateAssertionID string
	for _, requestID := range g.recordIDs(KindEvidenceRequest) {
		request := g.Records[requestID].(*EvidenceRequest)
		if !slices.Contains(request.ObligationIDs, obligation.ID) {
			continue
		}
		for _, assertionID := range request.AssertionIDs {
			assertion := g.Records[assertionID].(*Assertion)
			if assertion.BehaviorID == obligation.BehaviorID && assertion.FacetID == obligation.FacetID && assertionAdmissible(facet.Name, assertion.Class, assertion.Oracle) {
				candidateRequestID = requestID
				candidateAssertionID = assertionID
				if requestHasAdmissibleWitness(request, facet.Name) {
					return requestID, assertionID, true
				}
			}
		}
	}
	return candidateRequestID, candidateAssertionID, false
}

func requestHasAdmissibleWitness(request *EvidenceRequest, facet string) bool {
	for _, witness := range request.Witnesses {
		if witnessAdmissible(facet, witness.WitnessType) {
			return true
		}
	}
	return false
}

func MappingCoverageScopes(ctx context.Context, path string) ([]CoverageScope, error) {
	database, err := openStore(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = database.Close() }()
	records, err := readRecords(ctx, database)
	if err != nil {
		return nil, err
	}
	graph, err := Build(records)
	if err != nil {
		return nil, err
	}
	var scopes []CoverageScope
	seen := make(map[string]struct{})
	for _, targetID := range graph.recordIDs(KindTarget) {
		target := graph.Records[targetID].(*Target)
		for _, pinID := range target.PinIDs {
			pin := graph.Records[pinID].(*Pin)
			if pin.Repository != "pig" {
				continue
			}
			key := fmt.Sprintf("%s:%d-%d", pin.Path, pin.StartLine, pin.EndLine)
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			scopes = append(scopes, CoverageScope{Path: pin.Path, StartLine: pin.StartLine, EndLine: pin.EndLine})
		}
	}
	slices.SortFunc(scopes, func(left, right CoverageScope) int {
		return cmp.Or(cmp.Compare(left.Path, right.Path), cmp.Compare(left.StartLine, right.StartLine), cmp.Compare(left.EndLine, right.EndLine))
	})
	return scopes, nil
}

func MappingReview(ctx context.Context, path string) ([]byte, error) {
	database, err := openStore(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = database.Close() }()
	records, err := readRecords(ctx, database)
	if err != nil {
		return nil, err
	}
	graph, err := Build(records)
	if err != nil {
		return nil, err
	}
	var output strings.Builder
	output.WriteString("behavior\tmapping-status\tpromotion\tupstream\ttarget\tblockers\n")
	for _, mappingID := range graph.recordIDs(KindMapping) {
		mapping := graph.Records[mappingID].(*Mapping)
		behavior := graph.Records[mapping.BehaviorID].(*Behavior)
		upstream := renderPinReferences(graph, behavior.OriginPinIDs)
		var blockerIDs []string
		for _, finding := range graph.unresolvedAdversaryFindings(mapping.BehaviorID) {
			blockerIDs = append(blockerIDs, finding.ID)
		}
		status := mapping.Status
		promotion := "eligible"
		if len(blockerIDs) > 0 {
			promotion = "blocked-by-adversary"
		}
		for _, targetID := range mapping.TargetIDs {
			target := graph.Records[targetID].(*Target)
			targetReference := target.Symbol + "@" + strings.Join(renderPinReferences(graph, target.PinIDs), ",")
			fmt.Fprintf(&output, "%s\t%s\t%s\t%s\t%s\t%s\n", behavior.ID, status, promotion, strings.Join(upstream, ","), targetReference, strings.Join(blockerIDs, ","))
		}
	}
	return []byte(output.String()), nil
}

func renderPinReferences(graph *Graph, ids []string) []string {
	references := make([]string, 0, len(ids))
	for _, id := range ids {
		pin, ok := graph.Records[id].(*Pin)
		if !ok {
			continue
		}
		references = append(references, fmt.Sprintf("%s:%d-%d#%s", pin.Path, pin.StartLine, pin.EndLine, pin.QuoteHash))
	}
	slices.Sort(references)
	return references
}

func VerifyStore(ctx context.Context, path string) error {
	database, err := openStore(path)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()
	if err := verifySupportResolution(ctx, database); err != nil {
		return err
	}
	records, err := readRecords(ctx, database)
	if err != nil {
		return err
	}
	graph, err := Build(records)
	if err != nil {
		return fmt.Errorf("verify closure store: rebuild graph: %w", err)
	}
	stored, err := readStoredVerdicts(ctx, database)
	if err != nil {
		return err
	}
	derived := graph.EffectiveVerdicts()
	if !slices.EqualFunc(stored, derived, equalVerdict) {
		return fmt.Errorf("verify closure store: stored verdicts differ from derivation")
	}
	return nil
}

func openStore(path string) (*sql.DB, error) {
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open closure store: %w", err)
	}
	database.SetMaxOpenConns(1)
	return database, nil
}

func writeGraph(ctx context.Context, database *sql.DB, graph *Graph) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("rebuild closure store: begin: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if _, err := transaction.ExecContext(ctx, storeSchema); err != nil {
		return fmt.Errorf("rebuild closure store: schema: %w", err)
	}
	for _, id := range sortedRecordIDs(graph.Records) {
		record := graph.Records[id]
		body, err := json.Marshal(record)
		if err != nil {
			return fmt.Errorf("rebuild closure store: encode %s: %w", id, err)
		}
		if _, err := transaction.ExecContext(ctx,
			"INSERT INTO records(id, kind, content_hash, body) VALUES (?, ?, ?, ?)",
			id, record.RecordKind(), graph.RecordHashes[id], body); err != nil {
			return fmt.Errorf("rebuild closure store: insert %s: %w", id, err)
		}
	}
	for _, verdict := range graph.EffectiveVerdicts() {
		if _, err := transaction.ExecContext(ctx,
			"INSERT INTO verdicts(obligation_id, state, reason) VALUES (?, ?, ?)",
			verdict.ObligationID, verdict.State, verdict.Reason); err != nil {
			return fmt.Errorf("rebuild closure store: insert verdict %s: %w", verdict.ObligationID, err)
		}
		for _, support := range verdict.SupportHashes {
			if _, err := transaction.ExecContext(ctx,
				"INSERT INTO verdict_support(obligation_id, content_hash) VALUES (?, ?)", verdict.ObligationID, support); err != nil {
				return fmt.Errorf("rebuild closure store: insert support for %s: %w", verdict.ObligationID, err)
			}
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("rebuild closure store: commit: %w", err)
	}
	return nil
}

func readProvisionalClaims(ctx context.Context, database *sql.DB) ([]ProvisionalClaim, error) {
	rows, err := database.QueryContext(ctx, "SELECT body FROM records WHERE kind = ? ORDER BY id", KindProvisionalClaim)
	if err != nil {
		return nil, fmt.Errorf("read provisional claims: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var claims []ProvisionalClaim
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, fmt.Errorf("read provisional claim: %w", err)
		}
		records, err := decodeStoredJSONL(bytes.NewReader(body))
		if err != nil || len(records) != 1 {
			return nil, fmt.Errorf("read provisional claim: invalid canonical body")
		}
		claim, ok := records[0].(*ProvisionalClaim)
		if !ok {
			return nil, fmt.Errorf("read provisional claim: body has kind %s", records[0].RecordKind())
		}
		claims = append(claims, *claim)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read provisional claims: %w", err)
	}
	slices.SortFunc(claims, func(left, right ProvisionalClaim) int {
		return cmp.Or(cmp.Compare(left.SubjectID, right.SubjectID), cmp.Compare(left.ID, right.ID))
	})
	return claims, nil
}

func readEffectiveVerdicts(ctx context.Context, database *sql.DB) ([]Verdict, error) {
	verdicts, err := readStoredVerdicts(ctx, database)
	if err != nil {
		return nil, err
	}
	for index := range verdicts {
		var unresolved, supportCount, obligationCount int
		if err := database.QueryRowContext(ctx, `
SELECT count(*)
FROM verdict_support AS support
LEFT JOIN records AS record ON record.content_hash = support.content_hash
WHERE support.obligation_id = ? AND record.id IS NULL`, verdicts[index].ObligationID).Scan(&unresolved); err != nil {
			return nil, fmt.Errorf("read closure status support for %s: %w", verdicts[index].ObligationID, err)
		}
		if err := database.QueryRowContext(ctx,
			"SELECT count(*) FROM verdict_support WHERE obligation_id = ?", verdicts[index].ObligationID).Scan(&supportCount); err != nil {
			return nil, fmt.Errorf("read closure status support count for %s: %w", verdicts[index].ObligationID, err)
		}
		if err := database.QueryRowContext(ctx,
			"SELECT count(*) FROM records WHERE id = ? AND kind = ?", verdicts[index].ObligationID, KindObligation).Scan(&obligationCount); err != nil {
			return nil, fmt.Errorf("read closure status obligation %s: %w", verdicts[index].ObligationID, err)
		}
		if unresolved > 0 || supportCount == 0 || obligationCount != 1 {
			verdicts[index].State = VerdictOpen
			verdicts[index].Reason = "unresolved support"
			verdicts[index].SupportHashes = nil
		}
	}
	records, err := readRecords(ctx, database)
	if err != nil {
		return downgradeVerdicts(verdicts, "closure records invalid"), nil
	}
	graph, err := Build(records)
	if err != nil {
		return downgradeVerdicts(verdicts, "closure graph invalid"), nil
	}
	seen := make(map[string]struct{}, len(verdicts))
	for index := range verdicts {
		seen[verdicts[index].ObligationID] = struct{}{}
		if verdicts[index].State == VerdictOpen && verdicts[index].Reason == "unresolved support" {
			continue
		}
		derived, exists := graph.Verdicts[verdicts[index].ObligationID]
		if !exists {
			verdicts[index] = Verdict{ObligationID: verdicts[index].ObligationID, State: VerdictOpen, Reason: "obligation is not derivable"}
			continue
		}
		verdicts[index] = derived
	}
	for _, derived := range graph.EffectiveVerdicts() {
		if _, exists := seen[derived.ObligationID]; exists {
			continue
		}
		verdicts = append(verdicts, Verdict{ObligationID: derived.ObligationID, State: VerdictOpen, Reason: "stored verdict missing"})
	}
	slices.SortFunc(verdicts, func(left, right Verdict) int { return cmp.Compare(left.ObligationID, right.ObligationID) })
	return verdicts, nil
}

func downgradeVerdicts(verdicts []Verdict, reason string) []Verdict {
	for index := range verdicts {
		verdicts[index].State = VerdictOpen
		verdicts[index].Reason = reason
		verdicts[index].SupportHashes = nil
	}
	return verdicts
}

func readStoredVerdicts(ctx context.Context, database *sql.DB) ([]Verdict, error) {
	rows, err := database.QueryContext(ctx, "SELECT obligation_id, state, reason FROM verdicts ORDER BY obligation_id")
	if err != nil {
		return nil, fmt.Errorf("read closure verdicts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var verdicts []Verdict
	for rows.Next() {
		var verdict Verdict
		if err := rows.Scan(&verdict.ObligationID, &verdict.State, &verdict.Reason); err != nil {
			return nil, fmt.Errorf("read closure verdict: %w", err)
		}
		verdicts = append(verdicts, verdict)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read closure verdicts: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("read closure verdicts: %w", err)
	}
	for index := range verdicts {
		supportRows, err := database.QueryContext(ctx,
			"SELECT content_hash FROM verdict_support WHERE obligation_id = ? ORDER BY content_hash", verdicts[index].ObligationID)
		if err != nil {
			return nil, fmt.Errorf("read closure support for %s: %w", verdicts[index].ObligationID, err)
		}
		for supportRows.Next() {
			var support string
			if err := supportRows.Scan(&support); err != nil {
				_ = supportRows.Close()
				return nil, fmt.Errorf("read closure support for %s: %w", verdicts[index].ObligationID, err)
			}
			verdicts[index].SupportHashes = append(verdicts[index].SupportHashes, support)
		}
		if err := supportRows.Close(); err != nil {
			return nil, fmt.Errorf("read closure support for %s: %w", verdicts[index].ObligationID, err)
		}
	}
	return verdicts, nil
}

func verifySupportResolution(ctx context.Context, database *sql.DB) error {
	rows, err := database.QueryContext(ctx, `
SELECT support.obligation_id, support.content_hash
FROM verdict_support AS support
LEFT JOIN records AS record ON record.content_hash = support.content_hash
WHERE record.id IS NULL
ORDER BY support.obligation_id, support.content_hash`)
	if err != nil {
		return fmt.Errorf("verify closure store support: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		var obligationID, hash string
		if err := rows.Scan(&obligationID, &hash); err != nil {
			return fmt.Errorf("verify closure store support: %w", err)
		}
		return fmt.Errorf("verify closure store: unresolved support %s for %s", hash, obligationID)
	}
	return rows.Err()
}

func readRecords(ctx context.Context, database *sql.DB) ([]Record, error) {
	rows, err := database.QueryContext(ctx, "SELECT content_hash, body FROM records ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("read closure records: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var input bytes.Buffer
	for rows.Next() {
		var storedHash string
		var body []byte
		if err := rows.Scan(&storedHash, &body); err != nil {
			return nil, fmt.Errorf("read closure record: %w", err)
		}
		if actual := HashBytes(body); actual != storedHash {
			return nil, fmt.Errorf("read closure record: body hash %s, stored %s", actual, storedHash)
		}
		input.Write(body)
		input.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read closure records: %w", err)
	}
	records, err := decodeStoredJSONL(&input)
	if err != nil {
		return nil, fmt.Errorf("read closure records: %w", err)
	}
	return records, nil
}

func equalVerdict(left, right Verdict) bool {
	return left.ObligationID == right.ObligationID && left.State == right.State && left.Reason == right.Reason &&
		slices.Equal(left.SupportHashes, right.SupportHashes)
}

func SafeStorePath(root, configured string) (string, error) {
	rootAbsolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve closure root: %w", err)
	}
	rootReal, err := filepath.EvalSymlinks(rootAbsolute)
	if err != nil {
		return "", fmt.Errorf("resolve closure root: %w", err)
	}
	path := configured
	if path == "" {
		path = filepath.Join(rootAbsolute, "closure.db")
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(rootAbsolute, path)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve closure store path: %w", err)
	}
	relative, err := filepath.Rel(rootAbsolute, path)
	if err != nil {
		return "", fmt.Errorf("resolve closure store path: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("closure store path escapes root")
	}
	existingParent := filepath.Dir(path)
	for {
		if _, statErr := os.Stat(existingParent); statErr == nil {
			break
		} else if !os.IsNotExist(statErr) {
			return "", fmt.Errorf("resolve closure store parent: %w", statErr)
		}
		next := filepath.Dir(existingParent)
		if next == existingParent {
			return "", fmt.Errorf("resolve closure store parent: no existing ancestor")
		}
		existingParent = next
	}
	parentReal, err := filepath.EvalSymlinks(existingParent)
	if err != nil {
		return "", fmt.Errorf("resolve closure store parent: %w", err)
	}
	realRelative, err := filepath.Rel(rootReal, parentReal)
	if err != nil {
		return "", fmt.Errorf("resolve closure store parent: %w", err)
	}
	if realRelative == ".." || strings.HasPrefix(realRelative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("closure store parent symlink escapes root")
	}
	return path, nil
}
