package inproc

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// TestEmitToolCallAwaitsAnInProcessHandlerThroughTheAbort: Pi awaits every tool_call handler with no race against the run's abort (runner.ts:1256-1274 emitToolCall; agent-loop.ts:738).
// The in-process runner calls its handlers on the caller's goroutine, so an abort of the run while a handler runs does not end the emit: it returns the handler's result once the handler does.
func TestEmitToolCallAwaitsAnInProcessHandlerThroughTheAbort(t *testing.T) {
	budget := testbudget.Wait(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	handler := func(args ...any) (any, error) {
		close(entered)
		<-release
		return &extension.ToolCallEventResult{Block: true, Reason: "answered after the abort"}, nil
	}
	runner := NewRunner([]extension.Extension{{Path: "slow", Handlers: map[string][]extension.HandlerFn{"tool_call": {handler}}}}, t.TempDir())
	run, abort := context.WithCancel(t.Context())
	type outcome struct {
		result *extension.ToolCallEventResult
		err    error
	}
	emitted := make(chan outcome, 1)
	go func() {
		result, err := runner.EmitToolCall(run, extension.CustomToolCallEvent{Type: "tool_call", ToolCallID: "c1", ToolName: "bash"})
		emitted <- outcome{result, err}
	}()
	select {
	case <-entered:
	case <-time.After(budget):
		t.Fatal("the handler never started")
	}
	abort()
	select {
	case got := <-emitted:
		t.Fatalf("EmitToolCall returned %+v, %v while the handler was still running after the abort", got.result, got.err)
	default:
	}
	close(release)
	select {
	case got := <-emitted:
		if got.err != nil || got.result == nil || !got.result.Block || got.result.Reason != "answered after the abort" {
			t.Fatalf("EmitToolCall = %+v, %v, want the handler's block", got.result, got.err)
		}
	case <-time.After(budget):
		t.Fatal("EmitToolCall did not return after the handler did")
	}
}
