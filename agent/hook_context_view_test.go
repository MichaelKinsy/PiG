package agent

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi hands prepareRequest the live context (agent-loop.ts runLoop: config.prepareRequest({ context: currentContext, ... })): a hook that
// replaces one of its messages in place changes the request that follows. The hook receives the run's own messages, not a copy of the
// history made for each call; an append by the hook goes to an array of its own and does not reach the run.
func TestAHookThatReplacesAMessageOfItsContextInPlaceChangesTheRequest(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("done")}
	replaced := userMessage("replaced by the hook")
	a := mustNewAgent(AgentOptions{
		Model: scriptedModel(provider),
		PrepareRequest: func(_ context.Context, request PrepareRequestContext) (*AgentRequestUpdate, error) {
			messages := request.Context.Messages
			if len(messages) != 1 || cap(messages) != len(messages) {
				t.Errorf("the hook's context has %d messages and room for %d: want the prompt and no room", len(messages), cap(messages))
			}
			_ = append(messages, userMessage("appended by the hook"))
			messages[len(messages)-1] = replaced
			return nil, nil
		},
	})
	cfg := a.createLoopConfig(false)

	runPrompt(t, a, cfg, userMessage("prompt"))

	if provider.calls() != 1 {
		t.Fatalf("model requests %d, want 1", provider.calls())
	}
	if got := userTexts(provider.request(1).transcript); !reflect.DeepEqual(got, []string{"replaced by the hook"}) {
		t.Fatalf("the request carries %q, want the message the hook wrote in place and nothing it appended", got)
	}
}

// A turn's hooks see the run's messages as they are when the hook runs, and a view a hook keeps does not change when the run goes on.
func TestATurnHookKeepsTheMessagesItWasGiven(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("done")}
	var kept []AgentMessage
	var keptNew []AgentMessage
	a := mustNewAgent(AgentOptions{
		Model: scriptedModel(provider),
		FinishTurn: func(_ context.Context, turn AgentTurnContext) (*AgentTurnDecision, error) {
			if kept == nil {
				kept, keptNew = turn.Context.Messages, turn.NewMessages
				if cap(kept) != len(kept) || cap(keptNew) != len(keptNew) {
					t.Errorf("the turn's messages have room: context %d of %d, new %d of %d", len(kept), cap(kept), len(keptNew), cap(keptNew))
				}
			}
			return nil, nil
		},
	})
	cfg := a.createLoopConfig(false)

	runPrompt(t, a, cfg, userMessage("first"))

	if len(kept) != 2 || kept[0].User == nil || kept[1].Assistant == nil || len(keptNew) != 2 {
		t.Fatalf("the first turn's hook saw %d messages and %d new ones: want the prompt and the answer in both", len(kept), len(keptNew))
	}
}

// Pi hands the after hooks of a parallel batch the one live context (agent-loop.ts executeToolCallsParallel: currentContext reaches each
// call's closure under Promise.all and, through finalizeExecutedToolCall, afterToolCall): they write the run's own messages, the later
// hook reads what the earlier one wrote, and the next request carries the last write. The hooks run on the calls' goroutines; the batch
// runs them one at a time, in the order the tools settled, so neither their writes to the messages nor the test's unguarded record of
// their order race. (Run with -race.)
func TestAfterHooksOfParallelToolCallsWriteTheRunsMessagesOneAtATime(t *testing.T) {
	provider := &scriptedProvider{respond: toolCallsThenText(
		toolCall("tool-1", "echo", ai.JsonObject{"value": "first"}),
		toolCall("tool-2", "echo", ai.JsonObject{"value": "second"}),
	)}
	var release sync.WaitGroup
	release.Add(2)
	var order, read []string
	a := mustNewAgent(AgentOptions{
		Model:         scriptedModel(provider),
		Tools:         []AgentTool{valueEchoTool(ToolModeParallel, func(string) { release.Done(); release.Wait() })},
		ToolExecution: ToolModeParallel,
		BeforeToolCall: func(_ context.Context, call BeforeToolCallContext) *ToolCallHookResult {
			call.Context.Messages[promptIndex(call.Context.Messages)] = userMessage("written before " + argValue(call.Args))
			return nil
		},
		AfterToolCall: func(_ context.Context, call AfterToolCallContext) *AfterToolCallResult {
			// Both calls are past their tools here: their goroutines reach the after hooks together.
			index := promptIndex(call.Context.Messages)
			order = append(order, argValue(call.Args))
			read = append(read, userText(call.Context.Messages[index]))
			for range 1000 {
				call.Context.Messages[index] = userMessage("written after " + argValue(call.Args))
			}
			return nil
		},
	})

	mustSend(t, a, "echo both")

	if provider.calls() != 2 || len(order) != 2 {
		t.Fatalf("model requests %d and after hooks %q, want 2 requests and both hooks", provider.calls(), order)
	}
	// The before hooks ran one after another (the second call's wrote last); each after hook then wrote the same message of the run.
	if want := []string{"written before second", "written after " + order[0]}; !reflect.DeepEqual(read, want) {
		t.Errorf("the after hooks read %q, want %q: the last before hook's message, then the earlier after hook's", read, want)
	}
	if got := userTexts(provider.request(2).transcript); !reflect.DeepEqual(got, []string{"written after " + order[1]}) {
		t.Errorf("the request after the tools carries %q, want the message the last after hook (%s) wrote in place", got, order[1])
	}
}

// Pi runs prepareToolCall, and so beforeToolCall, for the calls of a parallel batch one after another in a loop on one thread, handing
// each currentContext (agent-loop.ts executeToolCallsParallel): what the first call's hook writes in place is what the second call's hook
// reads and what the next request carries. The before hooks of a parallel batch receive the run's own messages, as Pi's do.
func TestTheBeforeHooksOfParallelToolCallsShareTheRunsMessages(t *testing.T) {
	provider := &scriptedProvider{respond: toolCallsThenText(
		toolCall("tool-1", "echo", ai.JsonObject{"value": "first"}),
		toolCall("tool-2", "echo", ai.JsonObject{"value": "second"}),
	)}
	var seenBySecond string
	a := mustNewAgent(AgentOptions{
		Model:         scriptedModel(provider),
		Tools:         []AgentTool{valueEchoTool(ToolModeParallel, nil)},
		ToolExecution: ToolModeParallel,
		BeforeToolCall: func(_ context.Context, call BeforeToolCallContext) *ToolCallHookResult {
			index := promptIndex(call.Context.Messages)
			switch argValue(call.Args) {
			case "first":
				call.Context.Messages[index] = userMessage("replaced by the first before hook")
			case "second":
				seenBySecond = call.Context.Messages[index].User.Content.(ai.UserContentBlocks)[0].(ai.TextContent).Text
			}
			return nil
		},
	})

	mustSend(t, a, "echo both")

	if seenBySecond != "replaced by the first before hook" {
		t.Errorf("the second call's before hook saw the prompt %q: want the message the first call's hook replaced in place", seenBySecond)
	}
	if got := userTexts(provider.request(2).transcript); !reflect.DeepEqual(got, []string{"replaced by the first before hook"}) {
		t.Errorf("the request after the tools carries %q, want the message the first call's before hook wrote in place", got)
	}
}

// The hooks of a call that runs alone, or of calls that run in sequence, run one at a time and receive the run's messages, as Pi's
// hooks do (agent-loop.ts executeToolCalls: beforeToolCall and afterToolCall get currentContext).
func TestAHookOfASequentialToolCallThatReplacesAMessageInPlaceChangesTheRun(t *testing.T) {
	for _, mode := range []ToolExecutionMode{ToolModeSequential, ToolModeParallel} {
		provider := &scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "echo", ai.JsonObject{"value": "only"}))}
		a := mustNewAgent(AgentOptions{
			Model:         scriptedModel(provider),
			Tools:         []AgentTool{valueEchoTool(mode, nil)},
			ToolExecution: mode,
			AfterToolCall: func(_ context.Context, call AfterToolCallContext) *AfterToolCallResult {
				call.Context.Messages[promptIndex(call.Context.Messages)] = userMessage("replaced by the hook")
				return nil
			},
		})

		mustSend(t, a, "echo one")

		if got := userTexts(provider.request(2).transcript); !reflect.DeepEqual(got, []string{"replaced by the hook"}) {
			t.Fatalf("%s: the request after the tool carries %q, want the message the hook wrote in place", mode, got)
		}
	}
}

// promptIndex is the position of the first user message: a run with tools declares them in a system message ahead of it.
func promptIndex(messages []AgentMessage) int {
	for index, message := range messages {
		if message.User != nil {
			return index
		}
	}
	panic("no user message")
}
