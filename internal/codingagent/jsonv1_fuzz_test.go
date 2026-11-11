package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonseed"
)

func FuzzDecodeOrderedHeaderEntriesMatchesV1(f *testing.F) {
	jsonseed.Add(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if !json.Valid(data) {
			t.Skip("the reader returns once it has the headers, or the end of the keys, and never checked the closing token; the caller has parsed the JSON")
		}
		got, gotErr := decodeOrderedHeaderEntries(data)
		want, wantErr := decodeOrderedHeaderEntriesV1(data)
		jsonseed.Same(t, data, "decodeOrderedHeaderEntries", got, want, gotErr, wantErr)
	})
}

func FuzzOrderedObjectValuesMatchesV1(f *testing.F) {
	jsonseed.Add(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		jsonseed.SkipJSKeys(t, data)
		if !json.Valid(data) || !jsonseed.IsObject(data) {
			t.Skip("the reader is lenient about a cut-off document and is called with objects; response.json() has rejected the rest")
		}
		got, gotMap, gotErr := orderedObjectValues(data)
		want, wantMap, wantErr := orderedObjectValuesV1(data)
		jsonseed.Same(t, data, "orderedObjectValues", []any{got, gotMap}, []any{want, wantMap}, gotErr, wantErr)
	})
}

func FuzzModelsJSONProviderOrderMatchesV1(f *testing.F) {
	jsonseed.Add(f)
	f.Add([]byte(`{"providers":{"b":{},"a":{},"b":{"x":1}}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(t.TempDir(), "models.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		jsonseed.SameResult(t, data, "modelsJSONProviderOrder", modelsJSONProviderOrder(path), modelsJSONProviderOrderV1(path))
	})
}

func BenchmarkModelRegistryReaders(b *testing.B) {
	doc := jsonseed.Typical()
	b.Run("decodeOrderedHeaderEntries", func(b *testing.B) {
		jsonseed.Bench(b, doc, func(d []byte) { _, _ = decodeOrderedHeaderEntriesV1(d) }, func(d []byte) { _, _ = decodeOrderedHeaderEntries(d) })
	})
	b.Run("orderedObjectValues", func(b *testing.B) {
		jsonseed.Bench(b, doc, func(d []byte) { _, _, _ = orderedObjectValuesV1(d) }, func(d []byte) { _, _, _ = orderedObjectValues(d) })
	})
}
