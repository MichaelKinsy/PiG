package main

import (
	"encoding/json"
	"os"

	"github.com/MichaelKinsy/PiG/ai"
)

func main() {
	rows := [][]any{}
	for _, spec := range []string{"anthropic/claude-opus-5-5", "openai/gpt-6-sol", "openai-codex/gpt-6-sol", "openai/gpt-6-luna", "openai-codex/gpt-6-luna"} {
		generated, ok := ai.LookupModelExact(spec)
		if !ok {
			panic("missing model: " + spec)
		}
		m := generated.ToModel()
		c := m.Capabilities
		cost := m.CostRates()
		tiers := [][]any{}
		for _, tier := range cost.Tiers {
			tiers = append(tiers, []any{tier.InputTokensAbove, tier.InputCostPer1M, tier.OutputCostPer1M, tier.CacheReadCostPer1M, tier.CacheWriteCostPer1M})
		}
		rows = append(rows, []any{spec, ai.GetSupportedThinkingLevels(m), m.Input, c.SupportsImages, c.ContextWindow, c.MaxOutputTokens, []any{cost.Input, cost.Output, cost.CacheRead, cost.CacheWrite}, tiers})
	}
	if err := json.NewEncoder(os.Stdout).Encode(rows); err != nil {
		panic(err)
	}
}
