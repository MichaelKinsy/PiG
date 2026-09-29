package ai

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// .upstream/v0.87.1/packages/ai/test/providers.test.ts:747
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
}

// .upstream/v0.87.1/packages/ai/test/providers.test.ts:780
func TestFauxModelsDeferredFailureAndCancellationUpstream(t *testing.T) {
	faux := NewFauxProvider(FauxConfig{})
	models := CreateModels(CreateModelsOptions{})
	defer models.Close()
	models.SetProvider(faux.Provider())
	faux.SetResponses([]FauxResponseStep{FauxFactoryStep(func(TranscriptContext, StreamOptions, *FauxProviderState, *Model) (FauxResponse, error) {
		return FauxResponse{}, errors.New("deferred failed")
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
