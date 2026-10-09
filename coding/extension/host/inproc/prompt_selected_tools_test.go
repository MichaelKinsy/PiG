package inproc

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi runner.ts:1332-1364 preserves the shared options after every handler, even a null list that a later handler repairs before AgentSession.prompt reads its length.
func TestBeforeAgentStartSelectionCanBeRepaired(t *testing.T) {
	base := extension.BuildSystemPromptOptions{SelectedTools: []string{"read"}}
	// Keep the pre-0.3 value-typed event construction source-compatible.
	_ = extension.BeforeAgentStartEvent{SystemPromptOptions: base}
	runner := NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {
		func(args ...any) (any, error) {
			extension.SetBeforeAgentStartSelectedTools(args[1].(context.Context), json.RawMessage(`null`))
			return nil, nil
		},
		func(args ...any) (any, error) {
			ctx := args[1].(context.Context)
			if string(extension.BeforeAgentStartSelectedTools(ctx)) != "null" {
				t.Fatal("later handler lost the null edit")
			}
			extension.BeforeAgentStartOptions(ctx).SelectedTools = []string{"write"}
			return nil, nil
		},
	}}}}, t.TempDir())
	result, err := runner.EmitBeforeAgentStart(t.Context(), "repair", nil, base)
	if err != nil || result == nil || result.SystemPromptOptions == nil || !slices.Equal(result.SystemPromptOptions.SelectedTools, []string{"write"}) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if !slices.Equal(base.SelectedTools, []string{"read"}) {
		t.Fatalf("mutated base: %v", base.SelectedTools)
	}
}
