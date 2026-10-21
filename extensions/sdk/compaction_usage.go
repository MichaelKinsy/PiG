package sdk

import (
	"math"
	"slices"
)

// Compaction usage helpers, mirroring Pi's exports of core/compaction/compaction.ts (CompactionSettings, DEFAULT_COMPACTION_SETTINGS,
// calculateContextTokens, shouldCompact, getLastAssistantUsage). They are pure: they read values the caller already holds.

// CompactionSettings is Pi's CompactionSettings.
type CompactionSettings struct {
	Enabled          bool `json:"enabled"`
	ReserveTokens    int  `json:"reserveTokens"`
	KeepRecentTokens int  `json:"keepRecentTokens"`
}

// DefaultCompactionSettings is Pi's DEFAULT_COMPACTION_SETTINGS.
var DefaultCompactionSettings = CompactionSettings{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000}

// CalculateContextTokens is Pi's calculateContextTokens: the native total when it is non-zero, else the sum of the components.
func CalculateContextTokens(usage UsageInfo) int {
	if usage.TotalTokens != 0 {
		return usage.TotalTokens
	}
	return usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
}

// ShouldCompact is Pi's shouldCompact: compaction triggers when the context exceeds the window less the reserve.
func ShouldCompact(contextTokens, contextWindow int, settings CompactionSettings) bool {
	if !settings.Enabled {
		return false
	}
	return contextTokens > contextWindow-settings.ReserveTokens
}

// GetLastAssistantUsage is Pi's getLastAssistantUsage over the session entries Pi hands an extension (for example [ReadonlySessionManager.GetEntries]): the usage of
// the last assistant message that did not abort or fail and whose usage is not all zero, or nil. As in JavaScript's arithmetic, a null token count is 0 and
// a missing or non-numeric one is NaN, so it never makes a usage valid.
func GetLastAssistantUsage(entries []map[string]any) *UsageInfo {
	for _, entry := range slices.Backward(entries) {

		if kind, _ := entry["type"].(string); kind != "message" {
			continue
		}
		message, _ := entry["message"].(map[string]any)
		if role, _ := message["role"].(string); role != "assistant" {
			continue
		}
		if stop, _ := message["stopReason"].(string); stop == "aborted" || stop == "error" {
			continue
		}
		usage, _ := message["usage"].(map[string]any)
		if usage == nil {
			continue
		}
		total := jsNumber(usage["totalTokens"])
		if total == 0 || math.IsNaN(total) {
			total = usageAddend(usage, "input") + usageAddend(usage, "output") + usageAddend(usage, "cacheRead") + usageAddend(usage, "cacheWrite")
		}
		if !(total > 0) {
			continue
		}
		return &UsageInfo{
			Input: tokenCount(usage["input"]), Output: tokenCount(usage["output"]), CacheRead: tokenCount(usage["cacheRead"]),
			CacheWrite: tokenCount(usage["cacheWrite"]), TotalTokens: tokenCount(usage["totalTokens"]),
		}
	}
	return nil
}

func jsNumber(value any) float64 {
	if number, ok := value.(float64); ok {
		return number
	}
	return math.NaN()
}

// usageAddend is a token count as JavaScript's + reads it: null is 0, and a missing (undefined) or non-numeric count is NaN.
func usageAddend(usage map[string]any, key string) float64 {
	if value, present := usage[key]; present && value == nil {
		return 0
	}
	return jsNumber(usage[key])
}

func tokenCount(value any) int {
	if number := jsNumber(value); !math.IsNaN(number) && !math.IsInf(number, 0) {
		return int(number)
	}
	return 0
}
