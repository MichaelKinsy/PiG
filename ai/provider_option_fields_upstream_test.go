package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Provider-specific options of upstream (AnthropicOptions.thinkingDisplay, BedrockOptions.thinkingDisplay and .bearerToken,
// GoogleVertexOptions.project and .location, OpenAIResponsesOptions.serviceTier, OpenAICodexResponsesOptions.serviceTier and
// .textVerbosity) are fields of StreamOptions. Each test captures the request the provider builds.

func captureResponsesPayload(t *testing.T, codex bool, options StreamOptions) map[string]any {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		event := "response.completed"
		if codex {
			event = "response.done"
		}
		_, _ = w.Write([]byte("data: {\"type\":\"" + event + "\",\"response\":{\"id\":\"r\",\"status\":\"completed\"}}\n\n"))
	}))
	defer server.Close()
	var provider Provider
	if codex {
		provider = NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: codexTestToken(t, "acct"), Model: "gpt-5.2", ProviderID: "openai-codex", BaseURL: server.URL})
		options.Transport = TransportSSE
	} else {
		provider = NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "key", Model: "gpt-5.2", ProviderID: "openai", BaseURL: server.URL})
	}
	var payload map[string]any
	options.OnPayload = func(value any, _ *Model) (any, error) {
		encoded, err := json.Marshal(value)
		if err == nil {
			err = json.Unmarshal(encoded, &payload)
		}
		return nil, err
	}
	stream, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), options)
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result.StopReason != StopReasonStop {
		t.Fatalf("stop=%q error=%q", result.StopReason, result.ErrorMessage)
	}
	return payload
}

// upstream: packages/ai/src/api/openai-responses.ts:348 and openai-codex-responses.ts:570 copy options.serviceTier to service_tier.
func TestResponsesServiceTierOption(t *testing.T) {
	for _, codex := range []bool{false, true} {
		name := map[bool]string{false: "openai-responses", true: "openai-codex-responses"}[codex]
		t.Run(name, func(t *testing.T) {
			if got := captureResponsesPayload(t, codex, StreamOptions{ServiceTier: "priority"})["service_tier"]; got != "priority" {
				t.Fatalf("service_tier = %#v, want priority", got)
			}
			if got, present := captureResponsesPayload(t, codex, StreamOptions{})["service_tier"]; present {
				t.Fatalf("service_tier = %#v, want omitted without the option", got)
			}
			// openai-responses.ts:383-385 sampling parameters override the named request fields; openai-codex-responses.ts applies none.
			want := map[bool]string{false: "flex", true: "priority"}[codex]
			if got := captureResponsesPayload(t, codex, StreamOptions{ServiceTier: "priority", SamplingParams: map[string]any{"service_tier": "flex"}})["service_tier"]; got != want {
				t.Fatalf("service_tier = %#v, want %s", got, want)
			}
		})
	}
}

// upstream: openai-codex-responses.ts:559 text.verbosity is options.textVerbosity, else "low".
func TestCodexTextVerbosityOption(t *testing.T) {
	for _, tc := range []struct{ option, want string }{{"", "low"}, {"high", "high"}, {"medium", "medium"}} {
		text, _ := captureResponsesPayload(t, true, StreamOptions{TextVerbosity: tc.option})["text"].(map[string]any)
		if text["verbosity"] != tc.want {
			t.Fatalf("textVerbosity %q: text = %#v, want verbosity %q", tc.option, text, tc.want)
		}
	}
}
