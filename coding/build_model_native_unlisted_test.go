package coding

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A provider object's catalog is what the provider lists, but its stream function takes any model that names it: Pi's prepareRequest only requires the
// model's provider to exist (model-runtime.ts:667-689), and an extension's setModel reaches a model the provider does not list (agent-session.ts:3583-3587).
// BuildModel answered "model not found" for such an id on a provider object while it built a model for an unlisted id of every other provider shape.
func TestBuildModelOnNativeProviderBuildsUnlistedModel(t *testing.T) {
	services := newRegistrationServices(t)
	var streamed []string
	listed := &ai.Model{ID: "m1", ProviderMeta: ai.ProviderMetadata{ProviderID: "native", API: ai.APIOpenAICompletions}, Capabilities: ai.ModelCapabilities{ContextWindow: 128, MaxOutputTokens: 16}}
	stream := func(_ context.Context, model *ai.Model, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		streamed = append(streamed, model.ProviderMeta.ProviderID+"/"+model.ID)
		events := ai.NewAssistantMessageEventStream()
		events.End(&ai.AssistantMessage{StopReason: ai.StopReasonStop})
		return events, nil
	}
	if err := services.ModelRuntime().RegisterNativeProvider(&ai.ModelsProvider{ID: "native", Name: "native", GetModels: func() ([]*ai.Model, error) { return []*ai.Model{listed}, nil },
		Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) {
			return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "test"}}, nil
		}}},
		Stream: stream, StreamSimple: stream,
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"m1", "m2"} {
		model, err := BuildModel("native/"+id, services)
		if err != nil {
			t.Fatalf("BuildModel(native/%s): %v", id, err)
		}
		if model.ID != id || providerID(model) != "native" || model.Provider == nil {
			t.Fatalf("BuildModel(native/%s) = %s/%s provider %v", id, providerID(model), model.ID, model.Provider)
		}
		events, err := model.Provider.Stream(t.Context(), ai.TranscriptContext{}, ai.StreamOptions{})
		if err != nil {
			t.Fatalf("stream native/%s: %v", id, err)
		}
		for range events.Events(t.Context()) {
		}
	}
	if len(streamed) != 2 || streamed[0] != "native/m1" || streamed[1] != "native/m2" {
		t.Fatalf("the provider's stream function received %v, want native/m1 then native/m2", streamed)
	}
}
