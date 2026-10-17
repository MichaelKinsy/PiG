package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi agent.ts:238 and :473 hand the Agent's public onPayload field to every request's stream options, read at run time, so assigning it after
// construction changes the next request (agent.ts:198). SetBeforeProviderHook is that assignment: the hook set later runs for the next request, replaces the
// one from AgentOptions, and a nil hook removes it. Mutation-checked: the setter storing nothing, and the loop ignoring the stored hook, each fail it.
func TestSetBeforeProviderHookReplacesTheHookForTheNextRequest(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("done")}
	var fromOptions, fromSetter int
	a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), OnPayload: func(payload any, _ *ai.Model) (any, error) {
		fromOptions++
		return payload, nil
	}})
	mustSend(t, a, "one")
	if _, err := provider.request(1).opts.OnPayload(map[string]any{}, nil); err != nil || fromOptions != 1 {
		t.Fatalf("the constructor's hook: calls = %d, err = %v", fromOptions, err)
	}
	a.SetBeforeProviderHook(func(payload any, _ *ai.Model) (any, error) {
		fromSetter++
		return map[string]any{"replaced": true}, nil
	})
	mustSend(t, a, "two")
	got, err := provider.request(2).opts.OnPayload(map[string]any{"original": true}, nil)
	if err != nil || fromSetter != 1 || fromOptions != 1 {
		t.Fatalf("after the setter: setter calls = %d, options calls = %d, err = %v", fromSetter, fromOptions, err)
	}
	if payload, _ := got.(map[string]any); payload["replaced"] != true {
		t.Fatalf("the replacement hook's result was not returned: %v", got)
	}
	a.SetBeforeProviderHook(nil)
	mustSend(t, a, "three")
	if provider.request(3).opts.OnPayload != nil {
		t.Fatal("a nil hook left a payload callback on the request")
	}
}

// Pi agent.ts:243 and :482 pass the public finishTurn field to every run, so assigning it after construction (agent.ts:209) governs the next run:
// SetFinishTurn installs the hook that FinishTurnHook returns, the loop calls it once per completed turn with the turn it just ran, and a hook that
// returns continue makes one more request (agent-loop.ts:286-294). Mutation-checked: the setter storing nothing, FinishTurnHook returning a
// stale hook, and the loop not calling the hook each fail it.
func TestSetFinishTurnInstallsTheHookTheNextRunCalls(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("ok")}
	a := mustNewAgent(AgentOptions{Model: scriptedModel(provider)})
	if a.FinishTurnHook() != nil {
		t.Fatal("a new Agent already has a finishTurn hook")
	}
	var calls int
	hook := FinishTurn(func(context.Context, AgentTurnContext) (*AgentTurnDecision, error) {
		calls++
		if calls == 1 {
			return &AgentTurnDecision{Action: AgentTurnContinue}, nil
		}
		return nil, nil
	})
	a.SetFinishTurn(hook)
	if a.FinishTurnHook() == nil {
		t.Fatal("FinishTurnHook returned nil after SetFinishTurn")
	}
	mustSend(t, a, "go")
	if calls != 2 || provider.calls() != 2 {
		t.Fatalf("finishTurn calls = %d, provider calls = %d, want 2 and 2 (one continue)", calls, provider.calls())
	}
	failure := errors.New("finish failed")
	a.SetFinishTurn(func(context.Context, AgentTurnContext) (*AgentTurnDecision, error) { return nil, failure })
	_, _ = a.Send(t.Context(), "again")
	last := a.Messages()[len(a.Messages())-1]
	if last.Assistant == nil || last.Assistant.StopReason != ai.StopReasonError {
		t.Fatalf("a rejected finishTurn did not fail the turn: %+v", last)
	}
	a.SetFinishTurn(nil)
	if a.FinishTurnHook() != nil {
		t.Fatal("SetFinishTurn(nil) kept the hook")
	}
}

// agent.ts:198 onPayload is a public field a run reads once when it starts (:473). In Go the assignment and that read share stateMu, so assigning the
// hook while a run starts is not a data race. Run with -race: the setter storing the hook without the lock fails it.
func TestSetBeforeProviderHookIsSafeWhileRunsCaptureIt(t *testing.T) {
	a := mustNewAgent(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: replyText("done")})})
	hook := func(payload any, _ *ai.Model) (any, error) { return payload, nil }
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 200 {
			a.SetBeforeProviderHook(hook)
		}
	}()
	for range 200 {
		a.createLoopConfig(false)
	}
	<-done
}
