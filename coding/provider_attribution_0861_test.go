package coding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestMergeProviderAttributionHeadersMatchesPinnedProvidersAndHosts(t *testing.T) {
	tests := []struct {
		name      string
		provider  string
		baseURL   string
		telemetry bool
		sessionID string
		want      map[string]string
	}{
		{
			name:     "openrouter provider",
			provider: "openrouter", baseURL: "https://example.test/v1", telemetry: true,
			want: map[string]string{
				"HTTP-Referer":            "https://github.com/MichaelKinsy/PiG",
				"X-OpenRouter-Title":      "PiG",
				"X-OpenRouter-Categories": "cli-agent",
			},
		},
		{
			// Upstream sdk-openrouter-attribution.test.ts "preserves legacy
			// OpenRouter base URL substring attribution matching".
			name:     "openrouter base URL substring matches an invalid URL",
			provider: "custom-openrouter", baseURL: "not-a-url-openrouter.ai", telemetry: true,
			want: map[string]string{
				"HTTP-Referer":            "https://github.com/MichaelKinsy/PiG",
				"X-OpenRouter-Title":      "PiG",
				"X-OpenRouter-Categories": "cli-agent",
			},
		},
		{
			name:     "openrouter base URL substring matches a proxy path",
			provider: "custom", baseURL: "https://proxy.example.test/openrouter.ai/v1", telemetry: true,
			want: map[string]string{
				"HTTP-Referer":            "https://github.com/MichaelKinsy/PiG",
				"X-OpenRouter-Title":      "PiG",
				"X-OpenRouter-Categories": "cli-agent",
			},
		},
		{
			name:     "custom provider routed through the OpenRouter host",
			provider: "custom-openrouter", baseURL: "https://openrouter.ai/api/v1", telemetry: true,
			want: map[string]string{
				"HTTP-Referer":            "https://github.com/MichaelKinsy/PiG",
				"X-OpenRouter-Title":      "PiG",
				"X-OpenRouter-Categories": "cli-agent",
			},
		},
		{
			name:     "nvidia provider",
			provider: "nvidia", baseURL: "https://example.test/v1", telemetry: true,
			want: map[string]string{"X-BILLING-INVOKE-ORIGIN": "PiG"},
		},
		{
			name:     "nvidia host",
			provider: "custom", baseURL: "https://integrate.api.nvidia.com/v1", telemetry: true,
			want: map[string]string{"X-BILLING-INVOKE-ORIGIN": "PiG"},
		},
		{
			name:     "nvidia hostname suffix is not the host",
			provider: "custom", baseURL: "https://integrate.api.nvidia.com.example.test/v1", telemetry: true,
			want: nil,
		},
		{
			name:     "cloudflare provider",
			provider: "cloudflare-workers-ai", baseURL: "https://example.test/v1", telemetry: true,
			want: map[string]string{"User-Agent": "pig-coding-agent"},
		},
		{
			name:     "cloudflare gateway host",
			provider: "custom", baseURL: "https://gateway.ai.cloudflare.com/v1/acct/gateway", telemetry: true,
			want: map[string]string{"User-Agent": "pig-coding-agent"},
		},
		{
			name:     "telemetry disables defaults",
			provider: "openrouter", baseURL: "https://openrouter.ai/api/v1", telemetry: false,
			want: nil,
		},
		{
			name:     "opencode session is outside telemetry gate",
			provider: "opencode", baseURL: "https://example.test/v1", telemetry: false, sessionID: "session-1",
			want: map[string]string{"x-opencode-session": "session-1", "x-opencode-client": "pig"},
		},
		{
			name:     "opencode host",
			provider: "custom", baseURL: "https://opencode.ai/zen/v1", telemetry: false, sessionID: "session-2",
			want: map[string]string{"x-opencode-session": "session-2", "x-opencode-client": "pig"},
		},
		{
			name:     "opencode without session",
			provider: "opencode-go", baseURL: "https://opencode.ai/zen/v1", telemetry: true,
			want: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := mergeProviderAttributionHeaders(test.provider, test.baseURL, test.telemetry, test.sessionID)
			if !reflect.DeepEqual(got, ai.ProviderHeadersFromStrings(test.want)) {
				t.Fatalf("headers = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestMergeProviderAttributionHeadersUsesSourceOrder(t *testing.T) {
	got := mergeProviderAttributionHeaders(
		"openrouter",
		"https://openrouter.ai/api/v1",
		true,
		"",
		ai.ProviderHeadersFromStrings(map[string]string{
			"HTTP-Referer":            "https://configured.example",
			"X-OpenRouter-Categories": "configured-category",
		}),
		ai.ProviderHeadersFromStrings(map[string]string{
			"X-OpenRouter-Title":      "request-title",
			"X-OpenRouter-Categories": "request-category",
		}),
	)
	want := ai.ProviderHeadersFromStrings(map[string]string{
		"HTTP-Referer":            "https://configured.example",
		"X-OpenRouter-Title":      "request-title",
		"X-OpenRouter-Categories": "request-category",
	})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("headers = %#v, want %#v", got, want)
	}
}

func TestProviderAttributionWrapperMergesAtRequestTime(t *testing.T) {
	capture := &attributionCaptureProvider{}
	provider := newProviderAttributionProvider(
		capture,
		"opencode",
		"https://opencode.ai/zen/v1",
		func() bool { return false },
		map[string]string{"x-opencode-client": "configured-client"},
	)
	requestHeaders := ai.ProviderHeadersFromStrings(map[string]string{"x-opencode-session": "request-session"})
	if _, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{}), ai.StreamOptions{
		SessionID: "default-session",
		Headers:   requestHeaders,
	}); err != nil {
		t.Fatal(err)
	}
	want := ai.ProviderHeadersFromStrings(map[string]string{
		"x-opencode-client":  "configured-client",
		"x-opencode-session": "request-session",
	})
	if !reflect.DeepEqual(capture.options.Headers, want) {
		t.Fatalf("request headers = %#v, want %#v", capture.options.Headers, want)
	}
	requestHeaders["x-opencode-client"] = new("mutated-after-stream")
	if capture.options.Headers["x-opencode-client"] == nil || *capture.options.Headers["x-opencode-client"] != "configured-client" {
		t.Fatal("Provider Stream retained the request header map")
	}
}

func TestProviderAttributionWrapperReachesHTTPRequest(t *testing.T) {
	requestHeaders := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestHeaders <- request.Header.Clone()
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()

	configured := map[string]string{"HTTP-Referer": "https://configured.example"}
	inner := ai.NewOpenAIProvider(ai.OpenAIConfig{
		BaseURL:      server.URL,
		Model:        "test",
		ProviderID:   "openrouter",
		ExtraHeaders: configured,
	})
	provider := newProviderAttributionProvider(inner, "openrouter", server.URL, func() bool { return true }, configured)
	stream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{
		ai.UserMessage{Content: ai.UserText("hello")},
	}}), ai.StreamOptions{Headers: ai.ProviderHeadersFromStrings(map[string]string{"X-OpenRouter-Title": "request-title"})})
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result == nil || result.StopReason != ai.StopReasonStop {
		t.Fatalf("result = %#v", result)
	}

	headers := <-requestHeaders
	if got := headers.Get("HTTP-Referer"); got != "https://configured.example" {
		t.Fatalf("HTTP-Referer = %q", got)
	}
	if got := headers.Get("X-OpenRouter-Title"); got != "request-title" {
		t.Fatalf("X-OpenRouter-Title = %q", got)
	}
	if got := headers.Get("X-OpenRouter-Categories"); got != "cli-agent" {
		t.Fatalf("X-OpenRouter-Categories = %q", got)
	}
}

type attributionCaptureProvider struct {
	options ai.StreamOptions
}

func (provider *attributionCaptureProvider) ID() string   { return "capture" }
func (provider *attributionCaptureProvider) Close() error { return nil }
func (provider *attributionCaptureProvider) Stream(_ context.Context, _ ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	provider.options = options
	stream := ai.NewAssistantMessageEventStream()
	message := &ai.AssistantMessage{StopReason: ai.StopReasonStop}
	if err := stream.Push(ai.StartEvent{Partial: message}); err != nil {
		return nil, err
	}
	if err := stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}); err != nil {
		return nil, err
	}
	return stream, nil
}
