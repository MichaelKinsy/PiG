package codingagent

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// D199: OpenRouter's routing variants are derived on demand. The available models of a registry list them only for a configured OpenRouter, so a
// registry without its auth derives none (the Copilot account picker and every other provider's availability read no OpenRouter variant), and one with
// its auth lists each model's :nitro and :floor right after the model.
func TestAvailableModelDataDerivesRoutingVariantsOnlyForAConfiguredOpenRouter(t *testing.T) {
	previous := currentBuiltinChatCatalogs.Load()
	t.Cleanup(func() { currentBuiltinChatCatalogs.Store(previous) })
	listedVariants := func() bool {
		catalogs := currentBuiltinChatCatalogs.Load()
		if catalogs == nil {
			return false
		}
		catalogs.mu.RLock()
		defer catalogs.mu.RUnlock()
		return catalogs.openRouterListed != nil
	}

	t.Setenv("OPENROUTER_API_KEY", "")
	currentBuiltinChatCatalogs.Store(nil)
	registry := NewModelRegistry(t.TempDir())
	t.Cleanup(registry.CloseModelTasks)
	for _, model := range registry.GetAvailableModelData() {
		if model.ProviderMeta.ProviderID == "openrouter" {
			t.Fatalf("%s is available without OpenRouter auth", model.ID)
		}
	}
	if listedVariants() {
		t.Fatal("the available models of a registry without OpenRouter auth derived OpenRouter's routing variants")
	}

	t.Setenv("OPENROUTER_API_KEY", "fixture")
	var ids []string
	for _, model := range registry.GetAvailableModelData() {
		if model.ProviderMeta.ProviderID == "openrouter" {
			ids = append(ids, model.ID)
		}
	}
	if len(ids) == 0 {
		t.Fatal("no OpenRouter model is available with its auth")
	}
	for i, id := range ids {
		if _, _, variant := ai.OpenRouterRoutingVariant("openrouter", id); variant {
			continue
		}
		for offset, name := range ai.OpenRouterRoutingVariants {
			if i+1+offset >= len(ids) || ids[i+1+offset] != id+":"+name {
				if slices.Contains(ids, id+":"+name) {
					continue // the catalog lists this variant itself, somewhere else
				}
				t.Fatalf("%s is not followed by its %s variant: %v", id, name, strings.Join(ids[i:min(len(ids), i+4)], ", "))
			}
		}
	}
	if !listedVariants() {
		t.Fatal("a configured OpenRouter listed no routing variants")
	}
}
