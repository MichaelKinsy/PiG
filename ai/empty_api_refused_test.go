package ai

// pi: packages/ai/src/compat.ts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// A model object with no api reaches Pi streamSimple and stream with api undefined: getApiProvider(undefined) is a Map miss and resolveApiProvider throws
// "No API provider registered for api: undefined" (compat.ts:244-247, :290-293), a missing key and a built-in provider id included. The Go model with an empty api name is that object.
func TestEmptyAPINameIsRefusedAtEveryStreamEntry(t *testing.T) {
	const want = "No API provider registered for api: undefined"
	entries := map[string]func(context.Context, *Model, string) (*AssistantMessageEventStream, error){
		"StreamSimple": func(ctx context.Context, m *Model, key string) (*AssistantMessageEventStream, error) {
			return StreamSimple(ctx, m, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{APIKey: key})
		},
		"Stream": func(ctx context.Context, m *Model, key string) (*AssistantMessageEventStream, error) {
			return Stream(ctx, m, Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}, StreamOptions{APIKey: key})
		},
		"StreamSimpleContext": func(ctx context.Context, m *Model, key string) (*AssistantMessageEventStream, error) {
			return StreamSimpleContext(ctx, m, Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}, StreamOptions{APIKey: key})
		},
	}
	for name, stream := range entries {
		for _, provider := range []string{"odd", "openai"} {
			for _, key := range []string{"k", ""} {
				t.Run(name+"/"+provider+"/key="+key, func(t *testing.T) {
					var requests atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						requests.Add(1)
						w.WriteHeader(http.StatusBadRequest)
					}))
					defer server.Close()
					model := &Model{ID: "m", ProviderMeta: ProviderMetadata{ProviderID: provider, BaseURL: server.URL + "/v1"}, Input: []string{"text"}}
					got, err := stream(t.Context(), model, key)
					if err == nil {
						t.Fatalf("the stream started: %+v", got.Result())
					}
					if err.Error() != want {
						t.Errorf("error = %q, want %q", err, want)
					}
					if n := requests.Load(); n != 0 {
						t.Errorf("requests at the endpoint = %d, want 0", n)
					}
				})
			}
		}
	}
}
