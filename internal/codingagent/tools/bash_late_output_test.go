package tools

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
)

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/5208-late-bash-output.test.ts:14. A saved callback gives the same post-resolution ordering without a wall-clock timer.
func TestShellToolIgnoresOutputAfterOperationsResolve(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var late func([]byte)
		var updates []string
		cfg := shellToolConfig{
			name: "bash", shellName: "bash", tempFilePrefix: "pi-bash",
			operations: lateOutputOperations{save: func(onData func([]byte)) { late = onData }},
		}
		result, err := executeShellTool(t.Context(), t.TempDir(), cfg, json.RawMessage(`{"command":"late-output"}`), func(partial agent.AgentToolResult) {
			updates = append(updates, partial.Text())
		})
		if err != nil || result.IsError {
			t.Fatalf("execute = %+v, %v", result, err)
		}
		before := slices.Clone(updates)
		late([]byte("late\n"))
		synctest.Wait()
		if strings.TrimSpace(result.Text()) != "before" {
			t.Fatalf("output = %q, want before", result.Text())
		}
		if !slices.Equal(updates, before) {
			t.Fatalf("late updates = %q, want unchanged %q", updates, before)
		}
	})
}

type lateOutputOperations struct {
	save func(func([]byte))
}

func (o lateOutputOperations) Exec(_ context.Context, _, _ string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
	opts.OnData([]byte("before\n"))
	o.save(opts.OnData)
	return BashOperationsResult{ExitCode: new(0)}, nil
}

// bash.ts:313-320 stops accepting output before the final snapshot and file close. Assert retained accumulator state too: a copied result string alone cannot detect an accepted late chunk.
func TestFinishedShellOutputDoesNotRetainLateChunks(t *testing.T) {
	output := NewOutputAccumulator("pi-bash")
	output.Append([]byte("before\n"))
	output.Finish()
	before := output.Snapshot(false)
	output.Append([]byte("late\n"))
	if after := output.Snapshot(false); after != before {
		t.Fatalf("late chunk changed retained output: %+v, want %+v", after, before)
	}
}
