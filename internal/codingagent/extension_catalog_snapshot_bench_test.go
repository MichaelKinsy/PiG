package codingagent

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestCloneCatalogInputDoesNotAllocateInlineStructs(t *testing.T) {
	input := reflect.ValueOf(struct {
		ID     string
		Limits struct{ Tokens int }
	}{ID: "model"})
	allocations := testing.AllocsPerRun(100, func() {
		if _, ok := cloneCatalogInput(input); !ok {
			panic("scalar snapshot rejected")
		}
	})
	// One owned root holds both the outer fields and its inline scalar-only child.
	if allocations != 1 {
		t.Fatalf("snapshot allocated %.0f objects, want its one owned root", allocations)
	}
}

func BenchmarkCloneCatalogInput(b *testing.B) {
	models := make([]*ai.Model, len(ai.GeneratedModels))
	for i := range ai.GeneratedModels {
		models[i] = ai.GeneratedModels[i].ToModel()
	}
	value := reflect.ValueOf(models)
	b.ReportAllocs()
	for b.Loop() {
		if _, ok := cloneCatalogInput(value); !ok {
			b.Fatal("catalog snapshot rejected")
		}
	}
}
