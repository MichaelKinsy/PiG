package coding

// pi: packages/coding-agent/src/core/nested-tool-calls.ts

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
)

// failingRunHost is a nestedTestHost whose RunToolCall fails for a tool named "boom" after it was asked to run.
type failingRunHost struct {
	nestedTestHost
	entered chan struct{}
	release chan struct{}
}

func (h *failingRunHost) RunToolCall(ctx context.Context, toolCall agent.AgentToolCall, callerID string, onUpdate agent.ToolUpdateSink) (agent.AgentToolCallOutcome, error) {
	if toolCall.Name != "boom" {
		return h.nestedTestHost.RunToolCall(ctx, toolCall, callerID, onUpdate)
	}
	close(h.entered)
	<-h.release
	return agent.AgentToolCallOutcome{}, errors.New("host failed")
}

// nested-tool-calls.ts:209-233: a call to a sequential tool queues behind the previous exclusive call and starts only after it
// ended, and the queue is released however that call ends (finally), so a call that fails does not strand the queued one.
func TestNestedToolCallRunnerQueuedCallStartsOnlyAfterTheHolderEndsEvenWhenTheHolderFails(t *testing.T) {
	for _, holderFails := range []bool{false, true} {
		name := "holder succeeds"
		if holderFails {
			name = "holder fails"
		}
		t.Run(name, func(t *testing.T) {
			entered := make(chan struct{})
			release := make(chan struct{})
			secondStarted := make(chan struct{})
			holderName := "holder"
			if holderFails {
				holderName = "boom"
			}
			tools := []agent.AgentTool{
				&nestedTestTool{name: holderName, mode: agent.ToolModeSequential, run: func(context.Context, string, agent.ToolUpdateCallback) agent.AgentToolResult {
					close(entered)
					<-release
					return textResult("holder")
				}},
				&nestedTestTool{name: "queued", mode: agent.ToolModeSequential, run: func(context.Context, string, agent.ToolUpdateCallback) agent.AgentToolResult {
					close(secondStarted)
					return textResult("queued")
				}},
			}
			host := &failingRunHost{tools: &tools, entered: entered, release: release}
			runner := NewNestedToolCallRunner(host)
			holderDone := make(chan error, 1)
			go func() {
				_, err := runner.Execute(t.Context(), "call-1", holderName, json.RawMessage(`{}`), NestedToolCallOptions{})
				holderDone <- err
			}()
			<-entered
			queuedDone := make(chan error, 1)
			go func() {
				_, err := runner.Execute(t.Context(), "call-2", "queued", json.RawMessage(`{}`), NestedToolCallOptions{})
				queuedDone <- err
			}()
			select {
			case <-secondStarted:
				t.Fatal("the queued sequential call started while the exclusive call was still running")
			case <-time.After(50 * time.Millisecond):
			}
			close(release)
			select {
			case err := <-holderDone:
				if holderFails != (err != nil) {
					t.Fatalf("holder error = %v, want failure %t", err, holderFails)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the exclusive call did not end")
			}
			select {
			case err := <-queuedDone:
				if err != nil {
					t.Fatalf("queued call failed: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the queued call never ran after the exclusive call ended: the queue was not released")
			}
		})
	}
}
