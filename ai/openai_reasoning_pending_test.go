package ai

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func runOpenAICompletionsSSEForProvider(t *testing.T, providerID, sse string) *AssistantMessage {
	t.Helper()
	provider := &openAIProvider{cfg: OpenAIConfig{ProviderID: providerID}}
	builder := newAssistantStreamBuilder(context.Background(), APIOpenAICompletions, providerID, "model")
	provider.parseSSE(context.Background(), strings.NewReader(sse), builder, nil)
	return builder.stream.Result()
}

func runOpenAICompletionsSSE(t *testing.T, sse string) *AssistantMessage {
	t.Helper()
	return runOpenAICompletionsSSEForProvider(t, "openai", sse)
}

func TestParseSSE_ReasoningFieldSignatureReplaysOnLaterTurns(t *testing.T) {
	tests := []struct {
		name       string
		providerID string
		field      string
	}{
		{name: "reasoning_content", providerID: "deepseek", field: "reasoning_content"},
		{name: "opencode_go_reasoning_remap", providerID: "opencode-go", field: "reasoning"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sse := `data: {"choices":[{"delta":{"` + test.field + `":"think"},"finish_reason":"stop"}]}` + "\n\n" +
				"data: [DONE]\n"
			message := runOpenAICompletionsSSEForProvider(t, test.providerID, sse)
			thinking, ok := message.Content[0].(ThinkingContent)
			if !ok {
				t.Fatalf("content[0] = %T, want ThinkingContent", message.Content[0])
			}
			if thinking.ThinkingSignature != "reasoning_content" {
				t.Fatalf("thinking signature = %q, want reasoning_content", thinking.ThinkingSignature)
			}

			converted, err := convertMessages([]Message{*message}, false, nil)
			if err != nil {
				t.Fatalf("convertMessages: %v", err)
			}
			if len(converted) != 1 || converted[0].ReasoningContent == nil || *converted[0].ReasoningContent != "think" {
				t.Fatalf("replayed message = %#v, want reasoning_content=think", converted)
			}
		})
	}
}

func TestParseSSE_ReasoningDetailsBecomeThinkingSignatureAndReplay(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"reasoning\":\"signed thought\",\"reasoning_details\":[" +
		"{\"type\":\"reasoning.text\",\"text\":\"signed thought\",\"signature\":\"sig\"}," +
		"{\"type\":\"reasoning.encrypted\",\"id\":\"enc\",\"data\":\"ciphertext\"}," +
		"{\"type\":\"reasoning.summary\",\"summary\":\"summary\"}" +
		"]},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n"
	message := runOpenAICompletionsSSE(t, sse)
	thinking, ok := message.Content[0].(ThinkingContent)
	if !ok {
		t.Fatalf("content = %#v, want ThinkingContent", message.Content)
	}
	var details []map[string]any
	if err := json.Unmarshal([]byte(thinking.ThinkingSignature), &details); err != nil {
		t.Fatalf("thinking signature = %q: %v", thinking.ThinkingSignature, err)
	}
	if len(details) != 3 || details[0]["type"] != "reasoning.text" || details[1]["type"] != "reasoning.encrypted" || details[2]["type"] != "reasoning.summary" {
		t.Fatalf("reasoning details = %#v", details)
	}

	converted, err := convertMessages([]Message{*message}, false, nil)
	if err != nil {
		t.Fatalf("convertMessages: %v", err)
	}
	if len(converted) != 1 || len(converted[0].ReasoningDetails) != 3 || converted[0].Reasoning != "" {
		t.Fatalf("replayed message = %#v", converted)
	}
}

func TestParseSSE_ReasoningDetailDeltasMergeLikeUpstream(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"reasoning_details\":[{\"type\":\"reasoning.text\",\"text\":\"The\",\"index\":0}]},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning_details\":[{\"type\":\"reasoning.text\",\"text\":\" answer\",\"signature\":\"sig\",\"format\":\"v1\",\"index\":0}]},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning_details\":[{\"type\":\"reasoning.summary\",\"summary\":\"Short\",\"index\":1}]},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning_details\":[{\"type\":\"reasoning.summary\",\"summary\":\" summary\",\"format\":\"v1\",\"index\":1}]},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n"
	message := runOpenAICompletionsSSE(t, sse)
	if len(message.Content) != 1 {
		t.Fatalf("content = %#v, want one thinking block", message.Content)
	}
	thinking, ok := message.Content[0].(ThinkingContent)
	if !ok {
		t.Fatalf("content[0] = %T, want ThinkingContent", message.Content[0])
	}
	var details []map[string]any
	if err := json.Unmarshal([]byte(thinking.ThinkingSignature), &details); err != nil {
		t.Fatalf("thinking signature = %q: %v", thinking.ThinkingSignature, err)
	}
	if len(details) != 2 || details[0]["text"] != "The answer" || details[0]["signature"] != "sig" || details[0]["format"] != "v1" || details[1]["summary"] != "Short summary" || details[1]["format"] != "v1" {
		t.Fatalf("merged details = %#v", details)
	}
}

func TestParseSSE_ReasoningDetailsAfterToolCallStayOnOneThinkingBlock(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"reasoning\":\"plan\",\"reasoning_details\":[{\"type\":\"reasoning.text\",\"text\":\"plan\",\"index\":0}]},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{}\"}}],\"reasoning_details\":[{\"type\":\"reasoning.encrypted\",\"id\":\"call_1\",\"data\":\"enc\",\"index\":1}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n"
	message := runOpenAICompletionsSSE(t, sse)
	if len(message.Content) != 2 {
		t.Fatalf("content = %#v, want one thinking block and one tool call", message.Content)
	}
	thinking, thinkingOK := message.Content[0].(ThinkingContent)
	tool, toolOK := message.Content[1].(ToolCall)
	if !thinkingOK || !toolOK || tool.ID != "call_1" {
		t.Fatalf("content = %#v, want [thinking, toolCall]", message.Content)
	}
	var details []map[string]any
	if err := json.Unmarshal([]byte(thinking.ThinkingSignature), &details); err != nil {
		t.Fatalf("thinking signature = %q: %v", thinking.ThinkingSignature, err)
	}
	if len(details) != 2 || details[0]["type"] != "reasoning.text" || details[1]["type"] != "reasoning.encrypted" {
		t.Fatalf("reasoning details = %#v", details)
	}

	converted, err := convertMessages([]Message{*message}, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(converted) != 1 || len(converted[0].ReasoningDetails) != 2 {
		t.Fatalf("replayed message = %#v", converted)
	}
}

func TestParseSSE_ReasoningDetailsAfterTextDoNotSplitText(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning_details\":[{\"type\":\"reasoning.encrypted\",\"id\":\"r\",\"data\":\"enc\"}]},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n"
	message := runOpenAICompletionsSSE(t, sse)
	if len(message.Content) != 2 {
		t.Fatalf("content = %#v, want one text block and one thinking block", message.Content)
	}
	text, textOK := message.Content[0].(TextContent)
	_, thinkingOK := message.Content[1].(ThinkingContent)
	if !textOK || text.Text != "Hello" || !thinkingOK {
		t.Fatalf("content = %#v, want [text Hello, thinking]", message.Content)
	}
}

func TestParseSSE_ToolCallsWithoutIndexRemainDistinct(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[" +
		"{\"id\":\"a\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"p\\\":1}\"}}," +
		"{\"id\":\"b\",\"type\":\"function\",\"function\":{\"name\":\"ls\",\"arguments\":\"{}\"}}" +
		"]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n"
	message := runOpenAICompletionsSSE(t, sse)
	if len(message.Content) != 2 {
		t.Fatalf("content = %#v, want two tool calls", message.Content)
	}
	first, firstOK := message.Content[0].(ToolCall)
	second, secondOK := message.Content[1].(ToolCall)
	if !firstOK || !secondOK {
		t.Fatalf("content = %#v, want two ToolCall blocks", message.Content)
	}
	if first.ID != "a" || first.Name != "read" || first.Arguments["p"] != float64(1) {
		t.Fatalf("first tool call = %#v", first)
	}
	if second.ID != "b" || second.Name != "ls" || len(second.Arguments) != 0 {
		t.Fatalf("second tool call = %#v", second)
	}
}

func TestParseSSE_ReasoningDetailBeforeToolCallUsesThinkingSignature(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"reasoning_details\":[{\"type\":\"reasoning.encrypted\",\"id\":\"call_sse\",\"data\":\"sse_encrypted_payload\"}]},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_sse\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n"
	message := runOpenAICompletionsSSE(t, sse)
	if len(message.Content) != 2 {
		t.Fatalf("content = %#v, want thinking and tool-call blocks", message.Content)
	}
	thinking, ok := message.Content[0].(ThinkingContent)
	if !ok {
		t.Fatalf("content[0] = %T, want ThinkingContent", message.Content[0])
	}
	var details []map[string]any
	if err := json.Unmarshal([]byte(thinking.ThinkingSignature), &details); err != nil {
		t.Fatal(err)
	}
	if len(details) != 1 || details[0]["type"] != "reasoning.encrypted" || details[0]["id"] != "call_sse" || details[0]["data"] != "sse_encrypted_payload" {
		t.Fatalf("reasoning details = %#v", details)
	}
	tool, ok := message.Content[1].(ToolCall)
	if !ok || tool.ThoughtSignature != "" {
		t.Fatalf("tool call = %#v, want replay metadata only on thinking block", message.Content[1])
	}
}

func TestParseSSE_EncryptedReasoningDetailRequiresData(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_nd\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{}\"}}]},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning_details\":[{\"type\":\"reasoning.encrypted\",\"id\":\"call_nd\"}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n"
	message := runOpenAICompletionsSSE(t, sse)
	tool := message.Content[0].(ToolCall)
	if tool.ThoughtSignature != "" {
		t.Fatalf("dataless encrypted detail was attached: %q", tool.ThoughtSignature)
	}
}
