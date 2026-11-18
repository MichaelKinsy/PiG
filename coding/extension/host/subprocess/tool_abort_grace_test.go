package subprocess

import (
	"context"
	"encoding/json"
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
