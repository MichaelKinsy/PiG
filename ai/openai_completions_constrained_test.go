package ai

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// completionsGrammarSchema is a single-required-string-property object schema
// usable as a grammar tool's parameters (infers input property "expr").
func completionsGrammarSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"expr": map[string]any{"type": "string"}},
		"required":   []any{"expr"},
	}
}

func grammarTrueProvider() *openAIProvider {
	tr := true
	return &openAIProvider{cfg: OpenAIConfig{Compat: &OpenAICompat{SupportsOpenAIGrammarTools: &tr}}}
}

// TestCompletionsConvertToolsGrammar pins the completions custom (grammar) tool
// wire shape. Unlike responses, completions nests the grammar under
// custom.format.grammar (openai-completions.ts:1345-1358). The byte shape is the
// one the provider-wire grammar probe matches against the pi oracle.
func TestCompletionsConvertToolsGrammar(t *testing.T) {
	p := grammarTrueProvider()
	grammarTool := ToolSchema{
		Name: "calc", Description: "calculator", Parameters: completionsGrammarSchema(),
		ConstrainedSampling: &ConstrainedSamplingConfig{Type: "grammar", Variants: map[string]string{GrammarFormatOpenAILark: "start: NUMBER"}},
	}
	out, err := p.convertTools([]ToolSchema{grammarTool})
	if err != nil {
		t.Fatalf("convertTools: %v", err)
	}
	got, _ := json.Marshal(out[0])
	const want = `{"type":"custom","custom":{"name":"calc","description":"calculator","format":{"type":"grammar","grammar":{"syntax":"lark","definition":"start: NUMBER"}}}}`
	if string(got) != want {
		t.Fatalf("grammar tool bytes:\n got %s\nwant %s", got, want)
	}
}

// TestCompletionsConvertToolsGrammarFallback verifies a grammar tool degrades to
// a plain function tool when the provider does not advertise grammar support.
func TestCompletionsConvertToolsGrammarFallback(t *testing.T) {
	p := &openAIProvider{cfg: OpenAIConfig{}} // supportsOpenAIGrammarTools defaults false
	grammarTool := ToolSchema{
		Name: "calc", Description: "calculator", Parameters: completionsGrammarSchema(),
		ConstrainedSampling: &ConstrainedSamplingConfig{Type: "grammar", Variants: map[string]string{GrammarFormatOpenAILark: "start: NUMBER"}},
	}
	out, err := p.convertTools([]ToolSchema{grammarTool})
	if err != nil {
		t.Fatalf("convertTools: %v", err)
	}
	if out[0].Type != "function" || out[0].Custom != nil || out[0].Function == nil {
		t.Fatalf("fallback = %+v, want plain function tool", out[0])
	}
	// Upstream detectCompat defaults supportsStrictMode to false, so the fallback
	// function tool carries no strict field.
	if out[0].Function.Strict != nil {
		t.Fatalf("fallback strict = %v, want the field omitted", *out[0].Function.Strict)
	}
}

// TestCompletionsGrammarReplay pins convertMessages: a prior grammar tool call
// replays as a custom tool call whose input is the single grammar string pulled
// back out of the recorded arguments (openai-completions.ts:1189-1198).
func TestCompletionsGrammarReplay(t *testing.T) {
	grammarProps := map[string]string{"calc": "expr"}
	messages := []Message{AssistantMessage{Content: []AssistantContentBlock{
		ToolCall{ID: "call_1", Name: "calc", Arguments: JsonObject{"expr": "3+4"}},
	}}}
	out, err := convertMessages(messages, false, grammarProps)
	if err != nil {
		t.Fatalf("convertMessages: %v", err)
	}
	if len(out) != 1 || len(out[0].ToolCalls) != 1 {
		t.Fatalf("out = %+v, want one assistant message with one tool call", out)
	}
	tc := out[0].ToolCalls[0]
	if tc.Type != "custom" || tc.Function != nil || tc.Custom == nil {
		t.Fatalf("tool call = %+v, want custom variant", tc)
	}
	if tc.Custom.Name != "calc" || tc.Custom.Input != "3+4" {
		t.Fatalf("custom = %+v, want {calc 3+4}", *tc.Custom)
	}

	// Without a grammar property for the tool, replay stays a function tool call
	// carrying the JSON-serialized arguments.
	out2, err := convertMessages(messages, false, nil)
	if err != nil {
		t.Fatalf("convertMessages plain: %v", err)
	}
	tc2 := out2[0].ToolCalls[0]
	if tc2.Type != "function" || tc2.Custom != nil || tc2.Function == nil {
		t.Fatalf("plain tool call = %+v, want function variant", tc2)
	}
	if tc2.Function.Arguments != `{"expr":"3+4"}` {
		t.Fatalf("plain args = %s, want json arguments", tc2.Function.Arguments)
	}
}

// TestCompletionsGrammarReplayBadInput verifies a grammar tool call whose input
// argument is not a string surfaces an error (getGrammarToolInput), matching the
// responses path.
func TestCompletionsGrammarReplayBadInput(t *testing.T) {
	grammarProps := map[string]string{"calc": "expr"}
	messages := []Message{AssistantMessage{Content: []AssistantContentBlock{
		ToolCall{ID: "call_1", Name: "calc", Arguments: JsonObject{"expr": 42}},
	}}}
	_, err := convertMessages(messages, false, grammarProps)
	if err == nil || !strings.Contains(err.Error(), "to be a string") {
		t.Fatalf("bad grammar input err = %v, want string-required error", err)
	}
}

// TestCompletionsGrammarStreamReconstruction drives a streamed custom (grammar)
// tool call and verifies the raw input chunks are reconstructed into complete
// JSON arguments {"expr":"3+4"}, including the trailing framing appended when the
// stream terminates (openai-completions.ts appendCustomToolCallInput/finishBlock).
func TestCompletionsGrammarStreamReconstruction(t *testing.T) {
	provider := &openAIProvider{}
	builder := newAssistantStreamBuilder(context.Background(), APIOpenAICompletions, "openai", "model")
	sse := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_g\",\"type\":\"custom\",\"custom\":{\"name\":\"calc\",\"input\":\"3+\"}}]},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"custom\":{\"input\":\"4\"}}]},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n"
	provider.parseSSE(context.Background(), strings.NewReader(sse), builder, map[string]string{"calc": "expr"})
	message := builder.stream.Result()
	if message.StopReason != StopReasonToolUse || len(message.Content) != 1 {
		t.Fatalf("message = %#v", message)
	}
	tool, ok := message.Content[0].(ToolCall)
	if !ok || tool.ID != "call_g" || tool.Name != "calc" || !reflect.DeepEqual(tool.Arguments, JsonObject{"expr": "3+4"}) {
		t.Fatalf("tool call = %#v", message.Content[0])
	}
}
