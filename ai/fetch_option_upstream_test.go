package ai

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type fetchOptionTransport func(*http.Request) (*http.Response, error)

func (f fetchOptionTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func fetchOptionProvider(api API, baseURL string) Provider {
	switch api {
	case APIAnthropicMessages:
		return NewAnthropicProvider(AnthropicConfig{APIKey: "test-key", Model: "test-model", ProviderID: "test-provider", BaseURL: baseURL})
	case APIOpenAICompletions:
		return NewOpenAIProvider(OpenAIConfig{APIKey: "test-key", Model: "test-model", ProviderID: "test-provider", BaseURL: baseURL})
	case APIOpenAIResponses:
		return NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "test-key", Model: "test-model", ProviderID: "test-provider", BaseURL: baseURL})
	case APIAzureOpenAIResponses:
		return NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{APIKey: "test-key", Model: "test-model", ProviderID: "test-provider", BaseURL: baseURL})
	case APIMistralConversations:
		return NewMistralProvider(MistralConfig{APIKey: "test-key", Model: "test-model", ProviderID: "test-provider", BaseURL: baseURL})
	case APIPiMessages:
		return NewPiMessagesProvider(PiMessagesConfig{APIKey: "test-key", Model: "test-model", ProviderID: "test-provider", BaseURL: baseURL})
	case APIGoogleGenerativeAI:
		return NewGoogleProvider(GoogleConfig{APIKey: "test-key", Model: "test-model", ProviderID: "test-provider", BaseURL: baseURL})
	case APIGoogleVertex:
		return NewGoogleVertexProvider(GoogleVertexConfig{APIKey: "test-key", Model: "test-model", ProviderID: "test-provider", BaseURL: baseURL})
	case APIOpenAICodexResponses:
		token := "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"account"}}`)) + ".signature"
		return NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: token, Model: "test-model", BaseURL: baseURL})
	default:
		panic("unexpected test API")
	}
}

func fetchOptionResult(t *testing.T, provider Provider, fetch *http.Client) string {
	t.Helper()
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello"), Timestamp: 1}}}), StreamOptions{Fetch: fetch, Transport: TransportSSE})
	if err != nil {
		return err.Error()
	}
	return stream.Result().ErrorMessage
}

// A caller-supplied HTTP implementation owns redirects just as a custom fetch returning a manual Response does.
func TestFetchOptionOwnsRedirectPolicy(t *testing.T) {
	var calls atomic.Int32
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: fetchOptionTransport(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://redirect.test/landing"}}, Body: io.NopCloser(strings.NewReader("manual redirect")), Request: request}, nil
		}),
	}
	message := fetchOptionResult(t, fetchOptionProvider(APIOpenAICompletions, "https://request.test/v1"), client)
	if calls.Load() != 1 || !strings.Contains(message, "302") {
		t.Fatalf("custom calls=%d message=%q", calls.Load(), message)
	}
}

func TestFetchOptionUpstream(t *testing.T) {
	for _, tc := range []struct {
		name    string
		apis    []API
		rejects bool
	}{
		// .upstream/v0.87.1/packages/ai/test/fetch-option.test.ts:63
		{"passes fetch through streamSimple to the Anthropic SDK", []API{APIAnthropicMessages}, false},
		// .upstream/v0.87.1/packages/ai/test/fetch-option.test.ts:73
		{"passes fetch through streamSimple to OpenAI SDK adapters", []API{APIOpenAICompletions, APIOpenAIResponses, APIAzureOpenAIResponses}, false},
		// .upstream/v0.87.1/packages/ai/test/fetch-option.test.ts:103
		{"uses fetch for Mistral, Codex SSE, and pi-messages HTTP requests", []API{APIMistralConversations, APIOpenAICodexResponses, APIPiMessages}, false},
		// .upstream/v0.87.1/packages/ai/test/fetch-option.test.ts:124
		{"rejects custom fetch for Google adapters instead of silently bypassing it", []API{APIGoogleGenerativeAI, APIGoogleVertex}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var fallbackCalls, customCalls atomic.Int32
			fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fallbackCalls.Add(1)
				w.WriteHeader(http.StatusUnauthorized)
			}))
			t.Cleanup(fallback.Close)
			ambient := http.DefaultClient
			custom := &http.Client{Transport: fetchOptionTransport(func(request *http.Request) (*http.Response, error) {
				customCalls.Add(1)
				return &http.Response{StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"upstream rejected request"}}`)), Request: request}, nil
			})}
			for _, api := range tc.apis {
				message := fetchOptionResult(t, fetchOptionProvider(api, fallback.URL), custom)
				if tc.rejects {
					name := "Google Generative AI"
					if api == APIGoogleVertex {
						name = "Google Vertex"
					}
					if !strings.Contains(message, "Custom fetch is not supported by the "+name+" adapter") {
						t.Errorf("%s error = %q", api, message)
					}
				}
			}
			want := int32(len(tc.apis))
			if tc.rejects {
				want = 0
			}
			if customCalls.Load() != want || fallbackCalls.Load() != 0 {
				t.Fatalf("custom/fallback calls = %d/%d, want %d/0", customCalls.Load(), fallbackCalls.Load(), want)
			}
			if http.DefaultClient != ambient {
				t.Fatal("global HTTP client was replaced")
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/fetch-option.test.ts:142
	t.Run("allows Google adapters to receive globalThis.fetch explicitly", func(t *testing.T) {
		var calls atomic.Int32
		original := http.DefaultClient
		t.Cleanup(func() { http.DefaultClient = original })
		http.DefaultClient = &http.Client{Transport: fetchOptionTransport(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return &http.Response{StatusCode: http.StatusUnauthorized, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"upstream rejected request"}}`)), Request: request}, nil
		})}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
		t.Cleanup(server.Close)
		ambient := http.DefaultClient
		result := fetchOptionResult(t, fetchOptionProvider(APIGoogleGenerativeAI, server.URL), ambient)
		if calls.Load() != 1 || strings.Contains(result, "Custom fetch is not supported") || http.DefaultClient != ambient {
			t.Fatalf("calls=%d result=%q globalPreserved=%v", calls.Load(), result, http.DefaultClient == ambient)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/fetch-option.test.ts:161
	t.Run("uses fetch for image generation", func(t *testing.T) {
		var customCalls, fallbackCalls atomic.Int32
		fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fallbackCalls.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
		}))
		t.Cleanup(fallback.Close)
		ambient := http.DefaultClient
		custom := &http.Client{Transport: fetchOptionTransport(func(request *http.Request) (*http.Response, error) {
			customCalls.Add(1)
			return &http.Response{StatusCode: http.StatusUnauthorized, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"upstream rejected request"}}`)), Request: request}, nil
		})}
		result := GenerateImagesOpenRouter(t.Context(), ImageModel{ID: "test-model", API: APIImagesOpenRouter, Provider: ProviderImagesOpenRouter, BaseURL: fallback.URL, Output: []string{"image"}}, ImagesContext{Input: []ContentBlock{TextContent{Text: "draw"}}}, ProviderImagesOptions{APIKey: "test-key", Fetch: custom})
		if result.StopReason != ImagesStopReasonError || customCalls.Load() != 1 || fallbackCalls.Load() != 0 || http.DefaultClient != ambient {
			t.Fatalf("result=%+v custom=%d fallback=%d", result, customCalls.Load(), fallbackCalls.Load())
		}
	})
}
