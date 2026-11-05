package tools

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// .upstream/main 66c655677 packages/coding-agent/test/bash-executor-truncation.test.ts
func TestExecuteBashWithOperationsReportsDiscardedChunks10164(t *testing.T) {
	first := padEnd("error: missing build dependency\n", 64*1024, 'x')
	last := padEnd("remaining build output\n", 44*1024, 'y')
	for _, cancelled := range []bool{false, true} {
		name := "completed"
		if cancelled {
			name = "cancelled"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result, err := ExecuteBashWithOperations(ctx, "cat build.log", t.TempDir(), portBashOperations(func(_ context.Context, _, _ string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
				opts.OnData([]byte(first))
				opts.OnData([]byte(last))
				if cancelled {
					cancel()
					return BashOperationsResult{}, errors.New("aborted")
				}
				code := 0
				return BashOperationsResult{ExitCode: &code}, nil
			}), BashExecOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if result.FullOutputPath == "" {
				t.Fatal("no full output path")
			}
			defer os.Remove(result.FullOutputPath)
			full, err := os.ReadFile(result.FullOutputPath)
			if err != nil || string(full) != first+last {
				t.Fatalf("full output file wrong: %v (len %d)", err, len(full))
			}
			if result.Cancelled != cancelled || result.Output != last || !result.Truncated {
				t.Fatalf("cancelled=%v outputIsLast=%v truncated=%v", result.Cancelled, result.Output == last, result.Truncated)
			}
		})
	}
	t.Run("does not mark short output as truncated", func(t *testing.T) {
		result, err := ExecuteBashWithOperations(t.Context(), "echo done", t.TempDir(), portBashOperations(func(_ context.Context, _, _ string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
			opts.OnData([]byte("done\n"))
			code := 0
			return BashOperationsResult{ExitCode: &code}, nil
		}), BashExecOptions{})
		if err != nil || result.Output != "done\n" || result.Truncated || result.FullOutputPath != "" {
			t.Fatalf("%+v, %v", result, err)
		}
	})
}

func padEnd(s string, n int, c byte) string {
	return s + strings.Repeat(string(c), n-len(s))
}
