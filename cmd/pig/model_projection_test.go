package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func TestModelInfoPreservesPiModelShape(t *testing.T) {
	strict := false
	model := &ai.Model{
		ID:          "org/model/name",
		DisplayName: "Configured slash model",
		Capabilities: ai.ModelCapabilities{
			SupportsImages:      true,
			ContextWindow:       321000,
			MaxOutputTokens:     1234,
			InputCostPer1M:      0,
			OutputCostPer1M:     7,
			CacheReadCostPer1M:  0.5,
			CacheWriteCostPer1M: 1.5,
			CostTiers:           []ai.CostTier{{InputTokensAbove: 1000, InputCostPer1M: 1, OutputCostPer1M: 2}},
		},
		ThinkingLevelMap: ai.ThinkingLevelMap{ai.ThinkingHigh: new("configured-high")},
		SamplingParams:   map[string]any{"temperature": float64(0)},
		PromptCache:      ai.ModelPromptCache{"short": 120, "long": 3600},
		ProviderMeta: ai.ProviderMetadata{
			ProviderID: "openrouter",
			API:        ai.APIOpenAIResponses,
			BaseURL:    "https://proxy.invalid/v1",
			Headers:    map[string]string{"X-Test": "value"},
			Compat:     &ai.OpenAICompat{SupportsStrictMode: &strict},
		},
	}
	got := extension.ModelInfo(model)
	if got["id"] != model.ID || got["modelId"] != model.ID {
		t.Errorf("serialized identity = id:%v modelId:%v, want %q", got["id"], got["modelId"], model.ID)
	}
	for _, field := range []string{"baseUrl", "input", "cost", "thinkingLevelMap", "samplingParams", "promptCache", "headers", "compat"} {
		if _, ok := got[field]; !ok {
			t.Errorf("serialized model is missing %q: %#v", field, got)
		}
	}
	if cost, ok := got["cost"].(map[string]any); ok && !reflect.DeepEqual(cost["tiers"], model.Capabilities.CostTiers) {
		t.Errorf("serialized cost tiers = %#v", cost["tiers"])
	}
}

func TestHeadlessModelCatalogUsesCompleteProjection(t *testing.T) {
	agentDir := t.TempDir()
	config := `{"providers":{"custom":{"baseUrl":"https://proxy.invalid/v1","api":"openai-responses","authHeader":false,"models":[{"id":"org/model/name","name":"Configured slash model","reasoning":false,"input":[],"cost":{"input":0,"output":7,"cacheRead":0.5,"cacheWrite":1.5,"tiers":[{"inputTokensAbove":1000,"input":1,"output":2,"cacheRead":0,"cacheWrite":0}]},"promptCache":{"short":120,"long":3600},"contextWindow":321000,"maxTokens":1234,"samplingParams":{"temperature":0},"headers":{"X-Test":"value"},"compat":{"supportsStrictMode":false}}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	bridge := subprocess.NewUIBridge(func() {})
	session := newModelRegistryTestSession(t, services)
	detach := wireSubprocessModelRegistry(bridge, session, services)
	defer detach()
	var got map[string]any
	for _, model := range bridge.ModelCatalog() {
		if model["provider"] == "custom" && model["id"] == "org/model/name" {
			got = model
			break
		}
	}
	if got == nil {
		t.Fatal("slash-containing model is absent from headless catalog")
	}
	for _, field := range []string{"baseUrl", "input", "cost", "thinkingLevelMap", "samplingParams", "promptCache", "headers", "compat"} {
		if _, ok := got[field]; !ok {
			t.Errorf("serialized model is missing %q: %#v", field, got)
		}
	}
	if input, ok := got["input"].([]string); !ok || input == nil || len(input) != 0 {
		t.Errorf("serialized input = %#v, want explicit empty array", got["input"])
	}
	cost, ok := got["cost"].(map[string]any)
	if !ok || cost["input"] != float64(0) || cost["output"] != float64(7) || len(cost["tiers"].([]ai.CostTier)) != 1 {
		t.Errorf("serialized cost = %#v", got["cost"])
	}
}
