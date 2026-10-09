package ai

import (
	"fmt"
	"testing"
)

type interleavedModelCase struct{ name, provider, id string }

func interleavedThinkingCases() []interleavedModelCase {
	return []interleavedModelCase{
		// .upstream/v0.87.1/packages/ai/test/interleaved-thinking.test.ts:123
		{"Amazon Bedrock/should do interleaved thinking on Claude Opus 4.5", "amazon-bedrock", "global.anthropic.claude-opus-4-5-20251101-v1:0"},
		// .upstream/v0.87.1/packages/ai/test/interleaved-thinking.test.ts:128
		{"Amazon Bedrock/should do interleaved thinking on Claude Opus 4.6", "amazon-bedrock", "global.anthropic.claude-opus-4-6-v1"},
		// .upstream/v0.87.1/packages/ai/test/interleaved-thinking.test.ts:135
		{"Anthropic/should do interleaved thinking on Claude Opus 4.5", "anthropic", "claude-opus-4-5"},
		// .upstream/v0.87.1/packages/ai/test/interleaved-thinking.test.ts:140
		{"Anthropic/should do interleaved thinking on Claude Opus 4.6", "anthropic", "claude-opus-4-6"},
	}
}

func assertInterleavedThinking(t *testing.T, p Provider) {
	t.Helper()
	defer func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	}()
	request := Context{
		SystemPrompt: "You are a helpful assistant that must use tools for arithmetic. Always think before every tool call, not just the first one. Do not answer with plain text when a tool call is required.",
		Messages:     []Message{UserMessage{Content: UserText("Use calculator to calculate 328 * 29. You must call the calculator tool exactly once. Provide the final answer based on the best guess given the tool result, even if it seems unreliable. Start by thinking about the steps you will take to solve the problem.")}},
		Tools:        []ToolSchema{{Name: "calculator", Description: "Perform basic arithmetic operations", Parameters: map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "number", "description": "First number"}, "b": map[string]any{"type": "number", "description": "Second number"}, "operation": map[string]any{"type": "string", "enum": []string{"add", "subtract", "multiply", "divide"}, "description": "The operation to perform."}}, "required": []string{"a", "b", "operation"}}}},
	}
	complete := func() *AssistantMessage {
		t.Helper()
		stream, err := p.Stream(t.Context(), NormalizeContext(request), StreamOptions{Thinking: ThinkingLevelHigh, IsReasoning: true})
		if err != nil {
			t.Fatal(err)
		}
		return stream.Result()
	}
	first := complete()
	if first.StopReason != StopReasonToolUse {
		t.Fatalf("first stop=%s error=%s", first.StopReason, first.ErrorMessage)
	}
	var call *ToolCall
	thinking := false
	for _, block := range first.Content {
		switch block := block.(type) {
		case ThinkingContent:
			thinking = true
		case ToolCall:
			if call == nil {
				call = &block
			}
		}
	}
	if !thinking || call == nil {
		t.Fatalf("first content=%+v", first.Content)
	}
	a, aOK := call.Arguments["a"].(float64)
	b, bOK := call.Arguments["b"].(float64)
	op, opOK := call.Arguments["operation"].(string)
	if !aOK || !bOK || !opOK {
		t.Fatalf("invalid calculator arguments=%v", call.Arguments)
	}
	var answer float64
	switch op {
	case "add":
		answer = a + b
	case "subtract":
		answer = a - b
	case "multiply":
		answer = a * b
	case "divide":
		answer = a / b
	default:
		t.Fatalf("invalid operation=%s", op)
	}
	request.Messages = append(request.Messages, *first, ToolResultMessage{ToolCallID: call.ID, ToolName: call.Name, Content: []ToolResultMessageContent{TextContent{Text: fmt.Sprintf("The answer is %g or %g.", answer, answer*2)}}, IsError: false})
	second := complete()
	if second.StopReason != StopReasonStop {
		t.Fatalf("second stop=%s error=%s", second.StopReason, second.ErrorMessage)
	}
	thinking, text := false, false
	for _, block := range second.Content {
		switch block.(type) {
		case ThinkingContent:
			thinking = true
		case TextContent:
			text = true
		}
	}
	if !thinking || !text {
		t.Fatalf("second content=%+v", second.Content)
	}
}

func TestInterleavedThinkingFauxUpstream(t *testing.T) {
	for _, tc := range interleavedThinkingCases() {
		t.Run(tc.name, func(t *testing.T) {
			p := NewFauxProvider(FauxConfig{ProviderID: tc.provider, Model: tc.id})
			p.SetResponses([]FauxResponseStep{
				FauxStaticStep(FauxResponse{StopReason: "toolUse", Content: []FauxContentBlock{FauxThinking("I will multiply with calculator."), FauxToolCall("calculator", map[string]any{"a": 328.0, "b": 29.0, "operation": "multiply"}, &FauxToolCallOptions{ID: "calculator-1"})}}),
				FauxFactoryStep(func(transcript TranscriptContext, opts StreamOptions, state *FauxProviderState, _ *Model) (AssistantMessage, error) {
					count := state.CallCount()
					if opts.Thinking != ThinkingLevelHigh || count != 2 {
						t.Errorf("call=%d thinking=%s", count, opts.Thinking)
					}
					messages := transcript.Messages()
					tool, ok := messages[len(messages)-1].(ToolResultMessage)
					if !ok || tool.ToolCallID != "calculator-1" || tool.ToolName != "calculator" || tool.IsError || len(tool.Content) != 1 || tool.Content[0] != (TextContent{Text: "The answer is 9512 or 19024."}) {
						t.Errorf("tool result=%+v", messages[len(messages)-1])
					}
					return FauxResponse{StopReason: "stop", Content: []FauxContentBlock{FauxThinking("The first answer is the product."), FauxText("9512")}}.AssistantMessage(), nil
				}),
			})
			assertInterleavedThinking(t, p)
		})
	}
}
