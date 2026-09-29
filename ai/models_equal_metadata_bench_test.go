package ai

import (
	"fmt"
	"testing"
)

func BenchmarkMetadataModelEqualityDuringCycle(b *testing.B) {
	models := make([]*Model, 128)
	for i := range models {
		models[i] = &Model{ID: fmt.Sprintf("model-%d", i), ProviderMeta: ProviderMetadata{ProviderID: "catalog"}}
	}
	current := *models[len(models)-1]
	b.ReportAllocs()
	for b.Loop() {
		found := -1
		for i, model := range models {
			if ModelsAreEqual(model, &current) {
				found = i
				break
			}
		}
		if found != len(models)-1 {
			b.Fatal("current model not found")
		}
	}
}
