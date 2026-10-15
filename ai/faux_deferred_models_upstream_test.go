package ai

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// .upstream/v0.87.1/packages/ai/test/providers.test.ts:747
// Pi source: packages/ai/src/types.ts, packages/ai/src/compat.ts, packages/ai/src/providers/faux.ts
// mutation-checked: dropping the reads and writes of DeferredHandle.API, DeferredHandle.ID, DeferredHandle.ModelID, DeferredHandle.Provider, FauxConfig.Deferred fails it
// mutation-checked: zeroing the results of FauxProviderState.DeferredFetchCount fails it
// mutation-checked: the mutant "the faux provider ignores StreamOptions.Deferred" (ai/faux.go, the deferred branch of the stream) fails it.
func TestFauxModelsSubmitPollRedeemDeferredUpstream(t *testing.T) {
	faux := NewFauxProvider(FauxConfig{Deferred: &FauxDeferredConfig{PendingFetches: 1, PollAfterMS: new(int64(25))}})
	models := CreateModels(CreateModelsOptions{})
	defer models.Close()
	models.SetProvider(faux.Provider())
	faux.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxText("ready")}, StopReason: "stop"})})
	model := faux.GetModel()
	submission := models.StreamSimple(t.Context(), model, Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}, StreamOptions{Deferred: &DeferredOption{Object: true, Window: "1h"}})
	types := []AssistantEventType{}
	for event := range submission.Events(t.Context()) {
		types = append(types, event.EventType())
	}
	deferred := submission.Result()
	if !reflect.DeepEqual(types, []AssistantEventType{EventStart, EventDone}) || deferred.StopReason != StopReasonDeferred || len(deferred.Content) != 0 || deferred.Deferred == nil {
		t.Fatalf("submission=%#v events=%v", deferred, types)
	}
	handle := deferred.Deferred
	if handle.Provider != faux.ID() || handle.ModelID != model.ID || handle.API != model.ProviderMeta.API || handle.ID == "" || handle.PollAfterMS == nil || *handle.PollAfterMS != 25 {
		t.Fatalf("handle=%#v", handle)
	}
	pending := models.FetchDeferred(t.Context(), model, *handle, DeferredFetchOptions{})
	if pending.StopReason != StopReasonDeferred || !reflect.DeepEqual(pending.Deferred, handle) {
		t.Fatalf("pending=%#v", pending)
	}
	ready := models.FetchDeferred(t.Context(), model, *handle, DeferredFetchOptions{Wait: new(0.0)})
	if ready.StopReason != StopReasonStop || !reflect.DeepEqual(ready.Content, []AssistantContentBlock{TextContent{Text: "ready"}}) || ready.Usage.TotalTokens <= 0 {
		t.Fatalf("ready=%#v", ready)
	}
	if faux.CallCount() != 1 || faux.DeferredFetchCount() != 2 {
		t.Fatalf("calls=%d fetches=%d", faux.CallCount(), faux.DeferredFetchCount())
	}
	// upstream: faux.ts:575 (state.deferredFetchCount++ on every fetch): the shared state counts the pending fetch and the redeeming one.
	if got := faux.state.DeferredFetchCount(); got != 2 {
		t.Fatalf("state.DeferredFetchCount = %d, want 2", got)
	}
}

// .upstream/v0.87.1/packages/ai/test/providers.test.ts:780
// mutation-checked: the mutant "the faux provider ignores StreamOptions.Deferred" (ai/faux.go, the deferred branch of the stream) fails it.
func TestFauxModelsDeferredFailureAndCancellationUpstream(t *testing.T) {
	faux := NewFauxProvider(FauxConfig{})
	models := CreateModels(CreateModelsOptions{})
	defer models.Close()
	models.SetProvider(faux.Provider())
	faux.SetResponses([]FauxResponseStep{FauxFactoryStep(func(TranscriptContext, StreamOptions, *FauxProviderState, *Model) (AssistantMessage, error) {
		return FauxResponse{}.AssistantMessage(), errors.New("deferred failed")
	}), FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxText("cancelled")}, StopReason: "stop"})})
	model := faux.GetModel()
	request := Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}
	failedSubmission := models.CompleteSimple(t.Context(), model, request, StreamOptions{Deferred: &DeferredOption{Enabled: true}})
	if failedSubmission.Deferred == nil {
		t.Fatalf("missing failed submission handle: %#v", failedSubmission)
	}
	failed := models.FetchDeferred(t.Context(), model, *failedSubmission.Deferred, DeferredFetchOptions{})
	if failed.StopReason != StopReasonError || failed.ErrorMessage != "deferred failed" {
		t.Fatalf("failed=%#v", failed)
	}
	cancelledSubmission := models.CompleteSimple(t.Context(), model, request, StreamOptions{Deferred: &DeferredOption{Enabled: true}})
	if cancelledSubmission.Deferred == nil {
		t.Fatalf("missing cancellation handle: %#v", cancelledSubmission)
	}
	if err := models.CancelDeferred(t.Context(), model, *cancelledSubmission.Deferred, DeferredCancelOptions{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(faux.CancelledDeferred(), []DeferredHandle{*cancelledSubmission.Deferred}) {
		t.Fatalf("cancelled=%#v", faux.CancelledDeferred())
	}
	cancelled := models.FetchDeferred(t.Context(), model, *cancelledSubmission.Deferred, DeferredFetchOptions{})
	if cancelled.StopReason != StopReasonError || !strings.Contains(cancelled.ErrorMessage, "was cancelled") {
		t.Fatalf("cancelled=%#v", cancelled)
	}
}

// Pi providers/faux.ts FauxProviderState.deferredFetchCount: a response factory sees the fetch that redeems its deferred submission already counted, and callCount the submission.
// mutation-checked: zeroing the results of FauxProviderState.CallCount fails it
// Pi source: packages/ai/src/providers/faux.ts:447 (state) and :508 (callCount).
// mutation-checked: the mutant "the faux provider ignores StreamOptions.Deferred" (ai/faux.go, the deferred branch of the stream) fails it.
func TestFauxProviderStateCountersReachResponseFactories(t *testing.T) {
	faux := NewFauxProvider(FauxConfig{Deferred: &FauxDeferredConfig{}})
	models := CreateModels(CreateModelsOptions{})
	defer models.Close()
	models.SetProvider(faux.Provider())
	var seenFetches, seenCalls int64 = -1, -1
	faux.SetResponses([]FauxResponseStep{FauxFactoryStep(func(_ TranscriptContext, _ StreamOptions, state *FauxProviderState, _ *Model) (AssistantMessage, error) {
		seenFetches, seenCalls = int64(state.DeferredFetchCount()), int64(state.CallCount())
		return FauxResponse{Content: []FauxContentBlock{FauxText("ok")}, StopReason: "stop"}.AssistantMessage(), nil
	})})
	model := faux.GetModel()
	submission := models.CompleteSimple(t.Context(), model, Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}, StreamOptions{Deferred: &DeferredOption{Enabled: true}})
	if submission.Deferred == nil {
		t.Fatalf("submission %#v", submission)
	}
	if ready := models.FetchDeferred(t.Context(), model, *submission.Deferred, DeferredFetchOptions{Wait: new(0.0)}); ready.StopReason != StopReasonStop {
		t.Fatalf("ready %#v", ready)
	}
	if seenFetches != 1 || seenCalls != 1 {
		t.Fatalf("factory saw deferredFetchCount=%d callCount=%d, want 1 and 1", seenFetches, seenCalls)
	}
}
