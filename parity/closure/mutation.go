package closure

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

func NewMutant(snapshotID, obligationID, testID, targetID, pinID, facet, mode, operator string, edit MutationEdit, expectedFailureTest, expectedFailureHash string) (*Mutant, error) {
	mutant := &Mutant{
		Kind: KindMutant, SnapshotID: snapshotID, ObligationID: obligationID, TestID: testID,
		TargetID: targetID, PinID: pinID, Facet: facet, Mode: mode, Operator: operator,
		Edit: edit, ExpectedFailureTest: expectedFailureTest, ExpectedFailureHash: expectedFailureHash,
	}
	if err := validateMutantLocal(mutant); err != nil {
		return nil, err
	}
	id, err := mutantContentID(mutant)
	if err != nil {
		return nil, err
	}
	mutant.ID = id
	return mutant, nil
}

func NewMutationRequest(snapshotID, obligationID, testID string, mutants []*Mutant, commands []EvidenceCommand, environment []string, timeoutSeconds int) (*MutationRequest, error) {
	mutantIDs := make([]string, 0, len(mutants))
	automatic := 0
	for _, mutant := range mutants {
		if mutant == nil {
			return nil, fmt.Errorf("mutation request contains nil mutant")
		}
		if err := validateMutantLocal(mutant); err != nil {
			return nil, err
		}
		if mutant.SnapshotID != snapshotID || mutant.ObligationID != obligationID || mutant.TestID != testID {
			return nil, fmt.Errorf("mutation request mutant %s differs from request scope", mutant.ID)
		}
		if mutant.Mode == "automatic" {
			automatic++
		}
		mutantIDs = append(mutantIDs, mutant.ID)
	}
	if len(mutantIDs) == 0 || !sortedUnique(mutantIDs) || len(commands) != automatic || environment == nil || !sortedUnique(environment) || timeoutSeconds < 1 {
		return nil, fmt.Errorf("mutation request has invalid mutants, commands, environment, or timeout")
	}
	for index, command := range commands {
		if err := validateMutationCommand(command); err != nil {
			return nil, fmt.Errorf("mutation request command %d: %w", index+1, err)
		}
	}
	request := &MutationRequest{
		Kind: KindMutationRequest, SnapshotID: snapshotID, ObligationID: obligationID, TestID: testID,
		MutantIDs: mutantIDs, Commands: slices.Clone(commands), Environment: slices.Clone(environment), TimeoutSeconds: timeoutSeconds,
	}
	id, err := mutationRequestContentID(request)
	if err != nil {
		return nil, err
	}
	request.ID = id
	return request, nil
}

func validateMutantLocal(mutant *Mutant) error {
	if mutant == nil || mutant.SnapshotID == "" || mutant.ObligationID == "" || mutant.TestID == "" || mutant.TargetID == "" || mutant.PinID == "" || !oneOf(mutant.Mode, "automatic", "manual") || !mutationOperatorAllowed(mutant.Facet, mutant.Operator) || !ValidHash(mutant.ExpectedFailureHash) {
		return fmt.Errorf("mutant has incomplete identity, mode, facet, operator, or failure hash")
	}
	if err := validateMutationEdit(mutant.Edit); err != nil {
		return err
	}
	if mutant.Mode == "automatic" && mutant.ExpectedFailureTest == "" {
		return fmt.Errorf("automatic mutant requires an expected failing test")
	}
	if mutant.Mode == "manual" && mutant.ExpectedFailureTest != "" {
		return fmt.Errorf("manual mutant cannot name an automatic failing test")
	}
	return nil
}

func validateMutationEdit(edit MutationEdit) error {
	clean := filepath.ToSlash(filepath.Clean(edit.Path))
	if edit.Path == "" || edit.Path != strings.TrimSpace(edit.Path) || strings.ContainsRune(edit.Path, '\x00') || filepath.IsAbs(edit.Path) || !filepath.IsLocal(edit.Path) || clean != edit.Path {
		return fmt.Errorf("mutation edit path must be canonical and repository-relative")
	}
	if !ValidHash(edit.OriginalHash) || !ValidHash(edit.MutatedHash) || edit.Before == "" || edit.Before == edit.After || strings.ContainsRune(edit.Before, '\x00') || strings.ContainsRune(edit.After, '\x00') {
		return fmt.Errorf("mutation edit has invalid hashes or replacement")
	}
	return nil
}

func validateMutationCommand(command EvidenceCommand) error {
	if filepath.Base(command.Name) != "go" || len(command.Args) == 0 || command.Args[0] != "test" {
		return fmt.Errorf("automatic mutation commands must invoke go test directly")
	}
	hasJSON := false
	hasRun := false
	for index, argument := range command.Args {
		if strings.ContainsRune(argument, '\x00') {
			return fmt.Errorf("command argument contains NUL")
		}
		if argument == "-json" {
			hasJSON = true
		}
		if argument == "-run" && index+1 < len(command.Args) && command.Args[index+1] != "" {
			hasRun = true
		}
		if strings.HasPrefix(argument, "-run=") && strings.TrimPrefix(argument, "-run=") != "" {
			hasRun = true
		}
	}
	if !hasJSON || !hasRun {
		return fmt.Errorf("automatic mutation commands require -json and a nonempty -run filter")
	}
	return nil
}

func mutantContentID(mutant *Mutant) (string, error) {
	return mutationContentID("mutant", mutant)
}

func mutationRequestContentID(request *MutationRequest) (string, error) {
	return mutationContentID("mutation-request", request)
}

func mutationRunContentID(run *MutationRun) (string, error) {
	return mutationContentID("mutation-run", run)
}

func mutationAttestationContentID(attestation *MutationAttestation) (string, error) {
	return mutationContentID("mutation-attestation", attestation)
}

func mutationContentID(prefix string, value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		return "", err
	}
	delete(object, "id")
	canonical, err := json.Marshal(object)
	if err != nil {
		return "", err
	}
	return prefix + ":" + strings.TrimPrefix(HashBytes(canonical), "sha256:"), nil
}

func mutationOperatorAllowed(facet, operator string) bool {
	switch facet {
	case "result", "state", "history", "restoration":
		return oneOf(operator, "change-value", "drop-branch", "invert-guard")
	case "error":
		return oneOf(operator, "drop-error", "change-error", "swallow-error")
	case "cancel", "shutdown", "lifetime":
		return oneOf(operator, "remove-cancel", "allow-post-cancel-effect", "leak-owner")
	case "order", "concurrency":
		return oneOf(operator, "swap-order", "drop-step", "duplicate-step")
	case "dispatch", "input":
		return oneOf(operator, "drop-dispatch", "wrong-recipient", "invert-guard")
	case "wire", "compat":
		return oneOf(operator, "change-field", "drop-field", "change-order")
	case "render", "layout":
		return oneOf(operator, "change-cell", "drop-render", "change-width")
	case "persistence":
		return oneOf(operator, "drop-write", "change-roundtrip", "overwrite-external-edit")
	case "realization", "resource", "boundedness":
		return oneOf(operator, "drop-realization", "leak-resource", "remove-bound")
	default:
		return false
	}
}

func resultMutantIDs(results []MutationResult) []string {
	ids := make([]string, 0, len(results))
	for _, result := range results {
		ids = append(ids, result.MutantID)
	}
	return ids
}

func (g *Graph) testAssertsObligation(testID string, obligation *Obligation) bool {
	for _, id := range g.recordIDs(KindAssertion) {
		assertion := g.Records[id].(*Assertion)
		if assertion.TestID == testID && assertion.BehaviorID == obligation.BehaviorID && assertion.FacetID == obligation.FacetID {
			return true
		}
	}
	return false
}

func (g *Graph) mutationBindings(request *MutationRequest) []EvidenceBinding {
	ids := []string{request.ID, request.SnapshotID, request.ObligationID, request.TestID}
	ids = append(ids, request.MutantIDs...)
	if obligation, ok := g.Records[request.ObligationID].(*Obligation); ok {
		ids = append(ids, obligation.BehaviorID, obligation.FacetID, obligation.RuleID)
		ids = append(ids, obligation.OriginPinIDs...)
		if mapping := g.acceptedMapping(obligation.BehaviorID); mapping != nil {
			ids = append(ids, mapping.ID)
			ids = append(ids, g.mappingAcceptanceIDs(mapping)...)
			for _, targetID := range mapping.TargetIDs {
				ids = append(ids, targetID)
				if target, targetOK := g.Records[targetID].(*Target); targetOK {
					ids = append(ids, target.PinIDs...)
				}
			}
		}
	}
	if test, ok := g.Records[request.TestID].(*Test); ok {
		ids = append(ids, test.PinID)
		ids = append(ids, test.FixturePinIDs...)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	bindings := make([]EvidenceBinding, 0, len(ids))
	for _, id := range ids {
		if hash := g.RecordHashes[id]; hash != "" {
			bindings = append(bindings, EvidenceBinding{RecordID: id, ContentHash: hash})
		}
	}
	return bindings
}

func (g *Graph) mutationEvidence(testID, obligationID string) (*MutationRequest, *MutationRun) {
	var provingRequest *MutationRequest
	var provingRun *MutationRun
	for _, runID := range g.recordIDs(KindMutationRun) {
		run := g.Records[runID].(*MutationRun)
		request, ok := g.Records[run.RequestID].(*MutationRequest)
		if !ok || request.TestID != testID || request.ObligationID != obligationID || !g.mutationRunFresh(request, run) {
			continue
		}
		if !g.mutationRunKillsAll(request, run) {
			return nil, nil
		}
		provingRequest, provingRun = request, run
	}
	return provingRequest, provingRun
}

func (g *Graph) blockingMutationRun(testID, obligationID string) *MutationRun {
	for _, runID := range g.recordIDs(KindMutationRun) {
		run := g.Records[runID].(*MutationRun)
		request, ok := g.Records[run.RequestID].(*MutationRequest)
		if ok && request.TestID == testID && request.ObligationID == obligationID && g.mutationRunFresh(request, run) && !g.mutationRunKillsAll(request, run) {
			return run
		}
	}
	return nil
}

func (g *Graph) mutationRunFresh(request *MutationRequest, run *MutationRun) bool {
	snapshot, ok := g.Records[run.SnapshotID].(*Snapshot)
	return ok && g.mutationAttestation(run.ID) != nil && run.SnapshotID == request.SnapshotID && run.ToolchainHash == snapshot.ToolchainHash && run.EnvironmentHash == snapshot.EnvironmentHash && slices.Equal(run.Support, g.mutationBindings(request))
}

func (g *Graph) mutationRunKillsAll(request *MutationRequest, run *MutationRun) bool {
	for _, result := range run.Results {
		mutant := g.Records[result.MutantID].(*Mutant)
		if result.FailureHash != mutant.ExpectedFailureHash || result.BoundTest != mutant.ExpectedFailureTest || result.MutatedPath != mutant.Edit.Path || result.MutatedHash != mutant.Edit.MutatedHash {
			return false
		}
		if mutant.Mode == "manual" {
			continue
		}
		if result.CommandIndex < 1 || result.CommandIndex > len(run.Commands) {
			return false
		}
		command := run.Commands[result.CommandIndex-1]
		baseline := run.Baselines[result.CommandIndex-1]
		if baseline.ExitCode != 0 || baseline.Error != "" || baseline.Overflow || command.ExitCode == 0 || command.Error != "" || command.Overflow || result.FailureTest != mutant.ExpectedFailureTest {
			return false
		}
	}
	return len(run.Results) == len(request.MutantIDs)
}

func (g *Graph) mutationAttestation(runID string) *MutationAttestation {
	for _, id := range g.recordIDs(KindMutationAttestation) {
		attestation := g.Records[id].(*MutationAttestation)
		if attestation.RunID == runID {
			return attestation
		}
	}
	return nil
}
