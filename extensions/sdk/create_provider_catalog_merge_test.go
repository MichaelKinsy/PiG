package sdk

import (
	"reflect"
	"testing"
)

func TestCreateProviderCatalogMergesOncePerPublish(t *testing.T) {
	noop := func(map[string]any, map[string]any, ProviderStreamOptions) (*ModelEventStream, error) {
		return nil, nil
	}
	baseline := []map[string]any{
		{"id": "a", "name": "a", "api": "api-a", "provider": "dyn"},
		{"id": "b", "name": "b", "api": "api-a", "provider": "dyn"},
		{"id": "img-1", "type": "image", "name": "img-1", "api": "img-a", "provider": "dyn"},
	}
	fetchedB := map[string]any{"id": "b", "name": "fetched b", "api": "api-a", "provider": "dyn"}
	fetchedC := map[string]any{"id": "c", "name": "c", "api": "api-a", "provider": "dyn"}
	provider, err := CreateProvider(CreateProviderOptions{
		ID:     "dyn",
		Models: baseline,
		FetchModels: func(RefreshModelsContext) ([]map[string]any, error) {
			return []map[string]any{fetchedC, fetchedB}, nil
		},
		API: &ProviderStreams{Stream: noop, StreamSimple: noop},
	})
	if err != nil {
		t.Fatal(err)
	}

	before1, err := provider.GetModels()
	if err != nil {
		t.Fatal(err)
	}
	before2, err := provider.GetModels()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before1, before2) {
		t.Fatalf("before1=%#v != before2=%#v", before1, before2)
	}
	if len(before1) != 2 || &before1[0] == &before2[0] {
		t.Fatalf("expected public GetModels to return an owned copy slice")
	}
	// Caller mutation of returned slice must not affect subsequent reads.
	before1[0] = map[string]any{"id": "mutated"}

	published := false
	err = provider.RefreshModels(RefreshModelsContext{
		AllowNetwork: true,
		Publish: func(pub ModelsPublication) (bool, error) {
			if pub.Update != nil {
				if err := pub.Update(); err != nil {
					return false, err
				}
			}
			published = true
			return true, nil
		},
	})
	if err != nil || !published {
		t.Fatalf("refresh err=%v published=%v", err, published)
	}

	after, err := provider.GetModels()
	if err != nil {
		t.Fatal(err)
	}
	wantChat := []map[string]any{baseline[0], fetchedB, fetchedC}
	if !reflect.DeepEqual(after, wantChat) {
		t.Fatalf("after chat=%#v, want %#v", after, wantChat)
	}
	all, err := provider.GetAllModels()
	if err != nil {
		t.Fatal(err)
	}
	wantAll := []map[string]any{baseline[0], fetchedB, baseline[2], fetchedC}
	if !reflect.DeepEqual(all, wantAll) {
		t.Fatalf("after all=%#v, want %#v", all, wantAll)
	}

	// Each public read allocates only the shallow copy slice (1 alloc, no merge).
	if allocs := testing.AllocsPerRun(50, func() {
		list, err := provider.GetModels()
		if err != nil || len(list) != 3 {
			t.Fatalf("len=%d err=%v", len(list), err)
		}
	}); allocs != 1 {
		t.Fatalf("GetModels allocs = %.1f, want 1", allocs)
	}
	if allocs := testing.AllocsPerRun(50, func() {
		list, err := provider.GetAllModels()
		if err != nil || len(list) != 4 {
			t.Fatalf("len=%d err=%v", len(list), err)
		}
	}); allocs != 1 {
		t.Fatalf("GetAllModels allocs = %.1f, want 1", allocs)
	}
}
