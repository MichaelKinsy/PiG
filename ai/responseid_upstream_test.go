package ai

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The live upstream matrix asserts response metadata, not model wording. Hermetic responses exercise each real API decoder without credential or generation variability.
func TestResponseIDUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, provider, model string
		api                   API
		vertexADC             bool
	}{
		// .upstream/v0.87.1/packages/ai/test/responseid.test.ts:29
		{"Google Provider/should expose responseId", "google", "gemini-2.5-flash", APIGoogleGenerativeAI, false},
		// .upstream/v0.87.1/packages/ai/test/responseid.test.ts:42
		{"Google Vertex Provider/should expose responseId with ADC", "google-vertex", "gemini-3-flash-preview", APIGoogleVertex, true},
		// .upstream/v0.87.1/packages/ai/test/responseid.test.ts:46
		{"Google Vertex Provider/should expose responseId with API key", "google-vertex", "gemini-3-flash-preview", APIGoogleVertex, false},
		// .upstream/v0.87.1/packages/ai/test/responseid.test.ts:59
		{"OpenAI Completions Provider/should expose responseId", "openai", "gpt-4o-mini", APIOpenAICompletions, false},
		// .upstream/v0.87.1/packages/ai/test/responseid.test.ts:67
		{"OpenAI Responses Provider/should expose responseId", "openai", "gpt-5-mini", APIOpenAIResponses, false},
		// .upstream/v0.87.1/packages/ai/test/responseid.test.ts:75
		{"Anthropic Provider/should expose responseId", "anthropic", "claude-sonnet-4-5", APIAnthropicMessages, false},
		// .upstream/v0.87.1/packages/ai/test/responseid.test.ts:85
		{"Azure OpenAI Responses Provider/should expose responseId", "azure-openai-responses", "gpt-4o-mini", APIAzureOpenAIResponses, false},
		// .upstream/v0.87.1/packages/ai/test/responseid.test.ts:93
		{"Mistral Provider/should expose responseId", "mistral", "devstral-medium-latest", APIMistralConversations, false},
		// .upstream/v0.87.1/packages/ai/test/responseid.test.ts:99
		{"GitHub Copilot Provider/OpenAI path should expose responseId", "github-copilot", "gpt-5.3-codex", APIOpenAIResponses, false},
		// .upstream/v0.87.1/packages/ai/test/responseid.test.ts:104
		{"GitHub Copilot Provider/Anthropic path should expose responseId", "github-copilot", "claude-sonnet-4.6", APIAnthropicMessages, false},
		// .upstream/v0.87.1/packages/ai/test/responseid.test.ts:115
		{"OpenAI Codex Provider/should expose responseId", "openai-codex", "gpt-5.5", APIOpenAICodexResponses, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			options := StreamOptions{Transport: TransportSSE}
			if tc.vertexADC {
				var adcOptions StreamOptions
				ctx, adcOptions = vertexADCTestContext(t, &http.Client{Transport: openAITestRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
					if request.URL.Host != "oauth2.googleapis.com" {
						t.Errorf("unexpected ADC endpoint: %s", request.URL)
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"access_token":"response-id-adc","token_type":"Bearer","expires_in":3600}`))}, nil
				})})
				options.Env = adcOptions.Env
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if tc.vertexADC && request.Header.Get("Authorization") != "Bearer response-id-adc" {
					t.Errorf("ADC credential did not reach response-ID request: %v", request.Header)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, responseIDWireFixture(tc.api))
			}))
			t.Cleanup(server.Close)
			var provider Provider
			switch tc.api {
			case APIAnthropicMessages:
				provider = NewAnthropicProvider(AnthropicConfig{APIKey: "test-key", ProviderID: tc.provider, Model: tc.model, BaseURL: server.URL})
			case APIOpenAICompletions:
				provider = NewOpenAIProvider(OpenAIConfig{APIKey: "test-key", ProviderID: tc.provider, Model: tc.model, BaseURL: server.URL})
			case APIOpenAIResponses:
				provider = NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "test-key", ProviderID: tc.provider, Model: tc.model, BaseURL: server.URL})
			case APIAzureOpenAIResponses:
				provider = NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{APIKey: "test-key", ProviderID: tc.provider, Model: tc.model, BaseURL: server.URL})
			case APIMistralConversations:
				provider = NewMistralProvider(MistralConfig{APIKey: "test-key", ProviderID: tc.provider, Model: tc.model, BaseURL: server.URL})
			case APIGoogleVertex:
				key := "test-key"
				if tc.vertexADC {
					key = ""
				}
				provider = NewGoogleVertexProvider(GoogleVertexConfig{APIKey: key, ProviderID: tc.provider, Model: tc.model, BaseURL: server.URL, Project: "test-project", Location: "us-central1"})
			case APIGoogleGenerativeAI:
				provider = NewGoogleProvider(GoogleConfig{APIKey: "test-key", ProviderID: tc.provider, Model: tc.model, BaseURL: server.URL})
			case APIOpenAICodexResponses:
				token := "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"account"}}`)) + ".signature"
				provider = NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: token, Model: tc.model, BaseURL: server.URL})
			default:
				t.Fatal("unexpected API", tc.api)
			}
			stream, err := provider.Stream(ctx, NormalizeContext(Context{SystemPrompt: "You are a helpful assistant. Be concise.", Messages: []Message{UserMessage{Content: UserText("Reply with exactly: response id test")}}}), options)
			if err != nil {
				t.Fatal(err)
			}
			response := stream.Result()
			if response.StopReason == StopReasonError {
				t.Fatal(response.ErrorMessage)
			}
			// Stronger than upstream truthy/string: bind the response to the distinct fixture ID.
			if response.ResponseID != "response-id-fixture" {
				t.Fatalf("responseId = %q", response.ResponseID)
			}
		})
	}
}

// Pi api/openai-completions.ts:558-560 keeps the first nonempty ID and first model differing from the request.
func TestCompletionsResponseMetadataKeepsFirstValue(t *testing.T) {
	provider := &openAIProvider{cfg: OpenAIConfig{Model: "requested"}}
	builder := newAssistantStreamBuilder(t.Context(), APIOpenAICompletions, "openai", "requested")
	provider.parseSSE(t.Context(), strings.NewReader("data: {\"id\":\"first\",\"model\":\"requested\",\"choices\":[]}\n\ndata: {\"id\":\"second\",\"model\":\"resolved\",\"choices\":[]}\n\ndata: {\"id\":\"third\",\"model\":\"ignored\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"), builder, nil)
	message := builder.stream.Result()
	if message.ResponseID != "first" || message.ResponseModel != "resolved" {
		t.Fatalf("response metadata = %q/%q, want first/resolved", message.ResponseID, message.ResponseModel)
	}
}

// Pi openai-responses-shared.ts:551-558 uses the authoritative completed ID and does not publish response.model.
func TestResponsesResponseMetadataUsesFinalIDWithoutModel(t *testing.T) {
	provider := &openAIResponsesProvider{}
	builder := newAssistantStreamBuilder(t.Context(), APIOpenAIResponses, "openai", "requested")
	provider.parseResponsesSSE(t.Context(), strings.NewReader("data: {\"type\":\"response.created\",\"response\":{\"id\":\"first\",\"model\":\"resolved\"}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"final\",\"model\":\"resolved\",\"status\":\"completed\"}}\n\n"), builder, nil)
	message := builder.stream.Result()
	if message.ResponseID != "final" || message.ResponseModel != "" {
		t.Fatalf("response metadata = %q/%q, want final/empty", message.ResponseID, message.ResponseModel)
	}
}

func responseIDWireFixture(api API) string {
	switch api {
	case APIGoogleGenerativeAI, APIGoogleVertex:
		return "data: {\"responseId\":\"response-id-fixture\",\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"response id test\"}]},\"finishReason\":\"STOP\"}]}\n\n"
	case APIAnthropicMessages:
		return "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"response-id-fixture\",\"usage\":{}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	case APIOpenAIResponses, APIAzureOpenAIResponses, APIOpenAICodexResponses:
		return "data: {\"type\":\"response.created\",\"response\":{\"id\":\"response-id-fixture\"}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-id-fixture\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
	default:
		return "data: {\"id\":\"response-id-fixture\",\"choices\":[{\"delta\":{\"content\":\"response id test\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	}
}
