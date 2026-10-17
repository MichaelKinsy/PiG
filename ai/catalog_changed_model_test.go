package ai

import (
	"bytes"
	"testing"
)

// A built-in model changed after it was built encodes as the record with its changed members merged in, exactly as the same change to the model decoded from
// that record does: the decoded model's shape is the reference, so the unchanged-record shortcut of a built-in model must not differ from it.
func TestChangedBuiltinModelEncodesAsItsDecodedRecord(t *testing.T) {
	changes := map[string]func(*Model){
		"unchanged":     func(*Model) {},
		"name":          func(m *Model) { m.DisplayName += " (changed)" },
		"context":       func(m *Model) { m.Capabilities.ContextWindow++ },
		"output cost":   func(m *Model) { m.Capabilities.OutputCostPer1M += 0.5 },
		"input":         func(m *Model) { m.Input = append(append([]string(nil), m.Input...), "audio") },
		"base url":      func(m *Model) { m.ProviderMeta.BaseURL += "/changed" },
		"cleared input": func(m *Model) { m.Input = nil },
	}
	for i := 0; i < len(GeneratedModels); i += 7 {
		record := GeneratedModels[i].Record
		for name, change := range changes {
			built := GeneratedModels[i].ToModel()
			change(built)
			got, ok, err := CatalogModelJSON(built)
			if err != nil || !ok {
				t.Fatalf("%s %s: ok %t, %v", GeneratedModels[i].ID, name, ok, err)
			}
			decoded, known, err := DecodeStoredModel([]byte(record))
			if err != nil || !known {
				t.Fatalf("%s: decode: known %t, %v", GeneratedModels[i].ID, known, err)
			}
			change(decoded.(*Model))
			want, err := EncodeStoredModel(decoded)
			if err != nil {
				t.Fatalf("%s %s: encode: %v", GeneratedModels[i].ID, name, err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("%s %s:\n got %s\nwant %s", GeneratedModels[i].ID, name, got, want)
			}
		}
	}
}
