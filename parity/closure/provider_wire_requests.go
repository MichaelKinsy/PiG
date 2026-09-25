package closure

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

func AddProviderWireEvidenceRequests(records []Record) ([]Record, error) {
	result := slices.Clone(records)
	obligationByFacet := make(map[string]string)
	for _, record := range records {
		obligation, ok := record.(*Obligation)
		if !ok || !strings.HasPrefix(obligation.BehaviorID, "behavior:provider-wire:") {
			continue
		}
		obligationByFacet[obligation.BehaviorID+"\x00"+obligation.FacetID] = obligation.ID
	}
	assertionsByTest := make(map[string][]string)
	obligationsByTest := make(map[string][]string)
	for _, record := range records {
		assertion, ok := record.(*Assertion)
		if !ok || !strings.HasPrefix(assertion.BehaviorID, "behavior:provider-wire:") {
			continue
		}
		obligationID := obligationByFacet[assertion.BehaviorID+"\x00"+assertion.FacetID]
		if obligationID == "" {
			return nil, fmt.Errorf("provider-wire assertion %s has no matching obligation", assertion.ID)
		}
		assertionsByTest[assertion.TestID] = append(assertionsByTest[assertion.TestID], assertion.ID)
		obligationsByTest[assertion.TestID] = append(obligationsByTest[assertion.TestID], obligationID)
	}
	testIDs := make([]string, 0, len(assertionsByTest))
	for testID := range assertionsByTest {
		testIDs = append(testIDs, testID)
	}
	slices.Sort(testIDs)
	for _, testID := range testIDs {
		assertionIDs := assertionsByTest[testID]
		obligationIDs := obligationsByTest[testID]
		slices.Sort(assertionIDs)
		slices.Sort(obligationIDs)
		assertionIDs = slices.Compact(assertionIDs)
		obligationIDs = slices.Compact(obligationIDs)
		var test *Test
		for _, record := range records {
			candidate, ok := record.(*Test)
			if ok && candidate.ID == testID {
				test = candidate
				break
			}
		}
		if test == nil {
			return nil, fmt.Errorf("provider-wire evidence request references missing test %s", testID)
		}
		requestName, ok := strings.CutPrefix(testID, "test:provider-wire:")
		if !ok {
			return nil, fmt.Errorf("provider-wire assertion references invalid test identity %s", testID)
		}
		commands, comparator, err := providerWireEvidenceCommands(requestName)
		if err != nil {
			return nil, err
		}
		witness, err := providerWireWitnessRequest(requestName, obligationIDs, records)
		if err != nil {
			return nil, err
		}
		result = append(result, &EvidenceRequest{
			Kind: KindEvidenceRequest, ID: "request:provider-wire:" + requestName, SnapshotID: test.SnapshotID,
			ObligationIDs: obligationIDs, AssertionIDs: assertionIDs, Commands: commands, Environment: []string{}, Witnesses: []EvidenceWitnessRequest{witness}, Durability: 1,
			Comparator: comparator, TimeoutSeconds: 120,
		})
	}
	return result, nil
}

func providerWireWitnessRequest(requestName string, obligationIDs []string, records []Record) (EvidenceWitnessRequest, error) {
	recordByID := make(map[string]Record, len(records))
	for _, record := range records {
		recordByID[record.RecordID()] = record
	}
	targetSet := make(map[string]struct{})
	pinSet := make(map[string]struct{})
	for _, obligationID := range obligationIDs {
		obligation, ok := recordByID[obligationID].(*Obligation)
		if !ok {
			return EvidenceWitnessRequest{}, fmt.Errorf("provider-wire witness references missing obligation %s", obligationID)
		}
		var mapping *Mapping
		for _, record := range records {
			candidate, candidateOK := record.(*Mapping)
			if candidateOK && candidate.BehaviorID == obligation.BehaviorID {
				mapping = candidate
				break
			}
		}
		if mapping == nil {
			return EvidenceWitnessRequest{}, fmt.Errorf("provider-wire witness has no mapping for %s", obligation.BehaviorID)
		}
		for _, targetID := range mapping.TargetIDs {
			if !providerWireRequestTargets(requestName, targetID) {
				continue
			}
			target, targetOK := recordByID[targetID].(*Target)
			if !targetOK {
				return EvidenceWitnessRequest{}, fmt.Errorf("provider-wire witness references missing target %s", targetID)
			}
			targetSet[targetID] = struct{}{}
			for _, pinID := range target.PinIDs {
				pinSet[pinID] = struct{}{}
			}
		}
	}
	witness := EvidenceWitnessRequest{
		WitnessType: "go-covered-range", TargetIDs: slices.Sorted(maps.Keys(targetSet)),
		SubjectPinIDs: slices.Sorted(maps.Keys(pinSet)), CommandIndexes: []int{1},
		ArtifactPath: filepath.ToSlash(filepath.Join("tmp", "closure", "evidence-work", "provider-wire-"+requestName+".coverprofile")),
	}
	if requestName == "differential" {
		witness.WitnessType = "provider-capture"
		witness.CommandIndexes = []int{1, 2}
		witness.ArtifactPath = ""
	}
	return witness, nil
}

func providerWireRequestTargets(requestName, targetID string) bool {
	switch requestName {
	case "sampling-completions":
		return targetID == "target:provider-wire:openai-completions-sampling"
	case "sampling-responses":
		return targetID == "target:provider-wire:openai-responses-sampling"
	default:
		return true
	}
}

func providerWireEvidenceCommands(requestName string) ([]EvidenceCommand, string, error) {
	testCommand := func(requestName, packagePath, testName string) []EvidenceCommand {
		coveragePath := filepath.ToSlash(filepath.Join("tmp", "closure", "evidence-work", "provider-wire-"+requestName+".coverprofile"))
		return []EvidenceCommand{{Name: "go", Args: []string{"test", packagePath, "-run", "^" + testName + "$", "-count=1", "-coverprofile=" + coveragePath}}}
	}
	switch requestName {
	case "differential":
		return []EvidenceCommand{
			{Name: "node", Args: []string{"parity/testdata/provider-wire-pi.mjs"}},
			{Name: "go", Args: []string{"run", "./parity/testdata/provider-wire-pig"}},
		}, "output-equal", nil
	case "finish-reason":
		return testCommand(requestName, "./ai", "TestMapOAIFinishReasonCompatibility"), "exit-zero", nil
	case "nullable-headers-differential":
		commands := testCommand(requestName, "./internal/codingagent", "TestMergeHeadersMatchesPinnedPiSource")
		commands[0].Args = slices.Insert(commands[0].Args, 1, "-tags=parity")
		return commands, "exit-zero", nil
	case "nullable-headers":
		return testCommand(requestName, "./internal/codingagent", "TestMergeHeadersSupportsCaseInsensitiveDeletionMarkers"), "exit-zero", nil
	case "sampling-completions":
		return testCommand(requestName, "./ai", "TestOpenAICompletionsModelSamplingDefaultsMergeBeforeRequestOverrides"), "exit-zero", nil
	case "sampling-responses":
		return testCommand(requestName, "./ai", "TestOpenAIResponsesSamplingParamsOverrideNamedFields"), "exit-zero", nil
	case "thinking-payload":
		return testCommand(requestName, "./ai", "TestOpenAICompletionsBasetenAndVLLMThinkingPayload"), "exit-zero", nil
	default:
		return nil, "", fmt.Errorf("provider-wire evidence request %s has no canonical command", requestName)
	}
}
