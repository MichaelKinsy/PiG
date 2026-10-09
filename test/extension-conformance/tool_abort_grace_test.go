package extensionconformance

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// pig divergence (D111): every SDK realization in its own process, packed, or fused settles an aborted tool call by one rule.
// A tool that honours its signal settles with its own result, as Pi's executePreparedToolCall awaits tool.execute and keeps
// its result (agent-loop.ts:831-853): abort_tool answers "aborted". A tool that ignores its signal fails with Pi's abort text
// "Operation aborted" (agent-loop.ts:623) once subprocess.ToolAbortGrace has passed, where Pi would await it indefinitely:
// hang_tool sleeps 8 s, longer than the grace period, and its late "late" never reaches the caller. Both answers differ from
// the host's former immediate "context canceled", so an SDK that never delivers the cancel, or a host that stops waiting at
// the cancel, fails the row.
func TestConformance_AbortedToolSettlesWithinTheGrace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	cases := append(sdkHarnessCases(), packedSDKHarnessCases()...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			result, _, err := abortConformanceTool(t, h, "abort_tool")
			if err != nil || result.Text() != "aborted" {
				t.Fatalf("abort_tool = %q, %v; want the tool's own result %q", result.Text(), err, "aborted")
			}
			result, elapsed, err := abortConformanceTool(t, h, "hang_tool")
			if err == nil || err.Error() != "Operation aborted" {
				t.Fatalf("hang_tool = %q, %v; want exactly %q", result.Text(), err, "Operation aborted")
			}
			if elapsed < subprocess.ToolAbortGrace {
				t.Fatalf("hang_tool failed %v after its abort, before the %v grace period", elapsed, subprocess.ToolAbortGrace)
			}
		})
	}
}

// abortConformanceTool runs a tool, cancels its context once the tool reports that it is waiting, and returns the outcome,
// the time from the cancel to the outcome, and its error.
func abortConformanceTool(t *testing.T, h *harness, name string) (agent.AgentToolResult, time.Duration, error) {
	t.Helper()
	tool, ok := findTool(h.runner, name)
	if !ok {
		t.Fatalf("%s not registered", name)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	waiting := make(chan struct{})
	var once sync.Once
	type outcome struct {
		result agent.AgentToolResult
		err    error
		at     time.Time
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := tool.Definition.Execute(ctx, "tc-"+name, json.RawMessage(`{}`), func(agent.AgentToolResult) { once.Do(func() { close(waiting) }) })
		done <- outcome{result, err, time.Now()}
	}()
	select {
	case <-waiting:
	case <-time.After(45 * time.Second):
		t.Fatalf("%s never reported that it was waiting", name)
	}
	cancelled := time.Now()
	cancel()
	select {
	case got := <-done:
		return got.result, got.at.Sub(cancelled), got.err
	case <-time.After(subprocess.ToolAbortGrace + 30*time.Second):
		t.Fatalf("%s did not settle after its abort", name)
	}
	return agent.AgentToolResult{}, 0, nil
}
