package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

func e2eAgent(cfg ai.FauxConfig, steps ...ai.FauxResponseStep) *Agent {
	provider := ai.NewFauxProvider(cfg)
	provider.SetResponses(steps)
	return mustNewAgent(AgentOptions{SystemPrompt: "You are a helpful assistant.", Model: &ai.Model{ID: "faux-1", Provider: provider, ProviderMeta: ai.ProviderMetadata{API: "faux", ProviderID: "faux"}}, ThinkingLevel: ai.ThinkingOff})
}
func e2eText(text string) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}})
}
func e2eCalculateTool() *scriptTool {
	return &scriptTool{name: "calculate", params: map[string]any{"type": "object", "properties": map[string]any{"expression": map[string]any{"type": "string"}}, "required": []string{"expression"}}, execute: func(_ context.Context, _ string, args json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
		var p struct {
			Expression string `json:"expression"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return AgentToolResult{}, err
		}
		// These are the upstream test fixture's two arithmetic expressions; the
		// JavaScript evaluator itself is not production code under test here.
		switch p.Expression {
		case "123 * 456":
			return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("%s = %d", p.Expression, 123*456)}}}, nil
		case "5 + 3":
			return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("%s = %d", p.Expression, 5+3)}}}, nil
		default:
			return AgentToolResult{}, fmt.Errorf("unexpected expression %q", p.Expression)
		}
	}}
}
func e2eAssistantText(message AgentMessage) string {
	var text []string
	if message.Assistant != nil {
		for _, block := range message.Assistant.Content {
			if b, ok := block.(ai.TextContent); ok {
				text = append(text, b.Text)
			}
		}
	}
	return strings.Join(text, "\n")
}

// .upstream/v0.87.1/packages/agent/test/e2e.test.ts:187
func TestAgentE2E_HandlesBasicTextPrompt(t *testing.T) {
	a := e2eAgent(ai.FauxConfig{}, e2eText("4"))
	a.SetSystemPrompt("You are a helpful assistant. Keep your responses concise.")
	messages := mustSend(t, a, "What is 2+2? Answer with just the number.")
	if a.IsStreaming() || !slices.Equal(roles(messages), []string{"system", "user", "assistant"}) || !strings.Contains(e2eAssistantText(messages[2]), "4") {
		t.Fatalf("messages=%v streaming=%v", messages, a.IsStreaming())
	}
}

// .upstream/v0.87.1/packages/agent/test/e2e.test.ts:193
func TestAgentE2E_ExecutesToolsAndTracksPendingToolCalls(t *testing.T) {
	a := e2eAgent(ai.FauxConfig{}, ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Let me calculate that."), ai.FauxToolCall("calculate", map[string]any{"expression": "123 * 456"}, &ai.FauxToolCallOptions{ID: "calc-1"})}, StopReason: "toolUse"}), e2eText("The result is 56088."))
	a.SetTools([]AgentTool{e2eCalculateTool()})
	a.SetSystemPrompt("You are a helpful assistant. Always use the calculator tool for math.")
	var pending [][]string
	var eventNames []string
	a.Subscribe(func(_ context.Context, event AgentEvent) error {
		switch event.(type) {
		case ToolExecutionStartEvent, ToolExecutionEndEvent:
			pending = append(pending, a.PendingToolCalls())
			eventNames = append(eventNames, eventType(event))
		}
		return nil
	})
	messages := mustSend(t, a, "Calculate 123 * 456 using the calculator tool.")
	result := findToolResult(t, messages, "calc-1")
	if a.IsStreaming() || len(messages) < 4 || !strings.Contains(result.Text(), "123 * 456 = 56088") || !strings.Contains(e2eAssistantText(messages[len(messages)-1]), "56088") || len(a.PendingToolCalls()) != 0 {
		t.Fatalf("messages=%v pending=%v", messages, a.PendingToolCalls())
	}
	if !slices.Equal(eventNames, []string{"tool_execution_start", "tool_execution_end"}) || len(pending) != 2 || !slices.Equal(pending[0], []string{"calc-1"}) || len(pending[1]) != 0 {
		t.Fatalf("events=%v pending=%v", eventNames, pending)
	}
}

// .upstream/v0.87.1/packages/agent/test/e2e.test.ts:208
// Pi source: packages/ai/src/providers/faux.ts
// mutation-checked: dropping the reads and writes of FauxConfig.TokensPerSecond fails it
func TestAgentE2E_HandlesAbortDuringStreaming(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := e2eAgent(ai.FauxConfig{TokensPerSecond: 20, TokenSize: &ai.FauxTokenSize{Min: new(2), Max: new(2)}}, e2eText("one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen"))
		done := sendAsync(t, a, "Count slowly from 1 to 20.")
		time.Sleep(30 * time.Millisecond)
		a.Abort()
		<-done
		messages := a.Messages()
		if a.IsStreaming() || len(messages) < 2 {
			t.Fatalf("messages=%v streaming=%v", messages, a.IsStreaming())
		}
		last := messages[len(messages)-1].Assistant
		if last == nil || last.StopReason != ai.StopReasonAborted || last.ErrorMessage == "" || a.ErrorMessage() != last.ErrorMessage {
			t.Fatalf("last=%+v state error=%q", last, a.ErrorMessage())
		}
	})
}

// .upstream/v0.87.1/packages/agent/test/e2e.test.ts:221
func TestAgentE2E_EmitsLifecycleUpdatesWhileStreaming(t *testing.T) {
	a := e2eAgent(ai.FauxConfig{TokenSize: &ai.FauxTokenSize{Min: new(1), Max: new(1)}}, e2eText("1 2 3 4 5"))
	var events []string
	a.Subscribe(func(_ context.Context, event AgentEvent) error {
		if name := eventType(event); name != "" {
			events = append(events, name)
		}
		return nil
	})
	messages := mustSend(t, a, "Count from 1 to 5.")
	for _, name := range []string{"agent_start", "turn_start", "message_start", "message_update", "message_end", "turn_end", "agent_end"} {
		if !slices.Contains(events, name) {
			t.Fatalf("events=%v lack %s", events, name)
		}
	}
	if slices.Index(events, "agent_start") >= slices.Index(events, "message_start") || slices.Index(events, "message_start") >= slices.Index(events, "message_end") || slices.Index(events, "message_end") >= slices.Index(events, "agent_end") {
		t.Fatalf("events=%v", events)
	}
	if a.IsStreaming() || len(messages) != 3 {
		t.Fatalf("messages=%v streaming=%v", messages, a.IsStreaming())
	}
}

// .upstream/v0.87.1/packages/agent/test/e2e.test.ts:227
func TestAgentE2E_MaintainsContextAcrossMultipleTurns(t *testing.T) {
	a := e2eAgent(ai.FauxConfig{}, e2eText("Nice to meet you, Alice."), ai.FauxFactoryStep(func(transcript ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
		response := "I do not know your name."
		if slices.ContainsFunc(userTexts(transcript), func(text string) bool { return strings.Contains(text, "Alice") }) {
			response = "Your name is Alice."
		}
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(response)}}.AssistantMessage(), nil
	}))
	if messages := mustSend(t, a, "My name is Alice."); len(messages) != 3 {
		t.Fatalf("messages=%v", messages)
	}
	messages := mustSend(t, a, "What is my name?")
	if len(messages) != 5 || !strings.Contains(strings.ToLower(e2eAssistantText(messages[4])), "alice") {
		t.Fatalf("messages=%v", messages)
	}
}

// .upstream/v0.87.1/packages/agent/test/e2e.test.ts:243
func TestAgentE2E_PreservesThinkingContentBlocks(t *testing.T) {
	a := e2eAgent(ai.FauxConfig{Model: "faux-reasoning"}, ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxThinking("step by step"), ai.FauxText("4")}}))
	a.SetThinkingLevel(ai.ThinkingLow)
	messages := mustSend(t, a, "What is 2+2?")
	want := []ai.AssistantContentBlock{ai.ThinkingContent{Thinking: "step by step"}, ai.TextContent{Text: "4"}}
	if len(messages) != 3 || messages[2].Assistant == nil || !reflect.DeepEqual(messages[2].Assistant.Content, want) {
		t.Fatalf("messages=%v", messages)
	}
}

// .upstream/v0.87.1/packages/agent/test/e2e.test.ts:270
func TestAgentE2E_ThrowsWhenNoMessagesInContext(t *testing.T) {
	a := e2eAgent(ai.FauxConfig{})
	if _, err := a.ContinueMessages(t.Context()); err != ErrNoMessagesToContinue || err.Error() != "No messages to continue from" {
		t.Fatalf("Continue error=%v", err)
	}
}

// .upstream/v0.87.1/packages/agent/test/e2e.test.ts:283
func TestAgentE2E_ThrowsWhenLastMessageIsAssistant(t *testing.T) {
	a := e2eAgent(ai.FauxConfig{})
	a.SetMessages([]AgentMessage{assistantText("Hello")})
	if _, err := a.ContinueMessages(t.Context()); err == nil || err.Error() != "Cannot continue from message role: assistant" {
		t.Fatalf("Continue error=%v", err)
	}
}

// .upstream/v0.87.1/packages/agent/test/e2e.test.ts:318
func TestAgentE2E_ContinuesAndGetsResponseFromUserTail(t *testing.T) {
	a := e2eAgent(ai.FauxConfig{}, e2eText("HELLO WORLD"))
	a.SetMessages([]AgentMessage{userMessage("Say exactly: HELLO WORLD")})
	messages, err := a.ContinueMessages(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if a.IsStreaming() || !slices.Equal(roles(messages), []string{"user", "assistant"}) || !strings.Contains(strings.ToUpper(e2eAssistantText(messages[1])), "HELLO WORLD") {
		t.Fatalf("messages=%v", messages)
	}
}

// .upstream/v0.87.1/packages/agent/test/e2e.test.ts:352
func TestAgentE2E_ContinuesAndProcessesToolResults(t *testing.T) {
	a := e2eAgent(ai.FauxConfig{}, e2eText("The answer is 8."))
	a.SetTools([]AgentTool{e2eCalculateTool()})
	assistant := assistantText("Let me calculate that.")
	assistant.Assistant.Content = append(assistant.Assistant.Content, toolCall("calc-1", "calculate", ai.JsonObject{"expression": "5 + 3"}))
	assistant.Assistant.StopReason = ai.StopReasonToolUse
	a.SetMessages([]AgentMessage{userMessage("What is 5 + 3?"), assistant, {ToolResult: &ToolResultMessage{Role: RoleToolResult, ToolCallID: "calc-1", ToolName: "calculate", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "5 + 3 = 8"}}}}})
	messages, err := a.ContinueMessages(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if a.IsStreaming() || len(messages) < 4 || !strings.Contains(e2eAssistantText(messages[len(messages)-1]), "8") {
		t.Fatalf("messages=%v", messages)
	}
}
