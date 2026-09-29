package ai

import (
	"reflect"
	"strings"
	"testing"
)

func TestAssistantMessageFrameProjectsToolScratchWithoutJSONRoundTrip(t *testing.T) {
	// upstream: packages/ai/src/utils/assistant-message-frame.ts:67-77 cloneToolCall whitelists public fields.
	// upstream: packages/ai/test/assistant-message-frame.test.ts:475-524 whitelists provider-shaped partials.
	for _, tc := range []struct {
		name    string
		scratch toolCallScratch
	}{
		{"completions-function", toolCallScratch{hasPartialArgs: true, partialArgs: `{"input":"start`, hasStreamIndex: true, streamIndex: 0}},
		{"responses-function", toolCallScratch{hasPartialJson: true, partialJson: `{"input":"start`}},
		{"completions-grammar", toolCallScratch{hasStreamIndex: true, streamIndex: 3, customInput: true, property: "input", jsonBuffer: grammarToolInputJSONBuffer{Input: "start", Started: true}}},
		{"responses-grammar", toolCallScratch{customInput: true, property: "input", jsonBuffer: grammarToolInputJSONBuffer{Input: "start", Started: true, Closed: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			partial := frameSeed()
			encoder := &AssistantMessageFrameEncoder{}
			start := mustFrame(t, encoder, StartEvent{Partial: partial})
			tool := ToolCall{ID: "call", Name: "run", Arguments: JsonObject{"nested": map[string]any{"value": "start"}, "customInput": "user argument"}, ThoughtSignature: "thought", Namespace: "tools", scratch: tc.scratch}
			partial.Content = append(partial.Content, tool)
			toolStart := mustFrame(t, encoder, ToolCallStartEvent{ContentIndex: 0, Partial: partial}).(ToolCallStartFrame)
			want := ToolCall{ID: "call", Name: "run", Arguments: JsonObject{"nested": map[string]any{"value": "start"}, "customInput": "user argument"}, ThoughtSignature: "thought", Namespace: "tools"}
			// Compare concrete values before serialization: private scratch must not escape into the frame.
			if !reflect.DeepEqual(toolStart.ToolCall, want) {
				t.Errorf("projected tool = %#v; want %#v", toolStart.ToolCall, want)
			}
			if partial.Content[0].(ToolCall).scratch != tc.scratch {
				t.Error("projection changed the provider scratch")
			}
			tool.Arguments["nested"].(map[string]any)["value"] = "provider-mutated"
			tool.scratch = toolCallScratch{}
			partial.Content[0] = tool
			if !reflect.DeepEqual(toolStart.ToolCall, want) {
				t.Error("provider mutation changed the start frame")
			}
			unfinished := mustReduce(t, []AssistantMessageFrame{start, toolStart})
			if !reflect.DeepEqual(unfinished.Content[0], want) {
				t.Errorf("unfinished reduced tool = %#v; want %#v", unfinished.Content[0], want)
			}
			unfinished.Content[0].(ToolCall).Arguments["nested"].(map[string]any)["value"] = "reducer-mutated"
			if !reflect.DeepEqual(toolStart.ToolCall, want) {
				t.Error("reduction aliased the start frame")
			}

			final := ToolCall{ID: "final-call", Name: "final-name", Arguments: JsonObject{"nested": map[string]any{"value": "final"}}, scratch: tc.scratch}
			toolEnd := mustFrame(t, encoder, ToolCallEndEvent{ContentIndex: 0, ToolCall: final, Partial: partial})
			final.Arguments["nested"].(map[string]any)["value"] = "provider-mutated"
			frames := []AssistantMessageFrame{start, toolStart, toolEnd}
			reduced := mustReduce(t, frames)
			wantFinal := ToolCall{ID: "final-call", Name: "final-name", Arguments: JsonObject{"nested": map[string]any{"value": "final"}}}
			if !reflect.DeepEqual(reduced.Content[0], wantFinal) {
				t.Errorf("final reduced tool = %#v; want %#v", reduced.Content[0], wantFinal)
			}
			reduced.Content[0].(ToolCall).Arguments["nested"].(map[string]any)["value"] = "reducer-mutated"
			if !reflect.DeepEqual(mustReduce(t, frames).Content[0], wantFinal) {
				t.Error("reduction aliased the end frame")
			}
		})
	}
}

func TestAssistantMessageFrameProjectsProviderToolLifecycles(t *testing.T) {
	// upstream: packages/ai/src/utils/assistant-message-frame.ts:244-258,290-312
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses} {
		for _, custom := range []bool{false, true} {
			name := string(api) + "/function"
			if custom {
				name = string(api) + "/grammar"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				var frames string
				if api == APIOpenAICompletions {
					tool := `[{"id":"call","index":0,"function":{"name":"run","arguments":"{\"input\":\"final\"}"}}]`
					if custom {
						tool = `[{"id":"call","index":0,"custom":{"name":"run","input":"final"}}]`
					}
					frames = completionToolFrame(tool, `"tool_calls"`) + "data: [DONE]\n\n"
				} else {
					item := `"output_index":0,"item":{"type":"function_call","id":"item","call_id":"call","name":"run","arguments":""}`
					end := `"output_index":0,"item":{"type":"function_call","arguments":"{\"input\":\"final\"}"}`
					if custom {
						item = `"output_index":0,"item":{"type":"custom_tool_call","id":"item","call_id":"call","name":"run","input":""}`
						end = `"output_index":0,"item":{"type":"custom_tool_call","input":"final"}`
					}
					frames = responseScratchFrame("output_item.added", item) + responseScratchFrame("output_item.done", end) + responseScratchFrame("completed", `"response":{"status":"completed"}`)
				}
				builder := newAssistantStreamBuilder(t.Context(), api, "scratch-provider", "scratch-model")
				if api == APIOpenAICompletions {
					(&openAIProvider{}).parseSSE(t.Context(), strings.NewReader(frames), builder, nil)
				} else {
					(&openAIResponsesProvider{}).parseResponsesSSE(t.Context(), strings.NewReader(frames), builder, nil)
				}
				encoded := encodeAll(t, collectBuilderEvents(builder.stream))
				for _, frame := range encoded {
					if start, ok := frame.(ToolCallStartFrame); ok && start.ToolCall.scratch != (toolCallScratch{}) {
						t.Errorf("start frame leaked scratch: %#v", start.ToolCall)
					}
				}
				assertJSONEqual(t, mustReduce(t, encoded).Content, builder.stream.Result().Content)
			})
		}
	}
}

func BenchmarkAssistantMessageFrameToolProjection(b *testing.B) {
	message := &AssistantMessage{Content: []AssistantContentBlock{ToolCall{ID: "call", Name: "run", Arguments: JsonObject{"nested": map[string]any{"input": strings.Repeat("fragment", 64)}}, scratch: toolCallScratch{hasPartialJson: true, partialJson: `{"input":"fragment`}}}}
	tool := message.Content[0].(ToolCall)
	b.ReportAllocs()
	for b.Loop() {
		encoder := &AssistantMessageFrameEncoder{}
		var frames []AssistantMessageFrame
		for _, event := range []AssistantMessageEvent{StartEvent{Partial: message}, ToolCallStartEvent{ContentIndex: 0, Partial: message}, ToolCallEndEvent{ContentIndex: 0, ToolCall: tool, Partial: message}} {
			frame, err := encoder.Encode(event)
			if err != nil {
				b.Fatal(err)
			}
			frames = append(frames, frame)
		}
		if _, err := ReduceAssistantMessageFrames(frames); err != nil {
			b.Fatal(err)
		}
	}
}
