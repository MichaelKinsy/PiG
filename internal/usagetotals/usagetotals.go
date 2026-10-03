// Package usagetotals sums model usage records.
//
// Ports packages/coding-agent/src/core/usage-totals.ts (combineUsage).
package usagetotals

import "github.com/MichaelKinsy/PiG/ai"

// CombineUsage is the sum of two usages, keeping the optional token splits when either side reports them.
func CombineUsage(first, second ai.Usage) ai.Usage {
	sum := ai.Usage{
		Input: first.Input + second.Input, Output: first.Output + second.Output,
		CacheRead: first.CacheRead + second.CacheRead, CacheWrite: first.CacheWrite + second.CacheWrite,
		TotalTokens: first.TotalTokens + second.TotalTokens,
		Cost: ai.UsageCost{
			Input: first.Cost.Input + second.Cost.Input, Output: first.Cost.Output + second.Cost.Output,
			CacheRead: first.Cost.CacheRead + second.Cost.CacheRead, CacheWrite: first.Cost.CacheWrite + second.Cost.CacheWrite,
			Total: first.Cost.Total + second.Cost.Total,
		},
	}
	if first.CacheWrite1h != nil || second.CacheWrite1h != nil {
		sum.CacheWrite1h = new(deref(first.CacheWrite1h) + deref(second.CacheWrite1h))
	}
	if first.Reasoning != nil || second.Reasoning != nil {
		sum.Reasoning = new(deref(first.Reasoning) + deref(second.Reasoning))
	}
	return sum
}

func deref(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
