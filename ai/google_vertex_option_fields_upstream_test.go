//go:build !pig_strip_google_vertex

package ai

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// upstream: google-vertex.ts:439-460 project and location come from the options, then the environment.
func TestGoogleVertexProjectAndLocationOptions(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_API_KEY", "")
	for _, tc := range []struct {
		name    string
		options StreamOptions
		wantURL string
	}{
		{"options win over the config", StreamOptions{Project: "option-project", Location: "europe-west4"}, "https://europe-west4-aiplatform.googleapis.com/v1/projects/option-project/locations/europe-west4/publishers/google"},
		{"config is the fallback", StreamOptions{}, "https://us-central1-aiplatform.googleapis.com/v1/projects/config-project/locations/us-central1/publishers/google"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := NewGoogleVertexProvider(GoogleVertexConfig{Model: "gemini-3-flash-preview", Project: "config-project", Location: "us-central1"}).(*googleVertexProvider)
			provider.accessToken = func(context.Context, ProviderEnv) (string, error) { return "adc", nil }
			var captured *http.Request
			provider.client = &http.Client{Transport: openAITestRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				captured = r
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n"))}, nil
			})}
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), tc.options)
			if err != nil {
				t.Fatal(err)
			}
			if result := stream.Result(); result.StopReason != StopReasonStop {
				t.Fatal(result)
			}
			if captured == nil || !strings.HasPrefix(captured.URL.String(), tc.wantURL) {
				t.Fatalf("request URL = %v, want prefix %s", captured.URL, tc.wantURL)
			}
		})
	}
}
