package utils

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestEmptyUsageIsZero(t *testing.T) {
	encoded, err := json.Marshal(EmptyUsage())
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}` {
		t.Fatalf("empty usage = %s", encoded)
	}
}

func TestAddUsageSumsFieldsAndKeepsOptionalAbsence(t *testing.T) {
	left := ai.Usage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, TotalTokens: 10, Cost: ai.UsageCost{Input: 0.5, Output: 1, CacheRead: 0.25, CacheWrite: 0.125, Total: 1.875}}
	right := ai.Usage{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, TotalTokens: 100, Cost: ai.UsageCost{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, Total: 10}}
	sum := AddUsage(left, right)
	if sum.Input != 11 || sum.Output != 22 || sum.CacheRead != 33 || sum.CacheWrite != 44 || sum.TotalTokens != 110 || sum.Cost.Total != 11.875 || sum.Cost.CacheWrite != 4.125 {
		t.Fatalf("sum = %#v", sum)
	}
	if sum.CacheWrite1h != nil || sum.Reasoning != nil {
		t.Fatalf("absent optional fields became present: %#v", sum)
	}
	right.CacheWrite1h = new(7)
	left.Reasoning = new(5)
	sum = AddUsage(left, right)
	if sum.CacheWrite1h == nil || *sum.CacheWrite1h != 7 || sum.Reasoning == nil || *sum.Reasoning != 5 {
		t.Fatalf("optional sums = %#v", sum)
	}
}
