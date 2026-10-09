package codingagent

import (
	"bytes"
	"encoding/json"
	"math"
	"os/exec"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/coding-agent/src/core/cache-stats.ts

// collectCacheMisses against pinned Pi over generated sessions: assistant turns with and without cache activity, token counts around the
// noise floor, model switches, idle gaps, cache_warm usage entries (also with no prompt tokens), and compaction / branch_summary resets.
func TestCollectCacheMissesMatchesPi(t *testing.T) {
	state := uint64(0x9e3779b97f4a7c15)
	next := func(n int) int {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		return int(state % uint64(n))
	}
	pick := func(values ...int) int { return values[next(len(values))] }
	costs := []float64{0, 0.001, 0.0375, 0.375, 1.5}
	prices := map[string]float64{"a/m1": 0.3, "a/m2": 3, "b/m1": 0.03}
	type wireUsage struct {
		Input      int `json:"input"`
		Output     int `json:"output"`
		CacheRead  int `json:"cacheRead"`
		CacheWrite int `json:"cacheWrite"`
		Cost       struct {
			Input      float64 `json:"input"`
			Output     float64 `json:"output"`
			CacheRead  float64 `json:"cacheRead"`
			CacheWrite float64 `json:"cacheWrite"`
			Total      float64 `json:"total"`
		} `json:"cost"`
		TotalTokens int `json:"totalTokens"`
	}
	type wireMessage struct {
		Role      string    `json:"role"`
		Provider  string    `json:"provider"`
		Model     string    `json:"model"`
		Timestamp int64     `json:"timestamp"`
		Usage     wireUsage `json:"usage"`
	}
	type wireEntry struct {
		Type      string       `json:"type"`
		Kind      string       `json:"kind,omitempty"`
		Provider  string       `json:"provider,omitempty"`
		Model     string       `json:"model,omitempty"`
		Timestamp string       `json:"timestamp,omitempty"`
		Usage     *wireUsage   `json:"usage,omitempty"`
		Message   *wireMessage `json:"message,omitempty"`
	}
	randomUsage := func() wireUsage {
		var u wireUsage
		u.Input = pick(0, 0, 500, 1500, 5000, 20000, 100000)
		u.CacheRead = pick(0, 0, 500, 1500, 5000, 20000, 100000)
		u.CacheWrite = pick(0, 0, 500, 1500, 5000, 20000, 110000)
		u.Output = 10
		u.Cost.Input, u.Cost.CacheRead, u.Cost.CacheWrite = costs[next(len(costs))], costs[next(len(costs))], costs[next(len(costs))]
		return u
	}
	providers, models := []string{"a", "b", "c"}, []string{"m1", "m2"}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var sessions [][]wireEntry
	for range 800 {
		var entries []wireEntry
		clock := int64(0)
		for range 1 + next(14) {
			clock += int64(pick(0, 1000, 60_000, 300_000, 301_000, 900_000))
			switch r := next(20); {
			case r < 14:
				entries = append(entries, wireEntry{Type: "message", Message: &wireMessage{Role: "assistant", Provider: providers[next(3)], Model: models[next(2)], Timestamp: clock, Usage: randomUsage()}})
			case r < 16:
				entries = append(entries, wireEntry{Type: "compaction"})
			case r < 17:
				entries = append(entries, wireEntry{Type: "branch_summary"})
			default:
				u := randomUsage()
				entries = append(entries, wireEntry{Type: "usage", Kind: "cache_warm", Provider: providers[next(3)], Model: models[next(2)], Usage: &u,
					Timestamp: base.Add(time.Duration(clock) * time.Millisecond).Format("2006-01-02T15:04:05.000Z")})
			}
		}
		sessions = append(sessions, entries)
	}
	// Misses at the noise floor: the turn re-bills exactly 1023, 1024 and 1025 tokens of the previous prompt.
	for _, rebilled := range []int{1023, 1024, 1025} {
		var first, second wireUsage
		first.Input, first.CacheWrite = 0, 10000
		first.Cost.CacheWrite = 0.0375
		second.CacheRead, second.Input = 10000-rebilled, rebilled
		second.Cost.CacheRead, second.Cost.Input = 0.003, 0.003
		sessions = append(sessions, []wireEntry{
			{Type: "message", Message: &wireMessage{Role: "assistant", Provider: "a", Model: "m1", Timestamp: 0, Usage: first}},
			{Type: "message", Message: &wireMessage{Role: "assistant", Provider: "a", Model: "m1", Timestamp: 1000, Usage: second}},
		})
	}
	input, err := json.Marshal(map[string]any{"prices": prices, "sessions": sessions})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/cache_stats.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]*struct {
		MissedTokens int     `json:"missedTokens"`
		MissedCost   float64 `json:"missedCost"`
		IdleMs       int64   `json:"idleMs"`
		ModelChanged bool    `json:"modelChanged"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	toUsage := func(u wireUsage) ai.Usage {
		return ai.Usage{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, TotalTokens: 0,
			Cost: ai.UsageCost{Input: u.Cost.Input, Output: u.Cost.Output, CacheRead: u.Cost.CacheRead, CacheWrite: u.Cost.CacheWrite, Total: u.Cost.Total}}
	}
	priceSource := func(provider, model string) float64 { return prices[provider+"/"+model] }
	misses, differences, counted := 0, 0, 0
	for si, entries := range sessions {
		goEntries := make([]cacheStatsEntry, len(entries))
		for i, e := range entries {
			switch e.Type {
			case "message":
				usage := toUsage(e.Message.Usage)
				goEntries[i] = cacheStatsEntry{kind: "message", message: &agent.AssistantMessage{Role: "assistant", Provider: e.Message.Provider, ModelID: e.Message.Model, Timestamp: e.Message.Timestamp, Usage: &usage}}
			case "usage":
				usage := toUsage(*e.Usage)
				entry := &UsageEntry{Kind: e.Kind, Provider: e.Provider, Model: e.Model, Usage: usage}
				entry.Timestamp = e.Timestamp
				goEntries[i] = cacheStatsEntry{kind: "usage", usage: entry}
			default:
				goEntries[i] = cacheStatsEntry{kind: e.Type}
			}
		}
		got := collectCacheMisses(goEntries, priceSource)
		for i, e := range entries {
			want := expected[si][i]
			var miss *cacheMiss
			if e.Type == "message" {
				miss = got[goEntries[i].message]
			}
			switch {
			case want == nil && miss == nil:
			case want == nil || miss == nil:
				differences++
				t.Errorf("session %d entry %d: PiG miss %+v, Pi %+v", si, i, miss, want)
			default:
				counted++
				if miss.missedTokens != want.MissedTokens || miss.idleMillis != want.IdleMs || miss.modelChanged != want.ModelChanged ||
					math.Abs(miss.missedCost-want.MissedCost) > 1e-9*math.Max(1, math.Abs(want.MissedCost)) {
					differences++
					t.Errorf("session %d entry %d: PiG %+v, Pi %+v", si, i, *miss, *want)
				}
			}
			if want != nil {
				misses++
			}
			if differences > 6 {
				t.FailNow()
			}
		}
	}
	if misses < 200 {
		t.Fatalf("only %d counted misses over %d sessions: the generator no longer exercises the miss path", misses, len(sessions))
	}
}
