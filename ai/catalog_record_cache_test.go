package ai

import "testing"

// clearCatalogRecordBaselines forgets every cached baseline, so the next encoding of a built-in model computes its baseline again.
func clearCatalogRecordBaselines() {
	for i := range catalogRecordBaselines {
		catalogRecordBaselines[i].ready.Store(false)
	}
}

// cachedCatalogRecordBaselines is the number of built-in records whose baseline is cached.
func cachedCatalogRecordBaselines() int {
	cached := 0
	for i := range catalogRecordBaselines {
		if catalogRecordBaselines[i].ready.Load() {
			cached++
		}
	}
	return cached
}

// The record-shape cache holds only the catalog compiled into the binary. A models store record (here, a Radius gateway
// model carried through CatalogRecordOf) changes with each refresh, so caching it would grow without bound.
func TestCatalogRecordShapeCacheHoldsOnlyBuiltinRecords(t *testing.T) {
	clearCatalogRecordBaselines()
	builtin := GeneratedModels[0].ToModel()
	if _, ok, err := CatalogModelJSON(builtin); !ok || err != nil {
		t.Fatalf("builtin: ok %t, %v", ok, err)
	}
	if !catalogRecordBaselines[0].ready.Load() || cachedCatalogRecordBaselines() != 1 {
		t.Fatal("the built-in record's shape was not cached")
	}

	stored, known, err := DecodeStoredModel([]byte(`{"id":"gw-1","name":"Gateway One","api":"pi-messages","provider":"radius","baseUrl":"https://gw.example/v1","reasoning":false,"input":["text"],"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0},"contextWindow":1000,"maxTokens":100,"extra":"kept"}`))
	if err != nil || !known {
		t.Fatalf("decode: known %t, %v", known, err)
	}
	record := CatalogRecordOf(stored.(*Model))
	if record == "" {
		t.Fatal("no record for a decoded store model")
	}
	rebuilt := (&GeneratedModel{Record: record}).ToModel()
	rebuilt.ID, rebuilt.DisplayName, rebuilt.ProviderMeta = "gw-1", "Gateway One", stored.(*Model).ProviderMeta
	if _, ok, err := CatalogModelJSON(rebuilt); !ok || err != nil {
		t.Fatalf("store record: ok %t, %v", ok, err)
	}
	if cachedCatalogRecordBaselines() != 1 {
		t.Fatal("a models store record's shape was cached")
	}
}
