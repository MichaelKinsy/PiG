package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func codexTestToken(t *testing.T, accountID string) string {
	t.Helper()
	return codexTestTokenForAccount(accountID)
}

func codexTestTokenForAccount(accountID string) string {
	claims, _ := json.Marshal(map[string]any{
		codexJWTClaimPath: map[string]string{"chatgpt_account_id": accountID},
	})
	return "header." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
}

func TestOpenAICodexResponses_RequestMatchesCodexProtocol(t *testing.T) {
	t.Parallel()

	var capturedHeader http.Header
	var capturedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeader = r.Header.Clone()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if r.Header.Get("Content-Encoding") == "zstd" {
			body, err = decodeZstdRawFrameForTest(body)
			if err != nil {
				t.Error(err)
			}
		}
		if err := json.Unmarshal(body, &capturedBody); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(`{"error":{"message":"probe stop"}}`))
	}))
	defer server.Close()

	provider := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{
		APIKey:     codexTestToken(t, "acct_test"),
		Model:      "gpt-5.2",
		ProviderID: "openai-codex",
		BaseURL:    server.URL,
	})
	transcript := NormalizeContext(Context{
		SystemPrompt: "system prompt",
		Messages:     []Message{UserMessage{Content: UserText("hello")}},
	})
	_, err := provider.Stream(context.Background(), transcript, StreamOptions{
		MaxTokens:   1000,
		Thinking:    ThinkingHigh,
		IsReasoning: true,
		SessionID:   "sess",
		Transport:   TransportSSE,
	})
	if err == nil {
		t.Fatal("Stream() error = nil, want probe HTTP error")
	}

	if got := capturedBody["instructions"]; got != "system prompt" {
		t.Errorf("instructions = %v, want system prompt", got)
	}
	if got := capturedBody["text"]; !jsonObjectsEqual(got, map[string]any{"verbosity": "low"}) {
		t.Errorf("text = %#v, want low verbosity", got)
	}
	if got := capturedBody["include"]; !jsonObjectsEqual(got, []any{"reasoning.encrypted_content"}) {
		t.Errorf("include = %#v, want encrypted reasoning", got)
	}
	if got := capturedBody["tool_choice"]; got != "auto" {
		t.Errorf("tool_choice = %v, want auto", got)
	}
	if got := capturedBody["parallel_tool_calls"]; got != true {
		t.Errorf("parallel_tool_calls = %v, want true", got)
	}
	if got := capturedBody["reasoning"]; !jsonObjectsEqual(got, map[string]any{"effort": "high", "summary": "auto"}) {
		t.Errorf("reasoning = %#v, want high/auto", got)
	}
	if got := capturedBody["prompt_cache_key"]; got != "sess" {
		t.Errorf("prompt_cache_key = %v, want sess", got)
	}
	if _, ok := capturedBody["max_output_tokens"]; ok {
		t.Errorf("max_output_tokens = %v, want omitted", capturedBody["max_output_tokens"])
	}
	input, _ := capturedBody["input"].([]any)
	for _, raw := range input {
		item, _ := raw.(map[string]any)
		if item["role"] == "system" || item["role"] == "developer" {
			t.Errorf("input contains top-level instruction role: %#v", item)
		}
	}

	if got := capturedHeader.Get("chatgpt-account-id"); got != "acct_test" {
		t.Errorf("chatgpt-account-id = %q, want acct_test", got)
	}
	if got := capturedHeader.Get("session-id"); got != "sess" {
		t.Errorf("session-id = %q, want sess", got)
	}
	if got := capturedHeader.Get("session_id"); got != "" {
		t.Errorf("session_id = %q, want omitted", got)
	}
	if got := capturedHeader.Get("x-client-request-id"); got != "sess" {
		t.Errorf("x-client-request-id = %q, want sess", got)
	}
	if got := capturedHeader.Get("Accept"); got != "text/event-stream" {
		t.Errorf("Accept = %q, want text/event-stream", got)
	}
	if got := capturedHeader.Get("originator"); got != "pi" {
		t.Errorf("originator = %q, want pi", got)
	}
	if got := capturedHeader.Get("OpenAI-Beta"); got != "responses=experimental" {
		t.Errorf("OpenAI-Beta = %q, want responses=experimental", got)
	}
	if got := capturedHeader.Get("Content-Encoding"); got != "zstd" {
		t.Errorf("Content-Encoding = %q, want zstd", got)
	}
}

func TestOpenAICodexResponses_ReasoningOffUsesModelOffMapping(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.done\",\"response\":{\"id\":\"response-off\",\"status\":\"completed\"}}\n\n"))
	}))
	defer server.Close()
	provider := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{
		APIKey: codexTestToken(t, "acct_off"), Model: "gpt-5.2", ProviderID: "openai-codex", BaseURL: server.URL,
	})
	stream, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{
		Transport: TransportSSE,
		Thinking:  ThinkingOff,
		OnPayload: func(value any, _ *Model) (any, error) {
			encoded, marshalErr := json.Marshal(value)
			if marshalErr == nil {
				marshalErr = json.Unmarshal(encoded, &payload)
			}
			return nil, marshalErr
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result.StopReason != StopReasonStop {
		t.Fatalf("stop=%q error=%q", result.StopReason, result.ErrorMessage)
	}
	if got := payload["reasoning"]; got != nil {
		t.Errorf("reasoning = %#v, want omitted because this model maps off to null", got)
	}
	if got := payload["include"]; !jsonObjectsEqual(got, []any{"reasoning.encrypted_content"}) {
		t.Errorf("include = %#v, want encrypted reasoning", got)
	}
}

func TestConvertResponsesMessages_EmptyV1TextSignatureUsesFallbackID(t *testing.T) {
	provider := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{Model: "gpt-5.2", ProviderID: "openai-codex", Codex: true}}
	items, err := provider.convertMessages([]Message{AssistantMessage{Content: []AssistantContentBlock{
		TextContent{Text: "answer", TextSignature: `{"v":1,"id":"","phase":"commentary"}`},
	}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "msg_pi_0" || items[0].Phase != "commentary" {
		t.Fatalf("items = %+v, want fallback id msg_pi_0 with commentary phase", items)
	}
}

func jsonObjectsEqual(got, want any) bool {
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	return string(gotJSON) == string(wantJSON)
}
