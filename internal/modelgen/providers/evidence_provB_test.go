package providers

// Behavioral evidence for packages/ai/catalog/providers/{moonshotai,nvidia,openai-codex,openai,opencode-go,opencode,openrouter,
// qwen-token-plan-cn,qwen-token-plan-individual,qwen-token-plan,radius,together,typesafe,vercel-ai-gateway,xai,
// xiaomi-token-plan-ams,xiaomi-token-plan-cn,xiaomi-token-plan-sgp,xiaomi,zai-coding-cn,zai}.ts.
//
// Pi has no test for the provider definitions; they are exercised by its catalog generator. These tables run the registered
// definitions (All) through BuildCatalog over hand-written upstream data and compare serialized model fields (member order
// included, the generated JSON is compared byte for byte) with what the TypeScript definition produces for the same input. The
// provider-wide check against Pi's generator output is cmd/gen-models TestCatalogGenerationMatchesPi.

import (
	"fmt"
	"strings"
	"testing"

	. "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: same dot import as the provider files
)

// provBSpec is one models.dev v2 model entry.
type provBSpec struct {
	ID, Name string
	// Reasoning is the body of capabilities.reasoning, e.g. `"supported":true,"effort":["low","high"]`.
	Reasoning string
	Image     bool
	Status    string
	// API is the JSON object of the model's api member, e.g. `{"responses":{"base_url":"https://x/v1"}}`.
	API string
	// Cost is the JSON object of the cost member.
	Cost string
	// Output overrides the output modalities (default text).
	Output string
	// NoTools drops tool support.
	NoTools      bool
	Context, Max int
}

func (s provBSpec) json() string {
	reasoning := ""
	if s.Reasoning != "" {
		reasoning = `,"reasoning":{` + s.Reasoning + `}`
	}
	input := `["text"]`
	if s.Image {
		input = `["text","image"]`
	}
	output := s.Output
	if output == "" {
		output = `["text"]`
	}
	extra := ""
	if s.Name != "" {
		extra += `,"name":"` + s.Name + `"`
	}
	if s.Status != "" {
		extra += `,"status":"` + s.Status + `"`
	}
	if s.API != "" {
		extra += `,"api":` + s.API
	}
	cost := s.Cost
	if cost == "" {
		cost = `{"text":{"input":{"price":1},"output":{"price":2}}}`
	}
	context, max := s.Context, s.Max
	if context == 0 {
		context = 8000
	}
	if max == 0 {
		max = 800
	}
	return fmt.Sprintf(`{"id":%q,"type":"chat"%s,"capabilities":{"tools":{"supported":%t}%s},"modalities":{"input":%s,"output":%s},"limit":{"context":%d,"output":%d},"cost":%s}`,
		s.ID, extra, !s.NoTools, reasoning, input, output, context, max, cost)
}

// provBModelsDev is a models.dev v2 document: provider key -> models.
func provBModelsDev(t *testing.T, providers map[string][]provBSpec) *Obj {
	t.Helper()
	var members []string
	for key, specs := range providers {
		var models []string
		for _, spec := range specs {
			models = append(models, fmt.Sprintf("%q:%s", spec.ID, spec.json()))
		}
		members = append(members, fmt.Sprintf(`%q:{"models":{%s}}`, key, strings.Join(models, ",")))
	}
	value, err := ParseJSON([]byte("{" + strings.Join(members, ",") + "}"))
	if err != nil {
		t.Fatal(err)
	}
	return value.(*Obj)
}

func provBDefinition(t *testing.T, id string) ProviderDefinition {
	t.Helper()
	for _, definition := range All() {
		if definition.ID == id {
			return definition
		}
	}
	t.Fatalf("no provider %q registered", id)
	return ProviderDefinition{}
}

func provBBuild(t *testing.T, id string, sources *CatalogSources, strict bool) (ProviderCatalog, error) {
	t.Helper()
	catalogs, err := BuildCatalog([]ProviderDefinition{provBDefinition(t, id)}, sources, strict)
	if err != nil {
		return ProviderCatalog{}, err
	}
	return catalogs[0], nil
}

func provBModelsOf(catalog ProviderCatalog) map[string]*Obj {
	models := map[string]*Obj{}
	for _, model := range catalog.Chat {
		models[model.Str("id")] = model
	}
	return models
}

func provBIDs(catalog ProviderCatalog) []string {
	ids := []string{}
	for _, model := range catalog.Chat {
		ids = append(ids, model.Str("id"))
	}
	return ids
}

// provBField serializes one model member; an absent member is "undefined".
func provBField(model *Obj, key string) string {
	value, ok := model.Get(key)
	if !ok {
		return "undefined"
	}
	return string(Stringify(value, ""))
}

// provBWant is the expectation of one model member (want is JSON, or "undefined" for an absent member).
type provBWant struct{ model, field, want string }

func provBCheck(t *testing.T, models map[string]*Obj, wants []provBWant) {
	t.Helper()
	for _, want := range wants {
		model := models[want.model]
		if model == nil {
			t.Errorf("model %q is missing", want.model)
			continue
		}
		if got := provBField(model, want.field); got != want.want {
			t.Errorf("%s %s = %s, want %s", want.model, want.field, got, want.want)
		}
	}
}

func provBIDsEqual(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("model ids = %v, want %v", got, want)
	}
}

func TestProvBMoonshotai(t *testing.T) {
	// pi: providers/moonshotai.ts moonshotModels + finalizeMoonshotModel, families/kimi.ts
	sources := &CatalogSources{ModelsDev: provBModelsDev(t, map[string][]provBSpec{"moonshotai": {
		// K3 reports no reasoning upstream and lists the input rate as cache_write: reasoning is forced on, cache writes are free.
		{ID: "kimi-k3.1-pro-highspeed", Cost: `{"text":{"input":{"price":6,"cache":{"read":0.6,"write":6}},"output":{"price":30}}}`},
		// kimi-k3 itself carries Moonshot's own price list.
		{ID: "kimi-k3", Cost: `{"text":{"input":{"price":9,"cache":{"read":0.9,"write":9}},"output":{"price":99}}}`},
		// K2.6 keeps its cache write price; it accepts text-only mid-conversation system messages.
		{ID: "kimi-k2.6", Reasoning: `"supported":true,"toggle":true`, Cost: `{"text":{"input":{"price":1,"cache":{"read":0.2,"write":1.5}},"output":{"price":4}}}`},
		// K2.7 Code is always thinking: no "off".
		{ID: "kimi-k2.7-code", Reasoning: `"supported":true,"toggle":true`},
		{ID: "kimi-k2.7-code-highspeed", Reasoning: `"supported":true,"toggle":true`},
		{ID: "kimi-k2.5"},
	}})}
	catalog, err := provBBuild(t, "moonshotai", sources, true)
	if err != nil {
		t.Fatal(err)
	}
	models := provBModelsOf(catalog)
	k2Compat := `{"supportsStore":false,"supportsDeveloperRole":false,"supportsReasoningEffort":false,"maxTokensField":"max_tokens","supportsStrictMode":false,"thinkingFormat":"deepseek"`
	k3Compat := `{"supportsStore":false,"supportsDeveloperRole":false,"supportsReasoningEffort":true,"maxTokensField":"max_tokens","supportsStrictMode":false,"thinkingFormat":"openai","requiresReasoningContentOnAssistantMessages":true,"supportsMidConvoSystemMessages":true,"supportsMidConvoToolAdditions":true}`
	provBCheck(t, models, []provBWant{
		{"kimi-k3.1-pro-highspeed", "reasoning", "true"},
		{"kimi-k3.1-pro-highspeed", "cost", `{"input":6,"output":30,"cacheRead":0.6,"cacheWrite":0}`},
		{"kimi-k3.1-pro-highspeed", "compat", k3Compat},
		{"kimi-k3", "reasoning", "true"},
		{"kimi-k3", "cost", `{"input":3,"output":15,"cacheRead":0.3,"cacheWrite":0}`},
		{"kimi-k3", "compat", k3Compat},
		{"kimi-k2.6", "reasoning", "true"},
		{"kimi-k2.6", "cost", `{"input":1,"output":4,"cacheRead":0.2,"cacheWrite":1.5}`},
		{"kimi-k2.6", "compat", k2Compat + `,"supportsMidConvoSystemMessages":true}`},
		{"kimi-k2.6", "thinkingLevelMap", "undefined"},
		{"kimi-k2.7-code", "thinkingLevelMap", `{"off":null}`},
		{"kimi-k2.7-code", "compat", k2Compat + `,"supportsMidConvoSystemMessages":true}`},
		{"kimi-k2.7-code-highspeed", "thinkingLevelMap", `{"off":null}`},
		{"kimi-k2.7-code-highspeed", "compat", k2Compat + `,"supportsMidConvoSystemMessages":true}`},
		{"kimi-k2.5", "compat", k2Compat + `}`},
		{"kimi-k2.5", "baseUrl", `"https://api.moonshot.ai/v1"`},
		{"kimi-k2.5", "reasoning", "false"},
	})
}

func TestProvBNvidia(t *testing.T) {
	// pi: providers/nvidia.ts
	live := map[string]string{}
	for _, id := range []string{"acme/Model.One", "acme/embedder", "acme/plain", "google/gemma-4-31b-it", "acme/named"} {
		live[id] = id
		live[NormalizeNvidiaModelID(id)] = id
	}
	sources := &CatalogSources{
		NvidiaModelIDs: live,
		ModelsDev: provBModelsDev(t, map[string][]provBSpec{"nvidia": {
			// models.dev spells the id differently from NIM: the live id wins, and an unnamed model is named after it.
			{ID: "acme/model_one"},
			{ID: "acme/named", Name: "Pretty Name"},
			{ID: "acme/plain", Name: "acme/plain"},
			// Text in, but not text out.
			{ID: "acme/embedder", Output: `["embedding"]`},
			// Listed by NIM but unusable through pi.
			{ID: "google/gemma-4-31b-it"},
			// Not served by NIM.
			{ID: "acme/gone"},
		}}),
	}
	catalog, err := provBBuild(t, "nvidia", sources, true)
	if err != nil {
		t.Fatal(err)
	}
	provBIDsEqual(t, provBIDs(catalog), "acme/Model.One", "acme/named", "acme/plain")
	models := provBModelsOf(catalog)
	compat := `{"supportsStore":false,"supportsDeveloperRole":false,"supportsReasoningEffort":false,"maxTokensField":"max_tokens","supportsLongCacheRetention":false,"supportsStrictMode":false}`
	provBCheck(t, models, []provBWant{
		{"acme/Model.One", "name", `"acme/Model.One"`},
		{"acme/named", "name", `"Pretty Name"`},
		{"acme/plain", "name", `"acme/plain"`},
		{"acme/Model.One", "compat", compat},
		{"acme/Model.One", "headers", `{"NVCF-POLL-SECONDS":"3600"}`},
		{"acme/Model.One", "baseUrl", `"https://integrate.api.nvidia.com/v1"`},
		{"acme/Model.One", "api", `"openai-completions"`},
	})
}

func TestProvBOpenAICodex(t *testing.T) {
	// pi: providers/openai-codex.ts (an authored list: models.dev does not list the ChatGPT OAuth models)
	catalog, err := provBBuild(t, "openai-codex", &CatalogSources{}, true)
	if err != nil {
		t.Fatal(err)
	}
	provBIDsEqual(t, provBIDs(catalog), "gpt-6.1-sol", "gpt-6.1-terra", "gpt-6-astra", "gpt-6-sol", "gpt-6-luna", "gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra")
	models := provBModelsOf(catalog)
	provBCheck(t, models, []provBWant{
		{"gpt-6.1-sol", "name", `"GPT-6.1 Sol"`},
		{"gpt-6.1-sol", "contextWindow", "500000"},
		{"gpt-6.1-terra", "contextWindow", "500000"},
		{"gpt-6-astra", "contextWindow", "272000"},
		{"gpt-5.6-terra", "contextWindow", "272000"},
		{"gpt-6-sol", "maxTokens", "128000"},
		{"gpt-6-sol", "api", `"openai-codex-responses"`},
		{"gpt-6-sol", "baseUrl", `"https://chatgpt.com/backend-api"`},
		{"gpt-6-sol", "reasoning", "true"},
		{"gpt-6-sol", "input", `["text","image"]`},
		{"gpt-6-luna", "cost", string(Stringify(OpenAIStandardCost("gpt-6-luna"), ""))},
		{"gpt-5.6-sol", "cost", string(Stringify(OpenAIStandardCost("gpt-5.6-sol"), ""))},
	})
	// Every Codex model supports tool search, additional tools, grammar tools and in-place mid-conversation system messages.
	for _, model := range catalog.Chat {
		compat := model.Object("compat")
		for _, flag := range []string{"supportsToolSearch", "supportsAdditionalTools", "supportsOpenAIGrammarTools", "supportsMidConvoSystemMessages"} {
			if compat == nil || !compat.Bool(flag) {
				t.Errorf("%s: compat.%s missing in %s", model.Str("id"), flag, provBField(model, "compat"))
			}
		}
	}
}

func TestProvBOpenAI(t *testing.T) {
	// pi: providers/openai.ts
	longCost := `{"text":{"input":{"price":5,"cache":{"read":0.5,"write":6.25},"overrides":[{"when":{"context":{"gt":272000}},"price":10}]},"output":{"price":30,"overrides":[{"when":{"context":{"gt":272000}},"price":45}]}}}`
	both := `{"responses":{"base_url":"https://api.openai.com/v1"},"chat-completions":{"base_url":"https://api.openai.com/v1"}}`
	chatOnly := `{"chat-completions":{"base_url":"https://api.openai.com/v1"}}`
	sources := &CatalogSources{ModelsDev: provBModelsDev(t, map[string][]provBSpec{"openai": {
		// models.dev alias that OpenAI rejects.
		{ID: "gpt-5.6", API: both},
		// Audio models are only served on Chat Completions.
		{ID: "gpt-audio", API: chatOnly},
		// models.dev reports 272000 output for gpt-5-pro; the real limit is 128000.
		{ID: "gpt-5-pro", API: both, Max: 272000, Context: 400000},
		// Short-context capped models.
		{ID: "gpt-5.5", API: both, Context: 1050000, Max: 128000, Cost: longCost, Reasoning: `"supported":true,"effort":["none","minimal","low","medium","high","xhigh"]`},
		{ID: "gpt-6-sol", Image: true, API: both, Context: 1050000, Max: 128000, Reasoning: `"supported":true,"effort":["none","low","medium","high"]`},
		{ID: "gpt-5.1", API: both, Reasoning: `"supported":true,"effort":["none","low","medium","high"]`},
		{ID: "gpt-5.4-pro", API: both, Context: 1050000, Max: 128000},
	}})}
	catalog, err := provBBuild(t, "openai", sources, true)
	if err != nil {
		t.Fatal(err)
	}
	models := provBModelsOf(catalog)
	if _, ok := models["gpt-5.6"]; ok {
		t.Errorf("gpt-5.6 is not accepted by OpenAI APIs: %v", provBIDs(catalog))
	}
	if _, ok := models["gpt-audio"]; ok {
		t.Errorf("gpt-audio is not served on Responses: %v", provBIDs(catalog))
	}
	provBCheck(t, models, []provBWant{
		// authored model: not listed by models.dev
		{"gpt-5-chat-latest", "name", `"GPT-5 Chat Latest"`},
		{"gpt-5-chat-latest", "api", `"openai-responses"`},
		{"gpt-5-chat-latest", "baseUrl", `"https://api.openai.com/v1"`},
		{"gpt-5-chat-latest", "reasoning", "false"},
		{"gpt-5-chat-latest", "input", `["text","image"]`},
		{"gpt-5-chat-latest", "cost", `{"input":1.25,"output":10,"cacheRead":0.125,"cacheWrite":0}`},
		{"gpt-5-chat-latest", "contextWindow", "128000"},
		{"gpt-5-chat-latest", "maxTokens", "16384"},
		{"gpt-5-pro", "maxTokens", "128000"},
		{"gpt-5.5", "contextWindow", "272000"},
		{"gpt-5.5", "maxTokens", "128000"},
		{"gpt-6-sol", "contextWindow", "272000"},
		{"gpt-5.4-pro", "contextWindow", "1050000"},
		// Long-context pricing: above 272K input tokens OpenAI bills 2x input and cache, 1.5x output.
		{"gpt-5.4-pro", "cost", `{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"tiers":[{"inputTokensAbove":272000,"input":2,"output":3,"cacheRead":0,"cacheWrite":0}]}`},
		{"gpt-5.5", "cost", `{"input":5,"output":30,"cacheRead":0.5,"cacheWrite":6.25,"tiers":[{"inputTokensAbove":272000,"input":10,"output":45,"cacheRead":1,"cacheWrite":12.5}]}`},
		// The standard price list is authoritative over models.dev.
		{"gpt-6-sol", "cost", `{"input":2,"output":10,"cacheRead":0.2,"cacheWrite":2.5,"tiers":[{"inputTokensAbove":272000,"input":4,"output":15,"cacheRead":0.4,"cacheWrite":5}]}`},
		// "none" is the verified off value of these models; gpt-5.5 has no "minimal".
		{"gpt-5.1", "thinkingLevelMap", `{"off":"none","minimal":null,"low":"low","medium":"medium","high":"high","xhigh":null,"max":null}`},
		{"gpt-5.5", "thinkingLevelMap", `{"off":"none","minimal":null,"low":"low","medium":"medium","high":"high","xhigh":"xhigh","max":null}`},
		{"gpt-6-sol", "thinkingLevelMap", `{"off":"none","minimal":null,"low":"low","medium":"medium","high":"high","xhigh":"xhigh","max":"max"}`},
		{"gpt-6-sol", "inputLimits", `{"maxRequestBytes":536870912,"images":{"maxPerRequest":1500,"resize":{"maxWidth":2000,"maxHeight":2000,"maxBytes":4718592,"jpegQuality":80}}}`},
	})
	for _, id := range []string{"gpt-5.1", "gpt-5.5", "gpt-6-sol", "gpt-5-chat-latest"} {
		if compat := provBField(models[id], "compat"); !strings.Contains(compat, `"supportsStrictMode":true`) {
			t.Errorf("%s compat = %s", id, compat)
		}
	}
	// gpt-6-sol: tool search + additional tools, the GPT-6 mid-conversation effort support, in place developer messages.
	if compat := provBField(models["gpt-6-sol"], "compat"); !strings.Contains(compat, `"supportsMidConvoEffort":true`) ||
		!strings.Contains(compat, `"supportsAdditionalTools":true`) || !strings.Contains(compat, `"supportsToolSearch":true`) ||
		!strings.Contains(compat, `"supportsMidConvoSystemMessages":true`) || !strings.Contains(compat, `"supportsOpenAIGrammarTools":true`) {
		t.Errorf("gpt-6-sol compat = %s", compat)
	}
	// gpt-5.5 bills cache writes: exactly those models accept prompt_cache_options.
	if compat := provBField(models["gpt-5.5"], "compat"); !strings.Contains(compat, `"supportsExplicitPromptCacheMode":true`) {
		t.Errorf("gpt-5.5 compat = %s", compat)
	}
	if compat := provBField(models["gpt-5.1"], "compat"); strings.Contains(compat, "supportsExplicitPromptCacheMode") {
		t.Errorf("gpt-5.1 has no cache write price but compat = %s", compat)
	}
	// The classifier model (OpenAI Decisions API, public beta).
	if len(catalog.Classifier) != 1 {
		t.Fatalf("classifiers = %d", len(catalog.Classifier))
	}
	classifier := catalog.Classifier[0]
	if got, want := string(Stringify(classifier, "")), `{"type":"classifier","id":"gpt-6-luna","name":"GPT-6 Luna","api":"openai-decisions","provider":"openai","baseUrl":"https://api.openai.com/v1","input":["text","image"],"cost":`; !strings.HasPrefix(got, want) {
		t.Errorf("classifier = %s", got)
	}
	if got := provBField(classifier, "contextWindow"); got != "922000" {
		t.Errorf("classifier contextWindow = %s", got)
	}
	if got := classifier.Object("cost"); got.Num("input") != 0.1 || got.Num("output") != 0 {
		t.Errorf("classifier cost = %s", Stringify(got, ""))
	}
}
