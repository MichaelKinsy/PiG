// Package evalsuites holds the eval suites of Pi's packages/evals/evals directory, written against the PiG Session
// harness. Each suite names the upstream file it ports; prompts say PiG where Pi's say Pi, and the documentation the
// agent reads is PiG's.
package evalsuites

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/internal/evals"
)

// All returns every suite in file order.
func All() []evals.Suite {
	return []evals.Suite{
		DocumentationAudit(), CustomProvider(), Extensions(), Models(), OpenAIProvider(), Smoke(), TUI(),
	}
}

// documentationHarness is createPiDocumentationEvalHarness bound to output; it runs inside the container sandbox.
func documentationHarness(options evals.PiCodingAgentHarnessOptions) func(context.Context, evals.PiCodingAgentInput) (*evals.HarnessRun, error) {
	return func(ctx context.Context, input evals.PiCodingAgentInput) (*evals.HarnessRun, error) {
		harness, err := evals.CreatePiDocumentationEvalHarness(options)
		if err != nil {
			return nil, err
		}
		return evals.RunPiCodingAgent(ctx, input, harness)
	}
}

// promptThenReload is the input of the configuration evals: one prompt, then a reload of the Session.
func promptThenReload(prompt string) evals.PiCodingAgentInput {
	return evals.PiCodingAgentInput{
		{Type: evals.PiCodingAgentStepPrompt, Content: prompt},
		{Type: evals.PiCodingAgentStepReload},
	}
}

// singleCase is a case that runs input once and lets the judges score it.
func singleCase(name string, input evals.PiCodingAgentInput) evals.Case {
	return evals.Case{Name: name, Run: func(ctx context.Context, run evals.RunFunc) error {
		_, err := run(ctx, input)
		return err
	}}
}

func mustJudge(expected any) evals.Judge {
	judge, err := evals.StructuredOutputJudge(expected)
	if err != nil {
		panic(err)
	}
	return judge
}

// repositoryRoot finds the module root from the working directory, or PI_EVAL_REPOSITORY_ROOT.
func repositoryRoot() (string, error) {
	if configured := os.Getenv("PI_EVAL_REPOSITORY_ROOT"); configured != "" {
		return configured, nil
	}
	directory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", errors.New("no go.mod above the working directory; set PI_EVAL_REPOSITORY_ROOT")
		}
		directory = parent
	}
}
