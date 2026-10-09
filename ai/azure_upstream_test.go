package ai

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// captureUpstreamPayload stops at the same onPayload boundary used by Pi's provider tests.
func captureUpstreamPayload(t *testing.T, provider Provider, transcript Context, options StreamOptions) map[string]json.RawMessage {
	t.Helper()
	defer func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	}()
	return captureUpstreamPayloadCall(t, func(options StreamOptions) (*AssistantMessageEventStream, error) {
		return provider.Stream(t.Context(), NormalizeContext(transcript), options)
	}, options)
}

func captureUpstreamPayloadCall(t *testing.T, call func(StreamOptions) (*AssistantMessageEventStream, error), options StreamOptions) map[string]json.RawMessage {
	t.Helper()
	captured := errors.New("payload captured")
	var payload map[string]json.RawMessage
	options.OnPayload = func(value any, _ *Model) (any, error) {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(encoded, &payload); err != nil {
			return nil, err
		}
		return nil, captured
	}
	stream, err := call(options)
	if err != nil && !errors.Is(err, captured) {
		t.Fatal(err)
	}
	if stream != nil {
		stream.Result()
	}
	if payload == nil {
		t.Fatal("onPayload was not called")
	}
	return payload
}

func azureUpstreamContext() Context {
	return Context{Messages: []Message{UserMessage{Content: UserText("Summarize this"), Timestamp: 1}}, Tools: []ToolSchema{{
		Name: "read", Description: "Read a file", Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}},
	}}}
}

func TestAzureUpstreamToolChoice(t *testing.T) {
	for _, tc := range []struct{ name, choice string }{
		// .upstream/v0.87.1/packages/ai/test/azure-openai-tool-choice.test.ts:21
		{"forwards provider-specific tool choice while preserving tool definitions", "required"},
		// .upstream/v0.87.1/packages/ai/test/azure-openai-tool-choice.test.ts:50
		{"forwards provider-neutral tool choice from simple options", "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var payload map[string]json.RawMessage
			if tc.choice == "none" {
				model := &Model{ID: "test-deployment", DisplayName: "Test Deployment", ProviderMeta: ProviderMetadata{API: APIAzureOpenAIResponses, ProviderID: "azure-openai-responses", BaseURL: "http://127.0.0.1:9/openai/v1"}, Capabilities: ModelCapabilities{ContextWindow: 10_000, MaxOutputTokens: 1_000}}
				payload = captureUpstreamPayloadCall(t, func(options StreamOptions) (*AssistantMessageEventStream, error) {
					return StreamSimple(t.Context(), model, NormalizeContext(azureUpstreamContext()), options)
				}, StreamOptions{APIKey: "test-key", ToolChoice: tc.choice})
			} else {
				payload = captureUpstreamPayload(t, NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{Model: "test-deployment", APIKey: "test-key", BaseURL: "http://127.0.0.1:9/openai/v1"}), azureUpstreamContext(), StreamOptions{ToolChoice: tc.choice})
			}
			assertShapeJSON(t, payload["tool_choice"], `"`+tc.choice+`"`)
			var tools []json.RawMessage
			if err := json.Unmarshal(payload["tools"], &tools); err != nil {
				t.Fatal(err)
			}
			if len(tools) != 1 {
				t.Fatalf("tools = %s", payload["tools"])
			}
		})
	}
}

// packages/ai/src/api/azure-openai-responses.ts:332 and openai-responses.ts:340 forward the complete provider-specific tool-choice union, not only its string members.
func TestAzureAndOpenAIResponsesObjectToolChoice(t *testing.T) {
	for _, providerID := range []string{"azure-openai-responses", "openai"} {
		t.Run(providerID, func(t *testing.T) {
			var provider Provider
			if providerID == "azure-openai-responses" {
				provider = NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{Model: "test-deployment", APIKey: "test-key", BaseURL: "http://127.0.0.1:9/openai/v1"})
			} else {
				provider = NewOpenAIResponsesProvider(OpenAIResponsesConfig{Model: "test-model", APIKey: "test-key", BaseURL: "http://127.0.0.1:9/v1"})
			}
			payload := captureUpstreamPayload(t, provider, azureUpstreamContext(), StreamOptions{ToolChoice: map[string]string{"type": "function", "name": "read"}})
			if _, present := payload["tool_choice"]; !present {
				t.Fatal("provider-specific object tool_choice was omitted")
			}
			assertShapeJSON(t, payload["tool_choice"], `{"type":"function","name":"read"}`)
		})
	}
}

func TestAzureUpstreamBaseURL(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		// .upstream/v0.87.1/packages/ai/test/azure-openai-base-url.test.ts:110
		{"normalizes Cognitive Services root endpoints to /openai/v1", "https://marc-quicktests-resource.cognitiveservices.azure.com", "https://marc-quicktests-resource.cognitiveservices.azure.com/openai/v1"},
		// .upstream/v0.87.1/packages/ai/test/azure-openai-base-url.test.ts:115
		{"normalizes Microsoft Foundry root endpoints to /openai/v1", "https://marc-quicktests-resource.ai.azure.com", "https://marc-quicktests-resource.ai.azure.com/openai/v1"},
		// .upstream/v0.87.1/packages/ai/test/azure-openai-base-url.test.ts:120
		{"normalizes Azure OpenAI root endpoints to /openai/v1", "https://my-resource.openai.azure.com", "https://my-resource.openai.azure.com/openai/v1"},
		// .upstream/v0.87.1/packages/ai/test/azure-openai-base-url.test.ts:125
		{"normalizes /openai to /openai/v1", "https://my-resource.cognitiveservices.azure.com/openai", "https://my-resource.cognitiveservices.azure.com/openai/v1"},
		// .upstream/v0.87.1/packages/ai/test/azure-openai-base-url.test.ts:130
		{"preserves /openai/v1 endpoints", "https://my-resource.cognitiveservices.azure.com/openai/v1", "https://my-resource.cognitiveservices.azure.com/openai/v1"},
		// .upstream/v0.87.1/packages/ai/test/azure-openai-base-url.test.ts:135
		{"normalizes /openai/v1/responses to /openai/v1", "https://my-resource.services.ai.azure.com/openai/v1/responses", "https://my-resource.services.ai.azure.com/openai/v1"},
		// .upstream/v0.87.1/packages/ai/test/azure-openai-base-url.test.ts:140
		{"preserves explicit non-Azure proxy paths", "https://my-proxy.example.com/v1", "https://my-proxy.example.com/v1"},
		// .upstream/v0.87.1/packages/ai/test/azure-openai-base-url.test.ts:145
		{"strips query params when normalizing Azure host URLs", "https://my-resource.openai.azure.com/openai?api-version=2024-12-01", "https://my-resource.openai.azure.com/openai/v1"},
		// .upstream/v0.87.1/packages/ai/test/azure-openai-base-url.test.ts:150
		{"preserves query params on non-Azure proxy URLs", "https://my-proxy.example.com/v1?custom=true", "https://my-proxy.example.com/v1?custom=true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AZURE_OPENAI_BASE_URL", tc.input)
			got, err := ResolveAzureBaseURL("", StreamOptions{})
			if err != nil || got != tc.want {
				t.Fatalf("baseURL = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/azure-openai-base-url.test.ts:155
	t.Run("throws on invalid URLs", func(t *testing.T) {
		t.Setenv("AZURE_OPENAI_BASE_URL", "not-a-url")
		p := NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{Model: "gpt-4o-mini", APIKey: "test-api-key"})
		_, err := p.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{})
		if err == nil || !strings.Contains(err.Error(), "Invalid Azure OpenAI base URL") {
			t.Fatalf("error = %v", err)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/azure-openai-base-url.test.ts:210
	t.Run("builds correct default URL from AZURE_OPENAI_RESOURCE_NAME", func(t *testing.T) {
		t.Setenv("AZURE_OPENAI_BASE_URL", "")
		t.Setenv("AZURE_OPENAI_RESOURCE_NAME", "my-resource")
		got, err := ResolveAzureBaseURL("", StreamOptions{})
		if err != nil || got != "https://my-resource.openai.azure.com/openai/v1" {
			t.Fatalf("baseURL = %q, %v", got, err)
		}
	})
}

func TestAzureUpstreamPayload(t *testing.T) {
	config := AzureOpenAIResponsesConfig{Model: "gpt-4o-mini", APIKey: "test-api-key", BaseURL: "https://my-resource.openai.azure.com"}
	// .upstream/v0.87.1/packages/ai/test/azure-openai-base-url.test.ts:165
	t.Run("clamps prompt_cache_key to OpenAI's 64-character limit", func(t *testing.T) {
		payload := captureUpstreamPayload(t, NewAzureOpenAIResponsesProvider(config), azureUpstreamContext(), StreamOptions{SessionID: strings.Repeat("x", 67)})
		assertShapeJSON(t, payload["prompt_cache_key"], `"`+strings.Repeat("x", 64)+`"`)
	})
	// .upstream/v0.87.1/packages/ai/test/azure-openai-base-url.test.ts:176
	t.Run("disables server-side response storage", func(t *testing.T) {
		payload := captureUpstreamPayload(t, NewAzureOpenAIResponsesProvider(config), azureUpstreamContext(), StreamOptions{})
		assertShapeJSON(t, payload["store"], "false")
	})
	// .upstream/v0.87.1/packages/ai/test/azure-openai-base-url.test.ts:187
	t.Run("honors supportsStrictMode: false", func(t *testing.T) {
		config.Compat = &OpenAIResponsesCompat{SupportsStrictMode: new(false)}
		ctx := Context{Messages: []Message{UserMessage{Content: UserText("hello")}}, Tools: []ToolSchema{{Name: "preferred", Description: "Preferred constrained tool", Parameters: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []string{"value"}}, ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"}}}}
		payload := captureUpstreamPayload(t, NewAzureOpenAIResponsesProvider(config), ctx, StreamOptions{})
		var tools []map[string]json.RawMessage
		if err := json.Unmarshal(payload["tools"], &tools); err != nil {
			t.Fatal(err)
		}
		if len(tools) != 1 {
			t.Fatalf("tools = %s", payload["tools"])
		}
		if _, ok := tools[0]["strict"]; ok {
			t.Fatalf("strict must be omitted: %s", payload["tools"])
		}
	})
}

func TestAzureUpstreamUserAgent(t *testing.T) {
	for _, tc := range []struct{ name, custom string }{
		// .upstream/v0.87.1/packages/ai/test/azure-openai-base-url.test.ts:222
		// D65 intentionally changes only the product identity, not header precedence.
		{"uses pi's User-Agent by default", ""},
		// .upstream/v0.87.1/packages/ai/test/azure-openai-base-url.test.ts:226
		{"lets explicit headers override the default User-Agent", "custom-agent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{Model: "gpt-4o-mini", APIKey: "test-api-key", BaseURL: "https://my-resource.openai.azure.com"}).(*openAIResponsesProvider)
			want := PiUserAgent()
			opts := StreamOptions{}
			if tc.custom != "" {
				want = tc.custom
				opts.Headers = ProviderHeadersFromStrings(map[string]string{"User-Agent": tc.custom})
			}
			called := false
			p.client = &http.Client{Transport: responsesTestRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
				called = true
				if got := req.Header.Get("User-Agent"); got != want {
					t.Errorf("User-Agent = %q, want %q", got, want)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))}, nil
			})}
			stream, err := p.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), opts)
			if err != nil {
				t.Fatal(err)
			}
			if result := stream.Result(); result.StopReason != StopReasonStop {
				t.Fatalf("result = %+v", result)
			}
			if !called {
				t.Fatal("no request")
			}
		})
	}
}
