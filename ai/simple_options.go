package ai

//
// upstream: ai/src/providers/simple-options.ts

import (
	"cmp"
	"maps"
)

// ThinkingBudgets maps thinking level names to token budgets.
type ThinkingBudgets struct {
	Minimal int
	Low     int
	Medium  int
	High    int
}

// DefaultThinkingBudgets returns the default budget map.
// Mirrors upstream defaultBudgets (simple-options.ts:32-37).
func DefaultThinkingBudgets() ThinkingBudgets {
	return ThinkingBudgets{
		Minimal: 1024,
		Low:     2048,
		Medium:  8192,
		High:    16384,
	}
}

// MinAnswerTokens is the number of tokens always left for the answer when a thinking budget shares the response ceiling.
// Mirrors upstream MIN_ANSWER_TOKENS (simple-options.ts:68).
const MinAnswerTokens = 1024

// ClampThinkingBudgetToAnswerRoom caps a thinking budget so at least [MinAnswerTokens] remain under a shared response
// ceiling. Mirrors upstream clampThinkingBudgetToAnswerRoom (simple-options.ts:88).
func ClampThinkingBudgetToAnswerRoom(thinkingBudget, ceiling int) int {
	return min(thinkingBudget, max(0, ceiling-MinAnswerTokens))
}

// ThinkingBudgetForLevel is the token budget for a reasoning level: a custom budget above zero replaces the default, and
// xhigh and max use the high budget. A level without a budget (for example "off") yields zero.
// Mirrors upstream thinkingBudgetForLevel (api/simple-options.ts:81).
func ThinkingBudgetForLevel(reasoningLevel string, custom *ThinkingBudgets) int {
	budgets := DefaultThinkingBudgets()
	if custom != nil {
		if custom.Minimal > 0 {
			budgets.Minimal = custom.Minimal
		}
		if custom.Low > 0 {
			budgets.Low = custom.Low
		}
		if custom.Medium > 0 {
			budgets.Medium = custom.Medium
		}
		if custom.High > 0 {
			budgets.High = custom.High
		}
	}
	switch string(ClampReasoning(ModelThinkingLevel(reasoningLevel))) {
	case string(ThinkingMinimal):
		return budgets.Minimal
	case string(ThinkingLow):
		return budgets.Low
	case string(ThinkingMedium):
		return budgets.Medium
	case string(ThinkingHigh):
		return budgets.High
	}
	return 0
}

// ClampReasoning maps the levels above high, xhigh and max, to high and returns every other level, including the empty "undefined" level, unchanged.
// Mirrors upstream clampReasoning (simple-options.ts:77).
func ClampReasoning(effort ModelThinkingLevel) ModelThinkingLevel {
	if effort == ThinkingXHigh || effort == ThinkingMax {
		return ThinkingHigh
	}
	return effort
}

// AdjustMaxTokensForThinking computes maxTokens and thinkingBudget given an
// optional caller max, model limit, thinking level, and optional custom budgets.
// A nil base max means "unset": use the model cap and fit the thinking budget
// inside it. Mirrors upstream adjustMaxTokensForThinking (simple-options.ts:24-47).
func AdjustMaxTokensForThinking(baseMaxTokens *int, modelMaxTokens int, reasoningLevel string, custom *ThinkingBudgets) (maxTokens, thinkingBudget int) {
	thinkingBudget = ThinkingBudgetForLevel(reasoningLevel, custom)

	if baseMaxTokens == nil {
		maxTokens = modelMaxTokens
	} else {
		maxTokens = min(*baseMaxTokens+thinkingBudget, modelMaxTokens)
	}
	if maxTokens <= thinkingBudget {
		thinkingBudget = ClampThinkingBudgetToAnswerRoom(thinkingBudget, maxTokens)
	}
	return maxTokens, thinkingBudget
}

// Upstream simple-options.ts request budget constants.
const (
	contextSafetyTokens = 4096
	minMaxTokens        = 1
)

// ClampMaxTokensToContext reduces a requested output budget to what fits in
// the model's context window after the estimated request context and a safety
// margin, never below one token. Mirrors upstream clampMaxTokensToContext,
// which buildBaseOptions applies to every request.
func ClampMaxTokensToContext(model *Model, context TranscriptContext, maxTokens int) int {
	contextWindow := model.Capabilities.ContextWindow
	if contextWindow <= 0 {
		return max(minMaxTokens, maxTokens)
	}
	available := contextWindow - EstimateContextTokens(context.messages).Tokens - contextSafetyTokens
	return min(maxTokens, max(minMaxTokens, available))
}

// ResolveSamplingParams merges the model's sampling defaults, the overrides for the effective thinking level, and the request's sampling keys, later keys winning. The level is first clamped to one the model supports. It returns nil when none of the three is set.
// Mirrors upstream resolveSamplingParams (simple-options.ts:24-34).
func ResolveSamplingParams(model *Model, thinkingLevel ModelThinkingLevel, requestParams SamplingParams) SamplingParams {
	var defaults SamplingParams
	var levelParams SamplingParams
	if model != nil {
		defaults = model.SamplingParams
		levelParams = model.SamplingParamsByThinkingLevel[ClampThinkingLevel(model, thinkingLevel)]
	}
	if defaults == nil && levelParams == nil && requestParams == nil {
		return nil
	}
	merged := make(SamplingParams, len(defaults)+len(levelParams)+len(requestParams))
	maps.Copy(merged, defaults)
	maps.Copy(merged, levelParams)
	maps.Copy(merged, requestParams)
	return merged
}

// BuildBaseOptions returns the request options every simple stream sends: the output budget is the requested one, or the model's maximum when none was requested, clamped to the context window; the sampling parameters are resolved for the simple reasoning level, "off" when it is unset; and apiKey, when not empty, replaces options.APIKey. Go has one StreamOptions type for the simple and the provider-specific options, so every other field passes through unchanged.
//
// upstream: packages/ai/src/api/simple-options.ts:buildBaseOptions
func BuildBaseOptions(model *Model, context TranscriptContext, options StreamOptions, apiKey string) StreamOptions {
	if options.MaxTokens == 0 {
		options.MaxTokens = model.Capabilities.MaxOutputTokens
	}
	options.MaxTokens = ClampMaxTokensToContext(model, context, options.MaxTokens)
	options.SamplingParams = ResolveSamplingParams(model, cmp.Or(ModelThinkingLevel(options.Thinking), ThinkingOff), options.SamplingParams)
	if apiKey != "" {
		options.APIKey = apiKey
	}
	return options
}
