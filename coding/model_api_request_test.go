package coding

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

// An extension that registers a provider with streamSimple and calls pi-ai's
// compat stream()/streamSimple() with one of that provider's models reaches
// the pi-ai API leaf for model.api, not its own streamSimple again, even when
// the provider registered its streamSimple for that same api. Pi's
// compat dispatch resolves `getApiProvider(model.api)` and never consults
// the extension provider registry (packages/ai/src/compat.ts:278-293). Re-entering the
// registered callback recursed without bound (issue #164, pi-commandcode-provider).
func TestIndependentAPIRequestSkipsRegisteredProviderCallback(t *testing.T) {
	for _, tc := range []struct {
		api      ai.API
		response string
	}{
		{ai.APIOpenAICompletions, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"},
		{ai.APIOpenAIResponses, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"},
	} {
		for _, simple := range []bool{false, true} {
			for _, registration := range []struct {
				providerID string
				api        ai.API
			}{{"commandcode", "custom-api"}, {"commandcode", tc.api}, {"openai", "custom-api"}, {"openai", tc.api}} {
				providerID := registration.providerID
				t.Run(fmt.Sprintf("%s/%s/registered=%s/simple=%t", providerID, tc.api, registration.api, simple), func(t *testing.T) {
					var requests, callbacks atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						requests.Add(1)
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = w.Write([]byte(tc.response))
					}))
					defer server.Close()
					services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
					if err != nil {
						t.Fatal(err)
					}
					if err := services.Registry().RegisterProvider(providerID, extension.ProviderConfig{API: registration.api, BaseURL: "https://extension.invalid", APIKey: "key", Models: []extension.ProviderModelConfig{{ID: "model", Name: "Model", ContextWindow: 128, MaxTokens: 16}}, StreamSimple: func(extension.Model, extension.AIContext, extension.SimpleStreamOptions) extension.AssistantMessageEventStream {
						callbacks.Add(1)
						stream := ai.NewAssistantMessageEventStream()
						stream.End(&ai.AssistantMessage{Provider: providerID, Model: "model", StopReason: ai.StopReasonError, ErrorMessage: "registered streamSimple re-entered"})
						return stream
					}}); err != nil {
						t.Fatal(err)
					}
					ctx := extension.WithModelStreamRequest(extension.WithProviderStreamSimple(t.Context(), simple), extension.ModelStreamRequest{API: true})
					model := &ai.Model{ID: "model", ProviderMeta: ai.ProviderMetadata{ProviderID: providerID, BaseURL: server.URL, API: tc.api}, Capabilities: ai.ModelCapabilities{ContextWindow: 128, MaxOutputTokens: 16}}
					request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}
					var result *ai.AssistantMessage
					if simple {
						result = services.ModelRuntime().StreamSimple(ctx, model, request, ai.StreamOptions{APIKey: "key"}).Result()
					} else {
						result = services.ModelRuntime().Stream(ctx, model, request, ai.StreamOptions{APIKey: "key"}).Result()
					}
					if callbacks.Load() != 0 || requests.Load() != 1 || result.StopReason != ai.StopReasonStop {
						t.Fatalf("callbacks=%d requests=%d result=%+v", callbacks.Load(), requests.Load(), result)
					}
				})
			}
		}
	}
}
