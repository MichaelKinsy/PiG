package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseDataJSONRejectsUnknownModelFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	writeFile(t, path, `{
	  "api": {
	    "chat:model": {
	      "type": "chat", "id": "model", "name": "Model", "api": "openai-responses",
	      "provider": "provider", "baseUrl": "https://example.test",
	      "reasoning": false, "input": ["text"], "contextWindow": 1000,
	      "maxTokens": 100, "newUpstreamCapability": true
	    }
	  }
	}`)
	if _, err := parseDataJSON(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("parseDataJSON() error = %v, want unknown-field failure", err)
	}
}

func TestParseDataJSONAcceptsInputLimits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	writeFile(t, path, `{
	  "anthropic-messages": {
	    "chat:model": {
	      "type": "chat", "id": "model", "name": "Model", "api": "anthropic-messages",
	      "provider": "anthropic", "baseUrl": "https://example.test",
	      "reasoning": true, "input": ["text", "image"], "contextWindow": 1000,
	      "maxTokens": 100,
	      "inputLimits": {
	        "maxRequestBytes": 33554432,
	        "images": {
	          "maxPerMessage": 20, "maxPerRequest": 600,
	          "resize": {"maxWidth": 2000, "maxHeight": 1800, "maxBytes": 4718592, "jpegQuality": 80}
	        }
	      }
	    }
	  }
	}`)
	rows, err := parseDataJSON(path)
	if err != nil {
		t.Fatalf("parseDataJSON() rejected inputLimits: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("parseDataJSON() returned %d rows, want 1", len(rows))
	}
	limits := rows[0].InputLimits
	if limits == nil || !limits.MaxRequestBytes.Present || limits.MaxRequestBytes.Value != 33554432 || limits.Images == nil || !limits.Images.MaxPerMessage.Present || limits.Images.MaxPerMessage.Value != 20 || !limits.Images.MaxPerRequest.Present || limits.Images.MaxPerRequest.Value != 600 || limits.Images.Resize == nil {
		t.Fatalf("input limits = %#v", limits)
	}
	resize := limits.Images.Resize
	if !resize.MaxWidth.Present || resize.MaxWidth.Value != 2000 || !resize.MaxHeight.Present || resize.MaxHeight.Value != 1800 || !resize.MaxBytes.Present || resize.MaxBytes.Value != 4718592 || !resize.JPEGQuality.Present || resize.JPEGQuality.Value != 80 {
		t.Fatalf("resize limits = %#v", resize)
	}
}

func TestInputLimitsPreserveOptionalNumericPresence(t *testing.T) {
	tests := []struct {
		name        string
		inputLimits string
		want        string
	}{
		{name: "empty input limits", inputLimits: `{}`, want: `&ModelInputLimits{}`},
		{name: "empty images", inputLimits: `{"images":{}}`, want: `&ModelInputLimits{Images: &ModelImageInputLimits{}}`},
		{name: "empty resize", inputLimits: `{"images":{"resize":{}}}`, want: `&ModelInputLimits{Images: &ModelImageInputLimits{Resize: &ModelImageResizeOptions{}}}`},
		{name: "partial resize", inputLimits: `{"images":{"resize":{"maxWidth":2000}}}`, want: `&ModelInputLimits{Images: &ModelImageInputLimits{Resize: &ModelImageResizeOptions{MaxWidth: 2000}}}`},
		{name: "partial images", inputLimits: `{"images":{"maxPerRequest":600}}`, want: `&ModelInputLimits{Images: &ModelImageInputLimits{MaxPerRequest: 600}}`},
		{name: "request bytes", inputLimits: `{"maxRequestBytes":33554432}`, want: `&ModelInputLimits{MaxRequestBytes: 33554432}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "models.json")
			writeFile(t, path, `{"anthropic-messages":{"chat:model":{"type": "chat", "id": "model","name":"Model","api":"anthropic-messages","provider":"anthropic","baseUrl":"https://example.test","reasoning":false,"input":["text"],"contextWindow":1000,"maxTokens":100,"inputLimits":`+tt.inputLimits+`}}}`)
			rows, err := parseDataJSON(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := inputLimitsLiteral(rows[0].InputLimits); got != tt.want {
				t.Fatalf("inputLimitsLiteral() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInputLimitsRejectNonPositivePresentValues(t *testing.T) {
	tests := []struct {
		name        string
		inputLimits string
		field       string
	}{
		{name: "input limits null", inputLimits: `null`, field: "inputLimits"},
		{name: "images null", inputLimits: `{"images":null}`, field: "images"},
		{name: "resize null", inputLimits: `{"images":{"resize":null}}`, field: "resize"},
		{name: "request bytes null", inputLimits: `{"maxRequestBytes":null}`, field: "maxRequestBytes"},
		{name: "request bytes zero", inputLimits: `{"maxRequestBytes":0}`, field: "maxRequestBytes"},
		{name: "per message zero", inputLimits: `{"images":{"maxPerMessage":0}}`, field: "maxPerMessage"},
		{name: "per request zero", inputLimits: `{"images":{"maxPerRequest":0}}`, field: "maxPerRequest"},
		{name: "width null", inputLimits: `{"images":{"resize":{"maxWidth":null}}}`, field: "maxWidth"},
		{name: "width zero", inputLimits: `{"images":{"resize":{"maxWidth":0}}}`, field: "maxWidth"},
		{name: "height negative", inputLimits: `{"images":{"resize":{"maxHeight":-1}}}`, field: "maxHeight"},
		{name: "bytes zero", inputLimits: `{"images":{"resize":{"maxBytes":0}}}`, field: "maxBytes"},
		{name: "quality zero", inputLimits: `{"images":{"resize":{"jpegQuality":0}}}`, field: "jpegQuality"},
		{name: "quality above maximum", inputLimits: `{"images":{"resize":{"jpegQuality":101}}}`, field: "jpegQuality"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "models.json")
			writeFile(t, path, `{"anthropic-messages":{"chat:model":{"type": "chat", "id": "model","name":"Model","api":"anthropic-messages","provider":"anthropic","baseUrl":"https://example.test","reasoning":false,"input":["text"],"contextWindow":1000,"maxTokens":100,"inputLimits":`+tt.inputLimits+`}}}`)
			_, err := parseDataJSON(path)
			if err == nil || !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("parseDataJSON() error = %v, want %s validation error", err, tt.field)
			}
		})
	}
}

func TestParseDataJSONAcceptsCurrentUpstreamModelMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	writeFile(t, path, `{
	  "anthropic-messages": {
	    "chat:model": {
	      "type": "chat", "id": "model", "name": "Model", "api": "anthropic-messages",
	      "provider": "anthropic", "baseUrl": "https://example.test",
	      "reasoning": true, "input": ["text"], "contextWindow": 1000,
	      "maxTokens": 100, "promptCache": {"short": 300, "long": 3600},
	      "compat": {
	        "allowedFallbackModels": [{
	          "provider": "anthropic", "model": "fallback",
	          "cost": {"input": 1, "output": 2, "cacheRead": 0.1, "cacheWrite": 1.25}
	        }],
	        "supportsAdditionalTools": true,
	        "supportsMidConvoEffort": true,
	        "supportsMidConvoSystemMessages": true,
	        "supportsMidConvoToolAdditions": true,
	        "supportsMidConvoToolChanges": true
	      }
	    }
	  },
	  "pi-messages": {
	    "chat:radius-model": {
	      "type": "chat", "id": "radius-model", "name": "Radius Model", "api": "pi-messages",
	      "provider": "radius", "baseUrl": "https://radius.example.test",
	      "reasoning": false, "input": ["text"], "contextWindow": 1000,
	      "maxTokens": 100, "enabled": true, "lab": "Example Lab",
	      "providers": [{"id": "backend", "name": "Backend", "credential": "radius", "source": "radius"}]
	    }
	  }
	}`)
	rows, err := parseDataJSON(path)
	if err != nil {
		t.Fatalf("parseDataJSON() rejected current upstream metadata: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("parseDataJSON() returned %d rows, want 2", len(rows))
	}
	anthropic := rows[0]
	if anthropic.PromptCache["short"] != 300 || anthropic.PromptCache["long"] != 3600 {
		t.Fatalf("prompt cache = %v", anthropic.PromptCache)
	}
	if anthropic.Compat == nil || anthropic.Compat.SupportsAdditionalTools == nil || !*anthropic.Compat.SupportsAdditionalTools || anthropic.Compat.SupportsMidConvoEffort == nil || !*anthropic.Compat.SupportsMidConvoEffort || anthropic.Compat.SupportsMidConvoSystemMessages == nil || !*anthropic.Compat.SupportsMidConvoSystemMessages || anthropic.Compat.SupportsMidConvoToolAdditions == nil || !*anthropic.Compat.SupportsMidConvoToolAdditions || anthropic.Compat.SupportsMidConvoToolChanges == nil || !*anthropic.Compat.SupportsMidConvoToolChanges {
		t.Fatalf("compat metadata = %+v", anthropic.Compat)
	}
	if len(anthropic.Compat.AllowedFallbackModels) != 1 || anthropic.Compat.AllowedFallbackModels[0].Model != "fallback" || anthropic.Compat.AllowedFallbackModels[0].Cost.CacheWrite != 1.25 {
		t.Fatalf("fallback metadata = %+v", anthropic.Compat.AllowedFallbackModels)
	}
	radius := rows[1]
	if radius.Enabled == nil || !*radius.Enabled || radius.Lab != "Example Lab" || len(radius.Providers) != 1 || radius.Providers[0].Credential != "radius" {
		t.Fatalf("Radius metadata = enabled %v lab %q providers %+v", radius.Enabled, radius.Lab, radius.Providers)
	}
}

func TestCollectRowsAPIGroupedJSONProvider(t *testing.T) {
	dir := t.TempDir()
	providerDir := filepath.Join(dir, "providers")
	dataDir := filepath.Join(providerDir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(providerDir, "anthropic.models.js"),
		"import values from \"./data/anthropic.json\" with { type: \"json\" };\n")
	writeFile(t, filepath.Join(dataDir, "anthropic.json"), `{
	  "anthropic-messages": {
	    "chat:claude-opus-5": {
	      "type": "chat", "id": "claude-opus-5", "name": "Claude Opus 5",
	      "api": "anthropic-messages", "provider": "anthropic",
	      "baseUrl": "https://api.anthropic.com", "reasoning": true,
	      "input": ["text", "image"], "contextWindow": 1000000, "maxTokens": 128000,
	      "samplingParams": {"top_p": 0.8, "min_p": 0.1},
	      "cost": {"input": 5, "output": 25, "cacheRead": 0.5, "cacheWrite": 6.25}
	    },
	    "chat:claude-sonnet-5": {
	      "type": "chat", "id": "claude-sonnet-5", "name": "Claude Sonnet 5",
	      "api": "anthropic-messages", "provider": "anthropic",
	      "baseUrl": "https://api.anthropic.com", "reasoning": true,
	      "input": ["text", "image"], "contextWindow": 1000000, "maxTokens": 128000
	    }
	  }
	}`)
	barrelPath := filepath.Join(dir, "models.generated.js")
	writeFile(t, barrelPath, "import { ANTHROPIC_CLASSIFIER_MODELS, ANTHROPIC_IMAGE_MODELS, ANTHROPIC_MODELS } from \"./providers/anthropic.models.js\";\n")

	catalog, err := collectCatalog(barrelPath)
	if err != nil {
		t.Fatal(err)
	}
	rows := catalog.Chat
	if len(rows) != 2 || rows[0].ID != "claude-opus-5" || rows[1].ID != "claude-sonnet-5" || rows[0].API != "anthropic-messages" {
		t.Fatalf("collectCatalogRows() order = %+v", rows)
	}
	if rows[0].SamplingParams["top_p"] != 0.8 || rows[0].SamplingParams["min_p"] != 0.1 {
		t.Fatalf("collectCatalogRows() sampling params = %v", rows[0].SamplingParams)
	}
}

// TestCollectRowsJSONBackedProvider proves the 0.81 catalog layout, where each
// provider module re-exports a data/<provider>.json file, is read via the JSON
// path and mapped onto the same modelRow schema. It also checks that cost tiers
// are retained and an all-empty compat object normalizes to nil. Unknown model
// fields are rejected by TestParseDataJSONRejectsUnknownModelFields.
func TestCollectRowsJSONBackedProvider(t *testing.T) {
	dir := t.TempDir()
	provDir := filepath.Join(dir, "providers")
	dataDir := filepath.Join(provDir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(provDir, "anthropic.models.js"),
		"import values from \"./data/anthropic.json\" with { type: \"json\" };\n"+
			"export const ANTHROPIC_MODELS = values;\n")
	// Two models: one with cost/compat/thinking, one with an empty compat.
	writeFile(t, filepath.Join(dataDir, "anthropic.json"), `{"anthropic-messages": {
	  "chat:claude-j": {
	    "type": "chat", "id": "claude-j", "name": "Claude J", "api": "anthropic-messages",
	    "provider": "anthropic", "baseUrl": "https://api.anthropic.com",
	    "reasoning": true, "input": ["text", "image"],
	    "contextWindow": 200000, "maxTokens": 64000,
	    "cost": {"input": 1, "output": 5, "cacheRead": 0.1, "cacheWrite": 1.25,
	             "tiers": [{"inputTokensAbove": 200000, "input": 2}]},
	    "compat": {"supportsDeveloperRole": false, "supportsToolSearch": true,
	               "chatTemplateArgs": {"enable_thinking": true, "budget": 1024},
	               "supportsThinkingTokenBudget": true},
	    "thinkingLevelMap": {"off": null, "xhigh": "xhigh", "max": "max"}
	  },
	  "chat:claude-k": {
	    "type": "chat", "id": "claude-k", "name": "Claude K", "api": "anthropic-messages",
	    "provider": "anthropic", "baseUrl": "https://api.anthropic.com",
	    "contextWindow": 100000, "maxTokens": 8192, "compat": {}
	  }
	}}`)

	barrel := "import { ANTHROPIC_CLASSIFIER_MODELS, ANTHROPIC_IMAGE_MODELS, ANTHROPIC_MODELS } from \"./providers/anthropic.models.js\";\n" +
		"export const MODELS = { \"anthropic\": ANTHROPIC_MODELS } as const;\n"
	barrelPath := filepath.Join(dir, "models.generated.js")
	writeFile(t, barrelPath, barrel)

	catalog, err := collectCatalog(barrelPath)
	if err != nil {
		t.Fatalf("collectCatalog: %v", err)
	}
	rows := catalog.Chat
	if len(rows) != 2 {
		t.Fatalf("parsed %d models, want 2", len(rows))
	}
	byID := map[string]modelRow{}
	for _, r := range rows {
		byID[r.ID] = r
	}

	j := byID["claude-j"]
	if j.Provider != "anthropic" || j.API != "anthropic-messages" || j.ContextWindow != 200000 || j.MaxTokens != 64000 {
		t.Errorf("claude-j core = %+v", j)
	}
	if j.InputCost != 1 || j.OutputCost != 5 || j.CacheRead != 0.1 || j.CacheWrite != 1.25 {
		t.Errorf("claude-j cost = in %v out %v cr %v cw %v", j.InputCost, j.OutputCost, j.CacheRead, j.CacheWrite)
	}
	if len(j.Tiers) != 1 || j.Tiers[0].InputTokensAbove != 200000 || j.Tiers[0].Input != 2 {
		t.Errorf("claude-j tiers = %+v", j.Tiers)
	}
	if !j.Reasoning || len(j.Inputs) != 2 {
		t.Errorf("claude-j reasoning/inputs = %v/%v", j.Reasoning, j.Inputs)
	}
	if j.ThinkingLevelMap["max"] == nil || *j.ThinkingLevelMap["max"] != "max" {
		t.Errorf("claude-j thinkingLevelMap max = %v", j.ThinkingLevelMap["max"])
	}
	if j.Compat == nil || j.Compat.SupportsDeveloperRole == nil || *j.Compat.SupportsDeveloperRole != false {
		t.Errorf("claude-j compat = %+v", j.Compat)
	}
	if j.Compat.SupportsThinkingTokenBudget == nil || !*j.Compat.SupportsThinkingTokenBudget || j.Compat.ChatTemplateArgs["budget"] != float64(1024) {
		t.Errorf("claude-j 0.84 compat = %+v", j.Compat)
	}

	k := byID["claude-k"]
	if k.Compat != nil {
		t.Errorf("claude-k empty compat should normalize to nil, got %+v", k.Compat)
	}
	if k.MaxTokens != 8192 || k.InputCost != 0 {
		t.Errorf("claude-k = maxTokens %d inputCost %v", k.MaxTokens, k.InputCost)
	}
}

func TestTiersLiteral(t *testing.T) {
	got := tiersLiteral([]jsonCostTier{
		{InputTokensAbove: 200000, Input: 2, Output: 10, CacheRead: 0.2, CacheWrite: 2.5},
	})
	want := "[]CostTier{{InputTokensAbove: 200000, InputCostPer1M: 2, OutputCostPer1M: 10, CacheReadCostPer1M: 0.2, CacheWriteCostPer1M: 2.5}, }"
	if got != want {
		t.Fatalf("tiersLiteral =\n%q\nwant\n%q", got, want)
	}
}

// TestCompatLiteralNewFlags pins the 0.81 compat additions so a catalog regen
// cannot silently drop them. The generator otherwise ignores unknown JSON keys.
func TestCompatLiteralNewFlags(t *testing.T) {
	tr := true
	c := &ModelCompat{
		DeferredToolsMode:              "kimi",
		SessionAffinityFormat:          "openrouter",
		SupportsToolSearch:             &tr,
		SupportsToolReferences:         &tr,
		ChatTemplateArgs:               map[string]any{"enable_thinking": true},
		SupportsThinkingTokenBudget:    &tr,
		ThinkingTokenBudgetField:       "thinking_budget",
		SupportsAdditionalTools:        &tr,
		SupportsMidConvoEffort:         &tr,
		SupportsMidConvoSystemMessages: &tr,
		SupportsMidConvoToolAdditions:  &tr,
		SupportsMidConvoToolChanges:    &tr,
		AllowedFallbackModels: []jsonAllowedFallbackModel{{
			Provider: "anthropic",
			Model:    "fallback",
			Cost:     jsonCost{Input: 1, Output: 2, CacheRead: 0.1, CacheWrite: 1.25},
		}},
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := compatLiteral(string(b))
	for _, want := range []string{
		`DeferredToolsMode:"kimi"`,
		`SessionAffinityFormat:"openrouter"`,
		"SupportsToolSearch:ptrBool(true)",
		"SupportsToolReferences:ptrBool(true)",
		`ChatTemplateArgs:map[string]interface {}{"enable_thinking":true}`,
		"SupportsThinkingTokenBudget:ptrBool(true)",
		`ThinkingTokenBudgetField:"thinking_budget"`,
		"SupportsAdditionalTools:ptrBool(true)",
		"SupportsMidConvoEffort:ptrBool(true)",
		"SupportsMidConvoSystemMessages:ptrBool(true)",
		"SupportsMidConvoToolAdditions:ptrBool(true)",
		"SupportsMidConvoToolChanges:ptrBool(true)",
		`AllowedFallbackModels:[]AnthropicAllowedFallbackModel{{Provider: "anthropic", Model: "fallback", Cost: ModelCost{Input: 1, Output: 2, CacheRead: 0.1, CacheWrite: 1.25`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("compatLiteral missing %q\ngot: %s", want, got)
		}
	}
}

// writeTypedCatalog writes a 0.99.1 layout catalog: a barrel importing the three catalog names of each provider shard and one data/<provider>.json per shard.
func writeTypedCatalog(t *testing.T, providers []string, data map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "providers", "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var barrel strings.Builder
	for _, provider := range providers {
		name := strings.ToUpper(strings.ReplaceAll(provider, "-", "_"))
		barrel.WriteString("import { " + name + "_CLASSIFIER_MODELS, " + name + "_IMAGE_MODELS, " + name + "_MODELS } from \"./providers/" + provider + ".models.js\";\n")
		writeFile(t, filepath.Join(dir, "providers", provider+".models.js"), "import values from \"./data/"+provider+".json\" with { type: \"json\" };\nimport { flattenChatModelCatalog, flattenClassifierModelCatalog, flattenImageModelCatalog } from \"../model-catalog.js\";\nexport const "+name+"_MODELS = flattenChatModelCatalog(\""+provider+"\", values);\n")
		writeFile(t, filepath.Join(dataDir, provider+".json"), data[provider])
	}
	path := filepath.Join(dir, "models.generated.js")
	writeFile(t, path, barrel.String())
	return path
}

const typedChatFixture = `"chat:%[1]s": {"type": "chat", "id": "%[1]s", "name": "%[1]s", "api": "openai-completions", "provider": "%[2]s", "baseUrl": "https://example.test/v1", "reasoning": false, "input": ["text"], "cost": {"input": 1, "output": 2, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 1000, "maxTokens": 100}`

func typedChat(id, provider string) string {
	return strings.NewReplacer("%[1]s", id, "%[2]s", provider).Replace(typedChatFixture)
}

// Upstream flattenModelCatalog (model-catalog.ts:57-63) selects every API group's models by type and keeps
// insertion order; the barrel (models.generated.ts) fixes the provider order.
func TestCollectCatalogSplitsTypesInBarrelOrder(t *testing.T) {
	image := `"image:img-1": {"type": "image", "id": "img-1", "name": "Image 1", "api": "test-images", "provider": "zeta", "baseUrl": "https://example.test/img", "headers": {"X-Test": "1"}, "input": ["text", "image"], "output": ["image", "text"], "cost": {"input": 3, "output": 4, "cacheRead": 5, "cacheWrite": 6}, "inputLimits": {"images": {"maxPerRequest": 4}}}`
	classifier := `"classifier:cls-1": {"type": "classifier", "id": "cls-1", "name": "Classifier 1", "api": "test-classifier", "provider": "zeta", "baseUrl": "https://example.test/cls", "input": ["text"], "cost": {"input": 7, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 32000}`
	path := writeTypedCatalog(t, []string{"zeta", "alpha"}, map[string]string{
		"zeta":  `{"openai-completions": {` + typedChat("z-b", "zeta") + `, ` + typedChat("z-a", "zeta") + `}, "test-images": {` + image + `}, "test-classifier": {` + classifier + `}}`,
		"alpha": `{"openai-completions": {` + typedChat("a-1", "alpha") + `}}`,
	})
	catalog, err := collectCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	var chat []string
	for _, row := range catalog.Chat {
		chat = append(chat, row.Provider+"/"+row.ID)
	}
	if want := "zeta/z-b zeta/z-a alpha/a-1"; strings.Join(chat, " ") != want {
		t.Fatalf("chat order = %v, want %s", chat, want)
	}
	if len(catalog.Image) != 1 || len(catalog.Classifier) != 1 {
		t.Fatalf("image=%d classifier=%d, want 1 each", len(catalog.Image), len(catalog.Classifier))
	}
	img := catalog.Image[0]
	if img.Type != "image" || img.API != "test-images" || img.Headers["X-Test"] != "1" || strings.Join(img.Inputs, ",") != "text,image" || strings.Join(img.Output, ",") != "image,text" || img.InputCost != 3 || img.CacheWrite != 6 || img.InputLimits == nil || img.InputLimits.Images.MaxPerRequest.Value != 4 {
		t.Fatalf("image row = %+v", img)
	}
	cls := catalog.Classifier[0]
	if cls.Type != "classifier" || cls.API != "test-classifier" || cls.ContextWindow != 32000 || cls.InputCost != 7 {
		t.Fatalf("classifier row = %+v", cls)
	}
}

// Upstream model-data.ts:279-281 rejects a key that is not type:id, model-data.ts:189-193 an unknown type, and
// model-data.ts:270-273 the same key in two API groups; flatten would silently drop or replace those models.
func TestParseDataJSONRejectsIdentityDrift(t *testing.T) {
	for name, data := range map[string]struct{ json, want string }{
		"key without type prefix": {`{"openai-completions": {"other": ` + typedChat("model", "p")[len(`"chat:model": `):] + `}}`, "type/id identity"},
		"key names another id":    {`{"openai-completions": {"chat:other": ` + typedChat("model", "p")[len(`"chat:model": `):] + `}}`, "type/id identity"},
		"missing type":            {`{"openai-completions": {"chat:model": {"id": "model", "name": "M", "api": "openai-completions", "provider": "p", "baseUrl": "u", "input": ["text"]}}}`, `expected "chat", "image", or "classifier"`},
		"unknown type":            {`{"openai-completions": {"audio:model": {"type": "audio", "id": "model", "name": "M", "api": "openai-completions", "provider": "p", "baseUrl": "u", "input": ["text"]}}}`, `expected "chat", "image", or "classifier"`},
		"duplicate across groups": {`{"openai-completions": {` + typedChat("model", "p") + `}, "openai-responses": {` + typedChat("model", "p") + `}}`, "more than one API group"},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "p.json")
			writeFile(t, path, data.json)
			if _, err := parseDataJSON(path); err == nil || !strings.Contains(err.Error(), data.want) {
				t.Fatalf("parseDataJSON() error = %v, want %q", err, data.want)
			}
		})
	}
}

// A provider whose shard holds no chat model (typesafe holds only classifier models) is still a barrel key of MODELS, so
// getBuiltinProviders (providers/all.ts:94-96) returns it and builtinProviders() constructs it (providers/all.ts:136-183).
func TestCollectCatalogListsEveryBarrelProviderInBarrelOrder(t *testing.T) {
	classifier := `"classifier:cls-1": {"type": "classifier", "id": "cls-1", "name": "Classifier 1", "api": "test-classifier", "provider": "zeta", "baseUrl": "https://example.test/cls", "input": ["text"], "cost": {"input": 7, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 32000}`
	path := writeTypedCatalog(t, []string{"zeta", "alpha", "empty"}, map[string]string{
		"zeta":  `{"test-classifier": {` + classifier + `}}`,
		"alpha": `{"openai-completions": {` + typedChat("a-1", "alpha") + `}}`,
		"empty": `{}`,
	})
	catalog, err := collectCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(catalog.Providers, " "), "zeta alpha empty"; got != want {
		t.Fatalf("providers = %q, want %q", got, want)
	}
}

func TestEmitProviders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers_generated.go")
	if err := emitProviders(path, "models.generated.js", []string{"zeta", "alpha"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "// Code generated by cmd/gen-models. DO NOT EDIT.\n// Source: models.generated.js\n// Providers: 2\n\npackage ai\n\n// GeneratedProviders lists the catalog barrel's providers in barrel order (providers/all.ts getBuiltinProviders).\nvar GeneratedProviders = []string{\n\t\"zeta\",\n\t\"alpha\",\n}\n"
	if string(got) != want {
		t.Fatalf("providers file =\n%s\nwant\n%s", got, want)
	}
}

// A shard's models carry the shard's provider id (model-data.ts:141-143).
func TestCollectCatalogRejectsProviderMismatch(t *testing.T) {
	path := writeTypedCatalog(t, []string{"alpha"}, map[string]string{"alpha": `{"openai-completions": {` + typedChat("m", "beta") + `}}`})
	if _, err := collectCatalog(path); err == nil || !strings.Contains(err.Error(), `provider "beta"`) {
		t.Fatalf("collectCatalog() error = %v, want provider mismatch", err)
	}
}

func TestEmitImageAndClassifierModels(t *testing.T) {
	dir := t.TempDir()
	imagePath, classifierPath := filepath.Join(dir, "image_models_generated.go"), filepath.Join(dir, "classifier_models_generated.go")
	images := []modelRow{{Type: "image", ID: "img-1", Name: "Image 1", API: "test-images", Provider: "zeta", BaseURL: "https://example.test/img", Headers: map[string]string{"B": "2", "A": "1"}, Inputs: []string{"text", "image"}, Output: []string{"image", "text"}, InputCost: 3, OutputCost: 4, CacheRead: 5, CacheWrite: 6, InputLimits: &jsonModelInputLimits{MaxRequestBytes: jsonOptionalInt{Value: 9, Present: true}}}}
	classifiers := []modelRow{{Type: "classifier", ID: "cls-1", Name: "Classifier 1", API: "test-classifier", Provider: "zeta", BaseURL: "https://example.test/cls", Inputs: []string{"text"}, InputCost: 0.042, ContextWindow: 32000}}
	if err := emitImages(imagePath, "models.generated.js", images); err != nil {
		t.Fatal(err)
	}
	if err := emitClassifiers(classifierPath, "models.generated.js", classifiers); err != nil {
		t.Fatal(err)
	}
	gotImage, err := os.ReadFile(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	wantImage := "// Code generated by cmd/gen-models. DO NOT EDIT.\n// Source: models.generated.js\n// Models: 1\n\npackage ai\n\n// GeneratedImageModels is the static image model catalog (1 entries).\nvar GeneratedImageModels = []ImageModel{\n\t{ID: \"img-1\", Name: \"Image 1\", API: ImageAPI(\"test-images\"), Provider: \"zeta\", BaseURL: \"https://example.test/img\", Headers: map[string]string{\"A\": \"1\", \"B\": \"2\"}, Input: []string{\"text\", \"image\"}, InputLimits: &ModelInputLimits{MaxRequestBytes: 9}, Output: []string{\"image\", \"text\"}, Cost: ModelCost{Input: 3, Output: 4, CacheRead: 5, CacheWrite: 6}},\n}\n"
	if string(gotImage) != wantImage {
		t.Fatalf("image catalog =\n%s\nwant\n%s", gotImage, wantImage)
	}
	gotClassifier, err := os.ReadFile(classifierPath)
	if err != nil {
		t.Fatal(err)
	}
	wantClassifier := "// Code generated by cmd/gen-models. DO NOT EDIT.\n// Source: models.generated.js\n// Models: 1\n\npackage ai\n\n// GeneratedClassifierModels is the static classifier model catalog (1 entries).\nvar GeneratedClassifierModels = []ClassifierModel{\n\t{ID: \"cls-1\", Name: \"Classifier 1\", API: ClassifierAPI(\"test-classifier\"), Provider: \"zeta\", BaseURL: \"https://example.test/cls\", Input: []string{\"text\"}, Cost: ModelCost{Input: 0.042, Output: 0, CacheRead: 0, CacheWrite: 0}, ContextWindow: 32000},\n}\n"
	if string(gotClassifier) != wantClassifier {
		t.Fatalf("classifier catalog =\n%s\nwant\n%s", gotClassifier, wantClassifier)
	}
}

// ImageModel and ClassifierModel carry the base fields plus output or contextWindow, and chat models carry no output
// (model-data.ts:170-172); a catalog field beyond that must fail generation instead of vanishing from the Go catalog.
func TestCollectCatalogRejectsFieldsTheGoModelsCannotCarry(t *testing.T) {
	image := func(extra string) string {
		return `{"test-images": {"image:img": {"type": "image", "id": "img", "name": "Img", "api": "test-images", "provider": "alpha", "baseUrl": "u", "input": ["text"], "output": ["image"], "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}` + extra + `}}}`
	}
	classifier := func(extra string) string {
		return `{"test-classifier": {"classifier:cls": {"type": "classifier", "id": "cls", "name": "Cls", "api": "test-classifier", "provider": "alpha", "baseUrl": "u", "input": ["text"], "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 10` + extra + `}}}`
	}
	for name, row := range map[string]struct{ data, want string }{
		"image compat":          {image(`, "compat": {"supportsStore": false}`), "compat"},
		"image max tokens":      {image(`, "maxTokens": 10`), "maxTokens"},
		"image context window":  {image(`, "contextWindow": 10`), "contextWindow"},
		"classifier thinking":   {classifier(`, "thinkingLevelMap": {"off": null}`), "thinkingLevelMap"},
		"classifier output":     {classifier(`, "output": ["image"]`), "output"},
		"classifier reasoning":  {classifier(`, "reasoning": true`), "reasoning"},
		"chat output modalites": {`{"openai-completions": {` + typedChat("m", "alpha")[:len(typedChat("m", "alpha"))-1] + `, "output": ["text"]}}}`, "output"},
	} {
		t.Run(name, func(t *testing.T) {
			path := writeTypedCatalog(t, []string{"alpha"}, map[string]string{"alpha": row.data})
			if _, err := collectCatalog(path); err == nil || !strings.Contains(err.Error(), "cannot carry") || !strings.Contains(err.Error(), row.want) {
				t.Fatalf("collectCatalog() error = %v, want a field-loss error naming %q", err, row.want)
			}
		})
	}
}

// flattenModelCatalog builds its record with Object.fromEntries, which enumerates canonical array-index ids first, ascending.
func TestCollectCatalogOrdersNumericIDsLikeObjectFromEntries(t *testing.T) {
	path := writeTypedCatalog(t, []string{"alpha"}, map[string]string{"alpha": `{"openai-completions": {` + typedChat("b", "alpha") + `, ` + typedChat("10", "alpha") + `, ` + typedChat("a", "alpha") + `, ` + typedChat("2", "alpha") + `, ` + typedChat("01", "alpha") + `}}`})
	catalog, err := collectCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, row := range catalog.Chat {
		ids = append(ids, row.ID)
	}
	if want := "2 10 b a 01"; strings.Join(ids, " ") != want {
		t.Fatalf("order = %v, want %s", ids, want)
	}
}
