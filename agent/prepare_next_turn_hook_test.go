package agent

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/agent/src/agent.ts:211-213,245,484-492. Agent.prepareNextTurn is a public property that holds a context-free hook; the run builds
// `prepareNextTurnWithContext || prepareNextTurn` and calls it after each turn (`await this.prepareNextTurn?.(this.signal)`), so the hook set through
// SetPrepareNextTurn runs once per completed turn and its update (agent-loop.ts:188,228 `update.context ?? currentContext`) takes effect; a hook that
// takes the turn context wins when both are set.
func TestAgentSetPrepareNextTurnRunsTheContextFreeHookAfterEachTurn(t *testing.T) {
	newProvider := func() *scriptedProvider {
		return &scriptedProvider{respond: func(call int, _ scriptedRequest) *ai.AssistantMessageEventStream {
			if call <= 2 {
				return doneStream(toolUseMessage(toolCall("tool-"+string(rune('0'+call)), "echo", ai.JsonObject{"value": "hello"})))
			}
			return doneStream(textMessage("done"))
		}}
	}
	t.Run("context-free hook replaces the executable tools", func(t *testing.T) {
		provider := newProvider()
		echoed := 0
		a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{valueEchoTool(ToolModeParallel, func(string) { echoed++ })}})
		calls := 0
		a.SetPrepareNextTurn(func(context.Context) (*AgentLoopTurnUpdate, error) {
			calls++
			if calls > 1 {
				return nil, nil
			}
			return &AgentLoopTurnUpdate{Context: &AgentContext{Messages: nil, Tools: []AgentTool{noopTool()}}}, nil
		})
		if a.PrepareNextTurnHook() == nil {
			t.Fatal("PrepareNextTurnHook is nil after SetPrepareNextTurn")
		}
		mustSend(t, a, "echo twice")
		if calls == 0 {
			t.Fatal("the context-free hook never ran")
		}
		if echoed > 1 {
			t.Fatalf("echo executed %d times: the hook's replacement tools did not apply", echoed)
		}
	})

	t.Run("context-taking hook wins when both are set", func(t *testing.T) {
		provider := newProvider()
		a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{valueEchoTool(ToolModeParallel, func(string) {})}})
		var free, withContext int
		a.SetPrepareNextTurn(func(context.Context) (*AgentLoopTurnUpdate, error) { free++; return nil, nil })
		a.SetPrepareNextTurnWithContext(func(_ context.Context, turn PrepareNextTurnContext) (*AgentLoopTurnUpdate, error) {
			withContext++
			if !slices.ContainsFunc(turn.Context.Tools, func(tool AgentTool) bool { return tool.Name() == "echo" }) {
				t.Errorf("turn context tools = %v, want the echo tool", turn.Context.Tools)
			}
			return nil, nil
		})
		mustSend(t, a, "echo twice")
		if withContext == 0 || free != 0 {
			t.Fatalf("context-taking hook ran %d times and the context-free hook %d times, want the first only", withContext, free)
		}
	})
}
