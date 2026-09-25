package services

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

// models-provider.ts registers the provider during setup and activates it with BACKGROUND_CONTEXT, not the host caller's cancellation.
func TestModelsFacetOnConcreteChordHost(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	setupFinished := false
	model := testModel("local", "chosen", true)
	lane := &testModelsLane{getModel: func(ctx context.Context) (*ai.Model, error) {
		if !setupFinished {
			t.Error("lane read before all facets completed setup")
		}
		return model, ctx.Err()
	}, thinking: ai.ThinkingHigh}
	facet := CreateModelsServiceFacet(ModelsServiceFacetOptions{Lane: lane})
	checkModelsEqual(t, facet.Id, "@pi/models")
	observer := chord.Facet{Id: "setup-observer", Setup: func(*chord.FacetEnvironment) error { setupFinished = true; return nil }}
	host, err := chord.CreateFacetHost(ctx, chord.FacetOptions{Facets: []chord.Facet{facet, observer}})
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
	checkModelsEqual(t, service.State().Value().Configuration.ThinkingLevel, ai.ThinkingHigh)
	_, err = transport.Invoke(t.Context(), chord.ServiceCall{ServiceId: ModelsDefinition.Id(), Member: "selectThinking", Args: []json.RawMessage{json.RawMessage(`"low"`)}})
	requireModelsOK(t, err)
	checkModelsEqual(t, service.State().Value().Configuration.ThinkingLevel, ai.ThinkingLow)
	if len(updates) != 1 || updates[0].Type != chord.UpdateState || updates[0].Member != "state" {
		t.Fatalf("missing remote state update: %+v", updates)
	}
	requireModelsOK(t, subscription.Close(t.Context()))
}

// Upstream facet service facades retain methods and state across replacement, and revoke access on disposal.
func TestModelsFacetConsumerRetainsGuardedServiceAcrossReload(t *testing.T) {
	local, other := testModel("local", "chosen", true), testModel("other", "chosen", false)
	lane := &testModelsLane{model: local, models: []*ai.Model{local, other}, thinking: ai.ThinkingHigh}
	runtime := &testModelsRuntime{all: lane.models, refresh: func(context.Context) (ModelsRefreshResult, error) { return ModelsRefreshResult{}, nil }}
	options := ModelsServiceFacetOptions{Lane: lane, ModelRuntime: runtime}
	var ref *chord.ServiceRef[Models]
	var service Models
	wantThinkingAtActivation := ai.ThinkingHigh
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
	stop, err := state.Subscribe(func(value *ModelsState, _ context.Context, _ pico3.ReplicatedStateDelivery) {
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
	if _, err := levels(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("remote call lost caller cancellation: %v", err)
	}
	lane = &testModelsLane{model: local, thinking: ai.ThinkingLow}
	options.Lane = lane
	requireModelsOK(t, host.Reload(t.Context(), []chord.Facet{CreateModelsServiceFacet(options)}))
	checkModelsEqual(t, state.Value().Configuration.ThinkingLevel, ai.ThinkingLow)
	checkModelsEqual(t, snapshots[len(snapshots)-1].Configuration.ThinkingLevel, ai.ThinkingLow)
	requireModelsOK(t, cycle(t.Context()))
	checkModelsEqual(t, state.Value().Configuration.ThinkingLevel, ai.ThinkingMedium)
	checkModelsEqual(t, snapshots[len(snapshots)-1].Configuration.ThinkingLevel, ai.ThinkingMedium)
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
	if _, err := state.Subscribe(func(*ModelsState, context.Context, pico3.ReplicatedStateDelivery) {}); err == nil {
		t.Fatal("retained state remained subscribable after consumer retirement")
	}
	requireModelsOK(t, service.CycleThinking(t.Context()))
	checkModelsEqual(t, service.State().Value().Configuration.ThinkingLevel, ai.ThinkingHigh)
	requireModelsOK(t, host.Dispose(t.Context()))
	if err := service.Refresh(t.Context()); err == nil {
		t.Fatal("service remained usable after host disposal")
	}
}

func TestModelsFacetActivationFailurePropagates(t *testing.T) {
	failure := errors.New("lane read failed")
	lane := &testModelsLane{getModel: func(context.Context) (*ai.Model, error) { return nil, failure }, thinking: ai.ThinkingOff}
	host, err := chord.CreateFacetHost(t.Context(), chord.FacetOptions{Facets: []chord.Facet{CreateModelsServiceFacet(ModelsServiceFacetOptions{Lane: lane})}})
	if host != nil || !errors.Is(err, failure) {
		t.Fatalf("host=%v error=%v", host, err)
	}
}
