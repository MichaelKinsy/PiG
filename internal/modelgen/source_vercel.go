package modelgen

// Ports packages/ai/catalog/sources/vercel-ai-gateway.ts

import (
	"math"
	"slices"
)

const (
	aiGatewayModelsURL = "https://ai-gateway.vercel.sh/v1/models"
	aiGatewayBaseURL   = "https://ai-gateway.vercel.sh"
	// TypeSafe-compatible System One endpoint for evaluation models.
	// https://vercel.com/docs/ai-gateway/sdks-and-apis/typesafe
	aiGatewayTypesafeBaseURL = "https://ai-gateway.vercel.sh/typesafe/v1"
)

// aiGatewayRateFields pair each rate with the key listing its prompt-length brackets.
var aiGatewayRateFields = [...]struct{ rate, tiers string }{
	{"input", "input_tiers"},
	{"output", "output_tiers"},
	{"cacheRead", "input_cache_read_tiers"},
	{"cacheWrite", "input_cache_write_tiers"},
}

// aiGatewayPerMillion converts a $/token price (a number or a decimal string) to $/million tokens; an absent or non-finite rate is 0.
func aiGatewayPerMillion(value any) float64 {
	var parsed float64
	switch v := value.(type) {
	case float64:
		parsed = v
	case nil:
		parsed = ParseFloat("0")
	default:
		parsed = ParseFloat(stringify(v))
	}
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0
	}
	return RoundCost(parsed * 1_000_000)
}

// GetAiGatewayCost converts AI Gateway pricing ($/token) to $/million tokens. Each rate lists its own prompt-length brackets; every
// bracket start above zero becomes a request-wide tier with the rates in effect there.
func GetAiGatewayCost(pricing *Obj) *Obj {
	base := [4]float64{
		aiGatewayPerMillion(pricing.member("input")),
		aiGatewayPerMillion(pricing.member("output")),
		aiGatewayPerMillion(pricing.member("input_cache_read")),
		aiGatewayPerMillion(pricing.member("input_cache_write")),
	}
	baseCost := func() *Obj {
		return O("input", base[0], "output", base[1], "cacheRead", base[2], "cacheWrite", base[3])
	}
	brackets := func(field int) []*Obj {
		var out []*Obj
		for _, item := range pricing.List(aiGatewayRateFields[field].tiers) {
			if bracket, ok := item.(*Obj); ok {
				out = append(out, bracket)
			}
		}
		return out
	}

	var starts []float64
	for field := range aiGatewayRateFields {
		for _, bracket := range brackets(field) {
			if minimum, ok := bracket.member("min").(float64); ok && minimum > 0 && !slices.Contains(starts, minimum) {
				starts = append(starts, minimum)
			}
		}
	}
	slices.Sort(starts)

	var tiers []any
	for _, start := range starts {
		tier := Spread(O("inputTokensAbove", start-1), baseCost())
		for field, spec := range aiGatewayRateFields {
			for _, bracket := range brackets(field) {
				minimum, _ := bracket.member("min").(float64)
				maximum, hasMax := bracket.member("max").(float64)
				if minimum <= start && (!hasMax || start < maximum) {
					if cost, present := bracket.Get("cost"); present {
						tier.Set(spec.rate, aiGatewayPerMillion(cost))
					}
					break
				}
			}
		}
		tiers = append(tiers, tier)
	}
	cost := baseCost()
	if len(tiers) > 0 {
		cost.Set("tiers", tiers)
	}
	return cost
}

// AiGatewayCatalog is the tool-capable chat models and the evaluation (classifier) models of the Vercel AI Gateway.
type AiGatewayCatalog struct {
	Chat, Classifiers []*Obj
}

func nameOrID(model *Obj) string {
	if name := model.Str("name"); name != "" {
		return name
	}
	return model.Str("id")
}

// BuildAiGatewayCatalog converts the listing: an evaluation model is a classifier served through the TypeSafe-compatible System One
// endpoint, and any other model becomes a chat model when it carries the tool-use tag.
func BuildAiGatewayCatalog(items []*Obj) AiGatewayCatalog {
	catalog := AiGatewayCatalog{Chat: []*Obj{}, Classifiers: []*Obj{}}
	for _, model := range items {
		// Evaluation models such as TypeSafe's Jev are served through the TypeSafe-compatible System One endpoint.
		if model.Str("type") == "evaluation" {
			pricing := objectOf(model.member("pricing"))
			catalog.Classifiers = append(catalog.Classifiers, O(
				"type", "classifier",
				"id", model.Str("id"),
				"name", nameOrID(model),
				"api", "typesafe-system-one",
				"provider", "vercel-ai-gateway",
				"baseUrl", aiGatewayTypesafeBaseURL,
				"input", Strings("text"),
				"cost", O("input", aiGatewayPerMillion(pricing.member("input")), "output", aiGatewayPerMillion(pricing.member("output")), "cacheRead", 0, "cacheWrite", 0),
				"contextWindow", orFallback(model.member("context_window"), 4096),
			))
			continue
		}
		// Only include models that support tools
		if !model.Includes("tags", "tool-use") {
			continue
		}
		input := Strings("text")
		if model.Includes("tags", "vision") {
			input = Strings("text", "image")
		}
		catalog.Chat = append(catalog.Chat, O(
			"id", model.Str("id"),
			"name", nameOrID(model),
			"api", "anthropic-messages",
			"baseUrl", aiGatewayBaseURL,
			"provider", "vercel-ai-gateway",
			"reasoning", model.Includes("tags", "reasoning"),
			"input", input,
			"compat", O("allowEmptySignature", true),
			"cost", GetAiGatewayCost(objectOf(model.member("pricing"))),
			"contextWindow", orFallback(model.member("context_window"), 4096),
			"maxTokens", orFallback(model.member("max_tokens"), 4096),
		))
	}
	return catalog
}

// FetchAiGatewayCatalog fetches tool-capable chat models and evaluation (classifier) models.
func FetchAiGatewayCatalog() (AiGatewayCatalog, error) {
	data, err := fetchJSON(aiGatewayModelsURL, "Vercel AI Gateway API")
	if err != nil {
		return AiGatewayCatalog{}, err
	}
	var items []*Obj
	for _, item := range listOf(objectOf(data).member("data")) {
		if object, ok := item.(*Obj); ok {
			items = append(items, object)
		}
	}
	return BuildAiGatewayCatalog(items), nil
}
