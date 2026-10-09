package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// packages/ai/test/azure-openai-base-url.test.ts (stream(model, context, { azureBaseUrl })) and api/azure-openai-config.ts: the Azure endpoint
// options are options of each request, not of the provider. azureBaseUrl, azureResourceName, azureApiVersion and azureDeploymentName outrank
// the AZURE_OPENAI_* variables, and one provider serves requests that differ in them.
type azureWire struct {
	path, query, model string
}

type azureWireServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []azureWire
}

func newAzureWireServer(t *testing.T, sse string) *azureWireServer {
	t.Helper()
	s := &azureWireServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(raw, &body)
		s.mu.Lock()
		s.requests = append(s.requests, azureWire{r.URL.Path, r.URL.RawQuery, body.Model})
		s.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *azureWireServer) last(t *testing.T) azureWire {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		t.Fatal("no request reached the server")
	}
	return s.requests[len(s.requests)-1]
}

const (
	azureResponsesDoneSSE   = "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
	azureCompletionsDoneSSE = "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
)

func TestAzureEndpointOptionsApplyPerRequestToBothAzureProviders(t *testing.T) {
	t.Setenv("AZURE_OPENAI_BASE_URL", "http://127.0.0.1:1/never")
	t.Setenv("AZURE_OPENAI_API_VERSION", "env-version")
	t.Setenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP", "gpt-4o-mini=env-deployment")
	request := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello"), Timestamp: 1}}})
	for _, tc := range []struct {
		name         string
		sse          string
		build        func() Provider
		wantPath     string
		wantAPIQuery func(version string) string
	}{
		{"azure-openai-responses", azureResponsesDoneSSE, func() Provider {
			return NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{APIKey: "k", Model: "gpt-4o-mini"})
		}, "/openai/v1/responses", func(v string) string { return "api-version=" + v }},
		{"azure-completions", azureCompletionsDoneSSE, func() Provider {
			return NewOpenAIProvider(OpenAIConfig{APIKey: "k", Model: "gpt-4o-mini", ProviderID: azureProviderID})
		}, "/openai/v1/chat/completions", func(string) string { return "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := newAzureWireServer(t, tc.sse)
			provider := tc.build()
			defer func() { _ = provider.Close() }()
			run := func(opts StreamOptions) azureWire {
				t.Helper()
				stream, err := provider.Stream(t.Context(), request, opts)
				if err != nil {
					t.Fatal(err)
				}
				if result := stream.Result(); result.StopReason != StopReasonStop {
					t.Fatalf("result = %+v", result)
				}
				return server.last(t)
			}

			// azureBaseUrl, azureApiVersion and azureDeploymentName each outrank their AZURE_OPENAI_* variable.
			first := run(StreamOptions{AzureBaseURL: server.URL + "/openai/v1", AzureAPIVersion: "2025-01-01", AzureDeploymentName: "deployment-one"})
			if first.path != tc.wantPath || first.model != "deployment-one" || first.query != tc.wantAPIQuery("2025-01-01") {
				t.Fatalf("first request = %+v", first)
			}
			// The same provider then serves a request with other options: the options are not fixed at construction.
			second := run(StreamOptions{AzureBaseURL: server.URL + "/openai/v1/", AzureAPIVersion: "2026-02-02", AzureDeploymentName: "deployment-two"})
			if second.model != "deployment-two" || second.query != tc.wantAPIQuery("2026-02-02") || second.path != tc.wantPath {
				t.Fatalf("second request = %+v", second)
			}
			// Without a deployment option the variable map applies, then the endpoint variable is the fallback after the option.
			third := run(StreamOptions{AzureBaseURL: server.URL + "/openai/v1"})
			if third.model != "env-deployment" || third.query != tc.wantAPIQuery("env-version") {
				t.Fatalf("third request = %+v", third)
			}
		})
	}
}

// azureResourceName builds https://<resource>.openai.azure.com/openai/v1; the request fails before any I/O when no endpoint resolves.
func TestAzureResponsesWithoutAnEndpointFailsTheRequest(t *testing.T) {
	t.Setenv("AZURE_OPENAI_BASE_URL", "")
	t.Setenv("AZURE_OPENAI_RESOURCE_NAME", "")
	provider := NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{APIKey: "k", Model: "gpt-4o-mini"})
	request := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello"), Timestamp: 1}}})
	if _, err := provider.Stream(t.Context(), request, StreamOptions{AzureResourceName: " "}); err == nil || !strings.Contains(err.Error(), "Azure OpenAI base URL") {
		t.Fatalf("err = %v, want the base URL error", err)
	}
}
