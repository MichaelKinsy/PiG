package ai

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

// waitForConsumer blocks until a consumer is waiting on the stream, so a later push or end takes the waiting-consumer path.
func waitForConsumer[T, R any](t *testing.T, stream *EventStream[T, R]) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		stream.mu.Lock()
		waiting := len(stream.waiting)
		stream.mu.Unlock()
		if waiting > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no consumer started waiting on the stream")
		}
		time.Sleep(time.Millisecond)
	}
}

// event-stream.ts EventStream: push queues events, the isComplete event settles the result, push after completion is dropped, end(result) settles an explicit result and releases waiting consumers.
// Pi: packages/ai/src/utils/event-stream.ts.
func TestEventStreamQueuesSettlesAndEnds(t *testing.T) {
	// drain reads the stream to its end; a stream that never ends fails the test instead of hanging it.
	drain := func(stream *EventStream[int, string]) []int {
		t.Helper()
		out := make(chan []int, 1)
		go func() {
			var got []int
			for event := range stream.Events() {
				got = append(got, event)
			}
			out <- got
		}()
		select {
		case got := <-out:
			return got
		case <-time.After(2 * time.Second):
			t.Fatal("the stream did not end")
			return nil
		}
	}
	result := func(stream *EventStream[int, string]) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return stream.ResultContext(ctx)
	}

	stream := NewEventStream(func(n int) bool { return n < 0 }, func(n int) string { return "done" })
	stream.Push(1)
	stream.Push(2)
	stream.Push(-1)
	stream.Push(3) // dropped: the stream completed
	if got := drain(stream); !slices.Equal(got, []int{1, 2, -1}) {
		t.Fatalf("events = %v, want [1 2 -1]", got)
	}
	if got, err := result(stream); err != nil || got != "done" {
		t.Fatalf("Result = %q, %v, want the extracted result", got, err)
	}
	if got := stream.Result(); got != "done" {
		t.Fatalf("Result() = %q", got)
	}
	// A completing event hands over to a consumer already waiting for it.
	waiting := NewEventStream(func(n int) bool { return n < 0 }, func(n int) string { return "x" })
	received := make(chan int, 1)
	go func() {
		for event := range waiting.Events() {
			received <- event
			return
		}
		received <- 0
	}()
	waitForConsumer(t, waiting)
	waiting.Push(7)
	select {
	case event := <-received:
		if event != 7 {
			t.Fatalf("waiting consumer received %d, want 7", event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a pushed event never reached the waiting consumer")
	}

	// A consumer that stops early is not called again.
	early := NewEventStream(func(int) bool { return false }, func(int) string { return "" })
	early.Push(1)
	early.Push(2)
	early.Push(3)
	calls := 0
	for range early.Events() {
		calls++
		break
	}
	if calls != 1 {
		t.Fatalf("early-exit consumer saw %d events", calls)
	}

	// end(result) settles the explicit result; a later second end keeps the first.
	ended := NewEventStream(func(int) bool { return false }, func(int) string { return "extracted" })
	ended.Push(1)
	ended.End("explicit")
	ended.End("second")
	ended.Push(2)
	if got := drain(ended); !slices.Equal(got, []int{1}) {
		t.Fatalf("events after end = %v, want only the queued one", got)
	}
	if got, err := result(ended); err != nil || got != "explicit" {
		t.Fatalf("Result = %q, %v, want the first explicit result", got, err)
	}

	// end() without a result releases consumers but leaves the result pending: ResultContext reports the cancellation cause.
	bare := NewEventStream(func(int) bool { return false }, func(int) string { return "never" })
	released := make(chan struct{})
	go func() {
		for range bare.Events() {
		}
		close(released)
	}()
	waitForConsumer(t, bare)
	bare.End()
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("End did not release the waiting consumer")
	}
	bare.Push(5) // dropped: End marked the stream done
	if got := drain(bare); len(got) != 0 {
		t.Fatalf("a push after End was queued: %v", got)
	}
	cause := errors.New("stopped waiting")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	if _, err := bare.ResultContext(ctx); !errors.Is(err, cause) {
		t.Fatalf("ResultContext on an unsettled stream = %v, want the cancellation cause", err)
	}
}

// models-store.ts InMemoryModelsStore: read returns a copy or nothing, write stores a copy, delete removes, and a cancelled context fails every operation.
// Pi: packages/ai/src/models-store.ts.
func TestInMemoryModelsStoreReadWriteDelete(t *testing.T) {
	store := NewInMemoryModelsStore()
	ctx := context.Background()
	if entry, err := store.Read(ctx, "p"); entry != nil || err != nil {
		t.Fatalf("Read of an unknown provider = %v, %v, want nil, nil", entry, err)
	}
	modified := 12.5
	if err := store.Write(ctx, "p", ModelsStoreEntry{ETag: "v1", LastModified: &modified}); err != nil {
		t.Fatal(err)
	}
	entry, err := store.Read(ctx, "p")
	if err != nil || entry == nil || entry.ETag != "v1" || entry.LastModified == nil || *entry.LastModified != 12.5 {
		t.Fatalf("Read = %+v, %v", entry, err)
	}
	entry.ETag = "mutated"
	*entry.LastModified = 99
	again, _ := store.Read(ctx, "p")
	if again.ETag != "v1" || *again.LastModified != 12.5 {
		t.Fatalf("a read copy aliased the stored entry: %+v", again)
	}
	modified = 1
	if again, _ = store.Read(ctx, "p"); *again.LastModified != 12.5 {
		t.Fatalf("Write kept the caller's pointer: %v", *again.LastModified)
	}
	if other, _ := store.Read(ctx, "q"); other != nil {
		t.Fatalf("an entry leaked to another provider: %+v", other)
	}
	if err := store.Delete(ctx, "p"); err != nil {
		t.Fatal(err)
	}
	if gone, _ := store.Read(ctx, "p"); gone != nil {
		t.Fatalf("entry survived Delete: %+v", gone)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.Read(cancelled, "p"); !errors.Is(err, context.Canceled) {
		t.Errorf("Read with a cancelled context = %v", err)
	}
	if err := store.Write(cancelled, "p", ModelsStoreEntry{ETag: "x"}); !errors.Is(err, context.Canceled) {
		t.Errorf("Write with a cancelled context = %v", err)
	}
	if err := store.Delete(cancelled, "q"); !errors.Is(err, context.Canceled) {
		t.Errorf("Delete with a cancelled context = %v", err)
	}
	if stored, _ := store.Read(ctx, "p"); stored != nil {
		t.Errorf("a cancelled Write stored %+v", stored)
	}

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() { _ = store.Write(ctx, "c", ModelsStoreEntry{ETag: string(rune('a' + i))}) })
	}
	wg.Wait()
	if entry, _ := store.Read(ctx, "c"); entry == nil {
		t.Fatal("concurrent writes lost the entry")
	}
}

// image-models.ts getImageProviders lists every provider of the generated catalog once, in stable order.
// Pi: packages/ai/src/image-models.ts getImageProviders.
func TestGetImageProvidersListsTheCatalogProvidersSorted(t *testing.T) {
	seen := map[string]bool{}
	var want []string
	for _, model := range GeneratedImageModels {
		if !seen[model.Provider] {
			seen[model.Provider] = true
			want = append(want, model.Provider)
		}
	}
	slices.Sort(want)
	for range 64 { // the registry is a map: an unsorted list differs between calls
		var got []string
		for _, provider := range GetImageProviders() {
			got = append(got, string(provider))
		}
		if len(want) == 0 || !slices.Equal(got, want) {
			t.Fatalf("GetImageProviders = %v, want %v", got, want)
		}
	}
	if len(GetImageModels(BuiltinImageProvider(want[0]))) == 0 {
		t.Fatalf("provider %s lists no image models", want[0])
	}
}

// providers/typesafe.ts typesafeProvider is the typesafe provider, freshly constructed per call.
// Pi: packages/ai/src/providers/typesafe.ts.
func TestTypesafeProviderIsTheTypesafeProvider(t *testing.T) {
	first, second := TypesafeProvider(), TypesafeProvider()
	if first.ID != "typesafe" || first.Name != ProviderDisplayName("typesafe") || first.BaseURL != builtinProviderBaseURLs()["typesafe"] {
		t.Fatalf("provider = %q %q %q", first.ID, first.Name, first.BaseURL)
	}
	if first == second {
		t.Fatal("TypesafeProvider returned a shared instance")
	}
	if models, err := first.GetModels(); err != nil || len(models) != len(GetBuiltinModels("typesafe")) {
		t.Fatalf("GetModels = %d models, %v, want the built-in chat models of typesafe", len(models), err)
	}
}

// pi-messages.ts PiMessagesResponseError carries message, an optional code and diagnostic details; the code property is absent when none was sent.
// Pi: packages/ai/src/api/pi-messages.ts PiMessagesResponseError.
func TestPiMessagesResponseErrorCarriesCodeAndDetails(t *testing.T) {
	details := map[string]any{"status": 502}
	err := NewPiMessagesResponseError("backend failed", "overloaded", details)
	if err.Error() != "backend failed" || err.Name() != "PiMessagesResponseError" || err.DiagnosticCode() != "overloaded" || err.DiagnosticDetails["status"] != 502 {
		t.Fatalf("error = %+v", err)
	}
	if bare := NewPiMessagesResponseError("m", "", nil); bare.DiagnosticCode() != nil || bare.DiagnosticDetails != nil {
		t.Fatalf("code-less error = %+v", bare)
	}
}

// cloudflare-stream.ts cloudflareClassifier substitutes the account and gateway placeholders of the model's base URL from the request env before dispatch, and passes the model through untouched when nothing changes.
// Pi: packages/ai/src/providers/cloudflare-stream.ts:31-36.
func TestCloudflareClassifierResolvesPlaceholdersBeforeDispatch(t *testing.T) {
	var seen []*ClassifierModel
	var seenOptions []ClassifierOptions
	inner := &ProviderClassifier{Classify: func(_ context.Context, model *ClassifierModel, _ ClassifierContext, options ClassifierOptions) (ClassifierResult, error) {
		seen = append(seen, model)
		seenOptions = append(seenOptions, options)
		return ClassifierResult{}, errors.New("inner called")
	}}
	classifier := CloudflareClassifier(inner)
	model := &ClassifierModel{BaseURL: CloudflareAIGatewayCompatBaseURL}
	_, err := classifier.Classify(context.Background(), model, ClassifierContext{}, ClassifierOptions{Env: ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "acct", "CLOUDFLARE_GATEWAY_ID": "gw"}})
	if err == nil || err.Error() != "inner called" {
		t.Fatalf("error = %v, want the inner classifier's", err)
	}
	if want := "https://gateway.ai.cloudflare.com/v1/acct/gw/compat"; seen[0].BaseURL != want {
		t.Fatalf("resolved base URL = %q, want %q", seen[0].BaseURL, want)
	}
	if seenOptions[0].Env["CLOUDFLARE_GATEWAY_ID"] != "gw" {
		t.Fatalf("the inner classifier received options %+v, want the caller's", seenOptions[0])
	}
	if model.BaseURL != CloudflareAIGatewayCompatBaseURL {
		t.Fatalf("the caller's model was modified: %q", model.BaseURL)
	}
	plain := &ClassifierModel{BaseURL: "https://example.invalid/v1"}
	_, _ = classifier.Classify(context.Background(), plain, ClassifierContext{}, ClassifierOptions{Env: ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "acct"}})
	if seen[1] != plain {
		t.Fatal("a model without placeholders was copied instead of passed through")
	}
}

// getImageProviders lists providers in a stable order whatever the registry's iteration order is.
func TestGetImageProvidersSortsAMultiProviderRegistry(t *testing.T) {
	saved := imageModelRegistry
	t.Cleanup(func() { imageModelRegistry = saved })
	registry := map[string]map[string]ImageModel{}
	for _, provider := range []string{"zeta", "alpha", "mid", "beta", "omega", "gamma"} {
		registry[provider] = map[string]ImageModel{"m": {ID: "m", Provider: provider}}
	}
	imageModelRegistry = func() map[string]map[string]ImageModel { return registry }
	for range 32 {
		var got []string
		for _, provider := range GetImageProviders() {
			got = append(got, string(provider))
		}
		if want := []string{"alpha", "beta", "gamma", "mid", "omega", "zeta"}; !slices.Equal(got, want) {
			t.Fatalf("GetImageProviders = %v, want %v", got, want)
		}
	}
}
