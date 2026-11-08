package tui

import (
	"fmt"
	"slices"
	"testing"
)

// model-selector.ts refreshModels reloads the snapshot, preserves scope and
// search, reanchors on current, and finally filters (selecting the best match).
func TestModelSelectorUpdateModels(t *testing.T) {
	for _, tc := range []struct {
		name              string
		old, next, scoped []ModelSelectorItem
		current, query    string
		down              int
		want              string
	}{
		{name: "empty", want: ""},
		{name: "arrival", next: mkItems("radius/new"), want: "radius/new"},
		{name: "removed last clamps", old: mkItems("p/a", "p/b", "p/c"), next: mkItems("p/a", "p/b"), down: 2, want: "p/b"},
		{name: "current reanchors when nothing was browsed", old: mkItems("p/a", "p/b"), next: mkItems("p/b", "p/a"), current: "p/a", want: "p/a"},
		// model-selector.test.ts "when the catalog refresh lands" (#9109, Pi df0d88aac).
		{name: "keeps the selection on the browsed model", old: mkItems("p/alpha-1", "p/alpha-2", "p/alpha-3", "p/beta-1"), next: mkItems("p/alpha-1", "p/alpha-2", "p/alpha-3", "p/beta-1"), current: "p/alpha-1", down: 2, want: "p/alpha-3"},
		{name: "follows the selected model when new rows shift its position", old: mkItems("p/alpha-1", "p/alpha-2", "p/alpha-3", "p/beta-1"), next: mkItems("p/added-model", "p/alpha-1", "p/alpha-2", "p/alpha-3", "p/beta-1"), current: "p/alpha-1", down: 1, want: "p/alpha-2"},
		{name: "falls back to the current model when the selected model disappears", old: mkItems("p/alpha-1", "p/alpha-2", "p/alpha-3", "p/beta-1"), next: mkItems("p/alpha-1", "p/alpha-3", "p/beta-1"), current: "p/alpha-1", down: 1, want: "p/alpha-1"},
		{name: "query retained", old: mkItems("p/a"), next: mkItems("p/a", "p/new"), query: "new", want: "p/new"},
		{name: "scoped order retained", scoped: mkItems("p/b", "p/a"), next: mkItems("p/a", "p/b", "p/c"), want: "p/b"},
		{name: "unavailable scoped retained", scoped: mkItems("p/gone"), next: mkItems("p/new"), want: "p/gone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selector := NewStaticModelSelectorComponent("Select model", tc.scoped, tc.old, tc.current)
			for range tc.down {
				selector.HandleInput("\x1b[B")
			}
			selector.SetFilter(tc.query)
			scope := selector.Scope()
			selector.UpdateModels(tc.next)
			if selector.Scope() != scope || selector.searchInput.Text() != tc.query {
				t.Fatal("refresh changed scope or search")
			}
			selector.HandleInput("\r")
			if got := selector.SelectedFQ(); got != tc.want {
				t.Errorf("selected %q, want %q", got, tc.want)
			}
		})
	}
}

// model-selector.test.ts "keeps the selection while a search query is active" (#9109).
func TestModelSelectorUpdateModelsKeepsTheSelectionWhileASearchQueryIsActive(t *testing.T) {
	items := mkItems("p/alpha-1", "p/alpha-2", "p/alpha-3", "p/beta-1")
	selector := NewStaticModelSelectorComponent("Select model", nil, items, "p/alpha-1")
	selector.SetFilter("alpha")
	selector.HandleInput("\x1b[B")
	selector.UpdateModels(items)
	selector.HandleInput("\r")
	if got := selector.SelectedFQ(); got != "p/alpha-2" {
		t.Fatalf("selected %q, want the browsed p/alpha-2", got)
	}
}

func TestModelSelectorUpdateModelsRefreshesScopedMetadataAndIgnoresClosed(t *testing.T) {
	old := []ModelSelectorItem{{Provider: "radius", ID: "current", Name: "Old"}}
	selector := NewStaticModelSelectorComponent("Select model", old, old, "radius/current")
	next := []ModelSelectorItem{{Provider: "radius", ID: "current", Name: "Updated"}}
	selector.UpdateModels(next)
	if selector.active[0].Name != "Updated" {
		t.Fatal("scoped metadata is stale")
	}
	next[0].Name = "mutated caller data"
	if selector.active[0].Name != "Updated" {
		t.Fatal("snapshot aliases caller data")
	}
	selector.HandleInput("\x1b")
	before := selector.Render(100)
	selector.UpdateModels(nil)
	if !slices.Equal(before, selector.Render(100)) {
		t.Fatal("closed selector accepted a late refresh")
	}
}

func BenchmarkModelSelectorCatalogRefresh(b *testing.B) {
	for _, count := range []int{100, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			items := make([]ModelSelectorItem, count)
			for i := range items {
				items[i] = ModelSelectorItem{Provider: fmt.Sprintf("provider-%d", i%20), ID: fmt.Sprintf("model-%d", i), Name: "Catalog model"}
			}
			selector := NewStaticModelSelectorComponent("Select model", items[:20], items, items[0].FQ())
			selector.HandleInput("\t")
			selector.SetFilter("model-9")
			b.ReportAllocs()
			for b.Loop() {
				selector.UpdateModels(items)
				selector.Render(100)
			}
		})
	}
}
