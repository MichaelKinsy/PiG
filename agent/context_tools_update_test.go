package agent

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/agent/src/agent-loop.ts:188 and :228 (`currentContext = update.context ?? currentContext`) and types.ts:161-170
// (AgentLoopTurnUpdate.context is an AgentContext): an update replaces the tools the run executes together with the transcript, and the next
// request declares the change.
func TestAgentLoop_ContextUpdateReplacesTheExecutableTools(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(a *Agent, replacement []AgentTool)
	}{
		{"prepareNextTurn", func(a *Agent, replacement []AgentTool) {
			calls := 0
			a.SetPrepareNextTurnWithContext(func(_ context.Context, turn PrepareNextTurnContext) (*AgentLoopTurnUpdate, error) {
				calls++
				if calls > 1 {
					return nil, nil
				}
				if len(turn.Context.Tools) != 1 || turn.Context.Tools[0].Name() != "echo" {
					t.Errorf("turn context tools = %v, want the run's echo tool", turn.Context.Tools)
				}
				return &AgentLoopTurnUpdate{Context: &AgentContext{Messages: slices.Clone(turn.Context.Messages), Tools: replacement}}, nil
			})
		}},
		{"prepareRequest", func(a *Agent, replacement []AgentTool) {
			calls := 0
			a.SetPrepareRequest(func(_ context.Context, request PrepareRequestContext) (*AgentRequestUpdate, error) {
				calls++
				if calls == 1 {
					return nil, nil
				}
				return &AgentRequestUpdate{Context: &AgentContext{Messages: slices.Clone(request.Context.Messages), Tools: replacement}}, nil
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &scriptedProvider{respond: func(call int, _ scriptedRequest) *ai.AssistantMessageEventStream {
				if call <= 2 {
					return doneStream(toolUseMessage(toolCall("tool-"+string(rune('0'+call)), "echo", ai.JsonObject{"value": "hello"})))
				}
				return doneStream(textMessage("done"))
			}}
			echoed := 0
			a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{valueEchoTool(ToolModeParallel, func(string) { echoed++ })}})
			tc.setup(a, []AgentTool{noopTool()})

			messages := mustSend(t, a, "echo twice")

			if echoed != 1 {
				t.Fatalf("echo executed %d times, want once: the update removed it for the second call", echoed)
			}
			var results []*ToolResultMessage
			for _, m := range messages {
				if m.ToolResult != nil {
					results = append(results, m.ToolResult)
				}
			}
			if len(results) != 2 || results[0].IsError || !results[1].IsError {
				t.Fatalf("tool results = %+v, want a result then a not-found error", results)
			}
			declared := false
			for _, m := range provider.request(3).transcript.Messages() {
				if system, ok := m.(ai.SystemMessage); ok && slices.ContainsFunc(system.ToolsRemoved, func(tool ai.ToolReference) bool { return tool.Name == "echo" }) {
					declared = true
				}
			}
			if !declared {
				t.Fatal("the request after the update does not declare the removed echo tool")
			}
		})
	}
}

// upstream: packages/agent/src/agent.ts:460 (createContextSnapshot copies state.tools when a run starts): a SetTools during the run does not change
// the tools the run executes.
func TestAgentRunExecutesTheToolsItStartedWith(t *testing.T) {
	provider := &scriptedProvider{respond: func(call int, _ scriptedRequest) *ai.AssistantMessageEventStream {
		if call <= 2 {
			return doneStream(toolUseMessage(toolCall("tool-"+string(rune('0'+call)), "echo", ai.JsonObject{"value": "hello"})))
		}
		return doneStream(textMessage("done"))
	}}
	var a *Agent
	echoed := 0
	a = mustNewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{valueEchoTool(ToolModeParallel, func(string) {
		echoed++
		a.SetTools([]AgentTool{noopTool()})
	})}})

	messages := mustSend(t, a, "echo twice")

	if echoed != 2 {
		t.Fatalf("echo executed %d times, want 2: the run keeps the tools it started with", echoed)
	}
	for _, m := range messages {
		if m.ToolResult != nil && m.ToolResult.IsError {
			t.Fatalf("tool result error %+v", m.ToolResult)
		}
	}
	if tools := a.Tools(); len(tools) != 1 || tools[0].Name() != "noop" {
		t.Fatalf("agent tools after the run = %v, want the SetTools replacement", tools)
	}
}

// upstream: packages/agent/src/agent.ts:211,245,484-489 (public Agent.prepareNextTurn runs as `prepareNextTurn?.(signal)` when no prepareNextTurnWithContext is set) and
// packages/agent/test/agent-loop.test.ts:1503 ("should use prepareNextTurn snapshot before continuing"): the update the context-only hook returns after a tool turn is
// what the next request runs with. SetPrepareNextTurn assigns that hook for later runs.
func TestAgentSetPrepareNextTurnHookUpdatesTheNextTurn(t *testing.T) {
	provider := &scriptedProvider{respond: func(call int, _ scriptedRequest) *ai.AssistantMessageEventStream {
		if call <= 2 {
			return doneStream(toolUseMessage(toolCall("tool-"+string(rune('0'+call)), "echo", ai.JsonObject{"value": "hello"})))
		}
		return doneStream(textMessage("done"))
	}}
	echoed := 0
	a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{valueEchoTool(ToolModeParallel, func(string) { echoed++ })}})
	var seen []context.Context
	calls := 0
	a.SetPrepareNextTurn(func(ctx context.Context) (*AgentLoopTurnUpdate, error) {
		seen = append(seen, ctx)
		calls++
		if calls > 1 {
			return nil, nil
		}
		return &AgentLoopTurnUpdate{Context: &AgentContext{Tools: []AgentTool{noopTool()}}}, nil
	})
	if a.PrepareNextTurnHook() == nil {
		t.Fatal("SetPrepareNextTurn did not install the hook")
	}
	messages := mustSend(t, a, "echo twice")
	if echoed != 1 {
		t.Fatalf("echo executed %d times, want once: the hook's update removed it before the second call", echoed)
	}
	if len(seen) == 0 || seen[0] == nil {
		t.Fatal("the hook never ran with a context")
	}
	var results []*ToolResultMessage
	for _, m := range messages {
		if m.ToolResult != nil {
			results = append(results, m.ToolResult)
		}
	}
	if len(results) != 2 || results[0].IsError || !results[1].IsError {
		t.Fatalf("tool results = %+v, want a result then a not-found error", results)
	}
	// Clearing the hook stops the updates for the next run.
	a.SetPrepareNextTurn(nil)
	if a.PrepareNextTurnHook() != nil {
		t.Fatal("SetPrepareNextTurn(nil) kept the hook")
	}
}
