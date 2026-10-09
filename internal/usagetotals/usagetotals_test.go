package usagetotals

// pi: packages/coding-agent/src/core/usage-totals.ts

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestCombineUsageSumsEveryFieldAndKeepsAbsentSplitsAbsent(t *testing.T) {
	left := ai.Usage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, TotalTokens: 10, Cost: ai.UsageCost{Input: 0.5, Output: 1, CacheRead: 0.25, CacheWrite: 0.125, Total: 1.875}}
	right := ai.Usage{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, TotalTokens: 100, Cost: ai.UsageCost{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, Total: 10}}
	sum := CombineUsage(left, right)
	want := ai.Usage{Input: 11, Output: 22, CacheRead: 33, CacheWrite: 44, TotalTokens: 110, Cost: ai.UsageCost{Input: 1.5, Output: 3, CacheRead: 3.25, CacheWrite: 4.125, Total: 11.875}}
	if sum != want {
		t.Fatalf("sum = %#v, want %#v", sum, want)
	}
}

func TestCombineUsageKeepsASplitEitherSideReports(t *testing.T) {
	for name, tc := range map[string]struct {
		first, second ai.Usage
		cacheWrite1h  *int
		reasoning     *int
	}{
		"first only":  {ai.Usage{CacheWrite1h: new(7)}, ai.Usage{Reasoning: new(5)}, new(7), new(5)},
		"second only": {ai.Usage{Reasoning: new(5)}, ai.Usage{CacheWrite1h: new(7)}, new(7), new(5)},
		"both":        {ai.Usage{CacheWrite1h: new(1), Reasoning: new(2)}, ai.Usage{CacheWrite1h: new(3), Reasoning: new(4)}, new(4), new(6)},
		"explicit 0":  {ai.Usage{CacheWrite1h: new(0)}, ai.Usage{}, new(0), nil},
	} {
		t.Run(name, func(t *testing.T) {
			sum := CombineUsage(tc.first, tc.second)
			if !equalOptional(sum.CacheWrite1h, tc.cacheWrite1h) || !equalOptional(sum.Reasoning, tc.reasoning) {
				t.Fatalf("cacheWrite1h=%v reasoning=%v, want %v and %v", sum.CacheWrite1h, sum.Reasoning, tc.cacheWrite1h, tc.reasoning)
			}
		})
	}
}

func equalOptional(a, b *int) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
