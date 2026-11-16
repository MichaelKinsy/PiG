package codingagent

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/catalogfixture"
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

// BenchmarkCloneCatalogInput measures the snapshot copy on a fixed input: the synthetic catalog of internal/catalogfixture, sized and shaped like the shipped one on 2026-11-16, so a catalog refresh does not move its row. The copy of the live catalog is bounded per entry by TestCloneCatalogInputLiveAllocationBudget.
func BenchmarkCloneCatalogInput(b *testing.B) {
	value := reflect.ValueOf(catalogfixture.Models())
	b.ReportAllocs()
	for b.Loop() {
		if _, ok := cloneCatalogInput(value); !ok {
			b.Fatal("catalog snapshot rejected")
		}
	}
}

// The snapshot copy of the shipped catalog costs a bounded number of allocations per entry: more entries are not a regression, a record that costs more to copy is. The budget is the mean of 17.69 allocations per entry on 2026-11-16 (1,663 entries) and 13 % more.
func TestCloneCatalogInputLiveAllocationBudget(t *testing.T) {
	const budget = 20
	models := make([]*ai.Model, len(ai.GeneratedModels))
	for i := range ai.GeneratedModels {
		models[i] = ai.GeneratedModels[i].ToModel()
	}
	value := reflect.ValueOf(models)
	got := testing.AllocsPerRun(5, func() {
		if _, ok := cloneCatalogInput(value); !ok {
			t.Fatal("catalog snapshot rejected")
		}
	})
	if perModel := got / float64(len(models)); perModel > budget {
		t.Fatalf("the catalog snapshot allocates %.1f times per catalog model, budget %d: make the clone cheaper per entry, or raise the budget with the attribution of what got richer, by the coordinator's word", perModel, budget)
	}
}

// The fixture of BenchmarkCloneCatalogInput stands for the shipped catalog: its snapshot copy costs the 17.69 allocations per entry that the shipped catalog's copy cost on 2026-11-16, within 3 %. The figure is pinned, not read from the live catalog, so a refresh does not turn this test red.
func TestCatalogFixtureCloneCostMatchesTheShippedCatalog(t *testing.T) {
	models := catalogfixture.Models()
	if len(models) != catalogfixture.LiveEntries {
		t.Fatalf("fixture has %d entries, want %d", len(models), catalogfixture.LiveEntries)
	}
	value := reflect.ValueOf(models)
	got := testing.AllocsPerRun(5, func() {
		if _, ok := cloneCatalogInput(value); !ok {
			t.Fatal("catalog snapshot rejected")
		}
	}) / float64(len(models))
	const shipped = 17.69
	if got < shipped*0.97 || got > shipped*1.03 {
		t.Fatalf("fixture snapshot allocates %.2f times per entry, want %.2f within 3 %%", got, shipped)
	}
}
