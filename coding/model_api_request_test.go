package coding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func BenchmarkIndependentAPIRequest(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	services, err := NewServices(ServicesOptions{CWD: b.TempDir(), AgentDir: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	ctx := extension.WithModelStreamRequest(b.Context(), extension.ModelStreamRequest{API: true})
	model := &ai.Model{ID: "private", ProviderMeta: ai.ProviderMetadata{ProviderID: "private", BaseURL: server.URL, API: ai.APIOpenAICompletions}}
	request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}
	for b.Loop() {
		result := services.ModelRuntime().Stream(ctx, model, request, ai.StreamOptions{APIKey: "private-key"}).Result()
		if result.StopReason != ai.StopReasonStop {
			b.Fatalf("provider result = %+v", result)
		}
	}
}

// Pi API providers catch cancellation of onResponse as aborted, not error
// (openai-completions.ts:710-716; openai-responses.ts catch).
func TestIndependentAPIRequestAbortDuringResponse(t *testing.T) {
	for _, api := range []ai.API{ai.APIOpenAICompletions, ai.APIOpenAIResponses} {
		t.Run(string(api), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(extension.WithModelStreamRequest(t.Context(), extension.ModelStreamRequest{API: true}))
			defer cancel()
			entered := make(chan struct{})
			model := &ai.Model{ID: "child-only", ProviderMeta: ai.ProviderMetadata{ProviderID: "child-only", BaseURL: server.URL, API: api}}
			stream := services.ModelRuntime().Stream(ctx, model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, ai.StreamOptions{
				APIKey: "child-key", OnResponse: func(ctx context.Context, _ ai.ProviderResponse, _ *ai.Model) error {
					close(entered)
					<-ctx.Done()
					return ctx.Err()
				},
			})
			select {
			case <-entered:
			case <-t.Context().Done():
				t.Fatal("provider response callback was not reached")
			}
			cancel()
			if result := stream.Result(); result.StopReason != ai.StopReasonAborted {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}
