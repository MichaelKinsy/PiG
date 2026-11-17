package ai

import (
	"bytes"
	"testing"
)

// A built-in model that is exactly what its catalog entry makes is encoded as its record without encoding it or decoding the record: DeepEqual with the
// entry's typed model decides it. Every listed entry, the bundled models and the D199 variants, takes that path, and the record it emits is the one the
// encoding path emits: unchangedBundledRecord reports a model unchanged only when it equals the entry's typed model, so what the encoding path would emit
// for the model is what it emits for the entry, which this test checks for all of them.
func TestEveryListedCatalogEntryIsRecognizedAndEncodesAsItsRecord(t *testing.T) {
	entries := listedCatalogEntries()
	if len(entries) <= len(GeneratedModels) {
		t.Fatalf("the listed catalog holds %d entries and the bundled one %d: no D199 variants", len(entries), len(GeneratedModels))
	}
	clearCatalogRecordBaselines()
	for i := range entries {
		model := entries[i].ToModel()
		record, unchanged := unchangedBundledRecord(model)
		if !unchanged || record != entries[i].Record {
			t.Fatalf("entry %s/%s: unchanged %t, record is the entry's %t", entries[i].Provider, entries[i].ID, unchanged, record == entries[i].Record)
		}
		fast, ok, err := CatalogModelJSON(model)
		if err != nil || !ok {
			t.Fatalf("entry %s/%s: ok %t, %v", entries[i].Provider, entries[i].ID, ok, err)
		}
		encoded, err := marshalCatalogModelByEncoding(model)
		if err != nil {
			t.Fatalf("entry %s/%s by encoding: %v", entries[i].Provider, entries[i].ID, err)
		}
		if !bytes.Equal(fast, encoded) || string(fast) != ResolveCatalogRecord(entries[i].Record) {
			t.Fatalf("entry %s/%s: the entry path emits\n%s\nthe encoding path\n%s\nthe record is\n%s", entries[i].Provider, entries[i].ID, fast, encoded, ResolveCatalogRecord(entries[i].Record))
		}
	}
}

// catalogFamilyEntries is one entry of each API kind the bundled catalog holds that satisfies has, in the order the kinds first appear in the catalog: the
// provider families are read from the data, not listed.
func catalogFamilyEntries(has func(*GeneratedModel) bool) []*GeneratedModel {
	seen := map[API]bool{}
	var picked []*GeneratedModel
	for i := range GeneratedModels {
		entry := &GeneratedModels[i]
		if seen[entry.API] || !has(entry) {
			continue
		}
		seen[entry.API] = true
		picked = append(picked, entry)
	}
	return picked
}

// reflect.DeepEqual tells a nil slice or map from an empty one, which the record does not (omitempty): such a model is reported changed, takes the
// encoding path, and emits the same bytes. A model that differs in a member the record keeps takes that path and emits different ones. Each case runs
// against one entry of every API family that has the member in the state the case changes: the nil-to-empty cases need an entry whose member is nil
// and fail when the catalog holds none, so a case cannot pass by changing nothing.
func TestCatalogModelDeepEqualErrsTowardTheEncodingPath(t *testing.T) {
	everyEntry := func(*GeneratedModel) bool { return true }
	for _, test := range []struct {
		name         string
		precondition func(*GeneratedModel) bool
		change       func(*Model)
		same         bool
	}{
		{"empty thinking level map for none", func(e *GeneratedModel) bool { return e.ThinkingLevelMap == nil }, func(m *Model) { m.ThinkingLevelMap = ThinkingLevelMap{} }, true},
		{"empty sampling params for none", func(e *GeneratedModel) bool { return e.SamplingParams == nil }, func(m *Model) { m.SamplingParams = map[string]any{} }, true},
		{"empty prompt cache for none", func(e *GeneratedModel) bool { return e.PromptCache == nil }, func(m *Model) { m.PromptCache = ModelPromptCache{} }, true},
		{"empty headers for none", func(e *GeneratedModel) bool { return e.Headers == nil }, func(m *Model) { m.ProviderMeta.Headers = map[string]string{} }, true},
		{"a changed context window", everyEntry, func(m *Model) { m.Capabilities.ContextWindow++ }, false},
		{"a changed name", everyEntry, func(m *Model) { m.DisplayName += "!" }, false},
		{"a changed reasoning flag", everyEntry, func(m *Model) { m.ProviderMeta.Reasoning = !m.ProviderMeta.Reasoning }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			entries := catalogFamilyEntries(test.precondition)
			if len(entries) == 0 {
				t.Fatalf("no catalog entry satisfies the case's precondition: the case would change nothing")
			}
			for _, entry := range entries {
				model := entry.ToModel()
				test.change(model)
				if _, unchanged := unchangedBundledRecord(model); unchanged {
					t.Fatalf("%s %s/%s: the changed model was recognized as the entry's", entry.API, entry.Provider, entry.ID)
				}
				got, ok, err := CatalogModelJSON(model)
				if err != nil || !ok {
					t.Fatalf("%s %s/%s: ok %t, %v", entry.API, entry.Provider, entry.ID, ok, err)
				}
				encoded, err := marshalCatalogModelByEncoding(model)
				if err != nil || !bytes.Equal(got, encoded) {
					t.Fatalf("%s %s/%s: the model encodes as\n%s\nand by the encoding path as\n%s (%v)", entry.API, entry.Provider, entry.ID, got, encoded, err)
				}
				if same := bytes.Equal(got, []byte(ResolveCatalogRecord(entry.Record))); same != test.same {
					t.Fatalf("%s %s/%s: emits the record: %t, want %t", entry.API, entry.Provider, entry.ID, same, test.same)
				}
			}
		})
	}
}

// A model with a record that no entry holds (an extension provider's registered model, a models store record) is never recognized.
func TestModelOfNoEntryTakesTheEncodingPath(t *testing.T) {
	record := ExtensionModelRecord([]byte(`{"id":"ext-1","name":"Ext One","reasoning":false,"input":["text"],"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0},"contextWindow":1000,"maxTokens":100}`), API("openai-completions"), "ext", "https://ext.example/v1")
	if record == "" {
		t.Fatal("no record")
	}
	model := (&GeneratedModel{Record: record}).ToModel()
	model.ID, model.DisplayName = "ext-1", "Ext One"
	if _, unchanged := unchangedBundledRecord(model); unchanged {
		t.Fatal("a model of no entry was recognized")
	}
	got, ok, err := CatalogModelJSON(model)
	if err != nil || !ok {
		t.Fatalf("ok %t, %v", ok, err)
	}
	encoded, err := marshalCatalogModelByEncoding(model)
	if err != nil || !bytes.Equal(got, encoded) {
		t.Fatalf("got\n%s\nby encoding\n%s (%v)", got, encoded, err)
	}
}
