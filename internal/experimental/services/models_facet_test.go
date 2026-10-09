package services

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

// models-provider.ts:131-157 registers the provider during setup and activates it with BACKGROUND_CONTEXT, not the host caller's cancellation; after activation it follows the agent document.
func TestModelsFacetOnConcreteChordHost(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	model := testModel("local", "chosen", true)
	lane := &testModelsLane{model: refOf(model), thinking: ai.ThinkingHigh}
	facet := CreateModelsServiceFacet(ModelsServiceFacetOptions{Conversation: lane, Agent: lane, ModelRuntime: &testModelsRuntime{all: []*ai.Model{model}}})
	checkModelsEqual(t, facet.Id, "@pi/models")
	host, err := chord.CreateFacetHost(ctx, chord.FacetOptions{Facets: []chord.Facet{facet}})
	requireModelsOK(t, err)
	t.Cleanup(func() { requireModelsOK(t, host.Dispose(context.Background())) })
	endpoint := chord.CreateRemoteServiceEndpoint(host.Services())
	t.Cleanup(endpoint.Dispose)
	transport := chord.NewJSONCopyTransport(endpoint)
	var updates []chord.ServiceProviderUpdate
	subscription, err := transport.Subscribe(t.Context(), ModelsDefinition.Id(), chord.ServiceSingleton, func(_ context.Context, update chord.ServiceProviderUpdate) { updates = append(updates, update) })
	requireModelsOK(t, err)
	requireModelsOK(t, subscription.Activate())
	service, err := chord.Use(host.Services(), ModelsDefinition)
	requireModelsOK(t, err)
	checkModelsEqual(t, service.State().Value().Catalog.Revision, 1)
	checkModelsEqual(t, service.State().Value().Configuration.ThinkingLevel, ai.ModelThinkingLevel(ai.ThinkingHigh))
	_, err = transport.Invoke(t.Context(), chord.ServiceCall{ServiceId: ModelsDefinition.Id(), Member: "selectThinking", Args: []json.RawMessage{json.RawMessage(`"low"`)}})
	requireModelsOK(t, err)
	checkModelsEqual(t, service.State().Value().Configuration.ThinkingLevel, ai.ModelThinkingLevel(ai.ThinkingLow))
	if len(updates) != 1 || updates[0].Type != chord.UpdateState || updates[0].Member != "state" {
		t.Fatalf("missing remote state update: %+v", updates)
	}
	// A change made by another client reaches the state through the document subscription.
	other := ai.ModelThinkingLevel(ai.ThinkingMedium)
	requireModelsOK(t, lane.Configure(t.Context(), ConversationConfiguration{ThinkingLevel: &other}))
	checkModelsEqual(t, service.State().Value().Configuration.ThinkingLevel, ai.ModelThinkingLevel(ai.ThinkingMedium))
	requireModelsOK(t, subscription.Close(t.Context()))
}

// Upstream facet service facades retain methods and state across replacement, and revoke access on disposal.
func TestModelsFacetConsumerRetainsGuardedServiceAcrossReload(t *testing.T) {
	local, other := testModel("local", "chosen", true), testModel("other", "chosen", false)
	lane := &testModelsLane{model: refOf(local), thinking: ai.ThinkingHigh}
	runtime := &testModelsRuntime{all: []*ai.Model{local, other}, refresh: func(context.Context) (ModelsRefreshResult, error) { return ModelsRefreshResult{}, nil }}
	options := ModelsServiceFacetOptions{Conversation: lane, Agent: lane, ModelRuntime: runtime}
	var ref *chord.ServiceRef[Models]
	var service Models
	wantThinkingAtActivation := ai.ModelThinkingLevel(ai.ThinkingHigh)
	consumer := chord.Facet{Id: "models-consumer", Setup: func(env *chord.FacetEnvironment) error {
		var err error
		ref, err = chord.UseService(env, ModelsDefinition)
		if err != nil {
			return err
		}
		return env.OnActivate(func(context.Context) error {
			service, err = ref.Get()
			if err == nil {
				checkModelsEqual(t, service.State().Value().Configuration.ThinkingLevel, wantThinkingAtActivation)
			}
			return err
		})
	}}
	host, err := chord.CreateFacetHost(t.Context(), chord.FacetOptions{Facets: []chord.Facet{consumer, CreateModelsServiceFacet(options)}})
	requireModelsOK(t, err)
	t.Cleanup(func() { requireModelsOK(t, host.Dispose(context.Background())) })
	state := service.State()
	var snapshots []*ModelsState
	stop, err := state.Subscribe(func(value *ModelsState, _ context.Context, _ chord.ReplicatedStateDelivery) {
		snapshots = append(snapshots, value)
	})
	requireModelsOK(t, err)
	t.Cleanup(stop)
	cycle, levels, refresh, selectModel, selectThinking := service.CycleThinking, service.GetThinkingLevels, service.Refresh, service.Select, service.SelectThinking
	available, err := levels(t.Context())
	requireModelsOK(t, err)
	checkModelsEqual(t, available, ai.GetSupportedThinkingLevels(local))
	requireModelsOK(t, cycle(t.Context()))
	checkModelsEqual(t, state.Value().Configuration.ThinkingLevel, ai.ThinkingOff)
	requireModelsOK(t, selectThinking(t.Context(), ai.ThinkingMedium))
	requireModelsOK(t, selectModel(t.Context(), ModelRef{"other", "chosen"}))
	checkModelsEqual(t, state.Value().Configuration.Model, &ModelRef{"other", "chosen"})
	requireModelsOK(t, refresh(t.Context()))
	checkModelsEqual(t, state.Value().Refresh.Status, "done")
	if err := selectThinking(t.Context(), ai.ThinkingHigh); err == nil {
		t.Fatal("remote service lost unsupported-thinking error")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	// getThinkingLevels reads the agent document synchronously (models-provider.ts:57-60), so a cancelled caller no longer changes its result.
	if _, err := levels(ctx); err != nil {
		t.Fatalf("thinking levels with a cancelled caller: %v", err)
	}
	lane = &testModelsLane{model: refOf(local), thinking: ai.ThinkingLow}
	options.Conversation, options.Agent = lane, lane
	requireModelsOK(t, host.Reload(t.Context(), []chord.Facet{CreateModelsServiceFacet(options)}))
	checkModelsEqual(t, state.Value().Configuration.ThinkingLevel, ai.ModelThinkingLevel(ai.ThinkingLow))
	checkModelsEqual(t, snapshots[len(snapshots)-1].Configuration.ThinkingLevel, ai.ModelThinkingLevel(ai.ThinkingLow))
	requireModelsOK(t, cycle(t.Context()))
	checkModelsEqual(t, state.Value().Configuration.ThinkingLevel, ai.ModelThinkingLevel(ai.ThinkingMedium))
	checkModelsEqual(t, snapshots[len(snapshots)-1].Configuration.ThinkingLevel, ai.ModelThinkingLevel(ai.ThinkingMedium))
	wantThinkingAtActivation = ai.ThinkingMedium
	requireModelsOK(t, host.Reload(t.Context(), []chord.Facet{consumer}))
	for _, call := range []func() error{
		func() error { return cycle(t.Context()) },
		func() error { _, err := levels(t.Context()); return err },
		func() error { return refresh(t.Context()) },
		func() error { return selectModel(t.Context(), ModelRef{"local", "chosen"}) },
		func() error { return selectThinking(t.Context(), ai.ThinkingOff) },
	} {
		if err := call(); err == nil {
			t.Fatal("retained method remained usable after consumer retirement")
		}
	}
	if _, err := state.Subscribe(func(*ModelsState, context.Context, chord.ReplicatedStateDelivery) {}); err == nil {
		t.Fatal("retained state remained subscribable after consumer retirement")
	}
	requireModelsOK(t, service.CycleThinking(t.Context()))
	checkModelsEqual(t, service.State().Value().Configuration.ThinkingLevel, ai.ModelThinkingLevel(ai.ThinkingHigh))
	requireModelsOK(t, host.Dispose(t.Context()))
	if err := service.Refresh(t.Context()); err == nil {
		t.Fatal("service remained usable after host disposal")
	}
}

// models-provider.ts:139 the facet owns the agent document it was given and disposes it with the host.
func TestModelsFacetDisposesItsAgentDocument(t *testing.T) {
	lane := &disposeCountingAgent{testModelsLane: &testModelsLane{thinking: ai.ThinkingOff}}
	host, err := chord.CreateFacetHost(t.Context(), chord.FacetOptions{Facets: []chord.Facet{CreateModelsServiceFacet(ModelsServiceFacetOptions{Conversation: lane, Agent: lane})}})
	requireModelsOK(t, err)
	checkModelsEqual(t, lane.disposed, 0)
	requireModelsOK(t, host.Dispose(t.Context()))
	checkModelsEqual(t, lane.disposed, 1)
}

type disposeCountingAgent struct {
	*testModelsLane
	disposed int
}

func (agent *disposeCountingAgent) Dispose() { agent.disposed++ }
