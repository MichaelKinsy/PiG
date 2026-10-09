//go:build !pig_strip_google_vertex && !pig_strip_mistral_conversations

package ai

import (
	"encoding/json"
	"testing"
)

// google-generative-ai.ts:390-399 passes a string system instruction through the Google SDK and omits a default tool mode. google-shared.ts:423-436 preserves explicit none/any before strict VALIDATED mode.
func TestGoogleWireSystemInstructionAndToolChoice(t *testing.T) {
	for _, vertex := range []bool{false, true} {
		for _, tc := range []struct {
			name, choice, mode string
			tools, strict      bool
		}{
			{name: "no-tools"},
			{name: "default", tools: true},
			{name: "auto", tools: true, choice: "auto", mode: "AUTO"},
			{name: "none", tools: true, choice: "none", mode: "NONE"},
			{name: "any", tools: true, choice: "any", mode: "ANY"},
			{name: "unknown", tools: true, choice: "future", mode: "AUTO"},
			{name: "strict", tools: true, strict: true, mode: "VALIDATED"},
			{name: "strict-auto", tools: true, strict: true, choice: "auto", mode: "VALIDATED"},
			{name: "strict-none", tools: true, strict: true, choice: "none", mode: "NONE"},
			{name: "strict-any", tools: true, strict: true, choice: "any", mode: "ANY"},
		} {
			api := "google"
			if vertex {
				api = "vertex"
			}
			t.Run(api+"/"+tc.name, func(t *testing.T) {
				system := SystemMessage{Content: SystemText("Be precise")}
				if tc.tools {
					tool := ToolSchema{Name: "lookup", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}
					if tc.strict {
						tool.ConstrainedSampling = &ConstrainedSamplingConfig{Type: "json_schema", Strict: "require"}
					}
					system.ToolsAdded = []ToolSchema{tool}
				}
				body := captureShapeRequest(t, func(url string) Provider {
					if vertex {
						return NewGoogleVertexProvider(GoogleVertexConfig{BaseURL: url, APIKey: "test", Model: "gemini-3-pro"})
					}
					return NewGoogleProvider(GoogleConfig{BaseURL: url, APIKey: "test", Model: "gemini-3-pro"})
				}, []Message{system, UserMessage{Content: UserText("hello")}}, StreamOptions{ToolChoice: tc.choice}, "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n")
				assertShapeJSON(t, body["systemInstruction"], `{"role":"user","parts":[{"text":"Be precise"}]}`)
				if tc.mode == "" {
					if value, exists := body["toolConfig"]; exists {
						t.Errorf("toolConfig = %s, want absent", value)
					}
				} else {
					assertShapeJSON(t, body["toolConfig"], `{"functionCallingConfig":{"mode":"`+tc.mode+`"}}`)
				}
			})
		}
	}
}

// mistral-conversations.ts:527-533 sends a prompt cache key only with sessionId and cache retention other than none. :843 marks replayed assistants as non-prefix messages.
func TestMistralWireCacheKeyAndAssistantPrefix(t *testing.T) {
	for _, tc := range []struct {
		name, session string
		retention     CacheRetention
		wantKey       bool
	}{
		{name: "no-session"},
		{name: "default", session: "session-1", wantKey: true},
		{name: "short", session: "session-1", retention: CacheRetentionShort, wantKey: true},
		{name: "long", session: "session-1", retention: CacheRetentionLong, wantKey: true},
		{name: "none", session: "session-1", retention: CacheRetentionNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := captureShapeRequest(t, func(url string) Provider {
				return NewMistralProvider(MistralConfig{BaseURL: url, APIKey: "test", Model: "mistral-large-latest"})
			}, []Message{
				UserMessage{Content: UserText("hello")},
				AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "answer"}}},
			}, StreamOptions{SessionID: tc.session, CacheRetention: tc.retention}, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
			key, exists := body["prompt_cache_key"]
			if exists != tc.wantKey {
				t.Errorf("prompt_cache_key presence = %t, want %t", exists, tc.wantKey)
			}
			if tc.wantKey && exists {
				assertShapeJSON(t, key, `"session-1"`)
			}
			assertShapeJSON(t, body["messages"], `[{"role":"user","content":"hello"},{"role":"assistant","prefix":false,"content":[{"type":"text","text":"answer"}]}]`)
		})
	}
}

// The cache key and automatic affinity header share shouldUsePromptCaching (mistral-conversations.ts:347-349,527-533). An explicit empty model header remains explicit.
func TestMistralAffinityUsesPromptCachePolicy(t *testing.T) {
	for _, tc := range []struct {
		name      string
		retention CacheRetention
		headers   map[string]string
		want      string
		present   bool
	}{
		{name: "default", want: "session-1", present: true},
		{name: "none", retention: CacheRetentionNone},
		{name: "explicit-empty", headers: map[string]string{"x-affinity": ""}, present: true},
		{name: "explicit-with-none", retention: CacheRetentionNone, headers: map[string]string{"X-Affinity": "explicit"}, want: "explicit", present: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, requests := rejectingProviderServer(t)
			provider := NewMistralProvider(MistralConfig{BaseURL: server.URL, APIKey: "test", Model: "mistral-large-latest", ExtraHeaders: tc.headers})
			if _, err := provider.Stream(t.Context(), providerWireTranscript(), StreamOptions{SessionID: "session-1", CacheRetention: tc.retention}); err == nil {
				t.Fatal("expected the capture server's HTTP rejection")
			}
			request := <-requests
			_, present := request.header["X-Affinity"]
			if got := request.header.Get("X-Affinity"); got != tc.want || present != tc.present {
				t.Fatalf("x-affinity = %q (present %t), want %q (present %t)", got, present, tc.want, tc.present)
			}
		})
	}
}

// The replay input and expectation come from packages/ai/test/mistral-http-transport.test.ts:169-255.
func TestMistralAssistantReplayPrefixAndToolIndex(t *testing.T) {
	provider := NewMistralProvider(MistralConfig{}).(*mistralProvider)
	message := provider.convertAssistantMessage(AssistantMessage{Content: []AssistantContentBlock{
		ThinkingContent{Thinking: "reason"}, TextContent{Text: "answer"},
		ToolCall{ID: "abc123456", Name: "lookup", Arguments: JsonObject{"query": "pi"}},
	}})
	// The message is SDK-shaped; assert wire fields only after the production lowering step.
	wire, err := toMistralWirePayload(mistralRequest{Messages: []mistralMessage{message}})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(wire["messages"].([]any)[0])
	if err != nil {
		t.Fatal(err)
	}
	assertShapeJSON(t, body, `{"role":"assistant","prefix":false,"content":[{"type":"thinking","thinking":[{"type":"text","text":"reason"}]},{"type":"text","text":"answer"}],"tool_calls":[{"id":"abc123456","type":"function","function":{"name":"lookup","arguments":"{\"query\":\"pi\"}"},"index":0}]}`)
}
