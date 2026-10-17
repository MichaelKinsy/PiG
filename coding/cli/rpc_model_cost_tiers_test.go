package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// rpc-mode.ts get_state and get_available_models send the model object as it is: a composed model whose cost has tiers: []
// sends it, and one whose cost has no tiers sends none.
func TestRPCModelValueKeepsCostTiersPresence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tiers []ai.CostTier
		want  string
	}{
		{"empty", []ai.CostTier{}, `"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"tiers":[]}`},
		{"absent", nil, `"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0},`},
	} {
		model := &ai.Model{ID: "m", DisplayName: "M", Input: []string{"text"},
			ProviderMeta: ai.ProviderMetadata{ProviderID: "p", API: ai.APIOpenAICompletions, BaseURL: "https://example.test"},
			Capabilities: ai.ModelCapabilities{ContextWindow: 1, MaxOutputTokens: 1, InputCostPer1M: 1, OutputCostPer1M: 2, CostTiers: tc.tiers}}
		for name, value := range map[string]*RPCModel{"get_state": rpcModelValue(model), "get_available_models": rpcModelList([]*ai.Model{model})[0]} {
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), tc.want) {
				t.Errorf("%s %s model = %s, want it to contain %s", tc.name, name, data, tc.want)
			}
		}
	}
}
