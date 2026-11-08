package ai

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonseed"
)

// The ordered-object decoders read with encoding/json v1 before they read with jsontext. Each target feeds the same bytes to the
// old reader (jsonv1_ref_test.go, as it was) and the new one and requires the same entries, and an error from one only when the other
// errs. A reader that never checked the closing token (the "lenient" ones) accepted a document cut off after a value; the new one
// rejects it, so those targets compare only complete JSON.
func fuzzSeeds(f *testing.F) {
	for _, doc := range jsonseed.Documents() {
		f.Add(doc)
	}
}

func sameResult(t *testing.T, data []byte, name string, got, want any, gotErr, wantErr error) {
	t.Helper()
	if (gotErr == nil) != (wantErr == nil) {
		t.Fatalf("%s(%q): error %v, v1 error %v", name, data, gotErr, wantErr)
	}
	if gotErr == nil && !reflect.DeepEqual(got, want) {
		t.Fatalf("%s(%q): %#v, v1 %#v", name, data, got, want)
	}
}

func FuzzOrderedJSONObjectMatchesV1(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		jsonseed.SkipJSKeys(t, data)
		got, gotErr := orderedJSONObject(data)
		want, wantErr := orderedJSONObjectV1(data)
		sameResult(t, data, "orderedJSONObject", got, want, gotErr, wantErr)
	})
}

func FuzzReadJSONObjectMembersMatchesV1(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		jsonseed.SkipJSKeys(t, data)
		got, gotErr := readJSONObjectMembers(data)
		want, wantErr := readJSONObjectMembersV1(data)
		sameResult(t, data, "readJSONObjectMembers", got, want, gotErr, wantErr)
	})
}

func FuzzOrderedSectionsMatchesV1(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		jsonseed.SkipJSKeys(t, data)
		var got, want OrderedSections
		gotErr := got.UnmarshalJSON(data)
		wantErr := want.UnmarshalJSONV1(data)
		sameResult(t, data, "OrderedSections.UnmarshalJSON", got, want, gotErr, wantErr)
	})
}

func FuzzOpenAIReasoningDetailMatchesV1(f *testing.F) {
	fuzzSeeds(f)
	f.Add([]byte(`{"type":"reasoning.text","text":"x","signature":"s","text":"y"}`))
	f.Add([]byte(`{"type":"reasoning.encrypted","data":"d","id":"i"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		jsonseed.SkipJSKeys(t, data)
		if !json.Valid(data) {
			t.Skip("the reader is lenient about a cut-off document; its caller has validated the JSON")
		}
		got, gotOK := decodeOpenAIReasoningDetail(data)
		want, wantOK := decodeOpenAIReasoningDetailV1(data)
		if gotOK != wantOK || !reflect.DeepEqual(got, want) {
			t.Fatalf("decodeOpenAIReasoningDetail(%q): %#v %v, v1 %#v %v", data, got, gotOK, want, wantOK)
		}
	})
}

func FuzzParseStoredModelsMatchesV1(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		jsonseed.SkipJSKeys(t, data)
		if !json.Valid(data) {
			t.Skip("the reader is lenient about a cut-off document; Pi's JSON.parse rejects it")
		}
		got, gotRoot, gotErr := parseStoredModels(data)
		want, wantRoot, wantErr := parseStoredModelsV1(data)
		sameResult(t, data, "parseStoredModels", []any{got, gotRoot}, []any{want, wantRoot}, gotErr, wantErr)
	})
}

func FuzzOrderedCatalogPairsMatchesV1(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if !json.Valid(data) {
			t.Skip("the reader is lenient about a cut-off document; it reads what marshalStoreJSON wrote")
		}
		got, gotErr := orderedCatalogPairs(data)
		want, wantErr := orderedCatalogPairsV1(data)
		sameResult(t, data, "orderedCatalogPairs", got, want, gotErr, wantErr)
	})
}

func FuzzErrorKeyBeforeTimestampMatchesV1(f *testing.F) {
	fuzzSeeds(f)
	f.Add([]byte(`{"errorMessage":"x","timestamp":1}`))
	f.Add([]byte(`{"timestamp":1,"errorMessage":"x"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if got, want := ErrorKeyBeforeTimestamp(data), ErrorKeyBeforeTimestampV1(data); got != want {
			t.Fatalf("ErrorKeyBeforeTimestamp(%q) = %v, v1 %v", data, got, want)
		}
	})
}

func FuzzReadSchemaObjectOrderMatchesV1(f *testing.F) {
	fuzzSeeds(f)
	f.Add([]byte(`{"properties":{"b":{"type":"string"},"a":{"type":"number"},"b":{"x":1}},"required":["b"]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		got, gotErr := readSchemaObjectOrder(data)
		want, wantErr := readSchemaObjectOrderV1(data)
		sameResult(t, data, "readSchemaObjectOrder", got, want, gotErr, wantErr)
	})
}

func BenchmarkOrderedObjectReaders(b *testing.B) {
	doc := jsonseed.Typical()
	b.Run("orderedJSONObject", func(b *testing.B) {
		jsonseed.Bench(b, doc, func(d []byte) { _, _ = orderedJSONObjectV1(d) }, func(d []byte) { _, _ = orderedJSONObject(d) })
	})
	b.Run("readJSONObjectMembers", func(b *testing.B) {
		jsonseed.Bench(b, doc, func(d []byte) { readJSONObjectMembersV1(d) }, func(d []byte) { readJSONObjectMembers(d) })
	})
	b.Run("readSchemaObjectOrder", func(b *testing.B) {
		jsonseed.Bench(b, doc, func(d []byte) { readSchemaObjectOrderV1(d) }, func(d []byte) { readSchemaObjectOrder(d) })
	})
	b.Run("orderedCatalogPairs", func(b *testing.B) {
		jsonseed.Bench(b, doc, func(d []byte) { orderedCatalogPairsV1(d) }, func(d []byte) { orderedCatalogPairs(d) })
	})
	b.Run("parseStoredModels", func(b *testing.B) {
		jsonseed.Bench(b, doc, func(d []byte) { parseStoredModelsV1(d) }, func(d []byte) { parseStoredModels(d) })
	})
}
