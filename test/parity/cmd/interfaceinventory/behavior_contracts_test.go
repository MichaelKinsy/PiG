package main

import (
	"strings"
	"testing"
)

func TestValidateMappingBehaviorContractsRequiresClosedHandleInputContract(t *testing.T) {
	entry := mappingEntry{
		ID:     "pkg:coding-agent/.#ModelSelector::property:handleInput",
		Layers: map[string]string{"behavior": "complete"},
	}
	problems := validateMappingBehaviorContracts(entry, map[string]string{})
	if !containsProblem(problems, "without a state-transition contract") {
		t.Fatalf("problems = %v", problems)
	}

	entry.BehaviorContracts = []string{"selector/wrap"}
	problems = validateMappingBehaviorContracts(entry, map[string]string{"selector/wrap": "pending"})
	if !containsProblem(problems, "contract \"selector/wrap\" is pending") {
		t.Fatalf("problems = %v", problems)
	}
	if problems := validateMappingBehaviorContracts(entry, map[string]string{"selector/wrap": "ported"}); len(problems) != 0 {
		t.Fatalf("ported contract problems = %v", problems)
	}
}

func containsProblem(problems []string, text string) bool {
	for _, problem := range problems {
		if strings.Contains(problem, text) {
			return true
		}
	}
	return false
}
