//go:build !pig_strip_google_vertex

package ai

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestGoogleVertexAPIKeyResolutionUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, key, envKey, baseURL, wantURL, userAgent string
		keyMode                                        bool
	}{
		// .upstream/v0.87.1/packages/ai/test/google-vertex-api-key-resolution.test.ts:75
		{name: "falls back to ADC when options.apiKey is a placeholder marker", key: "<authenticated>"},
		// .upstream/v0.87.1/packages/ai/test/google-vertex-api-key-resolution.test.ts:94
		{name: "falls back to ADC when options.apiKey is the gcp-vertex-credentials marker", key: "gcp-vertex-credentials"},
		// .upstream/v0.87.1/packages/ai/test/google-vertex-api-key-resolution.test.ts:113
		{name: "falls back to ADC when GOOGLE_CLOUD_API_KEY is a placeholder marker", envKey: "<authenticated>"},
		// .upstream/v0.87.1/packages/ai/test/google-vertex-api-key-resolution.test.ts:133
		{name: "still uses the API key client for real API keys", key: "AIzaSyExampleRealisticLookingApiKey123456", keyMode: true, wantURL: "https://aiplatform.googleapis.com/v1/publishers/google"},
		// .upstream/v0.87.1/packages/ai/test/google-vertex-api-key-resolution.test.ts:150
		{name: "does not forward generated Vertex base URL placeholders", baseURL: "https://{location}-aiplatform.googleapis.com"},
		// .upstream/v0.87.1/packages/ai/test/google-vertex-api-key-resolution.test.ts:164
		{name: "lets explicit headers override the default User-Agent", userAgent: "custom-agent"},
		// .upstream/v0.87.1/packages/ai/test/google-vertex-api-key-resolution.test.ts:179
		{name: "forwards custom baseUrl to the ADC client", baseURL: "https://proxy.example.com", wantURL: "https://proxy.example.com/v1/publishers/google"},
		// .upstream/v0.87.1/packages/ai/test/google-vertex-api-key-resolution.test.ts:201
		{name: "forwards custom baseUrl to the API key client", key: "AIzaSyExampleRealisticLookingApiKey123456", keyMode: true, baseURL: "https://proxy.example.com", wantURL: "https://proxy.example.com/v1/publishers/google"},
		// .upstream/v0.87.1/packages/ai/test/google-vertex-api-key-resolution.test.ts:221
		{name: "does not append apiVersion when custom baseUrl already includes one", baseURL: "https://proxy.example.com/v1/projects/test-project/locations/global", wantURL: "https://proxy.example.com/v1/projects/test-project/locations/global/publishers/google"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GOOGLE_CLOUD_API_KEY", tc.envKey)
			config := GoogleVertexConfig{APIKey: tc.key, Model: "gemini-3-flash-preview", BaseURL: tc.baseURL}
			if !tc.keyMode {
				config.Project = "test-project"
				config.Location = "us-central1"
			}
			provider := NewGoogleVertexProvider(config).(*googleVertexProvider)
			authCalls := 0
			provider.accessToken = func(context.Context, ProviderEnv) (string, error) { authCalls++; return "adc-fixture", nil }
			var captured *http.Request
			provider.client = &http.Client{Transport: openAITestRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				captured = r
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"responseId\":\"vertex-response-id\",\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":1,\"totalTokenCount\":2}}\n\n"))}, nil
			})}
			options := StreamOptions{}
			if tc.userAgent != "" {
				options.Headers = ProviderHeadersFromStrings(map[string]string{"User-Agent": tc.userAgent})
			}
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), options)
			if err != nil {
				t.Fatal(err)
			}
			if result := stream.Result(); result.StopReason != StopReasonStop {
				t.Fatal(result)
			}
			if captured == nil {
				t.Fatal("no request")
			}
			key := ""
			if tc.keyMode {
				key = tc.key
				if authCalls != 0 {
					t.Fatal("API-key client acquired ADC")
				}
			} else if authCalls != 1 || captured.Header.Get("Authorization") != "Bearer adc-fixture" {
				t.Fatalf("ADC calls=%d Authorization=%q", authCalls, captured.Header.Get("Authorization"))
			}
			if got := captured.Header.Get("x-goog-api-key"); got != key {
				t.Errorf("api key=%q want=%q", got, key)
			}
			userAgent := tc.userAgent
			if userAgent == "" {
				userAgent = PiUserAgent()
			}
			if captured.Header.Get("User-Agent") != userAgent {
				t.Errorf("User-Agent=%q want=%q", captured.Header.Get("User-Agent"), userAgent)
			}
			base := tc.wantURL
			if base == "" {
				base = "https://us-central1-aiplatform.googleapis.com/v1/projects/test-project/locations/us-central1/publishers/google"
			}
			if got, want := captured.URL.String(), base+"/models/gemini-3-flash-preview:streamGenerateContent?alt=sse"; got != want {
				t.Errorf("URL=%q want=%q", got, want)
			}
		})
	}
}
