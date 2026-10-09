package ai

import (
	"reflect"
	"testing"
)

func fauxCoreRequest() (TranscriptContext, StreamOptions) {
	return fauxUpstreamRequest(), StreamOptions{}
}

func fauxCoreAnswer(text string) FauxResponseStep {
	return FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxText(text)}, StopReason: "stop"})
}

// faux.ts:454-492 createFauxCore: the api is the requested name (else a random "faux:" one), the provider is "faux" unless requested, and the models carry both.
// mutation-checked: a core that ignores the requested api or provider id fails it.
func TestCreateFauxCoreNamesItsApiProviderAndModels(t *testing.T) {
	core := CreateFauxCore(RegisterFauxProviderOptions{API: "custom-api", ProviderID: "custom-provider", Models: []FauxModelDefinition{{ID: "a"}, {ID: "b", Name: "Bee"}}})
	if core.API != "custom-api" || core.Provider != "custom-provider" || len(core.Models) != 2 {
		t.Fatalf("api=%q provider=%q models=%d", core.API, core.Provider, len(core.Models))
	}
	for _, model := range core.Models {
		if model.ProviderMeta.API != "custom-api" || model.ProviderID() != "custom-provider" {
			t.Fatalf("model %q has api %q provider %q", model.ID, model.ProviderMeta.API, model.ProviderID())
		}
	}
	if core.Models[0].DisplayName != "a" || core.Models[1].DisplayName != "Bee" {
		t.Fatalf("model names = %q, %q, want the id when no name is given", core.Models[0].DisplayName, core.Models[1].DisplayName)
	}
	defaults := CreateFauxCore(RegisterFauxProviderOptions{})
	if defaults.Provider != "faux" || len(defaults.API) < len("faux:") || defaults.API[:5] != "faux:" || len(defaults.Models) != 1 {
		t.Fatalf("defaults: api=%q provider=%q models=%d", defaults.API, defaults.Provider, len(defaults.Models))
	}
}

// faux.ts:652-659 getModel(): no id is the first model, an id finds that model, an unknown id is undefined.
func TestCreateFauxCoreGetModelOverloads(t *testing.T) {
	core := CreateFauxCore(RegisterFauxProviderOptions{Models: []FauxModelDefinition{{ID: "a"}, {ID: "b"}}})
	if core.GetModel() != core.Models[0] || core.GetModel("b") != core.Models[1] || core.GetModel("missing") != nil {
		t.Fatal("getModel() is the first model, getModel(id) the model with that id, and an unknown id is nil")
	}
}

// faux.ts:667-675 and :508-571: setResponses copies the queue, appendResponses extends it, getPendingResponseCount counts it, and each stream shifts one response,
// counting the call; once the queue is empty the stream ends with "No more faux responses queued". streamSimple shares stream's queue.
// mutation-checked: a stream that does not shift the queue fails the order and count checks.
func TestCreateFauxCoreStreamsTheQueuedResponsesInOrder(t *testing.T) {
	core := CreateFauxCore(RegisterFauxProviderOptions{})
	request, options := fauxCoreRequest()
	steps := []FauxResponseStep{fauxCoreAnswer("one"), fauxCoreAnswer("two")}
	core.SetResponses(steps)
	steps[0] = fauxCoreAnswer("changed")
	core.AppendResponses([]FauxResponseStep{fauxCoreAnswer("three")})
	if got := core.GetPendingResponseCount(); got != 3 {
		t.Fatalf("pending = %d, want 3", got)
	}
	text := func(message *AssistantMessage) string {
		for _, block := range message.Content {
			if t, ok := block.(TextContent); ok {
				return t.Text
			}
		}
		return ""
	}
	first, err := core.Stream(t.Context(), core.GetModel(), request, options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := core.StreamSimple(t.Context(), core.GetModel(), request, options)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{text(first.Result()), text(second.Result())}; !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatalf("answers = %q, want one then two: setResponses copied the queue and streamSimple shares it", got)
	}
	if core.GetPendingResponseCount() != 1 || core.State.CallCount() != 2 {
		t.Fatalf("pending %d calls %d, want 1 and 2", core.GetPendingResponseCount(), core.State.CallCount())
	}
	third, _ := core.Stream(t.Context(), core.GetModel(), request, options)
	empty, _ := core.Stream(t.Context(), core.GetModel(), request, options)
	if text(third.Result()) != "three" {
		t.Fatalf("third answer = %q", text(third.Result()))
	}
	if result := empty.Result(); result.StopReason != StopReasonError || result.ErrorMessage != "No more faux responses queued" {
		t.Fatalf("an empty queue answered %+v", result)
	}
}

// faux.ts:508-531, :576-650: a deferred submission returns a handle; fetchDeferred counts the fetch and cancelDeferred records the handle in state.
// mutation-checked: a cancelDeferred that does not record the handle fails it.
func TestCreateFauxCoreDeferredFetchAndCancelAreCounted(t *testing.T) {
	core := CreateFauxCore(RegisterFauxProviderOptions{})
	request, _ := fauxCoreRequest()
	core.SetResponses([]FauxResponseStep{fauxCoreAnswer("later"), fauxCoreAnswer("never")})
	submitted, err := core.Stream(t.Context(), core.GetModel(), request, StreamOptions{Deferred: &DeferredOption{Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	handle := submitted.Result().Deferred
	if handle == nil {
		t.Fatal("a deferred submission returned no handle")
	}
	fetched, err := core.FetchDeferred(t.Context(), core.GetModel(), *handle, DeferredFetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result := fetched.Result(); result.StopReason != StopReasonStop {
		t.Fatalf("fetched %+v", result)
	}
	other, _ := core.Stream(t.Context(), core.GetModel(), request, StreamOptions{Deferred: &DeferredOption{Enabled: true}})
	cancelled := other.Result().Deferred
	if err := core.CancelDeferred(t.Context(), core.GetModel(), *cancelled, DeferredCancelOptions{}); err != nil {
		t.Fatal(err)
	}
	if core.State.DeferredFetchCount() != 1 || !reflect.DeepEqual(core.State.CancelledDeferred(), []DeferredHandle{*cancelled}) {
		t.Fatalf("fetches %d cancelled %v", core.State.DeferredFetchCount(), core.State.CancelledDeferred())
	}
}
