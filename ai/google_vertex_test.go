//go:build !pig_strip_google_vertex

package ai

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestResolveVertexBaseURL(t *testing.T) {
	cases := []struct {
		name     string
		explicit string
		project  string
		location string
		want     string
	}{
		{
			name:     "explicit override",
			explicit: "https://custom.endpoint/v1/",
			want:     "https://custom.endpoint/v1/publishers/google",
		},
		{
			name:     "project+location",
			project:  "my-project",
			location: "us-east4",
			want:     "https://us-east4-aiplatform.googleapis.com/v1/projects/my-project/locations/us-east4/publishers/google",
		},
		{
			name:     "no project fallback",
			location: "europe-west1",
			want:     "https://aiplatform.googleapis.com/v1/publishers/google",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveVertexBaseURL(tc.explicit, tc.project, tc.location)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveVertexLocation(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_LOCATION", "")
	if got := resolveVertexLocation("", nil); got != "" {
		t.Fatalf("missing location = %q, want empty so the request rejects missing ADC configuration", got)
	}
	t.Setenv("GOOGLE_CLOUD_LOCATION", "asia-east1")
	if got := resolveVertexLocation("", nil); got != "asia-east1" {
		t.Fatalf("env location = %q, want asia-east1", got)
	}
	if got := resolveVertexLocation("explicit", nil); got != "explicit" {
		t.Fatalf("explicit location = %q, want explicit", got)
	}
}

func TestNewGoogleVertexProvider(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	p := NewGoogleVertexProvider(GoogleVertexConfig{
		APIKey: "test-key",
		Model:  "gemini-2.5-flash",
	})
	gp, ok := p.(*googleVertexProvider)
	if !ok {
		t.Fatalf("type = %T, want *googleVertexProvider", p)
	}
	if gp.cfg.ProviderID != string(APIGoogleVertex) {
		t.Fatalf("ProviderID = %q", gp.cfg.ProviderID)
	}
	if gp.cfg.Model != "gemini-2.5-flash" {
		t.Fatalf("Model = %q", gp.cfg.Model)
	}
	var requestURL string
	gp.client = &http.Client{Transport: openAITestRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		requestURL = request.URL.String()
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n"))}, nil
	})}
	stream, err := p.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result.API != APIGoogleVertex {
		t.Fatalf("API=%q", result.API)
	}
	wantURL := "https://aiplatform.googleapis.com/v1/publishers/google/models/gemini-2.5-flash:streamGenerateContent?alt=sse"
	if requestURL != wantURL {
		t.Fatalf("URL=%q, want %q", requestURL, wantURL)
	}
}
