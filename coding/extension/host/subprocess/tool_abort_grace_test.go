package subprocess

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

const toolAbortGraceExtension = `export default function (pi) {
  const params = { type: "object", properties: {} };
  const waiting = (onUpdate) => onUpdate({ content: [{ type: "text", text: "waiting" }] });
  pi.registerTool({ name: "coop_reject", label: "coop_reject", description: "rejects on abort", parameters: params,
    execute: (_id, _p, signal, onUpdate) => new Promise((_res, rej) => { signal.addEventListener("abort", () => rej(new Error("coop stopped by signal"))); waiting(onUpdate); }) });
  pi.registerTool({ name: "coop_resolve", label: "coop_resolve", description: "resolves on abort", parameters: params,
    execute: (_id, _p, signal, onUpdate) => new Promise((res) => { signal.addEventListener("abort", () => res({ content: [{ type: "text", text: "coop partial result" }] })); waiting(onUpdate); }) });
  pi.registerTool({ name: "hang_tool", label: "hang_tool", description: "ignores abort", parameters: params,
    execute: (_id, _p, _signal, onUpdate) => new Promise(() => { waiting(onUpdate); }) });
}
`

// abortToolAfterItStarts runs a tool, cancels its context once the tool reports that it is waiting, and returns its outcome,
// the time from the cancel to the outcome, and its error.
func abortToolAfterItStarts(t *testing.T, runner *inproc.Runner, name string) (extension.AgentToolResult, time.Duration, error) {
	t.Helper()
	tool, ok := runner.GetToolDefinition(name)
	if !ok {
		t.Fatalf("tool %s not registered", name)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	waiting := make(chan struct{})
	var once sync.Once
	type outcome struct {
		result extension.AgentToolResult
		err    error
		at     time.Time
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := tool.Execute(ctx, "tc-"+name, json.RawMessage(`{}`), func(extension.AgentToolResult) { once.Do(func() { close(waiting) }) })
		done <- outcome{result, err, time.Now()}
	}()
	select {
	case <-waiting:
	case <-time.After(30 * time.Second):
		t.Fatalf("%s never reported that it was waiting", name)
	}
	cancelled := time.Now()
	cancel()
	select {
	case got := <-done:
		return got.result, got.at.Sub(cancelled), got.err
	case <-time.After(ToolAbortGrace + 30*time.Second):
		t.Fatalf("%s did not settle after its abort", name)
	}
	return extension.AgentToolResult{}, 0, nil
}

// pig divergence (D111): an aborted extension tool call waits ToolAbortGrace for the tool to settle. A tool that honours its
// AbortSignal settles as in Pi, whose executePreparedToolCall awaits tool.execute and makes its result or thrown message the
// tool result (agent-loop.ts:831-853). A tool that ignores the signal, which Pi awaits indefinitely, fails with Pi's abort text
// "Operation aborted" (agent-loop.ts:623) once the grace period ends.
func TestAbortedToolCallSettlesCooperativelyOrFailsAfterTheGrace(t *testing.T) {
	for _, isolation := range []string{"strict", "shared-ok"} {
		t.Run(isolation, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "abort-grace.mjs")
			if err := os.WriteFile(source, []byte(toolAbortGraceExtension), 0o644); err != nil {
				t.Fatal(err)
			}
			h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			t.Cleanup(func() { h.Shutdown("test complete") })
			loaded, failures := h.LoadAll(t.Context(), []ExtConfig{{Name: "abort-grace", Source: source, Enabled: true, Isolation: isolation}})
			if len(failures) != 0 || len(loaded) != 1 {
				t.Fatalf("load: %v %v", loaded, failures)
			}
			runner := inproc.NewRunner(loaded, t.TempDir())

			if _, _, err := abortToolAfterItStarts(t, runner, "coop_reject"); err == nil || err.Error() != "coop stopped by signal" {
				t.Fatalf("coop_reject error = %v, want the tool's own rejection %q", err, "coop stopped by signal")
			}
			result, _, err := abortToolAfterItStarts(t, runner, "coop_resolve")
			if err != nil || result.Text() != "coop partial result" {
				t.Fatalf("coop_resolve = %q, %v; want the tool's own result %q", result.Text(), err, "coop partial result")
			}
			_, elapsed, err := abortToolAfterItStarts(t, runner, "hang_tool")
			if err == nil || err.Error() != "Operation aborted" {
				t.Fatalf("hang_tool error = %v, want exactly %q", err, "Operation aborted")
			}
			if elapsed < ToolAbortGrace {
				t.Fatalf("hang_tool failed %v after its abort, before the %v grace period", elapsed, ToolAbortGrace)
			}
		})
	}
}
