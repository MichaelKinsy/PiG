package ai

import (
	"reflect"
	"sync"
	"testing"
)

// faux.ts FauxProviderState { callCount, deferredFetchCount, cancelledDeferred } is handed to every FauxResponseFactory
// (faux.ts:109) and counts the provider's calls, deferred fetches and cancelled handles (faux.ts:508, 575, 640).
// mutation-checked: the mutant "the faux provider ignores StreamOptions.Deferred" (ai/faux.go, the deferred branch of the stream) fails it.
func TestFauxProviderStateIsVisibleToFactoryUpstream(t *testing.T) {
	faux := NewFauxProvider(FauxConfig{})
	models := CreateModels(CreateModelsOptions{})
	defer models.Close()
	models.SetProvider(faux.Provider())
	model := faux.GetModel()
	request := Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}

	var seen *FauxProviderState
	var factory FauxResponseFactory = func(_ TranscriptContext, _ StreamOptions, state *FauxProviderState, _ *Model) (AssistantMessage, error) {
		seen = state
		return FauxResponse{Content: []FauxContentBlock{FauxText("ok")}, StopReason: "stop"}.AssistantMessage(), nil
	}
	faux.SetResponses([]FauxResponseStep{FauxFactoryStep(factory), FauxFactoryStep(factory)})

	submission := models.CompleteSimple(t.Context(), model, request, StreamOptions{Deferred: &DeferredOption{Enabled: true}})
	if submission.Deferred == nil {
		t.Fatalf("missing deferred handle: %#v", submission)
	}
	if got := models.FetchDeferred(t.Context(), model, *submission.Deferred, DeferredFetchOptions{Wait: new(0.0)}); got.StopReason != StopReasonStop {
		t.Fatalf("fetched=%#v", got)
	}
	if seen == nil || seen.CallCount() != 1 || seen.DeferredFetchCount() != 1 || len(seen.CancelledDeferred()) != 0 {
		t.Fatalf("state seen by the factory: calls=%d fetches=%d cancelled=%v", seen.CallCount(), seen.DeferredFetchCount(), seen.CancelledDeferred())
	}

	second := models.CompleteSimple(t.Context(), model, request, StreamOptions{Deferred: &DeferredOption{Enabled: true}})
	if second.Deferred == nil {
		t.Fatalf("missing second handle: %#v", second)
	}
	if err := models.CancelDeferred(t.Context(), model, *second.Deferred, DeferredCancelOptions{}); err != nil {
		t.Fatal(err)
	}
	if seen.CallCount() != 2 || seen.DeferredFetchCount() != 1 || !reflect.DeepEqual(seen.CancelledDeferred(), []DeferredHandle{*second.Deferred}) {
		t.Fatalf("state after cancellation: calls=%d fetches=%d cancelled=%v", seen.CallCount(), seen.DeferredFetchCount(), seen.CancelledDeferred())
	}
	if faux.CallCount() != seen.CallCount() || faux.DeferredFetchCount() != seen.DeferredFetchCount() || !reflect.DeepEqual(faux.CancelledDeferred(), seen.CancelledDeferred()) {
		t.Fatal("the provider reports a different state from the one its factories see")
	}
}

// structuredClone(handle) in faux.ts:640 stores a copy: changing the returned handles does not change the state.
// Pi source: packages/ai/src/providers/faux.ts
// mutation-checked: zeroing the results of FauxProviderState.CancelledDeferred fails it
func TestFauxProviderStateCancelledDeferredIsACopyUpstream(t *testing.T) {
	var state FauxProviderState
	poll := int64(25)
	state.recordCancelledDeferred(DeferredHandle{ID: "a", PollAfterMS: &poll})
	poll = 99
	got := state.CancelledDeferred()
	*got[0].PollAfterMS = 7
	got[0].ID = "changed"
	again := state.CancelledDeferred()
	if len(again) != 1 || again[0].ID != "a" || *again[0].PollAfterMS != 25 {
		t.Fatalf("state=%#v", again)
	}
}

// A factory runs while other streams update the state; the accessors must not race (go test -race).
func TestFauxProviderStateConcurrentAccess(t *testing.T) {
	var state FauxProviderState
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			state.callCount.Add(1)
			state.deferredFetchCount.Add(1)
			state.recordCancelledDeferred(DeferredHandle{ID: "x"})
			_ = state.CancelledDeferred()
		})
	}
	wg.Wait()
	if state.CallCount() != 8 || state.DeferredFetchCount() != 8 || len(state.CancelledDeferred()) != 8 {
		t.Fatalf("calls=%d fetches=%d cancelled=%d", state.CallCount(), state.DeferredFetchCount(), len(state.CancelledDeferred()))
	}
}
