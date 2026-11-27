package providers

import (
	"testing"

	. "github.com/MichaelKinsy/PiG/internal/modelgen"
)

func claudeModelsDevEntry(id string, reasoning *Obj, canonicalID string) *Obj {
	entry := O(
		"id", id,
		"type", "chat",
		"name", id,
		"modalities", O("input", Strings("text"), "output", Strings("text")),
		"capabilities", O("tools", O("supported", true), "reasoning", reasoning),
		"api", O(),
	)
	if canonicalID != "" {
		entry.Set("canonical_id", canonicalID)
	}
	return entry
}

// Ports packages/ai/test/claude-model-generation.test.ts (Pi 433c1fd94):
// verifies cannotTurnThinkingOff with a real mocked models.dev ProviderContext.
func TestClaudeThinkingOffFromAnthropicModelsDev(t *testing.T) {
	effort := Strings("low", "medium", "high", "xhigh", "max")
	noToggle := O("supported", true, "effort", effort)
	withToggle := O("supported", true, "toggle", true, "effort", effort)

	t.Run("follows the thinking toggle of Anthropic models.dev entry on GitHub Copilot", func(t *testing.T) {
		sources := &CatalogSources{
			ModelsDev: O(
				"anthropic", O("models", O(
					"claude-sonnet-5-5", claudeModelsDevEntry("claude-sonnet-5-5", noToggle, ""),
					"claude-haiku-5-5", claudeModelsDevEntry("claude-haiku-5-5", withToggle, ""),
				)),
				"github-copilot", O("models", O(
					"claude-sonnet-5.5", claudeModelsDevEntry("claude-sonnet-5.5", noToggle, "anthropic/claude-sonnet-5-5"),
					"claude-haiku-5.5", claudeModelsDevEntry("claude-haiku-5.5", noToggle, "anthropic/claude-haiku-5-5"),
				)),
			),
		}
		catalog, err := provBBuild(t, "github-copilot", sources, false)
		if err != nil {
			t.Fatal(err)
		}
		models := provBModelsOf(catalog)
		sonnet := models["claude-sonnet-5.5"].Object("thinkingLevelMap")
		if val, ok := sonnet.Get("off"); !ok || val != nil {
			t.Fatalf("expected claude-sonnet-5.5 to have off:null, got %v", sonnet)
		}
		haiku := models["claude-haiku-5.5"].Object("thinkingLevelMap")
		if val, ok := haiku.Get("off"); ok && val == nil {
			t.Fatalf("expected claude-haiku-5.5 to allow off, got %v", haiku)
		}
	})

	t.Run("resolves Anthropic entry through canonical_id on Amazon Bedrock", func(t *testing.T) {
		sources := &CatalogSources{
			ModelsDev: O(
				"anthropic", O("models", O(
					"claude-sonnet-5-5", claudeModelsDevEntry("claude-sonnet-5-5", noToggle, ""),
				)),
				"amazon-bedrock", O("models", O(
					"us.anthropic.claude-sonnet-5-5", claudeModelsDevEntry("us.anthropic.claude-sonnet-5-5", noToggle, "anthropic/claude-sonnet-5-5"),
				)),
			),
		}
		catalog, err := provBBuild(t, "amazon-bedrock", sources, false)
		if err != nil {
			t.Fatal(err)
		}
		models := provBModelsOf(catalog)
		bedrock := models["us.anthropic.claude-sonnet-5-5"].Object("thinkingLevelMap")
		if val, ok := bedrock.Get("off"); !ok || val != nil {
			t.Fatalf("expected Bedrock model resolved via canonical_id to have off:null, got %v", bedrock)
		}
	})

	t.Run("authored claude-fable-5-2 keeps off unsupported without models.dev data", func(t *testing.T) {
		sources := &CatalogSources{ModelsDev: O("anthropic", O("models", O()))}
		catalog, err := provBBuild(t, "anthropic", sources, false)
		if err != nil {
			t.Fatal(err)
		}
		models := provBModelsOf(catalog)
		fable := models["claude-fable-5-2"].Object("thinkingLevelMap")
		if val, ok := fable.Get("off"); !ok || val != nil {
			t.Fatalf("expected authored claude-fable-5-2 to keep off:null, got %v", fable)
		}
	})
}

// Ports Pi b8be10a7a (Workers AI requiresNonNullAssistantContent for the 4 affected models + control).
func TestWorkersAINonNullAssistantContentCatalog(t *testing.T) {
	affected := []string{
		"@cf/ibm-granite/granite-4.0-h-micro",
		"@cf/meta/llama-3.3-70b-instruct-fp8-fast",
		"@cf/openai/gpt-oss-120b",
		"@cf/openai/gpt-oss-20b",
	}
	control := "@cf/qwen/qwen3-30b-a3b-fp8"
	specs := make([]provBSpec, 0, len(affected)+1)
	for _, id := range append(append([]string(nil), affected...), control) {
		specs = append(specs, provBSpec{ID: id})
	}
	sources := &CatalogSources{ModelsDev: provBModelsDev(t, map[string][]provBSpec{"cloudflare-workers-ai": specs})}
	catalog, err := provBBuild(t, "cloudflare-workers-ai", sources, false)
	if err != nil {
		t.Fatal(err)
	}
	models := provBModelsOf(catalog)
	for _, id := range affected {
		if !models[id].Object("compat").Bool("requiresNonNullAssistantContent") {
			t.Errorf("expected %s to set requiresNonNullAssistantContent=true, compat=%v", id, models[id].Object("compat"))
		}
	}
	if models[control].Object("compat").Bool("requiresNonNullAssistantContent") {
		t.Errorf("control model %s should not set requiresNonNullAssistantContent", control)
	}
}

// Verifies Groq SkipDeprecated (8c47efc7b), Mistral mistral-medium-3.5 removal (8c47efc7b),
// OpenAI gpt-5-chat-latest removal (b3adbc804), and Copilot gpt-6-luna-decisions + OpenAIDecisionsCost (90ef46ffe).
func TestV161CatalogRemovalsAndDecisions(t *testing.T) {
	t.Run("Groq skips deprecated models", func(t *testing.T) {
		sources := &CatalogSources{
			ModelsDev: O("groq", O("models", O(
				"llama-3.3-70b-versatile", O("id", "llama-3.3-70b-versatile", "type", "chat", "name", "Old", "status", "deprecated", "modalities", O("input", Strings("text"), "output", Strings("text")), "capabilities", O("tools", O("supported", true)), "api", O()),
				"openai/gpt-oss-120b", O("id", "openai/gpt-oss-120b", "type", "chat", "name", "Active", "modalities", O("input", Strings("text"), "output", Strings("text")), "capabilities", O("tools", O("supported", true)), "api", O()),
			))),
		}
		catalog, err := provBBuild(t, "groq", sources, false)
		if err != nil {
			t.Fatal(err)
		}
		models := provBModelsOf(catalog)
		if models["llama-3.3-70b-versatile"] != nil {
			t.Fatalf("expected deprecated Groq model skipped, got %v", models["llama-3.3-70b-versatile"])
		}
		if models["openai/gpt-oss-120b"] == nil {
			t.Fatal("expected active Groq model kept")
		}
	})

	t.Run("Mistral drops hand-added mistral-medium-3.5 and OpenAI drops gpt-5-chat-latest", func(t *testing.T) {
		sources := &CatalogSources{ModelsDev: O("mistral", O("models", O()), "openai", O("models", O()))}
		mistralCat, err := provBBuild(t, "mistral", sources, false)
		if err != nil {
			t.Fatal(err)
		}
		if provBModelsOf(mistralCat)["mistral-medium-3.5"] != nil {
			t.Fatal("expected mistral-medium-3.5 removed from Mistral catalog")
		}
		openaiCat, err := provBBuild(t, "openai", sources, false)
		if err != nil {
			t.Fatal(err)
		}
		if provBModelsOf(openaiCat)["gpt-5-chat-latest"] != nil {
			t.Fatal("expected gpt-5-chat-latest removed from OpenAI catalog")
		}
	})

	t.Run("Copilot lists gpt-6-luna-decisions with shared OpenAIDecisionsCost", func(t *testing.T) {
		sources := &CatalogSources{ModelsDev: O("github-copilot", O("models", O()), "openai", O("models", O()))}
		copilotCat, err := provBBuild(t, "github-copilot", sources, false)
		if err != nil {
			t.Fatal(err)
		}
		var found *Obj
		for _, c := range copilotCat.Classifier {
			if c.Str("id") == "gpt-6-luna-decisions" {
				found = c
				break
			}
		}
		if found == nil {
			t.Fatalf("expected gpt-6-luna-decisions in Copilot classifiers, got %v", copilotCat.Classifier)
		}
		if found.Str("api") != "openai-decisions" || found.Num("contextWindow") != 872000 {
			t.Fatalf("unexpected gpt-6-luna-decisions metadata: %v", found)
		}
		cost := found.Object("cost")
		want := OpenAIDecisionsCost()
		if cost.Num("input") != want.Num("input") || cost.Num("cacheRead") != 0.1 || cost.Num("cacheWrite") != 0.1 {
			t.Fatalf("cost=%v want=%v", cost, want)
		}
	})
}
