package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// api/openai-completions.ts:343-346 (`createGrammarToolInputProperties(getDeclaredTools(normalizedContext.messages), compat.supportsOpenAIGrammarTools)`) feeds the replay of a prior tool call: with the
// compat flag on, a call of a declared grammar tool is sent back as a `custom` tool call whose input is the grammar string, and the declared tool itself goes out as a `custom`
// tool; with the flag off both stay plain `function` shapes. The request is what the provider sends for a whole transcript, not a leaf conversion.
func TestOpenAICompletionsGrammarToolReplayFollowsTheCompatFlagInTheRequest(t *testing.T) {
	grammarTool := ToolSchema{
		Name: "calc", Description: "calculator", Parameters: completionsGrammarSchema(),
		ConstrainedSampling: &ConstrainedSamplingConfig{Type: "grammar", Variants: map[string]string{GrammarFormatOpenAILark: "start: NUMBER"}},
	}
	messages := []Message{
		SystemMessage{Content: SystemText("base"), Timestamp: 1, ToolsAdded: []ToolSchema{grammarTool}},
		UserMessage{Content: UserText("add"), Timestamp: 2},
		AssistantMessage{Content: []AssistantContentBlock{ToolCall{ID: "call_1", Name: "calc", Arguments: JsonObject{"expr": "3+4"}}}, StopReason: StopReasonToolUse, Timestamp: 3},
		ToolResultMessage{ToolCallID: "call_1", ToolName: "calc", Content: []ToolResultMessageContent{TextContent{Text: "7"}}, Timestamp: 4},
		UserMessage{Content: UserText("again"), Timestamp: 5},
	}
	for _, tc := range []struct {
		name         string
		flag         *bool
		wantToolType string
		wantCallType string
	}{
		{"flag on", new(true), "custom", "custom"},
		{"flag off", new(false), "function", "function"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusBadRequest)
			}))
			defer server.Close()
			provider := NewOpenAIProvider(OpenAIConfig{BaseURL: server.URL, APIKey: "k", Model: "m", ProviderID: "custom", Compat: &OpenAICompat{SupportsOpenAIGrammarTools: tc.flag}})
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: messages}), StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for range stream.Events(t.Context()) {
			}
			var request struct {
				Tools    []struct{ Type string } `json:"tools"`
				Messages []struct {
					Role      string `json:"role"`
					ToolCalls []struct {
						Type   string `json:"type"`
						Custom *struct {
							Name  string `json:"name"`
							Input string `json:"input"`
						} `json:"custom"`
						Function *struct {
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(body, &request); err != nil {
				t.Fatalf("request body %q: %v", body, err)
			}
			if len(request.Tools) != 1 || request.Tools[0].Type != tc.wantToolType {
				t.Errorf("tools = %+v, want one %q tool", request.Tools, tc.wantToolType)
			}
			var calls int
			for _, message := range request.Messages {
				for _, call := range message.ToolCalls {
					calls++
					if call.Type != tc.wantCallType {
						t.Errorf("replayed call type = %q, want %q", call.Type, tc.wantCallType)
					}
					if tc.wantCallType == "custom" && (call.Custom == nil || call.Custom.Name != "calc" || call.Custom.Input != "3+4") {
						t.Errorf("replayed custom call = %+v, want calc with input 3+4", call.Custom)
					}
					if tc.wantCallType == "function" && (call.Function == nil || call.Function.Arguments != `{"expr":"3+4"}`) {
						t.Errorf("replayed function call = %+v, want the JSON arguments", call.Function)
					}
				}
			}
			if calls != 1 {
				t.Errorf("replayed %d tool calls, want 1 (body %s)", calls, body)
			}
		})
	}
}
