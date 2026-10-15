package evalsuites

// Ports packages/evals/evals/smoke.eval.ts.

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/evals"
)

// Smoke asks a basic question with no tools and checks the answer and the usage record.
func Smoke() evals.Suite {
	return evals.Suite{
		Name: "Answer a basic prompt",
		File: "evals/smoke.eval.go",
		Harness: func(ctx context.Context, input evals.PiCodingAgentInput) (*evals.HarnessRun, error) {
			return evals.RunPiCodingAgent(ctx, input, evals.PiCodingAgentHarnessOptions{NoTools: "all"})
		},
		Cases: []evals.Case{{Name: "returns the expected answer", Run: func(ctx context.Context, run evals.RunFunc) error {
			result, err := run(ctx, evals.PromptInput("What's the capital of France? Respond with only the city name."))
			if err != nil {
				return err
			}
			if output, _ := result.Output.(string); strings.TrimSpace(output) != "Paris" {
				return fmt.Errorf("expected output %q to be \"Paris\"", result.Output)
			}
			if len(result.Errors) != 0 {
				return fmt.Errorf("expected no errors, got %v", result.Errors)
			}
			if provider, model := os.Getenv("PI_PROVIDER"), os.Getenv("PI_MODEL"); result.Usage.Provider != provider || result.Usage.Model != model {
				return fmt.Errorf("usage %s/%s does not match %s/%s", result.Usage.Provider, result.Usage.Model, provider, model)
			}
			if result.Usage.TotalTokens <= 0 {
				return fmt.Errorf("expected totalTokens > 0, got %v", result.Usage.TotalTokens)
			}
			return nil
		}}},
	}
}
