package ai

//
// upstream: ai/src/providers/simple-options.ts

import (
	"cmp"
	"encoding/json"
	"maps"
	"strconv"
)

// ThinkingBudgets maps thinking level names to token budgets. Upstream's ThinkingBudgets has an optional number per level, and a configured budget wins over the default whatever its value (simple-options.ts:82 spreads it over
// the defaults; google-generative-ai.ts:436 tests !== undefined), so a budget of zero or below is a real setting. A Go int cannot tell "0" from "unset", so Set records the levels configured explicitly; a level
// also counts as configured when its field is non-zero, which keeps a literal with only the level fields working. Use [NewThinkingBudgets] to build the value from optional settings.
type ThinkingBudgets struct {
	Minimal int
	Low     int
	Medium  int
	High    int
	// Set flags the levels whose budget was configured, including zero and negative budgets.
	Set ThinkingBudgetLevels
}

// ThinkingBudgetLevels is a set of thinking levels that have a configured budget.
type ThinkingBudgetLevels uint8

// The levels of ThinkingBudgets that can carry a configured budget.
const (
	ThinkingBudgetMinimalSet ThinkingBudgetLevels = 1 << iota
	ThinkingBudgetLowSet
	ThinkingBudgetMediumSet
	ThinkingBudgetHighSet
)

// NewThinkingBudgets builds the custom budgets from optional per-level settings: a nil level is unset, any other value (zero and negative included) is configured. It returns nil when no level is set.
func NewThinkingBudgets(minimal, low, medium, high *int) *ThinkingBudgets {
	budgets := thinkingBudgetsOf(minimal, low, medium, high)
	if budgets.Set == 0 {
		return nil
	}
	return &budgets
}

func thinkingBudgetsOf(minimal, low, medium, high *int) ThinkingBudgets {
	var budgets ThinkingBudgets
	for _, level := range []struct {
		value *int
		field *int
		flag  ThinkingBudgetLevels
	}{{minimal, &budgets.Minimal, ThinkingBudgetMinimalSet}, {low, &budgets.Low, ThinkingBudgetLowSet}, {medium, &budgets.Medium, ThinkingBudgetMediumSet}, {high, &budgets.High, ThinkingBudgetHighSet}} {
		if level.value != nil {
			*level.field = *level.value
			budgets.Set |= level.flag
		}
	}
	return budgets
}

// MarshalJSON writes the configured levels as upstream's ThinkingBudgets object ({"minimal":n,...}) in the declaration order minimal, low, medium, high; a configured zero is written, an unset level is omitted.
func (b ThinkingBudgets) MarshalJSON() ([]byte, error) {
	out := []byte{'{'}
	for _, level := range [...]ModelThinkingLevel{ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh} {
		if value, ok := b.configured(level); ok {
			if len(out) > 1 {
				out = append(out, ',')
			}
			out = append(append(append(out, '"'), level...), '"', ':')
			out = strconv.AppendInt(out, int64(value), 10)
		}
	}
	return append(out, '}'), nil
}

// UnmarshalJSON reads upstream's ThinkingBudgets object: a present number is a configured level, whatever its value; an absent or null level is unset.
func (b *ThinkingBudgets) UnmarshalJSON(data []byte) error {
	var values map[string]*int
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	*b = thinkingBudgetsOf(values["minimal"], values["low"], values["medium"], values["high"])
	return nil
}

// configured is customBudgets?.[level] !== undefined for one of the four token-budget levels, without clamping the level.
func (b *ThinkingBudgets) configured(level ModelThinkingLevel) (int, bool) {
	if b == nil {
		return 0, false
	}
	var value int
	var flag ThinkingBudgetLevels
	switch level {
	case ThinkingMinimal:
		value, flag = b.Minimal, ThinkingBudgetMinimalSet
	case ThinkingLow:
		value, flag = b.Low, ThinkingBudgetLowSet
	case ThinkingMedium:
		value, flag = b.Medium, ThinkingBudgetMediumSet
	case ThinkingHigh:
		value, flag = b.High, ThinkingBudgetHighSet
	default:
		return 0, false
	}
	return value, b.Set&flag != 0 || value != 0
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

// ThinkingBudgetForLevel is the token budget for a reasoning level: a configured budget replaces the default, whatever its value, and xhigh and max use the high budget. A level without a budget (for example "off") yields zero.
// Mirrors upstream thinkingBudgetForLevel (api/simple-options.ts:81).
func ThinkingBudgetForLevel(reasoningLevel string, custom *ThinkingBudgets) int {
	level := ClampReasoning(ModelThinkingLevel(reasoningLevel))
	if budget, ok := custom.configured(level); ok {
		return budget
	}
	defaults := DefaultThinkingBudgets()
	switch level {
	case ThinkingMinimal:
		return defaults.Minimal
	case ThinkingLow:
		return defaults.Low
	case ThinkingMedium:
		return defaults.Medium
	case ThinkingHigh:
		return defaults.High
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
