package ai

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// grammarSchema is a single-required-string-property object schema usable as a
// grammar tool's parameters.
func grammarSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"expr": map[string]any{"type": "string"}},
		"required":   []any{"expr"},
	}
}

// TestResponsesConvertToolsGrammar pins the custom (grammar) tool wire shape
// byte-for-byte against the upstream oracle (convertResponsesTools). The custom
// tool carries no parameters map, so key ordering is not a factor.
func TestResponsesConvertToolsGrammar(t *testing.T) {
	p := &openAIResponsesProvider{}
	grammarTool := ToolSchema{
		Name: "calc", Description: "calculator", Parameters: grammarSchema(),
		ConstrainedSampling: &ConstrainedSamplingConfig{Type: "grammar", Variants: map[string]string{GrammarFormatOpenAILark: "start: NUMBER"}},
	}
	out, err := p.convertTools([]ToolSchema{grammarTool}, false, true)
	if err != nil {
		t.Fatalf("convertTools: %v", err)
	}
	got, _ := json.Marshal(out[0])
	const want = `{"type":"custom","name":"calc","description":"calculator","format":{"type":"grammar","syntax":"lark","definition":"start: NUMBER"}}`
	if string(got) != want {
		t.Fatalf("grammar tool bytes:\n got %s\nwant %s", got, want)
	}

	// Grammar unsupported → plain function tool, no strict, no format.
	fb, err := p.convertTools([]ToolSchema{grammarTool}, false, false)
	if err != nil {
		t.Fatalf("fallback: %v", err)
	}
	if fb[0].Type != "function" || fb[0].Format != nil || fb[0].Strict != nil {
		t.Fatalf("fallback = %+v, want plain function (no format, no strict)", fb[0])
	}
}

// TestResponsesConvertToolsStrict pins the json_schema strict path: strict:true
// when the model supports strict mode, error when strict is required but
// unsupported.
func TestResponsesConvertToolsStrict(t *testing.T) {
	p := &openAIResponsesProvider{}
	prefer := ToolSchema{Name: "lookup", Description: "look up", Parameters: grammarSchema(),
		ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"}}
	out, err := p.convertTools([]ToolSchema{prefer}, true, false)
	if err != nil {
		t.Fatalf("convertTools: %v", err)
	}
	if string(out[0].Strict) != "true" {
		t.Fatalf("strict = %s, want true", out[0].Strict)
	}
	if out[0].Parameters["additionalProperties"] != false || !slices.Contains(toStringSlice(out[0].Parameters["required"]), "expr") {
		t.Fatalf("strict parameters = %#v", out[0].Parameters)
	}
	// strict:"require" + unsupported strict mode → error (upstream throws).
	require := ToolSchema{Name: "lookup", Parameters: grammarSchema(),
		ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: "require"}}
	if _, err := p.convertTools([]ToolSchema{require}, false, false); err == nil ||
		!strings.Contains(err.Error(), "requires JSON-schema constrained sampling") {
		t.Fatalf("require+unsupported err = %v", err)
	}
}

// TestResponsesGrammarReplay pins convertMessages: a prior grammar tool call
// replays as custom_tool_call and its result as custom_tool_call_output, while a
// non-grammar tool keeps function_call/function_call_output.
func TestResponsesGrammarReplay(t *testing.T) {
	p := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{ProviderID: "openai"}}
	grammarProps := map[string]string{"calc": "expr"}
	messages := []Message{
		AssistantMessage{Content: []AssistantContentBlock{
			ToolCall{ID: "call_1|ctc_1", Name: "calc", Arguments: JsonObject{"expr": "1+2"}},
			ToolCall{ID: "call_2|fc_2", Name: "plain", Arguments: JsonObject{"q": "x"}},
		}},
		ToolResultMessage{ToolCallID: "call_1|ctc_1", ToolName: "calc", Content: []ToolResultMessageContent{TextContent{Text: "3"}}},
		ToolResultMessage{ToolCallID: "call_2|fc_2", ToolName: "plain", Content: []ToolResultMessageContent{TextContent{Text: "ok"}}},
	}
	items, err := p.convertMessages(messages, grammarProps)
	if err != nil {
		t.Fatalf("convertMessages: %v", err)
	}
	got := map[string]string{} // type keyed by name/callid for the assertions we care about
	var customCallInput string
	for _, it := range items {
		switch it.Type {
		case "custom_tool_call":
			got["callType:"+it.Name] = "custom_tool_call"
			customCallInput = it.Input
		case "function_call":
			got["callType:"+it.Name] = "function_call"
		case "custom_tool_call_output":
			got["resultType:"+it.CallID] = "custom_tool_call_output"
		case "function_call_output":
			got["resultType:"+it.CallID] = "function_call_output"
		}
	}
	if got["callType:calc"] != "custom_tool_call" {
		t.Errorf("calc call type = %q, want custom_tool_call", got["callType:calc"])
	}
	if customCallInput != "1+2" {
		t.Errorf("custom call input = %q, want 1+2", customCallInput)
	}
	if got["callType:plain"] != "function_call" {
		t.Errorf("plain call type = %q, want function_call", got["callType:plain"])
	}
	if got["resultType:call_1"] != "custom_tool_call_output" {
		t.Errorf("calc result type = %q, want custom_tool_call_output", got["resultType:call_1"])
	}
	if got["resultType:call_2"] != "function_call_output" {
		t.Errorf("plain result type = %q, want function_call_output", got["resultType:call_2"])
	}
}

// TestResponsesGrammarReplayBadInput pins the error path: a grammar tool call
// whose grammar argument is missing/non-string surfaces an error (upstream
// getGrammarToolInput throws).
func TestResponsesGrammarReplayBadInput(t *testing.T) {
	p := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{ProviderID: "openai"}}
	messages := []Message{AssistantMessage{Content: []AssistantContentBlock{
		ToolCall{ID: "call_1|ctc_1", Name: "calc", Arguments: JsonObject{"expr": 42}},
	}}}
	if _, err := p.convertMessages(messages, map[string]string{"calc": "expr"}); err == nil ||
		!strings.Contains(err.Error(), "to be a string") {
		t.Fatalf("bad grammar input err = %v", err)
	}
}

// TestResponsesGrammarStreaming drives the custom_tool_call SSE path and proves
// the reconstructed toolcall_delta stream concatenates to valid JSON arguments.
func TestResponsesGrammarStreaming(t *testing.T) {
	p := &openAIResponsesProvider{}
	sse := strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"custom_tool_call","id":"ctc_1","call_id":"call_1","name":"calc","input":""}}`,
		`data: {"type":"response.custom_tool_call_input.delta","output_index":0,"delta":"1+"}`,
		`data: {"type":"response.custom_tool_call_input.delta","output_index":0,"delta":"2"}`,
		`data: {"type":"response.custom_tool_call_input.done","output_index":0,"input":"1+2"}`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"custom_tool_call","id":"ctc_1","call_id":"call_1","name":"calc","input":"1+2"}}`,
		`data: {"type":"response.completed","response":{"status":"completed"}}`,
		"",
	}, "\n\n")

	builder := newAssistantStreamBuilder(context.Background(), APIOpenAIResponses, "openai", "model")
	p.parseResponsesSSE(context.Background(), strings.NewReader(sse), builder, map[string]string{"calc": "expr"})
	message := builder.stream.Result()
	if message.StopReason != StopReasonToolUse || len(message.Content) != 1 {
		t.Fatalf("message = %#v", message)
	}
	tool, ok := message.Content[0].(ToolCall)
	if !ok || tool.ID != "call_1|ctc_1" || tool.Name != "calc" || !reflect.DeepEqual(tool.Arguments, JsonObject{"expr": "1+2"}) {
		t.Fatalf("tool call = %#v", message.Content[0])
	}
}
