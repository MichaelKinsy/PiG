package agent

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi packages/agent/src/agent.ts:484-492 createLoopConfig builds one prepareNextTurn closure per run when either hook is set at that moment; the closure reads this.prepareNextTurnWithContext and this.prepareNextTurn on every call. So an assignment during the run replaces (or removes) the hook the run's later turns call, while a run that began with neither hook never calls one.

type prepareNextTurnRun struct {
	agent *Agent
	calls []string
}

// newPrepareNextTurnRun builds an agent whose first turn calls tool "swap"; swap runs the supplied mutation while the run is in flight. Every other request is text, so each run ends after one tool turn and the hook runs once.
func newPrepareNextTurnRun(t *testing.T, mutate func(*prepareNextTurnRun)) *prepareNextTurnRun {
	t.Helper()
	run := &prepareNextTurnRun{}
	provider := &scriptedProvider{respond: func(call int, _ scriptedRequest) *ai.AssistantMessageEventStream {
		if call == 1 || call == 3 {
			return doneStream(toolUseMessage(toolCall("t"+string(rune('0'+call)), "swap", nil)))
		}
		return doneStream(textMessage("done"))
	}}
	swap := &scriptTool{name: "swap", params: map[string]any{"type": "object"}, execute: func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
		mutate(run)
		return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "swapped"}}}, nil
	}}
	run.agent = mustNewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{swap}})
	return run
}

func (r *prepareNextTurnRun) hook(name string) func(context.Context) (*AgentLoopTurnUpdate, error) {
	return func(context.Context) (*AgentLoopTurnUpdate, error) {
		r.calls = append(r.calls, name)
		return nil, nil
	}
}

// agent.ts:488-490 the closure calls whichever hook is current when the turn ends: replacing prepareNextTurn mid-run redirects the run's next call.
func TestPrepareNextTurnReplacedDuringARunRedirectsTheRunsNextCall(t *testing.T) {
	run := newPrepareNextTurnRun(t, func(r *prepareNextTurnRun) { r.agent.SetPrepareNextTurn(r.hook("replacement")) })
	run.agent.SetPrepareNextTurn(run.hook("original"))
	mustSend(t, run.agent, "go")
	if want := []string{"replacement"}; !slices.Equal(run.calls, want) {
		t.Fatalf("hook calls = %v, want %v: the turn end calls the hook current at that moment", run.calls, want)
	}
}

// agent.ts:489-491 `await this.prepareNextTurn?.(...)`: clearing the hook mid-run makes the later turns call nothing.
func TestPrepareNextTurnClearedDuringARunIsNotCalledAgain(t *testing.T) {
	run := newPrepareNextTurnRun(t, func(r *prepareNextTurnRun) { r.agent.SetPrepareNextTurn(nil) })
	run.agent.SetPrepareNextTurn(run.hook("original"))
	mustSend(t, run.agent, "go")
	if len(run.calls) != 0 {
		t.Fatalf("hook calls = %v, want none: the hook was cleared before the first turn ended", run.calls)
	}
}

// agent.ts:484-486 the closure is only created when a hook is set at run start: a hook assigned during a run that began without one first runs on the next run.
func TestPrepareNextTurnAssignedDuringARunWithoutOneWaitsForTheNextRun(t *testing.T) {
	run := newPrepareNextTurnRun(t, func(r *prepareNextTurnRun) { r.agent.SetPrepareNextTurn(r.hook("late")) })
	mustSend(t, run.agent, "first")
	if len(run.calls) != 0 {
		t.Fatalf("first run called %v: it started with no hook", run.calls)
	}
	mustSend(t, run.agent, "second")
	if len(run.calls) == 0 {
		t.Fatal("the next run never called the assigned hook")
	}
}

// agent.ts:486-490 prepareNextTurnWithContext is checked first on every call: assigning it during a run that started with only prepareNextTurn moves the next call to it.
func TestPrepareNextTurnWithContextAssignedDuringARunTakesPrecedence(t *testing.T) {
	run := newPrepareNextTurnRun(t, func(r *prepareNextTurnRun) {
		r.agent.SetPrepareNextTurnWithContext(func(context.Context, PrepareNextTurnContext) (*AgentLoopTurnUpdate, error) {
			r.calls = append(r.calls, "with-context")
			return nil, nil
		})
	})
	run.agent.SetPrepareNextTurn(run.hook("legacy"))
	mustSend(t, run.agent, "go")
	if want := []string{"with-context"}; !slices.Equal(run.calls, want) {
		t.Fatalf("hook calls = %v, want %v", run.calls, want)
	}
}
