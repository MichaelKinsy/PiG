package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func captureCompletionsRequestMap(t *testing.T, cfg OpenAIConfig) map[string]any {
	t.Helper()
	var request map[string]any
	provider := NewOpenAIProvider(cfg).(*openAIProvider)
	provider.client = &http.Client{Transport: openAITestRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))}, nil
	})}
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result.StopReason != StopReasonStop {
		t.Fatal(result)
	}
	return request
}

// openai-completions.ts:992-1001: compat.vercelGatewayRouting becomes providerOptions.gateway whenever its only or order list is set, on every base URL, and an empty list is still set.
func TestVercelGatewayRoutingBuildsProviderOptionsForEveryBaseURL(t *testing.T) {
	cases := []struct {
		name    string
		routing *VercelGatewayRouting
		want    string
	}{
		{"only", &VercelGatewayRouting{Only: []string{"bedrock", "anthropic"}}, `{"gateway":{"only":["bedrock","anthropic"]}}`},
		{"order", &VercelGatewayRouting{Order: []string{"anthropic", "openai"}}, `{"gateway":{"order":["anthropic","openai"]}}`},
		{"both", &VercelGatewayRouting{Only: []string{"a"}, Order: []string{"b"}}, `{"gateway":{"only":["a"],"order":["b"]}}`},
		{"empty only", &VercelGatewayRouting{Only: []string{}}, `{"gateway":{"only":[]}}`},
		{"no lists", &VercelGatewayRouting{}, ``},
		{"no routing", nil, ``},
	}
	for _, baseURL := range []string{"https://ai-gateway.vercel.sh/v1", "https://proxy.example.com/v1", "https://api.openai.com/v1"} {
		for _, tc := range cases {
			t.Run(tc.name+" "+baseURL, func(t *testing.T) {
				request := captureCompletionsRequestMap(t, OpenAIConfig{BaseURL: baseURL, Model: "model", ProviderID: "custom", APIKey: "k", Compat: &ModelCompat{VercelGatewayRouting: tc.routing}})
				got := ""
				if options, ok := request["providerOptions"]; ok {
					data, err := json.Marshal(options)
					if err != nil {
						t.Fatal(err)
					}
					got = string(data)
				}
				if got != tc.want {
					t.Fatalf("providerOptions = %q, want %q", got, tc.want)
				}
			})
		}
	}
}

func TestVercelGatewayRoutingDecodesFromCompatJSON(t *testing.T) {
	var compat ModelCompat
	if err := json.Unmarshal([]byte(`{"vercelGatewayRouting":{"only":[],"order":["a"],"unknown":1}}`), &compat); err != nil {
		t.Fatal(err)
	}
	routing := compat.VercelGatewayRouting
	if routing == nil || routing.Only == nil || len(routing.Only) != 0 || len(routing.Order) != 1 {
		t.Fatalf("routing = %#v", routing)
	}
	data, err := json.Marshal(compat)
	if err != nil || string(data) != `{"vercelGatewayRouting":{"only":[],"order":["a"]}}` {
		t.Fatalf("round trip = %s, %v", data, err)
	}
}
