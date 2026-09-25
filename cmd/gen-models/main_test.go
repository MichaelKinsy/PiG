package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCollectRowsAssemblesBarrel proves the 0.80 catalog layout (a barrel
// that imports per-provider files with inline model literals) is parsed
// equivalently to the old inline single-file format. It deliberately mixes a
// tab-indented provider file (.ts mirror) and a 4-space-indented one (.js npm
// dist) so the indentation normalization in the adapter is exercised on both.
func TestCollectRowsAssemblesBarrel(t *testing.T) {
	dir := t.TempDir()
	provDir := filepath.Join(dir, "providers")
	if err := os.MkdirAll(provDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Tab-indented provider (mirrors the .ts source).
	anthropic := "" +
		"export const ANTHROPIC_MODELS = {\n" +
		"\t\"claude-x\": {\n" +
		"\t\tid: \"claude-x\",\n" +
		"\t\tname: \"Claude X\",\n" +
		"\t\tapi: \"anthropic-messages\",\n" +
		"\t\tprovider: \"anthropic\",\n" +
		"\t\tbaseUrl: \"https://api.anthropic.com\",\n" +
		"\t\tcontextWindow: 200000,\n" +
		"\t} satisfies Model<\"anthropic-messages\">,\n" +
		"} as const;\n"
	writeFile(t, filepath.Join(provDir, "anthropic.models.ts"), anthropic)

	// 4-space-indented provider (mirrors the compiled .js dist; no satisfies).
	openai := "" +
		"export const OPENAI_MODELS = {\n" +
		"    \"gpt-z\": {\n" +
		"        id: \"gpt-z\",\n" +
		"        name: \"GPT Z\",\n" +
		"        api: \"openai-responses\",\n" +
		"        provider: \"openai\",\n" +
		"        baseUrl: \"https://api.openai.com\",\n" +
		"        contextWindow: 400000,\n" +
		"    },\n" +
		"} as const;\n"
	writeFile(t, filepath.Join(provDir, "openai.models.ts"), openai)

	barrel := "" +
		"import { ANTHROPIC_MODELS } from \"./providers/anthropic.models.ts\";\n" +
		"import { OPENAI_MODELS } from \"./providers/openai.models.ts\";\n" +
		"export const MODELS = {\n" +
		"\t\"anthropic\": ANTHROPIC_MODELS,\n" +
		"\t\"openai\": OPENAI_MODELS,\n" +
		"} as const;\n"
	barrelPath := filepath.Join(dir, "models.generated.ts")
	writeFile(t, barrelPath, barrel)

	rows, err := collectRows(barrelPath)
	if err != nil {
		t.Fatalf("collectRows: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("parsed %d models, want 2", len(rows))
	}
	byID := map[string]modelRow{}
	for _, row := range rows {
		byID[row.ID] = row
	}

	ant, ok := byID["claude-x"]
	if !ok {
		t.Fatal("claude-x (tab-indented provider) not parsed")
	}
	if ant.Provider != "anthropic" || ant.API != "anthropic-messages" || ant.ContextWindow != 200000 {
		t.Errorf("claude-x = %+v, want provider=anthropic api=anthropic-messages ctx=200000", ant)
	}

	oai, ok := byID["gpt-z"]
	if !ok {
		t.Fatal("gpt-z (4-space-indented provider) not parsed: indentation normalization failed")
	}
	if oai.Provider != "openai" || oai.API != "openai-responses" || oai.ContextWindow != 400000 {
		t.Errorf("gpt-z = %+v, want provider=openai api=openai-responses ctx=400000", oai)
	}
}

// TestCollectRowsInlinePassthrough proves the legacy single-file format
// (no ./providers/ imports) is parsed directly, unchanged.
func TestCollectRowsInlinePassthrough(t *testing.T) {
	dir := t.TempDir()
	inline := "" +
		"export const MODELS = {\n" +
		"\t\"anthropic\": {\n" +
		"\t\t\"claude-y\": {\n" +
		"\t\t\tid: \"claude-y\",\n" +
		"\t\t\tapi: \"anthropic-messages\",\n" +
		"\t\t\tprovider: \"anthropic\",\n" +
		"\t\t\tbaseUrl: \"https://api.anthropic.com\",\n" +
		"\t\t} satisfies Model<\"anthropic-messages\">,\n" +
		"\t},\n" +
		"} as const;\n"
	path := filepath.Join(dir, "models.generated.ts")
	writeFile(t, path, inline)

	rows, err := collectRows(path)
	if err != nil {
		t.Fatalf("collectRows: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "claude-y" || rows[0].Provider != "anthropic" {
		t.Fatalf("inline parse = %+v, want one anthropic/claude-y model", rows)
	}
}

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
	    "model": {
	      "id": "model", "name": "Model", "api": "openai-responses",
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
	    "model": {
	      "id": "model", "name": "Model", "api": "anthropic-messages",
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
			writeFile(t, path, `{"anthropic-messages":{"model":{"id":"model","name":"Model","api":"anthropic-messages","provider":"anthropic","baseUrl":"https://example.test","reasoning":false,"input":["text"],"contextWindow":1000,"maxTokens":100,"inputLimits":`+tt.inputLimits+`}}}`)
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
			writeFile(t, path, `{"anthropic-messages":{"model":{"id":"model","name":"Model","api":"anthropic-messages","provider":"anthropic","baseUrl":"https://example.test","reasoning":false,"input":["text"],"contextWindow":1000,"maxTokens":100,"inputLimits":`+tt.inputLimits+`}}}`)
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
	    "model": {
	      "id": "model", "name": "Model", "api": "anthropic-messages",
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
	    "radius-model": {
	      "id": "radius-model", "name": "Radius Model", "api": "pi-messages",
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
	    "claude-opus-5": {
	      "id": "claude-opus-5", "name": "Claude Opus 5",
	      "api": "anthropic-messages", "provider": "anthropic",
	      "baseUrl": "https://api.anthropic.com", "reasoning": true,
	      "input": ["text", "image"], "contextWindow": 1000000, "maxTokens": 128000,
	      "samplingParams": {"top_p": 0.8, "min_p": 0.1},
	      "cost": {"input": 5, "output": 25, "cacheRead": 0.5, "cacheWrite": 6.25}
	    },
	    "claude-sonnet-5": {
	      "id": "claude-sonnet-5", "name": "Claude Sonnet 5",
	      "api": "anthropic-messages", "provider": "anthropic",
	      "baseUrl": "https://api.anthropic.com", "reasoning": true,
	      "input": ["text", "image"], "contextWindow": 1000000, "maxTokens": 128000
	    }
	  }
	}`)
	barrelPath := filepath.Join(dir, "models.generated.js")
	writeFile(t, barrelPath, "import { ANTHROPIC_MODELS } from \"./providers/anthropic.models.js\";\n")

	rows, err := collectRows(barrelPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ID != "claude-opus-5" || rows[1].ID != "claude-sonnet-5" || rows[0].API != "anthropic-messages" {
		t.Fatalf("collectRows() order = %+v", rows)
	}
	if rows[0].SamplingParams["top_p"] != 0.8 || rows[0].SamplingParams["min_p"] != 0.1 {
		t.Fatalf("collectRows() sampling params = %v", rows[0].SamplingParams)
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
	writeFile(t, filepath.Join(dataDir, "anthropic.json"), `{
	  "claude-j": {
	    "id": "claude-j", "name": "Claude J", "api": "anthropic-messages",
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
	  "claude-k": {
	    "id": "claude-k", "name": "Claude K", "api": "anthropic-messages",
	    "provider": "anthropic", "baseUrl": "https://api.anthropic.com",
	    "contextWindow": 100000, "maxTokens": 8192, "compat": {}
	  }
	}`)

	barrel := "import { ANTHROPIC_MODELS } from \"./providers/anthropic.models.js\";\n" +
		"export const MODELS = { \"anthropic\": ANTHROPIC_MODELS } as const;\n"
	barrelPath := filepath.Join(dir, "models.generated.js")
	writeFile(t, barrelPath, barrel)

	rows, err := collectRows(barrelPath)
	if err != nil {
		t.Fatalf("collectRows: %v", err)
	}
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
