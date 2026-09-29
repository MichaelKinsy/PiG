package tui

import (
	"reflect"
	"strings"
	"testing"
)

func TestUnavailableScopedModelUpstream6949(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/6949-unavailable-scoped-model.test.ts:62
	t.Run("shows and removes an enabled model without a catalog entry", func(t *testing.T) {
		available, unavailable := "fixture/available", "fixture/unavailable"
		selector := NewScopedModelsList(ScopedModelsConfig{AllModels: []ModelItem{{FullID: available, Provider: "fixture", Name: "Available"}}, EnabledModelIDs: []string{unavailable, available}})
		rendered := strings.Join(selector.Render(100), "\n")
		if !strings.Contains(stripANSI(rendered), unavailable+" [unavailable]") || !strings.Contains(rendered, "\x1b[9m"+unavailable+"\x1b[29m") {
			t.Fatalf("missing struck unavailable row: %q", rendered)
		}
		selector.HandleInput("\r")
		if got := selector.EnabledIDs(); !reflect.DeepEqual(got, []string{available}) {
			t.Fatalf("onChange = %v", got)
		}
		selector.HandleInput("\x13")
		if got, ok := selector.ConsumeSave(); !ok || !reflect.DeepEqual(got, []string{available}) {
			t.Fatalf("onPersist = %v, %v", got, ok)
		}
	})
}
