package ai

import (
	"context"
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"sync"
	"testing"
)

// legacyMergeProviderCatalog is the pre-#12366 currentModels algorithm kept only as a test reference.
func legacyMergeProviderCatalog(baseline, dynamic []AnyModel) []AnyModel {
	merged := append([]AnyModel{}, baseline...)
	for _, model := range dynamic {
		index := slices.IndexFunc(merged, func(candidate AnyModel) bool {
			return GetModelType(candidate) == GetModelType(model) && candidate.ModelID() == model.ModelID()
		})
		if index < 0 {
			merged = append(merged, model)
		} else {
			merged[index] = model
		}
	}
	return merged
}

func catalogMergeTestModel(provider, id, name string) *Model {
	return &Model{
		ID:          id,
		DisplayName: name,
		ProviderMeta: ProviderMetadata{
			API:        APIOpenAICompletions,
			ProviderID: provider,
			BaseURL:    "https://example.test/v1",
		},
		Input:        []string{"text"},
		Capabilities: ModelCapabilities{ContextWindow: 10000, MaxOutputTokens: 1000},
	}
}

func catalogMergeTestImage(provider, id, name string) *ImageModel {
	return &ImageModel{
		ID:       id,
		Name:     name,
		API:      APIImagesOpenRouter,
		Provider: provider,
		BaseURL:  "https://example.test/v1",
	}
}

func catalogMergeTestClassifier(provider, id, name string) *ClassifierModel {
	return &ClassifierModel{
		ID:       id,
		Name:     name,
		API:      ClassifierAPITypesafeSystemOne,
		Provider: provider,
		BaseURL:  "https://example.test/v1",
	}
}

func catalogMergeTestStreams() *ProviderStreams {
	noop := func(context.Context, *Model, TranscriptContext, StreamOptions) (*AssistantMessageEventStream, error) {
		return NewAssistantMessageEventStream(), nil
	}
	return &ProviderStreams{Stream: noop, StreamSimple: noop}
}

func sameSliceBacking[T any](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	return &a[0] == &b[0]
}

// upstream: packages/ai/test/models-runtime.test.ts:424 (#12311, Pi 073b0bae8)
// Pi asserts `expect(provider.getModels()).toBe(provider.getModels())` because JS is single-threaded.
// In Go, the public getters return an owned shallow copy (equal content, distinct slice per call, 1 alloc),
// while the package-private snapshot read used by Models.GetModel returns the same immutable backing array
// across reads until the next publish (0 merges and 0 allocations on read).
func TestCreateProviderMergesCatalogOncePerPublishUpstream(t *testing.T) {
	baseline := []AnyModel{
		catalogMergeTestModel("dynamic", "a", "a"),
		catalogMergeTestModel("dynamic", "b", "b"),
	}
	fetchedB := catalogMergeTestModel("dynamic", "b", "fetched b")
	fetchedC := catalogMergeTestModel("dynamic", "c", "c")
	provider := CreateProvider(CreateProviderOptions{
		ID:          "dynamic",
		Auth:        configuredTestAuth(),
		Models:      baseline,
		FetchModels: func(RefreshModelsContext) ([]AnyModel, error) { return []AnyModel{fetchedC, fetchedB}, nil },
		API:         catalogMergeTestStreams(),
	})
	models := CreateModels()
	defer models.Close()
	models.SetProvider(provider)

	if got := provider.catalogMergeCount(); got != 0 {
		t.Fatalf("mergeCount before publish = %d, want 0", got)
	}

	// Package-private cached snapshot is identical across reads before publish.
	privBefore1, err := provider.cachedChatModels()
	if err != nil {
		t.Fatal(err)
	}
	privBefore2, err := provider.cachedChatModels()
	if err != nil {
		t.Fatal(err)
	}
	if !sameSliceBacking(privBefore1, privBefore2) {
		t.Fatalf("expected repeated private chat reads before refresh to share the cached snapshot")
	}
	privAllBefore1, err := provider.cachedAllModels()
	if err != nil {
		t.Fatal(err)
	}
	privAllBefore2, err := provider.cachedAllModels()
	if err != nil {
		t.Fatal(err)
	}
	if !sameSliceBacking(privAllBefore1, privAllBefore2) {
		t.Fatalf("expected repeated private all-model reads before refresh to share the cached snapshot")
	}

	// Public getters return equal content with distinct slices so caller mutations cannot race or corrupt the cache.
	pubBefore1, err := provider.GetModels()
	if err != nil {
		t.Fatal(err)
	}
	pubBefore2, err := provider.GetModels()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pubBefore1, pubBefore2) {
		t.Fatalf("public GetModels content mismatch: %#v != %#v", pubBefore1, pubBefore2)
	}
	if sameSliceBacking(pubBefore1, pubBefore2) {
		t.Fatalf("expected public GetModels to return a distinct copy slice per call")
	}
	// Mutating the returned copy must not affect subsequent reads.
	pubBefore1[0] = catalogMergeTestModel("dynamic", "mutated", "mutated")
	if got := models.GetModel("dynamic", "a"); got == nil || got.ID != "a" {
		t.Fatalf("caller mutation of GetModels slice corrupted provider cache: got %#v", got)
	}

	if got := provider.catalogMergeCount(); got != 0 {
		t.Fatalf("mergeCount after reads before publish = %d, want 0", got)
	}

	if res := models.Refresh(t.Context()); len(res.Errors) != 0 {
		t.Fatalf("refresh errors: %v", res.Errors)
	}
	if got := provider.catalogMergeCount(); got != 1 {
		t.Fatalf("mergeCount after one publish = %d, want 1", got)
	}

	after1, err := provider.GetModels()
	if err != nil {
		t.Fatal(err)
	}
	after2, err := provider.GetModels()
	if err != nil {
		t.Fatal(err)
	}
	want := []*Model{baseline[0].(*Model), fetchedB, fetchedC}
	if !reflect.DeepEqual(after1, want) || !reflect.DeepEqual(after2, want) {
		t.Fatalf("after refresh got %#v / %#v, want %#v", after1, after2, want)
	}
	if sameSliceBacking(after1, after2) {
		t.Fatalf("expected public GetModels after refresh to return distinct copy slices")
	}

	allAfter1, err := provider.GetAllModels()
	if err != nil {
		t.Fatal(err)
	}
	allAfter2, err := provider.GetAllModels()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(allAfter1, allAfter2) || sameSliceBacking(allAfter1, allAfter2) {
		t.Fatalf("expected public GetAllModels after refresh to return equal distinct copy slices")
	}
	allAfter1[0] = catalogMergeTestModel("dynamic", "mutated-all", "mutated-all")

	// Package-private reads change snapshot on publish and stay identical across N reads with 0 further merges.
	privAfter1, err := provider.cachedChatModels()
	if err != nil {
		t.Fatal(err)
	}
	if sameSliceBacking(privBefore1, privAfter1) {
		t.Fatalf("expected private chat snapshot to change after publish")
	}
	privAllAfter1, err := provider.cachedAllModels()
	if err != nil {
		t.Fatal(err)
	}
	if sameSliceBacking(privAllBefore1, privAllAfter1) {
		t.Fatalf("expected private all-model snapshot to change after publish")
	}
	if privAllAfter1[0].ModelID() != "a" {
		t.Fatalf("caller mutation of GetAllModels slice corrupted private cache: got %q", privAllAfter1[0].ModelID())
	}
	for range 50 {
		chat, err := provider.cachedChatModels()
		if err != nil || !sameSliceBacking(privAfter1, chat) {
			t.Fatalf("private chat snapshot changed between publishes")
		}
		all, err := provider.cachedAllModels()
		if err != nil || !sameSliceBacking(privAllAfter1, all) {
			t.Fatalf("private all snapshot changed between publishes")
		}
		if _, err := provider.GetModels(); err != nil {
			t.Fatal(err)
		}
		if _, err := provider.GetAllModels(); err != nil {
			t.Fatal(err)
		}
		if got := models.GetModel("dynamic", "c"); got == nil {
			t.Fatal("missing model c")
		}
	}
	if got := provider.catalogMergeCount(); got != 1 {
		t.Fatalf("mergeCount after 50 reads between publishes = %d, want 1", got)
	}
}

func TestCreateProviderRestoredCatalogMergesOncePerPublish(t *testing.T) {
	store := NewInMemoryModelsStore()
	storedB := catalogMergeTestModel("dynamic", "b", "stored b")
	storedC := catalogMergeTestModel("dynamic", "c", "stored c")
	if err := store.Write(t.Context(), "dynamic", ModelsStoreEntry{Models: []AnyModel{storedC, storedB}}); err != nil {
		t.Fatal(err)
	}
	baseline := []AnyModel{
		catalogMergeTestModel("dynamic", "a", "a"),
		catalogMergeTestModel("dynamic", "b", "b"),
	}
	provider := CreateProvider(CreateProviderOptions{
		ID:     "dynamic",
		Auth:   configuredTestAuth(),
		Models: baseline,
		FetchModels: func(RefreshModelsContext) ([]AnyModel, error) {
			t.Fatal("network fetch should not run when AllowNetwork=false")
			return nil, nil
		},
		API: catalogMergeTestStreams(),
	})
	models := CreateModels(CreateModelsOptions{ModelsStore: store})
	defer models.Close()
	models.SetProvider(provider)

	privBefore, err := provider.cachedChatModels()
	if err != nil {
		t.Fatal(err)
	}
	if res := models.Refresh(t.Context(), ModelsRefreshOptions{AllowNetwork: new(false)}); len(res.Errors) != 0 {
		t.Fatalf("refresh errors: %v", res.Errors)
	}
	if got := provider.catalogMergeCount(); got != 1 {
		t.Fatalf("mergeCount after stored restore publish = %d, want 1", got)
	}
	after1, err := provider.GetModels()
	if err != nil {
		t.Fatal(err)
	}
	if len(after1) != 3 || after1[0].ID != "a" || after1[1].DisplayName != "stored b" || after1[2].ID != "c" {
		t.Fatalf("unexpected restored catalog: %#v", after1)
	}
	privAfter1, err := provider.cachedChatModels()
	if err != nil {
		t.Fatal(err)
	}
	privAfter2, err := provider.cachedChatModels()
	if err != nil {
		t.Fatal(err)
	}
	if sameSliceBacking(privBefore, privAfter1) {
		t.Fatalf("expected private snapshot to change after stored restore publish")
	}
	if !sameSliceBacking(privAfter1, privAfter2) {
		t.Fatalf("expected repeated private reads after stored restore to share the cached snapshot")
	}
	if got := provider.catalogMergeCount(); got != 1 {
		t.Fatalf("mergeCount after reads = %d, want 1", got)
	}
}

func TestCreateProviderCatalogReadAllocs(t *testing.T) {
	baseline := make([]AnyModel, 20)
	for i := range baseline {
		baseline[i] = catalogMergeTestModel("custom", fmt.Sprintf("base-%02d", i), fmt.Sprintf("Base %d", i))
	}
	fetched := make([]AnyModel, 20)
	for i := range 5 {
		fetched[i] = catalogMergeTestModel("custom", fmt.Sprintf("base-%02d", i), fmt.Sprintf("Override %d", i))
	}
	for i := 5; i < 20; i++ {
		fetched[i] = catalogMergeTestModel("custom", fmt.Sprintf("dyn-%02d", i), fmt.Sprintf("Dynamic %d", i))
	}
	provider := CreateProvider(CreateProviderOptions{
		ID:          "custom",
		Auth:        configuredTestAuth(),
		Models:      baseline,
		FetchModels: func(RefreshModelsContext) ([]AnyModel, error) { return fetched, nil },
		API:         catalogMergeTestStreams(),
	})
	models := CreateModels()
	defer models.Close()
	models.SetProvider(provider)
	if res := models.Refresh(t.Context()); len(res.Errors) != 0 {
		t.Fatalf("refresh errors: %v", res.Errors)
	}

	// Warm once.
	if _, err := provider.GetModels(); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.GetAllModels(); err != nil {
		t.Fatal(err)
	}
	if got := models.GetModel("custom", "dyn-14"); got == nil {
		t.Fatal("missing dyn-14")
	}

	// Public getters return a shallow copy (1 slice allocation, no merge).
	if allocs := testing.AllocsPerRun(100, func() {
		list, err := provider.GetModels()
		if err != nil || len(list) != 35 {
			t.Fatalf("unexpected GetModels: len=%d err=%v", len(list), err)
		}
	}); allocs != 1 {
		t.Fatalf("provider.GetModels() allocated %.1f allocs/run, want 1", allocs)
	}

	if allocs := testing.AllocsPerRun(100, func() {
		list, err := provider.GetAllModels()
		if err != nil || len(list) != 35 {
			t.Fatalf("unexpected GetAllModels: len=%d err=%v", len(list), err)
		}
	}); allocs != 1 {
		t.Fatalf("provider.GetAllModels() allocated %.1f allocs/run, want 1", allocs)
	}

	// Internal lookup reads the immutable published snapshot directly without copying.
	if allocs := testing.AllocsPerRun(100, func() {
		if got := models.GetModel("custom", "dyn-14"); got == nil {
			t.Fatal("missing dyn-14")
		}
	}); allocs != 0 {
		t.Fatalf("models.GetModel() allocated %.1f allocs/run, want 0", allocs)
	}

	if allocs := testing.AllocsPerRun(100, func() {
		if got := models.GetModelOfType(ModelTypeChat, "custom", "dyn-14"); got == nil {
			t.Fatal("missing dyn-14")
		}
	}); allocs != 0 {
		t.Fatalf("models.GetModelOfType() allocated %.1f allocs/run, want 0", allocs)
	}
}

func TestCreateProviderCatalogMergeMatchesLegacy(t *testing.T) {
	rng := rand.New(rand.NewPCG(12366, 12311))
	makeModel := func(kind int, id, tag string) AnyModel {
		switch kind {
		case 0:
			return catalogMergeTestModel("prop", id, tag)
		case 1:
			return catalogMergeTestImage("prop", id, tag)
		default:
			return catalogMergeTestClassifier("prop", id, tag)
		}
	}
	idPool := []string{"m0", "m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8", "m9", "m\x00a", "m\x00b"}

	for trial := range 200 {
		baseLen := rng.IntN(16)
		dynLen := rng.IntN(20)
		baseline := make([]AnyModel, baseLen)
		for i := range baseLen {
			baseline[i] = makeModel(rng.IntN(3), idPool[rng.IntN(len(idPool))], fmt.Sprintf("b-%d-%d", trial, i))
		}
		dynamic := make([]AnyModel, dynLen)
		for i := range dynLen {
			dynamic[i] = makeModel(rng.IntN(3), idPool[rng.IntN(len(idPool))], fmt.Sprintf("d-%d-%d", trial, i))
		}

		wantAll := legacyMergeProviderCatalog(baseline, dynamic)
		wantChat := chatModels(wantAll)

		provider := CreateProvider(CreateProviderOptions{
			ID:          "prop",
			Auth:        configuredTestAuth(),
			Models:      baseline,
			FetchModels: func(RefreshModelsContext) ([]AnyModel, error) { return dynamic, nil },
			API:         catalogMergeTestStreams(),
		})
		models := CreateModels()
		models.SetProvider(provider)

		// Before refresh: baseline only.
		wantBeforeAll := legacyMergeProviderCatalog(baseline, nil)
		wantBeforeChat := chatModels(wantBeforeAll)
		gotBeforeAll, err := provider.GetAllModels()
		if err != nil {
			t.Fatal(err)
		}
		gotBeforeChat, err := provider.GetModels()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotBeforeAll, wantBeforeAll) {
			t.Fatalf("trial %d before refresh all mismatch:\ngot  %#v\nwant %#v", trial, gotBeforeAll, wantBeforeAll)
		}
		if !reflect.DeepEqual(gotBeforeChat, wantBeforeChat) {
			t.Fatalf("trial %d before refresh chat mismatch:\ngot  %#v\nwant %#v", trial, gotBeforeChat, wantBeforeChat)
		}

		if res := models.Refresh(t.Context()); len(res.Errors) != 0 {
			t.Fatalf("trial %d refresh errors: %v", trial, res.Errors)
		}
		gotAll, err := provider.GetAllModels()
		if err != nil {
			t.Fatal(err)
		}
		gotChat, err := provider.GetModels()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotAll, wantAll) {
			t.Fatalf("trial %d after refresh all mismatch:\ngot  %#v\nwant %#v", trial, gotAll, wantAll)
		}
		if !reflect.DeepEqual(gotChat, wantChat) {
			t.Fatalf("trial %d after refresh chat mismatch:\ngot  %#v\nwant %#v", trial, gotChat, wantChat)
		}
		models.Close()
	}
}

func TestCreateProviderCatalogConcurrentReadsDuringRefresh(t *testing.T) {
	baseline := []AnyModel{
		catalogMergeTestModel("conc", "a", "base-a"),
		catalogMergeTestImage("conc", "img-a", "base-img-a"),
	}
	var fetchSeq int
	var fetchMu sync.Mutex
	provider := CreateProvider(CreateProviderOptions{
		ID:     "conc",
		Auth:   configuredTestAuth(),
		Models: baseline,
		FetchModels: func(RefreshModelsContext) ([]AnyModel, error) {
			fetchMu.Lock()
			fetchSeq++
			n := fetchSeq
			fetchMu.Unlock()
			return []AnyModel{
				catalogMergeTestModel("conc", "a", fmt.Sprintf("dyn-a-%d", n)),
				catalogMergeTestModel("conc", fmt.Sprintf("dyn-%d", n), fmt.Sprintf("dyn-%d", n)),
			}, nil
		},
		API: catalogMergeTestStreams(),
	})
	models := CreateModels()
	defer models.Close()
	models.SetProvider(provider)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				chat, err := provider.GetModels()
				if err != nil || len(chat) == 0 {
					t.Errorf("GetModels failed: len=%d err=%v", len(chat), err)
					return
				}
				// Public copies belong to the caller: sorting/mutating them in place must not race with other readers or refreshes.
				slices.Reverse(chat)
				all, err := provider.GetAllModels()
				if err != nil || len(all) < 2 {
					t.Errorf("GetAllModels failed: len=%d err=%v", len(all), err)
					return
				}
				slices.Reverse(all)
				privChat, err := provider.cachedChatModels()
				if err != nil || len(privChat) == 0 {
					t.Errorf("cachedChatModels failed: len=%d err=%v", len(privChat), err)
					return
				}
				privAll, err := provider.cachedAllModels()
				if err != nil || len(privAll) < 2 {
					t.Errorf("cachedAllModels failed: len=%d err=%v", len(privAll), err)
					return
				}
				if got := models.GetModel("conc", "a"); got == nil {
					t.Errorf("GetModel returned nil")
					return
				}
				if got := models.GetModelOfType(ModelTypeImage, "conc", "img-a"); got == nil {
					t.Errorf("GetModelOfType returned nil")
					return
				}
			}
		}()
	}
	for range 25 {
		if res := models.Refresh(t.Context()); len(res.Errors) != 0 {
			t.Fatalf("refresh errors: %v", res.Errors)
		}
	}
	cancel()
	wg.Wait()
}

func TestCreateProviderReplacedGetterBypassesCache(t *testing.T) {
	provider := CreateProvider(CreateProviderOptions{
		ID:     "wrapped",
		Auth:   configuredTestAuth(),
		Models: []AnyModel{catalogMergeTestModel("wrapped", "a", "a")},
		API:    catalogMergeTestStreams(),
	})
	replacement := catalogMergeTestModel("wrapped", "replaced", "replaced")
	provider.GetModels = func() ([]*Model, error) { return []*Model{replacement}, nil }
	provider.GetAllModels = func() ([]AnyModel, error) { return []AnyModel{replacement}, nil }
	models := CreateModels()
	defer models.Close()
	models.SetProvider(provider)
	if got := models.GetModel("wrapped", "replaced"); got != replacement {
		t.Fatalf("GetModel after GetModels override = %#v, want %#v", got, replacement)
	}
	if got := models.GetModel("wrapped", "a"); got != nil {
		t.Fatalf("GetModel returned stale cached model after GetModels override: %#v", got)
	}
	if got := models.GetModelOfType(ModelTypeChat, "wrapped", "replaced"); got != replacement {
		t.Fatalf("GetModelOfType after GetAllModels override = %#v, want %#v", got, replacement)
	}
}
