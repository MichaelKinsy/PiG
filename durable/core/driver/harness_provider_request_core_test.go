// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"encoding/json"
	"maps"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// The beforeProviderRequest chain of a native Harness runs in the core over each body the provider builds (packages/durable
// test/harness-tools.test.ts "chains beforeProviderRequest over every provider request body", Pi d280e1ff): the hooks chain in
// extension order, each sees the body the one before replaced, and the provider's onPayload returns the chain's result.
func TestCoreHarnessChainsBeforeProviderRequestOverEveryProviderRequestBody(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	var sent []any
	extend := func(payload any, key string) map[string]any {
		next := maps.Clone(payload.(map[string]any))
		next[key] = true
		return next
	}
	hook := func(name string, replace bool) *harness.GenerationHooks {
		return &harness.GenerationHooks{BeforeProviderRequest: func(_ context.Context, request harness.ProviderRequest, _ harness.HookApi) (*harness.ProviderPayload, error) {
			mu.Lock()
			seen = append(seen, name+":"+request.Model.ProviderMeta.ProviderID+"/"+request.Model.ID)
			mu.Unlock()
			if !replace {
				return nil, nil
			}
			return &harness.ProviderPayload{Payload: extend(request.Payload, name)}, nil
		}}
	}
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	registry := harness.CreateRegistry()
	for i, h := range []*harness.GenerationHooks{hook("first", true), hook("keep", false), hook("last", true)} {
		if err := registry.Install(&durable.Extension{
			Name:  "ext" + string(rune('a'+i)),
			Hooks: []durable.HookRegistration{{Task: harness.GenerationTask.AnyDefinition().Name, Handlers: h}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, model *ai.Model) (ai.AssistantMessage, error) {
		for _, body := range []float64{1, 2} {
			replaced, err := options.OnPayload(map[string]any{"body": body}, model)
			if err != nil {
				return ai.AssistantMessage{}, err
			}
			mu.Lock()
			sent = append(sent, replaced)
			mu.Unlock()
		}
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}}.AssistantMessage(), nil
	})})
	h, err := driver.OpenHarness(t.Context(), filepath.Join(t.TempDir(), "store.db"), harness.HarnessOptions{Models: models, Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	root, err := h.Root(t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("go")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	settled, err := sub.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settled.Status != durable.SubmissionDone {
		t.Fatalf("status %s, want done", settled.Status)
	}
	mu.Lock()
	defer mu.Unlock()
	// The core's answer reaches the provider as JSON text (the providers take it as what JSON.parse read, payloadReplacement), in the order
	// the chain wrote its members.
	wantSent := []any{json.RawMessage(`{"body":1,"first":true,"last":true}`), json.RawMessage(`{"body":2,"first":true,"last":true}`)}
	if !reflect.DeepEqual(sent, wantSent) {
		t.Fatalf("sent %v, want %v", sent, wantSent)
	}
	wantSeen := []string{"first:faux/faux-1", "keep:faux/faux-1", "last:faux/faux-1", "first:faux/faux-1", "keep:faux/faux-1", "last:faux/faux-1"}
	if !reflect.DeepEqual(seen, wantSeen) {
		t.Fatalf("seen %v, want %v", seen, wantSeen)
	}
}
