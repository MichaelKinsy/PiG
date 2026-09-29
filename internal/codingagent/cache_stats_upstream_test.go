package codingagent

import (
	"math"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func cacheStatsAssistant(input, read, write int, cost ai.UsageCost, model string, timestamp int64) *agent.AssistantMessage {
	if model == "" {
		model = "test-model"
	}
	return &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{}, API: ai.APIAnthropicMessages, Provider: "test", ModelID: model, Usage: &ai.Usage{Input: input, Output: 10, CacheRead: read, CacheWrite: write, Cost: cost}, StopReason: ai.StopReasonStop, Timestamp: timestamp}
}
func cacheStatsSession(t *testing.T, entries ...any) *Session {
	t.Helper()
	session := NewSession("cache-stats", t.TempDir())
	session.SetCacheReadPriceSource(func(string, string) float64 { return .3 })
	for _, entry := range entries {
		var value any
		if message, ok := entry.(*agent.AssistantMessage); ok {
			value = MessageEntry{SessionEntryBase: SessionEntryBase{Type: "message", ID: "x"}, Message: agent.AgentMessage{Assistant: message}}
		} else {
			value = entry
		}
		if err := session.AppendEntry(value); err != nil {
			t.Fatal(err)
		}
	}
	return session
}
func TestCacheStatsUpstream(t *testing.T) {
	first := cacheStatsAssistant(0, 0, 100000, ai.UsageCost{CacheWrite: .375}, "", 0)
	second := cacheStatsAssistant(0, 100000, 5000, ai.UsageCost{CacheRead: .03, CacheWrite: .019}, "", 60000)
	missTurn := func(timestamp int64) *agent.AssistantMessage {
		return cacheStatsAssistant(0, 0, 110000, ai.UsageCost{CacheWrite: .4125}, "", timestamp)
	}
	// .upstream/v0.87.1/packages/coding-agent/test/cache-stats.test.ts:80
	t.Run("accumulates missed tokens and cost across turns", func(t *testing.T) {
		got := cacheStatsSession(t, first, second, missTurn(120000)).Accounting().CacheWaste
		if got.MissedTokens != 105000 || math.Abs(got.MissedCost-.36225) >= .000005 {
			t.Fatalf("totals = %+v", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/cache-stats.test.ts:89
	t.Run("counts nothing for healthy sessions", func(t *testing.T) {
		got := cacheStatsSession(t, first, second).Accounting().CacheWaste
		if got.MissedTokens != 0 || got.MissedCost != 0 {
			t.Fatalf("totals = %+v", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/cache-stats.test.ts:95
	t.Run("skips the turn after a compaction reset", func(t *testing.T) {
		got := cacheStatsSession(t, first, map[string]any{"type": "compaction", "id": "c", "parentId": nil, "timestamp": ""}, cacheStatsAssistant(0, 0, 20000, ai.UsageCost{CacheWrite: .075}, "", 0)).Accounting().CacheWaste
		if got.MissedTokens != 0 {
			t.Fatalf("totals = %+v", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/cache-stats.test.ts:102
	t.Run("counts misses caused by model switches", func(t *testing.T) {
		got := cacheStatsSession(t, first, cacheStatsAssistant(0, 0, 100000, ai.UsageCost{CacheWrite: .375}, "other-model", 0)).Accounting().CacheWaste
		if got.MissedTokens != 100000 || got.MissCount != 1 {
			t.Fatalf("totals = %+v", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/cache-stats.test.ts:109
	t.Run("skips providers that report no cache activity", func(t *testing.T) {
		got := cacheStatsSession(t, cacheStatsAssistant(100000, 0, 0, ai.UsageCost{}, "", 0), cacheStatsAssistant(110000, 0, 0, ai.UsageCost{}, "", 0)).Accounting().CacheWaste
		if got.MissedTokens != 0 {
			t.Fatalf("totals = %+v", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/cache-stats.test.ts:118
	t.Run("maps counted misses to their assistant messages by reference", func(t *testing.T) {
		message := missTurn(120000)
		misses := collectCacheMisses([]cacheStatsEntry{{kind: "message", message: first}, {kind: "message", message: second}, {kind: "message", message: message}}, func(string, string) float64 { return .3 })
		if len(misses) != 1 || misses[message] == nil || misses[message].missedTokens != 105000 {
			t.Fatalf("misses = %+v", misses)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/cache-stats.test.ts:127
	t.Run("detects a miss on a just-completed message with idle time", func(t *testing.T) {
		got := cacheStatsSession(t, first, second).detectCacheMiss(missTurn(600000))
		if got == nil || got.missedTokens != 105000 || math.Abs(got.missedCost-.36225) >= .000005 || got.idleMillis != 540000 || got.modelChanged {
			t.Fatalf("miss = %+v", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/cache-stats.test.ts:138
	t.Run("flags model switches on detected misses", func(t *testing.T) {
		other := missTurn(120000)
		other.ModelID = "other-model"
		got := cacheStatsSession(t, first, second).detectCacheMiss(other)
		if got == nil || got.missedTokens != 105000 || !got.modelChanged {
			t.Fatalf("miss = %+v", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/cache-stats.test.ts:150
	t.Run("uses only cache-warm usage entries as cache refreshes", func(t *testing.T) {
		for _, kind := range []string{"cache_warm", "custom_operation"} {
			entry := UsageEntry{SessionEntryBase: SessionEntryBase{Type: "usage", ID: "usage-" + kind, Timestamp: time.UnixMilli(500000).UTC().Format(time.RFC3339Nano)}, Kind: kind, Provider: "test", Model: "test-model", Usage: ai.Usage{CacheRead: 100000, TotalTokens: 100000}}
			got := cacheStatsSession(t, first, entry).detectCacheMiss(missTurn(600000))
			want := int64(600000)
			if kind == "cache_warm" {
				want = 100000
			}
			if got == nil || got.idleMillis != want {
				t.Fatalf("%s miss = %+v", kind, got)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/cache-stats.test.ts:164
	t.Run("returns undefined for healthy turns", func(t *testing.T) {
		healthy := cacheStatsAssistant(0, 105000, 2000, ai.UsageCost{CacheRead: .0315, CacheWrite: .0075}, "", 120000)
		if got := cacheStatsSession(t, first, second).detectCacheMiss(healthy); got != nil {
			t.Fatalf("miss = %+v", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/cache-stats.test.ts:174
	t.Run("returns undefined for the first turn of a session", func(t *testing.T) {
		if got := cacheStatsSession(t).detectCacheMiss(first); got != nil {
			t.Fatalf("miss = %+v", got)
		}
	})
}
