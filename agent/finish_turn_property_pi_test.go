package agent

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi agent.ts:209 and 482: finishTurn is a public mutable property of Agent that every run reads when it builds the loop config. Assigning it after
// construction applies to the next run, replacing it swaps which hook runs, and clearing it restores the loop without one (agent-loop.ts:252,286:
// `config.finishTurn?.(...)`, whose "end" action stops the run before the follow-up queue is polled; agent-loop.test.ts:1228-1260). FinishTurnHook
// reads the property back (the getter half of Pi's property).
func TestFinishTurnPropertyChoosesTheHookEachRunUses(t *testing.T) {
	prov := providerFromSeqs(
		textSeq("run 1 turn 1"), textSeq("run 1 turn 2"), // no hook: the queued follow-up gets its turn
		textSeq("run 2"),                                 // hook A ends the run, the follow-up stays queued
		textSeq("run 3"),                                 // hook B ends the run
		textSeq("run 4 turn 1"), textSeq("run 4 turn 2"), // cleared: the follow-up gets its turn again
	)
	a := mustNewAgent(AgentOptions{Model: fakeTestModel(prov), MaxTurns: 5})
	followUp := func() {
		a.FollowUp(AgentMessage{User: &UserMessage{Role: RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "queued"}}, Timestamp: time.Now().UnixMilli()}})
	}
	var calledA, calledB int
	endRun := func(counter *int) FinishTurn {
		return func(context.Context, AgentTurnContext) (*AgentTurnDecision, error) {
			*counter++
			return &AgentTurnDecision{Action: AgentTurnEnd}, nil
		}
	}
	providerCalls := func() int { return int(prov.idx.Load()) }

	if a.FinishTurnHook() != nil {
		t.Fatal("a new agent has a finishTurn hook")
	}
	followUp()
	if _, err := a.Send(context.Background(), "run 1"); err != nil {
		t.Fatal(err)
	}
	if providerCalls() != 2 || a.HasQueuedMessages() {
		t.Fatalf("without a hook: provider calls = %d, queued = %v, want 2 and false", providerCalls(), a.HasQueuedMessages())
	}

	a.SetFinishTurn(endRun(&calledA))
	hook := a.FinishTurnHook()
	if hook == nil {
		t.Fatal("FinishTurnHook is nil after SetFinishTurn")
	}
	followUp()
	if _, err := a.Send(context.Background(), "run 2"); err != nil {
		t.Fatal(err)
	}
	if calledA != 1 || providerCalls() != 3 || !a.HasQueuedMessages() {
		t.Fatalf("hook A: calls = %d, provider calls = %d, queued = %v, want 1, 3 and true", calledA, providerCalls(), a.HasQueuedMessages())
	}
	if _, err := hook(context.Background(), AgentTurnContext{}); err != nil || calledA != 2 {
		t.Fatalf("the hook read back from FinishTurnHook is not the one that was set (calls %d, err %v)", calledA, err)
	}
	calledA = 1

	a.SetFinishTurn(endRun(&calledB))
	if _, err := a.Send(context.Background(), "run 3"); err != nil {
		t.Fatal(err)
	}
	if calledA != 1 || calledB != 1 || providerCalls() != 4 {
		t.Fatalf("hook B: A calls = %d, B calls = %d, provider calls = %d, want 1, 1 and 4", calledA, calledB, providerCalls())
	}

	a.SetFinishTurn(nil)
	if a.FinishTurnHook() != nil {
		t.Fatal("FinishTurnHook is not nil after SetFinishTurn(nil)")
	}
	if _, err := a.Send(context.Background(), "run 4"); err != nil {
		t.Fatal(err)
	}
	if calledA != 1 || calledB != 1 || providerCalls() != 6 || a.HasQueuedMessages() {
		t.Fatalf("cleared: A = %d, B = %d, provider calls = %d, queued = %v, want 1, 1, 6 and false", calledA, calledB, providerCalls(), a.HasQueuedMessages())
	}
}

// Pi agent.ts:243 (`this.finishTurn = runtimeOptions.finishTurn`) and :482: a hook given at construction is the one the first run reads, so its "end"
// action stops the run before the queued follow-up is polled, and FinishTurnHook returns that same hook.
func TestFinishTurnConstructorOptionIsTheHookTheFirstRunUses(t *testing.T) {
	prov := providerFromSeqs(textSeq("only turn"), textSeq("never reached"))
	var called int
	a := mustNewAgent(AgentOptions{Model: fakeTestModel(prov), MaxTurns: 5, FinishTurn: func(context.Context, AgentTurnContext) (*AgentTurnDecision, error) {
		called++
		return &AgentTurnDecision{Action: AgentTurnEnd}, nil
	}})
	if a.FinishTurnHook() == nil {
		t.Fatal("FinishTurnHook is nil although the constructor was given one")
	}
	a.FollowUp(AgentMessage{User: &UserMessage{Role: RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "queued"}}, Timestamp: time.Now().UnixMilli()}})
	if _, err := a.Send(context.Background(), "run 1"); err != nil {
		t.Fatal(err)
	}
	if called != 1 || int(prov.idx.Load()) != 1 || !a.HasQueuedMessages() {
		t.Fatalf("constructor hook: calls = %d, provider calls = %d, queued = %v, want 1, 1 and true", called, prov.idx.Load(), a.HasQueuedMessages())
	}
}
