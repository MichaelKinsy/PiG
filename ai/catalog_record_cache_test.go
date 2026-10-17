package ai

import "testing"

// The record-shape cache holds only the catalog compiled into the binary. A models store record (here, a Radius gateway
// model carried through CatalogRecordOf) changes with each refresh, so caching it would grow without bound.
func TestCatalogRecordShapeCacheHoldsOnlyBuiltinRecords(t *testing.T) {
	builtin := GeneratedModels[0].ToModel()
	if _, ok, err := CatalogModelJSON(builtin); !ok || err != nil {
		t.Fatalf("builtin: ok %t, %v", ok, err)
	}
	if _, cached := catalogRecordBaselines.Load(GeneratedModels[0].Record); !cached {
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
	if _, cached := catalogRecordBaselines.Load(record); cached {
		t.Fatal("a models store record's shape was cached")
	}
}
