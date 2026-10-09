package coding

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

type modelExtensionEchoTool struct {
	usage   *ai.Usage
	blocked bool
	calls   int
}

func (*modelExtensionEchoTool) Name() string  { return "echo" }
func (*modelExtensionEchoTool) Label() string { return "Echo" }
func (*modelExtensionEchoTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "echo", Description: "Echo text back", Parameters: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}}}
}
func (*modelExtensionEchoTool) ExecutionMode() agent.ToolExecutionMode {
	return agent.ToolModeSequential
}
func (tool *modelExtensionEchoTool) Execute(_ context.Context, _ string, params json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	tool.calls++
	if tool.blocked {
		panic("tool should have been blocked")
	}
	var input struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return agent.AgentToolResult{}, err
	}
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: input.Text}}, Details: map[string]any{"text": input.Text}, Usage: tool.usage}, nil
}
func modelExtensionToolCall([]ai.Message) *ai.AssistantMessage {
	return &ai.AssistantMessage{API: ai.APIOpenAICompletions, Provider: "faux", Model: "faux-1", Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "echo-call", Name: "echo", Arguments: ai.JsonObject{"text": "hello"}}}, StopReason: ai.StopReasonToolUse}
}
func modelExtensionEchoResult(messages []ai.Message) *ai.AssistantMessage {
	var text string
	for _, message := range messages {
		if result, ok := message.(ai.ToolResultMessage); ok {
			for _, block := range result.Content {
				if part, ok := block.(ai.TextContent); ok {
					text += part.Text
				}
			}
		}
	}
	return fauxReply(text, ai.StopReasonStop, 0)(messages)
}
func modelExtensionAssistantTexts(messages []agent.AgentMessage) []string {
	var values []string
	for _, message := range messages {
		if message.Assistant != nil {
			var text string
			for _, block := range message.Assistant.Content {
				if part, ok := block.(ai.TextContent); ok {
					text += part.Text
				}
			}
			values = append(values, text)
		}
	}
	return values
}
func modelExtensionUserText(messages []ai.Message) string {
	for _, message := range messages {
		if user, ok := message.(ai.UserMessage); ok {
			switch content := user.Content.(type) {
			case ai.UserText:
				return string(content)
			case ai.UserContentBlocks:
				var text string
				for _, block := range content {
					if part, ok := block.(ai.TextContent); ok {
						text += part.Text
					}
				}
				return text
			}
		}
	}
	return ""
}

// Pi: packages/coding-agent/src/core/extensions/types.ts:1449 (ToolResultEventResult.usage).
func TestSessionModelExtensionHooksUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-model-extension.test.ts:244
	t.Run("allows extension tool_call handlers to block tool execution", func(t *testing.T) {
		tool := &modelExtensionEchoTool{blocked: true}
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"tool_call": {func(...any) (any, error) {
			return &extension.ToolCallEventResult{Block: true, Reason: "Blocked by test"}, nil
		}}}}
		h := newModelExtensionHarness(t, []bool{false}, "", true, ext, []agent.AgentTool{tool}, modelExtensionToolCall, modelExtensionEchoResult)
		messages, err := h.session.Send(t.Context(), "hi")
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, message := range messages {
			if message.ToolResult != nil && message.ToolResult.IsError {
				found = true
			}
		}
		if tool.calls != 0 || !found || !strings.Contains(strings.Join(modelExtensionAssistantTexts(messages), "\n"), "Blocked by test") {
			t.Fatalf("calls=%d messages=%+v", tool.calls, messages)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-model-extension.test.ts:286
	t.Run("allows extension tool_result handlers to modify tool results", func(t *testing.T) {
		original := &ai.Usage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, TotalTokens: 10, Cost: ai.UsageCost{Input: 0.1, Output: 0.2, CacheRead: 0.3, CacheWrite: 0.4, Total: 1}}
		patched := &ai.Usage{Input: 5, Output: 6, CacheRead: 7, CacheWrite: 8, TotalTokens: 26, Cost: ai.UsageCost{Input: 0.5, Output: 0.6, CacheRead: 0.7, CacheWrite: 0.8, Total: 2.6}}
		var observed any
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"tool_result": {func(args ...any) (any, error) {
			event := args[0].(extension.CustomToolResultEvent)
			observed = event.Usage
			return &extension.ToolResultEventResult{Content: []any{ai.TextContent{Text: "patched result"}}, Details: map[string]any{"patched": true}, Usage: patched}, nil
		}}}}
		h := newModelExtensionHarness(t, []bool{false}, "", true, ext, []agent.AgentTool{&modelExtensionEchoTool{usage: original}}, modelExtensionToolCall, modelExtensionEchoResult)
		messages, err := h.session.Send(t.Context(), "hi")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(observed, original) || !strings.Contains(strings.Join(modelExtensionAssistantTexts(messages), "\n"), "patched result") {
			t.Fatalf("observed=%+v messages=%+v", observed, messages)
		}
		var result *agent.ToolResultMessage
		for _, message := range messages {
			if message.ToolResult != nil {
				if details, ok := message.ToolResult.Details.(map[string]any); ok && details["patched"] == true {
					result = message.ToolResult
				}
			}
		}
		if result == nil || !reflect.DeepEqual(result.Usage, patched) {
			t.Fatalf("tool result=%+v", result)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-model-extension.test.ts:361
	t.Run("allows extension context handlers to modify messages before the LLM call", func(t *testing.T) {
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"context": {func(args ...any) (any, error) {
			event := args[0].(extension.ContextEvent)
			out := append([]extension.AgentMessage(nil), event.Messages...)
			for i, raw := range out {
				message := raw
				if message.User != nil {
					copy := *message.User
					copy.Content = ai.UserContentBlocks{ai.TextContent{Text: "rewritten"}}
					message.User = &copy
					out[i] = message
				}
			}
			return &extension.ContextEventResult{Messages: out}, nil
		}}}}
		var observed string
		h := newModelExtensionHarness(t, []bool{false}, "", true, ext, nil, func(messages []ai.Message) *ai.AssistantMessage {
			observed = modelExtensionUserText(messages)
			return fauxReply("done", ai.StopReasonStop, 0)(messages)
		})
		messages, err := h.session.Send(t.Context(), "original")
		if err != nil {
			t.Fatal(err)
		}
		if observed != "rewritten" {
			t.Fatalf("provider user=%q", observed)
		}
		found := false
		for _, message := range messages {
			if message.User != nil {
				found = true
				if !reflect.DeepEqual(message.User.Content, ai.UserContentBlocks{ai.TextContent{Text: "original"}}) {
					t.Fatalf("stored user=%+v", message.User)
				}
			}
		}
		if !found {
			t.Fatal("stored user missing")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-model-extension.test.ts:401
	t.Run("allows extension input handlers to transform or handle input", func(t *testing.T) {
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"input": {func(args ...any) (any, error) {
			event := args[0].(extension.InputEvent)
			if event.Text == "ping" {
				return extension.InputEventResultHandled{}, nil
			}
			return extension.InputEventResultTransform{Text: "transformed:" + event.Text}, nil
		}}}}
		var observed string
		h := newModelExtensionHarness(t, []bool{false}, "", true, ext, nil, func(messages []ai.Message) *ai.AssistantMessage {
			observed = modelExtensionUserText(messages)
			return fauxReply("done", ai.StopReasonStop, 0)(messages)
		}, fauxReply("unexpected", ai.StopReasonStop, 0))
		if err := h.session.Prompt(t.Context(), "hello"); err != nil {
			t.Fatal(err)
		}
		if err := h.session.Prompt(t.Context(), "ping"); err != nil {
			t.Fatal(err)
		}
		users := 0
		for _, message := range h.session.Messages() {
			if message.User != nil {
				users++
			}
		}
		if observed != "transformed:hello" || users != 1 {
			t.Fatalf("provider user=%q stored users=%d", observed, users)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-model-extension.test.ts:440
	t.Run("allows extension commands to inspect live system prompt options", func(t *testing.T) {
		var seen []*extension.BuildSystemPromptOptions
		ext := extension.Extension{Commands: map[string]extension.RegisteredCommand{"inspect-options": {Name: "inspect-options", Description: "Inspect system prompt options", Handler: func(ctx context.Context, _ string) error {
			options, err := extension.CommandContextFromContext(ctx).GetSystemPromptOptions()
			if err != nil {
				return err
			}
			seen = append(seen, options)
			options.SelectedTools = append(options.SelectedTools, "mutated_tool")
			return nil
		}}}}
		h := newModelExtensionHarness(t, []bool{false}, "", true, ext, nil)
		for range 2 {
			if err := h.session.Prompt(t.Context(), "/inspect-options"); err != nil {
				t.Fatal(err)
			}
		}
		if len(seen) != 2 || seen[0] != seen[1] || seen[0].Cwd != h.services.CWD() {
			t.Fatalf("options=%+v", seen)
		}
		if !slices.Contains(seen[0].SelectedTools, "read") || !slices.Contains(seen[1].SelectedTools, "mutated_tool") {
			t.Fatalf("tools=%v", seen[1].SelectedTools)
		}
	})

	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-model-extension.test.ts:468
	t.Run("allows before_agent_start handlers to inject custom messages and modify the system prompt", func(t *testing.T) {
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
			event := args[0].(extension.BeforeAgentStartEvent)
			return &extension.BeforeAgentStartEventResult{Message: &extension.CustomMessageRef{CustomType: "before-start", Content: "injected", Display: true, Details: map[string]any{"injected": true}}, SystemPrompt: new(event.SystemPrompt + "\n\nextra instructions")}, nil
		}}}}
		var prompt string
		injected := false
		h := newModelExtensionHarness(t, []bool{false}, "", true, ext, nil, func(messages []ai.Message) *ai.AssistantMessage {
			prompt = ai.GetCurrentSystemPrompt(messages)
			for _, message := range messages {
				if user, ok := message.(ai.UserMessage); ok {
					if blocks, ok := user.Content.(ai.UserContentBlocks); ok {
						for _, block := range blocks {
							if part, ok := block.(ai.TextContent); ok && part.Text == "injected" {
								injected = true
							}
						}
					}
				}
			}
			return fauxReply("done", ai.StopReasonStop, 0)(messages)
		})
		if _, err := h.session.Send(t.Context(), "hello"); err != nil {
			t.Fatal(err)
		}
		stored := false
		for _, message := range h.session.Messages() {
			if message.Custom != nil && message.Custom["customType"] == "before-start" {
				stored = true
			}
		}
		if !strings.Contains(prompt, "extra instructions") || !injected || !stored {
			t.Fatalf("prompt=%q injected=%v stored=%v", prompt, injected, stored)
		}
	})
}
