// Package attachstall stalls a straight-line stream consumer in its attach window, the way a loaded scheduler descheduling its goroutine would, but for as long as it takes the provider to do everything it can without the consumer. Only tests import it.
package attachstall

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/streamattachhook"
)

// Install stalls every consumer that calls streamattachhook.Attach until the provider has finished its whole response, the longest stall a descheduled goroutine can suffer, and the order in which a consumer outside a stream continuation reads a start event holding the finished message.
//
// A consumer inside its creating continuation is not stalled: it holds the executor, so the provider's response turn cannot start however long the consumer takes, and there is nothing to wait for (waiting for the result there would wait for the turn it holds).
func Install(t *testing.T) {
	t.Helper()
	restore := streamattachhook.Install(func(ctx context.Context, stream *ai.AssistantMessageEventStream) {
		if ai.StreamObservationFromContext(ctx) == nil {
			stream.Result()
		}
	})
	t.Cleanup(restore)
}

// ModelsOverOpenAI is a Models whose one provider, "probe", is the real openai-completions provider on a fixture server that answers one chunk, "DONE". A faux provider is not enough: its partials are not the ones a built-in provider shares with its consumers.
func ModelsOverOpenAI(t *testing.T) (models *ai.Models, model *ai.Model) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"DONE\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	provider := ai.NewOpenAIProvider(ai.OpenAIConfig{BaseURL: server.URL + "/v1", Model: "probe-1", APIKey: "test", ProviderID: "probe"})
	t.Cleanup(func() { _ = provider.Close() })
	model = &ai.Model{ID: "probe-1", Capabilities: ai.ModelCapabilities{ContextWindow: 100_000, MaxOutputTokens: 1000}, ProviderMeta: ai.ProviderMetadata{API: ai.APIOpenAICompletions, ProviderID: "probe"}}
	models = ai.CreateModels()
	models.SetProvider(&ai.ModelsProvider{
		ID: "probe", Name: "probe", Auth: ai.ProviderAuth{APIKey: ai.EnvAPIKeyAuth("test")}, 		GetModels:    func() ([]*ai.Model, error) { return []*ai.Model{model}, nil },
		Stream:       func(ctx context.Context, _ *ai.Model, request ai.TranscriptContext, opts ai.StreamOptions) (*ai.AssistantMessageEventStream, error) { return provider.Stream(ctx, request, opts) },
		StreamSimple: func(ctx context.Context, _ *ai.Model, request ai.TranscriptContext, opts ai.StreamOptions) (*ai.AssistantMessageEventStream, error) { return provider.Stream(ctx, request, opts) },
	})
	return models, model
}
