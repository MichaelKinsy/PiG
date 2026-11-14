package coding

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A provider object's models are what it lists (model-runtime.ts getModel over the provider's getModels); an id it does not list is no model, as for every other provider shape.
// BuildModel used to bind a bare object for it, and a provider that fills its API in itself posted that object to its own host.
func TestBuildModelOnNativeProviderRefusesAnUnlistedModel(t *testing.T) {
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
	model, err := BuildModel("native/m1", services)
	if err != nil {
		t.Fatalf("BuildModel(native/m1): %v", err)
	}
	if model.ID != "m1" || providerID(model) != "native" || model.Provider == nil {
		t.Fatalf("BuildModel(native/m1) = %s/%s provider %v", providerID(model), model.ID, model.Provider)
	}
	events, err := model.Provider.Stream(t.Context(), ai.TranscriptContext{}, ai.StreamOptions{})
	if err != nil {
		t.Fatalf("stream native/m1: %v", err)
	}
	for range events.Events(t.Context()) {
	}
	if unlisted, err := BuildModel("native/m2", services); unlisted != nil || err == nil || err.Error() != "Model not found: native/m2" {
		t.Fatalf("BuildModel(native/m2) = %v, %v; want no model and \"Model not found: native/m2\"", unlisted, err)
	}
	if len(streamed) != 1 || streamed[0] != "native/m1" {
		t.Fatalf("the provider's stream function received %v, want native/m1 only", streamed)
	}
}
