package ai

import "slices"

// CostRates returns the model's per-million-token prices in upstream
// Model.cost shape. A nil model has no price.
func (m *Model) CostRates() ModelCost {
	if m == nil {
		return ModelCost{}
	}
	c := m.Capabilities
	return ModelCost{
		Input:      c.InputCostPer1M,
		Output:     c.OutputCostPer1M,
		CacheRead:  c.CacheReadCostPer1M,
		CacheWrite: c.CacheWriteCostPer1M,
		Tiers:      c.CostTiers,
	}
}

// CalculateCost fills usage.Cost from the prices of a chat, image or classifier model and returns it.
// A nil model has no price. Mirrors upstream models.ts:calculateCost(model: AnyModel, usage).
func CalculateCost(m AnyModel, usage *Usage) UsageCost {
	if m == nil {
		return calculateUsageCost(ModelCost{}, usage)
	}
	return calculateUsageCost(m.CostRates(), usage)
}

// calculateUsageCost is models.ts:calculateCost over the model's cost rates:
// the highest tier whose threshold the whole prompt (input+cacheRead+
// cacheWrite) exceeds replaces every base rate, and 1h cache writes bill at
// twice the input rate. The explicit float64 conversions keep each product
// rounded before any later sum, as JavaScript evaluates it, instead of
// letting the compiler fuse a multiply-add across statements.
func calculateUsageCost(rates ModelCost, usage *Usage) UsageCost {
	if usage == nil {
		return UsageCost{}
	}
	inputTokens := usage.Input + usage.CacheRead + usage.CacheWrite
	input, output, cacheRead, cacheWrite := rates.Input, rates.Output, rates.CacheRead, rates.CacheWrite
	matchedThreshold := -1
	for _, tier := range rates.Tiers {
		if inputTokens > tier.InputTokensAbove && tier.InputTokensAbove > matchedThreshold {
			input, output = tier.InputCostPer1M, tier.OutputCostPer1M
			cacheRead, cacheWrite = tier.CacheReadCostPer1M, tier.CacheWriteCostPer1M
			matchedThreshold = tier.InputTokensAbove
		}
	}

	// Anthropic charges 2x base input for 1h cache writes.
	longWrite := 0
	if usage.CacheWrite1h != nil {
		longWrite = *usage.CacheWrite1h
	}
	shortWrite := usage.CacheWrite - longWrite
	usage.Cost.Input = float64((input / 1000000) * float64(usage.Input))
	usage.Cost.Output = float64((output / 1000000) * float64(usage.Output))
	usage.Cost.CacheRead = float64((cacheRead / 1000000) * float64(usage.CacheRead))
	usage.Cost.CacheWrite = (float64(cacheWrite*float64(shortWrite)) + float64(input*2*float64(longWrite))) / 1000000
	usage.Cost.Total = usage.Cost.Input + usage.Cost.Output + usage.Cost.CacheRead + usage.Cost.CacheWrite
	return usage.Cost
}

var extendedThinkingLevels = []ModelThinkingLevel{
	ThinkingOff,
	ThinkingMinimal,
	ThinkingLow,
	ThinkingMedium,
	ThinkingHigh,
	ThinkingXHigh,
	ThinkingMax,
}

// GetSupportedThinkingLevels returns the model's supported thinking levels. Selected model reasoning metadata or a native MaxThinking capability enables reasoning.
func GetSupportedThinkingLevels(m *Model) []ModelThinkingLevel {
	if m == nil || !m.ProviderMeta.Reasoning && m.Capabilities.MaxThinking == "" {
		return []ModelThinkingLevel{ThinkingOff}
	}
	return slices.DeleteFunc(slices.Clone(extendedThinkingLevels), func(level ModelThinkingLevel) bool {
		return thinkingLevelUnsupported(m.ThinkingLevelMap, level)
	})
}

// thinkingLevelUnsupported reports whether levelMap removes level from a reasoning model's supported levels.
func thinkingLevelUnsupported(levelMap ThinkingLevelMap, level ModelThinkingLevel) bool {
	mapped, ok := levelMap[level]
	if ok && mapped == nil {
		return true
	}
	if level == ThinkingXHigh || level == ThinkingMax {
		return !ok || mapped == nil
	}
	return false
}

// ClampThinkingLevel clamps level to the nearest supported thinking level.
// Mirrors upstream models.ts:clampThinkingLevel.
func ClampThinkingLevel(m *Model, level ModelThinkingLevel) ModelThinkingLevel {
	available := GetSupportedThinkingLevels(m)
	if slices.Contains(available, level) {
		return level
	}
	requestedIndex := slices.Index(extendedThinkingLevels, level)
	if requestedIndex == -1 {
		if len(available) == 0 {
			return ThinkingOff
		}
		return available[0]
	}
	for i := requestedIndex; i < len(extendedThinkingLevels); i++ {
		candidate := extendedThinkingLevels[i]
		if slices.Contains(available, candidate) {
			return candidate
		}
	}
	for i := requestedIndex - 1; i >= 0; i-- {
		candidate := extendedThinkingLevels[i]
		if slices.Contains(available, candidate) {
			return candidate
		}
	}
	if len(available) == 0 {
		return ThinkingOff
	}
	return available[0]
}

// ModelsAreEqual compares model IDs and declared provider IDs independently of backend construction. Native models without provider metadata use their backend's provider ID.
// Mirrors upstream models.ts:modelsAreEqual.
func ModelsAreEqual(a, b AnyModel) bool {
	if isNilModel(a) || isNilModel(b) {
		return false
	}
	return GetModelType(a) == GetModelType(b) && a.ModelID() == b.ModelID() && a.ProviderID() == b.ProviderID()
}

func isNilModel(model AnyModel) bool {
	switch typed := model.(type) {
	case nil:
		return true
	case *Model:
		return typed == nil
	case *ImageModel:
		return typed == nil
	case *ClassifierModel:
		return typed == nil
	}
	return false
}
