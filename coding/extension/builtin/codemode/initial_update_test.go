package codemode

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// Pi 014873d7 execute.ts executeCodemode: "Show the script as running before its first nested call, so the row's timer starts." A
// script that calls no tool still sends one update, with no calls and no content, before it ends.
func TestExecuteSendsAnInitialUpdateBeforeTheFirstNestedCall(t *testing.T) {
	var updates []agent.AgentToolResult
	result, err := Execute(context.Background(), "call-1", json.RawMessage(`{"code":"return 1"}`), func(partial agent.AgentToolResult) { updates = append(updates, partial) }, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("script failed: %+v", result)
	}
	if len(updates) != 1 {
		t.Fatalf("updates = %d, want exactly the initial one", len(updates))
	}
	details, ok := updates[0].Details.(ToolDetails)
	if !ok || len(details.Calls) != 0 || len(updates[0].Content) != 0 {
		t.Fatalf("initial update = %+v, want no content and no calls", updates[0])
	}
}
