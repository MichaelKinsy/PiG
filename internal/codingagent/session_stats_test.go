package codingagent

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestLoadedSessionStatsIncludeAllEntriesAndUsage(t *testing.T) {
	path := t.TempDir() + "/session.jsonl"
	session := NewSession("loaded-stats", t.TempDir())
	session.SetPath(path)
	if _, err := session.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role: "assistant", Provider: "provider", ModelID: "model",
		Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "call", Name: "read"}},
		Usage:   &ai.Usage{Input: 10, Output: 4, CacheRead: 20, CacheWrite: 2, Cost: ai.UsageCost{Total: 0.25}},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "call", Usage: &ai.Usage{Input: 1, Output: 1, Cost: ai.UsageCost{Total: 0.05}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendCompaction("summary", *session.LeafID(), 1, nil, false, &ai.Usage{Input: 3, Output: 1, Cost: ai.UsageCost{Total: 0.1}}); err != nil {
		t.Fatal(err)
	}

	loaded, err := loadSessionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stats := loaded.Accounting()
	if stats.TotalMessages != 3 || stats.UserMessages != 1 || stats.AssistantMessages != 1 || stats.ToolResults != 1 || stats.ToolCalls != 1 {
		t.Fatalf("message stats = %+v", stats)
	}
	if stats.Tokens.Input != 14 || stats.Tokens.Output != 6 || stats.Tokens.CacheRead != 20 || stats.Tokens.CacheWrite != 2 || stats.Tokens.Total != 42 || stats.Tokens.Cost != 0.4 {
		t.Fatalf("token stats = %+v", stats.Tokens)
	}
}

func TestSessionAccountingAcceptsStringMessageContent(t *testing.T) {
	session := NewSession("string-content", "/tmp")
	raw := json.RawMessage(`{"type":"message","id":"legacy-user","parentId":null,"timestamp":"2025-01-01T00:00:00Z","message":{"role":"user","content":"hello","timestamp":1}}`)
	if err := session.AppendEntry(NewSessionEntry(raw, SessionEntryBase{Type: "message", ID: "legacy-user", Timestamp: "2025-01-01T00:00:00Z"})); err != nil {
		t.Fatal(err)
	}
	stats := session.Accounting()
	if stats.TotalMessages != 1 || stats.UserMessages != 1 {
		t.Fatalf("message stats = %+v", stats)
	}
}

func TestSessionStatsCacheWasteResetsAtSummary(t *testing.T) {
	session := NewSession("cache-stats", "/tmp")
	appendAssistant := func(id string, usage *ai.Usage) {
		t.Helper()
		entry := MessageEntry{
			SessionEntryBase: SessionEntryBase{Type: "message", ID: id, ParentID: session.LeafID(), Timestamp: "2025-01-01T00:00:00Z"},
			Message: agent.AgentMessage{Assistant: &agent.AssistantMessage{
				Role: "assistant", Provider: "custom", ModelID: "model", Timestamp: 1, Usage: usage,
			}},
		}
		if err := session.AppendEntry(entry); err != nil {
			t.Fatal(err)
		}
	}
	appendAssistant("cached", &ai.Usage{Input: 100, CacheRead: 4_900})
	appendAssistant("miss", &ai.Usage{Input: 5_000, Cost: ai.UsageCost{Input: 0.05, Total: 0.05}})
	stats := session.Accounting()
	if stats.CacheWaste.MissedTokens != 5_000 || stats.CacheWaste.MissCount != 1 || stats.CacheWaste.MissedCost != 0.05 {
		t.Fatalf("cache waste = %+v", stats.CacheWaste)
	}
	if _, err := session.AppendCompaction("summary", "miss", 1, nil, false, nil); err != nil {
		t.Fatal(err)
	}
	appendAssistant("after-summary", &ai.Usage{Input: 5_000, Cost: ai.UsageCost{Input: 0.05, Total: 0.05}})
	stats = session.Accounting()
	if stats.CacheWaste.MissedTokens != 5_000 || stats.CacheWaste.MissCount != 1 {
		t.Fatalf("summary did not reset cache comparison: %+v", stats.CacheWaste)
	}
}

func BenchmarkSessionStatsSnapshot(b *testing.B) {
	for _, entries := range []int{100, 10_000} {
		b.Run(fmt.Sprintf("entries=%d", entries), func(b *testing.B) {
			session := NewSession("benchmark", "/tmp")
			for i := range entries {
				entry := MessageEntry{
					SessionEntryBase: SessionEntryBase{Type: "message", ID: fmt.Sprintf("u%d", i), Timestamp: "2025-01-01T00:00:00Z"},
					Message:          agent.AgentMessage{User: &agent.UserMessage{Role: "user"}},
				}
				if err := session.AppendEntry(entry); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_ = session.Accounting()
			}
		})
	}
}

func BenchmarkSessionCommandRecorded(b *testing.B) {
	path := os.Getenv("PIG_BENCH_SESSION_FILE")
	if path == "" {
		b.Skip("set PIG_BENCH_SESSION_FILE to a private Session JSONL path")
	}
	session, err := loadSessionFile(path)
	if err != nil {
		b.Fatal(err)
	}
	sc := &SlashContext{CurrentSession: func() *Session { return session }, AppendText: func(string) {}}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := sessionHandler(sc); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadSessionStatsRecorded(b *testing.B) {
	path := os.Getenv("PIG_BENCH_SESSION_FILE")
	if path == "" {
		b.Skip("set PIG_BENCH_SESSION_FILE to a private Session JSONL path")
	}
	info, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(info.Size())
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := loadSessionFile(path); err != nil {
			b.Fatal(err)
		}
	}
}
