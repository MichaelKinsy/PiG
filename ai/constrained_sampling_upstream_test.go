package ai

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func BenchmarkToolCallPartialArguments(b *testing.B) {
	for _, size := range []struct {
		name  string
		bytes int
	}{{"4KiB", 4 * 1024}, {"64KiB", 64 * 1024}} {
		b.Run(size.name, func(b *testing.B) {
			payload := strings.Repeat("x", size.bytes)
			arguments := `{"payload":"` + payload + `"}`
			b.ReportAllocs()
			b.SetBytes(int64(len(arguments)))
			for b.Loop() {
				builder := newAssistantStreamBuilder(b.Context(), APIOpenAIResponses, "openai", "test")
				for start := 0; start < len(arguments); start += 256 {
					builder.toolCallDelta(streamToolCallDelta{index: 0, id: "call", name: "tool", argumentsDelta: arguments[start:min(start+256, len(arguments))]})
				}
				builder.done(StopReasonToolUse, nil, "")
				for range builder.stream.Events(b.Context()) {
				}
				if got := builder.stream.Result().Content[0].(ToolCall).Arguments["payload"]; got != payload {
					b.Fatal("lost tool arguments")
				}
			}
		})
	}
}

// .upstream/v0.87.1/packages/ai/test/constrained-sampling.test.ts:90 — converts supported constraints and falls back when unsupported.
func TestConstrainedSamplingConversionUpstream(t *testing.T) {
	provider := &openAIResponsesProvider{}
	tool := sampleGrammarTool(&ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"})
	tools, err := provider.convertTools([]ToolSchema{tool}, true, false)
	if err != nil || tools[0].Type != "function" || tools[0].Name != "sample_tool" || string(tools[0].Strict) != "true" {
		t.Fatalf("strict=%#v error=%v", tools, err)
	}
	tool.ConstrainedSampling.Strict = "require"
	if _, err := provider.convertTools([]ToolSchema{tool}, false, false); err == nil || !strings.Contains(err.Error(), `Tool "sample_tool" requires JSON-schema constrained sampling`) {
		t.Fatalf("require=%v", err)
	}
	grammar := sampleGrammarTool(&ConstrainedSamplingConfig{Type: "grammar", Variants: map[string]string{"openai_lark": "start: /[a-z]+/"}})
	tools, err = provider.convertTools([]ToolSchema{grammar}, true, true)
	if err != nil || tools[0].Type != "custom" || tools[0].Name != "sample_tool" || !reflect.DeepEqual(tools[0].Format, &respToolFormat{Type: "grammar", Syntax: "lark", Definition: "start: /[a-z]+/"}) {
		t.Fatalf("grammar=%#v error=%v", tools, err)
	}
	missing := sampleGrammarTool(&ConstrainedSamplingConfig{Type: "grammar", Variants: map[string]string{}})
	if _, err := provider.convertTools([]ToolSchema{missing}, true, true); err == nil || !strings.Contains(err.Error(), `Tool "sample_tool" cannot use grammar constrained sampling: no supported grammar variant was provided`) {
		t.Fatalf("missing grammar=%v", err)
	}
	tools, err = provider.convertTools([]ToolSchema{grammar}, false, false)
	if err != nil || tools[0].Type != "function" || tools[0].Name != "sample_tool" || tools[0].Strict != nil {
		t.Fatalf("fallback=%#v error=%v", tools, err)
	}
	// types.go represents both upstream false and omitted constrainedSampling by nil; neither requests a sampling constraint.
	tools, err = provider.convertTools([]ToolSchema{sampleGrammarTool(nil)}, true, false)
	if err != nil || string(tools[0].Strict) != "false" {
		t.Fatalf("unconstrained=%#v error=%v", tools, err)
	}
}

// .upstream/v0.87.1/packages/ai/test/constrained-sampling.test.ts:156 — all four unsupported-schema rows, including wire fallback.
func TestConstrainedSamplingUnsupportedSchemasUpstream(t *testing.T) {
	for _, tc := range []struct{ schema, message string }{
		{`{"type":"object","properties":{"metadata":{"type":"object","properties":{},"additionalProperties":{"type":"string"}}},"required":["metadata"]}`, "additionalProperties is unsupported"},
		{`{"type":"object","allOf":[{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]},{"type":"object","properties":{"b":{"type":"number"}},"required":["b"]}]}`, "allOf schemas are unsupported"},
		{`{"type":"object","properties":{"value":{"anyOf":[{"type":"object","properties":{"nested":{"type":"string"}},"required":["nested"]},{"type":"null"}]}},"required":["value"]}`, "object and array unions are unsupported"},
		{`{"type":"object","properties":{"child":{"$ref":"https://example.com/child.json"}},"required":["child"]}`, "$ref schemas are unsupported"},
	} {
		t.Run(tc.message, func(t *testing.T) {
			var parameters map[string]any
			if err := json.Unmarshal([]byte(tc.schema), &parameters); err != nil {
				t.Fatal(err)
			}
			tool := sampleGrammarTool(&ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"})
			tool.Parameters = parameters
			if _, err := makeStrictJSONSchema(parameters); err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("strict schema=%v", err)
			}
			strict, err := resolveJSONSchemaStrictSampling(tool, true)
			if err != nil || strict != nil {
				t.Fatalf("prefer=%v/%v", strict, err)
			}
			provider := &openAIResponsesProvider{}
			converted, err := provider.convertTools([]ToolSchema{tool}, true, false)
			if err != nil || string(converted[0].Strict) != "false" || !reflect.DeepEqual(converted[0].Parameters, parameters) {
				t.Fatalf("fallback=%#v error=%v", converted, err)
			}
			tool.ConstrainedSampling.Strict = "require"
			if _, err := resolveJSONSchemaStrictSampling(tool, true); err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("require=%v", err)
			}
		})
	}
}

// .upstream/v0.87.1/packages/ai/test/constrained-sampling.test.ts:201 — replays grammar calls as custom Responses items.
func TestConstrainedSamplingGrammarReplayUpstream(t *testing.T) {
	provider := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{ProviderID: "openai", Model: "gpt-test"}}
	for _, arguments := range []JsonObject{{}, {"payload": 42}, {"payload": "abc"}} {
		messages := []Message{AssistantMessage{API: APIOpenAIResponses, Provider: "openai", Model: "gpt-test", StopReason: StopReasonToolUse, Content: []AssistantContentBlock{ToolCall{ID: "call_1|ctc_1", Name: "sample_tool", Arguments: arguments}}}, ToolResultMessage{ToolCallID: "call_1|ctc_1", ToolName: "sample_tool", Content: []ToolResultMessageContent{TextContent{Text: "done"}}}}
		items, err := provider.convertMessages(messages, map[string]string{"sample_tool": "payload"})
		if arguments["payload"] != "abc" {
			if err == nil || !strings.Contains(err.Error(), `Grammar tool call "sample_tool" requires argument "payload" to be a string`) {
				t.Fatalf("arguments=%#v error=%v", arguments, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		var got []map[string]any
		raw, err := json.Marshal(items)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		want := []map[string]any{{"type": "custom_tool_call", "id": "ctc_1", "call_id": "call_1", "name": "sample_tool", "input": "abc"}, {"type": "custom_tool_call_output", "call_id": "call_1", "output": "done"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("replay=%s", raw)
		}
	}
}

// .upstream/v0.87.1/packages/ai/test/constrained-sampling.test.ts:271 — starts custom Responses tool calls with their initial input.
func TestConstrainedSamplingInitialInputUpstream(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"custom_tool_call","call_id":"call_1","id":"ctc_1","name":"sample_tool","input":"a"}}`,
		`data: {"type":"response.custom_tool_call_input.delta","output_index":0,"item_id":"ctc_1","delta":"b"}`,
		`data: {"type":"response.custom_tool_call_input.done","output_index":0,"item_id":"ctc_1","input":"abc"}`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"custom_tool_call","call_id":"call_1","id":"ctc_1","name":"sample_tool","input":"abc"}}`,
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`, "",
	}, "\n\n")
	builder := newAssistantStreamBuilder(t.Context(), APIOpenAIResponses, "openai", "gpt-test")
	var events []AssistantMessageEvent
	var frames []string
	for frame := range strings.SplitSeq(body, "\n\n") {
		if frame != "" {
			frames = append(frames, frame+"\n\n")
		}
	}
	reader := &providerScratchReader{frames: frames, beforeRead: func(int) {
		events = append(events, drainObservedAssistantEvents(t, builder.stream)...)
	}}
	(&openAIResponsesProvider{}).parseResponsesSSE(t.Context(), reader, builder, map[string]string{"sample_tool": "payload"})
	for event := range builder.stream.Events(t.Context()) {
		events = append(events, mapAssistantEventPartial(event, (*AssistantMessage).Observe))
	}
	var starts []JsonObject
	var partials []JsonObject
	deltas := ""
	for _, event := range events {
		switch event := event.(type) {
		case ToolCallStartEvent:
			starts = append(starts, event.Partial.Content[event.ContentIndex].(ToolCall).Arguments)
		case ToolCallDeltaEvent:
			deltas += event.Delta
			partials = append(partials, event.Partial.Content[event.ContentIndex].(ToolCall).Arguments)
		}
	}
	if !reflect.DeepEqual(starts, []JsonObject{{"payload": "a"}}) {
		t.Fatalf("starts=%#v", starts)
	}
	if !reflect.DeepEqual(partials, []JsonObject{{"payload": "ab"}, {"payload": "abc"}}) {
		t.Fatalf("delta partials=%#v", partials)
	}
	result := builder.stream.Result()
	if result.StopReason != StopReasonToolUse || !reflect.DeepEqual(result.Content, []AssistantContentBlock{ToolCall{ID: "call_1|ctc_1", Name: "sample_tool", Arguments: JsonObject{"payload": "abc"}}}) {
		t.Fatalf("result=%#v", result)
	}
	var arguments JsonObject
	if err := json.Unmarshal([]byte(deltas), &arguments); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(arguments, JsonObject{"payload": "abc"}) {
		t.Fatalf("delta arguments=%#v", arguments)
	}
}
