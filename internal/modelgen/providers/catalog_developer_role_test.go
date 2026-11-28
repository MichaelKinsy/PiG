package providers

import (
	"testing"

	. "github.com/MichaelKinsy/PiG/internal/modelgen"
)

// Pi b82477b7b (#12134): every catalog provider touched by Pi b82477b7b removes
// redundant supportsDeveloperRole: false from openai-completions compat metadata.
// Expectations are derived directly from Pi b82477b7b catalog files:
//
//	families/kimi.ts, providers/{ant-ling,azure,baseten,cerebras,cloudflare-ai-gateway,
//	cloudflare-workers-ai,deepseek,fireworks,github-copilot,huggingface,moonshotai,
//	nvidia,opencode,openrouter,qwen-token-plan,together,zai}.ts
func TestCatalogOmitsRedundantSupportsDeveloperRole(t *testing.T) {
	assertNoSupportsDeveloperRole := func(t *testing.T, name string, obj *Obj) {
		t.Helper()
		if obj == nil {
			return
		}
		if _, has := obj.Get("supportsDeveloperRole"); has {
			t.Errorf("%s compat still sets supportsDeveloperRole (removed in Pi b82477b7b)", name)
		}
	}

	// 1. families/kimi.ts
	assertNoSupportsDeveloperRole(t, "KimiOpenAICompat", KimiOpenAICompat())
	assertNoSupportsDeveloperRole(t, "KimiK3OpenAICompat", KimiK3OpenAICompat())

	// 2. Direct compat helper constants/functions from providers/*.ts
	assertNoSupportsDeveloperRole(t, "basetenBaseCompat", basetenBaseCompat())
	assertNoSupportsDeveloperRole(t, "fireworksOpenAICompat", fireworksOpenAICompat())
	assertNoSupportsDeveloperRole(t, "moonshotCompletionsCompat", moonshotCompletionsCompat())
	assertNoSupportsDeveloperRole(t, "opencodeCompletionsCompat", opencodeCompletionsCompat())
	assertNoSupportsDeveloperRole(t, "qwenTokenPlanCompat", qwenTokenPlanCompat())
	assertNoSupportsDeveloperRole(t, "togetherBaseCompat", togetherBaseCompat())
	assertNoSupportsDeveloperRole(t, "zaiCompletionsCompat", zaiCompletionsCompat())

	// 3. ProviderDefinition.Compat["openai-completions"] for every provider in All()
	//    touched by Pi b82477b7b.
	touchedProviders := map[string]bool{
		"ant-ling":                      true,
		"azure":                         true,
		"baseten":                       true,
		"cerebras":                      true,
		"cloudflare-ai-gateway":         true,
		"cloudflare-workers-ai":         true,
		"deepseek":                      true,
		"fireworks":                     true,
		"github-copilot":                true,
		"huggingface":                   true,
		"kimi-coding":                   true,
		"moonshotai":                    true,
		"moonshotai-cn":                 true,
		"nvidia":                        true,
		"opencode":                      true,
		"opencode-go":                   true,
		"openrouter":                    true,
		"qwen-token-plan":               true,
		"qwen-token-plan-cn":            true,
		"qwen-token-plan-individual":    true,
		"qwen-token-plan-individual-cn": true,
		"together":                      true,
		"zai":                           true,
		"zai-coding-cn":                 true,
	}

	probeModels := []ChatModel{
		O("id", "deepseek/deepseek-v4-pro", "api", "openai-completions"),
		O("id", "openai/gpt-5.2-codex", "api", "openai-completions"),
		O("id", "anthropic/claude-fable-5.1:batch", "api", "openai-completions"),
		O("id", "moonshotai/kimi-k2.6", "api", "openai-completions"),
		O("id", "qwen3.7-max", "api", "openai-completions"),
	}

	for _, def := range All() {
		if !touchedProviders[def.ID] {
			continue
		}
		if fn, ok := def.Compat["openai-completions"]; ok {
			for _, m := range probeModels {
				assertNoSupportsDeveloperRole(t, def.ID+".Compat["+m.Str("id")+"]", fn(m))
			}
		}
		if def.Finalize != nil {
			for _, m := range probeModels {
				cloned := Spread(m)
				def.Finalize(cloned, nil)
				assertNoSupportsDeveloperRole(t, def.ID+".Finalize["+m.Str("id")+"]", cloned.Object("compat"))
			}
		}
	}

	// 4. openrouterCorrectUpstream for moonshotai/kimi-k2.6
	kimi := openrouterCorrectUpstream(O("id", "moonshotai/kimi-k2.6", "api", "openai-completions"))
	assertNoSupportsDeveloperRole(t, "openrouterCorrectUpstream(kimi-k2.6)", kimi.Object("compat"))

	// 5. Models closures of github-copilot and huggingface (executed via BuildCatalog).
	byID := map[string]ProviderDefinition{}
	for _, def := range All() {
		byID[def.ID] = def
	}
	hfSources := &CatalogSources{
		ModelsDev: O("huggingface", O("models", O(
			"deepseek-ai/DeepSeek-R1", O(
				"id", "deepseek-ai/DeepSeek-R1",
				"name", "DeepSeek R1",
				"type", "chat",
				"reasoning", true,
				"capabilities", O("tools", O("supported", true)),
				"modalities", O("input", Strings("text"), "output", Strings("text")),
				"api", O("chat", O("base_url", "https://router.huggingface.co/v1")),
			),
		))),
	}
	hfCatalogs, err := BuildCatalog([]ProviderDefinition{byID["huggingface"]}, hfSources, false)
	if err != nil {
		t.Fatalf("BuildCatalog(huggingface): %v", err)
	}
	if len(hfCatalogs) != 1 || len(hfCatalogs[0].Chat) != 1 {
		t.Fatalf("huggingface catalogs = %+v", hfCatalogs)
	}
	hfCompat := hfCatalogs[0].Chat[0].Object("compat")
	assertNoSupportsDeveloperRole(t, "huggingface.Models[deepseek-ai/DeepSeek-R1]", hfCompat)
	if got, want := string(Stringify(hfCompat, "")), `{"supportsStrictMode":true}`; got != want {
		t.Errorf("huggingface compat = %s, want %s", got, want)
	}

	copilotSources := &CatalogSources{
		ModelsDev: O("github-copilot", O("models", O(
			"gemini-3.8-flash", O(
				"id", "gemini-3.8-flash",
				"name", "Gemini 3.8 Flash",
				"type", "chat",
				"reasoning", true,
				"capabilities", O("tools", O("supported", true)),
				"modalities", O("input", Strings("text"), "output", Strings("text")),
				"api", O("chat", O("base_url", githubCopilotBaseURL)),
			),
		))),
	}
	copilotCatalogs, err := BuildCatalog([]ProviderDefinition{byID["github-copilot"]}, copilotSources, false)
	if err != nil {
		t.Fatalf("BuildCatalog(github-copilot): %v", err)
	}
	foundGemini := false
	for _, m := range copilotCatalogs[0].Chat {
		if m.Str("id") != "gemini-3.8-flash" {
			continue
		}
		foundGemini = true
		copCompat := m.Object("compat")
		assertNoSupportsDeveloperRole(t, "github-copilot.Models[gemini-3.8-flash]", copCompat)
		if got, want := string(Stringify(copCompat, "")), `{"supportsStrictMode":true,"supportsStore":false,"supportsReasoningEffort":false}`; got != want {
			t.Errorf("github-copilot gemini-3.8-flash compat = %s, want %s", got, want)
		}
	}
	if !foundGemini {
		t.Fatalf("github-copilot catalog missing gemini-3.8-flash")
	}
}
