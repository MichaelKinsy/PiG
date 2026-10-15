package coding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: provider-composer.ts:233 gives a models.json model its authored cost object, and provider-composer.ts:188-194 a
// modelOverrides cost `tiers: override.cost.tiers ?? model.cost.tiers`, so an authored `tiers: []` reaches ctx.model as [] and
// a cost that names no tiers has none (Pi 1.2.0's ModelRuntime prints these model objects for this models.json).
func TestModelObjectKeepsAnAuthoredEmptyCostTiers(t *testing.T) {
	agentDir := t.TempDir()
	config := `{"providers":{
		"custom":{"baseUrl":"https://example.test/v1","api":"openai-completions","apiKey":"k","models":[
			{"id":"empty","cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"tiers":[]}},
			{"id":"absent","cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0}}]},
		"openai":{"modelOverrides":{"gpt-5.4":{"cost":{"tiers":[]}},"gpt-5.4-mini":{"cost":{"input":1}}}}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	for _, tc := range []struct{ provider, id, want string }{
		{"custom", "empty", `"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"tiers":[]}`},
		{"custom", "absent", `"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0},`},
		{"openai", "gpt-5.4", `"cost":{"input":2.5,"output":15,"cacheRead":0.25,"cacheWrite":0,"tiers":[]}`},
		{"openai", "gpt-5.4-mini", `"cost":{"input":1,"output":4.5,"cacheRead":0.075,"cacheWrite":0},`},
	} {
		model := services.ModelRuntime().GetModel(tc.provider, tc.id)
		if model == nil {
			t.Fatalf("%s/%s is absent", tc.provider, tc.id)
		}
		object, err := extension.ModelObject(model)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(object), tc.want) {
			t.Errorf("%s/%s model object = %s\nwant it to contain %s", tc.provider, tc.id, object, tc.want)
		}
	}
}
