package agent

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/agent/test/agent.test.ts:112 "should create an agent instance with default state".
func TestAgentState_DefaultState(t *testing.T) {
	model := scriptedModel(&scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "echo", ai.JsonObject{"value": "x"}))})
	a := mustNewAgent(AgentOptions{Model: model})
	state := a.State()

	if state.Model() == nil || state.ThinkingLevel() != ai.ThinkingOff || len(state.Tools()) != 0 || len(state.Messages()) != 0 {
		t.Fatalf("model %v, thinking %q, tools %d, messages %d", state.Model(), state.ThinkingLevel(), len(state.Tools()), len(state.Messages()))
	}
	if state.IsStreaming() || state.StreamingMessage() != nil || len(state.PendingToolCalls()) != 0 || state.ErrorMessage() != "" {
		t.Fatalf("run status: streaming %v, partial %v, pending %v, error %q", state.IsStreaming(), state.StreamingMessage(), state.PendingToolCalls(), state.ErrorMessage())
	}
}

// upstream: agent.test.ts:127 "should create an agent instance with custom initial state" (the system prompt seeds the leading system message).
func TestAgentState_ReflectsInitialState(t *testing.T) {
	model := scriptedModel(&scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "echo", ai.JsonObject{"value": "x"}))})
	a := mustNewAgent(AgentOptions{Model: model, InitialState: &AgentInitialState{SystemPrompt: "You are a helpful assistant.", ThinkingLevel: ai.ThinkingLow}})
	state := a.State()

	if state.SystemPrompt() != "You are a helpful assistant." || state.ThinkingLevel() != ai.ThinkingLow || state.Model() != model {
		t.Fatalf("system prompt %q, thinking %q, model %v", state.SystemPrompt(), state.ThinkingLevel(), state.Model())
	}
	// agent.test.ts:138: messages toEqual [{ role: "system", content: "You are a helpful assistant.", timestamp: 0 }].
	messages := state.Messages()
	if len(messages) != 1 || messages[0].System == nil {
		t.Fatalf("messages = %+v, want exactly the leading system message", messages)
	}
	if system := messages[0].System; system.Content != ai.SystemText("You are a helpful assistant.") || system.Timestamp != 0 || len(system.Sections) != 0 || len(system.ToolsAdded) != 0 || len(system.ToolsRemoved) != 0 {
		t.Fatalf("system message = %+v", system)
	}
	// A snapshot is a copy: writing to it does not change the agent.
	if live := a.Messages(); &messages[0] == &live[0] {
		t.Fatal("State().Messages() returned the live transcript, want a copy")
	}
}

// upstream: agent.test.ts:189 `agent.state.tools = [second]` and :320 `agent.state.thinkingLevel = "low"`: Pi's state is writable and the writes change the agent.
func TestAgentState_WritesChangeTheAgent(t *testing.T) {
	provider := &scriptedProvider{respond: func(int, scriptedRequest) *ai.AssistantMessageEventStream { return doneStream(textMessage("done")) }}
	first, second := scriptedModel(provider), scriptedModel(provider)
	a := mustNewAgent(AgentOptions{Model: first})
	state := a.State()

	state.SetThinkingLevel(ai.ThinkingHigh)
	if a.ThinkingLevel() != ai.ThinkingHigh || state.ThinkingLevel() != ai.ThinkingHigh {
		t.Fatalf("thinking level after SetThinkingLevel: agent %q, state %q", a.ThinkingLevel(), state.ThinkingLevel())
	}
	state.SetModel(second)
	if a.Model() != second {
		t.Fatal("state.SetModel did not change the agent's model")
	}
	tools := []AgentTool{valueEchoTool(ToolModeParallel, func(string) {})}
	state.SetTools(tools)
	tools[0] = nil // assigning copies the top-level array
	if got := a.Tools(); len(got) != 1 || got[0] == nil || got[0].Name() != "echo" {
		t.Fatalf("agent tools after state.SetTools = %v", got)
	}
	message := AgentMessage{User: &UserMessage{Role: "user", Content: ai.UserText("hi"), Timestamp: 1}}
	messages := []AgentMessage{message}
	state.SetMessages(messages)
	messages[0] = AgentMessage{}
	if got := a.State().Messages(); len(got) != 1 || got[0].User == nil {
		t.Fatalf("transcript after state.SetMessages = %+v", got)
	}
}

// upstream: types.ts:416 pendingToolCalls is a ReadonlySet<string> that agent.ts:581-590 grows and shrinks by copying, so it iterates in tool start order.
// Ids "z" then "a" start in that order; a snapshot that sorted or hashed them would reorder them.
func TestAgentState_PendingToolCallsKeepStartOrder(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	tool := &scriptTool{name: "echo", label: "Echo", mode: ToolModeParallel, params: valueSchema,
		execute: func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
			started <- struct{}{}
			<-release
			return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}}, nil
		}}
	model := scriptedModel(&scriptedProvider{respond: toolCallsThenText(toolCall("z", "echo", ai.JsonObject{"value": "1"}), toolCall("a", "echo", ai.JsonObject{"value": "2"}))})
	a := mustNewAgent(AgentOptions{Model: model, Tools: []AgentTool{tool}})
	done := make(chan error, 1)
	go func() { done <- a.Prompt(context.Background(), "go") }()
	waitSignal(t, started, "first tool start")
	waitSignal(t, started, "second tool start")
	state := a.State().Snapshot()
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if !slices.Equal(state.PendingToolCalls, []string{"z", "a"}) || !state.IsStreaming {
		t.Fatalf("pending tool calls = %v (streaming %v), want [z a] while both run", state.PendingToolCalls, state.IsStreaming)
	}
	if after := a.State().Snapshot(); len(after.PendingToolCalls) != 0 || after.IsStreaming {
		t.Fatalf("after the run: pending %v, streaming %v", after.PendingToolCalls, after.IsStreaming)
	}
}

// upstream: agent.test.ts:127 "should create an agent instance with custom initial state" and :156 (the system prompt seeds the leading system message).
func TestAgentState_ReflectsInitialStateAndLaterWrites(t *testing.T) {
	model := scriptedModel(&scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "echo", ai.JsonObject{"value": "x"}))})
	a := mustNewAgent(AgentOptions{Model: model, InitialState: &AgentInitialState{SystemPrompt: "You are a helpful assistant.", ThinkingLevel: ai.ThinkingLow}})
	state := a.State().Snapshot()

	if state.SystemPrompt != "You are a helpful assistant." || state.ThinkingLevel != ai.ThinkingLow || state.Model != model {
		t.Fatalf("state = %+v", state)
	}
	// agent.test.ts:138: messages toEqual [{ role: "system", content: "You are a helpful assistant.", timestamp: 0 }].
	if len(state.Messages) != 1 || state.Messages[0].System == nil {
		t.Fatalf("messages = %+v, want exactly the leading system message", state.Messages)
	}
	if system := state.Messages[0].System; system.Content != ai.SystemText("You are a helpful assistant.") || system.Timestamp != 0 || len(system.Sections) != 0 || len(system.ToolsAdded) != 0 || len(system.ToolsRemoved) != 0 {
		t.Fatalf("system message = %+v", system)
	}
	// A snapshot is a copy: writing to it does not change the agent.
	if live := a.Messages(); &state.Messages[0] == &live[0] {
		t.Fatal("Snapshot returned the live transcript, want a copy")
	}
	a.SetThinkingLevel(ai.ThinkingHigh)
	if got := a.State().ThinkingLevel(); got != ai.ThinkingHigh {
		t.Fatalf("thinking level after SetThinkingLevel = %q", got)
	}
	tool := valueEchoTool(ToolModeParallel, func(string) {})
	a.SetTools([]AgentTool{tool})
	if got := a.State().Tools(); len(got) != 1 || got[0].Name() != "echo" {
		t.Fatalf("tools after SetTools = %v", got)
	}
}
