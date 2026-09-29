package tui

import (
	"strings"
	"testing"
)

func catalogSelectedID(t *testing.T, selector *ModelSelector) string {
	t.Helper()
	for _, line := range selector.Render(120) {
		plain := stripANSI(line)
		if after, ok := strings.CutPrefix(plain, "→ "); ok {
			rest := strings.TrimSpace(after)
			rest = strings.TrimSpace(strings.TrimPrefix(rest, "✓ "))
			id, _, _ := strings.Cut(rest, " [")
			return strings.TrimSpace(id)
		}
	}
	t.Fatal("selected row missing")
	return ""
}

func TestModelSelectorFilterResetsSelectionUpstream7209(t *testing.T) {
	items := []ModelSelectorItem{{Provider: "test", ID: "alpha-1", Name: "Alpha One"}, {Provider: "test", ID: "alpha-2", Name: "Alpha Two"}, {Provider: "test", ID: "alpha-3", Name: "Alpha Three"}, {Provider: "test", ID: "beta-1", Name: "Beta One"}}
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/7209-model-selector-filter-resets-selection.test.ts:38
	t.Run("moves selection to the first row in the All tab when typing a query", func(t *testing.T) {
		selector := NewModelSelector("Select model", nil, items, "test/alpha-1")
		selector.UpdateModels(items)
		selector.SetRefreshSuccess("Model catalogs refreshed.")
		if got := catalogSelectedID(t, selector); got != "alpha-1" {
			t.Fatal(got)
		}
		selector.HandleInput("\x1b[B")
		selector.HandleInput("\x1b[B")
		if got := catalogSelectedID(t, selector); got != "alpha-3" {
			t.Fatal(got)
		}
		for _, c := range "alpha" {
			selector.HandleInput(string(c))
		}
		if got := catalogSelectedID(t, selector); got != "alpha-1" {
			t.Fatal(got)
		}
		if rendered := stripANSI(strings.Join(selector.Render(120), "\n")); strings.Contains(rendered, "beta-1") {
			t.Fatal(rendered)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/7209-model-selector-filter-resets-selection.test.ts:83
	t.Run("moves selection to the first row in the Scoped tab when typing a query", func(t *testing.T) {
		selector := NewModelSelector("Select model", []ModelSelectorItem{items[1], items[2], items[0]}, items[:3], "test/alpha-1")
		selector.UpdateModels(items[:3])
		selector.SetRefreshSuccess("Model catalogs refreshed.")
		if got := catalogSelectedID(t, selector); got != "alpha-1" {
			t.Fatal(got)
		}
		for _, c := range "alpha" {
			selector.HandleInput(string(c))
		}
		if got := catalogSelectedID(t, selector); got != "alpha-2" {
			t.Fatal(got)
		}
	})
}
