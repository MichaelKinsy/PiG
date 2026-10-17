package evalsuites

// Ports packages/evals/evals/models.docs.eval.ts.

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/internal/evals"
)

const (
	modelsProviderID = "openai"
	modelsModelID    = "fixture-chat"
	modelsModelName  = "Fixture Chat"
)

// Models asks the agent to add a model to a built-in provider without replacing its models.
func Models() evals.Suite {
	expected := evals.AddedModelOutput{Result: evals.AddedModelResult{AddedModel: &evals.AddedModel{
		Model: evals.ModelFields{ID: modelsModelID, Name: modelsModelName, Provider: modelsProviderID, Reasoning: true, Input: []string{"text"},
			ContextWindow: 32768, MaxTokens: 4096},
		ExistingModelsPreserved: true,
	}}}
	return evals.Suite{
		Name: "Add model to existing provider",
		File: "evals/models.docs.eval.go",
		Harness: documentationHarness(evals.PiCodingAgentHarnessOptions{Output: func(ctx context.Context, run evals.AgentRun) (any, error) {
			runtime, err := evals.LoadConfiguredModelRuntime(ctx, run.AgentDir)
			if err != nil {
				return nil, err
			}
			defer runtime.Close()
			return evals.InspectAddedModel(ctx, runtime, modelsProviderID, modelsModelID)
		}}),
		Judges:           []evals.Judge{mustJudge(expected)},
		NoJudgeThreshold: true,
		Cases: []evals.Case{singleCase("adds the model without replacing existing models", promptThenReload(fmt.Sprintf(
			"Configure this running PiG installation with a new `%s/%s` model. Do not create project-local configuration. Show it as “%s”. It accepts text, supports reasoning, has a 32,768-token context window and a 4,096-token maximum output, and has no usage cost.",
			modelsProviderID, modelsModelID, modelsModelName)))},
	}
}
