package ai

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// fetchURLs collects the request URLs a custom fetch saw.
type fetchURLs struct {
	mu   sync.Mutex
	urls []string
}

func (f *fetchURLs) add(url string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.urls = append(f.urls, url)
}

func (f *fetchURLs) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.urls...)
}

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
		return newMistralTestProvider(MistralConfig{APIKey: "test-key", Model: "test-model", ProviderID: "test-provider", BaseURL: baseURL}, nil)
	case APIPiMessages:
		return NewPiMessagesProvider(PiMessagesConfig{APIKey: "test-key", Model: "test-model", ProviderID: "test-provider", BaseURL: baseURL})
	case APIGoogleGenerativeAI:
		return NewGoogleProvider(GoogleConfig{APIKey: "test-key", Model: "test-model", ProviderID: "test-provider", BaseURL: baseURL})
	case APIGoogleVertex:
		return newGoogleVertexTestProvider(GoogleVertexConfig{APIKey: "test-key", Model: "test-model", ProviderID: "test-provider", BaseURL: baseURL}, nil)
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
		name       string
		apis       []API
		googleSDKs bool
	}{
		// .upstream/v0.87.1/packages/ai/test/fetch-option.test.ts:63
		{"passes fetch through streamSimple to the Anthropic SDK", []API{APIAnthropicMessages}, false},
		// .upstream/v0.87.1/packages/ai/test/fetch-option.test.ts:73
		{"passes fetch through streamSimple to OpenAI SDK adapters", []API{APIOpenAICompletions, APIOpenAIResponses, APIAzureOpenAIResponses}, false},
		// .upstream/v0.87.1/packages/ai/test/fetch-option.test.ts:103
		{"uses fetch for Mistral, Codex SSE, and pi-messages HTTP requests", []API{APIMistralConversations, APIOpenAICodexResponses, APIPiMessages}, false},
		// packages/ai/test/fetch-option.test.ts:124 (Pi f72f799a4: the Google adapters take a custom fetch; maxRetries 0 there, a 401 here is not retried)
		{"passes fetch through streamSimple to the Google GenAI SDK adapters", []API{APIGoogleGenerativeAI, APIGoogleVertex}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var fallbackCalls, customCalls atomic.Int32
			var customURLs fetchURLs
			fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fallbackCalls.Add(1)
				w.WriteHeader(http.StatusUnauthorized)
			}))
			t.Cleanup(fallback.Close)
			ambient := http.DefaultClient
			custom := &http.Client{Transport: fetchOptionTransport(func(request *http.Request) (*http.Response, error) {
				customCalls.Add(1)
				customURLs.add(request.URL.String())
				return &http.Response{StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"upstream rejected request"}}`)), Request: request}, nil
			})}
			// built counts the APIs of the row this build links: all of tc.apis in the default build.
			built := int32(0)
			for _, api := range tc.apis {
				if !BuiltAPI(api) {
					// A build with this API's pig_strip tag has no provider to hand the client to.
					continue
				}
				built++
				message := fetchOptionResult(t, fetchOptionProvider(api, fallback.URL), custom)
				if tc.googleSDKs && !strings.Contains(message, "upstream rejected request") {
					t.Errorf("%s error = %q", api, message)
				}
			}
			if customCalls.Load() != built || fallbackCalls.Load() != 0 {
				t.Fatalf("custom/fallback calls = %d/%d, want %d/0", customCalls.Load(), fallbackCalls.Load(), built)
			}
			if tc.googleSDKs {
				for _, url := range customURLs.snapshot() {
					if !strings.HasPrefix(url, fallback.URL) {
						t.Errorf("custom fetch url = %q, want it under the model's base URL %q", url, fallback.URL)
					}
				}
			}
			if http.DefaultClient != ambient {
				t.Fatal("global HTTP client was replaced")
			}
		})
	}
	// packages/ai/test/fetch-option.test.ts: "allows Google adapters to receive globalThis.fetch explicitly" is gone in Pi 22f456d73 (#12108): it
	// covered the escape hatch of the removed custom-fetch rejection; "keeps the global dispatcher for Google adapters when no fetch is given" is
	// TestGoogleAdaptersWithoutFetchUseTheirOwnClientNotACallersFetch.
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
