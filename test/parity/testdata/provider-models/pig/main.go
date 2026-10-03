package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/ai"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func model(api ai.API, id, provider string) *ai.Model {
	return &ai.Model{ID: id, DisplayName: id, ProviderMeta: ai.ProviderMetadata{API: api, ProviderID: provider, BaseURL: "https://example.test/v1"}, Input: []string{"text"}, Capabilities: ai.ModelCapabilities{ContextWindow: 10000, MaxOutputTokens: 1000}}
}
func auth() ai.ProviderAuth {
	return ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "Test", Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) { return &ai.AuthResult{}, nil }}}
}
func recorder(label string, calls *[]string) *ai.ProviderStreams {
	respond := func(_ context.Context, model *ai.Model, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		if calls != nil {
			*calls = append(*calls, label+":"+model.ID)
		}
		message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "ok"}}, StopReason: ai.StopReasonStop}
		stream := ai.NewAssistantMessageEventStream()
		if err := stream.Push(ai.StartEvent{Partial: message}); err != nil {
			return nil, err
		}
		if err := stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}); err != nil {
			return nil, err
		}
		return stream, nil
	}
	return &ai.ProviderStreams{Stream: respond, StreamSimple: respond}
}
func run() error {
	ctx := context.Background()
	request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}
	output := map[string]any{}
	models := ai.CreateModels(ai.CreateModelsOptions{})
	defer models.Close()
	calls := []string{}
	mixed := ai.CreateProvider(ai.CreateProviderOptions{ID: "mixed", Auth: auth(), Models: []ai.AnyModel{model("api-a", "model-a", "mixed"), model("api-b", "model-b", "mixed")}, API: ai.ProviderAPIMap{"api-a": recorder("a", &calls), "api-b": recorder("b", &calls)}})
	models.SetProvider(mixed)
	for _, row := range [][2]string{{"api-a", "model-a"}, {"api-b", "model-b"}} {
		result := models.CompleteSimple(ctx, model(ai.API(row[0]), row[1], "mixed"), request, ai.StreamOptions{})
		if result.StopReason != ai.StopReasonStop {
			return errors.New(result.ErrorMessage)
		}
	}
	output["dispatch"] = calls
	ghost, err := mixed.StreamSimple(ctx, model("api-ghost", "model-x", "mixed"), ai.NormalizeContext(request), ai.StreamOptions{})
	if err != nil {
		return err
	}
	output["missingAPI"] = ghost.Result().ErrorMessage
	loads := 0
	lazyStreams := recorder("lazy", nil)
	lazyStreams.FetchDeferred = func(ctx context.Context, model *ai.Model, _ ai.DeferredHandle, _ ai.DeferredFetchOptions) (*ai.AssistantMessageEventStream, error) {
		return lazyStreams.StreamSimple(ctx, model, ai.NormalizeContext(request), ai.StreamOptions{})
	}
	lazy := ai.LazyAPI(func(context.Context) (*ai.ProviderStreams, error) { loads++; return lazyStreams, nil }, ai.LazyAPICapabilities{FetchDeferred: true})
	before := loads
	loaded, err := lazy.FetchDeferred(ctx, model("api-a", "model-a", "mixed"), ai.DeferredHandle{Provider: "mixed", ModelID: "model-a", API: "api-a", ID: "response-1"}, ai.DeferredFetchOptions{})
	if err != nil {
		return err
	}
	reason := loaded.Result().StopReason
	output["lazy"] = map[string]any{"before": before, "loads": loads, "cancel": lazy.CancelDeferred != nil, "reason": reason}
	if err := refresh(ctx, output); err != nil {
		return err
	}
	if err := deferred(ctx, request, output); err != nil {
		return err
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return err
	}
	var canonical any
	if err := json.Unmarshal(raw, &canonical); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(canonical)
}
func refresh(ctx context.Context, output map[string]any) error {
	var calls atomic.Int64
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	first := make(chan ai.ModelsRefreshResult, 1)
	store := ai.NewInMemoryModelsStore()
	models := ai.CreateModels(ai.CreateModelsOptions{ModelsStore: store})
	defer models.Close()
	defer once.Do(func() { close(release) })
	provider := ai.CreateProvider(ai.CreateProviderOptions{ID: "dynamic", Auth: auth(), Models: []ai.AnyModel{}, API: recorder("a", nil), FetchModels: func(ai.RefreshModelsContext) ([]ai.AnyModel, error) {
		current := calls.Add(1)
		if current == 1 {
			close(started)
			<-release
		}
		return []ai.AnyModel{model("api-a", fmt.Sprintf("listed-%d", current), "mixed")}, nil
	}})
	models.SetProvider(provider)
	go func() { first <- models.Refresh(ctx, ai.ModelsRefreshOptions{Providers: []string{"dynamic"}}) }()
	<-started
	second := models.Refresh(ctx, ai.ModelsRefreshOptions{Providers: []string{"dynamic"}})
	old := <-first
	once.Do(func() { close(release) })
	models.Close()
	entry, err := store.Read(ctx, "dynamic")
	if err != nil {
		return err
	}
	if entry == nil || len(entry.Models) != 1 {
		return errors.New("missing stored catalog")
	}
	var stored map[string]any
	if err := json.Unmarshal(entry.Models[0], &stored); err != nil {
		return err
	}
	current, err := provider.GetModels()
	if err != nil {
		return err
	}
	output["refresh"] = map[string]any{"firstAborted": old.Aborted, "secondAborted": second.Aborted, "errors": len(old.Errors) + len(second.Errors), "fetches": calls.Load(), "current": current[0].ID, "stored": stored["id"]}
	return nil
}
func deferred(ctx context.Context, request ai.Context, output map[string]any) error {
	faux := ai.NewFauxProvider(ai.FauxConfig{Model: "test-model", Deferred: &ai.FauxDeferredConfig{PendingFetches: 1, PollAfterMS: new(int64(25))}})
	models := ai.CreateModels(ai.CreateModelsOptions{})
	defer models.Close()
	models.SetProvider(faux.Provider())
	model := faux.GetModel()
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("ready")}, StopReason: "stop"})})
	stream := models.StreamSimple(ctx, model, request, ai.StreamOptions{Deferred: &ai.DeferredOption{Object: true, Window: "1h"}})
	events := []ai.AssistantEventType{}
	for event := range stream.Events(ctx) {
		events = append(events, event.EventType())
	}
	submission := stream.Result()
	if submission.Deferred == nil {
		return errors.New("missing deferred handle")
	}
	handle := *submission.Deferred
	pending := models.FetchDeferred(ctx, model, handle, ai.DeferredFetchOptions{})
	ready := models.FetchDeferred(ctx, model, handle, ai.DeferredFetchOptions{Wait: new(0.0)})
	output["deferred"] = map[string]any{"events": events, "submission": submission.StopReason, "content": submission.Content, "provider": handle.Provider, "modelId": handle.ModelID, "pollAfterMs": handle.PollAfterMS, "validId": handle.ID != "", "pending": pending.StopReason, "sameHandle": reflect.DeepEqual(pending.Deferred, &handle), "ready": ready.StopReason, "readyContent": ready.Content, "positiveUsage": ready.Usage.TotalTokens > 0, "calls": faux.CallCount(), "fetches": faux.DeferredFetchCount()}
	failedFaux := ai.NewFauxProvider(ai.FauxConfig{Model: "test-model"})
	models.SetProvider(failedFaux.Provider())
	model = failedFaux.GetModel()
	failedFaux.SetResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(ai.TranscriptContext, ai.StreamOptions, *ai.FauxProviderState, *ai.Model) (ai.FauxResponse, error) {
		return ai.FauxResponse{}, errors.New("deferred failed")
	}), ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("cancelled")}, StopReason: "stop"})})
	failedSubmission := models.CompleteSimple(ctx, model, request, ai.StreamOptions{Deferred: &ai.DeferredOption{Enabled: true}})
	if failedSubmission.Deferred == nil {
		return errors.New("missing failure handle")
	}
	failed := models.FetchDeferred(ctx, model, *failedSubmission.Deferred, ai.DeferredFetchOptions{})
	cancelledSubmission := models.CompleteSimple(ctx, model, request, ai.StreamOptions{Deferred: &ai.DeferredOption{Enabled: true}})
	if cancelledSubmission.Deferred == nil {
		return errors.New("missing cancel handle")
	}
	if err := models.CancelDeferred(ctx, model, *cancelledSubmission.Deferred, ai.DeferredCancelOptions{}); err != nil {
		return err
	}
	cancelled := models.FetchDeferred(ctx, model, *cancelledSubmission.Deferred, ai.DeferredFetchOptions{})
	output["failure"] = map[string]any{"reason": failed.StopReason, "message": failed.ErrorMessage, "cancelReason": cancelled.StopReason, "cancelMessage": cancelled.ErrorMessage == "Faux deferred response was cancelled: "+cancelledSubmission.Deferred.ID, "recorded": reflect.DeepEqual(failedFaux.CancelledDeferred(), []ai.DeferredHandle{*cancelledSubmission.Deferred})}
	return nil
}
