package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func assertThinkingLevelPresence(t *testing.T, model *GeneratedModel, want map[ModelThinkingLevel]*string) {
	t.Helper()
	for level, value := range want {
		got, present := model.ThinkingLevelMap[level]
		if !present || !reflect.DeepEqual(got, value) {
			t.Errorf("%s/%s thinkingLevelMap[%s] = %v (present=%t), want %v", model.Provider, model.ID, level, got, present, value)
		}
	}
}

// upstream: packages/ai/src/types.ts:147-154 and packages/ai/test/cloudflare-ai-binding.test.ts:70.
// The same request-scoped transport reaches each HTTP SDK path; provider construction must not capture a previous request's fetch.
func TestPortWave12ProviderFetchTransport(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("request escaped its fetch transport")
		http.Error(w, "unexpected dispatch", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	for _, row := range []struct {
		name     string
		provider Provider
		path     string
	}{
		{"openai-completions", NewOpenAIProvider(OpenAIConfig{BaseURL: server.URL + "/v1", Model: "test-model", APIKey: "test"}), "/v1/chat/completions"},
		{"openai-responses", NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: server.URL + "/v1", Model: "test-model", APIKey: "test"}), "/v1/responses"},
		{"anthropic-messages", NewAnthropicProvider(AnthropicConfig{BaseURL: server.URL, Model: "test-model", APIKey: "test"}), "/v1/messages?beta=true"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Cleanup(func() {
				if err := row.provider.Close(); err != nil {
					t.Error(err)
				}
			})
			// Models owns the public error-event boundary; the bound Provider can return setup errors directly.
			runtime := CreateModels()
			t.Cleanup(runtime.operations.Wait)
			runtime.SetProvider(&ModelsProvider{
				ID: row.name, Auth: ProviderAuth{APIKey: EnvAPIKeyAuth("test")},
				Stream: func(ctx context.Context, _ *Model, request TranscriptContext, opts StreamOptions) (*AssistantMessageEventStream, error) {
					return row.provider.Stream(ctx, request, opts)
				},
			})
			model := &Model{ID: "test-model", ProviderMeta: ProviderMetadata{API: API(row.name), ProviderID: row.name}}
			for _, message := range []string{"first fetch", "replacement fetch"} {
				body := `{"error":{"message":"` + message + `"}}`
				calls := 0
				stream := runtime.Stream(t.Context(), model, Context{Messages: []Message{UserMessage{Content: UserText("hello"), Timestamp: 1}}}, StreamOptions{
					APIKey: "test", MaxRetries: new(0), Fetch: &http.Client{Transport: FetchFunction(func(request *http.Request) (*http.Response, error) {
						calls++
						if request.URL.String() != server.URL+row.path || request.Method != http.MethodPost {
							t.Errorf("request = %s %s", request.Method, request.URL)
						}
						if _, err := io.ReadAll(request.Body); err != nil {
							return nil, err
						}
						if err := request.Body.Close(); err != nil {
							return nil, err
						}
						return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
					})},
				})
				if result := stream.Result(); calls != 1 || result.StopReason != StopReasonError || !strings.Contains(result.ErrorMessage, message) {
					t.Fatalf("fetch calls=%d, result=%+v; want one call and %q error", calls, result, message)
				}
			}
		})
	}
}
