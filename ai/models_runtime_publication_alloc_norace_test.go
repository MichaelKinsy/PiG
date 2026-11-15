//go:build !race

package ai

import (
	"context"
	"testing"
)

// A startup refresh publishes once per provider (123 publications in a warm first prompt with a models.json), so what one publication allocates is paid a
// hundred times over in the startup allocation budgets (coding/cli TestStartupAllocationBudget*, which only move down). A publication with nothing to
// write and no earlier one to wait for runs in place and allocates its chain link (a channel) and its publish closure. Settling its place in the chain once
// took a closure of its own, which put a Windows warm first prompt with a models.json 124 objects and 6 KB over its budget.
func TestPublishProviderModelsAllocatesOnlyItsChainLink(t *testing.T) {
	models := CreateModels()
	signal := context.Background()
	publication := ModelsPublication{}
	if _, err := models.publishProviderModels("p", 0, signal, publication); err != nil {
		t.Fatal(err)
	}
	got := testing.AllocsPerRun(200, func() { _, _ = models.publishProviderModels("p", 0, signal, publication) })
	if got > 2 {
		t.Fatalf("an empty publication allocates %v times, ceiling 2", got)
	}
}
