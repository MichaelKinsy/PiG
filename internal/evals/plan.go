// Package evals ports Pi's documentation eval tooling: the task plan, the paired comparison report, the harness's
// prompt and environment helpers, and the fixtures the evals use.
//
// The container runner (src/cli.ts and src/docker.ts) and the vitest-evals harness that drives an agent Session
// (createPiCodingAgentHarness) run Vitest eval files inside Docker images; they have no Go host, so they are not
// ported.
// stubgen:omit BuiltImages
// stubgen:omit BuildImages
// stubgen:omit RequireEvalAuthFile
// stubgen:omit CreateDockerContext
// stubgen:omit DiscoverCases
// stubgen:omit RunTask
// stubgen:omit CreatePiCodingAgentHarness
// stubgen:omit PiCodingAgentHarnessWithOutput
// stubgen:omit PiCodingAgentInput
package evals

// Ports packages/evals/src/plan.ts.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// DocumentationVariant selects whether the Pi documentation section stays in the system prompt.
type DocumentationVariant string

const (
	DocumentationVariantWithoutDocs DocumentationVariant = "without_docs"
	DocumentationVariantWithDocs    DocumentationVariant = "with_docs"
)

// DocumentationVariants lists the variants in plan order.
var DocumentationVariants = [...]DocumentationVariant{DocumentationVariantWithoutDocs, DocumentationVariantWithDocs}

// DiscoveredEvalCase is one Vitest eval case named "<eval set> > <case>".
type DiscoveredEvalCase struct {
	File     string `json:"file"`
	FullName string `json:"fullName"`
	EvalSet  string `json:"evalSet"`
	CaseID   string `json:"caseId"`
}

// EvalTask is one isolated run of a case under one variant, model and repetition.
type EvalTask struct {
	DiscoveredEvalCase
	Variant   DocumentationVariant `json:"variant"`
	Model     string               `json:"model"`
	RunNumber int                  `json:"runNumber"`
}

// ParseDiscoveredCases validates the decoded JSON list of discovered cases and derives each case identity.
func ParseDiscoveredCases(value any) ([]DiscoveredEvalCase, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, errors.New("Discovered eval cases must be an array.")
	}
	type identity struct{ evalSet, caseID string }
	identities := map[identity]bool{}
	cases := make([]DiscoveredEvalCase, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("Discovered eval case is invalid.")
		}
		name, nameOK := record["name"].(string)
		file, fileOK := record["file"].(string)
		if !nameOK || !fileOK {
			return nil, errors.New("Discovered eval case is invalid.")
		}
		parts := strings.Split(name, " > ")
		if len(parts) < 2 || jsstring.Trim(parts[0]) == "" || jsstring.Trim(parts[1]) == "" || len(parts) > 2 {
			return nil, fmt.Errorf(`Documentation eval must use "<eval set> > <case>": %s`, name)
		}
		key := identity{parts[0], parts[1]}
		if identities[key] {
			return nil, fmt.Errorf("Duplicate eval case identity: %s", name)
		}
		identities[key] = true
		cases = append(cases, DiscoveredEvalCase{File: file, FullName: name, EvalSet: parts[0], CaseID: parts[1]})
	}
	return cases, nil
}

// CreateTaskPlan creates one task per case, repetition and variant. Odd repetitions run without_docs first and even
// repetitions with_docs first.
func CreateTaskPlan(cases []DiscoveredEvalCase, model string, runsPerVariant int) ([]EvalTask, error) {
	if !strings.Contains(model, "/") || strings.HasPrefix(model, "/") || strings.HasSuffix(model, "/") {
		return nil, errors.New("Model identity must contain a provider and model.")
	}
	if runsPerVariant < 1 {
		return nil, errors.New("Runs per variant must be a positive integer.")
	}
	tasks := []EvalTask{}
	for _, evalCase := range cases {
		for runNumber := 1; runNumber <= runsPerVariant; runNumber++ {
			variants := []DocumentationVariant{DocumentationVariantWithoutDocs, DocumentationVariantWithDocs}
			if runNumber%2 == 0 {
				variants = []DocumentationVariant{DocumentationVariantWithDocs, DocumentationVariantWithoutDocs}
			}
			for _, variant := range variants {
				tasks = append(tasks, EvalTask{DiscoveredEvalCase: evalCase, Variant: variant, Model: model, RunNumber: runNumber})
			}
		}
	}
	return tasks, nil
}
