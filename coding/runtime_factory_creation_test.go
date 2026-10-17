package coding

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi: packages/coding-agent/src/core/agent-session-runtime.ts:418-438 createAgentSessionRuntime. `await createRuntime(options)` hands the caller's options to the factory unchanged
// (cwd, agentDir, sessionManager, sessionStartEvent) and a factory that rejects rejects the creation: no Runtime is returned and the factory's own error reaches the caller.
// mutation-checked: the mutant "CreateAgentSessionRuntime returns no error when the factory fails" (runtime_replacement.go:111) fails it.
func TestCreateAgentSessionRuntimeHandsTheOptionsToTheFactoryAndSurfacesItsError(t *testing.T) {
	manager, err := NewInMemorySessionManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	agentDir := t.TempDir()
	event := &extension.SessionStartEvent{Type: "session_start", Reason: "resume", PreviousSessionFile: "/previous.jsonl"}
	failure := errors.New("factory failed")
	var seen []CreateAgentSessionRuntimeOptions
	runtime, err := CreateAgentSessionRuntime(t.Context(), func(_ context.Context, options CreateAgentSessionRuntimeOptions) (CreateAgentSessionRuntimeResult, error) {
		seen = append(seen, options)
		return CreateAgentSessionRuntimeResult{}, failure
	}, CreateAgentSessionRuntimeOptions{CWD: manager.GetCwd(), AgentDir: agentDir, SessionManager: manager, SessionStartEvent: event})
	if !errors.Is(err, failure) || runtime != nil {
		t.Fatalf("CreateAgentSessionRuntime = %v, %v; want no Runtime and the factory's error", runtime, err)
	}
	if len(seen) != 1 {
		t.Fatalf("factory ran %d times, want once", len(seen))
	}
	if got := seen[0]; got.CWD != manager.GetCwd() || got.AgentDir != agentDir || got.SessionManager != manager || got.SessionStartEvent != event {
		t.Fatalf("factory received %+v, want the caller's cwd, agentDir, session manager and start event", got)
	}
}
