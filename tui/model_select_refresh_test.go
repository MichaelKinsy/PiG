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
		{name: "current reanchors", old: mkItems("p/a", "p/b"), next: mkItems("p/b", "p/a"), current: "p/a", down: 1, want: "p/a"},
		{name: "query retained", old: mkItems("p/a"), next: mkItems("p/a", "p/new"), query: "new", want: "p/new"},
		{name: "scoped order retained", scoped: mkItems("p/b", "p/a"), next: mkItems("p/a", "p/b", "p/c"), want: "p/b"},
		{name: "unavailable scoped retained", scoped: mkItems("p/gone"), next: mkItems("p/new"), want: "p/gone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selector := NewModelSelector("Select model", tc.scoped, tc.old, tc.current)
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

func TestModelSelectorUpdateModelsRefreshesScopedMetadataAndIgnoresClosed(t *testing.T) {
	old := []ModelSelectorItem{{Provider: "radius", ID: "current", Name: "Old"}}
	selector := NewModelSelector("Select model", old, old, "radius/current")
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
			selector := NewModelSelector("Select model", items[:20], items, items[0].FQ())
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
