package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

type completionsRuntimeCapture struct {
	Payload map[string]json.RawMessage
	Headers http.Header
	Path    string
}

func captureCompletionsRuntime(t *testing.T, providerID, modelID string, request ai.Context, options ai.StreamOptions, window, capTokens int) completionsRuntimeCapture {
	t.Helper()
	received := make(chan completionsRuntimeCapture, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			http.Error(w, err.Error(), 400)
			return
		}
		received <- completionsRuntimeCapture{payload, r.Header.Clone(), r.URL.Path}
		w.Header().Set("Content-Type", "text/event-stream")
		if strings.HasSuffix(r.URL.Path, "/responses") {
			_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
		} else {
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"prompt_tokens_details\":{\"cached_tokens\":0},\"completion_tokens_details\":{\"reasoning_tokens\":0}}}\n\n")
		}
	}))
	t.Cleanup(server.Close)
	model, ok := ai.LookupModelExact(providerID + "/" + modelID)
	if !ok {
		t.Fatalf("missing catalog model %s/%s", providerID, modelID)
	}
	base, err := url.Parse(model.BaseURL)
	if err != nil {
		t.Fatal(err)
	}
	override := map[string]any{}
	if window != 0 {
		override["contextWindow"] = window
	}
	if capTokens != 0 {
		override["maxTokens"] = capTokens
	}
	providerConfig := map[string]any{"baseUrl": server.URL + base.Path, "modelOverrides": map[string]any{modelID: override}}
	if providerID == "openai" {
		providerConfig["models"] = []any{map[string]any{"id": modelID, "name": model.DisplayName, "api": "openai-completions", "reasoning": model.Reasoning, "input": model.Capabilities, "contextWindow": model.ContextWindow, "maxTokens": model.MaxOutputTokens}}
	}
	config := map[string]any{"providers": map[string]any{providerID: providerConfig}}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	agentDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	runtimeModel, err := BuildModel(providerID+"/"+modelID, services)
	if err != nil {
		t.Fatal(err)
	}
	result := services.ModelRuntime().StreamSimple(t.Context(), runtimeModel, request, options).Result()
	if result.StopReason != ai.StopReasonStop {
		t.Fatalf("result=%#v", result)
	}
	return <-received
}

func BenchmarkModelRuntimeSimpleReplyBudget(b *testing.B) {
	services, err := NewServices(ServicesOptions{CWD: b.TempDir(), AgentDir: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	provider := &runtimeTestProvider{id: "benchmark", stream: func(_ context.Context, _ ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		if options.MaxTokens != 3904 {
			return nil, fmt.Errorf("max tokens %d, want 3904", options.MaxTokens)
		}
		stream, _ := runtimeTestTextStream("benchmark", "model", "ok")
		return stream, nil
	}}
	// Measure the stock-leaf branch through its production attribution wrapper; a bare caller-owned callback must not lower options.
	stock := newProviderAttributionProvider(provider, "benchmark", "", func() bool { return false }, nil)
	model := &ai.Model{ID: "model", Provider: stock, Capabilities: ai.ModelCapabilities{ContextWindow: 10000, MaxOutputTokens: 8000}}
	request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText(strings.Repeat("x", 8000))}}}
	b.ReportAllocs()
	for b.Loop() {
		if result := services.ModelRuntime().StreamSimple(b.Context(), model, request, ai.StreamOptions{}).Result(); result.StopReason != ai.StopReasonStop {
			b.Fatal(result)
		}
	}
}

func TestOpenAICompletionsEmptyToolsUpstream(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test")
	for _, empty := range []bool{true, false} {
		// .upstream/v0.87.1/packages/ai/test/openai-completions-empty-tools.test.ts:62,79
		name := "omits tools field when context.tools is undefined"
		if empty {
			name = "omits tools field when context.tools is an empty array"
		}
		t.Run(name, func(t *testing.T) {
			ctx := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}
			if empty {
				ctx.Tools = []ai.ToolSchema{}
			}
			captured := captureCompletionsRuntime(t, "openai", "gpt-4o-mini", ctx, ai.StreamOptions{APIKey: "test"}, 0, 0)
			if _, ok := captured.Payload["tools"]; ok {
				t.Fatalf("tools present: %s", captured.Payload["tools"])
			}
		})
	}
	for _, tc := range []struct {
		name, text                         string
		maxTokens, window, capTokens, want int
	}{
		// .upstream/v0.87.1/packages/ai/test/openai-completions-empty-tools.test.ts:95
		{name: "sends default maxTokens", text: "hi"},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-empty-tools.test.ts:112
		{name: "sends explicit maxTokens", text: "hi", maxTokens: 1234, want: 1234},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-empty-tools.test.ts:129
		{name: "clamps default maxTokens to remaining context", text: strings.Repeat("x", 8000), window: 10000, capTokens: 8000, want: 3904},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-empty-tools.test.ts:146
		{name: "clamps explicit maxTokens to remaining context", text: strings.Repeat("x", 8000), window: 10000, capTokens: 8000, maxTokens: 7000, want: 3904},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captured := captureCompletionsRuntime(t, "openai", "gpt-4o-mini", ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText(tc.text)}}}, ai.StreamOptions{APIKey: "test", MaxTokens: tc.maxTokens}, tc.window, tc.capTokens)
			if _, ok := captured.Payload["max_tokens"]; ok {
				t.Fatalf("max_tokens present: %s", captured.Payload["max_tokens"])
			}
			want := tc.want
			if want == 0 {
				model, _ := ai.LookupModelExact("openai/gpt-4o-mini")
				want = model.MaxOutputTokens
			}
			var got int
			if err := json.Unmarshal(captured.Payload["max_completion_tokens"], &got); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("max_completion_tokens=%d want=%d", got, want)
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/openai-completions-empty-tools.test.ts:253
	t.Run("still emits tools: [] for Anthropic/LiteLLM proxy when conversation has tool history", func(t *testing.T) {
		ctx := ai.Context{Tools: []ai.ToolSchema{}, Messages: []ai.Message{
			ai.UserMessage{Content: ai.UserText("use the tool")},
			ai.AssistantMessage{API: ai.APIOpenAICompletions, Provider: "openai", Model: "gpt-4o-mini", StopReason: ai.StopReasonToolUse, Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "t1", Name: "noop", Arguments: ai.JsonObject{}}}},
			ai.ToolResultMessage{ToolCallID: "t1", ToolName: "noop", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}},
		}}
		captured := captureCompletionsRuntime(t, "openai", "gpt-4o-mini", ctx, ai.StreamOptions{APIKey: "test"}, 0, 0)
		if string(captured.Payload["tools"]) != "[]" {
			t.Fatalf("tools=%s", captured.Payload["tools"])
		}
	})
}

func TestOpenAICompletionsCloudflareEmptyToolsUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, model  string
		options      ai.StreamOptions
		system       string
		conservative bool
	}{
		// .upstream/v0.87.1/packages/ai/test/openai-completions-empty-tools.test.ts:163
		{name: "uses conservative OpenAI-compatible fields for Cloudflare AI Gateway /compat models", model: "workers-ai/@cf/moonshotai/kimi-k2.6", options: ai.StreamOptions{MaxTokens: 1234, Thinking: ai.ThinkingHigh}, system: "You are helpful.", conservative: true},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-empty-tools.test.ts:200
		{name: "resolves Cloudflare AI Gateway base URL through provider auth", model: "workers-ai/@cf/moonshotai/kimi-k2.6"},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-empty-tools.test.ts:214
		{name: "preserves inline upstream Authorization for Cloudflare AI Gateway BYOK requests", model: "gpt-5.1", options: ai.StreamOptions{Headers: ai.ProviderHeadersFromStrings(map[string]string{"Authorization": "Bearer upstream-token"})}},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-empty-tools.test.ts:233
		{name: "sends session affinity headers for Workers AI through Cloudflare AI Gateway", model: "workers-ai/@cf/moonshotai/kimi-k2.6", options: ai.StreamOptions{SessionID: "session-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CLOUDFLARE_API_KEY", "cf-token")
			t.Setenv("CLOUDFLARE_ACCOUNT_ID", "account-id")
			t.Setenv("CLOUDFLARE_GATEWAY_ID", "gateway-id")
			captured := captureCompletionsRuntime(t, "cloudflare-ai-gateway", tc.model, ai.Context{SystemPrompt: tc.system, Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}, tc.options, 0, 0)
			if captured.Headers.Get("cf-aig-authorization") != "Bearer cf-token" {
				t.Fatalf("auth=%#v", captured.Headers)
			}
			wantAuthorization := ""
			if tc.options.Headers != nil {
				wantAuthorization = "Bearer upstream-token"
			}
			if got := captured.Headers.Get("Authorization"); got != wantAuthorization {
				t.Fatalf("Authorization=%q want=%q", got, wantAuthorization)
			}
			if tc.model != "gpt-5.1" {
				if captured.Path != "/v1/account-id/gateway-id/compat/chat/completions" {
					t.Fatalf("path=%s", captured.Path)
				}
				actual, err := ai.ResolveCloudflareBaseURL("cloudflare-ai-gateway", ai.CloudflareAIGatewayCompatBaseURL, ai.ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "account-id", "CLOUDFLARE_GATEWAY_ID": "gateway-id"})
				if err != nil || actual != "https://gateway.ai.cloudflare.com/v1/account-id/gateway-id/compat" {
					t.Fatalf("baseURL=%q err=%v", actual, err)
				}
			}
			if tc.conservative {
				for _, key := range []string{"max_completion_tokens", "reasoning_effort", "store"} {
					if _, ok := captured.Payload[key]; ok {
						t.Errorf("unexpected %s=%s", key, captured.Payload[key])
					}
				}
				if string(captured.Payload["max_tokens"]) != "1234" {
					t.Errorf("max_tokens=%s", captured.Payload["max_tokens"])
				}
				var messages []struct {
					Role string `json:"role"`
				}
				if err := json.Unmarshal(captured.Payload["messages"], &messages); err != nil {
					t.Fatal(err)
				}
				if len(messages) == 0 || messages[0].Role != "system" {
					t.Fatalf("messages=%#v", messages)
				}
			}
			if tc.options.SessionID != "" {
				for _, key := range []string{"session_id", "x-client-request-id", "x-session-affinity"} {
					if captured.Headers.Get(key) != "session-1" {
						t.Errorf("header %s=%q", key, captured.Headers.Get(key))
					}
				}
			}
		})
	}
}
