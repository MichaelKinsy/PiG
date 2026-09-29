package ai

import "testing"

func BenchmarkMutableModelsFauxDispatch(b *testing.B) {
	faux := NewFauxProvider(FauxConfig{})
	models := CreateModels(CreateModelsOptions{})
	defer models.Close()
	models.SetProvider(faux.Provider())
	request := Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}
	b.ReportAllocs()
	for b.Loop() {
		faux.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxText("ready")}, StopReason: "stop"})})
		result := models.CompleteSimple(b.Context(), faux.GetModel(), request, StreamOptions{})
		if result.StopReason != StopReasonStop {
			b.Fatal(result.ErrorMessage)
		}
	}
}

func BenchmarkMutableModelsFauxDeferred(b *testing.B) {
	request := Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}
	b.ReportAllocs()
	for b.Loop() {
		faux := NewFauxProvider(FauxConfig{Deferred: &FauxDeferredConfig{PendingFetches: 1}})
		models := CreateModels(CreateModelsOptions{})
		models.SetProvider(faux.Provider())
		faux.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxText("ready")}, StopReason: "stop"})})
		model := faux.GetModel()
		submitted := models.CompleteSimple(b.Context(), model, request, StreamOptions{Deferred: &DeferredOption{Enabled: true}})
		if submitted.Deferred == nil {
			b.Fatal("missing handle")
		}
		models.FetchDeferred(b.Context(), model, *submitted.Deferred, DeferredFetchOptions{})
		ready := models.FetchDeferred(b.Context(), model, *submitted.Deferred, DeferredFetchOptions{Wait: new(0.0)})
		if ready.StopReason != StopReasonStop {
			b.Fatal(ready.ErrorMessage)
		}
		models.Close()
	}
}
