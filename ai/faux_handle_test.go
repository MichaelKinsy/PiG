package ai

import (
	"reflect"
	"strings"
	"testing"
)

// faux.ts RegisterFauxProviderOptions / FauxProviderHandle: the handle's models come from the options (default one model), getModel(id)
// returns the model or undefined, and setResponses / appendResponses / getPendingResponseCount manage the shared response queue.
// packages/ai/src/providers/faux.ts:141-154,668 (FauxProviderHandle.appendResponses, unregister).
func TestFauxProviderHandleFromRegisterOptions(t *testing.T) {
	options := RegisterFauxProviderOptions{
		ProviderID: "faux-handle-test",
		Models:     []FauxModelDefinition{{ID: "m-one", Name: "One"}, {ID: "m-two", Name: "Two"}},
	}
	handle := NewFauxProvider(options)
	if reflect.TypeOf(handle) != reflect.TypeFor[*FauxProviderHandle]() {
		t.Fatalf("NewFauxProvider returned %T, want *FauxProviderHandle", handle)
	}
	if got := len(handle.Models()); got != 2 {
		t.Fatalf("models = %d, want the two requested", got)
	}
	if first := handle.GetModel(); first == nil || first.ID != "m-one" || first.Provider.ID() != "faux-handle-test" {
		t.Fatalf("default model = %+v", first)
	}
	if second := handle.GetModel("m-two"); second == nil || second.ID != "m-two" {
		t.Fatalf("GetModel(m-two) = %+v", second)
	}
	if handle.GetModel("missing") != nil {
		t.Fatal("an unknown model id returned a model")
	}
	step := FauxStaticStep(FauxResponse{})
	handle.SetResponses([]FauxResponseStep{step, step})
	handle.AppendResponses([]FauxResponseStep{step})
	if handle.PendingResponseCount() != 3 {
		t.Fatalf("pending = %d, want 3", handle.PendingResponseCount())
	}
	handle.SetResponses(nil)
	if handle.PendingResponseCount() != 0 {
		t.Fatalf("pending after SetResponses(nil) = %d", handle.PendingResponseCount())
	}
	if defaults := NewFauxProvider(RegisterFauxProviderOptions{}); len(defaults.Models()) != 1 || defaults.GetModel() == nil {
		t.Fatal("empty options did not yield the one default model")
	}
}

// faux.ts:438 `const api = options.api ?? randomId(DEFAULT_API)` and FauxProviderHandle.api: every model carries the handle's api, an explicit one is kept, and an omitted one is a distinct "faux:" id per handle.
func TestFauxProviderHandleAPIIsSharedByItsModels(t *testing.T) {
	explicit := NewFauxProvider(RegisterFauxProviderOptions{API: "faux-explicit-api", Models: []FauxModelDefinition{{ID: "a"}, {ID: "b"}}})
	if explicit.API() != "faux-explicit-api" {
		t.Fatalf("API() = %q, want the requested api", explicit.API())
	}
	for _, model := range explicit.Models() {
		if model.ProviderMeta.API != explicit.API() {
			t.Errorf("model %s api = %q, want the handle's %q", model.ID, model.ProviderMeta.API, explicit.API())
		}
	}
	first, second := NewFauxProvider(RegisterFauxProviderOptions{}), NewFauxProvider(RegisterFauxProviderOptions{})
	for _, handle := range []*FauxProviderHandle{first, second} {
		if !strings.HasPrefix(string(handle.API()), "faux:") || handle.GetModel().ProviderMeta.API != handle.API() {
			t.Errorf("default api = %q, model api = %q", handle.API(), handle.GetModel().ProviderMeta.API)
		}
	}
	if first.API() == second.API() {
		t.Errorf("two handles share the api %q; each registration needs its own", first.API())
	}
}
