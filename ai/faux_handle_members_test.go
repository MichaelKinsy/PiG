package ai

import (
	"reflect"
	"testing"
)

// faux.ts:146-156 FauxProviderHandle { api, models, state }: api is the one the options gave (faux.ts:438 `options.api ??
// randomId(DEFAULT_API)`) and every model of the handle carries it; state is the object handed to every response
// factory (faux.ts:447), so its counters move with the provider's calls.
// mutation-checked: the mutants "API() returns empty" and "State() returns a fresh state" fail it.
func TestFauxProviderHandleAPIAndStateMatchUpstream(t *testing.T) {
	if reflect.TypeFor[FauxConfig]() != reflect.TypeFor[RegisterFauxProviderOptions]() || reflect.TypeFor[*FauxProviderHandle]() != reflect.TypeFor[*FauxProviderRegistration]() {
		t.Fatal("RegisterFauxProviderOptions and FauxProviderRegistration are not the names of the faux config and handle")
	}

	explicit := NewFauxProvider(RegisterFauxProviderOptions{API: "faux:custom"})
	if explicit.API() != "faux:custom" {
		t.Fatalf("API() = %q, want the configured api", explicit.API())
	}
	generated := NewFauxProvider(FauxConfig{})
	if generated.API() == "" || generated.API() == explicit.API() {
		t.Fatalf("API() = %q, want a generated api distinct per provider", generated.API())
	}
	for _, model := range generated.Models() {
		if model.ProviderMeta.API != generated.API() {
			t.Fatalf("model %s api = %q, want the handle's %q", model.ID, model.ProviderMeta.API, generated.API())
		}
	}

	handle := NewFauxProvider(FauxConfig{})
	var seen *FauxProviderState
	handle.SetResponses([]FauxResponseStep{FauxFactoryStep(func(_ TranscriptContext, _ StreamOptions, state *FauxProviderState, _ *Model) (AssistantMessage, error) {
		seen = state
		return FauxResponse{Content: []FauxContentBlock{FauxText("ok")}, StopReason: "stop"}.AssistantMessage(), nil
	})})
	models := CreateModels(CreateModelsOptions{})
	defer models.Close()
	models.SetProvider(handle.Provider())
	if handle.State().CallCount() != 0 {
		t.Fatalf("a fresh handle reports %d calls", handle.State().CallCount())
	}
	models.CompleteSimple(t.Context(), handle.GetModel(), Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}, StreamOptions{})
	if seen != handle.State() {
		t.Fatal("State() is not the state the response factory received")
	}
	if handle.State().CallCount() != 1 {
		t.Fatalf("State().CallCount() = %d after one call, want 1", handle.State().CallCount())
	}
}
