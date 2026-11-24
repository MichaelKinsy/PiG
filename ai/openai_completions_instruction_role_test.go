package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Tests for Pi b82477b7b (#12134, closes #10741 #12101):
// On openai-completions, reasoning models default to the "system" instruction role
// (for both the initial system prompt and mid-conversation system messages) unless
// provider == "openai" or baseUrl includes "api.openai.com", or compat.supportsDeveloperRole
// explicitly overrides.

func captureInstructionRoles(t *testing.T, model *Model, ctx Context) []string {
	t.Helper()
	payload, _, _ := captureToolChoiceRequest(t, model, ctx, StreamOptions{}, nil)
	var messages []struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(payload["messages"], &messages); err != nil {
		t.Fatalf("unmarshal messages=%s: %v", payload["messages"], err)
	}
	roles := make([]string, len(messages))
	for i, m := range messages {
		roles[i] = m.Role
	}
	return roles
}

func TestOpenAICompletionsInstructionRoleDefaultsToSystem(t *testing.T) {
	hi := func() Context {
		ctx := toolChoiceHi()
		ctx.SystemPrompt = "Follow instructions."
		return ctx
	}

	t.Run("api.openai.com API-key reasoning model uses developer", func(t *testing.T) {
		model := toolChoiceModel(t, "openai", "gpt-5.5", true)
		roles := captureInstructionRoles(t, model, hi())
		if len(roles) < 1 || roles[0] != "developer" {
			t.Fatalf("roles=%v, want first role developer", roles)
		}
		// Custom provider ID pointing at api.openai.com also gets developer.
		byURL := localToolChoiceModel("gpt-5.5", "GPT-5.5 via OpenAI URL")
		byURL.ProviderMeta.ProviderID = "custom-openai"
		byURL.ProviderMeta.BaseURL = "https://api.openai.com/v1"
		roles = captureInstructionRoles(t, byURL, hi())
		if len(roles) < 1 || roles[0] != "developer" {
			t.Fatalf("api.openai.com URL roles=%v, want first role developer", roles)
		}
	})

	t.Run("OpenAI-compatible hosts default to system for reasoning models", func(t *testing.T) {
		groqQwen := toolChoiceModel(t, "groq", "qwen/qwen3.8-27b", false)
		customHost := localToolChoiceModel("qwen3.8-27b", "Qwen3.8 27B")
		openRouterDeepSeek := toolChoiceModel(t, "openrouter", "deepseek/deepseek-v4-pro", false)
		openRouterOpenAI := toolChoiceModel(t, "openrouter", "openai/gpt-5.2-codex", false)
		openRouterAnthropic := toolChoiceModel(t, "openrouter", "anthropic/claude-fable-5.1:batch", false)
		azureBaseURL := "https://my-resource.openai.azure.com/openai/v1"
		azureDeepSeek := toolChoiceModel(t, "azure", "deepseek-v4-pro", false)
		azureDeepSeek.ProviderMeta.BaseURL = azureBaseURL
		azureHost := localToolChoiceModel("gpt-5.5", "GPT-5.5")
		azureHost.ProviderMeta.ProviderID = "my-azure"
		azureHost.ProviderMeta.BaseURL = azureBaseURL
		xiaomi := toolChoiceModel(t, "xiaomi", "mimo-v2.6-pro", false)

		for _, tc := range []struct {
			name  string
			model *Model
		}{
			{"groq qwen3.8-27b", groqQwen},
			{"custom host localhost", customHost},
			{"openrouter deepseek", openRouterDeepSeek},
			{"openrouter openai", openRouterOpenAI},
			{"openrouter anthropic", openRouterAnthropic},
			{"azure deepseek", azureDeepSeek},
			{"azure custom host", azureHost},
			{"xiaomi mimo", xiaomi},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if tc.model.ProviderMeta.API != APIOpenAICompletions || !tc.model.ProviderMeta.Reasoning {
					t.Fatalf("model api=%s reasoning=%v", tc.model.ProviderMeta.API, tc.model.ProviderMeta.Reasoning)
				}
				roles := captureInstructionRoles(t, tc.model, hi())
				if len(roles) < 1 || roles[0] != "system" {
					t.Fatalf("roles=%v, want first role system", roles)
				}
			})
		}
	})

	t.Run("compat.supportsDeveloperRole overrides default each way", func(t *testing.T) {
		// Override true on custom host -> developer
		customTrue := localToolChoiceModel("qwen3.8-27b", "Custom True")
		customTrue.ProviderMeta.Compat = &ModelCompat{SupportsDeveloperRole: new(true)}
		if roles := captureInstructionRoles(t, customTrue, hi()); roles[0] != "developer" {
			t.Fatalf("custom compat=true roles=%v, want developer", roles)
		}

		// Override false on api.openai.com -> system
		openaiFalse := toolChoiceModel(t, "openai", "gpt-5.5", true)
		openaiFalse.ProviderMeta.Compat = &ModelCompat{SupportsDeveloperRole: new(false)}
		if roles := captureInstructionRoles(t, openaiFalse, hi()); roles[0] != "system" {
			t.Fatalf("openai compat=false roles=%v, want system", roles)
		}

		// Mid-conversation system messages follow the same resolved role.
		midCtx := Context{
			SystemPrompt: "Initial instruction.",
			Messages: []Message{
				UserMessage{Content: UserText("First")},
				AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "Reply"}}},
				SystemMessage{Content: SystemText("Mid-convo update.")},
				UserMessage{Content: UserText("Second")},
			},
		}
		customMid := localToolChoiceModel("gpt-5.5", "Custom Mid")
		customMid.ProviderMeta.Compat = &ModelCompat{SupportsMidConvoSystemMessages: new(true)}
		roles := captureInstructionRoles(t, customMid, midCtx)
		if len(roles) != 5 || roles[0] != "system" || roles[3] != "system" {
			t.Fatalf("custom mid-convo roles=%v, want [system user assistant system user]", roles)
		}
		customMidDev := localToolChoiceModel("gpt-5.5", "Custom Mid Dev")
		customMidDev.ProviderMeta.Compat = &ModelCompat{SupportsMidConvoSystemMessages: new(true), SupportsDeveloperRole: new(true)}
		roles = captureInstructionRoles(t, customMidDev, midCtx)
		if len(roles) != 5 || roles[0] != "developer" || roles[3] != "developer" {
			t.Fatalf("custom mid-convo compat=true roles=%v, want [developer user assistant developer user]", roles)
		}
	})

	t.Run("OAuth provider on completions path uses system", func(t *testing.T) {
		// GitHub Copilot is an OAuth provider with openai-completions models.
		model := localToolChoiceModel("gemini-3.8-flash", "Copilot Completions")
		model.ProviderMeta.ProviderID = "github-copilot"
		model.ProviderMeta.BaseURL = "https://api.individual.githubcopilot.com"
		model.ProviderMeta.Compat = nil
		roles := captureInstructionRoles(t, model, hi())
		if len(roles) < 1 || roles[0] != "system" {
			t.Fatalf("github-copilot roles=%v, want system", roles)
		}
	})

	t.Run("model with no provider or default compat", func(t *testing.T) {
		// OpenAIConfig with empty ProviderID and empty BaseURL defaults to openai / https://api.openai.com/v1
		// (matching prompt_cache_key via isOpenAIDirectURL), so it counts as OpenAI direct -> developer.
		bare := &Model{
			ID:           "bare-reasoning",
			ProviderMeta: ProviderMetadata{API: APIOpenAICompletions, Reasoning: true},
			Capabilities: ModelCapabilities{MaxOutputTokens: 1024},
		}
		roles := captureInstructionRoles(t, bare, hi())
		if len(roles) < 1 || roles[0] != "developer" {
			t.Fatalf("bare model (defaulted openai / api.openai.com) roles=%v, want developer", roles)
		}
		// Custom provider with no catalog default and a non-OpenAI BaseURL -> system.
		noDefault := &Model{
			ID:           "bare-reasoning",
			ProviderMeta: ProviderMetadata{API: APIOpenAICompletions, ProviderID: "custom-no-default", BaseURL: "https://example.invalid/v1", Reasoning: true},
			Capabilities: ModelCapabilities{MaxOutputTokens: 1024},
		}
		roles = captureInstructionRoles(t, noDefault, hi())
		if len(roles) < 1 || roles[0] != "system" {
			t.Fatalf("no-default custom model roles=%v, want system", roles)
		}
		// Direct detectOpenAICompletionsCompat with empty provider and empty baseURL -> false (system).
		if *detectOpenAICompletionsCompat("", "", "bare-reasoning").SupportsDeveloperRole {
			t.Fatalf("detectOpenAICompletionsCompat(\"\", \"\", ...) = true, want false")
		}
	})

	t.Run("poisoned history is unchanged", func(t *testing.T) {
		poisoned := Context{
			SystemPrompt: "Follow instructions.",
			Messages: []Message{
				UserMessage{Content: UserText("First")},
				AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: ""}}, StopReason: StopReasonError, ErrorMessage: "boom"},
				AssistantMessage{Content: nil, StopReason: StopReasonAborted},
				UserMessage{Content: UserText("Retry")},
			},
		}
		customHost := localToolChoiceModel("qwen3.8-27b", "Custom Poisoned")
		roles := captureInstructionRoles(t, customHost, poisoned)
		if len(roles) != 3 || roles[0] != "system" || roles[1] != "user" || roles[2] != "user" {
			t.Fatalf("poisoned history roles=%v, want [system user user]", roles)
		}
	})

	t.Run("openai-responses is unchanged and still defaults to developer", func(t *testing.T) {
		// Shared-path matrix (AGENTS.md): openai-responses keeps developer default for reasoning models.
		var captured map[string]json.RawMessage
		p := NewOpenAIResponsesProvider(OpenAIResponsesConfig{
			BaseURL:     "https://my-proxy.example/v1",
			APIKey:      "test",
			Model:       "o3-mini",
			ProviderID:  "custom-responses",
			IsReasoning: true,
		}).(*openAIResponsesProvider)
		p.client = &http.Client{Transport: openAITestRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				return nil, err
			}
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		})}
		stream, err := p.Stream(t.Context(), NormalizeContext(hi()), StreamOptions{IsReasoning: true})
		if err != nil {
			t.Fatal(err)
		}
		for range stream.Events(t.Context()) {
		}
		if len(captured) == 0 {
			t.Fatal("did not capture openai-responses request")
		}
		var input []struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(captured["input"], &input); err != nil || len(input) == 0 {
			t.Fatalf("responses input=%s err=%v", captured["input"], err)
		}
		if input[0].Role != "developer" {
			t.Fatalf("openai-responses first input role=%q, want developer", input[0].Role)
		}
	})
}

// TestOpenAICompletionsInstructionRoleRealPathsWithoutCatalogCompat exercises the real provider
// constructors (NewCopilotProvider, directAPIProvider for Azure, and LookupBuiltInProvider)
// with a compat that LACKS SupportsDeveloperRole (matching the regenerated catalog after Pi b82477b7b).
// Each must detect from the model's own provider ID and base URL as given (before NewOpenAIProvider
// defaults empty BaseURL to api.openai.com and empty ProviderID to "openai"), and serialize role "system".
func TestOpenAICompletionsInstructionRoleRealPathsWithoutCatalogCompat(t *testing.T) {
	hi := func() Context {
		return Context{
			SystemPrompt: "Follow instructions.",
			Messages:     []Message{UserMessage{Content: UserText("Hi"), Timestamp: 1}},
		}
	}

	assertFirstRoleSystem := func(t *testing.T, body []byte) {
		t.Helper()
		var req struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal serialized body=%s: %v", string(body), err)
		}
		if len(req.Messages) == 0 || req.Messages[0].Role != "system" {
			t.Fatalf("serialized messages=%+v (body=%s), want first role system", req.Messages, string(body))
		}
	}

	t.Run("NewCopilotProvider Chat Completions reasoning model without SupportsDeveloperRole sends system", func(t *testing.T) {
		auth, err := NewAuthStorage(t.TempDir() + "/auth.json")
		if err != nil {
			t.Fatal(err)
		}
		access := "tid=test;exp=9999999999;proxy-ep=proxy.enterprise.githubcopilot.com;"
		if err := auth.Set("github-copilot", Credential{
			Type:    CredentialOAuth,
			Refresh: "ghu",
			Access:  access,
			Expires: 9999999999000,
		}); err != nil {
			t.Fatal(err)
		}
		// Clone the built-in catalog model and clear SupportsDeveloperRole to match the post-regeneration catalog.
		generated, ok := LookupModelExact("github-copilot/gemini-3.8-flash")
		if !ok {
			t.Fatal("missing catalog model github-copilot/gemini-3.8-flash")
		}
		model := generated.ToModel()
		if model.ProviderMeta.Compat != nil {
			compatCopy := *model.ProviderMeta.Compat
			compatCopy.SupportsDeveloperRole = nil
			model.ProviderMeta.Compat = &compatCopy
		}
		provider, err := NewCopilotProvider(CopilotProviderConfig{
			Auth:          auth,
			Model:         "gemini-3.8-flash",
			API:           APIOpenAICompletions,
			Reasoning:     true,
			ModelMetadata: model,
		})
		if err != nil {
			t.Fatal(err)
		}
		var body []byte
		fetch := &http.Client{Transport: openAITestRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			var readErr error
			body, readErr = io.ReadAll(r.Body)
			if readErr != nil {
				return nil, readErr
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")),
			}, nil
		})}
		stream, err := provider.Stream(t.Context(), NormalizeContext(hi()), StreamOptions{Fetch: fetch, IsReasoning: true})
		if err != nil {
			t.Fatal(err)
		}
		for range stream.Events(t.Context()) {
		}
		if len(body) == 0 {
			t.Fatal("did not capture NewCopilotProvider serialized request body")
		}
		assertFirstRoleSystem(t, body)
	})

	t.Run("Azure direct provider with empty model BaseURL and AZURE_OPENAI_BASE_URL sends system", func(t *testing.T) {
		t.Setenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP", "")
		t.Setenv("AZURE_OPENAI_RESOURCE_NAME", "")
		t.Setenv("AZURE_OPENAI_BASE_URL", "https://my-resource.services.ai.azure.com")

		generated, ok := LookupModelExact("azure/deepseek-v4-pro")
		if !ok {
			t.Fatal("missing catalog model azure/deepseek-v4-pro")
		}
		model := generated.ToModel()
		model.ProviderMeta.BaseURL = ""
		if model.ProviderMeta.Compat != nil {
			compatCopy := *model.ProviderMeta.Compat
			compatCopy.SupportsDeveloperRole = nil
			model.ProviderMeta.Compat = &compatCopy
		}
		provider, err := directAPIProvider(model, "test-key", nil)
		if err != nil {
			t.Fatal(err)
		}
		var body []byte
		fetch := &http.Client{Transport: openAITestRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			var readErr error
			body, readErr = io.ReadAll(r.Body)
			if readErr != nil {
				return nil, readErr
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")),
			}, nil
		})}
		stream, err := provider.Stream(t.Context(), NormalizeContext(hi()), StreamOptions{
			Fetch:           fetch,
			APIKey:          "test-key",
			ReasoningEffort: "low",
			IsReasoning:     true,
		})
		if err != nil {
			t.Fatal(err)
		}
		for range stream.Events(t.Context()) {
		}
		if len(body) == 0 {
			t.Fatal("did not capture Azure directAPIProvider serialized request body")
		}
		assertFirstRoleSystem(t, body)
	})

	t.Run("LookupBuiltInProvider omits ProviderID and sends system on third-party hosts", func(t *testing.T) {
		factory, ok := LookupBuiltInProvider(APIOpenAICompletions)
		if !ok {
			t.Fatal("LookupBuiltInProvider(APIOpenAICompletions) not found")
		}
		hosts := []struct {
			name    string
			baseURL string
		}{
			{"x.ai", "https://api.x.ai/v1"},
			{"deepseek", "https://api.deepseek.com/v1"},
			{"cerebras", "https://api.cerebras.ai/v1"},
			{"together", "https://api.together.xyz/v1"},
			{"moonshot", "https://api.moonshot.ai/v1"},
			{"z.ai", "https://api.z.ai/api/paas/v4"},
			{"openrouter", "https://openrouter.ai/api/v1"},
		}
		for _, tc := range hosts {
			t.Run(tc.name, func(t *testing.T) {
				provider := factory("test-key", "reasoning-model", tc.baseURL)
				var body []byte
				fetch := &http.Client{Transport: openAITestRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
					var readErr error
					body, readErr = io.ReadAll(r.Body)
					if readErr != nil {
						return nil, readErr
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
						Body:       io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")),
					}, nil
				})}
				stream, err := provider.Stream(t.Context(), NormalizeContext(hi()), StreamOptions{
					Fetch:       fetch,
					IsReasoning: true,
				})
				if err != nil {
					t.Fatal(err)
				}
				for range stream.Events(t.Context()) {
				}
				if len(body) == 0 {
					t.Fatalf("%s: did not capture serialized request body", tc.name)
				}
				assertFirstRoleSystem(t, body)
			})
		}
	})

	t.Run("withEndpoint preserves pre-default compat identity when ProviderID is empty", func(t *testing.T) {
		provider := WrapBuiltinProvider("cloudflare-workers-ai", NewOpenAIProvider(OpenAIConfig{
			BaseURL: "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai/v1",
			APIKey:  "test-key",
			Model:   "reasoning-model",
			Env:     ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "acct"},
			ModelMetadata: &Model{
				ID:           "reasoning-model",
				ProviderMeta: ProviderMetadata{API: APIOpenAICompletions, Reasoning: true},
			},
		}))
		var body []byte
		fetch := &http.Client{Transport: openAITestRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			var readErr error
			body, readErr = io.ReadAll(r.Body)
			if readErr != nil {
				return nil, readErr
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")),
			}, nil
		})}
		stream, err := provider.Stream(t.Context(), NormalizeContext(hi()), StreamOptions{
			Fetch:       fetch,
			IsReasoning: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		for range stream.Events(t.Context()) {
		}
		if len(body) == 0 {
			t.Fatal("did not capture WrapBuiltinProvider serialized request body")
		}
		assertFirstRoleSystem(t, body)
	})

	t.Run("Azure direct provider detects compat from resolved AZURE_OPENAI_BASE_URL like Pi", func(t *testing.T) {
		t.Setenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP", "")
		t.Setenv("AZURE_OPENAI_RESOURCE_NAME", "")
		t.Setenv("AZURE_OPENAI_BASE_URL", "https://api.openai.com/v1")

		generated, ok := LookupModelExact("azure/deepseek-v4-pro")
		if !ok {
			t.Fatal("missing catalog model azure/deepseek-v4-pro")
		}
		model := generated.ToModel()
		model.ProviderMeta.BaseURL = ""
		if model.ProviderMeta.Compat != nil {
			compatCopy := *model.ProviderMeta.Compat
			compatCopy.SupportsDeveloperRole = nil
			model.ProviderMeta.Compat = &compatCopy
		}
		provider, err := directAPIProvider(model, "test-key", nil)
		if err != nil {
			t.Fatal(err)
		}
		var body []byte
		fetch := &http.Client{Transport: openAITestRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			var readErr error
			body, readErr = io.ReadAll(r.Body)
			if readErr != nil {
				return nil, readErr
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")),
			}, nil
		})}
		stream, err := provider.Stream(t.Context(), NormalizeContext(hi()), StreamOptions{
			Fetch:           fetch,
			APIKey:          "test-key",
			ReasoningEffort: "low",
			IsReasoning:     true,
		})
		if err != nil {
			t.Fatal(err)
		}
		for range stream.Events(t.Context()) {
		}
		if len(body) == 0 {
			t.Fatal("did not capture Azure resolved-URL request body")
		}
		var req struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal serialized body=%s: %v", string(body), err)
		}
		if len(req.Messages) == 0 || req.Messages[0].Role != "developer" {
			t.Fatalf("serialized messages=%+v (body=%s), want first role developer for resolved api.openai.com URL", req.Messages, string(body))
		}
	})
}
