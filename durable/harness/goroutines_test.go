package harness

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// goroutineGrace bounds how long a goroutine may take to return after its Harness closed.
const goroutineGrace = 10 * time.Second

// Every goroutine a Harness starts ends when Close returns: the run loop, generation and tool invocations, hooks, the
// task scheduler, event watchers and the progress writers. TestMain's leak check only sees goroutines that nothing can
// reach; this gate also sees one that waits on a channel, timer or context that is still reachable.

func TestHarnessLeavesNoGoroutineBehindAfterAChatTurnWithAToolCall(t *testing.T) {
	baseline := testenv.BaselineGoroutines()
	setup := chatSetup(t)
	store := storage.NewMemoryStorage()
	addTool(t, setup.Registry, DefineTool(durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{Name: "echo", Description: "echo", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
		Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}, nil
		},
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("echo", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"})}, StopReason: "toolUse"}),
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}),
	})
	harness, root := openChat(t, store, setup)
	if _, err := root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("go")}); err != nil {
		t.Fatal(err)
	}
	waitForIdle(t, root)
	mustClose(t, harness)
	baseline.RequireEnded(t, goroutineGrace)
}

func TestHarnessLeavesNoGoroutineBehindWhenItClosesAroundARunningTask(t *testing.T) {
	baseline := testenv.BaselineGoroutines()
	store := storage.NewMemoryStorage()
	reached, gate := deferred(), deferred()
	stubborn := oneStep("test.goroutine-gate-stubborn", func(context.Context) error {
		reached.resolve()
		_ = gate.wait(context.Background())
		return nil
	})
	harness, _, _ := openTasks(t, store, []durable.AnyTask{stubborn.Erased()})
	start(t, mustRoot(t, harness, nil), stubborn)
	harness.Resume()
	_ = reached.wait(testContext)
	closing := asyncErr(func() error { return harness.Close(testContext) })
	gate.resolve()
	if _, err := closing.wait(); err != nil {
		t.Fatal(err)
	}
	baseline.RequireEnded(t, goroutineGrace)
}
