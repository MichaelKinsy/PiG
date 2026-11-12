//go:build !pig_strip_google_vertex

package ai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Ports packages/ai/test/fetch-option.test.ts "passes fetch through streamSimple to the Google GenAI SDK adapters" and "keeps the global dispatcher for
// Google adapters when no fetch is given" (Pi f72f799a4, #12103; the final file is Pi 22f456d73) over every request shape of the two adapters:
// generative-ai with an API key, Vertex with an API key and Vertex with Application Default Credentials, each against the default host and against a
// custom base URL. The caller's fetch carries every request; Vertex's ADC token request is not one of them (packages/ai/CHANGELOG.md: "Vertex ADC token
// requests still use google-auth-library's own HTTP client").
func TestGoogleAdaptersRouteEveryRequestShapeThroughTheCallersFetch(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	t.Cleanup(local.Close)
	cases := []struct {
		name      string
		provider  func(baseURL string) Provider
		options   StreamOptions
		baseURL   string
		wantHost  string
		wantToken string
	}{
		{"generative-ai with an API key", func(base string) Provider {
			return NewGoogleProvider(GoogleConfig{APIKey: "test-key", Model: "test-model", ProviderID: "test-provider", BaseURL: base})
		}, StreamOptions{}, "", "generativelanguage.googleapis.com", ""},
		{"generative-ai with a custom base URL", func(base string) Provider {
			return NewGoogleProvider(GoogleConfig{APIKey: "test-key", Model: "test-model", ProviderID: "test-provider", BaseURL: base})
		}, StreamOptions{}, local.URL, strings.TrimPrefix(local.URL, "http://"), ""},
		{"vertex with an API key", func(base string) Provider {
			return newGoogleVertexTestProvider(GoogleVertexConfig{APIKey: "test-key", Model: "test-model", ProviderID: "test-provider", BaseURL: base}, nil)
		}, StreamOptions{}, "", "aiplatform.googleapis.com", ""},
		{"vertex with an API key and a custom base URL", func(base string) Provider {
			return newGoogleVertexTestProvider(GoogleVertexConfig{APIKey: "test-key", Model: "test-model", ProviderID: "test-provider", BaseURL: base}, nil)
		}, StreamOptions{}, local.URL, strings.TrimPrefix(local.URL, "http://"), ""},
		{"vertex with ADC", func(base string) Provider {
			return newGoogleVertexADCTestProvider(GoogleVertexConfig{Model: "test-model", ProviderID: "test-provider", BaseURL: base}, "adc-token")
		}, StreamOptions{Project: "p1", Location: "us-central1"}, "", "us-central1-aiplatform.googleapis.com", "adc-token"},
		{"vertex with ADC and a custom base URL", func(base string) Provider {
			return newGoogleVertexADCTestProvider(GoogleVertexConfig{Model: "test-model", ProviderID: "test-provider", BaseURL: base}, "adc-token")
		}, StreamOptions{Project: "p1", Location: "us-central1"}, local.URL, strings.TrimPrefix(local.URL, "http://"), "adc-token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var urls fetchURLs
			var authorization atomic.Value
			custom := &http.Client{Transport: fetchOptionTransport(func(request *http.Request) (*http.Response, error) {
				urls.add(request.URL.String())
				authorization.Store(request.Header.Get("Authorization"))
				return &http.Response{StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"upstream rejected request"}}`)), Request: request}, nil
			})}
			options := tc.options
			options.Fetch, options.Transport = custom, TransportSSE
			stream, err := tc.provider(tc.baseURL).Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello"), Timestamp: 1}}}), options)
			message := ""
			if err != nil {
				// An ADC request fails Stream itself where an API-key request ends the stream with the error.
				message = err.Error()
			} else {
				message = stream.Result().ErrorMessage
			}
			if !strings.Contains(message, "upstream rejected request") {
				t.Fatalf("error = %q", message)
			}
			seen := urls.snapshot()
			if len(seen) != 1 || !strings.Contains(seen[0], tc.wantHost) {
				t.Fatalf("custom fetch saw %v, want one request to %s", seen, tc.wantHost)
			}
			if got, _ := authorization.Load().(string); tc.wantToken != "" && got != "Bearer "+tc.wantToken {
				t.Fatalf("Authorization = %q, want the ADC token", got)
			}
		})
	}
}

// fetch-option.test.ts "keeps the global dispatcher for Google adapters when no fetch is given" (#12097): without a fetch the adapters use their own
// client, never a client a caller installed as the process default. (Pi's `dispatcher` property of the request init has no Go counterpart: Go has no
// per-request dispatcher; the idle timeouts live on the provider's client.)
func TestGoogleAdaptersWithoutFetchUseTheirOwnClientNotACallersFetch(t *testing.T) {
	var local atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		local.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	var ambientCalls atomic.Int32
	original := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = original })
	http.DefaultClient = &http.Client{Transport: fetchOptionTransport(func(request *http.Request) (*http.Response, error) {
		ambientCalls.Add(1)
		return nil, io.ErrUnexpectedEOF
	})}
	for _, api := range []API{APIGoogleGenerativeAI, APIGoogleVertex} {
		fetchOptionResult(t, fetchOptionProvider(api, server.URL), nil)
	}
	if local.Load() != 2 || ambientCalls.Load() != 0 {
		t.Fatalf("own client/process default calls = %d/%d, want 2/0", local.Load(), ambientCalls.Load())
	}
}
