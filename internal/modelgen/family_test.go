package modelgen

// pi: packages/ai/catalog/families/{anthropic,openai,deepseek,google,kimi}.ts
// No upstream test covers the family rules; the expectations are derived by reading the TypeScript tables.

import "testing"

type familyCase struct {
	name, provider, id, api string
	// levels and compat are the thinkingLevelMap and compat the rules leave on the model (`null`: absent).
	levels, compat string
}

func TestApplyOpenAIFamilyRules(t *testing.T) {
	const gpt6Levels = `{"off":null,"minimal":null,"low":"low","medium":"medium","high":"high","xhigh":"xhigh","max":"max"}`
	for _, tc := range []familyCase{
		{"gpt-5.2 on Responses can not turn thinking off and takes xhigh", "openai", "gpt-5.2", "openai-responses", `{"off":null,"xhigh":"xhigh"}`, `null`},
		{"gpt-5.2 on Chat Completions takes xhigh only", "openai", "gpt-5.2", "openai-completions", `{"xhigh":"xhigh"}`, `null`},
		{"pre-GPT-5 models get nothing", "openai", "gpt-4o", "openai-responses", `null`, `null`},
		{"gpt-5 on Azure Responses can not turn thinking off", "azure", "gpt-5", "azure-openai-responses", `{"off":null}`, `null`},
		{"gpt-5 on Codex Responses keeps off", "openai-codex", "gpt-5", "openai-codex-responses", `null`, `null`},
		{"gpt-5.6 on Responses adds max", "openai", "gpt-5.6-sol", "openai-responses", `{"off":null,"xhigh":"xhigh","max":"max"}`, `null`},
		{"gpt-5.6 on Chat Completions adds max without off", "openrouter", "gpt-5.6-sol", "openai-completions", `{"xhigh":"xhigh","max":"max"}`, `null`},
		{"gpt-5.6 on another API gets xhigh only", "x", "gpt-5.6-sol", "anthropic-messages", `{"xhigh":"xhigh"}`, `null`},
		{"gpt-6 astra rejects none", "openai", "gpt-6-astra", "openai-responses", gpt6Levels, `null`},
		{"gpt-6.1 sol rejects none", "openai-codex", "gpt-6.1-sol", "openai-codex-responses", gpt6Levels, `null`},
		{"gpt-6.1 terra accepts none", "openai-codex", "gpt-6.1-terra", "openai-codex-responses", `{"off":"none","minimal":null,"low":"low","medium":"medium","high":"high","xhigh":"xhigh","max":"max"}`, `null`},
		{"gpt-6 sol on Chat Completions takes xhigh and max only", "openrouter", "gpt-6-sol", "openai-completions", `{"xhigh":"xhigh","max":"max"}`, `null`},
		{"gpt-5.5-pro loses off, minimal and low after the xhigh rule", "openai", "gpt-5.5-pro", "openai-responses", `{"off":null,"xhigh":"xhigh","minimal":null,"low":null}`, `null`},
		{"a gateway-prefixed gpt-5.5-pro matches by suffix", "openrouter", "openai/gpt-5.5-pro", "openai-completions", `{"xhigh":"xhigh","off":null,"minimal":null,"low":null}`, `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := fakeModel(tc.provider, tc.id, tc.api)
			applyOpenAIFamilyRules(model)
			requireJSON(t, model.Object("thinkingLevelMap"), tc.levels)
			requireJSON(t, model.Object("compat"), tc.compat)
		})
	}
}

func TestSupportsOpenAIXhigh(t *testing.T) {
	for id, want := range map[string]bool{"gpt-5.1": false, "gpt-5.2-codex": true, "gpt-5.3": true, "x/gpt-6.1-sol": true, "gpt-4.5": false, "gpt-5": false, "gpt-5.4-mini": true} {
		if got := SupportsOpenAIXhigh(id); got != want {
			t.Errorf("SupportsOpenAIXhigh(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestEnableOpenAIGrammarTools(t *testing.T) {
	for _, tc := range []struct {
		id, api, want string
	}{
		{"gpt-5", "openai-responses", `{"supportsOpenAIGrammarTools":true}`},
		{"gpt-5.5-pro", "azure-openai-responses", `{"supportsOpenAIGrammarTools":true}`},
		{"gpt-10", "openai-codex-responses", `{"supportsOpenAIGrammarTools":true}`},
		{"gpt-4o", "openai-responses", `null`},
		{"gpt-4.1", "openai-responses", `null`},
		{"o3", "openai-responses", `null`},
		{"gpt-5", "openai-completions", `null`},
	} {
		t.Run(tc.api+"/"+tc.id, func(t *testing.T) {
			model := fakeModel("p", tc.id, tc.api)
			EnableOpenAIGrammarTools(model)
			requireJSON(t, model.Object("compat"), tc.want)
		})
	}
}

func TestEnableOpenAIMidConvoSystemMessages(t *testing.T) {
	for _, tc := range []struct {
		id, api string
		proxy   bool
		want    string
	}{
		{"gpt-5.5", "openai-responses", false, `{"supportsMidConvoSystemMessages":true}`},
		{"gpt-5.5", "openai-responses", true, `{"supportsMidConvoSystemMessages":true}`},
		{"gpt-6-sol", "openai-responses", false, `{"supportsMidConvoSystemMessages":true}`},
		{"gpt-6-sol", "openai-codex-responses", true, `{"supportsMidConvoSystemMessages":true,"supportsAdditionalTools":true}`},
		{"gpt-5.4-pro", "openai-codex-responses", false, `{"supportsMidConvoSystemMessages":true}`},
		{"gpt-6-sol", "azure-openai-responses", true, `null`},
		{"gpt-6-sol", "openai-completions", true, `null`},
		{"gpt-5.3", "openai-responses", true, `null`},
	} {
		t.Run(tc.api+"/"+tc.id, func(t *testing.T) {
			model := fakeModel("p", tc.id, tc.api)
			EnableOpenAIMidConvoSystemMessages(model, tc.proxy)
			requireJSON(t, model.Object("compat"), tc.want)
		})
	}
}

func TestOpenAILongContextPricing(t *testing.T) {
	requireJSON(t, OpenAIStandardCost("gpt-6-astra"), `{"input":10,"output":50,"cacheRead":1,"cacheWrite":12.5,"tiers":[{"inputTokensAbove":272000,"input":20,"output":75,"cacheRead":2,"cacheWrite":25}]}`)
	requireJSON(t, OpenAIStandardCost("gpt-5.6-luna"), `{"input":0.2,"output":1.2,"cacheRead":0.02,"cacheWrite":0.25,"tiers":[{"inputTokensAbove":272000,"input":0.4,"output":1.8,"cacheRead":0.04,"cacheWrite":0.5}]}`)
	// The tier replaces tiers the cost already has and keeps its other members in place.
	requireJSON(t, WithOpenAILongContextPricing(O("input", 1, "output", 2, "cacheRead", 0, "cacheWrite", 0, "tiers", []any{}, "extra", true)),
		`{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"tiers":[{"inputTokensAbove":272000,"input":2,"output":3,"cacheRead":0,"cacheWrite":0}],"extra":true}`)
}

func TestClaudeMidConvoSupport(t *testing.T) {
	for id, want := range map[string]bool{
		"claude-opus-5": true, "anthropic/claude-opus-5.5": true, "~anthropic/CLAUDE-SONNET-5-5-20260723": true, "claude-haiku-5.5": true,
		"claude-fable-5-1": true, "claude-mythos-5.1-20260101": true, "claude-fable-5": false, "claude-opus-4-8": false, "claude-sonnet-5": false, "claude-opus-5.4": false,
	} {
		if got := SupportsClaudeMidConvoEffort(id); got != want {
			t.Errorf("SupportsClaudeMidConvoEffort(%q) = %v, want %v", id, got, want)
		}
	}
	// The system-message rule matches the id as given: no prefix stripping and no lowercasing.
	for id, want := range map[string]bool{
		"claude-opus-4.8": true, "claude-opus-5": true, "claude-fable-5": true, "claude-haiku-5-5-20260723": true, "claude-mythos-5-1": true,
		"anthropic/claude-opus-5": false, "Claude-Opus-5": false, "claude-opus-4-7": false, "claude-sonnet-5": false,
	} {
		if got := SupportsClaudeMidConvoSystemMessages(id); got != want {
			t.Errorf("SupportsClaudeMidConvoSystemMessages(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestEnableClaudeMidConvoEffort(t *testing.T) {
	for _, tc := range []struct{ id, levels string }{
		{"claude-opus-5", `null`},
		{"claude-opus-5-20260723", `null`},
		{"anthropic/claude-haiku-5.5-20260723", `null`},
		{"claude-haiku-5-5", `null`},
		{"claude-opus-5-5", `{"off":null}`},
		{"~anthropic/claude-sonnet-5.5", `{"off":null}`},
		{"claude-fable-5-1", `{"off":null}`},
	} {
		t.Run(tc.id, func(t *testing.T) {
			model := fakeModel("anthropic", tc.id, "anthropic-messages")
			EnableClaudeMidConvoEffort(model)
			requireJSON(t, model.Object("compat"), `{"supportsMidConvoEffort":true}`)
			requireJSON(t, model.Object("thinkingLevelMap"), tc.levels)
		})
	}
}

func TestApplyClaudeFamilyRules(t *testing.T) {
	for _, tc := range []familyCase{
		{"opus 4.6 has max only and adaptive thinking", "anthropic", "claude-opus-4-6", "anthropic-messages", `{"max":"max"}`, `{"forceAdaptiveThinking":true}`},
		{"opus 4.7 adds xhigh and drops the temperature", "anthropic", "claude-opus-4-7", "anthropic-messages", `{"xhigh":"xhigh","max":"max"}`, `{"forceAdaptiveThinking":true,"supportsTemperature":false}`},
		{"a dotted prefixed id matches", "openrouter", "anthropic/claude-opus-4.7", "anthropic-messages", `{"xhigh":"xhigh","max":"max"}`, `{"forceAdaptiveThinking":true,"supportsTemperature":false}`},
		{"sonnet 5.5 drops the temperature", "anthropic", "claude-sonnet-5-5", "anthropic-messages", `{"xhigh":"xhigh","max":"max"}`, `{"forceAdaptiveThinking":true,"supportsTemperature":false}`},
		{"sonnet 5 keeps the temperature", "anthropic", "claude-sonnet-5", "anthropic-messages", `{"xhigh":"xhigh","max":"max"}`, `{"forceAdaptiveThinking":true}`},
		{"fable 5 can not turn thinking off", "anthropic", "claude-fable-5-1", "anthropic-messages", `{"off":null,"xhigh":"xhigh","max":"max"}`, `{"forceAdaptiveThinking":true}`},
		{"mythos 5 is adaptive without effort levels", "anthropic", "claude-mythos-5", "anthropic-messages", `null`, `{"forceAdaptiveThinking":true}`},
		{"the latest alias is adaptive", "openrouter", "~anthropic/claude-opus-latest", "anthropic-messages", `null`, `{"forceAdaptiveThinking":true}`},
		{"an alias with a digit is not an alias", "openrouter", "~anthropic/claude-opus-4-latest", "anthropic-messages", `null`, `null`},
		{"effort levels apply on any API but compat only on Messages", "openrouter", "claude-opus-4-8", "openai-completions", `{"xhigh":"xhigh","max":"max"}`, `null`},
		{"older models get nothing", "anthropic", "claude-haiku-4-5", "anthropic-messages", `null`, `null`},
		{"the level rules are case sensitive but the temperature rule is not", "anthropic", "Claude-Opus-5", "anthropic-messages", `null`, `{"supportsTemperature":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := fakeModel(tc.provider, tc.id, tc.api)
			applyClaudeFamilyRules(model)
			requireJSON(t, model.Object("thinkingLevelMap"), tc.levels)
			requireJSON(t, model.Object("compat"), tc.compat)
		})
	}
}

func TestClaude55ThinkingLevels(t *testing.T) {
	requireJSON(t, Claude55ThinkingLevels("claude-opus-5.5"), `{"off":null,"minimal":null,"low":"low","medium":"medium","high":"high","xhigh":"xhigh","max":"max"}`)
	requireJSON(t, Claude55ThinkingLevels("claude-haiku-5.5"), `{"minimal":null,"low":"low","medium":"medium","high":"high","xhigh":"xhigh","max":"max"}`)
}

func TestApplyDeepSeekRules(t *testing.T) {
	const v4Levels = `{"minimal":null,"low":null,"medium":null,"high":"high","max":"max"}`
	const deepSeekCompat = `{"requiresReasoningContentOnAssistantMessages":true,"thinkingFormat":"deepseek"}`
	for _, tc := range []familyCase{
		{"deepseek's own pro", "deepseek", "deepseek-v4-pro", "openai-completions", v4Levels, deepSeekCompat},
		{"deepseek's own flash adds low", "deepseek", "deepseek-v4-flash", "openai-completions", `{"minimal":null,"low":"low","medium":null,"high":"high","max":"max"}`, deepSeekCompat},
		{"opencode flash adds low but only replays reasoning", "opencode", "deepseek-v4-flash", "openai-completions", `{"minimal":null,"low":"low","medium":null,"high":"high","max":"max"}`, `{"requiresReasoningContentOnAssistantMessages":true}`},
		{"opencode-go flash takes DeepSeek's thinking format", "opencode-go", "deepseek-v4-flash", "openai-completions", `{"minimal":null,"low":"low","medium":null,"high":"high","max":"max"}`, deepSeekCompat},
		{"openrouter takes native efforts", "openrouter", "deepseek/deepseek-v4-pro", "openai-completions", `{"minimal":null,"low":null,"medium":null,"high":"high","max":null,"xhigh":"xhigh"}`, `{"requiresReasoningContentOnAssistantMessages":true}`},
		{"azure rejects max", "azure", "deepseek-v4-pro", "openai-completions", `{"minimal":null,"low":"low","medium":"medium","high":"high","xhigh":null,"max":null}`, deepSeekCompat},
		{"qwen token plan keeps its own format", "qwen-token-plan", "deepseek-v4-pro", "openai-completions", v4Levels, `null`},
		{"another host's flash gets the pro levels", "together", "deepseek-v4-flash", "openai-completions", v4Levels, deepSeekCompat},
		{"other APIs are not V4", "deepseek", "deepseek-v4-pro", "openai-responses", `null`, `null`},
		{"other DeepSeek models are not V4", "deepseek", "deepseek-chat", "openai-completions", `null`, `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := fakeModel(tc.provider, tc.id, tc.api)
			applyDeepSeekCompatRules(model)
			applyDeepSeekFamilyRules(model)
			requireJSON(t, model.Object("thinkingLevelMap"), tc.levels)
			requireJSON(t, model.Object("compat"), tc.compat)
		})
	}
	t.Run("levels the host reported are kept", func(t *testing.T) {
		model := fakeModel("deepseek", "deepseek-v4-pro", "openai-completions")
		model.Set("thinkingLevelMap", O("high", "xhigh"))
		applyDeepSeekFamilyRules(model)
		requireJSON(t, model.Object("thinkingLevelMap"), `{"high":"xhigh"}`)
	})
	t.Run("the flash map is the pro map with low", func(t *testing.T) {
		requireJSON(t, DeepSeekV4FlashThinkingLevelMap(), `{"minimal":null,"low":"low","medium":null,"high":"high","max":"max"}`)
		requireJSON(t, DeepSeekV4ThinkingLevelMap(), v4Levels)
	})
}

func TestIsKimiK3FamilyID(t *testing.T) {
	for id, want := range map[string]bool{
		"kimi-k3": true, "kimi-k3.1-pro": true, "kimi-k3.1-pro-highspeed": true, "kimi-k3-0905": true, "kimi-k3.12": true,
		"kimi-k31": false, "kimi-k2": false, "kimi-k3x": false, "kimi-k3.-pro": false, "moonshotai/kimi-k3": false, "Kimi-K3": false,
	} {
		if got := IsKimiK3FamilyID(id); got != want {
			t.Errorf("IsKimiK3FamilyID(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestKimiTables(t *testing.T) {
	requireJSON(t, KimiOpenAICompat(), `{"supportsStore":false,"supportsDeveloperRole":false,"supportsReasoningEffort":false,"maxTokensField":"max_tokens","supportsStrictMode":false,"thinkingFormat":"deepseek"}`)
	// K3 overrides keep the base key positions and append the new key.
	requireJSON(t, KimiK3OpenAICompat(), `{"supportsStore":false,"supportsDeveloperRole":false,"supportsReasoningEffort":true,"maxTokensField":"max_tokens","supportsStrictMode":false,"thinkingFormat":"openai","requiresReasoningContentOnAssistantMessages":true}`)
	requireJSON(t, KimiK3Cost(), `{"input":3,"output":15,"cacheRead":0.3,"cacheWrite":0}`)
	if KimiK3MaxTokens != 131072 {
		t.Fatalf("KimiK3MaxTokens = %d", KimiK3MaxTokens)
	}
}

func TestGoogleThinkingLevels(t *testing.T) {
	effort := []ModelsDevReasoningOption{{Type: "effort", Values: []*string{new("low"), new("high")}}}
	toggle := []ModelsDevReasoningOption{{Type: "toggle"}}
	for _, tc := range []struct {
		name, id string
		options  []ModelsDevReasoningOption
		want     string
	}{
		{"models.dev efforts", "gemini-3-pro", effort, `{"off":null,"minimal":null,"low":"low","medium":null,"high":"high","xhigh":null,"max":null}`},
		{"efforts win over the Gemma 4 fallback", "gemma-4-27b", effort, `{"off":null,"minimal":null,"low":"low","medium":null,"high":"high","xhigh":null,"max":null}`},
		{"Gemma 4 fallback", "gemma-4-27b", nil, `{"off":null,"minimal":"MINIMAL","low":null,"medium":null,"high":"HIGH"}`},
		{"Gemma 4 fallback ignores case and the dash", "models/Gemma4-it", toggle, `{"off":null,"minimal":"MINIMAL","low":null,"medium":null,"high":"HIGH"}`},
		{"other Gemma models", "gemma-3-27b", nil, `null`},
		{"Gemini without efforts", "gemini-2.5-flash", toggle, `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireJSON(t, GoogleThinkingLevels(tc.id, tc.options), tc.want)
		})
	}
}
