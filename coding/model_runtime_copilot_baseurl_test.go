package coding

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// TestModelRuntimeCopilotOAuthProxyEndpoint is a regression test for Copilot
// Business/Enterprise routing through the production ModelRuntime request
// path: upstream githubCopilotOAuth.toAuth derives baseUrl from the access
// token's proxy-ep claim and ModelRuntime sends the model with that baseUrl
// (.upstream/v0.87.1/packages/ai/src/auth/oauth/github-copilot.ts:501,
// packages/coding-agent/src/core/model-runtime.ts:601). The resolved OAuth
// access token must not be treated as an --api-key that pins the catalog's
// individual endpoint.
func TestModelRuntimeCopilotOAuthProxyEndpoint(t *testing.T) {
	for _, api := range []ai.API{ai.APIOpenAIResponses, ai.APIAnthropicMessages, ai.APIOpenAICompletions} {
		t.Run(string(api), func(t *testing.T) {
			services := newRuntimeTestServices(t)
			access := "tid=test;exp=9999999999;proxy-ep=proxy.enterprise.githubcopilot.com;"
			if err := services.Auth().Set("github-copilot", ai.Credential{Type: ai.CredentialOAuth, Refresh: "github-access-token", Access: access, Expires: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
				t.Fatal(err)
			}
			var model *ai.Model
			for _, candidate := range services.ModelRuntime().GetModels() {
				if candidate.ProviderMeta.ProviderID == "github-copilot" && candidate.ProviderMeta.API == api {
					model = candidate
					break
				}
			}
			if model == nil {
				t.Fatalf("catalog has no github-copilot %s model", api)
			}
			var host, authz string
			fetch := &http.Client{Transport: cloudflareCompatTransport(func(request *http.Request) (*http.Response, error) {
				host, authz = request.URL.Host, request.Header.Get("Authorization")
				return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"stop"}}`))}, nil
			})}
			services.ModelRuntime().CompleteSimple(t.Context(), model, ai.Context{Messages: []ai.Message{}}, ai.StreamOptions{Fetch: fetch})
			if host != "api.enterprise.githubcopilot.com" {
				t.Fatalf("request host=%q, want api.enterprise.githubcopilot.com (from the token's proxy-ep)", host)
			}
			if authz != "Bearer "+access {
				t.Fatalf("Authorization does not carry the stored Copilot access token")
			}
		})
	}
}

// TestModelRuntimeCopilotAnthropicAdaptiveThinking asserts the Copilot
// Anthropic path keeps the selected model's compat: upstream
// anthropic-messages sends thinking.type "adaptive" when
// model.compat.forceAdaptiveThinking is set (github-copilot.json claude-opus-5).
func TestModelRuntimeCopilotAnthropicAdaptiveThinking(t *testing.T) {
	services := newRuntimeTestServices(t)
	access := "tid=test;exp=9999999999;proxy-ep=proxy.enterprise.githubcopilot.com;"
	if err := services.Auth().Set("github-copilot", ai.Credential{Type: ai.CredentialOAuth, Refresh: "github-access-token", Access: access, Expires: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	var model *ai.Model
	for _, candidate := range services.ModelRuntime().GetModels() {
		compat := candidate.ProviderMeta.Compat
		if candidate.ProviderMeta.ProviderID == "github-copilot" && candidate.ProviderMeta.API == ai.APIAnthropicMessages && compat != nil && compat.ForceAdaptiveThinking != nil && *compat.ForceAdaptiveThinking {
			model = candidate
			break
		}
	}
	if model == nil {
		t.Fatal("catalog has no github-copilot adaptive-thinking Anthropic model")
	}
	var params struct {
		Thinking struct {
			Type string `json:"type"`
		} `json:"thinking"`
	}
	fetch := &http.Client{Transport: cloudflareCompatTransport(func(request *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(request.Body)
		_ = json.Unmarshal(data, &params)
		return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"stop"}}`))}, nil
	})}
	services.ModelRuntime().CompleteSimple(t.Context(), model, ai.Context{Messages: []ai.Message{}}, ai.StreamOptions{Fetch: fetch, Thinking: ai.ThinkingLevelHigh})
	if params.Thinking.Type != "adaptive" {
		t.Fatalf("%s thinking.type=%q, want adaptive", model.ID, params.Thinking.Type)
	}
}

// TestModelRuntimeCopilotCompletionsCompat asserts the Copilot
// openai-completions leaf keeps the selected model's compat. Upstream
// getCompat overlays model.compat on detectCompat
// (.upstream/v0.87.1/packages/ai/src/api/openai-completions.ts:1688), and
// github-copilot.json gives gemini-3.5-flash supportsStore, supportsDeveloperRole
// and supportsReasoningEffort false with supportsStrictMode true: the request
// uses the system role, omits store and reasoning_effort, and marks tools
// strict:false (openai-completions.ts:832, 962, 1225, 1505).
func TestModelRuntimeCopilotCompletionsCompat(t *testing.T) {
	services := newRuntimeTestServices(t)
	access := "tid=test;exp=9999999999;proxy-ep=proxy.enterprise.githubcopilot.com;"
	if err := services.Auth().Set("github-copilot", ai.Credential{Type: ai.CredentialOAuth, Refresh: "github-access-token", Access: access, Expires: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	model := services.ModelRuntime().GetModel("github-copilot", "gemini-3.5-flash")
	if model == nil || model.ProviderMeta.API != ai.APIOpenAICompletions {
		t.Fatal("catalog has no github-copilot gemini-3.5-flash openai-completions model")
	}
	var body map[string]any
	fetch := &http.Client{Transport: cloudflareCompatTransport(func(request *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(request.Body)
		_ = json.Unmarshal(data, &body)
		return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"stop"}}`))}, nil
	})}
	request := ai.Context{
		SystemPrompt: "system",
		Messages:     []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}},
		Tools:        []ai.ToolSchema{{Name: "lookup", Description: "Look up", Parameters: map[string]any{"type": "object"}}},
	}
	services.ModelRuntime().CompleteSimple(t.Context(), model, request, ai.StreamOptions{Fetch: fetch, Thinking: ai.ThinkingLevelHigh})
	if body == nil {
		t.Fatal("no request body captured")
	}
	if _, ok := body["store"]; ok {
		t.Errorf("store=%v, want omitted (supportsStore false)", body["store"])
	}
	if _, ok := body["reasoning_effort"]; ok {
		t.Errorf("reasoning_effort=%v, want omitted (supportsReasoningEffort false)", body["reasoning_effort"])
	}
	messages, _ := body["messages"].([]any)
	if len(messages) == 0 {
		t.Fatal("request has no messages")
	}
	if role := messages[0].(map[string]any)["role"]; role != "system" {
		t.Errorf("instruction role=%v, want system (supportsDeveloperRole false)", role)
	}
	tools, _ := body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools=%v, want one tool", body["tools"])
	}
	if strict, ok := tools[0].(map[string]any)["function"].(map[string]any)["strict"]; !ok || strict != false {
		t.Errorf("tool strict=%v (present=%v), want false (supportsStrictMode true)", strict, ok)
	}
}
