package compaction

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

type discardingBashOperations struct{}

func (o discardingBashOperations) Exec(ctx context.Context, _, _ string, opts tools.BashOperationsExecOptions) (tools.BashOperationsResult, error) {
	opts.OnData([]byte(strings.Repeat("x", 64*1024)))
	opts.OnData([]byte(strings.Repeat("y", 44*1024)))
	code := 0
	return tools.BashOperationsResult{ExitCode: &code}, nil
}

// Pi 66c655677 test/bash-executor-truncation.test.ts: the model-facing text of a user bash result whose head the window dropped names the complete log.
func TestBashExecutionToTextNamesTheLogOfDiscardedOutput(t *testing.T) {
	result, err := tools.ExecuteBashWithOperations(t.Context(), "cat build.log", t.TempDir(), discardingBashOperations{}, tools.BashExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(result.FullOutputPath) }()
	text := bashExecutionToText(map[string]any{"command": "cat build.log", "output": result.Output, "exitCode": float64(0), "truncated": result.Truncated, "fullOutputPath": result.FullOutputPath})
	if want := "[Output truncated. Full output: " + result.FullOutputPath + "]"; result.FullOutputPath == "" || !strings.Contains(text, want) {
		t.Fatalf("model-facing text lacks %q: %.200q", want, text[max(0, len(text)-200):])
	}
}
