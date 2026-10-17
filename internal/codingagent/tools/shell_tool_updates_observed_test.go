package tools

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// countShellUpdateClock counts the scheduler's reads of the time: a started scheduler reads it for every update it
// emits, including the final one finish sends, so zero reads means no scheduler ran.
func countShellUpdateClock(t *testing.T) *atomic.Int32 {
	t.Helper()
	var reads atomic.Int32
	saved := shellUpdateTime
	shellUpdateTime = shellUpdateClock{
		now:   func() time.Time { reads.Add(1); return saved.now() },
		after: saved.after,
	}
	t.Cleanup(func() { shellUpdateTime = saved })
	return &reads
}

func runBashCall(t *testing.T, onUpdate func(agent.AgentToolResult) error) agent.AgentToolCallOutcome {
	t.Helper()
	outcome, err := agent.RunToolCall(context.Background(), agent.AgentToolCall{ID: "c", Name: "bash", Arguments: ai.JsonObject{"command": "printf 'a\\nb\\n'"}},
		agent.RunToolCallOptions{Tools: []agent.AgentTool{&BashTool{CWD: t.TempDir()}}, OnUpdate: onUpdate})
	if err != nil {
		t.Fatal(err)
	}
	return outcome
}

// Print text mode has no observer of tool_execution_update, so bash runs no update scheduler and builds no snapshot; the
// result is the one an observed run returns.
func TestShellToolRunsNoUpdateSchedulerWhenUpdatesAreUnobserved(t *testing.T) {
	reads := countShellUpdateClock(t)
	unobserved := runBashCall(t, nil)
	if n := reads.Load(); n != 0 {
		t.Fatalf("an unobserved bash call ran an update scheduler (%d clock reads), want none", n)
	}
	var updates []string
	observed := runBashCall(t, func(partial agent.AgentToolResult) error { updates = append(updates, partial.Text()); return nil })
	if reads.Load() == 0 {
		t.Fatal("an observed bash call ran no update scheduler")
	}
	if len(updates) < 2 || updates[0] != "" || updates[len(updates)-1] != "a\nb\n" {
		t.Fatalf("observed updates = %q, want the initial empty update and the output", updates)
	}
	if unobserved.Result.Text() != observed.Result.Text() || string(unobserved.Result.StructuredContent) == "" || unobserved.IsError != observed.IsError {
		t.Fatalf("unobserved result %+v differs from observed %+v", unobserved.Result, observed.Result)
	}
}
