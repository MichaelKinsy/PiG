package coding

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// model-runtime.ts:973-1030 resolveModel: the router sees the latest successful response as `previous` (an errored or aborted response is skipped, an earlier success does not replace a later one),
// the request's messages as given, and fails with Pi's messages when the virtual model is not registered, when it routes to a model that is not physical, or to one without credentials.
func TestResolveModelReportsTheLatestSuccessfulResponseAndTheMessagesToTheRouter(t *testing.T) {
	f := createVirtualRuntime(t)
	small, large := f.runtime.GetModel("faux", "small"), f.runtime.GetModel("faux", "large")
	errored := assistantFromModel(large, "")
	errored.StopReason = ai.StopReasonError
	aborted := assistantFromModel(large, "")
	aborted.StopReason = ai.StopReasonAborted
	latest := assistantFromModel(small, "latest")
	latest.ThinkingLevel = ai.ThinkingLow
	earlier := assistantFromModel(large, "earlier")
	earlier.ThinkingLevel = ai.ThinkingHigh
	messages := []ai.Message{userText("a", 1), earlier, userText("b", 2), latest, userText("c", 3), errored, aborted}
	if _, err := f.runtime.ResolveModel(t.Context(), f.virtual, messages, ResolveModelOptions{Reason: ModelRouteReasonUser, ThinkingLevel: ai.ThinkingLow}); err != nil {
		t.Fatal(err)
	}
	got := (*f.requests)[0]
	if got.Previous == nil || got.Previous.Model.ID != "small" || got.Previous.ThinkingLevel != ai.ThinkingLow {
		t.Fatalf("previous = %+v, want the latest successful response (small, low)", got.Previous)
	}
	if !reflect.DeepEqual(got.Messages, messages) {
		t.Fatalf("the router saw %d messages, want the %d given", len(got.Messages), len(messages))
	}
	if got.Model != f.virtual || got.Reason != ModelRouteReasonUser || got.ThinkingLevel != ai.ThinkingLow {
		t.Fatalf("request = %+v", got)
	}
}

func TestResolveModelFailsWithPisMessagesWhenTheRouteIsNotUsable(t *testing.T) {
	f := createVirtualRuntime(t)
	messages := []ai.Message{userText("a", 1)}
	if _, err := f.runtime.ResolveModel(t.Context(), f.runtime.GetModel("faux", "small"), messages, ResolveModelOptions{Reason: ModelRouteReasonUser}); err == nil || err.Error() != "Virtual model faux/small is not registered." {
		t.Fatalf("unregistered = %v", err)
	}
	routeTo := func(model func() *ai.Model) error {
		definition := f.definition
		definition.ID = "picky"
		definition.Route = func(context.Context, ModelRouteRequest) (ModelRoute, error) {
			return ModelRoute{Model: model(), ThinkingLevel: ai.ThinkingOff}, nil
		}
		if err := f.runtime.RegisterVirtualModel(definition); err != nil {
			t.Fatal(err)
		}
		_, err := f.runtime.ResolveModel(t.Context(), f.runtime.GetModel("router", "picky"), messages, ResolveModelOptions{Reason: ModelRouteReasonUser})
		return err
	}
	if err := routeTo(func() *ai.Model { return f.virtual }); err == nil || err.Error() != "Virtual model router/picky routed to router/auto, which is not a physical model." {
		t.Fatalf("virtual target = %v", err)
	}
	unconfigured := &ai.ModelsProvider{ID: "nokey", Name: "nokey", GetModels: func() ([]*ai.Model, error) {
		return []*ai.Model{{ID: "m", DisplayName: "M", Input: []string{"text"}, ProviderMeta: ai.ProviderMetadata{ProviderID: "nokey", API: ai.APIOpenAICompletions, BaseURL: "https://example.test/v1"}, Capabilities: ai.ModelCapabilities{ContextWindow: 1000, MaxOutputTokens: 100}}}, nil
	}, Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "API key", Check: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthCheck, error) { return nil, nil }}}}
	if err := f.runtime.RegisterNativeProvider(unconfigured); err != nil {
		t.Fatal(err)
	}
	if result := f.runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); result.Aborted || len(result.Errors) != 0 {
		t.Fatalf("refresh = %+v", result)
	}
	if err := routeTo(func() *ai.Model { return f.runtime.GetModel("nokey", "m") }); err == nil || err.Error() != "Virtual model router/picky routed to nokey/m, which has no credentials." {
		t.Fatalf("unconfigured target = %v", err)
	}
}
