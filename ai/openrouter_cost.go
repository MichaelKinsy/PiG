package ai

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
)

// Ports packages/ai/src/utils/openrouter-cost.ts (pi-mono commit 83ca8020, #9980). pig divergence (D124): Pi 1.1.0 does not have this file.

// openRouterUsageAccounting holds the billing fields OpenRouter adds to streamed usage at runtime. Each field keeps its raw JSON so that
// absent, null and wrongly typed values stay distinct, as openrouter-cost.ts tests them with typeof and === null.
type openRouterUsageAccounting struct {
	Cost        json.RawMessage `json:"cost"`
	IsBYOK      json.RawMessage `json:"is_byok"`
	CostDetails json.RawMessage `json:"cost_details"`
}

// isOpenRouterModel is isOpenRouterModel.
func isOpenRouterModel(provider, baseURL string) bool {
	return provider == "openrouter" || strings.Contains(baseURL, "openrouter.ai")
}

// validOpenRouterCost is isValidCost: a finite number that is not negative.
func validOpenRouterCost(raw json.RawMessage) (float64, bool) {
	var value float64
	if len(raw) == 0 || raw[0] == 'n' || json.Unmarshal(raw, &value) != nil {
		return 0, false
	}
	return value, !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

// applyOpenRouterReportedCost replaces the catalog total with OpenRouter's billed cost when the response contains complete, valid accounting.
func applyOpenRouterReportedCost(usage *Usage, reported openRouterUsageAccounting) {
	cost, ok := validOpenRouterCost(reported.Cost)
	if !ok {
		return
	}
	switch string(bytes.TrimSpace(reported.IsBYOK)) {
	case "false":
		usage.Cost.Total = cost
	case "true":
		var details map[string]json.RawMessage
		if len(reported.CostDetails) == 0 || json.Unmarshal(reported.CostDetails, &details) != nil || details == nil {
			return
		}
		upstream, ok := validOpenRouterCost(details["upstream_inference_cost"])
		if string(bytes.TrimSpace(details["upstream_inference_cost"])) == "null" {
			prompt, promptOK := validOpenRouterCost(details["upstream_inference_prompt_cost"])
			completions, completionsOK := validOpenRouterCost(details["upstream_inference_completions_cost"])
			if !promptOK || !completionsOK {
				return
			}
			upstream, ok = prompt+completions, true
		}
		if !ok || math.IsNaN(upstream) || math.IsInf(upstream, 0) || upstream < 0 {
			return
		}
		usage.Cost.Total = cost + upstream
	}
}
