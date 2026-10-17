package codingagent

import (
	"maps"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi packages/coding-agent/src/core/provider-composer.ts:131-141 mergeCompat: for openRouterRouting and chatTemplateArgs (chatTemplateKwargs and vercelGatewayRouting merge the same way)
// an override object merges key by key over the base object (`{ ...base, ...override }`), so the base keys the override leaves out
// stay and a shared key takes the override's value. An override object with no keys over a missing base is still an object
// (`{}`), and when neither side carries the key it stays absent.
// mutation-checked: the mutant "mergeCompatMap drops the base keys" (model_registry.go:1950) fails it.
// mutation-checked: the mutant "mergeCompatMap returns nil for an empty override over a missing base" (model_registry.go:1947) fails it.
// mutation-checked: the mutant "mergeCompatMap aliases the base map" (model_registry.go:1950) fails it.
func TestMergeCompatMergesNestedMapKeysKeyByKey(t *testing.T) {
	type access struct {
		name string
		set  func(*ai.OpenAICompat, map[string]any)
		get  func(*ai.OpenAICompat) map[string]any
	}
	for _, field := range []access{
		{"openRouterRouting", func(c *ai.OpenAICompat, m map[string]any) { c.OpenRouterRouting = m }, func(c *ai.OpenAICompat) map[string]any { return c.OpenRouterRouting }},
		{"chatTemplateArgs", func(c *ai.OpenAICompat, m map[string]any) { c.ChatTemplateArgs = m }, func(c *ai.OpenAICompat) map[string]any { return c.ChatTemplateArgs }},
	} {
		t.Run(field.name, func(t *testing.T) {
			var base, override ai.OpenAICompat
			field.set(&base, map[string]any{"keep": "base", "shared": "base"})
			field.set(&override, map[string]any{"shared": "override", "new": "override"})
			merged := mergeCompat((*providerCompat)(&base), (*providerCompat)(&override))
			want := map[string]any{"keep": "base", "shared": "override", "new": "override"}
			if got := field.get(merged); !maps.Equal(got, want) {
				t.Fatalf("merged = %v, want %v", got, want)
			}
			field.get(merged)["keep"] = "mutated"
			if field.get(&base)["keep"] != "base" {
				t.Fatal("merge aliased the base map")
			}

			var empty, none ai.OpenAICompat
			field.set(&empty, map[string]any{})
			if got := field.get(mergeCompat((*providerCompat)(&none), (*providerCompat)(&empty))); got == nil || len(got) != 0 {
				t.Fatalf("empty override over a missing base = %#v, want an empty object", got)
			}
			if got := field.get(mergeCompat((*providerCompat)(&none), (*providerCompat)(&none))); got != nil {
				t.Fatalf("neither side set merged to %#v, want absent", got)
			}
		})
	}
}
