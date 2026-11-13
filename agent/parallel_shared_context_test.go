package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi hands one live context to every call of a parallel batch (agent-loop.ts executeToolCallsParallel: currentContext goes to
// prepareToolCall and to each call's closure under Promise.all, and from there to beforeToolCall and afterToolCall): what one call's after
// hook writes in place, the after hook of a call that settles later reads, and the next request carries what the last hook wrote.
func TestAfterHooksOfParallelToolCallsShareTheRunsMessages(t *testing.T) {
	for _, order := range [][2]string{{"first", "second"}, {"second", "first"}} {
		t.Run("the call "+order[0]+" settles first", func(t *testing.T) {
			provider := &scriptedProvider{respond: toolCallsThenText(
				toolCall("tool-1", "echo", ai.JsonObject{"value": "first"}),
				toolCall("tool-2", "echo", ai.JsonObject{"value": "second"}),
			)}
			earlierHookWrote := make(chan struct{})
			var mu sync.Mutex
			read := map[string]string{}
			a := mustNewAgent(AgentOptions{
				Model: scriptedModel(provider),
				// The later call's tool returns once the earlier call's after hook has written.
				Tools: []AgentTool{valueEchoTool(ToolModeParallel, func(value string) {
					if value == order[1] {
						<-earlierHookWrote
					}
				})},
				ToolExecution: ToolModeParallel,
				AfterToolCall: func(_ context.Context, call AfterToolCallContext) *AfterToolCallResult {
					value := argValue(call.Args)
					index := promptIndex(call.Context.Messages)
					mu.Lock()
					read[value] = userText(call.Context.Messages[index])
					mu.Unlock()
					call.Context.Messages[index] = userMessage("after " + value)
					if value == order[0] {
						close(earlierHookWrote)
					}
					return nil
				},
			})

			mustSend(t, a, "prompt")

			if read[order[0]] != "prompt" || read[order[1]] != "after "+order[0] {
				t.Errorf("the after hooks read %q then %q: want the prompt, then the message the earlier call's hook wrote in place", read[order[0]], read[order[1]])
			}
			if got := userTexts(provider.request(2).transcript); !reflect.DeepEqual(got, []string{"after " + order[1]}) {
				t.Errorf("the request after the tools carries %q, want the message the last after hook wrote in place", got)
			}
		})
	}
}

// A tool reaches the same context (in Pi through any reference to it; here through ToolCallHookContextFrom): what the first call's tool
// writes in place while both run, the second call's tool, the after hooks and the next request see.
func TestAToolOfAParallelBatchThatReplacesAMessageInPlaceChangesTheRun(t *testing.T) {
	provider := &scriptedProvider{respond: toolCallsThenText(
		toolCall("tool-1", "echo", ai.JsonObject{"value": "first"}),
		toolCall("tool-2", "echo", ai.JsonObject{"value": "second"}),
	)}
	wrote := make(chan struct{})
	var mu sync.Mutex
	read := map[string]string{}
	note := func(who string, messages []AgentMessage) {
		mu.Lock()
		defer mu.Unlock()
		read[who] = userText(messages[promptIndex(messages)])
	}
	tool := &scriptTool{name: "echo", label: "Echo", mode: ToolModeParallel, params: valueSchema,
		execute: func(ctx context.Context, _ string, args json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
			value := argValue(args)
			if value == "first" {
				hookContext, _ := ToolCallHookContextFrom(ctx)
				hookContext.Context.Messages[promptIndex(hookContext.Context.Messages)] = userMessage("written by the first call's tool")
				close(wrote)
			} else {
				<-wrote
				hookContext, _ := ToolCallHookContextFrom(ctx)
				note("the second call's tool", hookContext.Context.Messages)
			}
			return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "echoed: " + value}}}, nil
		}}
	a := mustNewAgent(AgentOptions{
		Model:         scriptedModel(provider),
		Tools:         []AgentTool{tool},
		ToolExecution: ToolModeParallel,
		AfterToolCall: func(_ context.Context, call AfterToolCallContext) *AfterToolCallResult {
			note("the after hook of "+argValue(call.Args), call.Context.Messages)
			return nil
		},
	})

	mustSend(t, a, "prompt")

	for _, who := range []string{"the second call's tool", "the after hook of first", "the after hook of second"} {
		if read[who] != "written by the first call's tool" {
			t.Errorf("%s read %q, want the message the first call's tool wrote in place", who, read[who])
		}
	}
	if got := userTexts(provider.request(2).transcript); !reflect.DeepEqual(got, []string{"written by the first call's tool"}) {
		t.Errorf("the request after the tools carries %q, want the message the tool wrote in place", got)
	}
}

// The tools of a parallel batch run at the same time, which Pi's never do, and share the run's messages: two tools that write one
// message lock between themselves, as Go code that shares memory does, and the next request carries the last write. Both tools are
// running before either writes, and each writes a thousand times. (Run with -race.)
func TestToolsOfAParallelBatchThatWriteOneMessageUnderTheirOwnLock(t *testing.T) {
	provider := &scriptedProvider{respond: toolCallsThenText(
		toolCall("tool-1", "echo", ai.JsonObject{"value": "first"}),
		toolCall("tool-2", "echo", ai.JsonObject{"value": "second"}),
	)}
	var running sync.WaitGroup
	running.Add(2)
	var mu sync.Mutex
	var last string
	tool := &scriptTool{name: "echo", label: "Echo", mode: ToolModeParallel, params: valueSchema,
		execute: func(ctx context.Context, _ string, args json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
			value := argValue(args)
			hookContext, _ := ToolCallHookContextFrom(ctx)
			messages := hookContext.Context.Messages
			running.Done()
			running.Wait()
			for n := range 1000 {
				mu.Lock()
				last = fmt.Sprintf("written by the tool of %s, %d", value, n)
				messages[promptIndex(messages)] = userMessage(last)
				mu.Unlock()
			}
			return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "echoed: " + value}}}, nil
		}}
	a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{tool}, ToolExecution: ToolModeParallel})

	mustSend(t, a, "prompt")

	if got := userTexts(provider.request(2).transcript); !reflect.DeepEqual(got, []string{last}) {
		t.Errorf("the request after the tools carries %q, want the last message a tool wrote in place, %q", got, last)
	}
}

// Pi's tools are not handed the context: a tool reaches it through a reference kept from a hook (the probe keeps the before hook's). The
// first call's tool writes the kept array in place while both tools run.
func TestAToolThatWritesTheMessagesKeptFromItsBeforeHook(t *testing.T) {
	provider := &scriptedProvider{respond: toolCallsThenText(
		toolCall("tool-1", "echo", ai.JsonObject{"value": "first"}),
		toolCall("tool-2", "echo", ai.JsonObject{"value": "second"}),
	)}
	wrote := make(chan struct{})
	var kept []AgentMessage
	var mu sync.Mutex
	read := map[string]string{}
	note := func(who string, messages []AgentMessage) {
		mu.Lock()
		defer mu.Unlock()
		read[who] = userText(messages[promptIndex(messages)])
	}
	a := mustNewAgent(AgentOptions{
		Model: scriptedModel(provider),
		Tools: []AgentTool{valueEchoTool(ToolModeParallel, func(value string) {
			if value == "first" {
				kept[promptIndex(kept)] = userMessage("written by the first call's tool")
				close(wrote)
				return
			}
			<-wrote
			note("the second call's tool", kept)
		})},
		ToolExecution: ToolModeParallel,
		BeforeToolCall: func(_ context.Context, call BeforeToolCallContext) *ToolCallHookResult {
			kept = call.Context.Messages
			return nil
		},
		AfterToolCall: func(_ context.Context, call AfterToolCallContext) *AfterToolCallResult {
			if argValue(call.Args) == "second" {
				note("the after hook of second", call.Context.Messages)
			}
			return nil
		},
	})

	mustSend(t, a, "prompt")

	t.Logf("read: %q; the request after the tools carries %q", read, userTexts(provider.request(2).transcript))
	for _, who := range []string{"the second call's tool", "the after hook of second"} {
		if read[who] != "written by the first call's tool" {
			t.Errorf("%s read %q, want the message the first call's tool wrote in place", who, read[who])
		}
	}
	if got := userTexts(provider.request(2).transcript); !reflect.DeepEqual(got, []string{"written by the first call's tool"}) {
		t.Errorf("the request after the tools carries %q, want the message the tool wrote in place", got)
	}
}

func userText(message AgentMessage) string {
	if message.User == nil {
		return "<not a user message>"
	}
	return message.User.Content.(ai.UserContentBlocks)[0].(ai.TextContent).Text
}
