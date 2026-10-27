package codingagent

import "testing"

// D199: Pi lists no OpenRouter routing variant, so a partial text must resolve to the model it resolves to in Pi, whose reverse-collation
// tie break would prefer ":nitro" (it collates after ":batch" and ":floor") if a variant were a candidate. A text that names a variant, or
// the variant's exact reference, selects it, derived from its base model.
func TestPartialModelTextIgnoresOpenRouterRoutingVariants(t *testing.T) {
	piModels := []RuntimeModel{
		{Provider: "openrouter", ID: "openai/gpt-4o", Name: "OpenAI: GPT-4o"},
		{Provider: "openrouter", ID: "openai/gpt-4o:batch", Name: "OpenAI: GPT-4o (batch)"},
		{Provider: "openai", ID: "gpt-4o", Name: "GPT-4o"},
	}
	withVariants := append([]RuntimeModel{}, piModels...)
	withVariants = append(withVariants,
		RuntimeModel{Provider: "openrouter", ID: "openai/gpt-4o:nitro", Name: "OpenAI: GPT-4o (nitro)"},
		RuntimeModel{Provider: "openrouter", ID: "openai/gpt-4o:floor", Name: "OpenAI: GPT-4o (floor)"})
	resolve := func(models []RuntimeModel, text string) (string, string) {
		result := ResolveModelReference(ModelReferenceRequest{Text: text, ThinkingSuffix: ThinkingSuffixStrict, Fuzzy: true}, models, func(provider string) bool { return provider == "openrouter" })
		if result.Model == nil {
			return "", ""
		}
		return result.Model.Provider, result.Model.ID
	}
	for _, text := range []string{"4o", "gpt-4o", "openrouter/gpt-4o"} {
		want1, want2 := resolve(piModels, text)
		if got1, got2 := resolve(withVariants, text); got1 != want1 || got2 != want2 {
			t.Errorf("%q with variants in the pool resolves to %s/%s, as Pi's list does to %s/%s", text, got1, got2, want1, want2)
		}
	}
	for text, want := range map[string]string{
		"openrouter/openai/gpt-4o:nitro": "openai/gpt-4o:nitro",
		"openrouter/openai/gpt-4o:floor": "openai/gpt-4o:floor",
		"openai/gpt-4o:nitro":            "openai/gpt-4o:nitro",
		"gpt-4o:nitro":                   "openai/gpt-4o:nitro",
		"openrouter/gpt-4o:floor":        "openai/gpt-4o:floor",
	} {
		for name, pool := range map[string][]RuntimeModel{"derived": piModels, "listed": withVariants} {
			if provider, id := resolve(pool, text); provider != "openrouter" || id != want {
				t.Errorf("%q (%s pool) resolves to %s/%s, want openrouter/%s", text, name, provider, id, want)
			}
		}
	}
	// The derived variant keeps the base model's fields.
	result := ResolveModelReference(ModelReferenceRequest{Text: "openrouter/openai/gpt-4o:nitro", ThinkingSuffix: ThinkingSuffixStrict}, piModels, func(string) bool { return true })
	if result.Model == nil || result.Model.Name != "OpenAI: GPT-4o (nitro)" {
		t.Fatalf("derived variant = %+v", result.Model)
	}
}
