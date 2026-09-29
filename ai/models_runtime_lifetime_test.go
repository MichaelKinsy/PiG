package ai

import (
	"context"
	"errors"
	"testing"
)

func TestModelsPublicationRejectsCompletedOldGeneration(t *testing.T) {
	models := CreateModels()
	var publish func(ModelsPublication) (bool, error)
	models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "dynamic", refreshModels: func(refresh RefreshModelsContext) error { publish = refresh.Publish; return nil }}))
	result := models.Refresh(t.Context(), ModelsRefreshOptions{AllowNetwork: new(false)})
	if len(result.Errors) != 0 || publish == nil {
		t.Fatalf("result=%+v", result)
	}
	models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "dynamic"}))
	updated := false
	accepted, err := publish(ModelsPublication{Update: func() { updated = true }})
	if err != nil || accepted || updated {
		t.Fatalf("accepted=%v updated=%v err=%v", accepted, updated, err)
	}
}

func TestModelsCompletedRefreshSignalFollowsItsCaller(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(context.Canceled)
	models := CreateModels()
	var signal context.Context
	models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "dynamic", refreshModels: func(refresh RefreshModelsContext) error { signal = refresh.Signal; return nil }}))
	result := models.Refresh(ctx)
	if result.Aborted || signal == nil || signal.Err() != nil {
		t.Fatalf("result=%+v signal=%v", result, signal)
	}
	cause := errors.New("later caller cancellation")
	cancel(cause)
	if signal.Err() == nil || context.Cause(signal) != cause { //nolint:errorlint // AbortSignal.any preserves the identical reason, not merely a wrapped match.
		t.Fatalf("signal error=%v cause=%v", signal.Err(), context.Cause(signal))
	}
}

func BenchmarkModelsRuntimeAuthDispatch(b *testing.B) {
	models := CreateModels()
	models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", auth: &ProviderAuth{APIKey: modelsRuntimeEnvKey("key")}}))
	model := modelsRuntimeModel("p1", "model-a")
	request := modelsRuntimeContext()
	b.ReportAllocs()
	for b.Loop() {
		if message := models.CompleteSimple(b.Context(), model, request); message.StopReason != StopReasonStop {
			b.Fatal(message.ErrorMessage)
		}
	}
	models.operations.Wait()
}

func BenchmarkModelsRuntimeRefresh(b *testing.B) {
	ctx, cancel := context.WithCancel(b.Context())
	defer cancel()
	models := CreateModels()
	models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "dynamic", refreshModels: func(RefreshModelsContext) error { return nil }}))
	b.ReportAllocs()
	for b.Loop() {
		if result := models.Refresh(ctx); result.Aborted || len(result.Errors) != 0 {
			b.Fatal(result)
		}
	}
	models.operations.Wait()
}
