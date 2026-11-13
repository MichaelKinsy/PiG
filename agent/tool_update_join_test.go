package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
)

// agent-loop.ts executePreparedToolCall awaits Promise.all(updateEvents) after tool.execute settles: an update emission still in flight when the tool
// returns is joined before the call completes, and its rejection is the call's rejection (the result is dropped).
func TestExecutePreparedToolCallJoinsAnUpdateStillBeingDeliveredWhenTheToolReturns(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sinkErr error
	}{{"delivered", nil}, {"rejected", errors.New("sink failed")}} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				delivering := make(chan struct{})
				release := make(chan struct{})
				toolReturned := make(chan struct{})
				tool := &scriptTool{name: "t", mode: ToolModeParallel, params: map[string]any{"type": "object"},
					execute: func(_ context.Context, _ string, _ json.RawMessage, onUpdate ToolUpdateCallback) (AgentToolResult, error) {
						go onUpdate(AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "partial"}}})
						<-delivering
						close(toolReturned)
						return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "final"}}}, nil
					}}
				var got finalizedToolCall
				var gotErr error
				done := make(chan struct{})
				go func() {
					defer close(done)
					got, gotErr = executePreparedToolCall(t.Context(), preparedToolCall{call: pendingToolCall{id: "c1", name: "t"}, tool: tool, args: json.RawMessage(`{}`)},
						func(AgentToolResult) error {
							close(delivering)
							<-release
							return tc.sinkErr
						}, nil)
				}()
				<-toolReturned
				synctest.Wait()
				select {
				case <-done:
					t.Fatal("the call completed while an update was still being delivered")
				default:
				}
				close(release)
				<-done
				if !errors.Is(gotErr, tc.sinkErr) {
					t.Fatalf("error = %v, want %v", gotErr, tc.sinkErr)
				}
				if tc.sinkErr == nil && got.result.Content == nil {
					t.Fatalf("result dropped without a rejection: %+v", got)
				}
			})
		})
	}
}
