package ai

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// .upstream/v0.87.1/packages/ai/test/providers.test.ts:686
func TestModelsNewerDynamicRefreshSupersedesBlockedFetchUpstream(t *testing.T) {
	var fetches atomic.Int64
	firstStarted := make(chan struct{})
	finishFirst := make(chan struct{})
	firstDone := make(chan ModelsRefreshResult, 1)
	workerReturned := make(chan struct{})
	var release sync.Once
	provider := CreateProvider(CreateProviderOptions{ID: "dynamic", Auth: configuredTestAuth(), Models: []AnyModel{}, API: recordingProviderStreams("a", nil), FetchModels: func(RefreshModelsContext) ([]AnyModel, error) {
		current := fetches.Add(1)
		if current == 1 {
			close(firstStarted)
			<-finishFirst
			defer close(workerReturned)
		}
		return []AnyModel{dispatchTestModel("api-a", fmt.Sprintf("listed-%d", current))}, nil
	}})
	store := NewInMemoryModelsStore()
	models := CreateModels(CreateModelsOptions{ModelsStore: store})
	models.SetProvider(provider)
	defer models.Close()
	defer release.Do(func() { close(finishFirst) })
	baseline, err := provider.GetModels()
	if err != nil || len(baseline) != 0 {
		t.Fatal("unexpected baseline")
	}
	go func() { firstDone <- models.Refresh(t.Context(), ModelsRefreshOptions{Providers: []string{"dynamic"}}) }()
	<-firstStarted
	second := models.Refresh(t.Context(), ModelsRefreshOptions{Providers: []string{"dynamic"}})
	if second.Aborted || len(second.Errors) != 0 {
		t.Fatalf("second=%#v", second)
	}
	select {
	case first := <-firstDone:
		if first.Aborted || len(first.Errors) != 0 {
			t.Fatalf("first=%#v", first)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("superseded refresh waited for old network work")
	}
	if fetches.Load() != 2 {
		t.Fatalf("fetches=%d", fetches.Load())
	}
	assertLatest := func() {
		t.Helper()
		catalog, err := provider.GetModels()
		if err != nil || len(catalog) != 1 || catalog[0].ID != "listed-2" {
			t.Fatalf("catalog=%#v", catalog)
		}
		entry, err := store.Read(t.Context(), "dynamic")
		if err != nil || entry == nil || len(entry.Models) != 1 {
			t.Fatalf("store=%#v error=%v", entry, err)
		}
		stored, err := decodeModelsCatalog(entry.Models, "mixed")
		if err != nil || len(stored) != 1 || stored[0].ModelID() != "listed-2" {
			t.Fatalf("stored=%#v error=%v", stored, err)
		}
	}
	assertLatest()
	release.Do(func() { close(finishFirst) })
	<-workerReturned
	models.Close()
	assertLatest()
}
