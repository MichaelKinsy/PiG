package coding

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi sdk.ts:375-385 routes the main request through ModelRuntime.streamSimple, whose lazy stream preserves setup errors as provider messages rather than Agent exceptions.
func TestSessionMainRequestUsesItsModelRuntime(t *testing.T) {
	session, provider := newWarmingSession(t, "idle", SessionOptions{NoSession: true})
	model := session.Model()
	var calls atomic.Int32
	session.modelRuntime.prepare = func(context.Context, *ai.Model, ai.StreamOptions) (*ai.Model, ai.Provider, ai.StreamOptions, error) {
		calls.Add(1)
		return nil, nil, ai.StreamOptions{}, errors.New("runtime request sentinel")
	}
	settled := make(chan struct{})
	detach := session.Subscribe(func(event agent.AgentEvent) {
		if _, ok := event.(agent.AgentSettledEvent); ok {
			close(settled)
		}
	})
	defer detach()
	messages, err := session.Send(t.Context(), "exercise the main request")
	if err != nil {
		t.Fatal(err)
	}
	<-settled
	if calls.Load() != 1 || len(provider.calls()) != 0 {
		t.Fatalf("runtime calls=%d direct provider calls=%d; want one runtime preparation and no backend after its failure", calls.Load(), len(provider.calls()))
	}
	message := messages[len(messages)-1].Assistant
	if message == nil || message.API != model.ProviderMeta.API || message.Provider != model.ProviderMeta.ProviderID || message.ModelID != model.ID || len(message.Content) != 0 || message.StopReason != ai.StopReasonError || message.ErrorMessage != "runtime request sentinel" {
		t.Fatalf("runtime failure lost provider identity/content: %+v", message)
	}
}

// A concrete caller provider remains the callback owner even when its model carries metadata. Provider identity is not evidence that the registry built the backend.
func TestModelRuntimePreservesCallerProviderWithMetadata(t *testing.T) {
	for _, api := range []ai.API{ai.APIAnthropicMessages, ai.APIOpenAICompletions, ai.APIOpenAIResponses} {
		t.Run(string(api), func(t *testing.T) {
			services := newTestServices(t)
			provider := &warmingProvider{}
			model := warmingModel(provider)
			model.ProviderMeta.API = api
			options := ai.StreamOptions{MaxTokens: 98765, ReasoningEffort: "caller-option"}
			result := services.ModelRuntime().CompleteSimple(t.Context(), model, ai.Context{}, options)
			calls := provider.calls()
			if result.StopReason != ai.StopReasonStop || len(calls) != 1 {
				t.Fatalf("caller callback replaced: result=%+v calls=%d", result, len(calls))
			}
			if calls[0].MaxTokens != options.MaxTokens || calls[0].ReasoningEffort != options.ReasoningEffort {
				t.Fatalf("caller options lowered: %+v", calls[0])
			}
		})
	}
}
