package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestCachePriceBindingWithoutMissesDoesNotReparseHistory(t *testing.T) {
	session := NewSession("prices", "/project")
	for range 1000 {
		if _, err := session.AppendMessage(mkUserMsg("history")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := session.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Provider: "provider", ModelID: "model", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "cached"}}, Usage: &ai.Usage{Input: 4096, CacheRead: 1024}, StopReason: ai.StopReasonStop}}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	price := func(string, string) float64 { calls++; return .25 }
	allocations := testing.AllocsPerRun(20, func() { session.SetCacheReadPriceSource(price) })
	if allocations != 0 {
		t.Fatalf("binding prices without historical misses allocates %.0f objects", allocations)
	}
	if calls != 0 {
		t.Fatal("price callback ran without a cache miss")
	}
	if _, err := session.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Provider: "provider", ModelID: "model", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "miss"}}, Usage: &ai.Usage{Input: 5120, Cost: ai.UsageCost{Input: 1}}, StopReason: ai.StopReasonStop}}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || session.Accounting().CacheWaste.MissCount != 1 {
		t.Fatalf("new price source or previous-request state lost: calls=%d stats=%+v", calls, session.Accounting().CacheWaste)
	}
}
