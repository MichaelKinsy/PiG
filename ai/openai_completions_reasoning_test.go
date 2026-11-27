package ai

// Ports packages/ai/test/openai-completions-reasoning.test.ts and
// packages/ai/test/openai-completions-reasoning-opaque.test.ts.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestOpenAICompletionsThinkingSignature(t *testing.T) {
	encryptedDetail := json.RawMessage(`{"type":"reasoning.encrypted","id":"call_1","data":"encrypted"}`)

	t.Run("round-trips", func(t *testing.T) {
		cases := []struct {
			name string
			sig  completionsThinkingSignature
		}{
			{"field", completionsThinkingSignature{kind: completionsThinkingSignatureField, field: "reasoning_content"}},
			{"details", completionsThinkingSignature{kind: completionsThinkingSignatureDetails, details: []json.RawMessage{encryptedDetail}}},
			{"opaque with field", completionsThinkingSignature{kind: completionsThinkingSignatureOpaque, field: "reasoning_text", opaque: "opaque-1"}},
			{"opaque without field", completionsThinkingSignature{kind: completionsThinkingSignatureOpaque, opaque: "opaque-1"}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got := decodeCompletionsThinkingSignature(encodeCompletionsThinkingSignature(tc.sig))
				if got == nil || !reflect.DeepEqual(*got, tc.sig) {
					t.Fatalf("round-trip %s: got %#v, want %#v", tc.name, got, tc.sig)
				}
			})
		}
	})

	t.Run("reads the stored formats", func(t *testing.T) {
		if got := decodeCompletionsThinkingSignature("reasoning"); got == nil || got.kind != completionsThinkingSignatureField || got.field != "reasoning" {
			t.Fatalf("field: got %#v", got)
		}
		if got := decodeCompletionsThinkingSignature(`[` + string(encryptedDetail) + `]`); got == nil || got.kind != completionsThinkingSignatureDetails || len(got.details) != 1 {
			t.Fatalf("details: got %#v", got)
		}
		if got := decodeCompletionsThinkingSignature(`{"field":"reasoning_text","reasoning_opaque":"o"}`); got == nil || got.kind != completionsThinkingSignatureOpaque || got.field != "reasoning_text" || got.opaque != "o" {
			t.Fatalf("opaque: got %#v", got)
		}
	})

	t.Run("decodes invalid signatures as nil", func(t *testing.T) {
		invalids := []string{
			"",
			"anthropic-signature",
			"[]",
			"[not json",
			`[{"type":"unknown"}]`,
			`{"reasoning_opaque":""}`,
			`{"field":"content","reasoning_opaque":"o"}`,
			"{not json",
		}
		for _, sig := range invalids {
			if got := decodeCompletionsThinkingSignature(sig); got != nil {
				t.Errorf("decodeCompletionsThinkingSignature(%q) = %#v, want nil", sig, got)
			}
		}
	})

	t.Run("reads encrypted details stored on tool calls by older sessions", func(t *testing.T) {
		if got := decodeLegacyToolCallReasoningDetail(string(encryptedDetail)); string(got) != string(encryptedDetail) {
			t.Fatalf("legacy detail = %q, want %q", got, encryptedDetail)
		}
		if got := decodeLegacyToolCallReasoningDetail(`{"type":"reasoning.encrypted","id":"","data":"encrypted"}`); got != nil {
			t.Fatalf("empty id legacy detail = %q, want nil", got)
		}
		if got := decodeLegacyToolCallReasoningDetail("google-thought-signature"); got != nil {
			t.Fatalf("non-json legacy detail = %q, want nil", got)
		}
	})
}

func TestOpenAICompletionsReasoningOpaqueReplay(t *testing.T) {
	chunk := func(delta string, finishReason string) string {
		fr := "null"
		if finishReason != "" {
			fr = `"` + finishReason + `"`
		}
		return `data: {"id":"chatcmpl-test","model":"gemini-test","choices":[{"index":0,"delta":` + delta + `,"finish_reason":` + fr + `}]}` + "\n\n"
	}
	toolCallChunk := func(index int, id string) string {
		return chunk(`{"tool_calls":[{"index":`+string(rune('0'+index))+`,"id":"`+id+`","type":"function","function":{"name":"read","arguments":"{\"path\":\"README.md\"}"}}]}`, "")
	}

	runTwoTurn := func(t *testing.T, turn1SSE string, mutate func(*AssistantMessage)) (AssistantMessage, map[string]any) {
		t.Helper()
		var payloads []map[string]any
		turn := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode request: %v", err)
			}
			payloads = append(payloads, body)
			w.Header().Set("Content-Type", "text/event-stream")
			if turn == 0 {
				turn++
				_, _ = w.Write([]byte(turn1SSE))
				return
			}
			_, _ = w.Write([]byte(chunk(`{"content":"ok"}`, "stop") + "data: [DONE]\n\n"))
		}))
		defer srv.Close()

		model := &Model{
			ID:           "gemini-test",
			DisplayName:  "Gemini Test",
			Input:        []string{"text"},
			Capabilities: ModelCapabilities{ContextWindow: 100000, MaxOutputTokens: 4096},
			ProviderMeta: ProviderMetadata{
				ProviderID: "github-copilot",
				API:        APIOpenAICompletions,
				BaseURL:    srv.URL,
				Reasoning:  true,
			},
		}
		provider := NewOpenAIProvider(OpenAIConfig{
			APIKey:        "test",
			BaseURL:       srv.URL,
			Model:         model.ID,
			ProviderID:    "github-copilot",
			ModelMetadata: model,
		})
		tools := []ToolSchema{{
			Name:        "read",
			Description: "Read a file",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}},
		}}
		run := func(msgs []Message) AssistantMessage {
			s, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: msgs, Tools: tools}), StreamOptions{IsReasoning: true})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			res := s.Result()
			return *res
		}
		msg1 := run([]Message{UserMessage{Content: UserText("hi")}})
		if mutate != nil {
			mutate(&msg1)
		}
		msg1.API = APIOpenAICompletions
		msg1.Provider = "github-copilot"
		if msg1.Model == "" {
			msg1.Model = model.ID
		}
		if msg1.StopReason == "" {
			msg1.StopReason = StopReasonStop
		}
		if msg1.StopReason == StopReasonError {
			t.Fatalf("turn 1 failed: %s", msg1.ErrorMessage)
		}
		msgs2 := []Message{UserMessage{Content: UserText("hi")}, msg1}
		for _, b := range msg1.Content {
			if tc, ok := b.(ToolCall); ok {
				msgs2 = append(msgs2, ToolResultMessage{ToolCallID: tc.ID, ToolName: tc.Name, Content: []ToolResultMessageContent{TextContent{Text: "ok"}}})
			}
		}
		_ = run(msgs2)
		if len(payloads) < 2 {
			t.Fatalf("expected 2 payloads, got %d", len(payloads))
		}
		msgs, _ := payloads[1]["messages"].([]any)
		for _, m := range msgs {
			mm, _ := m.(map[string]any)
			if mm["role"] == "assistant" {
				return msg1, mm
			}
		}
		t.Fatalf("no assistant message in second payload: %v", payloads[1])
		return msg1, nil
	}

	t.Run("replays reasoning_opaque with the reasoning_text it streamed after", func(t *testing.T) {
		sse := chunk(`{"role":"assistant","reasoning_text":"Read the readme"}`, "") +
			chunk(`{"reasoning_text":" first."}`, "") +
			chunk(`{"role":"assistant","reasoning_opaque":"opaque-1"}`, "") +
			toolCallChunk(0, "call_1") +
			chunk(`{}`, "tool_calls") +
			"data: [DONE]\n\n"
		msg, assistant := runTwoTurn(t, sse, nil)
		th, ok := msg.Content[0].(ThinkingContent)
		if !ok || th.Thinking != "Read the readme first." || th.ThinkingSignature != `{"field":"reasoning_text","reasoning_opaque":"opaque-1"}` {
			t.Fatalf("unexpected thinking block: %#v", msg.Content[0])
		}
		if assistant["reasoning_text"] != "Read the readme first." || assistant["reasoning_opaque"] != "opaque-1" {
			t.Fatalf("unexpected assistant payload: %#v", assistant)
		}
	})

	t.Run("replays the latest reasoning_opaque with concatenated reasoning text", func(t *testing.T) {
		sse := chunk(`{"reasoning_text":"Read the readme."}`, "") +
			chunk(`{"reasoning_opaque":"opaque-first"}`, "") +
			toolCallChunk(0, "call_1") +
			chunk(`{"reasoning_text":" Then the manifest."}`, "") +
			chunk(`{"reasoning_opaque":"opaque-second"}`, "") +
			toolCallChunk(1, "call_2") +
			chunk(`{}`, "tool_calls") +
			"data: [DONE]\n\n"
		_, assistant := runTwoTurn(t, sse, nil)
		if assistant["reasoning_text"] != "Read the readme. Then the manifest." || assistant["reasoning_opaque"] != "opaque-second" {
			t.Fatalf("unexpected assistant payload: %#v", assistant)
		}
	})

	t.Run("replays reasoning_opaque that arrived without reasoning text", func(t *testing.T) {
		sse := chunk(`{"content":"Checking.","reasoning_opaque":"opaque-only"}`, "") +
			toolCallChunk(0, "call_1") +
			chunk(`{}`, "tool_calls") +
			"data: [DONE]\n\n"
		msg, assistant := runTwoTurn(t, sse, nil)
		if len(msg.Content) != 3 {
			t.Fatalf("expected 3 blocks, got %d", len(msg.Content))
		}
		if assistant["content"] != "Checking." || assistant["reasoning_opaque"] != "opaque-only" {
			t.Fatalf("unexpected assistant payload: %#v", assistant)
		}
		if _, has := assistant["reasoning_text"]; has {
			t.Fatalf("expected no reasoning_text, got %#v", assistant["reasoning_text"])
		}
	})

	t.Run("reads reasoning_text and reasoning_opaque from the same chunk", func(t *testing.T) {
		sse := chunk(`{"reasoning_text":"Plan.","reasoning_opaque":"opaque-1"}`, "") +
			toolCallChunk(0, "call_1") +
			chunk(`{}`, "tool_calls") +
			"data: [DONE]\n\n"
		_, assistant := runTwoTurn(t, sse, nil)
		if assistant["reasoning_text"] != "Plan." || assistant["reasoning_opaque"] != "opaque-1" {
			t.Fatalf("unexpected assistant payload: %#v", assistant)
		}
	})

	t.Run("replays reasoning_details instead of reasoning_opaque when a stream has both", func(t *testing.T) {
		sse := chunk(`{"reasoning_text":"Plan.","reasoning_details":[{"type":"reasoning.encrypted","id":"call_1","data":"encrypted"}]}`, "") +
			chunk(`{"reasoning_opaque":"opaque-1"}`, "") +
			toolCallChunk(0, "call_1") +
			chunk(`{}`, "tool_calls") +
			"data: [DONE]\n\n"
		_, assistant := runTwoTurn(t, sse, nil)
		if _, has := assistant["reasoning_details"]; !has {
			t.Fatalf("expected reasoning_details, got %#v", assistant)
		}
		if _, has := assistant["reasoning_opaque"]; has {
			t.Fatalf("expected no reasoning_opaque, got %#v", assistant)
		}
		if _, has := assistant["reasoning_text"]; has {
			t.Fatalf("expected no reasoning_text, got %#v", assistant)
		}
	})

	t.Run("does not replay reasoning_opaque to another model", func(t *testing.T) {
		sse := chunk(`{"reasoning_text":"Plan."}`, "") +
			chunk(`{"reasoning_opaque":"opaque-1"}`, "") +
			chunk(`{"content":"Done."}`, "stop") +
			"data: [DONE]\n\n"
		_, assistant := runTwoTurn(t, sse, func(m *AssistantMessage) {
			m.Model = "other-model"
		})
		if assistant["content"] != "Done." {
			t.Fatalf("unexpected content: %#v", assistant["content"])
		}
		if _, has := assistant["reasoning_opaque"]; has {
			t.Fatalf("expected no reasoning_opaque, got %#v", assistant)
		}
		if _, has := assistant["reasoning_text"]; has {
			t.Fatalf("expected no reasoning_text, got %#v", assistant)
		}
	})

	t.Run("keeps reasoning_opaque on the thinking block when the stream fails", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(chunk(`{"reasoning_text":"Plan."}`, "") + chunk(`{"reasoning_opaque":"opaque-1"}`, "")))
		}))
		defer srv.Close()
		provider := NewOpenAIProvider(OpenAIConfig{
			APIKey:     "test",
			BaseURL:    srv.URL,
			Model:      "gemini-test",
			ProviderID: "github-copilot",
		})
		s, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{IsReasoning: true})
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		res := s.Result()
		if res.StopReason != StopReasonError || !strings.Contains(res.ErrorMessage, "finish_reason") {
			t.Fatalf("expected missing finish_reason error, got stop=%s err=%q", res.StopReason, res.ErrorMessage)
		}
		th, ok := res.Content[0].(ThinkingContent)
		if !ok || th.Thinking != "Plan." || th.ThinkingSignature != `{"field":"reasoning_text","reasoning_opaque":"opaque-1"}` {
			t.Fatalf("unexpected thinking block on stream error: %#v", res.Content[0])
		}
	})
}
