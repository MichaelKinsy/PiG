package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// modelObjectMembers decodes a model object's members.
func modelObjectMembers(t *testing.T, object json.RawMessage) map[string]any {
	t.Helper()
	var members map[string]any
	if err := json.Unmarshal(object, &members); err != nil {
		t.Fatalf("model object %s: %v", object, err)
	}
	return members
}

func TestModelObjectPreservesPiModelShape(t *testing.T) {
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
	object, err := extension.ModelObject(model)
	if err != nil {
		t.Fatal(err)
	}
	got := modelObjectMembers(t, object)
	if got["id"] != model.ID {
		t.Errorf("serialized id = %v, want %q", got["id"], model.ID)
	}
	for _, field := range []string{"baseUrl", "input", "cost", "thinkingLevelMap", "samplingParams", "promptCache", "headers", "compat"} {
		if _, ok := got[field]; !ok {
			t.Errorf("serialized model is missing %q: %s", field, object)
		}
	}
	// Pi's model object has none of PiG's former aliases.
	for _, field := range []string{"modelId", "displayName", "maxOutputTokens", "inputCostPer1M", "outputCostPer1M", "cacheReadCostPer1M", "cacheWriteCostPer1M"} {
		if _, ok := got[field]; ok {
			t.Errorf("serialized model carries %q: %s", field, object)
		}
	}
	if cost, ok := got["cost"].(map[string]any); !ok || len(cost["tiers"].([]any)) != 1 {
		t.Errorf("serialized cost = %#v", got["cost"])
	}
}

func TestHeadlessModelCatalogUsesCompleteProjection(t *testing.T) {
	agentDir := t.TempDir()
	config := `{"providers":{"custom":{"baseUrl":"https://proxy.invalid/v1","api":"openai-responses","authHeader":false,"models":[{"id":"org/model/name","name":"Configured slash model","reasoning":false,"input":[],"cost":{"input":0,"output":7,"cacheRead":0.5,"cacheWrite":1.5,"tiers":[{"inputTokensAbove":1000,"input":1,"output":2,"cacheRead":0,"cacheWrite":0}]},"promptCache":{"short":120,"long":3600},"contextWindow":321000,"maxTokens":1234,"samplingParams":{"temperature":0},"headers":{"X-Test":"value"},"compat":{"supportsStrictMode":false}}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	bridge := subprocess.NewUIBridge(func() {})
	session := newModelRegistryTestSession(t, services)
	detach := wireSubprocessModelRegistry(bridge, session, services)
	defer detach()
	var got map[string]any
	for _, object := range bridge.ModelCatalog() {
		model := modelObjectMembers(t, object)
		if model["provider"] == "custom" && model["id"] == "org/model/name" {
			got = model
			break
		}
	}
	if got == nil {
		t.Fatal("slash-containing model is absent from headless catalog")
	}
	for _, field := range []string{"baseUrl", "input", "cost", "samplingParams", "promptCache", "compat"} {
		if _, ok := got[field]; !ok {
			t.Errorf("serialized model is missing %q: %#v", field, got)
		}
	}
	if input, ok := got["input"].([]any); !ok || len(input) != 0 {
		t.Errorf("serialized input = %#v, want explicit empty array", got["input"])
	}
	cost, ok := got["cost"].(map[string]any)
	if !ok || cost["input"] != float64(0) || cost["output"] != float64(7) || len(cost["tiers"].([]any)) != 1 {
		t.Errorf("serialized cost = %#v", got["cost"])
	}
}
