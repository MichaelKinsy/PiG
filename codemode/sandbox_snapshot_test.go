package codemode_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/codemode"
)

// Pi packages/codemode/src/runtime/host.ts:427 execute() hands the Execution `tools: new Map(this.toolsByName)`: an execution runs against the tool set registered when it started. Registering or unregistering a tool while a script runs changes later executions (registerTool 373-376, unregisterTool 378-380) and the Tools getter (382-384, a fresh array each call), never the running one.
func TestRunningExecutionKeepsTheToolSetItStartedWith(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	gate := codemode.Tool{Name: "gate", Execute: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return json.RawMessage(`true`), nil
	}}
	sandbox := newSandbox(t, 10_000, gate, echo)

	done := make(chan codemode.Result, 1)
	go func() {
		result, err := sandbox.Execute(t.Context(), `
			await tools.gate();
			return { names: Object.keys(tools).sort(), echoed: await tools.echo("still here") };`, codemode.ExecuteOptions{})
		if err != nil {
			t.Error(err)
		}
		done <- result
	}()
	receive(t, started)

	if !sandbox.UnregisterTool("echo") {
		t.Fatal("UnregisterTool(echo) = false while an execution is running")
	}
	if err := sandbox.RegisterTool(codemode.Tool{Name: "late", Execute: echo.Execute}); err != nil {
		t.Fatal(err)
	}
	close(release)

	wantOK(t, receive(t, done), `{"names":["echo","gate"],"echoed":"still here"}`)

	// The registry itself did change: a later execution sees the new set.
	wantOK(t, run(t, sandbox, `return Object.keys(tools).sort()`), `["gate","late"]`)
}

// host.ts:382-384 `get tools()` spreads the Map values into a new array: the caller cannot reorder or truncate the registry through it.
func TestSandboxToolsListIsACopy(t *testing.T) {
	sandbox := newSandbox(t, 10_000, echo)
	list := sandbox.Tools()
	list[0] = codemode.Tool{Name: "tampered"}
	names := func() []string {
		var out []string
		for _, tool := range sandbox.Tools() {
			out = append(out, tool.Name)
		}
		return out
	}
	if got := names(); !slices.Equal(got, []string{"echo"}) {
		t.Fatalf("Tools() after mutating a returned list = %v, want [echo]", got)
	}
	if len(sandbox.Globals()) != 0 {
		t.Fatal("Globals() of a sandbox without globals must be empty")
	}
}
