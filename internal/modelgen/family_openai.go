package modelgen

// Ports packages/ai/catalog/families/openai.ts: OpenAI GPT models, facts that hold on every host that serves them.

import (
	"regexp"
	"strconv"
	"strings"
)

// OpenAILongContextInputThreshold is the prompt size above which OpenAI bills the long-context tier.
const OpenAILongContextInputThreshold = 272000

// WithOpenAILongContextPricing: OpenAI bills prompts above 272K input tokens at 2x input and 1.5x output rates.
func WithOpenAILongContextPricing(cost *Obj) *Obj {
	return Spread(cost).Set("tiers", []any{O(
		"inputTokensAbove", OpenAILongContextInputThreshold,
		"input", RoundCost(cost.Num("input")*2),
		"output", RoundCost(cost.Num("output")*1.5),
		"cacheRead", RoundCost(cost.Num("cacheRead")*2),
		"cacheWrite", RoundCost(cost.Num("cacheWrite")*2),
	)})
}

func rates(input, output, cacheRead, cacheWrite float64) *Obj {
	return O("input", input, "output", output, "cacheRead", cacheRead, "cacheWrite", cacheWrite)
}

// OpenAIStandardCosts keeps current OpenAI prices authoritative until models.dev and passthrough catalogs catch up.
// https://developers.openai.com/api/docs/pricing
func OpenAIStandardCosts() map[string]*Obj {
	return map[string]*Obj{
		"gpt-5.6-luna":  rates(0.2, 1.2, 0.02, 0.25),
		"gpt-5.6-sol":   rates(4, 20, 0.4, 5),
		"gpt-5.6-terra": rates(2, 12, 0.2, 2.5),
		"gpt-6-astra":   rates(10, 50, 1, 12.5),
		"gpt-6-luna":    rates(0.1, 0.5, 0.01, 0.125),
		"gpt-6-sol":     rates(2, 10, 0.2, 2.5),
		"gpt-6.1-sol":   rates(2, 10, 0.1, 2.5),
		"gpt-6.1-terra": rates(1, 5, 0.05, 1.25),
	}
}

// OpenAIStandardCost is the standard price of an OpenAI model with long-context pricing.
func OpenAIStandardCost(id string) *Obj {
	return WithOpenAILongContextPricing(OpenAIStandardCosts()[id])
}

// GPT-5.5 and older put tools loaded through tool search or additional_tools in a namespace named after the tool and reject
// replayed calls without it, so history from other models fails there (#11415). Like Codex, dynamic tool loading therefore starts
// with GPT-5.6, which does not do this.

// OpenAIToolSearchModelIDs are the models that accept tool search and `additional_tools`.
var OpenAIToolSearchModelIDs = map[string]bool{
	"gpt-5.6-sol": true, "gpt-5.6-terra": true, "gpt-5.6-luna": true,
	"gpt-6-astra": true, "gpt-6-sol": true, "gpt-6-luna": true, "gpt-6.1-sol": true, "gpt-6.1-terra": true,
}

// OpenAIMidConvoModelIDs are the models that accept mid-conversation developer messages.
var OpenAIMidConvoModelIDs = func() map[string]bool {
	ids := map[string]bool{"gpt-5.4": true, "gpt-5.4-mini": true, "gpt-5.4-pro": true, "gpt-5.5": true}
	for id := range OpenAIToolSearchModelIDs {
		ids[id] = true
	}
	return ids
}()

func isResponsesAPI(model ChatModel) bool {
	switch model.Str("api") {
	case "openai-responses", "azure-openai-responses", "openai-codex-responses":
		return true
	}
	return false
}

// SupportsOpenAIXhigh reports ids with the xhigh effort.
func SupportsOpenAIXhigh(modelID string) bool {
	for _, fragment := range []string{"gpt-5.2", "gpt-5.3", "gpt-5.4", "gpt-5.5", "gpt-5.6", "gpt-6"} {
		if strings.Contains(modelID, fragment) {
			return true
		}
	}
	return false
}

var gpt6ModelIDs = map[string]bool{"gpt-6-astra": true, "gpt-6-sol": true, "gpt-6-luna": true, "gpt-6.1-sol": true, "gpt-6.1-terra": true}

// applyOpenAIFamilyRules sets the thinking levels of GPT models on any host.
func applyOpenAIFamilyRules(model ChatModel) {
	id, api := model.Str("id"), model.Str("api")
	if (api == "openai-responses" || api == "azure-openai-responses") && strings.HasPrefix(id, "gpt-5") {
		MergeThinking(model, O("off", nil))
	}
	if gpt6ModelIDs[id] && isResponsesAPI(model) {
		// GPT-6 Astra and GPT-6.1 Sol reject reasoning.effort "none"; GPT-6.1 Terra accepts it. All reject "minimal" (verified on
		// Codex, October 2026).
		var off any = "none"
		if id == "gpt-6-astra" || id == "gpt-6.1-sol" {
			off = nil
		}
		MergeThinking(model, O("off", off, "minimal", nil, "low", "low", "medium", "medium", "high", "high", "xhigh", "xhigh", "max", "max"))
	}
	if SupportsOpenAIXhigh(id) {
		MergeThinking(model, O("xhigh", "xhigh"))
	}
	if (strings.Contains(id, "gpt-5.6") || strings.Contains(id, "gpt-6")) && (isResponsesAPI(model) || api == "openai-completions") {
		MergeThinking(model, O("max", "max"))
	}
	if strings.HasSuffix(id, "gpt-5.5-pro") {
		MergeThinking(model, O("off", nil, "minimal", nil, "low", nil))
	}
}

var gptMajor = regexp.MustCompile(`^gpt-(\d+)`)

// EnableOpenAIGrammarTools enables OpenAI custom grammar tools on Responses endpoints. OpenAI rejects `type: "custom"` tools for
// pre-GPT-5 models (gpt-4.x, gpt-4o, o-series).
func EnableOpenAIGrammarTools(model ChatModel) {
	if !isResponsesAPI(model) {
		return
	}
	match := gptMajor.FindStringSubmatch(model.Str("id"))
	if match == nil {
		return
	}
	if major, _ := strconv.ParseFloat(match[1], 64); major < 5 {
		return
	}
	MergeCompat(model, O("supportsOpenAIGrammarTools", true))
}

// EnableOpenAIMidConvoSystemMessages: newer OpenAI Responses models accept developer messages after the conversation has started.
// Proxies that pass them through also pass `additional_tools` items through unchanged, for the models that accept them.
func EnableOpenAIMidConvoSystemMessages(model ChatModel, proxy bool) {
	if api := model.Str("api"); api != "openai-responses" && api != "openai-codex-responses" {
		return
	}
	if !OpenAIMidConvoModelIDs[model.Str("id")] {
		return
	}
	compat := O("supportsMidConvoSystemMessages", true)
	if proxy && OpenAIToolSearchModelIDs[model.Str("id")] {
		compat.Set("supportsAdditionalTools", true)
	}
	MergeCompat(model, compat)
}
