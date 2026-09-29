package ai

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// Go's Provider.Stream is the shared implementation of native stream and streamSimple; coding.TestCloudflareModelRuntimeRequestEnvironment exercises both runtime entrypoints.
func TestCloudflareProviderStreamsUpstream(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  ProviderEnv
		want string
	}{
		// Ports packages/ai/test/cloudflare-stream.test.ts:23.
		{"materializes the model endpoint before dispatch", ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "account", "CLOUDFLARE_GATEWAY_ID": "gateway"}, "https://gateway.ai.cloudflare.com/v1/account/gateway/openai"},
		// Ports packages/ai/test/cloudflare-stream.test.ts:49.
		{"keeps placeholders when the provider env does not resolve them", nil, CloudflareAIGatewayOpenAIBaseURL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewOpenAIProvider(OpenAIConfig{APIKey: "test", Model: "model", ProviderID: "cloudflare-ai-gateway", BaseURL: CloudflareAIGatewayOpenAIBaseURL}).(*openAIProvider)
			captured := ""
			p.client = &http.Client{Transport: openAITestRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				captured = r.URL.Scheme + "://" + r.URL.Host + r.URL.Path
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))}, nil
			})}
			stream, err := p.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{}}), StreamOptions{Env: tc.env})
			if err != nil {
				t.Fatal(err)
			}
			if got := stream.Result(); got.StopReason != StopReasonStop {
				t.Fatalf("result=%#v", got)
			}
			if captured != tc.want+"/chat/completions" {
				t.Fatalf("endpoint=%q want %q", captured, tc.want+"/chat/completions")
			}
			if p.cfg.BaseURL != CloudflareAIGatewayOpenAIBaseURL {
				t.Fatal("request mutated model endpoint")
			}
		})
	}
}
