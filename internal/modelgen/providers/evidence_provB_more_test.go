package providers

// Behavioral evidence, second half: see evidence_provB_test.go.

import (
	"strings"
	"testing"

	. "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: same dot import as the provider files
)

const (
	provBZen  = "https://opencode.ai/zen/v1"
	provBGoV1 = "https://opencode.ai/zen/go/v1"
)

func provBOpenCodeAPIs(protocol, baseURL, aisdk string) string {
	sdk := ""
	if aisdk != "" {
		sdk = `,"sdk":{"aisdk":"` + aisdk + `"}`
	}
	return `{"` + protocol + `":{"base_url":"` + baseURL + `"` + sdk + `}}`
}

func TestProvBOpencode(t *testing.T) {
	// pi: providers/opencode.ts opencodeApi + opencodeModels + finalizeOpencodeModel
	defaults := `{"supportsStore":false,"supportsDeveloperRole":false,"supportsStrictMode":true`
	sources := &CatalogSources{ModelsDev: provBModelsDev(t, map[string][]provBSpec{"opencode": {
		// Native protocols come before Chat Completions when models.dev lists several.
		{ID: "alpha", API: `{"chat-completions":{"base_url":"` + provBZen + `"},"responses":{"base_url":"` + provBZen + `"}}`},
		// The Anthropic SDK appends /v1/messages itself.
		{ID: "beta", API: provBOpenCodeAPIs("messages", provBZen, "")},
		{ID: "claude-sonnet-4-5", Context: 1000000, API: provBOpenCodeAPIs("messages", provBZen, "")},
		{ID: "claude-opus-4-6", Context: 200000, API: provBOpenCodeAPIs("messages", provBZen, "")},
		{ID: "gpt-5.4", Context: 1050000, Max: 128000, API: provBOpenCodeAPIs("responses", provBZen, "")},
		{ID: "gamma", API: provBOpenCodeAPIs("chat-completions", provBZen, "@ai-sdk/alibaba")},
		{ID: "delta", API: provBOpenCodeAPIs("chat-completions", provBZen, "@ai-sdk/openai-compatible")},
		// OpenAI-compatible endpoints reject long prompt-cache retention for these.
		{ID: "deepseek-v4-flash", API: provBOpenCodeAPIs("chat-completions", provBZen, "")},
		{ID: "deepseek-v4.1-flash", API: provBOpenCodeAPIs("chat-completions", provBZen, "")},
		{ID: "deepseek-v4-pro", API: provBOpenCodeAPIs("chat-completions", provBZen, "")},
		{ID: "kimi-k2.5", API: provBOpenCodeAPIs("chat-completions", provBZen, "")},
		{ID: "minimax-m2.7", API: provBOpenCodeAPIs("chat-completions", provBZen, "")},
		{ID: "kimi-k2.6", API: provBOpenCodeAPIs("chat-completions", provBZen, "")},
		{ID: "grok-build-0.1", Reasoning: `"supported":true`, API: provBOpenCodeAPIs("chat-completions", provBZen, "")},
		{ID: "qwen3.8-flash", API: provBOpenCodeAPIs("messages", provBZen, "")},
		{ID: "old", Status: "deprecated", API: provBOpenCodeAPIs("chat-completions", provBZen, "")},
		{ID: "gpt-5.3-codex-spark", API: provBOpenCodeAPIs("responses", provBZen, "")},
		{ID: "orphan", API: provBOpenCodeAPIs("embeddings", provBZen, "")},
	}})}
	catalog, err := provBBuild(t, "opencode", sources, false)
	if err != nil {
		t.Fatal(err)
	}
	models := provBModelsOf(catalog)
	for _, id := range []string{"old", "gpt-5.3-codex-spark", "orphan"} {
		if models[id] != nil {
			t.Errorf("%s must be skipped", id)
		}
	}
	noLong := defaults + `,"maxTokensField":"max_tokens","supportsLongCacheRetention":false}`
	// DeepSeek V4 needs its reasoning content replayed on every host.
	noLongDeepSeek := defaults + `,"maxTokensField":"max_tokens","supportsLongCacheRetention":false,"requiresReasoningContentOnAssistantMessages":true}`
	provBCheck(t, models, []provBWant{
		{"alpha", "api", `"openai-responses"`},
		{"alpha", "compat", `{"sessionAffinityFormat":"openai-nosession"}`},
		{"beta", "api", `"anthropic-messages"`},
		{"beta", "baseUrl", `"https://opencode.ai/zen"`},
		{"claude-sonnet-4-5", "contextWindow", "200000"},
		{"claude-opus-4-6", "contextWindow", "1000000"},
		{"gpt-5.4", "contextWindow", "272000"},
		{"gpt-5.4", "maxTokens", "128000"},
		{"gamma", "compat", defaults + `,"cacheControlFormat":"anthropic","maxTokensField":"max_tokens"}`},
		{"delta", "compat", defaults + `,"maxTokensField":"max_tokens"}`},
		{"delta", "baseUrl", `"` + provBZen + `"`},
		{"deepseek-v4-flash", "compat", noLongDeepSeek},
		{"deepseek-v4.1-flash", "compat", noLongDeepSeek},
		{"deepseek-v4-pro", "compat", noLongDeepSeek},
		{"kimi-k2.5", "compat", noLong},
		{"minimax-m2.7", "compat", noLong},
		{"kimi-k2.6", "compat", defaults + `,"thinkingFormat":"deepseek","supportsReasoningEffort":false,"maxTokensField":"max_tokens","supportsLongCacheRetention":false}`},
		{"kimi-k2.6", "thinkingLevelMap", "undefined"},
		{"grok-build-0.1", "compat", defaults + `,"supportsReasoningEffort":false,"maxTokensField":"max_tokens"}`},
		{"grok-build-0.1", "thinkingLevelMap", `{"off":null,"minimal":null,"low":null,"medium":null}`},
		// OpenCode Qwen 3.8 Flash emits and accepts thinking blocks with empty signatures.
		{"qwen3.8-flash", "compat", `{"allowEmptySignature":true}`},
	})
	// Strict generation fails on a model served only on an unsupported protocol.
	_, err = provBBuild(t, "opencode", sources, true)
	if err == nil || err.Error() != "models.dev lists no supported API for opencode/orphan (embeddings)" {
		t.Errorf("strict error = %v", err)
	}
	// OpenCode Zen's Jev classifiers.
	if len(catalog.Classifier) != 2 {
		t.Fatalf("classifiers = %d", len(catalog.Classifier))
	}
	want := []string{
		`{"type":"classifier","id":"jev-1.13","name":"Jev 1.13","api":"typesafe-system-one","provider":"opencode","baseUrl":"https://opencode.ai/inference/systemone/v1","input":["text"],"cost":{"input":0.042,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":32000}`,
		`{"type":"classifier","id":"jev-1.13-free","name":"Jev 1.13 Free","api":"typesafe-system-one","provider":"opencode","baseUrl":"https://opencode.ai/inference/systemone/v1","input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":32000}`,
	}
	for i, classifier := range catalog.Classifier {
		if got := string(Stringify(classifier, "")); got != want[i] {
			t.Errorf("classifier %d = %s, want %s", i, got, want[i])
		}
	}
}

func TestProvBOpencodeGo(t *testing.T) {
	// pi: providers/opencode-go.ts (opencodeModels and finalizeOpencodeModel of providers/opencode.ts with OpenCode Go's rules)
	defaults := `{"supportsStore":false,"supportsDeveloperRole":false,"supportsStrictMode":true`
	sources := &CatalogSources{ModelsDev: provBModelsDev(t, map[string][]provBSpec{"opencode-go": {
		{ID: "kimi-k2.6", API: provBOpenCodeAPIs("chat-completions", provBGoV1, "")},
		// Only "opencode:deepseek-v4-flash" is rejected; the Go endpoint takes long cache retention.
		{ID: "deepseek-v4-flash", API: provBOpenCodeAPIs("chat-completions", provBGoV1, "")},
		// models.dev reports these as @ai-sdk/anthropic, but Go serves them through /openai/v1/chat/completions.
		{ID: "qwen3.5-plus", API: provBOpenCodeAPIs("messages", "https://opencode.ai/zen/go/anthropic", "")},
		{ID: "qwen3.6-plus", API: provBOpenCodeAPIs("messages", "https://opencode.ai/zen/go/anthropic", "")},
		{ID: "glm-5.2", Reasoning: `"supported":true,"toggle":true`, API: provBOpenCodeAPIs("chat-completions", provBGoV1, "")},
		{ID: "deepseek-v4.1-flash", Reasoning: `"supported":true,"effort":["high","max"]`, API: provBOpenCodeAPIs("chat-completions", provBGoV1, "")},
		{ID: "grok-build-0.1", Reasoning: `"supported":true`, API: provBOpenCodeAPIs("chat-completions", provBGoV1, "")},
	}})}
	catalog, err := provBBuild(t, "opencode-go", sources, true)
	if err != nil {
		t.Fatal(err)
	}
	models := provBModelsOf(catalog)
	provBCheck(t, models, []provBWant{
		{"kimi-k2.6", "compat", defaults + `,"thinkingFormat":"deepseek","supportsReasoningEffort":false,"maxTokensField":"max_tokens","supportsLongCacheRetention":false}`},
		// OpenCode Go exposes Kimi K2.6 thinking as on/off.
		{"kimi-k2.6", "thinkingLevelMap", `{"minimal":null,"low":null,"medium":null}`},
		{"deepseek-v4-flash", "compat", defaults + `,"maxTokensField":"max_tokens","requiresReasoningContentOnAssistantMessages":true,"thinkingFormat":"deepseek"}`},
		{"qwen3.5-plus", "api", `"openai-completions"`},
		{"qwen3.5-plus", "baseUrl", `"https://opencode.ai/zen/go/openai/v1"`},
		{"qwen3.5-plus", "compat", defaults + `,"thinkingFormat":"qwen","maxTokensField":"max_tokens"}`},
		{"qwen3.6-plus", "baseUrl", `"https://opencode.ai/zen/go/openai/v1"`},
		{"glm-5.2", "thinkingLevelMap", `{"off":null,"minimal":null,"low":null,"medium":null,"high":"high","max":"max"}`},
		{"deepseek-v4.1-flash", "thinkingLevelMap", `{"off":null,"minimal":null,"low":null,"medium":null,"high":"high","xhigh":null,"max":"max"}`},
		// grok-build-0.1 is only special on OpenCode Zen.
		{"grok-build-0.1", "compat", defaults + `,"maxTokensField":"max_tokens"}`},
		{"grok-build-0.1", "thinkingLevelMap", "undefined"},
	})
	if len(catalog.Classifier) != 0 {
		t.Errorf("OpenCode Go has no classifiers: %d", len(catalog.Classifier))
	}
}

func provBOpenRouterModel(id string) *Obj {
	return O(
		"id", id,
		"name", id,
		"api", "openai-completions",
		"provider", "openrouter",
		"baseUrl", "https://openrouter.ai/api/v1",
		"reasoning", false,
		"input", Strings("text"),
		"cost", O("input", 1, "output", 2, "cacheRead", 0, "cacheWrite", 0),
		"contextWindow", 1000,
		"maxTokens", 100,
	)
}

func TestProvBOpenRouter(t *testing.T) {
	// pi: providers/openrouter.ts
	sources := &CatalogSources{OpenRouter: OpenRouterCatalog{
		Chat: []*Obj{
			provBOpenRouterModel("anthropic/claude-x"),
			provBOpenRouterModel("~anthropic/claude-latest"),
			provBOpenRouterModel("openai/gpt-x"),
			provBOpenRouterModel("meta/llama-x"),
			provBOpenRouterModel("moonshotai/kimi-k2.5"),
			provBOpenRouterModel("moonshotai/kimi-k2.6"),
			provBOpenRouterModel("moonshotai/kimi-k3.1-pro"),
			provBOpenRouterModel("~moonshotai/kimi-latest"),
			provBOpenRouterModel("z-ai/glm-5"),
			provBOpenRouterModel("z-ai/glm-5.2"),
			provBOpenRouterModel("inception/mercury-2"),
		},
		Images:      []*Obj{O("type", "image", "id", "img", "provider", "openrouter")},
		Classifiers: []*Obj{O("type", "classifier", "id", "cls", "provider", "openrouter")},
	}}
	catalog, err := provBBuild(t, "openrouter", sources, true)
	if err != nil {
		t.Fatal(err)
	}
	models := provBModelsOf(catalog)
	provBIDsEqual(t, provBIDs(catalog), "anthropic/claude-x", "~anthropic/claude-latest", "openai/gpt-x", "meta/llama-x", "moonshotai/kimi-k2.5",
		"moonshotai/kimi-k2.6", "moonshotai/kimi-k3.1-pro", "~moonshotai/kimi-latest", "z-ai/glm-5", "z-ai/glm-5.2", "inception/mercury-2", "auto", "openrouter/fusion")
	tail := `"thinkingFormat":"openrouter","supportsStrictMode":true`
	provBCheck(t, models, []provBWant{
		// Developer messages pass through to Anthropic and OpenAI models only; Anthropic ones take cache_control.
		{"anthropic/claude-x", "compat", `{` + tail + `,"cacheControlFormat":"anthropic","sendSessionAffinityHeaders":true}`},
		{"~anthropic/claude-latest", "compat", `{"supportsDeveloperRole":false,` + tail + `,"cacheControlFormat":"anthropic","sendSessionAffinityHeaders":true}`},
		{"openai/gpt-x", "compat", `{` + tail + `,"sendSessionAffinityHeaders":true}`},
		{"meta/llama-x", "compat", `{"supportsDeveloperRole":false,` + tail + `,"sendSessionAffinityHeaders":true}`},
		// Corrections.
		{"moonshotai/kimi-k2.5", "cost", `{"input":0.41,"output":2.06,"cacheRead":0.07,"cacheWrite":0}`},
		{"moonshotai/kimi-k2.5", "maxTokens", "4096"},
		{"moonshotai/kimi-k2.6", "compat", `{"supportsDeveloperRole":false,` + tail + `,"sendSessionAffinityHeaders":true,"requiresReasoningContentOnAssistantMessages":true}`},
		{"moonshotai/kimi-k3.1-pro", "maxTokens", "131072"},
		{"~moonshotai/kimi-latest", "maxTokens", "131072"},
		{"z-ai/glm-5", "cost", `{"input":0.6,"output":1.9,"cacheRead":0.119,"cacheWrite":0}`},
		{"z-ai/glm-5.2", "thinkingLevelMap", `{"xhigh":"xhigh"}`},
		{"inception/mercury-2", "thinkingLevelMap", `{"off":null}`},
		// Router aliases: their price is unknown.
		{"auto", "name", `"Auto"`},
		{"auto", "contextWindow", "2000000"},
		{"auto", "maxTokens", "30000"},
		{"auto", "input", `["text","image"]`},
		{"auto", "reasoning", "true"},
		{"auto", "cost", `{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}`},
		{"openrouter/fusion", "name", `"OpenRouter: Fusion"`},
		{"openrouter/fusion", "contextWindow", "1000000"},
		{"openrouter/fusion", "maxTokens", "30000"},
		{"openrouter/fusion", "input", `["text"]`},
	})
	if len(catalog.Image) != 1 || catalog.Image[0].Str("id") != "img" || len(catalog.Classifier) != 1 || catalog.Classifier[0].Str("id") != "cls" {
		t.Errorf("image and classifier models are the upstream ones: %d, %d", len(catalog.Image), len(catalog.Classifier))
	}
}

func TestProvBQwenTokenPlans(t *testing.T) {
	// pi: providers/qwen-token-plan.ts, qwen-token-plan-cn.ts, qwen-token-plan-individual.ts
	effort := `"supported":true,"effort":["low","high"]`
	intl := []provBSpec{
		{ID: "qwen3.7-plus", Reasoning: effort},
		{ID: "qwen3.7-max", Reasoning: `"supported":true,"toggle":true`},
		{ID: "glm-5", Reasoning: `"supported":true,"toggle":true`},
		{ID: "glm-5.1", Reasoning: `"supported":true,"toggle":true`},
		{ID: "glm-5.2", Reasoning: `"supported":true,"toggle":true`},
		{ID: "qwen3.8-max-preview"},
		{ID: "intl-only"},
	}
	cn := []provBSpec{{ID: "cn-only"}, {ID: "qwen3.8-max-preview"}}
	sources := &CatalogSources{ModelsDev: provBModelsDev(t, map[string][]provBSpec{"alibaba-token-plan": intl, "alibaba-token-plan-cn": cn})}
	withLevels := `{"supportsStrictMode":true,"thinkingFormat":"qwen","supportsDeveloperRole":false,"supportsStore":false,"supportsReasoningEffort":true}`
	without := `{"supportsStrictMode":true,"thinkingFormat":"qwen","supportsDeveloperRole":false,"supportsStore":false,"supportsReasoningEffort":false}`
	fallback := `{"minimal":null,"low":null,"medium":null,"high":"high","xhigh":null,"max":"max"}`

	catalog, err := provBBuild(t, "qwen-token-plan", sources, true)
	if err != nil {
		t.Fatal(err)
	}
	provBIDsEqual(t, provBIDs(catalog), "qwen3.7-plus", "qwen3.7-max", "glm-5", "glm-5.1", "glm-5.2", "intl-only")
	provBCheck(t, provBModelsOf(catalog), []provBWant{
		{"qwen3.7-plus", "baseUrl", `"https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1"`},
		{"qwen3.7-plus", "compat", withLevels},
		{"qwen3.7-plus", "thinkingLevelMap", `{"off":null,"minimal":null,"low":"low","medium":null,"high":"high","xhigh":null,"max":null}`},
		{"qwen3.7-max", "compat", without},
		{"qwen3.7-max", "thinkingLevelMap", "undefined"},
		// glm-5 and glm-5.1 accept reasoning_effort but models.dev lists no verified values.
		{"glm-5", "compat", withLevels},
		{"glm-5", "thinkingLevelMap", fallback},
		{"glm-5.1", "thinkingLevelMap", fallback},
		{"glm-5.2", "compat", without},
		{"glm-5.2", "thinkingLevelMap", "undefined"},
	})

	cnCatalog, err := provBBuild(t, "qwen-token-plan-cn", sources, true)
	if err != nil {
		t.Fatal(err)
	}
	provBIDsEqual(t, provBIDs(cnCatalog), "cn-only")
	provBCheck(t, provBModelsOf(cnCatalog), []provBWant{
		{"cn-only", "baseUrl", `"https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1"`},
		{"cn-only", "compat", without},
	})

	// The individual plan keeps its allowlist of the international models, and strict generation requires exactly that list.
	all := []string{"deepseek-v4-flash-0731", "deepseek-v4-pro", "deepseek-v4-pro-0813", "glm-5.2", "qwen3.6-flash", "qwen3.7-max", "qwen3.7-plus", "qwen3.8-flash", "qwen3.8-max"}
	var allowed []provBSpec
	for _, id := range all {
		allowed = append(allowed, provBSpec{ID: id})
	}
	individual := &CatalogSources{ModelsDev: provBModelsDev(t, map[string][]provBSpec{"alibaba-token-plan": append(append([]provBSpec{}, allowed...), provBSpec{ID: "qwen3.8-max-preview"}, provBSpec{ID: "intl-only"})})}
	individualCatalog, err := provBBuild(t, "qwen-token-plan-individual", individual, true)
	if err != nil {
		t.Fatal(err)
	}
	provBIDsEqual(t, provBIDs(individualCatalog), all...)
	provBCheck(t, provBModelsOf(individualCatalog), []provBWant{
		{"qwen3.7-max", "baseUrl", `"https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1"`},
		{"qwen3.7-max", "compat", without},
	})
	missing := &CatalogSources{ModelsDev: provBModelsDev(t, map[string][]provBSpec{"alibaba-token-plan": allowed[1:]})}
	if _, err := provBBuild(t, "qwen-token-plan-individual", missing, true); err == nil || !strings.HasPrefix(err.Error(), "qwen-token-plan-individual model IDs do not match") {
		t.Errorf("strict individual error = %v", err)
	}
	if _, err := provBBuild(t, "qwen-token-plan-individual", missing, false); err != nil {
		t.Errorf("non-strict individual error = %v", err)
	}
}

func TestProvBTogether(t *testing.T) {
	// pi: providers/together.ts
	base := `"supportsStore":false,"supportsDeveloperRole":false`
	compat := func(effort, format string) string {
		return `{` + base + `,"supportsReasoningEffort":` + effort + `,"maxTokensField":"max_tokens","thinkingFormat":"` + format + `","supportsLongCacheRetention":false,"supportsStrictMode":false}`
	}
	toggle := `"supported":true,"toggle":true`
	sources := &CatalogSources{ModelsDev: provBModelsDev(t, map[string][]provBSpec{"togetherai": {
		{ID: "meta-llama/plain"},
		{ID: "openai/gpt-oss-20b", Reasoning: toggle},
		{ID: "openai/gpt-oss-120b", Reasoning: toggle},
		{ID: "deepseek-ai/DeepSeek-V4-Pro-0813", Reasoning: toggle},
		{ID: "zai-org/GLM-5.2", Reasoning: toggle},
		{ID: "retired/model", Status: "deprecated"},
	}})}
	catalog, err := provBBuild(t, "together", sources, true)
	if err != nil {
		t.Fatal(err)
	}
	provBIDsEqual(t, provBIDs(catalog), "meta-llama/plain", "openai/gpt-oss-20b", "openai/gpt-oss-120b", "deepseek-ai/DeepSeek-V4-Pro-0813", "zai-org/GLM-5.2")
	oss := `{"off":null,"minimal":null}`
	provBCheck(t, provBModelsOf(catalog), []provBWant{
		{"meta-llama/plain", "baseUrl", `"https://api.together.ai/v1"`},
		{"meta-llama/plain", "compat", compat("false", "together")},
		{"meta-llama/plain", "thinkingLevelMap", "undefined"},
		{"openai/gpt-oss-20b", "compat", compat("true", "openai")},
		{"openai/gpt-oss-20b", "thinkingLevelMap", oss},
		{"openai/gpt-oss-120b", "compat", compat("true", "openai")},
		{"openai/gpt-oss-120b", "thinkingLevelMap", oss},
		{"deepseek-ai/DeepSeek-V4-Pro-0813", "compat", compat("true", "together")},
		{"deepseek-ai/DeepSeek-V4-Pro-0813", "thinkingLevelMap", `{"minimal":null,"low":null,"medium":null,"high":"high","xhigh":null}`},
		{"zai-org/GLM-5.2", "compat", compat("false", "together")},
		{"zai-org/GLM-5.2", "thinkingLevelMap", `{"minimal":null,"low":null,"medium":null}`},
	})
}

func TestProvBTypesafe(t *testing.T) {
	// pi: providers/typesafe.ts
	decision := func(limit float64) *CatalogSources {
		return &CatalogSources{ModelsDevDecisions: map[string]ModelsDevDecisionMetadata{
			"typesafe/jev-latest": {ID: "typesafe/jev-latest", Type: "decision", Name: "Typesafe Jev", ContextLimit: limit},
		}}
	}
	for limit, window := range map[float64]string{0: "64000", 50000: "50000"} {
		catalog, err := provBBuild(t, "typesafe", decision(limit), true)
		if err != nil {
			t.Fatal(err)
		}
		if len(catalog.Chat) != 0 || len(catalog.Classifier) != 1 {
			t.Fatalf("typesafe has one classifier: %d chat, %d classifier", len(catalog.Chat), len(catalog.Classifier))
		}
		want := `{"type":"classifier","id":"jev-latest","name":"Typesafe Jev","api":"typesafe-system-one","provider":"typesafe","baseUrl":"https://api.typesafe.ai/v1/","input":["text","image"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":` + window + `,"compat":{"imageInput":"assets","sendImageOptInHeader":true}}`
		if got := string(Stringify(catalog.Classifier[0], "")); got != want {
			t.Errorf("limit %v: classifier = %s, want %s", limit, got, want)
		}
	}
	// models.dev did not return a decision model: none, or an error in strict generation.
	for name, sources := range map[string]*CatalogSources{
		"absent":    {ModelsDevDecisions: map[string]ModelsDevDecisionMetadata{}},
		"wrongType": {ModelsDevDecisions: map[string]ModelsDevDecisionMetadata{"typesafe/jev-latest": {ID: "typesafe/jev-latest", Type: "chat", Name: "Jev"}}},
	} {
		catalog, err := provBBuild(t, "typesafe", sources, false)
		if err != nil || len(catalog.Classifier) != 0 {
			t.Errorf("%s: classifiers = %d, err = %v", name, len(catalog.Classifier), err)
		}
		if _, err := provBBuild(t, "typesafe", sources, true); err == nil || err.Error() != "models.dev did not return decision model typesafe/jev-latest" {
			t.Errorf("%s: strict error = %v", name, err)
		}
	}
}

func TestProvBXai(t *testing.T) {
	// pi: providers/xai.ts
	sources := &CatalogSources{ModelsDev: provBModelsDev(t, map[string][]provBSpec{"xai": {
		{ID: "grok-3"},
		{ID: "grok-3-fast"},
		{ID: "grok-4.20-0309-non-reasoning"},
		{ID: "grok-4.20-0309-reasoning"},
		{ID: "grok-build-0.1"},
		{ID: "grok-code-fast-1"},
		// No verified effort options: "none"/"minimal" are never sent.
		{ID: "grok-4", Reasoning: `"supported":true`},
		{ID: "grok-4-toggle", Reasoning: `"supported":true,"toggle":true`},
		{ID: "grok-plain"},
		// Verified effort options win.
		{ID: "grok-4-fast", Reasoning: `"supported":true,"effort":["none","minimal","low","high"]`},
	}})}
	catalog, err := provBBuild(t, "xai", sources, true)
	if err != nil {
		t.Fatal(err)
	}
	provBIDsEqual(t, provBIDs(catalog), "grok-4", "grok-4-toggle", "grok-plain", "grok-4-fast")
	unverified := `{"off":null,"minimal":null}`
	provBCheck(t, provBModelsOf(catalog), []provBWant{
		{"grok-4", "api", `"openai-responses"`},
		{"grok-4", "baseUrl", `"https://api.x.ai/v1"`},
		{"grok-4", "compat", `{"supportsLongCacheRetention":false}`},
		{"grok-4", "thinkingLevelMap", unverified},
		{"grok-4-toggle", "thinkingLevelMap", unverified},
		{"grok-plain", "thinkingLevelMap", unverified},
		{"grok-4-fast", "thinkingLevelMap", `{"off":"none","minimal":"minimal","low":"low","medium":null,"high":"high","xhigh":null,"max":null}`},
	})
}

func TestProvBXiaomi(t *testing.T) {
	// pi: providers/xiaomi.ts, xiaomi-token-plan-ams.ts, xiaomi-token-plan-cn.ts, xiaomi-token-plan-sgp.ts
	specs := []provBSpec{
		{ID: "mimo-reasoning", Reasoning: `"supported":true,"effort":["low","high"]`},
		{ID: "mimo-plain"},
		{ID: "mimo-old", Status: "deprecated"},
	}
	sources := &CatalogSources{ModelsDev: provBModelsDev(t, map[string][]provBSpec{
		"xiaomi": specs, "xiaomi-token-plan-ams": specs, "xiaomi-token-plan-cn": specs, "xiaomi-token-plan-sgp": specs,
	})}
	compat := `{"supportsStrictMode":true,"supportsReasoningEffort":false,"requiresReasoningContentOnAssistantMessages":true,"thinkingFormat":"deepseek"}`
	for id, baseURL := range map[string]string{
		"xiaomi":                "https://api.xiaomimimo.com/v1",
		"xiaomi-token-plan-ams": "https://token-plan-ams.xiaomimimo.com/v1",
		"xiaomi-token-plan-cn":  "https://token-plan-cn.xiaomimimo.com/v1",
		"xiaomi-token-plan-sgp": "https://token-plan-sgp.xiaomimimo.com/v1",
	} {
		catalog, err := provBBuild(t, id, sources, true)
		if err != nil {
			t.Fatal(err)
		}
		provBIDsEqual(t, provBIDs(catalog), "mimo-reasoning", "mimo-plain")
		provBCheck(t, provBModelsOf(catalog), []provBWant{
			{"mimo-reasoning", "baseUrl", `"` + baseURL + `"`},
			{"mimo-plain", "baseUrl", `"` + baseURL + `"`},
			{"mimo-reasoning", "compat", compat},
			{"mimo-plain", "compat", compat},
			// Only off and high are exposed, whatever effort models.dev lists.
			{"mimo-reasoning", "thinkingLevelMap", `{"off":"off","minimal":null,"low":null,"medium":null,"high":"high","xhigh":null,"max":null}`},
			{"mimo-plain", "thinkingLevelMap", "undefined"},
		})
	}
}

func TestProvBZai(t *testing.T) {
	// pi: providers/zai.ts, zai-coding-cn.ts
	plan := `{"text":{"input":{"price":1},"output":{"price":2}}}`
	payg := `{"text":{"input":{"price":5,"cache":{"read":0.5}},"output":{"price":10}}}`
	effort := `"supported":true,"effort":["high"]`
	planModels := []provBSpec{
		{ID: "glm-5.2", Reasoning: effort, Cost: plan},
		{ID: "glm-5.2-highspeed", Reasoning: effort, Cost: plan},
		{ID: "glm-5.1", Reasoning: effort, Cost: plan},
		{ID: "glm-4.5", Cost: plan},
		{ID: "glm-4.5-air", Cost: plan},
		{ID: "glm-4.5-flash", Cost: plan},
		{ID: "glm-4.5v", Cost: plan},
		{ID: "glm-4.7", Reasoning: `"supported":true,"toggle":true`, Cost: plan},
		{ID: "plan-only", Cost: plan},
	}
	paygModels := []provBSpec{{ID: "glm-5.2", Cost: payg}, {ID: "glm-5.2-highspeed", Cost: payg}, {ID: "glm-5.1", Cost: payg}, {ID: "glm-4.5", Cost: payg}, {ID: "glm-4.5-air", Cost: payg}, {ID: "glm-4.5-flash", Cost: payg}, {ID: "glm-4.5v", Cost: payg}, {ID: "glm-4.7", Cost: payg}}
	sources := &CatalogSources{ModelsDev: provBModelsDev(t, map[string][]provBSpec{"zai-coding-plan": planModels, "zhipuai-coding-plan": planModels, "zai": paygModels, "zhipuai": {{ID: "plan-only", Cost: payg}}})}
	// supportsReasoningEffort and zaiToolStream follow the models: only models with verified effort levels take the effort;
	// the GLM-4.5 generation rejects tool_stream.
	compat := func(effort, toolStream bool) string {
		out := `{"supportsStore":false,"supportsDeveloperRole":false,"supportsReasoningEffort":`
		if effort {
			out += "true"
		} else {
			out += "false"
		}
		out += `,"maxTokensField":"max_tokens","thinkingFormat":"zai","supportsStrictMode":true`
		if toolStream {
			out += `,"zaiToolStream":true`
		}
		return out + "}"
	}
	paygCost := `{"input":5,"output":10,"cacheRead":0.5,"cacheWrite":0}`
	for id, baseURL := range map[string]string{
		"zai":           "https://api.z.ai/api/coding/paas/v4",
		"zai-coding-cn": "https://open.bigmodel.cn/api/coding/paas/v4",
	} {
		catalog, err := provBBuild(t, id, sources, true)
		if err != nil {
			t.Fatal(err)
		}
		provBCheck(t, provBModelsOf(catalog), []provBWant{
			{"glm-5.2", "baseUrl", `"` + baseURL + `"`},
			// GLM 5.2 turns thinking off with "none".
			{"glm-5.2", "thinkingLevelMap", `{"off":"none","minimal":null,"low":null,"medium":null,"high":"high","xhigh":null,"max":null}`},
			{"glm-5.2-highspeed", "thinkingLevelMap", `{"off":"none","minimal":null,"low":null,"medium":null,"high":"high","xhigh":null,"max":null}`},
			{"glm-5.1", "thinkingLevelMap", `{"off":null,"minimal":null,"low":null,"medium":null,"high":"high","xhigh":null,"max":null}`},
			{"glm-5.2", "compat", compat(true, true)},
			{"glm-5.1", "compat", compat(true, true)},
			{"glm-4.7", "compat", compat(false, true)},
			{"glm-4.7", "thinkingLevelMap", "undefined"},
			{"glm-4.5", "compat", compat(false, false)},
			{"glm-4.5-air", "compat", compat(false, false)},
			{"glm-4.5-flash", "compat", compat(false, false)},
			{"glm-4.5v", "compat", compat(false, false)},
			// Coding-plan entries carry plan prices; the pay-as-you-go rates are shown instead when the API model has a price.
			{"glm-5.2", "cost", paygCost},
			{"glm-4.5v", "cost", paygCost},
			{"plan-only", "cost", `{"input":1,"output":2,"cacheRead":0,"cacheWrite":0}`},
		})
	}
}
