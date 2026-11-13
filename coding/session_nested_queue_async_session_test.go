package coding

// pi: packages/coding-agent/src/core/agent-session.ts

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// agent-session.ts:791-824 (_executeNestedToolCall): every ctx.executeTool() of a Session goes through its one NestedToolCallRunner
// (:797 `??=`) and is awaited (:824), so the calls of one Session share the runner's queue: a call to a sequential tool starts only
// after the exclusive call before it has ended (nested-tool-calls.ts:209-213), although it announced its start before it queued (:194).
func TestSessionNestedCallToASequentialToolWaitsForTheExclusiveCallBeforeIt(t *testing.T) {
	holderEntered, releaseHolder, queuedAnnounced := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var holderEnded, startedEarly atomic.Bool
	sequential := func(name string, execute func() string) extension.RegisteredTool {
		return orchestrationTool(name, name, extension.ToolDefinition{Exposure: extension.ToolExposureCodemode, ExecutionMode: "sequential",
			Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
				return textResult(execute()), nil
			}})
	}
	holder := sequential("holder", func() string {
		close(holderEntered)
		<-releaseHolder
		holderEnded.Store(true)
		return "held"
	})
	queued := sequential("queued", func() string {
		if !holderEnded.Load() {
			startedEarly.Store(true)
		}
		return "queued"
	})
	type nestedOutcome struct {
		text string
		err  error
	}
	fanout := orchestrationTool("fanout", "Calls two sequential tools at once.", extension.ToolDefinition{Exposure: extension.ToolExposureModelOnly,
		Execute: func(ctx context.Context, _ string, _ json.RawMessage, _ extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			tc := extension.ToolContextFromContext(ctx)
			call := func(name string) chan nestedOutcome {
				done := make(chan nestedOutcome, 1)
				go func() {
					outcome, err := tc.ExecuteTool(name, map[string]any{}, nil)
					done <- nestedOutcome{textOutcome(outcome), err}
				}()
				return done
			}
			first := call("holder")
			<-holderEntered
			second := call("queued")
			<-queuedAnnounced
			var early *nestedOutcome
			select {
			case outcome := <-second:
				early = &outcome
			case <-time.After(50 * time.Millisecond):
			}
			close(releaseHolder)
			a := <-first
			b := nestedOutcome{}
			if early != nil {
				b = *early
			} else {
				b = <-second
			}
			if a.err != nil {
				return extension.AgentToolResult{}, a.err
			}
			if b.err != nil {
				return extension.AgentToolResult{}, b.err
			}
			if early != nil {
				return textResult("queued call ended while the holder ran"), nil
			}
			return textResult(a.text + " | " + b.text), nil
		}})
	session, faux := newOrchestrationSession(t, extension.Extension{
		Tools:     map[string]extension.RegisteredTool{"holder": holder, "queued": queued, "fanout": fanout},
		ToolOrder: []string{"holder", "queued", "fanout"},
	})
	var announced atomic.Bool
	session.Subscribe(func(event agent.AgentEvent) {
		if start, ok := event.(agent.ToolExecutionStartEvent); ok && start.ToolName == "queued" && announced.CompareAndSwap(false, true) {
			close(queuedAnnounced)
		}
	})
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("fanout", map[string]any{}, &ai.FauxToolCallOptions{ID: ""})}, StopReason: "toolUse"}),
		fauxText("done"),
	})
	if err := session.Prompt(t.Context(), "go"); err != nil {
		t.Fatal(err)
	}
	result := toolResultOf(t, session)
	if len(result.Content) != 1 || result.Content[0] != (ai.TextContent{Text: "held | queued"}) {
		t.Fatalf("content = %+v, want the holder's result then the queued call's", result.Content)
	}
	if startedEarly.Load() {
		t.Fatal("the queued sequential tool ran while the exclusive call before it was still running")
	}
}
