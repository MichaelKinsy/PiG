package ai

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestOpenAICompletionsToolChoiceStreamUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, provider, id, prompt   string
		chunks                       []string
		stop                         StopReason
		message, responseID, content string
		total                        int
		noFinish                     bool
	}{
		// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:625
		{name: "maps non-standard provider finish_reason values to stopReason error", provider: "zai", id: "glm-5.2", prompt: "Hi", chunks: []string{`{"choices":[{"delta":{"content":"partial"},"finish_reason":null}]}`, `{"choices":[{"delta":{},"finish_reason":"network_error"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}}}`}, stop: StopReasonError, message: "Provider finish_reason: network_error"},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:660
		{name: "ignores null stream chunks from openai-compatible providers", provider: "openai", id: "gpt-4o-mini", prompt: "Reply with exactly OK", chunks: []string{`null`, `{"id":"chatcmpl-test","choices":[{"delta":{"content":"OK"},"finish_reason":null}]}`, `{"id":"chatcmpl-test","choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}}}`}, stop: StopReasonStop, responseID: "chatcmpl-test", content: `[{"type":"text","text":"OK"}]`, total: 4},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:702
		{name: "errors when a stream ends after only null finish_reason chunks", provider: "openai", id: "gpt-4o-mini", prompt: "Reply with a longer sentence", chunks: []string{`{"id":"chatcmpl-truncated","choices":[{"delta":{"content":"partial answer"},"finish_reason":null}]}`, `{"id":"chatcmpl-truncated","choices":[{"delta":{"content":"partial answer"},"finish_reason":null}]}`}, stop: StopReasonError, message: "Stream ended without finish_reason"},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:734
		{name: "accepts streams without finish_reason when compat disables it", provider: "openai", id: "gpt-4o-mini", prompt: "Reply with a complete answer", chunks: []string{`{"id":"chatcmpl-no-finish-reason","choices":[{"delta":{"content":"complete answer"},"finish_reason":null}]}`}, stop: StopReasonStop, content: `[{"type":"text","text":"complete answer"}]`, noFinish: true},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1315
		{name: "normalizes OpenCode Go reasoning deltas to reasoning_content for replay", provider: "opencode-go", id: "kimi-k2.6", prompt: "Use reasoning.", chunks: []string{`{"id":"chatcmpl-opencode-go-reasoning","choices":[{"delta":{"reasoning":"think"},"finish_reason":"stop"}]}`}, stop: StopReasonStop, content: `[{"type":"thinking","thinking":"think","thinkingSignature":"reasoning_content"}]`},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1342
		{name: "keeps non-OpenCode Go reasoning deltas on the original reasoning field", provider: "openai", id: "gpt-4o-mini", prompt: "Use reasoning.", chunks: []string{`{"id":"chatcmpl-reasoning","choices":[{"delta":{"reasoning":"think"},"finish_reason":"stop"}]}`}, stop: StopReasonStop, content: `[{"type":"thinking","thinking":"think","thinkingSignature":"reasoning"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := toolChoiceModel(t, tc.provider, tc.id, tc.provider != "zai")
			if tc.noFinish {
				model.ProviderMeta.Compat = &ModelCompat{SupportsFinishReason: new(false)}
			}
			_, response, _ := captureToolChoiceRequest(t, model, Context{Messages: []Message{UserMessage{Content: UserText(tc.prompt)}}}, StreamOptions{}, tc.chunks)
			if response.StopReason != tc.stop || response.ErrorMessage != tc.message {
				t.Fatalf("response=%#v", response)
			}
			if tc.responseID != "" && response.ResponseID != tc.responseID {
				t.Fatalf("responseId=%q", response.ResponseID)
			}
			if tc.total != 0 && response.Usage.TotalTokens != tc.total {
				t.Fatalf("usage=%#v", response.Usage)
			}
			if tc.content != "" {
				assertCompletionsJSON(t, response.Content, tc.content)
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:761
	t.Run("ignores empty custom objects on function tool call deltas", func(t *testing.T) {
		_, response, _ := captureToolChoiceRequest(t, toolChoiceModel(t, "openai", "gpt-4o-mini", true), Context{Messages: []Message{UserMessage{Content: UserText("Read README.md")}}, Tools: []ToolSchema{{Name: "read", Description: "Read a file", Parameters: JsonObject{"type": "object", "properties": JsonObject{"path": JsonObject{"type": "string"}}, "required": []string{"path"}}}}}, StreamOptions{}, []string{`{"id":"chatcmpl-empty-custom","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"README.md\"}"},"custom":{}}]},"finish_reason":"tool_calls"}]}`})
		assertCompletionsJSON(t, response.Content, `[{"type":"toolCall","id":"call_1","name":"read","arguments":{"path":"README.md"}}]`)
	})
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:810
	t.Run("coalesces tool call deltas by stable index when provider mutates ids mid-stream", func(t *testing.T) {
		chunks := []string{
			`{"id":"chatcmpl-kimi-bad-stream","choices":[{"delta":{"tool_calls":[{"index":0,"id":"functions.read:0","type":"function","function":{"name":"read","arguments":""}}]},"finish_reason":null}]}`,
			`{"id":"chatcmpl-kimi-bad-stream","choices":[{"delta":{"tool_calls":[{"index":0,"id":"chatcmpl-tool-a","type":"function","function":{"name":null,"arguments":"{\"path\":\"README"}}]},"finish_reason":null}]}`,
			`{"id":"chatcmpl-kimi-bad-stream","choices":[{"delta":{"tool_calls":[{"index":0,"id":"chatcmpl-tool-b","type":"function","function":{"name":null,"arguments":".md\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}}}`,
		}
		_, response, events := captureToolChoiceRequest(t, toolChoiceModel(t, "openai", "gpt-4o-mini", true), Context{Messages: []Message{UserMessage{Content: UserText("Read README.md")}}, Tools: []ToolSchema{{Name: "read", Description: "Read a file", Parameters: JsonObject{"type": "object", "properties": JsonObject{"path": JsonObject{"type": "string"}}, "required": []string{"path"}}}}}, StreamOptions{}, chunks)
		var indexes []int
		for _, event := range events {
			switch event := event.(type) {
			case ToolCallStartEvent:
				indexes = append(indexes, event.ContentIndex)
			case ToolCallDeltaEvent:
				indexes = append(indexes, event.ContentIndex)
			case ToolCallEndEvent:
				indexes = append(indexes, event.ContentIndex)
			}
		}
		if response.StopReason != StopReasonToolUse || !reflect.DeepEqual(indexes, []int{0, 0, 0, 0, 0}) {
			t.Fatalf("stop=%s indexes=%v", response.StopReason, indexes)
		}
		assertCompletionsJSON(t, response.Content, `[{"type":"toolCall","id":"functions.read:0","name":"read","arguments":{"path":"README.md"}}]`)
	})
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:921
	t.Run("accumulates mixed content, reasoning, and parallel tool call deltas independently", func(t *testing.T) {
		chunks := []string{
			`{"id":"chatcmpl-mixed-deltas","choices":[{"delta":{"content":"answer 1","reasoning_content":"think 1","tool_calls":[{"index":0,"id":"tc_read_initial","type":"function","function":{"name":"read","arguments":"{\"path\":\"README"}},{"index":1,"id":"tc_grep_initial","type":"function","function":{"name":"grep","arguments":"{\"pattern\":\"TODO"}},{"id":"tc_list_no_index","type":"function","function":{"name":"list","arguments":"{\"path\":\"packages"}},{"id":"tc_write_no_index","type":"function","function":{"name":"write","arguments":"{\"path\":\"out"}}]},"finish_reason":null}]}`,
			`{"id":"chatcmpl-mixed-deltas","choices":[{"delta":{"content":" answer 2","tool_calls":[{"index":1,"id":"tc_grep_changed","type":"function","function":{"arguments":"\",\"path\":\"src"}},{"id":"tc_write_no_index","type":"function","function":{"arguments":".txt\",\"content\":\"ok\"}"}},{"id":"tc_list_no_index","type":"function","function":{"arguments":"/ai\"}"}}]},"finish_reason":null}]}`,
			`{"id":"chatcmpl-mixed-deltas","choices":[{"delta":{"content":"\n","reasoning_content":" think 2","tool_calls":[{"index":0,"id":"tc_read_changed","type":"function","function":{"arguments":".md\"}"}},{"index":1,"type":"function","function":{"arguments":"\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":8,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":2}}}`,
		}
		tools := []ToolSchema{}
		for _, tc := range []struct {
			name, description string
			fields            []string
		}{{"read", "Read a file", []string{"path"}}, {"grep", "Search a file", []string{"pattern", "path"}}, {"list", "List a directory", []string{"path"}}, {"write", "Write a file", []string{"path", "content"}}} {
			properties := JsonObject{}
			for _, field := range tc.fields {
				properties[field] = JsonObject{"type": "string"}
			}
			tools = append(tools, ToolSchema{Name: tc.name, Description: tc.description, Parameters: JsonObject{"type": "object", "properties": properties, "required": tc.fields}})
		}
		_, response, events := captureToolChoiceRequest(t, toolChoiceModel(t, "openai", "gpt-4o-mini", true), Context{Messages: []Message{UserMessage{Content: UserText("Think, answer, and use tools.")}}, Tools: tools}, StreamOptions{}, chunks)
		counts := map[AssistantEventType]int{}
		byIndex := map[int][]AssistantEventType{}
		for _, event := range events {
			counts[event.EventType()]++
			index := -1
			switch e := event.(type) {
			case ToolCallStartEvent:
				index = e.ContentIndex
			case ToolCallDeltaEvent:
				index = e.ContentIndex
			case ToolCallEndEvent:
				index = e.ContentIndex
			}
			if index >= 0 {
				byIndex[index] = append(byIndex[index], event.EventType())
			}
		}
		for typ, want := range map[AssistantEventType]int{EventTextStart: 1, EventTextDelta: 3, EventTextEnd: 1, EventThinkingStart: 1, EventThinkingDelta: 2, EventThinkingEnd: 1, EventToolCallStart: 4, EventToolCallDelta: 9, EventToolCallEnd: 4} {
			if counts[typ] != want {
				t.Errorf("%s=%d want=%d", typ, counts[typ], want)
			}
		}
		for _, index := range []int{2, 3, 4, 5} {
			want := []AssistantEventType{EventToolCallStart, EventToolCallDelta, EventToolCallDelta, EventToolCallEnd}
			if index == 3 {
				want = []AssistantEventType{EventToolCallStart, EventToolCallDelta, EventToolCallDelta, EventToolCallDelta, EventToolCallEnd}
			}
			if !reflect.DeepEqual(byIndex[index], want) {
				t.Errorf("index %d events=%v want=%v", index, byIndex[index], want)
			}
		}
		if response.StopReason != StopReasonToolUse {
			t.Fatal(response)
		}
		assertCompletionsJSON(t, response.Content, `[{"type":"text","text":"answer 1 answer 2\n"},{"type":"thinking","thinking":"think 1 think 2","thinkingSignature":"reasoning_content"},{"type":"toolCall","id":"tc_read_initial","name":"read","arguments":{"path":"README.md"}},{"type":"toolCall","id":"tc_grep_initial","name":"grep","arguments":{"pattern":"TODO","path":"src"}},{"type":"toolCall","id":"tc_list_no_index","name":"list","arguments":{"path":"packages/ai"}},{"type":"toolCall","id":"tc_write_no_index","name":"write","arguments":{"path":"out.txt","content":"ok"}}]`)
	})
	for _, tc := range []struct {
		name                              string
		chunks                            []string
		input, output, read, write, total int
	}{
		// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1641
		{"does not double-count reasoning tokens in completion usage", []string{`{"id":"chatcmpl-reasoning-usage","choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":33,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":21}}}`}, 10, 33, 0, 0, 43},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1676
		{"preserves prompt_tokens_details cache read/write fields from chunk usage", []string{`{"id":"chatcmpl-cache-write","choices":[{"delta":{"content":"OK"},"finish_reason":null}]}`, `{"id":"chatcmpl-cache-write","choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":50,"cache_write_tokens":30},"completion_tokens_details":{"reasoning_tokens":0}}}`}, 20, 5, 50, 30, 105},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1717
		{"preserves prompt_tokens_details cache read/write fields from choice usage fallback", []string{`{"id":"chatcmpl-cache-write-choice","choices":[{"delta":{"content":"OK"},"finish_reason":null}]}`, `{"id":"chatcmpl-cache-write-choice","choices":[{"delta":{},"finish_reason":"stop","usage":{"prompt_tokens":100,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":50,"cache_write_tokens":30},"completion_tokens_details":{"reasoning_tokens":0}}}]}`}, 20, 5, 50, 30, 105},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prompt := "Reply with exactly OK"
			if tc.input == 10 {
				prompt = "Use reasoning."
			}
			_, response, _ := captureToolChoiceRequest(t, toolChoiceModel(t, "openai", "gpt-4o-mini", true), Context{Messages: []Message{UserMessage{Content: UserText(prompt)}}}, StreamOptions{}, tc.chunks)
			u := response.Usage
			if u.Input != tc.input || u.Output != tc.output || u.CacheRead != tc.read || u.CacheWrite != tc.write || u.TotalTokens != tc.total {
				encoded, _ := json.Marshal(response)
				t.Fatalf("response=%s", encoded)
			}
		})
	}
}
